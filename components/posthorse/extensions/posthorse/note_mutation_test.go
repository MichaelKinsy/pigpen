package posthorse

// Tests added after the mutation check of the notes store (port/mutations/NotesStoreLane.json): each pins a behaviour
// of note_scopes.go, note_snapshots.go, note_provenance.go, note_evidence.go, note_inventory.go, note_discovery.go,
// note_nodepath.go or note_stat*.go that no earlier test constrained.

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// ---- note_scopes.go ---------------------------------------------------------------------------------------------

func TestNoteMutation_loneSurrogateIsAURIError(t *testing.T) {
	lone := "\xed\xa0\x80"
	if _, err := noteURIComponent("a" + lone); err == nil || err.Error() != "URI malformed" {
		t.Fatalf("encodeURIComponent of a lone surrogate: %v", err)
	}
	if _, err := forkScope(noteOwner{"a" + lone, nsTimestamp}); err == nil {
		t.Fatal("forkScope accepted an id with a lone surrogate")
	}
}

func TestNoteMutation_relativePathsResolveAgainstTheWorkingDirectory(t *testing.T) {
	root := nsRealTemp(t)
	forkMust(t, os.MkdirAll(filepath.Join(root, "sub"), 0o755))
	t.Chdir(root)
	if got, err := canonicalPath("sub/x.md"); err != nil || got != filepath.Join(root, "sub", "x.md") {
		t.Fatalf("canonicalPath(relative) = %q, %v", got, err)
	}
	if got := jsResolve("rel", "x"); got != filepath.Join(root, "rel", "x") {
		t.Fatalf("jsResolve(relative) = %q", got)
	}
}

func TestNoteMutation_schemaCornersTypeBoxRejects(t *testing.T) {
	identity := nsIdentity()
	identity["size"] = math.Inf(1)
	if _, ok := checkIdentity(identity); ok {
		t.Fatal("an infinite number passed Type.Number()")
	}
	identity["size"] = math.NaN()
	if _, ok := checkIdentity(identity); ok {
		t.Fatal("NaN passed Type.Number()")
	}
	if _, ok := checkAddress(rec{"root": "/r", "scope": ""}); ok {
		t.Fatal("an address without a path passed")
	}
}

func TestNoteMutation_seedsOfIgnoresTouchesOfOtherScopes(t *testing.T) {
	binding := parseBinding(nsFork())
	foreign := rec{"version": 1.0, "owner": nsOwner(), "scope": rec{"root": "/p/.pi/notes", "scope": ""}, "rootToolCallId": "r", "toolCallId": "c",
		"files": []any{rec{"root": "/p/.pi/notes", "scope": "", "path": "z.md", "kind": "adopt", "seed": nsWith(nsSeed(), func(s rec) { s["path"] = "z.md" })}}}
	seeds := seedsOf(binding, []rec{{"type": "custom", "customType": notesTouchType, "data": foreign}})
	if len(seeds) != 1 || seeds[0].path != "a.md" {
		t.Fatalf("a seed adopted into another scope became ours: %#v", seeds)
	}
}

// An address that reaches a managed .forks directory through an alias is not an address, whichever spelling matches.
func TestNoteMutation_addressOfRefusesAnAliasIntoManagedForks(t *testing.T) {
	root := nsRealTemp(t)
	notes := filepath.Join(root, "notes")
	private := filepath.Join(notes, ".forks", "x", "notes")
	forkMust(t, os.MkdirAll(private, 0o755))
	if err := os.Symlink(private, filepath.Join(notes, "sublink")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	got, ok, err := addressOf(filepath.Join(notes, "sublink", "a.md"), []noteScope{{root: notes}})
	if err != nil || ok {
		t.Fatalf("addressOf through an alias into .forks = %+v, %v, %v", got, ok, err)
	}
}

// ---- note_snapshots.go ------------------------------------------------------------------------------------------

func TestNoteMutation_manifestDecodingIsWHATWGAndDirectoryBound(t *testing.T) {
	root := nsRealTemp(t)
	binding := nsForkBinding(root)
	directory := scopeDirectory(binding)
	manifest := filepath.Join(filepath.Dir(directory), "manifest.json")
	forkMust(t, os.MkdirAll(filepath.Dir(manifest), 0o755))
	head := strings.TrimSuffix(string(binding.appendJSON(nil)), `"absent":[]}`)
	// A truncated three-byte sequence is ONE replacement character (maximal subpart), not one per byte.
	forkMust(t, os.WriteFile(manifest, []byte(head+"\"absent\":[\"\xe2\x82(.md\"]}"), 0o644))
	got, err := readManifest(directory)
	if err != nil || got == nil || got.absent[0] != "\uFFFD(.md" {
		t.Fatalf("truncated sequence: %v %v", got, err)
	}
	// A manifest of another owner (valid, but for another directory) is not this directory's manifest.
	other := nsForkBinding(root)
	other.owner.id = "other"
	other.scope, _ = forkScope(other.owner)
	forkMust(t, os.WriteFile(manifest, other.appendJSON(nil), 0o644))
	if got, err := readManifest(directory); got != nil || err != nil {
		t.Fatalf("another scope's manifest: %v %v", got, err)
	}
	// A project binding is not a fork manifest even where its directory matches.
	project := &noteBinding{kind: "project", owner: noteOwner{"s1", nsTimestamp}, root: filepath.Join(root, "sub")}
	forkMust(t, os.WriteFile(filepath.Join(root, "manifest.json"), project.appendJSON(nil), 0o644))
	if got, err := readManifest(project.root); got != nil || err != nil {
		t.Fatalf("project manifest: %v %v", got, err)
	}
}

func TestNoteMutation_mapBatchPreparesOncePerBatch(t *testing.T) {
	items := make([]int, 40)
	prepares := 0
	out, err := mapBatchPrepared(items, func() (int, error) { prepares++; return prepares, nil }, func(shared int, _ int) (int, error) { return shared, nil })
	if err != nil || prepares != 3 || out[0] != 1 || out[16] != 2 || out[39] != 3 {
		t.Fatalf("prepares %d, results %v, %v", prepares, out, err)
	}
	ran := false
	_, err = mapBatchPrepared(items, func() (int, error) { return 0, os.ErrInvalid }, func(int, int) (int, error) { ran = true; return 0, nil })
	if err != os.ErrInvalid || ran {
		t.Fatalf("a failing prepare must fail the batch before it runs: %v %v", err, ran)
	}
}

func TestNoteMutation_copyFailureRemovesTheDestination(t *testing.T) {
	root := nsRealTemp(t)
	dest := filepath.Join(root, "copy")
	// A directory opens but cannot be read: the copy fails after the destination was created.
	if err := copyFileExcl(root, dest, 0o644); err == nil {
		t.Fatal("copying a directory succeeded")
	}
	if forkExists(dest) {
		t.Fatal("the failed copy left its destination behind")
	}
}

func TestNoteMutation_snapshotKeepsTheNoteLineageAndStagesInAHiddenDirectory(t *testing.T) {
	root := nsRealTemp(t)
	nsWriteNote(t, root, "a.md", "a", time.Now().Add(-time.Hour))
	note := nsNote(root, "a.md")
	note.lineage = noteAddress{noteScope{root: "/lineage"}, "a.md"}
	var staged []string
	noteAfterCopy = func(string, string) {
		entries, _ := os.ReadDir(filepath.Join(root, ".forks"))
		for _, e := range entries {
			staged = append(staged, e.Name())
		}
	}
	defer func() { noteAfterCopy = nil }()
	got, err := snapshot(nsForkBinding(root), []snapshotNote{note})
	if err != nil || got.seeds[0].lineage.root != "/lineage" {
		t.Fatalf("lineage %+v, %v", got.seeds[0].lineage, err)
	}
	if len(staged) != 1 || !strings.HasPrefix(staged[0], ".staging-") {
		t.Fatalf("while copying, .forks held %v (want one .staging-* directory)", staged)
	}
}

func TestNoteMutation_ensureManifestReportsAWriteFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permissions do not bind root")
	}
	root := nsRealTemp(t)
	binding := nsForkBinding(root)
	container := filepath.Dir(scopeDirectory(binding))
	forkMust(t, os.MkdirAll(container, 0o755))
	forkMust(t, os.Chmod(container, 0o500))
	defer os.Chmod(container, 0o755)
	err := ensureManifest(binding)
	if err == nil || !strings.HasPrefix(err.Error(), "EACCES: permission denied, open '") {
		t.Fatalf("ensureManifest in a read-only container: %v", err)
	}
}

func TestNoteMutation_saveAdoptionNeedsAForkBinding(t *testing.T) {
	project := &noteBinding{kind: "project", owner: noteOwner{"s1", nsTimestamp}, root: "/r"}
	if err := saveAdoption(project, noteSeed{path: "a.md"}); err != nil {
		t.Fatalf("saveAdoption of a project binding must do nothing: %v", err)
	}
}

// ---- note_provenance.go -----------------------------------------------------------------------------------------

func TestNoteMutation_journalHeadType(t *testing.T) {
	cases := []struct {
		head string
		want string
		ok   bool
	}{
		{`{"type":"custom","id":"x"}`, "custom", true},
		{"  \t{  \"type\"  :  \"session\"", "session", true},
		{"\u00a0{\"type\":\"x\"", "x", true},
		{`{"id":"a","type":"x"}`, "", false},
		{`{"type":""}`, "", false},
		{`"type":"x"`, "", false},
		{`{"type":"abc`, "", false},
	}
	for _, c := range cases {
		if got, ok := journalHeadType(c.head); got != c.want || ok != c.ok {
			t.Errorf("journalHeadType(%q) = %q, %v; want %q, %v", c.head, got, ok, c.want, c.ok)
		}
	}
}

func TestNoteMutation_sourceEntriesReadRawJournalLines(t *testing.T) {
	dir := nsRealTemp(t)
	parent := filepath.Join(dir, "parent.jsonl")
	child := filepath.Join(dir, "child.jsonl")
	// Keys in any order: a line whose head does not start with "type" is classified by its parsed type.
	forkMust(t, os.WriteFile(child, []byte(strings.Join([]string{
		`{"type":"session","id":"h1","parentSession":"` + parent + `"}`,
		`{"id":"m1","type":"message","message":{"role":"user","content":"x"}}`,
		`{"id":"c1","type":"custom","customType":"x"}`,
		`{"id":"h2","type":"session","parentSession":"/never/read"}`,
		`{"type":"message","id":"m2","message":{"role":"user","content":"y"}}`,
	}, "\n")+"\n"), 0o644))
	forkMust(t, os.WriteFile(parent, []byte(`{"type":"session","id":"ph"}`+"\n"+`{"type":"custom","id":"pc","customType":"x"}`+"\n"), 0o644))
	entries, err := sourceEntries(nil, &child)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, e := range entries {
		id, _ := e["id"].(string)
		ids = append(ids, id)
	}
	if got := strings.Join(ids, ","); got != "h1,c1,h2,ph,pc" {
		t.Fatalf("entries %s, want h1,c1,h2,ph,pc (messages dropped, the first session's parent followed, ancestors after the child)", got)
	}
}

func nsToolPair(id, tool string, args rec, text string) []rec {
	return []rec{
		fxMessageEntry("a-"+id, fxAssistant([]any{fxToolCall(id, tool, args)})),
		fxMessageEntry("r-"+id, fxToolResult(id, tool, []any{fxText(text)}, false)),
	}
}

func TestNoteMutation_legacySource(t *testing.T) {
	base := nsRealTemp(t)
	one := filepath.Join(base, "one", ".pi", "notes")
	two := filepath.Join(base, "two", ".pi", "notes")
	read := func(id, path string) []rec { return nsToolPair(id, "read", rec{"path": path}, "text") }
	cat := func(parts ...[]rec) []rec {
		var out []rec
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}
	check := func(name string, entries []rec, wantRoot string) {
		t.Helper()
		scope, ok, err := legacySource(entries)
		if err != nil || ok != (wantRoot != "") || scope.root != wantRoot || scope.scope != "" {
			t.Errorf("%s: legacySource = %+v, %v, %v; want root %q", name, scope, ok, err, wantRoot)
		}
	}
	check("one receipt", read("r1", filepath.Join(one, "a.md")), one)
	check("two receipts of one store", cat(read("r1", filepath.Join(one, "a.md")), read("r2", filepath.Join(one, "sub", "b.md"))), one)
	check("two stores are ambiguous", cat(read("r1", filepath.Join(one, "a.md")), read("r2", filepath.Join(two, "a.md"))), "")
	check("a relative path names no root", read("r1", ".pi/notes/a.md"), "")
	check("no receipt", nil, "")
	// The notes tool announces its directory: that alone is the root.
	check("notes directory", nsToolPair("n1", "notes", rec{"op": "list"}, "Notes directory: "+one+"\n(no notes yet)"), one)
}

func TestNoteMutation_sourceScope(t *testing.T) {
	ctx := sdk.Context{}
	dir := nsRealTemp(t)
	header := func(id string) rec { return rec{"type": "session", "id": id, "timestamp": nsTimestamp} }
	binding := func(id, root string) rec {
		return rec{"type": "custom", "customType": notesBindingType, "data": rec{"version": 1.0, "kind": "project", "owner": rec{"id": id, "timestamp": nsTimestamp}, "root": root, "scope": ""}}
	}
	r1, r2, r3 := filepath.Join(dir, "r1"), filepath.Join(dir, "r2"), filepath.Join(dir, "r3")
	// The last binding of the header's owner wins; other owners' bindings do not count; bindings are read from the
	// source journal as well as from the session.
	source := noteSource{entries: []rec{header("p"), binding("p", r1), binding("other", r3), binding("p", r2)}}
	got, ok, err := sourceScope(ctx, nil, source)
	if err != nil || !ok || got.root != r2 {
		t.Fatalf("source scope = %+v, %v, %v; want %s", got, ok, err, r2)
	}
	got, ok, err = sourceScope(ctx, []rec{binding("p", r1)}, noteSource{entries: []rec{header("p"), binding("p", r2)}})
	if err != nil || !ok || got.root != r2 {
		t.Fatalf("session and source bindings = %+v, %v, %v; want the later %s", got, ok, err, r2)
	}
	// Without a header the last binding is taken, whoever owns it.
	got, ok, _ = sourceScope(ctx, nil, noteSource{entries: []rec{binding("a", r1), binding("b", r3)}})
	if !ok || got.root != r3 {
		t.Fatalf("headerless source = %+v, %v", got, ok)
	}
	// A header that names only its id still matches by id and timestamp.
	got, ok, _ = sourceScope(ctx, nil, noteSource{entries: []rec{header("p"), binding("p", r1), binding("p2", r3)}})
	if !ok || got.root != r1 {
		t.Fatalf("owner match by id = %+v, %v", got, ok)
	}
	// The timestamp is part of the owner: a binding of the same id from another start is not the header's.
	later := func(root string) rec {
		return rec{"type": "custom", "customType": notesBindingType, "data": rec{"version": 1.0, "kind": "project", "owner": rec{"id": "p", "timestamp": "2026-01-02T03:04:06.000Z"}, "root": root, "scope": ""}}
	}
	got, ok, _ = sourceScope(ctx, nil, noteSource{entries: []rec{header("p"), binding("p", r1), later(r3)}})
	if !ok || got.root != r1 {
		t.Fatalf("owner timestamp ignored: %+v, %v", got, ok)
	}
	// No binding: the receipts of the session decide when the source file is gone.
	notes := filepath.Join(dir, "legacy", ".pi", "notes")
	missing := filepath.Join(dir, "missing.jsonl")
	got, ok, err = sourceScope(ctx, nsToolPair("r1", "read", rec{"path": filepath.Join(notes, "a.md")}, "x"), noteSource{file: &missing})
	if err != nil || !ok || got.root != notes {
		t.Fatalf("legacy source with a missing file = %+v, %v, %v", got, ok, err)
	}
	// The source file's own receipts take precedence over the session's (read as a legacy journal).
	file := filepath.Join(dir, "source.jsonl")
	other := filepath.Join(dir, "other", ".pi", "notes")
	lines := nsToolPair("r1", "read", rec{"path": filepath.Join(other, "a.md")}, "x")
	forkMust(t, os.WriteFile(file, []byte(fxJSONL(lines...)), 0o644))
	got, ok, err = sourceScope(ctx, nsToolPair("r1", "read", rec{"path": filepath.Join(notes, "a.md")}, "x"), noteSource{file: &file})
	if err != nil || !ok || got.root != other {
		t.Fatalf("legacy source from the file = %+v, %v, %v; want %s", got, ok, err, other)
	}
}

func nsWriteManifest(t *testing.T, binding *noteBinding) {
	t.Helper()
	path := filepath.Join(filepath.Dir(scopeDirectory(binding)), "manifest.json")
	forkMust(t, os.MkdirAll(filepath.Dir(path), 0o755))
	forkMust(t, os.WriteFile(path, binding.appendJSON(nil), 0o644))
}

func nsForkOf(root, id string, source *noteScope) *noteBinding {
	b := &noteBinding{kind: "fork", owner: noteOwner{id, nsTimestamp}, root: root, source: source, absent: []string{}}
	b.scope, _ = forkScope(b.owner)
	return b
}

func nsJournalBinding(b *noteBinding) rec {
	return rec{"type": "custom", "customType": notesBindingType, "data": nsRawBinding(b)}
}

func TestNoteMutation_resolveReference(t *testing.T) {
	base := nsRealTemp(t)
	project := func(name string) noteScope {
		dir := filepath.Join(base, name)
		nsWriteNote(t, dir, "x.md", name, time.Now())
		return noteScope{root: dir}
	}
	p1, p2, p3 := project("p1"), project("p2"), project("p3")
	root := filepath.Join(base, "forks")

	t.Run("a cycle is reported", func(t *testing.T) {
		f1 := nsForkOf(root, "c1", nil)
		f2 := nsForkOf(root, "c2", &noteScope{root: root, scope: f1.scope})
		f1.source = &noteScope{root: root, scope: f2.scope}
		nsWriteManifest(t, f1)
		nsWriteManifest(t, f2)
		_, _, err := resolveReference(f1.scopeOf().plain(), "x.md", nil)
		if err == nil || err.Error() != "Cyclic note provenance at "+scopeDirectory(f1)+"." {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("the manifest is read before the journal and the walk follows the whole chain", func(t *testing.T) {
		deep := nsForkOf(root, "d2", &p1)
		top := nsForkOf(root, "d1", &noteScope{root: root, scope: deep.scope})
		nsWriteManifest(t, top)
		nsWriteManifest(t, deep)
		// The journal disagrees about the first hop; the manifest wins.
		stale := nsForkOf(root, "d1", &p2)
		got, ok, err := resolveReference(top.scopeOf().plain(), "x.md", []rec{nsJournalBinding(stale)})
		if err != nil || !ok || got.root != p1.root {
			t.Fatalf("resolved to %+v, %v, %v; want the project behind two manifests (%s)", got, ok, err, p1.root)
		}
	})
	t.Run("the last journal binding of a scope wins", func(t *testing.T) {
		scope := nsForkOf(root, "j1", &p2)
		first := nsForkOf(root, "j1", &p2)
		last := nsForkOf(root, "j1", &p3)
		got, ok, err := resolveReference(scope.scopeOf().plain(), "x.md", []rec{nsJournalBinding(first), nsJournalBinding(last)})
		if err != nil || !ok || got.root != p3.root {
			t.Fatalf("resolved to %+v, %v, %v; want %s", got, ok, err, p3.root)
		}
	})
}

func TestNoteMutation_touchedCandidates(t *testing.T) {
	base := nsRealTemp(t)
	root := filepath.Join(base, "notes")
	forkMust(t, os.MkdirAll(root, 0o755))
	// The default directory resolves a touch without an absolute path; the receipt, else the entry, anchors it.
	evidence := noteEvidenceResult{
		touches: []noteTouch{
			{notePath: "a.md", entryId: "e1", receiptId: "r1", kind: "clear"},
			{notePath: "b.md", entryId: "e2", kind: "read"},
			{notePath: "c.md", entryId: "e3", receiptId: "r3", kind: "full-write", absPath: filepath.Join(base, "unrelated", "c.md")},
		},
		defaultDir: root,
	}
	got, err := touchedCandidates(evidence, []noteScope{{root: root}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.keys, ",") != "a.md,b.md" {
		t.Fatalf("candidates %v", got.keys)
	}
	a, b := got.values["a.md"], got.values["b.md"]
	if !a.cleared || b.cleared || a.anchors[0] != "r1" || b.anchors[0] != "e2" || a.source.root != root {
		t.Fatalf("candidates %+v %+v", a, b)
	}
	// With no default directory the touch has no address and is skipped.
	got, _ = touchedCandidates(noteEvidenceResult{touches: evidence.touches[:1]}, []noteScope{{root: root}}, nil)
	if len(got.keys) != 0 {
		t.Fatalf("a touch without a directory became a candidate: %v", got.keys)
	}
	// The lineage of a note is the seed lineage of the LAST binding of its scope.
	owner := noteOwner{"o", nsTimestamp}
	scope, _ := forkScope(owner)
	private := noteScope{root: root, scope: scope}
	seed := func(lineage string) noteSeed {
		return noteSeed{path: "a.md", source: noteAddress{noteScope{root: lineage}, "a.md"}, lineage: noteAddress{noteScope{root: lineage}, "a.md"}, anchors: []string{}}
	}
	first := &noteBinding{kind: "fork", owner: owner, root: root, scope: scope, absent: []string{}, seeds: []noteSeed{seed(filepath.Join(base, "L1"))}}
	last := &noteBinding{kind: "fork", owner: owner, root: root, scope: scope, absent: []string{}, seeds: []noteSeed{seed(filepath.Join(base, "L2"))}}
	entries := []rec{nsJournalBinding(first), nsJournalBinding(last)}
	touch := noteTouch{notePath: "a.md", entryId: "e1", absPath: filepath.Join(scopeDirectory(private), "a.md"), kind: "full-write"}
	got, err = touchedCandidates(noteEvidenceResult{touches: []noteTouch{touch}}, []noteScope{private}, entries)
	if err != nil {
		t.Fatal(err)
	}
	if lineage := got.values["a.md"].lineage; lineage.root != filepath.Join(base, "L2") {
		t.Fatalf("lineage %+v, want the last binding's seed", lineage)
	}
}

// ---- note_evidence.go -------------------------------------------------------------------------------------------

func nsRecord(owner rec, rootCall, call string, files ...rec) rec {
	list := make([]any, len(files))
	for i, f := range files {
		list[i] = f
	}
	return rec{"type": "custom", "id": "t-" + call, "customType": notesTouchType, "data": rec{
		"version": 1.0, "owner": owner, "scope": rec{"root": "/p/.pi/notes", "scope": nsForkScope},
		"rootToolCallId": rootCall, "toolCallId": call, "files": list,
	}}
}

func nsFile(path, kind string) rec {
	return rec{"root": "/p/.pi/notes", "scope": nsForkScope, "path": path, "kind": kind}
}

func nsResultEntry(id, call string) rec {
	return fxMessageEntry(id, fxToolResult(call, "codemode", []any{fxText("done")}, false))
}

func nsPaths(touches []noteTouch) string {
	var out []string
	for _, t := range touches {
		out = append(out, t.notePath+":"+t.kind+":"+t.entryId+":"+t.receiptId)
	}
	return strings.Join(out, " ")
}

func TestNoteMutation_evidenceRecordedTouches(t *testing.T) {
	owner := nsOwner()
	eviFor := func(branch []rec, records ...rec) noteEvidence {
		return noteEvidence{branch: branch, allEntries: append(append([]rec(nil), branch...), records...)}
	}
	t.Run("kinds map and a receipt carries its own ids", func(t *testing.T) {
		branch := []rec{nsResultEntry("e1", "T")}
		record := nsRecord(owner, "T", "c1", nsFile("w.md", "write"), nsFile("d.md", "delete"), nsFile("c.md", "clear"), nsFile("r.md", "read"), nsFile("a.md", "adopt"))
		evidence := eviFor(branch, record)
		evidence.retained = map[string]struct{}{"e1": {}}
		got := sessionNoteTouches(evidence)
		want := "w.md:observed-write:e1:e1 d.md:clear:e1:e1 c.md:clear:e1:e1 r.md:read:e1:e1 a.md:observed-write:e1:e1"
		if nsPaths(got.touches) != want {
			t.Fatalf("touches %s\nwant    %s", nsPaths(got.touches), want)
		}
		for _, touch := range got.touches {
			if touch.inRetained || touch.owner == nil || *touch.owner != (noteOwner{"s1", nsTimestamp}) {
				t.Fatalf("touch %+v", touch)
			}
			if touch.absPath != "/p/.pi/notes/"+nsForkScope+"/"+touch.notePath {
				t.Fatalf("abs path %s", touch.absPath)
			}
		}
		if _, ok := got.visibleIds["e1"]; !ok || len(got.visibleIds) != 1 {
			t.Fatalf("visible ids %v", got.visibleIds)
		}
	})
	t.Run("a record is counted once per owner and call, and per owner timestamp", func(t *testing.T) {
		branch := []rec{nsResultEntry("e1", "T")}
		dup := nsRecord(owner, "T", "c1", nsFile("w.md", "write"))
		dup2 := nsRecord(owner, "T", "c1", nsFile("w.md", "write"))
		dup2["id"] = "t-dup"
		later := nsRecord(rec{"id": "s1", "timestamp": "2026-01-02T03:04:06.000Z"}, "T", "c1", nsFile("w.md", "write"))
		later["id"] = "t-later"
		got := sessionNoteTouches(eviFor(branch, dup, dup2, later))
		if len(got.touches) != 2 {
			t.Fatalf("touches %s", nsPaths(got.touches))
		}
	})
	t.Run("a context edit renames the entry but not the receipt", func(t *testing.T) {
		branch := []rec{nsResultEntry("e1", "T"), {"type": "context_edit", "id": "c1", "targetId": "e1", "replacement": rec{"content": []any{fxText("edited")}}}}
		got := sessionNoteTouches(eviFor(branch, nsRecord(owner, "T", "c1", nsFile("w.md", "write"))))
		if nsPaths(got.touches) != "w.md:observed-write:c1:e1" {
			t.Fatalf("touches %s", nsPaths(got.touches))
		}
	})
	t.Run("a background completion is keyed by its run id and the last receipt wins", func(t *testing.T) {
		completion := rec{"type": "custom_message", "id": "e2", "customType": "codemode-background", "content": "x", "display": false, "details": rec{"runId": "T"}}
		got := sessionNoteTouches(eviFor([]rec{nsResultEntry("e1", "T"), completion}, nsRecord(owner, "T", "c1", nsFile("w.md", "write"))))
		if nsPaths(got.touches) != "w.md:observed-write:e2:e2" {
			t.Fatalf("touches %s", nsPaths(got.touches))
		}
		only := sessionNoteTouches(eviFor([]rec{completion}, nsRecord(owner, "T", "c1", nsFile("w.md", "write"))))
		if nsPaths(only.touches) != "w.md:observed-write:e2:e2" {
			t.Fatalf("a completion alone is a receipt: %s", nsPaths(only.touches))
		}
	})
	t.Run("a context edit that deletes the receipt hides the touch; omitted entries are not visible", func(t *testing.T) {
		branch := []rec{nsResultEntry("e1", "T"), {"type": "context_edit", "id": "c1", "targetId": "e1", "replacement": nil}}
		got := sessionNoteTouches(eviFor(branch, nsRecord(owner, "T", "c1", nsFile("w.md", "write"))))
		if len(got.touches) != 0 {
			t.Fatalf("touches %s", nsPaths(got.touches))
		}
		evidence := eviFor([]rec{nsResultEntry("e1", "T"), nsResultEntry("e2", "U")}, nsRecord(owner, "T", "c1", nsFile("w.md", "write")))
		evidence.projected = []projectedEntry{{sourceEntry: rec{"id": "e1"}, messages: nil}, {sourceEntry: rec{"id": "e2"}, messages: []rec{{}}}}
		got = sessionNoteTouches(evidence)
		if _, ok := got.visibleIds["e1"]; ok || len(got.touches) != 0 {
			t.Fatalf("an entry the projection dropped stayed visible: %v %s", got.visibleIds, nsPaths(got.touches))
		}
	})
}

func TestNoteMutation_evidenceMergesLegacyAndRecordedTouches(t *testing.T) {
	owner := nsOwner()
	dir := "/p/.pi/notes/" + nsForkScope
	write := func(id, path string) []rec { return nsToolPair(id, "write", rec{"path": path, "content": "x"}, "ok") }
	// A native write the journal also recorded is one touch, with the recorded owner.
	branch := write("w1", dir+"/a.md")
	record := nsRecord(owner, "w1", "c1", nsFile("a.md", "write"))
	got := sessionNoteTouches(noteEvidence{branch: branch, allEntries: append(append([]rec(nil), branch...), record), directories: []string{dir}})
	if len(got.touches) != 1 || got.touches[0].owner == nil || got.touches[0].notePath != "a.md" {
		t.Fatalf("touches %s", nsPaths(got.touches))
	}
	// Touches of one receipt with the same note name in two scopes stay two touches.
	both := nsRecord(owner, "w1", "c1", nsFile("a.md", "write"), rec{"root": "/p/.pi/notes", "scope": "", "path": "a.md", "kind": "write"})
	got = sessionNoteTouches(noteEvidence{branch: branch, allEntries: append(append([]rec(nil), branch...), both), directories: []string{dir}})
	if len(got.touches) != 2 {
		t.Fatalf("two scopes, one receipt: %s", nsPaths(got.touches))
	}
	// The directories of the bindings in the journal and the explicit ones are known roots.
	binding := nsJournalBinding(parseBinding(nsFork()))
	got = sessionNoteTouches(noteEvidence{branch: write("w2", dir+"/b.md"), allEntries: []rec{binding}})
	if len(got.touches) != 1 || got.touches[0].notePath != "b.md" {
		t.Fatalf("binding directory not known: %s", nsPaths(got.touches))
	}
	got = sessionNoteTouches(noteEvidence{branch: write("w3", "/q/custom/c.md"), directories: []string{"/q/custom"}})
	if len(got.touches) != 1 || got.touches[0].notePath != "c.md" {
		t.Fatalf("explicit directory not known: %s", nsPaths(got.touches))
	}
	// Touches follow the order of their receipts on the branch, recorded ones in between legacy ones.
	branch = append(append(append(write("w4", dir+"/one.md"), nsResultEntry("e5", "T")), write("w6", dir+"/three.md")...),
		rec{"type": "context_edit", "id": "c7", "targetId": "e5", "replacement": rec{"content": []any{fxText("edited")}}})
	got = sessionNoteTouches(noteEvidence{branch: branch, allEntries: append(append([]rec(nil), branch...), nsRecord(owner, "T", "c5", nsFile("two.md", "write"))), directories: []string{dir}})
	var order []string
	for _, touch := range got.touches {
		order = append(order, touch.notePath)
	}
	if strings.Join(order, ",") != "one.md,two.md,three.md" {
		t.Fatalf("order %v", order)
	}
}

func TestNoteMutation_projectedNotesMarkRetainedEntries(t *testing.T) {
	branch := []rec{nsResultEntry("e1", "T"), nsResultEntry("e2", "U")}
	got := projectedNotes(noteEvidence{branch: branch, retained: map[string]struct{}{"e1": {}}})
	if len(got) != 2 || !got[0].inRetained || got[1].inRetained {
		t.Fatalf("retained flags %+v", got)
	}
	// An edit replaces the content of a message only.
	edited := []rec{nsResultEntry("e1", "T"), {"type": "context_edit", "id": "c1", "targetId": "e1", "replacement": rec{"content": []any{fxText("new")}}}}
	got = projectedNotes(noteEvidence{branch: edited})
	message, _ := recOf(got[0].entry["message"])
	if got[0].entryId != "c1" || textOf(message) != "new" {
		t.Fatalf("edited entry %+v", got[0])
	}
}

func TestNoteMutation_scopeRecoveryTouchesAcrossScopes(t *testing.T) {
	root := nsRealTemp(t)
	foreignOwner := noteOwner{"foreign", nsTimestamp}
	foreignScope, _ := forkScope(foreignOwner)
	foreign := &noteBinding{kind: "fork", owner: foreignOwner, root: root, scope: foreignScope, absent: []string{}}
	foreignDir := scopeDirectory(foreign)
	forkMust(t, os.MkdirAll(foreignDir, 0o755))
	entries := []rec{nsJournalBinding(foreign)}
	touch := noteTouch{notePath: "a.md", absPath: filepath.Join(foreignDir, "a.md"), kind: "full-write", entryId: "e1", inRetained: true}
	// A project session leaves every touch alone, whatever scope it names.
	project := &noteBinding{kind: "project", owner: noteOwner{"p", nsTimestamp}, root: root}
	got, err := scopeRecoveryTouches([]noteTouch{touch}, &notesRecovery{binding: project, entries: entries})
	if err != nil || got[0] != touch {
		t.Fatalf("project recovery changed a touch: %+v %v", got, err)
	}
	// A fork session turns another scope's touch into a read of that scope's directory.
	ownOwner := noteOwner{"own", nsTimestamp}
	ownScope, _ := forkScope(ownOwner)
	own := &noteBinding{kind: "fork", owner: ownOwner, root: root, scope: ownScope, absent: []string{}}
	got, err = scopeRecoveryTouches([]noteTouch{touch}, &notesRecovery{binding: own, entries: entries})
	if err != nil || got[0].kind != "read" || got[0].referenceScope != foreignDir || got[0].inRetained {
		t.Fatalf("fork recovery: %+v %v", got, err)
	}
	// Its own source scope is known too.
	source := nsRealTemp(t)
	own.source = &noteScope{root: source}
	inherited := noteTouch{notePath: "b.md", absPath: filepath.Join(source, "b.md"), kind: "partial-write", entryId: "e2"}
	got, _ = scopeRecoveryTouches([]noteTouch{inherited}, &notesRecovery{binding: own})
	if got[0].kind != "read" || got[0].referenceScope != source {
		t.Fatalf("source scope not known: %+v", got[0])
	}
}

// ---- note_inventory.go ------------------------------------------------------------------------------------------

func TestNoteMutation_cancelledWalkStopsStatting(t *testing.T) {
	root := nsRealTemp(t)
	for i := range 300 {
		nsWriteNote(t, root, "n"+string(rune('a'+i/26))+string(rune('a'+i%26))+".md", "x", time.Now())
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	stats := 0
	nsHook(t, func(path string) {
		mu.Lock()
		stats++
		mu.Unlock()
		if strings.HasSuffix(path, ".md") {
			cancel()
		}
	})
	if _, err := notesUnder(ctx, noteScope{root: root}, nil, func(string) {}); err == nil {
		t.Fatal("a cancelled walk returned its files")
	}
	if stats > 70 {
		t.Fatalf("the walk kept statting after the cancellation: %d stats", stats)
	}
}

func TestNoteMutation_cancellationDuringTheLastBatchStillAborts(t *testing.T) {
	root := nsRealTemp(t)
	for _, name := range []string{"a.md", "b.md", "c.md"} {
		nsWriteNote(t, root, name, "x", time.Now())
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	nsHook(t, func(path string) {
		if strings.HasSuffix(path, "c.md") {
			cancel()
		}
	})
	if _, err := notesUnder(ctx, noteScope{root: root}, nil, func(string) {}); err == nil {
		t.Fatal("a walk cancelled in its last batch returned its files")
	}
}

func TestNoteMutation_aFailingNestedWalkFailsTheListing(t *testing.T) {
	root := nsRealTemp(t)
	nsWriteNote(t, root, "sub/a.md", "x", time.Now())
	nsHook(t, func(path string) {
		if path == filepath.Join(root, "sub") {
			_ = os.RemoveAll(path)
		}
	})
	_, err := notesUnder(nil, noteScope{root: root}, nil, func(string) {})
	if want := "ENOENT: no such file or directory, realpath '" + filepath.Join(root, "sub") + "'"; err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %s", err, want)
	}
}

// ---- note_discovery.go ------------------------------------------------------------------------------------------

func TestNoteMutation_collapseCornerCases(t *testing.T) {
	root := nsRealTemp(t)
	owner := noteOwner{"s1", nsTimestamp}
	scope, _ := forkScope(owner)
	binding := &noteBinding{kind: "fork", owner: owner, root: root, scope: scope, absent: []string{}}
	session := &sessionNotes{binding: binding, calls: map[string]string{}, revisions: map[string]int{}}
	now := time.Now()
	lineage := noteAddress{noteScope{root: root}, "task.md"}
	mk := func(dir string, address noteScope, seed *noteSeed) discoveredNote {
		path := nsWriteNote(t, dir, "task.md", "same", now)
		info, _ := os.Stat(path)
		return discoveredNote{noteFile: noteFile{noteIdentity: fileIdentity(path, info), path: path}, address: noteAddress{address, "task.md"}, lineage: lineage, seed: seed}
	}
	current := mk(filepath.Join(root, filepath.FromSlash(scope)), noteScope{root: root, scope: scope}, nil)
	second := mk(filepath.Join(root, "two"), noteScope{root: filepath.Join(root, "two")}, nil)
	third := mk(filepath.Join(root, "three"), noteScope{root: filepath.Join(root, "three")}, nil)
	d := newNoteDiscovery()
	out, err := d.collapse(sdk.Context{}, []discoveredNote{second, third, current}, session, func(string) {})
	if err != nil || len(out) != 1 || len(out[0].copies) != 2 || !sameScope(out[0].address, binding) {
		t.Fatalf("three identical copies: %d rows, %+v, %v", len(out), out, err)
	}
	// A seed whose recorded identity no longer matches the file is not trusted: the digest is computed.
	stale := noteSeed{digest: "not-the-digest", identity: noteIdentity{1, 2, 3, 4, 5}}
	current.seed = &stale
	out, err = newNoteDiscovery().collapse(sdk.Context{}, []discoveredNote{current, second}, session, func(string) {})
	if err != nil || len(out) != 1 {
		t.Fatalf("a stale seed identity must be ignored: %d rows, %v", len(out), err)
	}
	// A file that changed after it was listed (new identity, same bytes) is not compared: its row stays apart.
	changed := second
	time.Sleep(20 * time.Millisecond)
	forkMust(t, os.WriteFile(changed.path, []byte("same"), 0o644))
	out, err = newNoteDiscovery().collapse(sdk.Context{}, []discoveredNote{current, changed}, session, func(string) {})
	if err != nil || len(out) != 2 {
		t.Fatalf("a changed file must not be digested into a copy: %d rows, %v", len(out), err)
	}
	// Rows keep the order of their first member: the session's own copy first, then each distinct content.
	other := mk(filepath.Join(root, "other"), noteScope{root: filepath.Join(root, "other")}, nil)
	forkMust(t, os.WriteFile(other.path, []byte("diff"), 0o644))
	info, _ := os.Stat(other.path)
	other.noteIdentity = fileIdentity(other.path, info)
	other.size = 4
	renamed := second
	forkMust(t, os.WriteFile(renamed.path, []byte("fine"), 0o644))
	info, _ = os.Stat(renamed.path)
	renamed.noteIdentity = fileIdentity(renamed.path, info)
	current.seed = nil
	out, err = newNoteDiscovery().collapse(sdk.Context{}, []discoveredNote{renamed, current, third}, session, func(string) {})
	if err != nil || len(out) != 2 || !sameScope(out[0].address, binding) || out[1].path != renamed.path {
		t.Fatalf("row order: %d rows, %v", len(out), err)
	}
}

func TestNoteMutation_listingScenarios(t *testing.T) {
	w := newForkWorld(t)
	cwd := w.project("project")
	parent := newForkSide(t, cwd, w.session(cwd))
	parent.mustStart()
	parent.notes(rec{"op": "write", "path": "selected.md", "content": "selected"})
	forkPoint := forkLeaf(parent.sess)
	parent.notes(rec{"op": "write", "path": "refa.md", "content": "A"})
	parent.notes(rec{"op": "write", "path": "refb.md", "content": "B"})
	otherBranch := forkLeaf(parent.sess)
	notes := filepath.Join(cwd, ".pi", "notes")
	forkMust(t, os.MkdirAll(filepath.Join(notes, "sub"), 0o755))
	childSession := w.fork(parent.sess, cwd)
	childSession.branch(forkPoint)
	child := newForkSide(t, cwd, childSession)
	child.mustStart()
	childSession.branch(otherBranch)
	childDir := scopeDirectory(child.bindingOf(t))
	// The folder filter of a listing selects references by folder, not by name prefix.
	if filtered := child.notes(rec{"op": "list", "path": "ref"}); strings.Contains(filtered, "refa.md") {
		t.Fatalf("path filter matched a name prefix:\n%s", filtered)
	}
	// An explicit scope lists that scope only: no references of the session's branch.
	scoped := child.notes(rec{"op": "list", "scope": childDir})
	if strings.Contains(scoped, "refa.md") || !strings.Contains(scoped, "selected.md") {
		t.Fatalf("scoped listing:\n%s", scoped)
	}
	// A directory touched by a native read is not a note.
	childSession.appendMessage(fxAssistant([]any{fxToolCall("dirread", "read", rec{"path": filepath.Join(notes, "sub")})}))
	childSession.appendMessage(fxToolResult("dirread", "read", []any{fxText("listing")}, false))
	if listing := child.notes(rec{"op": "list"}); strings.Contains(listing, "sub [") || strings.Contains(listing, "\nsub ") {
		t.Fatalf("a directory was listed as a reference:\n%s", listing)
	}
	// Notices of a scope walk are part of the listing; newest first across scopes.
	forkMust(t, os.WriteFile(filepath.Join(childDir, "fresh.md"), []byte("fresh"), 0o644))
	if err := os.Symlink(filepath.Join(notes, "refa.md"), filepath.Join(childDir, "outside.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	listing := child.notes(rec{"op": "list"})
	if !strings.Contains(listing, "Not indexed: alias outside private notes scope (") {
		t.Fatalf("no alias notice:\n%s", listing)
	}
	rows := strings.Split(strings.SplitN(listing, "\n", 2)[1], "\n")
	if !strings.HasPrefix(rows[0], "fresh.md ") {
		t.Fatalf("the newest note must lead the listing:\n%s", listing)
	}
}

func TestNoteMutation_aScopeWithoutItsManifestStillCollapsesSeededCopies(t *testing.T) {
	w := newForkWorld(t)
	cwd := w.project("project")
	parent := newForkSide(t, cwd, w.session(cwd))
	parent.mustStart()
	parent.notes(rec{"op": "write", "path": "task.md", "content": "same"})
	child := newForkSide(t, cwd, w.fork(parent.sess, cwd))
	child.mustStart()
	forkMust(t, os.Remove(filepath.Join(filepath.Dir(scopeDirectory(child.bindingOf(t))), "manifest.json")))
	listing := child.notes(rec{"op": "list"})
	if n := forkLines(listing, "task.md "); n != 1 {
		t.Fatalf("the seeds of the session's own binding give the lineage: %d rows\n%s", n, listing)
	}
	forkMatch(t, listing, "Not indexed: unmanaged or incomplete notes data at ")
	forkMatch(t, listing, `(?m)^task\.md \[current; `)
}

// ---- note_nodepath.go -------------------------------------------------------------------------------------------

func TestNoteMutation_nodePathCorners(t *testing.T) {
	if got := posixStyle.resolve("", ""); got != "." {
		t.Errorf("resolve of nothing = %q", got)
	}
	if got := posixStyle.resolve("", "a/../../b"); got != "../b" {
		t.Errorf("resolve above a relative root = %q", got)
	}
	if got := posixStyle.resolve("/w", "a/../../b"); got != "/b" {
		t.Errorf("resolve above an absolute root = %q", got)
	}
}

func TestNoteMutation_realpathFollowsRelativeLinksFromTheirDirectory(t *testing.T) {
	root := nsRealTemp(t)
	forkMust(t, os.MkdirAll(filepath.Join(root, "d", "target"), 0o755))
	if err := os.Symlink("target", filepath.Join(root, "d", "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	got, err := nodeRealpath(filepath.Join(root, "d", "link", "x"))
	if err == nil {
		t.Fatalf("a missing file resolved: %q", got)
	}
	if got, err = nodeRealpath(filepath.Join(root, "d", "link")); err != nil || got != filepath.Join(root, "d", "target") {
		t.Fatalf("relative link = %q, %v", got, err)
	}
}

func TestNoteMutation_directoryOrderIsSorted(t *testing.T) {
	root := nsRealTemp(t)
	for _, name := range []string{"m", "b", "z", "a", "k", "c", "x", "d", "q", "e", "w", "f"} {
		nsWriteNote(t, root, name, "x", time.Now())
	}
	entries, err := readDirNode(root)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if got := strings.Join(names, ""); got != "abcdefkmqwxz" {
		t.Fatalf("readdir order %q (libuv's scandir sorts on Unix)", got)
	}
}

// A path that reaches outside the scope through a link inside it is still addressed by its spelling under the scope.
func TestNoteMutation_addressOfFallsBackToTheSpelledPath(t *testing.T) {
	root := nsRealTemp(t)
	notes := filepath.Join(root, "notes")
	outside := filepath.Join(root, "outside")
	forkMust(t, os.MkdirAll(notes, 0o755))
	forkMust(t, os.MkdirAll(outside, 0o755))
	if err := os.Symlink(outside, filepath.Join(notes, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	got, ok, err := addressOf(filepath.Join(notes, "link", "a.md"), []noteScope{{root: notes}})
	if err != nil || !ok || got.root != notes || got.path != "link/a.md" {
		t.Fatalf("addressOf = %+v, %v, %v", got, ok, err)
	}
}
