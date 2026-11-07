package posthorse

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// Port of src/note-snapshots.ts.

// sameIdentity: note-snapshots.ts:41.
func sameIdentity(left, right noteIdentity) bool {
	return left == right
}

// digestBuffers pools the 64 KiB read buffers fs.createReadStream uses by default.
var digestBuffers = sync.Pool{New: func() any { b := make([]byte, 64*1024); return &b }}

// ctxErr is `signal?.throwIfAborted()`; a nil context never aborts (files.go throwIfAborted).
func ctxErr(ctx context.Context) error { return throwIfAborted(ctx) }

// fileDigest: note-snapshots.ts:50.
func fileDigest(ctx context.Context, path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", nodeFsError(err, "open", path)
	}
	defer file.Close()
	hash := sha256.New()
	bufp := digestBuffers.Get().(*[]byte)
	defer digestBuffers.Put(bufp)
	buf := *bufp
	for {
		n, err := file.Read(buf)
		if n > 0 {
			if err := ctxErr(ctx); err != nil {
				return "", err
			}
			hash.Write(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			// A failed stream read carries no path in Node (`EISDIR: illegal operation on a directory, read`).
			return "", nodeFsError(err, "read", "")
		}
	}
	var sum [sha256.Size]byte
	return hex.EncodeToString(hash.Sum(sum[:0])), nil
}

// readManifest: note-snapshots.ts:67. nil is `undefined`; a syntax error or a missing manifest is
// not an error.
func readManifest(directory string) (*noteBinding, error) {
	file, err := os.Open(filepath.Join(filepath.Dir(directory), "manifest.json"))
	if err != nil {
		if noteMissing(err) {
			return nil, nil
		}
		return nil, nodeFsError(err, "open", filepath.Join(filepath.Dir(directory), "manifest.json"))
	}
	data, err := io.ReadAll(file)
	file.Close()
	if err != nil {
		return nil, nodeFsError(err, "read", "")
	}
	// readFile(..., "utf8") decodes like Buffer.toString (WHATWG replacement of invalid bytes) and
	// JSON.parse keeps lone surrogate escapes and reads 1e400 as Infinity: the SDK's decoder does the same.
	var raw any
	if sdkjson.Unmarshal([]byte(jsFromBytes(data)), &raw) != nil {
		return nil, nil
	}
	binding := parseBinding(raw)
	if binding != nil && binding.kind == "fork" && scopeDirectory(binding) == directory {
		return binding, nil
	}
	return nil, nil
}

// snapshotNote is SnapshotNote (note-snapshots.ts:86).
type snapshotNote struct {
	path    string
	source  noteAddress
	lineage noteAddress
	anchors []string
}

// noteIOBatch is NOTE_IO_BATCH (note-snapshots.ts:92).
const noteIOBatch = 16

// mapBatch: note-snapshots.ts:93. At most noteIOBatch calls run at once; every call of a batch
// settles before the first failure (in item order) is returned, and no later batch starts.
func mapBatch[Input, Output any](items []Input, fn func(Input) (Output, error)) ([]Output, error) {
	return mapBatchPrepared(items,
		func() (struct{}, error) { return struct{}{}, nil },
		func(_ struct{}, item Input) (Output, error) { return fn(item) })
}

// mapBatchPrepared is mapBatch where prepare runs once at the start of each batch, before its calls
// start, and its result is shared by them: the synchronous prefixes of a batch's calls run together
// in the original, so per-call work that only reads shared state is done once per batch. A failing
// prepare fails the batch.
func mapBatchPrepared[Input, Output, Shared any](items []Input, prepare func() (Shared, error), fn func(Shared, Input) (Output, error)) ([]Output, error) {
	results := make([]Output, 0, len(items))
	for start := 0; start < len(items); start += noteIOBatch {
		batch := items[start:min(start+noteIOBatch, len(items))]
		shared, err := prepare()
		if err != nil {
			return nil, err
		}
		outputs := make([]Output, len(batch))
		errs := make([]error, len(batch))
		if len(batch) == 1 {
			outputs[0], errs[0] = fn(shared, batch[0])
		} else {
			var wg sync.WaitGroup
			wg.Add(len(batch))
			for i := range batch {
				go func() {
					defer wg.Done()
					outputs[i], errs[i] = fn(shared, batch[i])
				}()
			}
			wg.Wait()
		}
		for _, err := range errs {
			if err != nil {
				return nil, err
			}
		}
		results = append(results, outputs...)
	}
	return results, nil
}

// copyResult is CopyResult (note-snapshots.ts:111).
type copyResult struct {
	verified bool
	seed     noteSeed
	err      error // when !verified
}

// noteAfterCopy is a test seam: it runs right after the bytes are copied and before the source is
// verified again (the original's test replaces fs.promises.copyFile to change the source there).
var noteAfterCopy func(source, destination string)

// copyFileExcl is fs.copyFile(source, destination, COPYFILE_EXCL): the destination is created with
// the source's mode and removed again when the copy fails.
func copyFileExcl(source, destination string, mode fs.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm())
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Chmod(mode)
	}
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(destination)
	}
	return err
}

// copyNote: note-snapshots.ts:114. The error result is a failure before the copy exists; a failure
// after it is an unverified copy.
func copyNote(note snapshotNote, destination string) (copyResult, error) {
	source, err := notePath(note.source, note.source.path)
	if err != nil {
		return copyResult{}, err
	}
	// The original starts the stat and the mkdir together; both always run.
	before, statErr := os.Stat(source)
	mkdirErr := mkdirNode(filepath.Dir(destination))
	if statErr != nil {
		return copyResult{}, nodeFsError(statErr, "stat", source)
	}
	if mkdirErr != nil {
		return copyResult{}, nodeFsError(mkdirErr, "mkdir", filepath.Dir(destination))
	}
	if err := copyFileExcl(source, destination, before.Mode()); err != nil {
		return copyResult{}, nodeFsErrorDest(err, "copyfile", source, destination)
	}
	if noteAfterCopy != nil {
		noteAfterCopy(source, destination)
	}
	seed, err := verifyCopy(note, source, destination, before)
	if err != nil {
		return copyResult{err: err}, nil
	}
	return copyResult{verified: true, seed: seed}, nil
}

func verifyCopy(note snapshotNote, source, destination string, before fs.FileInfo) (noteSeed, error) {
	after, err := os.Stat(source)
	if err != nil {
		return noteSeed{}, nodeFsError(err, "stat", source)
	}
	if !sameIdentity(fileIdentity(source, before), fileIdentity(source, after)) {
		return noteSeed{}, errors.New("Source note changed during snapshot: " + source + ". Retry initialization.")
	}
	if err := os.Chtimes(destination, accessTime(before), before.ModTime()); err != nil {
		return noteSeed{}, nodeFsError(err, "utime", destination)
	}
	digest, err := fileDigest(nil, destination)
	if err != nil {
		return noteSeed{}, err
	}
	info, err := os.Stat(destination)
	if err != nil {
		return noteSeed{}, nodeFsError(err, "stat", destination)
	}
	return noteSeed{
		path: note.path, source: note.source, lineage: note.lineage, anchors: note.anchors,
		digest: digest, identity: fileIdentity(destination, info),
	}, nil
}

// snapshotPreparation is SnapshotPreparation (note-snapshots.ts:138).
type snapshotPreparation struct {
	kind      string // "existing" | "empty" | "staging"
	binding   *noteBinding
	stage     string
	container string
}

// prepareSnapshot: note-snapshots.ts:142.
func prepareSnapshot(binding *noteBinding, notes []snapshotNote) (snapshotPreparation, error) {
	directory := scopeDirectory(binding)
	existing, err := readManifest(directory)
	if err != nil {
		return snapshotPreparation{}, err
	}
	if existing != nil && sameOwner(existing.owner, binding.owner) {
		return snapshotPreparation{kind: "existing", binding: existing}, nil
	}
	container := filepath.Dir(directory)
	if _, err := os.Lstat(container); err == nil {
		return snapshotPreparation{}, errors.New("Incomplete notes snapshot: preserving unexpected data at " + container + ".")
	} else if !errors.Is(err, fs.ErrNotExist) {
		return snapshotPreparation{}, nodeFsError(err, "lstat", container)
	}
	if len(notes) == 0 {
		return snapshotPreparation{kind: "empty"}, nil
	}
	if err := mkdirNode(filepath.Dir(container)); err != nil {
		return snapshotPreparation{}, err
	}
	stage, err := os.MkdirTemp(filepath.Dir(container), ".staging-")
	if err != nil {
		// Node names the template with its random characters filled in, as Go's error does.
		failed := filepath.Join(filepath.Dir(container), ".staging-")
		var pathErr *fs.PathError
		if errors.As(err, &pathErr) {
			failed = pathErr.Path
		}
		return snapshotPreparation{}, nodeFsError(err, "mkdtemp", failed)
	}
	return snapshotPreparation{kind: "staging", stage: stage, container: container}, nil
}

// writeNew is writeFile(path, text, { flag: "wx" }).
func writeNew(path string, text []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
	if err != nil {
		return nodeFsError(err, "open", path)
	}
	_, err = file.Write(text)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return nodeFsError(err, "write", "")
}

// snapshot: note-snapshots.ts:170. Publication is the ownership boundary. An existing unrelated
// directory is never overwritten.
func snapshot(binding *noteBinding, notes []snapshotNote) (*noteBinding, error) {
	preparation, err := prepareSnapshot(binding, notes)
	if err != nil {
		return nil, err
	}
	if preparation.kind != "staging" {
		if preparation.kind == "existing" {
			return preparation.binding, nil
		}
		return binding, nil
	}
	stage, container := preparation.stage, preparation.container
	defer os.RemoveAll(stage)
	complete, err := stageSnapshot(binding, notes, stage, container)
	if err != nil {
		return nil, noteErrorf(err, "Incomplete notes snapshot for "+scopeDirectory(binding)+": "+err.Error())
	}
	return complete, nil
}

func stageSnapshot(binding *noteBinding, notes []snapshotNote, stage, container string) (*noteBinding, error) {
	// Keep each file's identity and digest paired with the bytes actually copied.
	seeds, err := mapBatch(notes, func(note snapshotNote) (noteSeed, error) {
		destination, err := notePath(noteScope{root: stage, scope: "notes"}, note.path)
		if err != nil {
			return noteSeed{}, err
		}
		result, err := copyNote(note, destination)
		if err != nil {
			return noteSeed{}, err
		}
		if !result.verified {
			return noteSeed{}, result.err
		}
		return result.seed, nil
	})
	if err != nil {
		return nil, err
	}
	complete := *binding
	complete.seeds = seeds
	if err := writeNew(filepath.Join(stage, "manifest.json"), complete.appendJSON(nil)); err != nil {
		return nil, err
	}
	if err := os.Rename(stage, container); err != nil {
		return nil, nodeFsErrorDest(err, "rename", stage, container)
	}
	return &complete, nil
}

// adoptNote: note-snapshots.ts:205. Called inside the destination's native mutation queue, never
// queues that path recursively.
func adoptNote(binding *noteBinding, note snapshotNote) (copyResult, error) {
	destination, err := notePath(binding, note.path)
	if err != nil {
		return copyResult{}, err
	}
	return copyNote(note, destination)
}

// ensureManifest: note-snapshots.ts:209. Empty initialized scopes publish their manifest when the
// first note is created.
func ensureManifest(binding *noteBinding) error {
	if binding.kind != "fork" {
		return nil
	}
	path := filepath.Join(filepath.Dir(scopeDirectory(binding)), "manifest.json")
	if err := mkdirNode(filepath.Dir(path)); err != nil {
		return err
	}
	err := writeNew(path, binding.appendJSON(nil))
	if err == nil {
		return nil
	}
	if !errors.Is(err, fs.ErrExist) {
		return err
	}
	existing, readErr := readManifest(scopeDirectory(binding))
	if readErr != nil {
		return readErr
	}
	if existing == nil || !sameOwner(existing.owner, binding.owner) {
		return noteErrorf(err, "Preserving unexpected notes manifest at "+path+".")
	}
	return nil
}

// saveAdoption: note-snapshots.ts:229.
func saveAdoption(binding *noteBinding, seed noteSeed) error {
	if binding.kind != "fork" {
		return nil
	}
	path := filepath.Join(filepath.Dir(scopeDirectory(binding)), "manifest.json")
	_, err := sdk.WithFileMutationQueue(path, func() (struct{}, error) {
		if err := ensureManifest(binding); err != nil {
			return struct{}{}, err
		}
		previous, err := readManifest(scopeDirectory(binding))
		if err != nil {
			return struct{}{}, err
		}
		if previous == nil {
			previous = binding
		}
		seeds := make([]noteSeed, 0, len(previous.seeds)+1)
		for _, item := range previous.seeds {
			if item.path != seed.path {
				seeds = append(seeds, item)
			}
		}
		next := *previous
		next.seeds = append(seeds, seed)
		return struct{}{}, publishFile(nil, path, string(next.appendJSON(nil)))
	})
	return err
}

// mkdirNode is fs.promises.mkdir(path, { recursive: true }): Go's MkdirAll reports ENOTDIR for a path
// that exists as a file where Node says EEXIST, and names the innermost ancestor where Node names the
// path it was given.
func mkdirNode(path string) error {
	err := os.MkdirAll(path, 0o777)
	if err == nil {
		return nil
	}
	if errors.Is(err, syscall.ENOTDIR) {
		if info, statErr := os.Stat(path); statErr == nil && !info.IsDir() {
			err = &fs.PathError{Op: "mkdir", Path: path, Err: syscall.EEXIST}
		}
	}
	return nodeFsError(err, "mkdir", path)
}
