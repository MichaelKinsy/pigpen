package posthorse

// Port of src/files.ts: link-aware file targets, same-directory publication, bounded LF streaming and
// the notes directory (with the migration of notes out of Pi package checkouts).
//
// Mapping notes
//   - `AbortSignal` is a context.Context (nil means no signal); signalOf adapts the handler's
//     sdk.Context. An aborted operation returns an *abortError.
//   - Node's fs errors carry "CODE: description, syscall 'path'" messages and codes; nodeFsError
//     rebuilds them from the Go errors so the user-visible texts match.
//   - A Node Buffer is a fresh []byte per read, kept alive by whoever slices it (lfChunks).

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"iter"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// signalOf adapts the handler's request cancellation (ctx.Done/Err) to an AbortSignal.
func signalOf(c sdk.Context) context.Context { return handlerSignal{c} }

type handlerSignal struct{ c sdk.Context }

func (handlerSignal) Deadline() (time.Time, bool) { return time.Time{}, false }
func (s handlerSignal) Done() <-chan struct{}     { return s.c.Done() }
func (s handlerSignal) Err() error                { return s.c.Err() }
func (handlerSignal) Value(any) any               { return nil }

// abortError is what an aborted signal throws. Pi aborts a run with AbortController.abort() and no
// reason, so `signal.throwIfAborted()` throws the DOMException "This operation was aborted" (throwIfAborted),
// while Node's own fs operations given the signal (readFile, createReadStream, writeFile) throw an
// AbortError "The operation was aborted" (fsAborted).
type abortError struct {
	cause error
	text  string
}

func (e *abortError) Error() string { return e.text }
func (e *abortError) Unwrap() error { return e.cause }

// fsAborted is the abort an fs call that was given the signal reports; unlike throwIfAborted it never
// returns the signal's reason itself (Node wraps it as the AbortError's cause).
func fsAborted(ctx context.Context) error {
	if ctx == nil || ctx.Err() == nil {
		return nil
	}
	return &abortError{cause: ctx.Err(), text: "The operation was aborted"}
}

// throwIfAborted is `signal?.throwIfAborted()` (nil signal never aborts): a plain cancellation
// throws the AbortError, a custom reason (context.WithCancelCause) is thrown as it is, so
// `error === signal.reason` holds.
func throwIfAborted(ctx context.Context) error {
	if ctx == nil || ctx.Err() == nil {
		return nil
	}
	cause := context.Cause(ctx)
	if cause == nil || errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return &abortError{cause: ctx.Err(), text: "This operation was aborted"}
	}
	return cause
}

// fsError is a Node-style file system error: Code is "ENOENT", Syscall "open", Path the subject.
type fsError struct {
	Code    string
	Syscall string
	Path    string
	Dest    string
	// Message replaces the generated "CODE: description, syscall 'path'" text.
	Message string
	Err     error
}

func (e *fsError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	text := e.Code + ": " + uvMessages[e.Code] + ", " + e.Syscall
	if e.Path != "" {
		text += " '" + e.Path + "'"
		if e.Dest != "" {
			text += " -> '" + e.Dest + "'"
		}
	}
	return text
}
func (e *fsError) Unwrap() error { return e.Err }

// errnoName is libuv's error name for an errno (errno_*.go hold the per-OS tables); an errno libuv
// does not map is "UNKNOWN", as Node's uvException falls back to ["UNKNOWN", "unknown error"].
func errnoName(errno syscall.Errno) string {
	if name, ok := platformErrnoNames[errno]; ok {
		return name
	}
	return "UNKNOWN"
}

// nodeFsError turns an os error into the Node error of the same call; errors that are not file
// system errors (aborts, our own) pass through unchanged.
func nodeFsError(err error, syscallName, path string) error {
	return nodeFsErrorDest(err, syscallName, path, "")
}

// nodeFsErrorDest is nodeFsError for two-path calls (rename, symlink, copyfile).
func nodeFsErrorDest(err error, syscallName, path, dest string) error {
	if err == nil {
		return nil
	}
	var existing *fsError
	var abort *abortError
	if errors.As(err, &existing) || errors.As(err, &abort) {
		return err
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		// An errno libuv has no name for is Node's UNKNOWN (uvUnmappedError).
		return &fsError{Code: errnoName(errno), Syscall: syscallName, Path: path, Dest: dest, Err: err}
	}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return &fsError{Code: "ENOENT", Syscall: syscallName, Path: path, Dest: dest, Err: err}
	case errors.Is(err, fs.ErrPermission):
		return &fsError{Code: "EACCES", Syscall: syscallName, Path: path, Dest: dest, Err: err}
	case errors.Is(err, fs.ErrExist):
		return &fsError{Code: "EEXIST", Syscall: syscallName, Path: path, Dest: dest, Err: err}
	}
	return err
}

// isMissing is `error.code === "ENOENT"` (files.ts:31).
func isMissing(err error) bool { return errors.Is(err, fs.ErrNotExist) }

// lstatOrMissing is `lstatOrMissing` (files.ts:29-38): nil info when the entry does not exist.
func lstatOrMissing(path string) (fs.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if isMissing(err) {
			return nil, nil
		}
		return nil, nodeFsError(err, "lstat", path)
	}
	return info, nil
}

// lstatSyncMissing is `lstatSync(path, { throwIfNoEntry: false })`: Node also treats ENOTDIR as no entry.
func lstatSyncMissing(path string) (fs.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if isMissing(err) || errors.Is(err, syscall.ENOTDIR) {
			return nil, nil
		}
		return nil, nodeFsError(err, "lstat", path)
	}
	return info, nil
}

// nodeDirname is path.dirname (POSIX semantics, no normalisation: ".." segments survive).
func nodeDirname(p string) string {
	if os.PathSeparator == '\\' {
		return filepath.Dir(p)
	}
	if p == "" {
		return "."
	}
	hasRoot := p[0] == '/'
	end := -1
	matchedSlash := true
	for i := len(p) - 1; i >= 1; i-- {
		if p[i] == '/' {
			if !matchedSlash {
				end = i
				break
			}
		} else {
			matchedSlash = false
		}
	}
	if end == -1 {
		if hasRoot {
			return "/"
		}
		return "."
	}
	if hasRoot && end == 1 {
		return "//"
	}
	return p[:end]
}

// nodeBasename is path.basename (POSIX semantics).
func nodeBasename(p string) string {
	if os.PathSeparator == '\\' {
		if p == "" {
			return ""
		}
		base := filepath.Base(p)
		if base == "." || base == string(filepath.Separator) {
			return ""
		}
		return base
	}
	end := len(p)
	for end > 0 && p[end-1] == '/' {
		end--
	}
	p = p[:end]
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// resolveFileTarget is `resolveFileTarget` (files.ts:40-78): every link is resolved before
// normalisation, preserving path traversal constraints.
func resolveFileTarget(absolutePath string) (string, error) {
	target := absolutePath
	links := map[string]struct{}{}
	for {
		if strings.HasSuffix(target, string(filepath.Separator)) || strings.HasSuffix(target, "/") {
			// Trailing separators require an existing directory, never a new regular file (files.ts:44-47).
			if _, err := os.Stat(target); err != nil {
				return "", nodeFsError(err, "stat", target)
			}
			resolved, err := filepath.EvalSymlinks(target)
			if err != nil {
				return "", nodeFsError(err, "realpath", target)
			}
			return resolved, nil
		}
		parentInput := nodeDirname(target)
		// stat enforces regular-file/.. traversal that macOS realpath alone accepts (files.ts:51).
		info, err := os.Stat(parentInput)
		if err != nil {
			return "", nodeFsError(err, "stat", parentInput)
		}
		if !info.IsDir() {
			return "", &fsError{Code: "ENOTDIR", Message: "Not a directory: " + parentInput}
		}
		parent, err := filepath.EvalSymlinks(parentInput)
		if err != nil {
			return "", nodeFsError(err, "realpath", parentInput)
		}
		target = filepath.Join(parent, nodeBasename(target))
		linkInfo, err := lstatOrMissing(target)
		if err != nil {
			return "", err
		}
		if linkInfo == nil {
			return target, nil
		}
		if linkInfo.Mode()&fs.ModeSymlink == 0 {
			resolved, err := filepath.EvalSymlinks(target)
			if err != nil {
				return "", nodeFsError(err, "realpath", target)
			}
			return resolved, nil
		}
		if _, seen := links[target]; seen {
			return "", &fsError{Code: "ELOOP", Message: "Symlink cycle: " + absolutePath}
		}
		links[target] = struct{}{}
		// Preserve .. until the preceding directory symlink has been followed (files.ts:74-75).
		link, err := os.Readlink(target)
		if err != nil {
			return "", nodeFsError(err, "readlink", target)
		}
		if filepath.IsAbs(link) {
			target = link
		} else {
			target = parent + string(filepath.Separator) + link
		}
	}
}

// existingNote is `existingNote` (files.ts:80-92).
func existingNote(target string) (fs.FileInfo, error) {
	previous, err := lstatOrMissing(target)
	if err != nil {
		return nil, err
	}
	if previous != nil && !previous.Mode().IsRegular() {
		code := "EINVAL"
		if previous.IsDir() {
			code = "EISDIR"
		}
		return nil, &fsError{Code: code, Path: target, Message: "Cannot publish to a non-regular file: " + target}
	}
	if previous != nil {
		// access(target, W_OK): opening for write without truncation reports the same refusals.
		file, err := os.OpenFile(target, os.O_WRONLY, 0)
		if err != nil {
			return nil, nodeFsError(err, "access", target)
		}
		_ = file.Close()
	}
	return previous, nil
}

// randomUUID is `crypto.randomUUID()`.
func randomUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	var out [36]byte
	hex.Encode(out[0:8], b[0:4])
	out[8] = '-'
	hex.Encode(out[9:13], b[4:6])
	out[13] = '-'
	hex.Encode(out[14:18], b[6:8])
	out[18] = '-'
	hex.Encode(out[19:23], b[8:10])
	out[23] = '-'
	hex.Encode(out[24:], b[10:])
	return string(out[:]), nil
}

// writeChunk bounds how long a write runs between abort checks, as Node's writeFile does.
const writeChunk = 64 * 1024

// publishFile is `publishFile` (files.ts:93-127): same-directory replacement; pre-rename failures
// preserve the old note and its metadata.
func publishFile(ctx context.Context, path, content string) (err error) {
	if err := throwIfAborted(ctx); err != nil {
		return err
	}
	target, err := resolveFileTarget(path)
	if err != nil {
		return err
	}
	previous, err := existingNote(target)
	if err != nil {
		return err
	}
	id, err := randomUUID()
	if err != nil {
		return err
	}
	temporary := filepath.Join(nodeDirname(target), ".posthorse-"+id+".tmp")
	mode := fs.FileMode(0o666)
	if previous != nil {
		mode = 0o600
	}
	file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return nodeFsError(err, "open", temporary)
	}
	defer func() {
		if err == nil {
			return
		}
		// Preserve the publication failure if closing or cleanup also fails (files.ts:118-123).
		_ = file.Close()
		_ = os.Remove(temporary)
	}()
	data := jsUTF8Bytes(content)
	for len(data) > 0 {
		if err := fsAborted(ctx); err != nil {
			return err
		}
		n := min(len(data), writeChunk)
		if _, err := file.Write(data[:n]); err != nil {
			return nodeFsError(err, "write", "")
		}
		data = data[n:]
	}
	if previous != nil {
		if err := matchOwnerAndMode(file, previous); err != nil {
			return err // already a Node fd error: fstat, fchown or fchmod
		}
	}
	if err := file.Close(); err != nil {
		return nodeFsError(err, "close", "")
	}
	if err := throwIfAborted(ctx); err != nil {
		return err
	}
	if err := os.Rename(temporary, target); err != nil {
		return nodeFsErrorDest(err, "rename", temporary, target)
	}
	return nil
}

// lfChunk is one LF-delimited segment of a file; end also closes an unterminated final line at EOF.
type lfChunk struct {
	bytes []byte
	end   bool
}

// lfChunkSize is the TS default highWaterMark (files.ts:130).
const lfChunkSize = 64 * 1024

// lfChunks is `lfChunks` (files.ts:128-157): bounded LF-delimited segments. Each read gets its own
// buffer, so the yielded slices stay valid (as Node Buffers do). An error is yielded once.
func lfChunks(ctx context.Context, file string, highWaterMark int) iter.Seq2[lfChunk, error] {
	return func(yield func(lfChunk, error) bool) {
		f, err := os.Open(file)
		if err != nil {
			yield(lfChunk{}, nodeFsError(err, "open", file))
			return
		}
		defer f.Close()
		lfChunksFile(ctx, f, file, highWaterMark, false)(yield)
	}
}

// lfChunksFile streams an open file from its current position (the `handle` form of lfChunks).
// With reuse one buffer serves every read and a yielded slice is valid only until the next
// iteration step; without it each read gets a fresh buffer.
func lfChunksFile(ctx context.Context, f *os.File, name string, highWaterMark int, reuse bool) iter.Seq2[lfChunk, error] {
	if highWaterMark <= 0 {
		highWaterMark = lfChunkSize
	}
	return func(yield func(lfChunk, error) bool) {
		var buf []byte
		openLine := false
		for {
			if err := fsAborted(ctx); err != nil {
				yield(lfChunk{}, err)
				return
			}
			if buf == nil || !reuse {
				buf = make([]byte, highWaterMark)
			}
			n, err := f.Read(buf)
			if n > 0 {
				chunk := buf[:n]
				start := 0
				for {
					i := indexLF(chunk[start:])
					if i < 0 {
						break
					}
					if !yield(lfChunk{bytes: chunk[start : start+i : start+i], end: true}, nil) {
						return
					}
					openLine = false
					start += i + 1
				}
				if start < len(chunk) {
					if !yield(lfChunk{bytes: chunk[start:], end: false}, nil) {
						return
					}
					openLine = true
				}
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				yield(lfChunk{}, nodeFsError(err, "read", ""))
				return
			}
		}
		if openLine {
			yield(lfChunk{end: true}, nil)
		}
	}
}

func indexLF(b []byte) int { return bytes.IndexByte(b, '\n') }

// lfLines is `lfLines` (files.ts:158-171): raw LF-delimited lines, each owned by the caller. UTF-8
// never encodes LF inside another character.
func lfLines(ctx context.Context, file string, highWaterMark int) iter.Seq2[[]byte, error] {
	return func(yield func([]byte, error) bool) {
		var pending [][]byte
		for chunk, err := range lfChunks(ctx, file, highWaterMark) {
			if err != nil {
				yield(nil, err)
				return
			}
			pending = append(pending, chunk.bytes)
			if !chunk.end {
				continue
			}
			var line []byte
			if len(pending) == 1 {
				line = pending[0]
			} else {
				size := 0
				for _, part := range pending {
					size += len(part)
				}
				line = make([]byte, 0, size)
				for _, part := range pending {
					line = append(line, part...)
				}
			}
			pending = pending[:0]
			if !yield(line, nil) {
				return
			}
		}
	}
}

// binarySniff is the span Git's heuristic inspects: a NUL in the first 8,000 bytes marks binary content.
const binarySniff = 8_000

// isBinaryFile is `isBinaryFile` (files.ts:172-180).
func isBinaryFile(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, nodeFsError(err, "open", path)
	}
	defer f.Close()
	var buf [binarySniff]byte
	n, err := f.ReadAt(buf[:], 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return false, nodeFsError(err, "read", "")
	}
	return indexNUL(buf[:n]) >= 0, nil
}

func indexNUL(b []byte) int { return bytes.IndexByte(b, 0) }

// readText is `readText` (files.ts:181-185): a whole file as UTF-8 text; ok is false (TS undefined)
// when isBinaryFile would call it binary.
func readText(ctx context.Context, path string) (text string, ok bool, err error) {
	if err := fsAborted(ctx); err != nil {
		return "", false, err
	}
	f, err := os.Open(path)
	if err != nil {
		return "", false, nodeFsError(err, "open", path)
	}
	defer f.Close()
	size := 0
	if info, err := f.Stat(); err == nil && info.Size() < 1<<31 {
		size = int(info.Size())
	}
	data := make([]byte, 0, size+1)
	for {
		if err := fsAborted(ctx); err != nil {
			return "", false, err
		}
		if len(data) == cap(data) {
			data = append(data, 0)[:len(data)]
		}
		n, err := f.Read(data[len(data):cap(data)])
		data = data[:len(data)+n]
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return "", false, nodeFsError(err, "read", "")
		}
	}
	if indexNUL(data[:min(len(data), binarySniff)]) >= 0 {
		return "", false, nil
	}
	return jsFromBytes(data), true, nil
}

// isLineTerminator reports the JavaScript LineTerminator characters (\n, \r, U+2028, U+2029).
func isLineTerminator(r rune) bool { return r == '\n' || r == '\r' || r == 0x2028 || r == 0x2029 }

// gitdirOf is `text.match(/^gitdir:\s*(.+?)\s*$/m)?.[1]` (files.ts:187): the multiline regex of
// JavaScript, whose `^`/`$` and `.` know four line terminators and whose `\s*` may cross lines.
func gitdirOf(text string) (string, bool) {
	runes := []rune(text)
	const prefix = "gitdir:"
	atLineEnd := func(pos int) bool { return pos == len(runes) || isLineTerminator(runes[pos]) }
	for p := 0; p < len(runes); p++ {
		if p > 0 && !isLineTerminator(runes[p-1]) {
			continue
		}
		if !strings.HasPrefix(string(runes[p:min(len(runes), p+len(prefix))]), prefix) {
			continue
		}
		i := p + len(prefix)
		j := i
		for j < len(runes) && jsWhitespace(runes[j]) {
			j++
		}
		// \s* backtracks from j down to i; (.+?) grows one non-terminator character at a time.
		for k := j; k >= i; k-- {
			for e := k + 1; e <= len(runes) && !isLineTerminator(runes[e-1]); e++ {
				for pos := e; ; pos++ {
					if atLineEnd(pos) {
						return string(runes[k:e]), true
					}
					if !jsWhitespace(runes[pos]) {
						break
					}
				}
			}
		}
	}
	return "", false
}

// existsSync is `existsSync` (follows links; false for dangling ones and for any error).
func existsSync(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// realpathSync is `realpathSync`.
func realpathSync(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", nodeFsError(err, "realpath", path)
	}
	return resolved, nil
}

// resolvePath is path.resolve for the absolute inputs of this module.
func resolvePath(base, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(base, p)
}

// gitRoot is `gitRoot` (files.ts:182-198).
func gitRoot(dir, marker string) (string, error) {
	target := marker
	info, err := os.Stat(marker)
	if err != nil {
		return "", nodeFsError(err, "stat", marker)
	}
	if !info.IsDir() {
		raw, err := os.ReadFile(marker)
		if err != nil {
			return "", nodeFsError(err, "open", marker)
		}
		gitdir, ok := gitdirOf(jsFromBytes(raw))
		if !ok || gitdir == "" {
			return dir, nil
		}
		target = resolvePath(dir, gitdir)
	}
	target, err = realpathSync(target)
	if err != nil {
		return "", err
	}
	commonFile := filepath.Join(target, "commondir")
	commonPath := target
	if existsSync(commonFile) {
		raw, err := os.ReadFile(commonFile)
		if err != nil {
			return "", nodeFsError(err, "open", commonFile)
		}
		commonPath = resolvePath(target, jsTrim(jsFromBytes(raw)))
	}
	common, err := realpathSync(commonPath)
	if err != nil {
		return "", err
	}
	if nodeBasename(common) == ".git" {
		return nodeDirname(common), nil
	}
	return common, nil
}

// notesRoot is `notesRoot` (files.ts:199-213): conventional repos share the primary checkout;
// separate Git directories own their shared root.
func notesRoot(cwd string) string {
	for dir := cwd; ; dir = nodeDirname(dir) {
		marker := filepath.Join(dir, ".git")
		if existsSync(marker) {
			root, err := gitRoot(dir, marker)
			if err != nil {
				return dir // Orphaned worktrees retain local notes.
			}
			return root
		}
		if nodeDirname(dir) == dir {
			return cwd
		}
	}
}

// packageRelativePath is `packageRelativePath` (files.ts:214-219); ok is false for undefined.
func packageRelativePath(installs, root string) (string, bool) {
	path, err := filepath.Rel(installs, root)
	if err != nil {
		return "", false
	}
	sep := string(filepath.Separator)
	// path.relative(x, x) is "" (excluded here); filepath.Rel spells it ".".
	if path != "" && path != "." && path != ".." && !strings.HasPrefix(path, ".."+sep) && !filepath.IsAbs(path) {
		return path, true
	}
	return "", false
}

// projectPackageNotes is `projectPackageNotes` (files.ts:220-240); ok is false for undefined.
func projectPackageNotes(root, cwd string) (moved string, ok bool, err error) {
	checkout := root
	for dir := cwd; nodeDirname(dir) != dir; dir = nodeDirname(dir) {
		// Retain the logical checkout path when a package container or source segment is linked.
		if existsSync(filepath.Join(dir, ".git")) {
			real, err := realpathSync(dir)
			if err != nil {
				return "", false, err
			}
			if real == root {
				checkout = dir
			}
		}
		if nodeBasename(dir) != "git" || nodeBasename(nodeDirname(dir)) != ".pi" {
			continue
		}
		if path, found := packageRelativePath(dir, checkout); found {
			// Flat full keys keep arbitrary Git URL prefix paths out of one another's note stores.
			sum := sha256.Sum256([]byte(strings.ReplaceAll(path, string(filepath.Separator), "/")))
			parent, err := realpathSync(nodeDirname(dir))
			if err != nil {
				return "", false, err
			}
			moved, ok = filepath.Join(parent, "package-notes", hex.EncodeToString(sum[:]), "notes"), true
		}
	}
	// The outermost package container also protects packages installed inside another package.
	return moved, ok, nil
}

// packageNotes is `packageNotes` (files.ts:241-262): Pi's package manager installs Git sources in
// <agent>/git or <project>/.pi/git and runs `git clean -fdx` inside their checkouts on update, so
// notes live alongside, not inside, that tree. ok is false for undefined.
func packageNotes(root, cwd string) (moved string, ok bool, err error) {
	// Pi cleans only the checkouts; folders that hold several of them keep their own notes.
	if !existsSync(filepath.Join(root, ".git")) {
		return "", false, nil
	}
	agentDir, err := sdk.AgentDir()
	if err != nil {
		return "", false, err
	}
	installs := filepath.Join(agentDir, "git")
	if existsSync(installs) {
		if installs, err = realpathSync(installs); err != nil {
			return "", false, err
		}
	}
	if path, found := packageRelativePath(installs, root); found {
		return filepath.Join(agentDir, "notes", path), true, nil
	}
	if moved, ok, err = projectPackageNotes(root, cwd); err != nil || ok {
		return moved, ok, err
	}
	return projectPackageNotes(root, root)
}

// Seams of the two process-wide calls the TS tests replace (`fs.renameSync` and `Date.now`,
// package-notes.test.ts:95-106,155). Production code never reassigns them.
var (
	renameSync = os.Rename
	nowMillis  = func() int64 { return time.Now().UnixMilli() }
)

// migrationTarget is `migrationTarget` (files.ts:263-271): preserve every existing entry,
// including empty directories and dangling links.
func migrationTarget(moved string) (string, error) {
	info, err := lstatSyncMissing(moved)
	if err != nil {
		return "", err
	}
	if info == nil {
		return moved, nil
	}
	base := filepath.Join(moved, "from-checkout-"+strconv.FormatInt(nowMillis(), 10))
	target := base
	for suffix := 1; ; suffix++ {
		info, err := lstatSyncMissing(target)
		if err != nil {
			return "", err
		}
		if info == nil {
			return target, nil
		}
		target = base + "-" + strconv.Itoa(suffix)
	}
}

func isDirectory(path string) (bool, error) {
	info, err := lstatSyncMissing(path)
	return info != nil && info.IsDir(), err
}

// mkdirRecursive is `mkdirSync(dir, { recursive: true })`: an existing directory is fine, an existing
// non-directory at dir is EEXIST (mkdir), and a non-directory ancestor stays ENOTDIR as in Node.
func mkdirRecursive(dir string) error {
	if info, err := os.Stat(dir); err == nil && !info.IsDir() {
		return &fsError{Code: "EEXIST", Syscall: "mkdir", Path: dir, Err: syscall.EEXIST}
	}
	if err := os.MkdirAll(dir, 0o777); err != nil {
		return nodeFsError(err, "mkdir", dir)
	}
	return nil
}

// notesDir is `notesDir` (files.ts:272-306): the notes directory for cwd, moving any notes found
// inside a Pi package checkout.
func notesDir(cwd string) (string, error) {
	root := notesRoot(cwd)
	local := filepath.Join(root, ".pi", "notes")
	moved, ok, err := packageNotes(root, cwd)
	if err != nil {
		return "", err
	}
	if !ok {
		return local, nil
	}
	localIsDir, err := isDirectory(local)
	if err != nil {
		return "", err
	}
	if localIsDir {
		if err := mkdirRecursive(nodeDirname(moved)); err != nil {
			return "", err
		}
		// Notes an older Posthorse wrote here after an earlier move keep a folder of their own.
		target, err := migrationTarget(moved)
		if err != nil {
			return "", err
		}
		if renameErr := renameSync(local, target); renameErr != nil {
			if stillDir, err := isDirectory(local); err != nil {
				return "", err
			} else if stillDir {
				return "", nodeFsErrorDest(renameErr, "rename", local, target)
			}
			return moved, nil
		}
		// Sessions still running an older Posthorse keep reaching the notes until they reload.
		if linkErr := symlinkJunction(moved, local); linkErr != nil {
			if stillDir, err := isDirectory(local); err != nil {
				return "", err
			} else if stillDir {
				return "", nodeFsErrorDest(linkErr, "symlink", moved, local)
			}
			// Notes are already outside the checkout; the compatibility link is optional.
		}
	}
	return moved, nil
}
