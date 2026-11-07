package posthorse

// The notes store (note-store.ts) through a probe extension on the fixture: the probe owns a createNotesStore() and
// exposes get/read/adopt/record/selectScope... as one tool, so every call crosses the fake host like the real
// extension's calls do (session reads, appendEntry, file mutation queue).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

type nsProbe struct {
	store *notesStore
	mu    sync.Mutex
	got   *sessionNotes
}

func (p *nsProbe) extension(policyReader) *sdk.Extension {
	e := sdk.New("notes-store-probe")
	text := func(s string) (any, error) { return sdk.ToolResult{Content: s}, nil }
	e.Tool("nsprobe", "Notes store probe", sdk.Schema{
		"type": "object",
		"properties": map[string]any{
			"op": map[string]any{"type": "string"}, "path": map[string]any{"type": "string"},
			"scope": map[string]any{"type": "string"}, "id": map[string]any{"type": "string"},
			"kind": map[string]any{"type": "string"},
		},
		"required": []any{"op"},
	}, func(ctx sdk.Context, params map[string]any) (any, error) {
		op, _ := params["op"].(string)
		path, _ := params["path"].(string)
		id, _ := params["id"].(string)
		scope, hasScope := params["scope"].(string)
		if op == "roots" {
			return text(strings.Join(p.store.roots(), "\n"))
		}
		if op == "parse" {
			b := parseBinding(rec{"version": 1.0, "kind": "project", "owner": rec{"id": "s1", "timestamp": id}, "root": "/r", "scope": ""})
			return text(map[bool]string{true: "accepted", false: "rejected"}[b != nil])
		}
		if op == "load" {
			root, _ := params["root"].(string)
			b, _, created, err := loadNotesBinding(ctx, root)
			if err != nil {
				return nil, err
			}
			return text(fmt.Sprint(b.kind, " ", b.root, " ", created, " ", len(b.seeds)))
		}
		if op == "recovery" {
			r, err := p.store.recovery(ctx)
			if err != nil {
				return nil, err
			}
			if r == nil {
				return text("undefined")
			}
			return text("ready " + r.binding.kind)
		}
		s, err := p.store.get(ctx)
		if err != nil {
			return nil, err
		}
		p.mu.Lock()
		if p.got != nil && p.got != s {
			p.mu.Unlock()
			return text("different session object")
		}
		p.got = s
		p.mu.Unlock()
		call := s.call(id)
		switch op {
		case "get":
			return text(s.binding.kind + " " + s.binding.scope + " " + s.directory())
		case "same":
			return text("ok")
		case "owns":
			owns, err := s.owns(path)
			if err != nil {
				return nil, err
			}
			return text(map[bool]string{true: "owned", false: "not owned"}[owns])
		case "read":
			var sel *string
			if hasScope {
				sel = &scope
			}
			r, err := s.read(ctx, path, sel)
			if err != nil {
				return nil, err
			}
			out, _ := json.Marshal(map[string]any{"address": r.address, "path": r.path, "writable": r.writable})
			return text(string(out))
		case "adopt":
			return func() (any, error) {
				result, err := s.adopt(ctx, path, call)
				if err != nil {
					return nil, err
				}
				return text(result)
			}()
		case "guard":
			reason, err := s.guardReference(ctx, path, call)
			if err != nil {
				return nil, err
			}
			return text(reason)
		case "select":
			sc, err := s.selectScope(ctx, scope)
			if err != nil {
				return nil, err
			}
			return text(sc.root + "|" + sc.scope)
		case "record":
			kind, _ := params["kind"].(string)
			file := noteTouchFile{noteAddress: noteAddress{noteScope{root: s.binding.root, scope: s.binding.scope}, path}, kind: kind}
			if err := s.record(ctx, []noteTouchFile{file}, call); err != nil {
				return nil, err
			}
			rev, err := s.revision(filepath.Join(s.directory(), path))
			if err != nil {
				return nil, err
			}
			return text(map[bool]string{true: "owned", false: "not owned"}[func() bool { o, _ := s.owns(path); return o }()] + " rev " + string(rune('0'+rev)))
		case "scopes":
			lines := make([]string, len(s.scopes))
			for i, sc := range s.scopes {
				lines[i] = sc.root + "|" + sc.scope
			}
			return text(strings.Join(lines, "\n"))
		case "revision":
			rev, err := s.revision(path)
			if err != nil {
				return nil, err
			}
			return text(fmt.Sprint(rev))
		case "recordnone":
			if err := s.record(ctx, nil, call); err != nil {
				return nil, err
			}
			return text("ok")
		case "recordforeign":
			kind, _ := params["kind"].(string)
			file := noteTouchFile{noteAddress: noteAddress{noteScope{root: s.binding.root}, path}, kind: kind}
			if err := s.record(ctx, []noteTouchFile{file}, call); err != nil {
				return nil, err
			}
			owns, _ := s.owns(path)
			return text(map[bool]string{true: "owned", false: "not owned"}[owns])
		case "save":
			digest, _ := params["digest"].(string)
			seed := noteSeed{path: path, source: noteAddress{noteScope{root: s.binding.root}, path}, lineage: noteAddress{noteScope{root: s.binding.root}, path}, digest: digest, anchors: []string{}}
			if err := saveAdoption(s.binding, seed); err != nil {
				return nil, err
			}
			data, err := os.ReadFile(filepath.Join(filepath.Dir(s.directory()), "manifest.json"))
			if err != nil {
				return nil, err
			}
			return text(string(data))
		case "prepare":
			return text("ok")
		}
		return nil, os.ErrInvalid
	})
	return e
}

func newProbeSide(t *testing.T, cwd string, sess *fakeSession) (*fixture, *nsProbe) {
	t.Helper()
	probe := &nsProbe{store: createNotesStore()}
	f := newFixture(t, withStartCwd(cwd), withSession(sess), withRealProjection(), withAppendToSession(), withFactory(probe.extension))
	return f, probe
}

func nsCall(t *testing.T, f *fixture, params rec) string {
	t.Helper()
	return f.Run("nsprobe", params).Text()
}

func bindingEntries(sess *fakeSession) []rec {
	var out []rec
	for _, e := range sess.getEntries() {
		if e["customType"] == notesBindingType {
			out = append(out, e)
		}
	}
	return out
}

func touchEntries(sess *fakeSession) []rec {
	var out []rec
	for _, e := range sess.getEntries() {
		if e["customType"] == notesTouchType {
			out = append(out, e)
		}
	}
	return out
}

func TestNoteStore_projectSessionBindsOnceAndPersistsTheBinding(t *testing.T) {
	w := newForkWorld(t)
	cwd := w.project("project")
	sess := w.session(cwd)
	f, _ := newProbeSide(t, cwd, sess)
	if got := nsCall(t, f, rec{"op": "recovery"}); got != "undefined" {
		t.Fatalf("recovery before the session is ready: %s", got)
	}
	notes := filepath.Join(cwd, ".pi", "notes")
	if got := nsCall(t, f, rec{"op": "get"}); got != "project  "+notes {
		t.Fatalf("get = %q", got)
	}
	if got := nsCall(t, f, rec{"op": "get"}); got != "project  "+notes {
		t.Fatalf("second get = %q", got)
	}
	bindings := bindingEntries(sess)
	if len(bindings) != 1 {
		t.Fatalf("%d binding entries, want 1", len(bindings))
	}
	data := bindings[0]["data"].(map[string]any)
	if data["kind"] != "project" || data["version"] != 1.0 || data["root"] != notes || data["scope"] != "" {
		t.Fatalf("binding data %v", data)
	}
	if got := nsCall(t, f, rec{"op": "recovery"}); got != "ready project" {
		t.Fatalf("recovery = %s", got)
	}
	if got := nsCall(t, f, rec{"op": "roots"}); got != notes {
		t.Fatalf("roots = %q", got)
	}
	// A new store on the same session resumes the recorded binding without writing another.
	g, _ := newProbeSide(t, cwd, sess)
	nsCall(t, g, rec{"op": "get"})
	if n := len(bindingEntries(sess)); n != 1 {
		t.Fatalf("resume appended another binding: %d", n)
	}
}

func TestNoteStore_concurrentFirstCallsShareOneInitialization(t *testing.T) {
	w := newForkWorld(t)
	cwd := w.project("project")
	sess := w.session(cwd)
	f, _ := newProbeSide(t, cwd, sess)
	var wg sync.WaitGroup
	results := make([]string, 8)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, failure := f.RunErr("nsprobe", rec{"op": "same"})
			results[i] = res.Text() + failure
		}()
	}
	wg.Wait()
	for i, r := range results {
		if r != "ok" {
			t.Fatalf("call %d = %q (every caller must get the same session object)", i, r)
		}
	}
	if n := len(bindingEntries(sess)); n != 1 {
		t.Fatalf("%d bindings appended", n)
	}
}

func TestNoteStore_requiresASessionHeader(t *testing.T) {
	w := newForkWorld(t)
	cwd := w.project("project")
	sess := w.session(cwd)
	sess.header = nil
	f, _ := newProbeSide(t, cwd, sess)
	if _, failure := f.RunErr("nsprobe", rec{"op": "get"}); failure != "Pi session header is required for notes." {
		t.Fatalf("failure = %q", failure)
	}
}

func TestNoteStore_recordOwnsWritesNotReadsAndBumpsTheRevision(t *testing.T) {
	w := newForkWorld(t)
	cwd := w.project("project")
	parent := newForkSide(t, cwd, w.session(cwd))
	parent.mustStart()
	parent.notes(rec{"op": "write", "path": "task.md", "content": "parent"})
	child := w.fork(parent.sess, cwd)
	f, _ := newProbeSide(t, cwd, child)
	nsCall(t, f, rec{"op": "get"})
	if got := nsCall(t, f, rec{"op": "owns", "path": "task.md"}); got != "owned" {
		t.Fatalf("a snapshotted note: %s", got)
	}
	if got := nsCall(t, f, rec{"op": "owns", "path": "fresh.md"}); got != "not owned" {
		t.Fatalf("a note nobody wrote: %s", got)
	}
	if got := nsCall(t, f, rec{"op": "record", "path": "fresh.md", "kind": "read", "id": "c1"}); got != "not owned rev 0" {
		t.Fatalf("read touch: %s", got)
	}
	if got := nsCall(t, f, rec{"op": "record", "path": "fresh.md", "kind": "write", "id": "c2"}); got != "owned rev 1" {
		t.Fatalf("write touch: %s", got)
	}
	if got := nsCall(t, f, rec{"op": "record", "path": "fresh.md", "kind": "delete", "id": "c3"}); got != "owned rev 2" {
		t.Fatalf("delete touch: %s", got)
	}
	touches := touchEntries(child)
	// The parent's own write is inherited; the three records of this session follow it.
	if len(touches) != 4 {
		t.Fatalf("%d touch entries", len(touches))
	}
	data := touches[2]["data"].(map[string]any)
	if data["version"] != 1.0 || data["rootToolCallId"] != "c2" || data["toolCallId"] != "c2" {
		t.Fatalf("touch data %v", data)
	}
	files := data["files"].([]any)
	file := files[0].(map[string]any)
	if file["kind"] != "write" || file["path"] != "fresh.md" || file["scope"] == "" {
		t.Fatalf("touch file %v", file)
	}
	// Ownership survives a resume of the session: the journal carries the touches.
	resumed, _ := newProbeSide(t, cwd, w.open(child))
	nsCall(t, resumed, rec{"op": "get"})
	if got := nsCall(t, resumed, rec{"op": "owns", "path": "fresh.md"}); got != "owned" {
		t.Fatalf("after resume: %s", got)
	}
}

func TestNoteStore_callBookkeeping(t *testing.T) {
	s := &sessionNotes{calls: map[string]string{}}
	parent := "root"
	if c := s.startCall("root", nil); c != (noteCall{"root", "root"}) {
		t.Fatalf("root call %v", c)
	}
	if c := s.startCall("child", &parent); c != (noteCall{"child", "root"}) {
		t.Fatalf("child call %v", c)
	}
	grand := "child"
	if c := s.startCall("grand", &grand); c.rootId != "root" {
		t.Fatalf("grandchild root %v", c)
	}
	unknown := "never-started"
	if c := s.startCall("orphan", &unknown); c.rootId != "never-started" {
		t.Fatalf("a parent that never started is its own root: %v", c)
	}
	s.finishCall("child")
	if c := s.call("child"); c != (noteCall{"child", "child"}) {
		t.Fatalf("a finished call roots at itself: %v", c)
	}
	if c := s.call("grand"); c.rootId != "root" {
		t.Fatalf("grandchild lost its root: %v", c)
	}
	if c := s.call("never"); c != (noteCall{"never", "never"}) {
		t.Fatalf("unknown call %v", c)
	}
}

func TestNoteStore_selectScopeAndReadWithAScope(t *testing.T) {
	w := newForkWorld(t)
	cwd := w.project("project")
	parent := newForkSide(t, cwd, w.session(cwd))
	parent.mustStart()
	parent.notes(rec{"op": "write", "path": "task.md", "content": "parent"})
	sibling := newForkSide(t, cwd, w.fork(parent.sess, cwd))
	sibling.mustStart()
	sibling.notes(rec{"op": "read", "path": "task.md"})
	child := w.fork(parent.sess, cwd)
	f, _ := newProbeSide(t, cwd, child)
	nsCall(t, f, rec{"op": "get"})
	notes := filepath.Join(cwd, ".pi", "notes")
	// The source scope and the project scope are known; a sibling's private scope is known through its manifest.
	if got := nsCall(t, f, rec{"op": "select", "scope": notes}); got != notes+"|" {
		t.Fatalf("project scope = %q", got)
	}
	siblingDir := scopeDirectory(sibling.bindingOf(t))
	siblingScope, _ := forkScope(noteOwner{id: sibling.sess.sessionID(), timestamp: sibling.sess.getHeader()["timestamp"].(string)})
	if got := nsCall(t, f, rec{"op": "select", "scope": siblingDir}); got != notes+"|"+siblingScope {
		t.Fatalf("sibling scope = %q", got)
	}
	_, failure := f.RunErr("nsprobe", rec{"op": "select", "scope": filepath.Join(cwd, "nowhere")})
	if want := "Unknown notes scope " + filepath.Join(cwd, "nowhere") + ". Copy a scope selector from notes list/search."; failure != want {
		t.Fatalf("failure %q\nwant %q", failure, want)
	}
	// A manifest of another root is not selectable.
	foreign := filepath.Join(cwd, ".pi", "notes", ".forks", "x", "notes")
	forkMust(t, os.MkdirAll(foreign, 0o755))
	manifest := &noteBinding{kind: "fork", owner: noteOwner{"x", nsTimestamp}, root: filepath.Join(cwd, "other-root"), absent: []string{}}
	manifest.scope, _ = forkScope(manifest.owner)
	forkMust(t, os.WriteFile(filepath.Join(filepath.Dir(foreign), "manifest.json"), manifest.appendJSON(nil), 0o644))
	if _, failure := f.RunErr("nsprobe", rec{"op": "select", "scope": foreign}); !strings.HasPrefix(failure, "Unknown notes scope ") {
		t.Fatalf("foreign-root manifest selected: %q", failure)
	}
	// read with a scope is writable only for the session's own scope.
	var read struct {
		Path     string
		Writable bool
		Address  map[string]string
	}
	forkMust(t, json.Unmarshal([]byte(nsCall(t, f, rec{"op": "read", "path": "task.md", "scope": notes})), &read))
	if read.Path != filepath.Join(notes, "task.md") || read.Writable || read.Address["path"] != "task.md" {
		t.Fatalf("scoped read %+v", read)
	}
	forkMust(t, json.Unmarshal([]byte(nsCall(t, f, rec{"op": "read", "path": "task.md"})), &read))
	if !read.Writable || !strings.Contains(read.Path, ".forks") {
		t.Fatalf("own read %+v", read)
	}
}

func (s *forkSide) bindingOf(t *testing.T) *noteBinding {
	t.Helper()
	bindings := bindingsOf(s.sess.getEntries())
	if len(bindings) == 0 {
		t.Fatal("no binding recorded")
	}
	return bindings[len(bindings)-1]
}

func TestNoteStore_adoptAndGuardReference(t *testing.T) {
	w := newForkWorld(t)
	cwd := w.project("project")
	parent := newForkSide(t, cwd, w.session(cwd))
	parent.mustStart()
	parent.notes(rec{"op": "write", "path": "selected.md", "content": "selected"})
	forkPoint := forkLeaf(parent.sess)
	parent.notes(rec{"op": "write", "path": "later.md", "content": "later reference"})
	parent.notes(rec{"op": "write", "path": "second.md", "content": "second reference"})
	otherBranch := forkLeaf(parent.sess)
	childSession := w.fork(parent.sess, cwd)
	childSession.branch(forkPoint)
	f, _ := newProbeSide(t, cwd, childSession)
	nsCall(t, f, rec{"op": "get"})
	childSession.branch(otherBranch)
	notes := filepath.Join(cwd, ".pi", "notes")

	if got := nsCall(t, f, rec{"op": "adopt", "path": "selected.md", "id": "a0"}); got != "owned" {
		t.Fatalf("owned note: %s", got)
	}
	if got := nsCall(t, f, rec{"op": "adopt", "path": "missing.md", "id": "a1"}); got != "unavailable" {
		t.Fatalf("missing note: %s", got)
	}
	if got := nsCall(t, f, rec{"op": "adopt", "path": "later.md", "id": "a2"}); got != "adopted" {
		t.Fatalf("reference: %s", got)
	}
	all := bindingsOf(childSession.getEntries())
	directory := scopeDirectory(all[len(all)-1])
	if got := forkRead(t, filepath.Join(directory, "later.md")); got != "later reference" {
		t.Fatalf("private copy %q", got)
	}
	// The adoption is journaled with its seed and saved in the manifest.
	touches := touchEntries(childSession)
	data := touches[len(touches)-1]["data"].(map[string]any)
	file := data["files"].([]any)[0].(map[string]any)
	seed, _ := file["seed"].(map[string]any)
	if file["kind"] != "adopt" || file["path"] != "later.md" || seed == nil || seed["digest"] == "" {
		t.Fatalf("adopt touch %v", data)
	}
	manifest := forkRead(t, filepath.Join(filepath.Dir(directory), "manifest.json"))
	if !strings.Contains(manifest, `"path":"later.md"`) {
		t.Fatalf("manifest lacks the adoption: %s", manifest)
	}
	if got := nsCall(t, f, rec{"op": "adopt", "path": "later.md", "id": "a3"}); got != "owned" {
		t.Fatalf("adopted twice: %s", got)
	}
	// guardReference: copies and explains, or explains.
	reason := nsCall(t, f, rec{"op": "guard", "path": "second.md", "id": "g1"})
	want := "Private copy created at " + filepath.Join(directory, "second.md") + "; the source was not edited. Read the private copy, then retry the edit on that path."
	if reason != want {
		t.Fatalf("guard reason %q\nwant %q", reason, want)
	}
	reason = nsCall(t, f, rec{"op": "guard", "path": "second.md", "id": "g2"})
	want = "This session writes only " + directory + ". Read " + filepath.Join(directory, "second.md") + ", then retry there; the foreign path was not edited."
	if reason != want {
		t.Fatalf("second guard reason %q\nwant %q", reason, want)
	}
	if got := forkRead(t, filepath.Join(notes, "second.md")); got != "second reference" {
		t.Fatalf("source changed: %q", got)
	}
	// Failed verification leaves the copy and says so, once; the copy is then owned.
	noteAfterCopy = func(src, _ string) { _ = os.WriteFile(src, []byte("changed"), 0o644) }
	forkMust(t, os.WriteFile(filepath.Join(notes, "third.md"), []byte("third reference"), 0o644))
	_, failure := f.RunErr("nsprobe", rec{"op": "adopt", "path": "third.md", "id": "a4"})
	noteAfterCopy = nil
	if want := "Private copy created at " + filepath.Join(directory, "third.md") + "; source not edited, but copy verification failed. Read the private copy before retrying."; failure != want {
		t.Fatalf("failure %q\nwant %q", failure, want)
	}
	if got := nsCall(t, f, rec{"op": "adopt", "path": "third.md", "id": "a5"}); got != "owned" {
		t.Fatalf("after a failed verification: %s", got)
	}
}
