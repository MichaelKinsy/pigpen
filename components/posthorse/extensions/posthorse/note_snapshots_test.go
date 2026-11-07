package posthorse

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Snapshot, manifest and batching behaviour of note-snapshots.ts, on real temporary directories.

func nsForkBinding(root string) *noteBinding {
	owner := noteOwner{id: "s1", timestamp: nsTimestamp}
	scope, _ := forkScope(owner)
	return &noteBinding{kind: "fork", owner: owner, root: root, scope: scope, absent: []string{}}
}

func nsWriteNote(t *testing.T, dir, rel, content string, at time.Time) string {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
	return path
}

func nsNote(root, rel string) snapshotNote {
	address := noteAddress{noteScope{root: root}, rel}
	return snapshotNote{path: rel, source: address, lineage: address, anchors: []string{"anchor-" + rel}}
}

func TestNoteSnapshots_mapBatchBoundsParallelismAndKeepsOrder(t *testing.T) {
	var current, peak atomic.Int64
	gate := make(chan struct{})
	var started sync.WaitGroup
	started.Add(16)
	var once sync.Once
	go func() { started.Wait(); once.Do(func() { close(gate) }) }()
	items := make([]int, 40)
	for i := range items {
		items[i] = i
	}
	out, err := mapBatch(items, func(i int) (int, error) {
		n := current.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		if i < 16 {
			started.Done()
			<-gate
		}
		current.Add(-1)
		return i * 2, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if peak.Load() != 16 {
		t.Fatalf("peak concurrency %d, want the batch size %d", peak.Load(), 16)
	}
	for i, v := range out {
		if v != i*2 {
			t.Fatalf("result %d = %d", i, v)
		}
	}
}

func TestNoteSnapshots_mapBatchSettlesABatchAndStopsAtTheFirstFailure(t *testing.T) {
	var ran sync.Map
	items := make([]int, 40)
	for i := range items {
		items[i] = i
	}
	_, err := mapBatch(items, func(i int) (int, error) {
		ran.Store(i, true)
		if i == 9 || i == 3 {
			return 0, errors.New("failed " + string(rune('0'+i)))
		}
		return i, nil
	})
	if err == nil || err.Error() != "failed 3" {
		t.Fatalf("the first failure in item order is returned, got %v", err)
	}
	for i := range 16 {
		if _, ok := ran.Load(i); !ok {
			t.Fatalf("item %d of the failing batch did not settle", i)
		}
	}
	if _, ok := ran.Load(16); ok {
		t.Fatal("a later batch started after a failure")
	}
	if got, err := mapBatch([]int(nil), func(int) (int, error) { return 0, nil }); err != nil || len(got) != 0 {
		t.Fatalf("empty input: %v %v", got, err)
	}
}

func TestNoteSnapshots_snapshotCopiesNotesAndPublishesTheManifest(t *testing.T) {
	root := nsRealTemp(t)
	notes := root
	at := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	source := nsWriteNote(t, notes, "task.md", "parent checkpoint", at)
	nsWriteNote(t, notes, "dir/deep.md", "deep", at.Add(time.Hour))
	nsWriteNote(t, notes, "untouched.md", "not part of the branch", at)
	if err := os.Chmod(source, 0o640); err != nil {
		t.Fatal(err)
	}
	binding := nsForkBinding(root)
	binding.source = &noteScope{root: root}
	got, err := snapshot(binding, []snapshotNote{nsNote(root, "task.md"), nsNote(root, "dir/deep.md")})
	if err != nil {
		t.Fatal(err)
	}
	directory := scopeDirectory(binding)
	if len(got.seeds) != 2 || got.seeds[0].path != "task.md" || got.seeds[1].path != "dir/deep.md" {
		t.Fatalf("seeds %#v", got.seeds)
	}
	copied := filepath.Join(directory, "task.md")
	info, err := os.Stat(copied)
	if err != nil {
		t.Fatal(err)
	}
	sourceInfo, _ := os.Stat(source)
	if !info.ModTime().Equal(sourceInfo.ModTime()) {
		t.Fatalf("mtime %v, source %v", info.ModTime(), sourceInfo.ModTime())
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode %v", info.Mode().Perm())
	}
	sum := sha256.Sum256([]byte("parent checkpoint"))
	if got.seeds[0].digest != hex.EncodeToString(sum[:]) {
		t.Fatalf("digest %s", got.seeds[0].digest)
	}
	if got.seeds[0].identity != fileIdentity(copied, info) {
		t.Fatalf("identity %v vs %v", got.seeds[0].identity, fileIdentity(copied, info))
	}
	if _, err := os.Stat(filepath.Join(directory, "untouched.md")); err == nil {
		t.Fatal("an untouched note was copied")
	}
	manifest, err := os.ReadFile(filepath.Join(filepath.Dir(directory), "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(manifest) != string(got.appendJSON(nil)) {
		t.Fatalf("manifest is not the JSON of the result:\n%s", manifest)
	}
	read, err := readManifest(directory)
	if err != nil || read == nil || len(read.seeds) != 2 {
		t.Fatalf("readManifest = %v, %v", read, err)
	}
	entries, _ := os.ReadDir(filepath.Join(root, ".forks"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".staging-") {
			t.Fatalf("staging directory left behind: %s", e.Name())
		}
	}
	// A second snapshot of the same owner returns the published one untouched.
	again, err := snapshot(nsForkBinding(root), []snapshotNote{nsNote(root, "untouched.md")})
	if err != nil || len(again.seeds) != 2 {
		t.Fatalf("repeat snapshot = %v, %v", again, err)
	}
}

func TestNoteSnapshots_snapshotPreservesUnexpectedData(t *testing.T) {
	root := nsRealTemp(t)
	nsWriteNote(t, root, "a.md", "a", time.Now())
	binding := nsForkBinding(root)
	container := filepath.Dir(scopeDirectory(binding))
	if err := os.MkdirAll(container, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(container, "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := snapshot(binding, []snapshotNote{nsNote(root, "a.md")})
	want := "Incomplete notes snapshot: preserving unexpected data at " + container + "."
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %s", err, want)
	}
	if _, err := os.Stat(filepath.Join(container, "keep.txt")); err != nil {
		t.Fatal("unexpected data was removed")
	}
	// Another owner's manifest in the container is unexpected data too.
	other := &noteBinding{kind: "fork", owner: noteOwner{"other", nsTimestamp}, root: root, scope: binding.scope, absent: []string{}}
	if err := os.WriteFile(filepath.Join(container, "manifest.json"), other.appendJSON(nil), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshot(binding, []snapshotNote{nsNote(root, "a.md")}); err == nil || !strings.HasPrefix(err.Error(), "Incomplete notes snapshot: preserving unexpected data") {
		t.Fatalf("foreign manifest overwritten: %v", err)
	}
}

func TestNoteSnapshots_snapshotWithoutNotesPublishesNothing(t *testing.T) {
	root := nsRealTemp(t)
	binding := nsForkBinding(root)
	got, err := snapshot(binding, nil)
	if err != nil || got != binding {
		t.Fatalf("snapshot = %v, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".forks")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(".forks created for an empty snapshot: %v", err)
	}
}

func TestNoteSnapshots_snapshotFailsWhenASourceChangesDuringTheCopy(t *testing.T) {
	root := nsRealTemp(t)
	source := nsWriteNote(t, root, "a.md", "original", time.Now().Add(-time.Hour))
	noteAfterCopy = func(src, _ string) {
		if src == source {
			_ = os.WriteFile(src, []byte("changed after the copy"), 0o644)
		}
	}
	defer func() { noteAfterCopy = nil }()
	binding := nsForkBinding(root)
	_, err := snapshot(binding, []snapshotNote{nsNote(root, "a.md")})
	want := "Incomplete notes snapshot for " + scopeDirectory(binding) + ": Source note changed during snapshot: " + source + ". Retry initialization."
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v\nwant %s", err, want)
	}
	if _, err := os.Stat(filepath.Dir(scopeDirectory(binding))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a failed snapshot published its container: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(root, ".forks"))
	if len(entries) != 0 {
		t.Fatalf("staging left behind: %v", entries)
	}
}

func TestNoteSnapshots_snapshotFailsForAMissingSource(t *testing.T) {
	root := nsRealTemp(t)
	binding := nsForkBinding(root)
	_, err := snapshot(binding, []snapshotNote{nsNote(root, "absent.md")})
	if err == nil || !strings.HasPrefix(err.Error(), "Incomplete notes snapshot for "+scopeDirectory(binding)+": ") {
		t.Fatalf("err = %v", err)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the cause is not preserved: %v", err)
	}
}

func TestNoteSnapshots_adoptNoteLeavesAnUnverifiedCopy(t *testing.T) {
	root := nsRealTemp(t)
	source := nsWriteNote(t, root, "changing.md", "original reference", time.Now().Add(-time.Hour))
	binding := nsForkBinding(root)
	noteAfterCopy = func(src, _ string) { _ = os.WriteFile(src, []byte("source changed after the copy"), 0o644) }
	defer func() { noteAfterCopy = nil }()
	result, err := adoptNote(binding, nsNote(root, "changing.md"))
	if err != nil {
		t.Fatal(err)
	}
	if result.verified || result.err == nil || !strings.HasPrefix(result.err.Error(), "Source note changed during snapshot: "+source) {
		t.Fatalf("result = %#v", result)
	}
	if got, _ := os.ReadFile(filepath.Join(scopeDirectory(binding), "changing.md")); string(got) != "original reference" {
		t.Fatalf("copy = %q", got)
	}
	// The destination exists now: a second adoption must not overwrite it (COPYFILE_EXCL).
	noteAfterCopy = nil
	if _, err := adoptNote(binding, nsNote(root, "changing.md")); err == nil || !errors.Is(err, os.ErrExist) {
		t.Fatalf("overwrote an existing private note: %v", err)
	}
}

func TestNoteSnapshots_ensureManifest(t *testing.T) {
	root := nsRealTemp(t)
	binding := nsForkBinding(root)
	path := filepath.Join(filepath.Dir(scopeDirectory(binding)), "manifest.json")
	if err := ensureManifest(binding); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(path)
	if string(first) != string(binding.appendJSON(nil)) {
		t.Fatalf("manifest %s", first)
	}
	if err := ensureManifest(binding); err != nil {
		t.Fatalf("an existing manifest of the same owner must be accepted: %v", err)
	}
	other := nsForkBinding(root)
	other.owner = noteOwner{"other", nsTimestamp}
	if err := ensureManifest(other); err == nil || err.Error() != "Preserving unexpected notes manifest at "+path+"." {
		t.Fatalf("foreign manifest: %v", err)
	} else if !errors.Is(err, os.ErrExist) {
		t.Fatalf("cause lost: %v", err)
	}
	project := &noteBinding{kind: "project", owner: binding.owner, root: root}
	if err := ensureManifest(project); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "manifest.json")); err == nil {
		t.Fatal("a project binding published a manifest")
	}
}

func TestNoteSnapshots_readManifestIgnoresWhatIsNotAManifestOfThatDirectory(t *testing.T) {
	root := nsRealTemp(t)
	binding := nsForkBinding(root)
	directory := scopeDirectory(binding)
	manifest := filepath.Join(filepath.Dir(directory), "manifest.json")
	if got, err := readManifest(directory); got != nil || err != nil {
		t.Fatalf("missing: %v %v", got, err)
	}
	if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{
		"syntax error":    "{not json",
		"empty":           "",
		"array":           "[]",
		"invalid binding": `{"version":1}`,
		"project":         string((&noteBinding{kind: "project", owner: binding.owner, root: root}).appendJSON(nil)),
		"other scope":     string((&noteBinding{kind: "fork", owner: binding.owner, root: root, scope: ".forks/x/notes", absent: []string{}}).appendJSON(nil)),
	} {
		if err := os.WriteFile(manifest, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		if got, err := readManifest(directory); got != nil || err != nil {
			t.Errorf("%s: readManifest = %v, %v", name, got, err)
		}
	}
	if err := os.WriteFile(manifest, binding.appendJSON(nil), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := readManifest(directory); got == nil || err != nil || got.owner != binding.owner {
		t.Fatalf("valid manifest: %v %v", got, err)
	}
	// ENOTDIR (the container is a file) is also "no manifest".
	file := filepath.Join(root, "afile")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := readManifest(filepath.Join(file, "notes")); got != nil || err != nil {
		t.Fatalf("ENOTDIR: %v %v", got, err)
	}
}

func TestNoteSnapshots_fileDigest(t *testing.T) {
	root := nsRealTemp(t)
	path := nsWriteNote(t, root, "big.md", strings.Repeat("0123456789", 20000), time.Now())
	sum := sha256.Sum256([]byte(strings.Repeat("0123456789", 20000)))
	got, err := fileDigest(nil, path)
	if err != nil || got != hex.EncodeToString(sum[:]) {
		t.Fatalf("digest %s %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fileDigest(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled digest = %v", err)
	}
	if _, err := fileDigest(nil, filepath.Join(root, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing = %v", err)
	}
}

func TestNoteSnapshots_sameIdentity(t *testing.T) {
	base := noteIdentity{1, 2, 3, 4, 5}
	if !sameIdentity(base, base) {
		t.Fatal("identical identities differ")
	}
	for i, other := range []noteIdentity{{9, 2, 3, 4, 5}, {1, 9, 3, 4, 5}, {1, 2, 9, 4, 5}, {1, 2, 3, 9, 5}, {1, 2, 3, 4, 9}} {
		if sameIdentity(base, other) {
			t.Errorf("field %d ignored", i)
		}
	}
}
