package posthorse

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Benchmarks of the notes store with hundreds of notes (snapshot, walk, discovery, journal scans).

func nsBenchNotes(b *testing.B, root string, dirs, perDir int, content string) {
	b.Helper()
	at := time.Now().Add(-time.Hour)
	for d := range dirs {
		dir := filepath.Join(root, fmt.Sprintf("topic-%02d", d))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			b.Fatal(err)
		}
		for n := range perDir {
			path := filepath.Join(dir, fmt.Sprintf("note-%03d.md", n))
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				b.Fatal(err)
			}
			if err := os.Chtimes(path, at, at); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func nsBenchRoot(b *testing.B) string {
	root, err := filepath.EvalSymlinks(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	return root
}

// BenchmarkNoteSnapshot300 copies, verifies and publishes 300 notes of about 1 KiB per operation.
func BenchmarkNoteSnapshot300(b *testing.B) {
	root := nsBenchRoot(b)
	source := filepath.Join(root, "source")
	nsBenchNotes(b, source, 15, 20, strings.Repeat("checkpoint ", 90))
	var notes []snapshotNote
	for d := range 15 {
		for n := range 20 {
			rel := fmt.Sprintf("topic-%02d/note-%03d.md", d, n)
			address := noteAddress{noteScope{root: source}, rel}
			notes = append(notes, snapshotNote{path: rel, source: address, lineage: address, anchors: []string{"a"}})
		}
	}
	target := filepath.Join(root, "target")
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		i++
		binding := nsForkBinding(target)
		binding.owner.id = fmt.Sprint("owner-", i)
		binding.scope, _ = forkScope(binding.owner)
		if _, err := snapshot(binding, notes); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkNotesUnder500 walks a scope of 500 notes in 25 folders.
func BenchmarkNotesUnder500(b *testing.B) {
	root := nsBenchRoot(b)
	nsBenchNotes(b, root, 25, 20, "x")
	scope := noteScope{root: root}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		files, err := notesUnder(nil, scope, nil, func(string) {})
		if err != nil || len(files) != 500 {
			b.Fatalf("%d files, %v", len(files), err)
		}
	}
}

func nsBenchCollapse(b *testing.B, warm bool) {
	root := nsBenchRoot(b)
	owner := noteOwner{"s1", nsTimestamp}
	scope, _ := forkScope(owner)
	binding := &noteBinding{kind: "fork", owner: owner, root: root, scope: scope, absent: []string{}}
	session := &sessionNotes{binding: binding, calls: map[string]string{}, revisions: map[string]int{}}
	parent := filepath.Join(root, "project")
	private := filepath.Join(root, filepath.FromSlash(scope))
	nsBenchNotes(b, parent, 10, 30, strings.Repeat("same bytes ", 40))
	nsBenchNotes(b, private, 10, 30, strings.Repeat("same bytes ", 40))
	var files []discoveredNote
	for _, set := range []struct {
		dir   string
		scope noteScope
	}{{private, noteScope{root: root, scope: scope}}, {parent, noteScope{root: parent}}} {
		found, err := notesUnder(nil, noteScope{root: set.dir}, nil, func(string) {})
		if err != nil {
			b.Fatal(err)
		}
		for _, f := range found {
			rel, _ := filepath.Rel(set.dir, f.path)
			address := noteAddress{set.scope, filepath.ToSlash(rel)}
			files = append(files, discoveredNote{noteFile: f, address: address, lineage: noteAddress{noteScope{root: root}, filepath.ToSlash(rel)}})
		}
	}
	d := newNoteDiscovery()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if !warm {
			d = newNoteDiscovery()
		}
		in := append([]discoveredNote(nil), files...)
		out, err := d.collapse(sdk.Context{}, in, session, func(string) {})
		if err != nil || len(out) != 300 {
			b.Fatalf("%d rows, %v", len(out), err)
		}
	}
}

// BenchmarkNoteCollapse600Cold digests 300 pairs of identical notes (no cache); Warm reuses the digest cache.
func BenchmarkNoteCollapse600Cold(b *testing.B) { nsBenchCollapse(b, false) }
func BenchmarkNoteCollapse600Warm(b *testing.B) { nsBenchCollapse(b, true) }

func nsBenchEntries(n int) []rec {
	owner := nsOwner()
	entries := make([]rec, 0, n)
	for i := range n {
		switch i % 3 {
		case 0:
			entries = append(entries, rec{"type": "custom", "customType": notesBindingType, "data": nsFork()})
		case 1:
			entries = append(entries, rec{"type": "custom", "customType": notesTouchType, "data": rec{
				"version": 1.0, "owner": owner, "scope": rec{"root": "/p/.pi/notes", "scope": nsForkScope},
				"rootToolCallId": fmt.Sprint("r", i), "toolCallId": fmt.Sprint("c", i),
				"files": []any{rec{"root": "/p/.pi/notes", "scope": nsForkScope, "path": fmt.Sprint("n", i, ".md"), "kind": "write"}},
			}})
		default:
			entries = append(entries, rec{"type": "message", "id": fmt.Sprint("m", i), "message": rec{"role": "user", "content": "hello"}})
		}
	}
	return entries
}

// BenchmarkNoteBindingsOf3000 validates the binding entries of a 3000-entry journal.
func BenchmarkNoteBindingsOf3000(b *testing.B) {
	entries := nsBenchEntries(3000)
	b.ReportAllocs()
	for b.Loop() {
		if got := bindingsOf(entries); len(got) != 1000 {
			b.Fatal(len(got))
		}
	}
}

// BenchmarkNoteTouchRecords3000 validates the touch entries of a 3000-entry journal.
func BenchmarkNoteTouchRecords3000(b *testing.B) {
	entries := nsBenchEntries(3000)
	b.ReportAllocs()
	for b.Loop() {
		if got := touchRecords(entries); len(got) != 1000 {
			b.Fatal(len(got))
		}
	}
}

// BenchmarkNoteSessionNoteTouches3000 is the evidence scan over the same journal as branch and journal.
func BenchmarkNoteSessionNoteTouches3000(b *testing.B) {
	entries := nsBenchEntries(3000)
	b.ReportAllocs()
	for b.Loop() {
		sessionNoteTouches(noteEvidence{branch: entries, allEntries: entries})
	}
}

// nsGrandchildBinding is the binding entry data of a fork of a fork with n notes: every seed of the grandchild
// leaks the parent's whole fork binding (resolveReference's `{ ...current, path }`, current = sourceScope's
// binding), which itself holds n seeds. The JSON is O(n^2) bytes, as in the original (about 5 MB at n=100,
// 45 MB at n=300).
func nsGrandchildBinding(b *testing.B, n int) any {
	b.Helper()
	root := "/p/.pi/notes"
	seed := func(path string, source noteAddress) noteSeed {
		return noteSeed{path: path, source: source, lineage: source, digest: "d", identity: noteIdentity{1, 2, 3, 4, 5}, anchors: []string{"anchor"}}
	}
	project := &noteBinding{kind: "project", owner: noteOwner{"grandparent", nsTimestamp}, root: root}
	parentOwner := noteOwner{"parent", nsTimestamp}
	parentScope, _ := forkScope(parentOwner)
	parent := &noteBinding{kind: "fork", owner: parentOwner, root: root, scope: parentScope, source: &noteScope{root: root}, absent: []string{}}
	childOwner := noteOwner{"child", nsTimestamp}
	childScope, _ := forkScope(childOwner)
	child := &noteBinding{kind: "fork", owner: childOwner, root: root, scope: childScope, source: &noteScope{root: root, scope: parentScope}, absent: []string{}}
	for i := range n {
		path := fmt.Sprint("n", i, ".md")
		parent.seeds = append(parent.seeds, seed(path, withPath(project, path)))
		child.seeds = append(child.seeds, seed(path, withPath(parent, path)))
	}
	var value any
	if err := jsonDecode(child.appendJSON(nil), &value); err != nil {
		b.Fatal(err)
	}
	return value
}

func nsBenchParseGrandchild(b *testing.B, n int) {
	value := nsGrandchildBinding(b, n)
	entries := []rec{{"type": "custom", "customType": notesBindingType, "data": value}}
	if parseBinding(value) == nil || len(bindingsOf(entries)) != 1 {
		b.Fatal("grandchild binding rejected")
	}
	b.ReportAllocs()
	for b.Loop() {
		bindingsOf(entries)
	}
}

// BenchmarkNoteParseLeakedForkOfFork100 and 300: `bindingsOf` of a journal holding the grandchild's binding.
// TypeBox's Check visits the declared members only; the leaked parent binding behind every seed source is kept
// raw and parsed only when written, so this is linear in the grandchild's seeds.
func BenchmarkNoteParseLeakedForkOfFork100(b *testing.B) { nsBenchParseGrandchild(b, 100) }
func BenchmarkNoteParseLeakedForkOfFork300(b *testing.B) { nsBenchParseGrandchild(b, 300) }
