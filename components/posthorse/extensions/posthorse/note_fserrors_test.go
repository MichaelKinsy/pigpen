package posthorse

// Node's error texts and decoding in the notes store (note-snapshots.ts, note-inventory.ts, note-discovery.ts):
// every fs error the lane reports is `CODE: description, syscall 'path'[ -> 'dest']` as Node 24 prints it (the
// expected strings come from running the same calls under Node on xd6702), a manifest decodes like
// `readFile(utf8)` + `JSON.parse`, and the references of a listing read the session entries once per batch.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNoteFsErrors_snapshotAndAdopt(t *testing.T) {
	root := nsRealTemp(t)
	source := nsWriteNote(t, root, "a.md", "a", time.Now().Add(-time.Hour))
	binding := nsForkBinding(root)
	directory := scopeDirectory(binding)

	// A missing source: the stat of the copy names it.
	_, err := snapshot(binding, []snapshotNote{nsNote(root, "absent.md")})
	want := "Incomplete notes snapshot for " + directory + ": ENOENT: no such file or directory, stat '" + filepath.Join(root, "absent.md") + "'"
	if err == nil || err.Error() != want {
		t.Fatalf("missing source\n got %v\nwant %s", err, want)
	}

	// A destination that exists: copyFile with COPYFILE_EXCL.
	adopter := nsForkBinding(root)
	adopter.owner.id = "adopter"
	adopter.scope, _ = forkScope(adopter.owner)
	if _, err := adoptNote(adopter, nsNote(root, "a.md")); err != nil {
		t.Fatal(err)
	}
	_, err = adoptNote(adopter, nsNote(root, "a.md"))
	dest := filepath.Join(scopeDirectory(adopter), "a.md")
	if want := "EEXIST: file already exists, copyfile '" + source + "' -> '" + dest + "'"; err == nil || err.Error() != want {
		t.Fatalf("existing destination\n got %v\nwant %s", err, want)
	}

	// A destination folder that is a file: recursive mkdir says EEXIST for the path it was given.
	nsWriteNote(t, scopeDirectory(adopter), "sub", "i am a file", time.Now())
	nsWriteNote(t, root, "sub/b.md", "b", time.Now())
	_, err = adoptNote(adopter, nsNote(root, "sub/b.md"))
	if want := "EEXIST: file already exists, mkdir '" + filepath.Join(scopeDirectory(adopter), "sub") + "'"; err == nil || err.Error() != want {
		t.Fatalf("folder is a file\n got %v\nwant %s", err, want)
	}
	// ... and an ancestor that is a file: ENOTDIR with the given path.
	nsWriteNote(t, root, "sub/deep/c.md", "c", time.Now())
	_, err = adoptNote(adopter, nsNote(root, "sub/deep/c.md"))
	if want := "ENOTDIR: not a directory, mkdir '" + filepath.Join(scopeDirectory(adopter), "sub", "deep") + "'"; err == nil || err.Error() != want {
		t.Fatalf("ancestor is a file\n got %v\nwant %s", err, want)
	}

	// The .forks directory is a file: lstat of the container reports ENOTDIR (only ENOENT means "free").
	other := &noteBinding{kind: "fork", owner: noteOwner{"o2", nsTimestamp}, root: filepath.Join(root, "blocked"), absent: []string{}}
	other.scope, _ = forkScope(other.owner)
	nsWriteNote(t, other.root, ".forks", "file", time.Now())
	_, err = snapshot(other, []snapshotNote{nsNote(root, "a.md")})
	if err == nil || !strings.HasPrefix(err.Error(), "ENOTDIR: not a directory, lstat '"+filepath.Dir(scopeDirectory(other))+"'") {
		t.Fatalf("blocked container: %v", err)
	}

	// A manifest that is a directory: readFile fails with EISDIR (no path), ensureManifest reports that.
	dirManifest := &noteBinding{kind: "fork", owner: noteOwner{"o3", nsTimestamp}, root: root, absent: []string{}}
	dirManifest.scope, _ = forkScope(dirManifest.owner)
	forkMust(t, os.MkdirAll(filepath.Join(filepath.Dir(scopeDirectory(dirManifest)), "manifest.json"), 0o755))
	if _, err := readManifest(scopeDirectory(dirManifest)); err == nil || err.Error() != "EISDIR: illegal operation on a directory, read" {
		t.Fatalf("manifest directory: %v", err)
	}
	if err := ensureManifest(dirManifest); err == nil || err.Error() != "EISDIR: illegal operation on a directory, read" {
		t.Fatalf("ensureManifest over a directory: %v", err)
	}
	// Digest of a directory: the stream fails on read, not on open.
	if _, err := fileDigest(nil, root); err == nil || err.Error() != "EISDIR: illegal operation on a directory, read" {
		t.Fatalf("digest of a directory: %v", err)
	}
	if _, err := fileDigest(nil, filepath.Join(root, "missing")); err == nil || err.Error() != "ENOENT: no such file or directory, open '"+filepath.Join(root, "missing")+"'" {
		t.Fatalf("digest of a missing file: %v", err)
	}
}

func TestNoteFsErrors_inventoryAndScandir(t *testing.T) {
	root := nsRealTemp(t)
	file := nsWriteNote(t, root, "file", "x", time.Now())
	if _, err := readDirNode(file); err == nil || err.Error() != "ENOTDIR: not a directory, scandir '"+file+"'" {
		t.Fatalf("scandir of a file: %v", err)
	}
	missing := filepath.Join(root, "nope")
	if _, err := readDirNode(missing); err == nil || err.Error() != "ENOENT: no such file or directory, scandir '"+missing+"'" {
		t.Fatalf("scandir of a missing folder: %v", err)
	}
	// A looping link inside a scope fails the whole walk with the stat error (only ENOENT/ENOTDIR are skipped).
	loop := filepath.Join(root, "loop")
	if err := os.Symlink("loop", loop); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err := notesUnder(nil, noteScope{root: root}, nil, func(string) {})
	if want := "ELOOP: too many symbolic links encountered, stat '" + loop + "'"; err == nil || err.Error() != want {
		t.Fatalf("looping link\n got %v\nwant %s", err, want)
	}
	// The directory walk's realpath names the directory it was given.
	if _, err := realpathNative(loop); err == nil || err.Error() != "ELOOP: too many symbolic links encountered, realpath '"+loop+"'" {
		t.Fatalf("realpath of a loop: %v", err)
	}
	if _, err := realpathNative(missing); err == nil || err.Error() != "ENOENT: no such file or directory, realpath '"+missing+"'" {
		t.Fatalf("realpath of a missing path: %v", err)
	}
}

// readFile(utf8) + JSON.parse: invalid UTF-8 becomes U+FFFD per maximal subpart, a lone surrogate escape survives, a
// number beyond float64 is Infinity (and only fails a schema member).
func TestNoteFsErrors_manifestDecodesLikeNode(t *testing.T) {
	root := nsRealTemp(t)
	binding := nsForkBinding(root)
	directory := scopeDirectory(binding)
	manifest := filepath.Join(filepath.Dir(directory), "manifest.json")
	forkMust(t, os.MkdirAll(filepath.Dir(manifest), 0o755))
	head := string(binding.appendJSON(nil))
	head = strings.TrimSuffix(head, `"absent":[]}`)
	write := func(absent, extra string) {
		t.Helper()
		forkMust(t, os.WriteFile(manifest, []byte(head+`"absent":[`+absent+`]`+extra+`}`), 0o644))
	}

	write("\"\xc3(.md\"", "") // an invalid two-byte sequence: U+FFFD then "("
	got, err := readManifest(directory)
	if err != nil || got == nil || len(got.absent) != 1 || got.absent[0] != "\uFFFD(.md" {
		t.Fatalf("invalid UTF-8: %v %v", got, err)
	}
	write(`"\ud800.md"`, "")
	if got, err = readManifest(directory); err != nil || got == nil || got.absent[0] != "\xed\xa0\x80.md" {
		t.Fatalf("lone surrogate escape: %v %v", got, err)
	}
	write(`"a.md"`, `,"extra":1e400`)
	if got, err = readManifest(directory); err != nil || got == nil {
		t.Fatalf("an out-of-range number outside the schema must not reject the manifest: %v %v", got, err)
	}
	write(`"a.md"`, `,"owner":1e400`)
	if got, _ = readManifest(directory); got != nil {
		t.Fatal("an out-of-range number in a schema member must reject the manifest")
	}
}

// note-store.ts:132 #reference reads getEntries() once per mapBatch batch: the 16 calls of a batch run their
// synchronous prefix together. A listing with 20 inherited, untouched notes is 2 batches.
func TestNoteStore_referencesReadTheEntriesOncePerBatch(t *testing.T) {
	w := newForkWorld(t)
	cwd := w.project("project")
	parent := newForkSide(t, cwd, w.session(cwd))
	parent.mustStart()
	parent.notes(rec{"op": "write", "path": "selected.md", "content": "selected"})
	forkPoint := forkLeaf(parent.sess)
	for i := 0; i < 20; i++ {
		parent.notes(rec{"op": "write", "path": "ref" + string(rune('a'+i)) + ".md", "content": "reference"})
	}
	otherBranch := forkLeaf(parent.sess)
	childSession := w.fork(parent.sess, cwd)
	childSession.branch(forkPoint)
	child := newForkSide(t, cwd, childSession)
	child.mustStart()
	childSession.branch(otherBranch)
	child.f.ResetReads()
	listing := child.notes(rec{"op": "list"})
	if n := forkLines(listing, "ref"); n != 20 {
		t.Fatalf("%d reference rows:\n%s", n, listing)
	}
	// One read for the recovery evidence, one per batch of references.
	if got := child.f.Reads("sessionRead.getEntries"); got != 3 {
		t.Fatalf("getEntries read %d times, want 3 (1 + 2 batches)", got)
	}
}
