package posthorse

// note-discovery.ts through the notes tool (list) and directly (labels, digest collapse).

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func TestNoteDiscovery_unmanagedForkDirectoriesAreNoticedAndStagingIsIgnored(t *testing.T) {
	w := newForkWorld(t)
	cwd := w.project("project")
	parent := newForkSide(t, cwd, w.session(cwd))
	parent.mustStart()
	parent.notes(rec{"op": "write", "path": "task.md", "content": "parent"})
	forks := filepath.Join(cwd, ".pi", "notes", ".forks")
	forkMust(t, os.MkdirAll(filepath.Join(forks, "junk", "notes"), 0o755))
	forkMust(t, os.MkdirAll(filepath.Join(forks, ".staging-abc123", "notes"), 0o755))
	forkMust(t, os.WriteFile(filepath.Join(forks, "stray-file"), []byte("x"), 0o644))
	listing := parent.notes(rec{"op": "list"})
	for _, name := range []string{"junk", "stray-file"} {
		if want := "Not indexed: unmanaged or incomplete notes data at " + filepath.Join(forks, name) + "."; !strings.Contains(listing, want) {
			t.Fatalf("no notice %q in:\n%s", want, listing)
		}
	}
	if strings.Contains(listing, ".staging-") {
		t.Fatalf("a staging directory was reported:\n%s", listing)
	}
	if n := forkLines(listing, "task.md "); n != 1 {
		t.Fatalf("task.md listed %d times", n)
	}
}

func TestNoteDiscovery_collapsesIdenticalCopiesAndKeepsSameSizeDivergedOnesApart(t *testing.T) {
	w := newForkWorld(t)
	cwd := w.project("project")
	parent := newForkSide(t, cwd, w.session(cwd))
	parent.mustStart()
	parent.notes(rec{"op": "write", "path": "task.md", "content": "aaaa"})
	child := newForkSide(t, cwd, w.fork(parent.sess, cwd))
	child.mustStart()
	child.notes(rec{"op": "read", "path": "task.md"})
	notes := filepath.Join(cwd, ".pi", "notes")
	childDir := scopeDirectory(child.bindingOf(t))

	// Identical bytes: one row, labeled from the child's view with the parent's copy named.
	listing := child.notes(rec{"op": "list"})
	label := regexp.MustCompile(`(?m)^task\.md \[current; read \{"scope":"` + regexp.QuoteMeta(childDir) + `","path":"task\.md"\}; identical copies: \{"scope":"` + regexp.QuoteMeta(notes) + `","path":"task\.md"\}\]  4B  \S+$`)
	if !label.MatchString(listing) {
		t.Fatalf("listing:\n%s", listing)
	}
	if n := forkLines(listing, "task.md "); n != 1 {
		t.Fatalf("%d rows for identical copies", n)
	}

	// Same size, different bytes: the digest tells them apart and both rows stay.
	forkMust(t, os.WriteFile(filepath.Join(notes, "task.md"), []byte("bbbb"), 0o644))
	listing = child.notes(rec{"op": "list"})
	if n := forkLines(listing, "task.md "); n != 2 {
		t.Fatalf("diverged same-size copies must stay apart, got %d rows:\n%s", n, listing)
	}
	forkMatch(t, listing, `(?m)^task\.md \[read-only; read \{"scope":"`+regexp.QuoteMeta(notes)+`","path":"task\.md"\}\]  4B  `)
	forkMatch(t, listing, `(?m)^task\.md \[current; read \{"scope":"`+regexp.QuoteMeta(childDir)+`","path":"task\.md"\}\]  4B  `)
	forkNoMatch(t, listing, `identical copies`)

	// The cached digest of an unchanged file is reused; a rewrite of the same bytes (new identity) is recomputed
	// and collapses again.
	forkMust(t, os.WriteFile(filepath.Join(notes, "task.md"), []byte("aaaa"), 0o644))
	listing = child.notes(rec{"op": "list"})
	if n := forkLines(listing, "task.md "); n != 1 {
		t.Fatalf("restored bytes must collapse again, got %d rows:\n%s", n, listing)
	}
}

func TestNoteDiscovery_listsUntouchedReferencesReadOnlyAndScopeSelectsOne(t *testing.T) {
	w := newForkWorld(t)
	cwd := w.project("project")
	parent := newForkSide(t, cwd, w.session(cwd))
	parent.mustStart()
	parent.notes(rec{"op": "write", "path": "selected.md", "content": "selected"})
	forkPoint := forkLeaf(parent.sess)
	parent.notes(rec{"op": "write", "path": "reference.md", "content": "original reference"})
	otherBranch := forkLeaf(parent.sess)
	childSession := w.fork(parent.sess, cwd)
	childSession.branch(forkPoint)
	child := newForkSide(t, cwd, childSession)
	child.mustStart()
	childSession.branch(otherBranch)
	notes := filepath.Join(cwd, ".pi", "notes")
	listing := child.notes(rec{"op": "list"})
	// reference.md exists in the parent's project scope and was touched on the visible branch: a read-only row.
	forkMatch(t, listing, `(?m)^reference\.md \[read-only; read \{"scope":"`+regexp.QuoteMeta(notes)+`","path":"reference\.md"\}\]  18B  `)
	if n := forkLines(listing, "reference.md "); n != 1 {
		t.Fatalf("reference.md listed %d times:\n%s", n, listing)
	}
	// A folder filter applies to references too.
	filtered := child.notes(rec{"op": "list", "path": "elsewhere"})
	if strings.Contains(filtered, "reference.md") {
		t.Fatalf("path filter ignored for references:\n%s", filtered)
	}
	// An explicit scope lists only that scope, without collapsing or references.
	scoped := child.notes(rec{"op": "list", "scope": notes})
	if !strings.Contains(scoped, "selected.md [read-only; read ") || !strings.Contains(scoped, "reference.md [read-only; read ") {
		t.Fatalf("scoped listing:\n%s", scoped)
	}
}

func TestNoteDiscovery_noteLabelAndTargets(t *testing.T) {
	root := "/r/.pi/notes"
	owner := noteOwner{"s1", nsTimestamp}
	scope, _ := forkScope(owner)
	binding := &noteBinding{kind: "fork", owner: owner, root: root, scope: scope, absent: []string{}}
	session := &sessionNotes{binding: binding}
	current := noteAddress{noteScope{root: root, scope: scope}, "a/b.md"}
	other := noteAddress{noteScope{root: root}, "a/b.md"}
	third := noteAddress{noteScope{root: root, scope: ".forks/x/notes"}, `q"uote.md`}
	note := &discoveredNote{address: current}
	if got := noteLabel(note, session, false); got != "a/b.md" {
		t.Fatalf("unlabeled %q", got)
	}
	dir := scopeDirectory(current)
	if got, want := noteLabel(note, session, true), `a/b.md [current; read {"scope":"`+dir+`","path":"a/b.md"}]`; got != want {
		t.Fatalf("current %q\nwant %q", got, want)
	}
	note = &discoveredNote{address: other, copies: []noteAddress{current, third}}
	want := `a/b.md [read-only; read {"scope":"` + root + `","path":"a/b.md"}; identical copies: {"scope":"` + dir + `","path":"a/b.md"}, {"scope":"` + scopeDirectory(third) + `","path":"q\"uote.md"}]`
	if got := noteLabel(note, session, true); got != want {
		t.Fatalf("copies %q\nwant %q", got, want)
	}
	if got := noteKey(third); got != `["`+root+`",".forks/x/notes","q\"uote.md"]` {
		t.Fatalf("noteKey %q", got)
	}
}

// collapse is the hot part of discovery: groups by lineage and size, digests only same-size candidates, and
// reuses a seed's recorded digest when the identity still matches.
func TestNoteDiscovery_collapseUsesSeedDigestsAndCachesComputedOnes(t *testing.T) {
	root := nsRealTemp(t)
	owner := noteOwner{"s1", nsTimestamp}
	scope, _ := forkScope(owner)
	binding := &noteBinding{kind: "fork", owner: owner, root: root, scope: scope, absent: []string{}}
	session := &sessionNotes{binding: binding, calls: map[string]string{}, revisions: map[string]int{}}
	now := time.Now()
	parentPath := nsWriteNote(t, root, "task.md", "same", now)
	childPath := nsWriteNote(t, filepath.Join(root, filepath.FromSlash(scope)), "task.md", "same", now)
	parentInfo, _ := os.Stat(parentPath)
	childInfo, _ := os.Stat(childPath)
	lineage := noteAddress{noteScope{root: root}, "task.md"}
	file := func(path string, info os.FileInfo, address noteAddress, seed *noteSeed) discoveredNote {
		return discoveredNote{noteFile: noteFile{noteIdentity: fileIdentity(path, info), path: path}, address: address, lineage: lineage, seed: seed}
	}
	// A bogus seed digest proves the seed is trusted while its identity matches: the files differ in digest only there.
	seed := &noteSeed{digest: "not-the-real-digest", identity: fileIdentity(childPath, childInfo)}
	d := newNoteDiscovery()
	files := []discoveredNote{
		file(parentPath, parentInfo, noteAddress{noteScope{root: root}, "task.md"}, nil),
		file(childPath, childInfo, noteAddress{noteScope{root: root, scope: scope}, "task.md"}, seed),
	}
	var notices []string
	out, err := d.collapse(sdk.Context{}, files, session, func(s string) { notices = append(notices, s) })
	if err != nil || len(notices) != 0 {
		t.Fatalf("collapse: %v %v", err, notices)
	}
	if len(out) != 2 {
		t.Fatalf("a trusted seed digest differing from the parent's must keep two rows, got %d", len(out))
	}
	// Without the seed both digests are computed and equal: one row, the current copy first, the other named.
	files[1].seed = nil
	out, err = d.collapse(sdk.Context{}, files, session, func(string) {})
	if err != nil || len(out) != 1 || len(out[0].copies) != 1 {
		t.Fatalf("collapse = %+v, %v", out, err)
	}
	if !sameScope(out[0].address, binding) || out[0].copies[0].scope != "" {
		t.Fatalf("the session's own copy must lead: %+v", out[0])
	}
	if _, ok := d.digests[parentPath]; !ok {
		t.Fatal("the computed digest was not cached")
	}
	// Only same-lineage, same-size candidates are digested: a copy of another size is kept without reading it.
	otherSize := nsWriteNote(t, filepath.Join(root, "elsewhere"), "task.md", "different size", now)
	otherInfo, _ := os.Stat(otherSize)
	d3 := newNoteDiscovery()
	three := append(append([]discoveredNote(nil), files...), file(otherSize, otherInfo, noteAddress{noteScope{root: filepath.Join(root, "elsewhere")}, "task.md"}, nil))
	if out, err = d3.collapse(sdk.Context{}, three, session, func(string) {}); err != nil || len(out) != 2 {
		t.Fatalf("three copies: %d rows, %v", len(out), err)
	}
	if _, digested := d3.digests[otherSize]; digested {
		t.Fatal("a copy of another size was digested")
	}
	// A revision bump invalidates the cached digest of that path.
	session.revisions[childPath]++
	if _, ok := d.knownDigest(files[1], 1); ok {
		t.Fatal("a cached digest of an older revision was reused")
	}
	// A vanished file keeps its row separate and says why.
	forkMust(t, os.Remove(parentPath))
	d2 := newNoteDiscovery()
	var gone []string
	out, err = d2.collapse(sdk.Context{}, files, session, func(s string) { gone = append(gone, s) })
	if err != nil || len(out) != 2 || len(gone) != 1 || !strings.HasPrefix(gone[0], "Duplicate comparison unavailable for ") || !strings.HasSuffix(gone[0], ". Copies remain separate.") {
		t.Fatalf("vanished file: %d rows, notices %q, %v", len(out), gone, err)
	}
}
