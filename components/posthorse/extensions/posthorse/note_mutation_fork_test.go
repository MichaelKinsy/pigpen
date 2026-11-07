package posthorse

// Mutation-check tests that need the store probe or a forked session (see note_mutation_test.go).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func nsCallErr(t *testing.T, f *fixture, params rec) string {
	t.Helper()
	_, failure := f.RunErr("nsprobe", params)
	if failure == "" {
		t.Fatalf("nsprobe %v succeeded", params)
	}
	return failure
}

func TestNoteMutation_storeScopesAndRoots(t *testing.T) {
	w := newForkWorld(t)
	cwd := w.project("project")
	parent := newForkSide(t, cwd, w.session(cwd))
	parent.mustStart()
	parent.notes(rec{"op": "write", "path": "task.md", "content": "parent"})
	childSession := w.fork(parent.sess, cwd)
	// A binding the parent's journal gains after the fork is known to the child through its source journal.
	r3 := filepath.Join(w.root, "r3", ".pi", "notes")
	parent.sess.appendCustomEntry(notesBindingType, rec{"version": 1.0, "kind": "project", "owner": rec{"id": "gp", "timestamp": nsTimestamp}, "root": r3, "scope": ""})
	f, probe := newProbeSide(t, cwd, childSession)
	nsCall(t, f, rec{"op": "get"})
	binding := bindingsOf(childSession.getEntries())
	own := binding[len(binding)-1]
	notes := filepath.Join(cwd, ".pi", "notes")
	want := strings.Join([]string{
		notes + "|" + own.scope, // the binding
		notes + "|",             // the project scope
		notes + "|",             // the source scope
		notes + "|",             // the parent's binding in the session
		notes + "|" + own.scope, // the session's own binding, appended at the first call
		notes + "|",             // the parent's binding in its journal
		r3 + "|",                // a binding only the source journal holds
	}, "\n")
	if got := nsCall(t, f, rec{"op": "scopes"}); got != want {
		t.Fatalf("scopes\n%s\nwant\n%s", got, want)
	}
	// roots() hands out a copy.
	roots := probe.store.roots()
	roots[0] = "changed"
	if again := probe.store.roots(); again[0] == "changed" {
		t.Fatal("roots() shares its slice")
	}
}

func TestNoteMutation_storeRevisionAndRecords(t *testing.T) {
	w := newForkWorld(t)
	cwd := w.project("project")
	parent := newForkSide(t, cwd, w.session(cwd))
	parent.mustStart()
	parent.notes(rec{"op": "write", "path": "task.md", "content": "parent"})
	childSession := w.fork(parent.sess, cwd)
	f, _ := newProbeSide(t, cwd, childSession)
	nsCall(t, f, rec{"op": "get"})
	all := bindingsOf(childSession.getEntries())
	directory := scopeDirectory(all[len(all)-1])
	alias := filepath.Join(w.root, "alias")
	if err := os.Symlink(directory, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	nsCall(t, f, rec{"op": "record", "path": "fresh.md", "kind": "write", "id": "c1"})
	// The revision is keyed by the canonical path: an alias of the directory sees it.
	if got := nsCall(t, f, rec{"op": "revision", "path": filepath.Join(alias, "fresh.md")}); got != "1" {
		t.Fatalf("revision through an alias = %s", got)
	}
	// A touch record names its scope.
	touches := touchEntries(childSession)
	data := touches[len(touches)-1]["data"].(map[string]any)
	if scope := data["scope"].(map[string]any); scope["scope"] != all[len(all)-1].scope || scope["root"] != all[len(all)-1].root {
		t.Fatalf("touch scope %v", scope)
	}
	// No files, no entry; a write in another scope is not ours.
	before := len(touchEntries(childSession))
	nsCall(t, f, rec{"op": "recordnone", "id": "c2"})
	if after := len(touchEntries(childSession)); after != before {
		t.Fatalf("an empty record appended an entry (%d -> %d)", before, after)
	}
	if got := nsCall(t, f, rec{"op": "recordforeign", "path": "foreign.md", "kind": "write", "id": "c3"}); got != "not owned" {
		t.Fatalf("a write to another scope: %s", got)
	}
}

func TestNoteMutation_selectScopeRefusesANestedRootManifest(t *testing.T) {
	w := newForkWorld(t)
	cwd := w.project("project")
	parent := newForkSide(t, cwd, w.session(cwd))
	parent.mustStart()
	parent.notes(rec{"op": "write", "path": "task.md", "content": "parent"})
	f, _ := newProbeSide(t, cwd, w.fork(parent.sess, cwd))
	nsCall(t, f, rec{"op": "get"})
	notes := filepath.Join(cwd, ".pi", "notes")
	// A manifest whose root differs from ours, although its directory lies inside our .forks.
	nested := nsForkOf(filepath.Join(notes, ".forks", "nest"), "x", nil)
	nsWriteManifest(t, nested)
	if failure := nsCallErr(t, f, rec{"op": "select", "scope": scopeDirectory(nested)}); !strings.HasPrefix(failure, "Unknown notes scope ") {
		t.Fatalf("failure %q", failure)
	}
}

func TestNoteMutation_adoptReportsAManifestItCannotSave(t *testing.T) {
	w := newForkWorld(t)
	cwd := w.project("project")
	parent := newForkSide(t, cwd, w.session(cwd))
	parent.mustStart()
	parent.notes(rec{"op": "write", "path": "selected.md", "content": "selected"})
	forkPoint := forkLeaf(parent.sess)
	parent.notes(rec{"op": "write", "path": "later.md", "content": "later"})
	other := forkLeaf(parent.sess)
	childSession := w.fork(parent.sess, cwd)
	childSession.branch(forkPoint)
	f, _ := newProbeSide(t, cwd, childSession)
	nsCall(t, f, rec{"op": "get"})
	childSession.branch(other)
	all := bindingsOf(childSession.getEntries())
	directory := scopeDirectory(all[len(all)-1])
	manifest := filepath.Join(filepath.Dir(directory), "manifest.json")
	forkMust(t, os.Remove(manifest))
	forkMust(t, os.MkdirAll(manifest, 0o755))
	failure := nsCallErr(t, f, rec{"op": "adopt", "path": "later.md", "id": "a1"})
	want := "Private copy created at " + filepath.Join(directory, "later.md") + "; source not edited, but its adoption manifest could not be saved. Read the private copy before retrying."
	if failure != want {
		t.Fatalf("failure %q\nwant    %q", failure, want)
	}
	// The foreign-path guard takes the turn of the destination in the file mutation queue.
	destination := filepath.Join(directory, "later.md")
	held, release := make(chan struct{}), make(chan struct{})
	go func() {
		_, _ = sdk.WithFileMutationQueue(destination, func() (struct{}, error) {
			close(held)
			<-release
			return struct{}{}, nil
		})
	}()
	<-held
	done := make(chan string, 1)
	go func() {
		res, failure := f.RunErr("nsprobe", rec{"op": "guard", "path": "later.md", "id": "g1"})
		done <- res.Text() + failure
	}()
	select {
	case got := <-done:
		t.Fatalf("the guard ran while another mutation held the destination: %q", got)
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	if got := <-done; !strings.HasPrefix(got, "This session writes only ") {
		t.Fatalf("guard after release: %q", got)
	}
}

func TestNoteMutation_rootsOfASessionThatFailedToInitialize(t *testing.T) {
	w := newForkWorld(t)
	a := w.project("a")
	b := w.project("b")
	parent := newForkSide(t, a, w.session(a))
	parent.mustStart()
	parent.notes(rec{"op": "write", "path": "task.md", "content": "parent"})
	forkMust(t, os.Remove(filepath.Join(a, ".pi", "notes", "task.md")))
	f, probe := newProbeSide(t, b, w.fork(parent.sess, b))
	if failure := nsCallErr(t, f, rec{"op": "get"}); !strings.Contains(failure, "source note task.md is unavailable") {
		t.Fatalf("failure %q", failure)
	}
	roots := strings.Join(probe.store.roots(), "\n")
	if !strings.Contains(roots, filepath.Join(a, ".pi", "notes")) || !strings.Contains(roots, filepath.Join(b, ".pi", "notes")) {
		t.Fatalf("roots %q: the session's root and the roots of its bindings are known before the binding exists", roots)
	}
}

func TestNoteMutation_rootsIncludeALegacySource(t *testing.T) {
	w := newForkWorld(t)
	a := w.project("a")
	b := w.project("b")
	notes := filepath.Join(a, ".pi", "notes")
	forkMust(t, os.MkdirAll(notes, 0o755))
	forkMust(t, os.WriteFile(filepath.Join(notes, "legacy.md"), []byte("legacy"), 0o644))
	parent := w.session(a)
	forkNativeReceipt(parent, "read-0", "read", rec{"path": filepath.Join(notes, "legacy.md")}, "legacy")
	f, probe := newProbeSide(t, b, w.fork(parent, b))
	nsCall(t, f, rec{"op": "get"})
	if roots := strings.Join(probe.store.roots(), "\n"); !strings.Contains(roots, notes) {
		t.Fatalf("roots %q lack the legacy source scope", roots)
	}
}

func TestNoteMutation_loadNotesBinding(t *testing.T) {
	w := newForkWorld(t)
	cwd := w.project("project")
	// No header: an error naming it.
	headless := w.session(cwd)
	headless.header = nil
	f, _ := newProbeSide(t, cwd, headless)
	if failure := nsCallErr(t, f, rec{"op": "load", "root": "/r"}); failure != "Pi did not supply the session header for notes ownership." {
		t.Fatalf("failure %q", failure)
	}
	// The last binding of the session's own owner wins and nothing is created.
	own := w.session(cwd)
	header := own.getHeader()
	ownOwner := rec{"id": header["id"], "timestamp": header["timestamp"]}
	for _, root := range []string{"/r1", "/r2"} {
		own.appendCustomEntry(notesBindingType, rec{"version": 1.0, "kind": "project", "owner": ownOwner, "root": root, "scope": ""})
	}
	g, _ := newProbeSide(t, cwd, own)
	if got := nsCall(t, g, rec{"op": "load", "root": "/r"}); got != "project /r2 false 0" {
		t.Fatalf("own bindings: %q", got)
	}
	// Another owner's binding in a session with no parent makes it a fork.
	inheriting := w.session(cwd)
	inheriting.appendCustomEntry(notesBindingType, rec{"version": 1.0, "kind": "project", "owner": rec{"id": "elsewhere", "timestamp": nsTimestamp}, "root": filepath.Join(w.root, "r3"), "scope": ""})
	h, _ := newProbeSide(t, cwd, inheriting)
	if got := nsCall(t, h, rec{"op": "load", "root": filepath.Join(w.root, "r3")}); !strings.HasPrefix(got, "fork ") || !strings.HasSuffix(got, " true 0") {
		t.Fatalf("inherited binding: %q", got)
	}
}

func TestNoteMutation_saveAdoptionRewritesTheManifest(t *testing.T) {
	w := newForkWorld(t)
	cwd := w.project("project")
	parent := newForkSide(t, cwd, w.session(cwd))
	parent.mustStart()
	parent.notes(rec{"op": "write", "path": "task.md", "content": "parent"})
	f, _ := newProbeSide(t, cwd, w.fork(parent.sess, cwd))
	nsCall(t, f, rec{"op": "get"})
	seeds := func(text string) string {
		var manifest struct {
			Seeds []struct{ Path, Digest string }
		}
		if err := json.Unmarshal([]byte(text), &manifest); err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, s := range manifest.Seeds {
			out = append(out, s.Path+"="+s.Digest)
		}
		return strings.Join(out, ",")
	}
	if got := seeds(nsCall(t, f, rec{"op": "save", "path": "a.md", "digest": "d1"})); !strings.HasSuffix(got, ",a.md=d1") {
		t.Fatalf("after the first save: %s", got)
	}
	nsCall(t, f, rec{"op": "save", "path": "b.md", "digest": "d2"})
	got := seeds(nsCall(t, f, rec{"op": "save", "path": "a.md", "digest": "d3"}))
	if !strings.HasSuffix(got, "b.md=d2,a.md=d3") || strings.Count(got, "a.md=") != 1 {
		t.Fatalf("a saved seed replaces its predecessor and goes last: %s", got)
	}
	// A manifest that is not ours is preserved, not overwritten.
	manifest := filepath.Join(filepath.Dir(nsProbeDirectory(t, f)), "manifest.json")
	forkMust(t, os.WriteFile(manifest, []byte("garbage"), 0o644))
	failure := nsCallErr(t, f, rec{"op": "save", "path": "c.md", "digest": "d4"})
	if !strings.HasPrefix(failure, "Preserving unexpected notes manifest at ") {
		t.Fatalf("failure %q", failure)
	}
}

// nsProbeDirectory is the session's private notes directory, from the probe.
func nsProbeDirectory(t *testing.T, f *fixture) string {
	t.Helper()
	line := strings.SplitN(nsCall(t, f, rec{"op": "get"}), " ", 3)
	return line[2]
}

// A fork whose notes were cleared at the source records them as absent instead of failing.
func TestNoteMutation_aClearedNoteMissingAtTheSourceIsAbsent(t *testing.T) {
	w := newForkWorld(t)
	cwd := w.project("project")
	parent := newForkSide(t, cwd, w.session(cwd))
	parent.mustStart()
	parent.notes(rec{"op": "write", "path": "x.md", "content": ""})
	forkMust(t, os.Remove(filepath.Join(cwd, ".pi", "notes", "x.md")))
	child := newForkSide(t, cwd, w.fork(parent.sess, cwd))
	child.mustStart()
	data := child.bindingOf(t)
	if len(data.absent) != 1 || data.absent[0] != "x.md" {
		t.Fatalf("absent %v", data.absent)
	}
}

// A fork of a fork keeps the first generation's lineage, anchors a touched note at its newest receipt and records
// its source scope as a plain { root, scope }.
func TestNoteMutation_grandchildSeedsKeepLineageAnchorsAndAPlainSource(t *testing.T) {
	w := newForkWorld(t)
	cwd := w.project("project")
	gp := newForkSide(t, cwd, w.session(cwd))
	gp.mustStart()
	gp.notes(rec{"op": "write", "path": "a.md", "content": "first"})
	child := newForkSide(t, cwd, w.fork(gp.sess, cwd))
	child.mustStart()
	child.notes(rec{"op": "read", "path": "a.md"})
	lastReceipt := ""
	for _, e := range child.sess.getEntries() {
		if m, ok := recOf(e["message"]); ok && m["role"] == "toolResult" && m["toolName"] == "notes" {
			lastReceipt = fxID(e)
		}
	}
	grand := newForkSide(t, cwd, w.fork(child.sess, cwd))
	grand.mustStart()
	binding := grand.bindingOf(t)
	if len(binding.seeds) != 1 {
		t.Fatalf("seeds %+v", binding.seeds)
	}
	seed := binding.seeds[0]
	notes := filepath.Join(cwd, ".pi", "notes")
	if seed.lineage.root != notes || seed.lineage.scope != "" {
		t.Fatalf("lineage %+v: the first generation's address, not the parent's private copy", seed.lineage)
	}
	if len(seed.anchors) != 1 || seed.anchors[0] != lastReceipt {
		t.Fatalf("anchors %v, want the newest receipt %s", seed.anchors, lastReceipt)
	}
	manifest := forkRead(t, filepath.Join(filepath.Dir(scopeDirectory(binding)), "manifest.json"))
	childScope := child.bindingOf(t).scope
	want := `"source":{"root":` + jsQuote(notes) + `,"scope":` + jsQuote(childScope) + `},"seeds":[`
	if !strings.Contains(manifest, want) {
		t.Fatalf("manifest source is not a plain scope:\n%s", manifest)
	}
}

// One receipt of a relative notes path leaves no root to snapshot from.
func TestNoteMutation_aSingleRelativeReceiptHasNoSourceRoot(t *testing.T) {
	w := newForkWorld(t)
	cwd := w.project("project")
	parent := w.session(cwd)
	forkNativeReceipt(parent, "read-0", "read", rec{"path": ".pi/notes/a.md"}, "text")
	child := newForkSide(t, cwd, w.fork(parent, cwd))
	failure := child.start()
	forkMatch(t, failure, `Incomplete notes snapshot: the source notes root is missing or ambiguous`)
}

// The parent's journal after the fork tells the child what the parent owns: re-read at every call while the
// parent's file exists, and kept from the child's start when it does not.
func TestNoteMutation_ownershipRecordedLaterInTheParentJournal(t *testing.T) {
	w := newForkWorld(t)
	cwd := w.project("project")
	p := newForkSide(t, cwd, w.session(cwd))
	p.mustStart()
	p.notes(rec{"op": "write", "path": "a.md", "content": "a"})
	forkPoint := forkLeaf(p.sess)
	p.notes(rec{"op": "write", "path": "m.md", "content": "project m"})
	p.notes(rec{"op": "write", "path": "q.md", "content": "project q"})
	f1Session := w.fork(p.sess, cwd)
	f1Session.branch(forkPoint)
	f1 := newForkSide(t, cwd, f1Session)
	f1.seq = 100 // call ids that no receipt of the parent's branch shares
	f1.mustStart()
	f2Session := w.fork(f1Session, cwd)
	f2 := newForkSide(t, cwd, f2Session)
	f1Dir := scopeDirectory(f1.bindingOf(t))
	// m.md: written and removed by f1 after f2 exists and before f2 starts.
	f1.notes(rec{"op": "write", "path": "m.md", "content": "f1 m"})
	forkMust(t, os.Remove(filepath.Join(f1Dir, "m.md")))
	f2.mustStart()
	// q.md: written and removed after f2 started.
	f1.notes(rec{"op": "write", "path": "q.md", "content": "f1 q"})
	forkMust(t, os.Remove(filepath.Join(f1Dir, "q.md")))
	forkMatch(t, f2.notesErr(rec{"op": "read", "path": "q.md"}).text, `No note`)
	forkMust(t, os.Remove(f1Session.getSessionFile()))
	forkMatch(t, f2.notesErr(rec{"op": "read", "path": "m.md"}).text, `No note`)
}

// Two sessions of one id that started at different times are two sessions.
func TestNoteMutation_sessionKeyIncludesTheStartTime(t *testing.T) {
	w := newForkWorld(t)
	cwd := w.project("project")
	a := w.session(cwd)
	b := w.session(cwd)
	b.mu.Lock()
	b.id = a.sessionID()
	b.header["id"] = a.sessionID()
	b.header["timestamp"] = "2027-03-04T05:06:07.000Z"
	b.mu.Unlock()
	f, _ := newProbeSide(t, cwd, a)
	first := nsCall(t, f, rec{"op": "get"})
	second := f.With(withSession(b)).Run("nsprobe", rec{"op": "get"}).Text()
	if first == second {
		t.Fatalf("one store entry served two sessions: %q", first)
	}
}

// Date.parse of a timestamp the local parser leaves to the host decides a binding's validity.
func TestNoteMutation_nonCanonicalOwnerTimestampsAreDecidedByTheHost(t *testing.T) {
	w := newForkWorld(t)
	cwd := w.project("project")
	f, _ := newProbeSide(t, cwd, w.session(cwd))
	// V8's answer, as the host gives it: no time value for text that is not a date.
	f.Set(withHostAnswer("date.parse", func(args map[string]any) (any, string) {
		if args["text"] == "yesterday" {
			return rec{"ms": nil}, ""
		}
		return rec{"ms": 1767323045678.0}, ""
	}))
	if got := nsCall(t, f, rec{"op": "parse", "id": "yesterday"}); got != "rejected" {
		t.Fatalf("an unparseable timestamp: %s", got)
	}
	if got := nsCall(t, f, rec{"op": "parse", "id": "2026-01-02T03:04:05.678+00:00"}); got != "accepted" {
		t.Fatalf("a parseable non-canonical timestamp: %s", got)
	}
}
