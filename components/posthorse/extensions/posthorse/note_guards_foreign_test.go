package posthorse

// Guard behaviour in a fork session, with the exact words the model reads: foreign reads and edits,
// managed metadata, the private copy an edit of an unowned note makes, and the guards' error paths.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// foreignWorld is a project session (parent) and a fork of it (child) taken before reference.md existed.
type foreignWorld struct {
	cwd, notes, childDir, shared string
	parent, child                *forkSide
}

func newForeignWorld(t *testing.T) *foreignWorld {
	t.Helper()
	w := newForkWorld(t)
	cwd := w.project("project")
	parent := newForkSide(t, cwd, w.session(cwd))
	parent.mustStart()
	parent.notes(rec{"op": "write", "path": "selected.md", "content": "selected checkpoint"})
	forkPoint := forkLeaf(parent.sess)
	parent.notes(rec{"op": "write", "path": "reference.md", "content": "original reference"})
	childSession := w.fork(parent.sess, cwd)
	childSession.branch(forkPoint)
	child := newForkSide(t, cwd, childSession)
	child.mustStart()
	selected := child.notes(rec{"op": "read", "path": "selected.md"})
	notes := filepath.Join(cwd, ".pi", "notes")
	return &foreignWorld{cwd: cwd, notes: notes, childDir: filepath.Dir(forkFilePath(t, selected)), shared: filepath.Join(notes, "reference.md"), parent: parent, child: child}
}

// guardRaw fires an event at the first handler of its kind and returns the handler's failure text.
func guardRaw(f *fixture, event string, data rec) string {
	host, leave := f.enter()
	defer leave()
	h := host.h
	h.mu.Lock()
	id := h.allHandlers[event][0]
	h.mu.Unlock()
	_, failure := fxStartRequest(h, fxEventRequest(event, id, data)).wait()
	return failure
}

func TestNoteGuardsForeignAndManagedTexts(t *testing.T) {
	w := newForeignWorld(t)
	foreign := func(own string) string {
		return "The foreign note was not changed. This session writes " + own + ". Retry explicitly there; creation semantics were not changed."
	}
	// The parent never knew the child's scope: its edit is a foreign write, its read a redirect.
	childNote := filepath.Join(w.childDir, "selected.md")
	if got := w.parent.begin("p-edit", "edit", rec{"path": childNote}, nil); got != foreign(filepath.Join(w.notes, "selected.md")) {
		t.Errorf("a parent edit of the child's note: %q", got)
	}
	redirect := "This is another notes scope. Use notes read with " + jsonObject2("scope", w.childDir, "path", "selected.md") + " for a labeled read-only comparison, or omit scope to read this session’s copy."
	if got := w.parent.begin("p-read", "read", rec{"path": childNote}, nil); got != redirect {
		t.Errorf("a parent read of the child's note: %q", got)
	}
	// Managed metadata cannot be written through file tools but may be read.
	manifest := filepath.Join(filepath.Dir(w.childDir), "manifest.json")
	managed := "Managed notes metadata is not editable through file tools: " + manifest + ". Use notes for this session’s checkpoint."
	if got := w.parent.begin("p-manifest", "write", rec{"path": manifest, "content": "x"}, nil); got != managed {
		t.Errorf("a write of the manifest: %q", got)
	}
	dirText := "Managed notes metadata is not editable through file tools: " + w.childDir + ". Use notes for this session’s checkpoint."
	if got := w.parent.begin("p-dir", "write", rec{"path": w.childDir, "content": "x"}, nil); got != dirText {
		t.Errorf("a write of another scope's notes directory itself: %q", got)
	}
	if got := w.parent.begin("p-manifest-read", "read", rec{"path": manifest}, nil); got != "" {
		t.Errorf("a read of the manifest was blocked: %q", got)
	}
	// A native write of a foreign note is not an existing edit: nothing is copied.
	if got := w.child.begin("c-write", "write", rec{"path": w.shared, "content": "x"}, nil); got != foreign(filepath.Join(w.childDir, "reference.md")) {
		t.Errorf("a native write of a foreign note: %q", got)
	}
	if _, err := os.Stat(filepath.Join(w.childDir, "reference.md")); err == nil {
		t.Error("a native write made a private copy")
	}
	// The notes tool is not a file tool: a path it was given is never guarded.
	if got := w.child.begin("c-notes", "notes", rec{"op": "read", "path": w.shared}, nil); got != "" {
		t.Errorf("the notes tool was guarded: %q", got)
	}
}

func TestNoteGuardsCopyAnUnownedPathOnItsFirstEdit(t *testing.T) {
	w := newForeignWorld(t)
	own := filepath.Join(w.childDir, "reference.md")
	want := "Private copy created at " + own + "; no requested edit ran. Read this copy, then retry the edit."
	if got := w.child.begin("c-edit", "edit", rec{"path": own}, nil); got != want {
		t.Fatalf("first edit of an unowned path: %q, want %q", got, want)
	}
	if text, err := os.ReadFile(own); err != nil || string(text) != "original reference" {
		t.Fatalf("private copy: %q, %v", text, err)
	}
	// The copy is made under the file mutation queue of that path.
	entered := false
	// sdk.WithFileMutationQueue rides the first running extension's connection: the parent's host here.
	for _, line := range append(w.parent.f.MutationLog(), w.child.f.MutationLog()...) {
		if line == "enter "+own {
			entered = true
		}
	}
	if !entered {
		t.Errorf("no queue turn for %s: %v", own, w.parent.f.MutationLog())
	}
	// Now it is owned: the same edit may run.
	if got := w.child.begin("c-edit-2", "edit", rec{"path": own}, nil); got != "" {
		t.Errorf("the second edit was blocked: %q", got)
	}
}

func TestNoteGuardsAndNotesPrepareTheManifest(t *testing.T) {
	w := newForeignWorld(t)
	manifest := filepath.Join(filepath.Dir(w.childDir), "manifest.json")
	restore := func(name string) {
		t.Helper()
		if _, err := os.Stat(manifest); err != nil {
			t.Fatalf("%s: the manifest was not written: %v", name, err)
		}
		forkMust(t, os.Remove(manifest))
	}
	forkMust(t, os.Remove(manifest))
	if got := w.child.begin("c-native", "write", rec{"path": filepath.Join(w.childDir, "n.md"), "content": "x"}, nil); got != "" {
		t.Fatalf("native write blocked: %q", got)
	}
	restore("a native write of an own note")
	// A native read does not write anything.
	if got := w.child.begin("c-read", "read", rec{"path": filepath.Join(w.childDir, "selected.md")}, nil); got != "" {
		t.Fatalf("native read blocked: %q", got)
	}
	if _, err := os.Stat(manifest); err == nil {
		t.Fatal("a native read wrote the manifest")
	}
	w.child.notes(rec{"op": "write", "path": "w.md", "content": "x"})
	restore("notes write")
	w.child.notes(rec{"op": "append", "path": "w.md", "content": "y"})
	restore("notes append")
}

// A folder read lists the scope it was read from; a search takes a scope selector and labels notes
// once more than the current scope is searched.
func TestNotesFolderReadAndSearchScopesInAFork(t *testing.T) {
	w := newForeignWorld(t)
	w.child.notes(rec{"op": "write", "path": "sub/own.md", "content": "needle in the fork"})
	w.parent.notes(rec{"op": "write", "path": "sub/shared.md", "content": "needle in the project"})
	folder := w.child.notes(rec{"op": "read", "path": "sub"})
	listed := w.child.notes(rec{"op": "list", "path": "sub", "scope": w.childDir})
	if folder != listed || !strings.HasPrefix(folder, "Notes directory: "+w.childDir+"\nsub/own.md  ") || strings.Contains(folder, "[") {
		t.Errorf("a folder read:\n%q\nlist of the scope:\n%q", folder, listed)
	}
	scoped := w.child.notes(rec{"op": "search", "query": "needle", "scope": w.childDir})
	if scoped != "sub/own.md:1: needle in the fork" {
		t.Errorf("a search of one scope: %q", scoped)
	}
	all := w.child.notes(rec{"op": "search", "query": "needle"})
	if !regexp.MustCompile(`(?m)^sub/own\.md \[current; read \{.*\}\]:1: needle in the fork$`).MatchString(all) ||
		!regexp.MustCompile(`(?m)^sub/shared\.md \[read-only; read \{.*\}\]:1: needle in the project$`).MatchString(all) {
		t.Errorf("a search of every scope: %q", all)
	}
}

// After a failed initialization the guards still protect the notes roots they learned.
func TestNoteGuardsAfterAFailedInitialization(t *testing.T) {
	w := newForkWorld(t)
	cwd := w.project("project")
	parent := newForkSide(t, cwd, w.session(cwd))
	parent.mustStart()
	parent.notes(rec{"op": "write", "path": "task.md", "content": "parent checkpoint"})
	source := filepath.Join(cwd, ".pi", "notes", "task.md")
	forkMust(t, os.Remove(source))
	child := newForkSide(t, cwd, w.fork(parent.sess, cwd))
	if failure := child.start(); !strings.Contains(failure, "Incomplete notes snapshot") {
		t.Fatalf("initialization: %q", failure)
	}
	call := func(tool string, input rec) string {
		return guardRaw(child.f, "tool_call", rec{"toolName": tool, "toolCallId": "c-" + tool, "input": input})
	}
	// The notes tool reports the failure; a file tool does only for a path inside a notes root.
	if failure := call("notes", rec{"op": "list"}); !strings.Contains(failure, "Incomplete notes snapshot") {
		t.Errorf("notes: %q", failure)
	}
	if failure := call("read", rec{"path": source}); !strings.Contains(failure, "Incomplete notes snapshot") {
		t.Errorf("a read inside the notes root: %q", failure)
	}
	if failure := call("write_files", rec{"files": []any{rec{"path": source, "content": "x"}}}); !strings.Contains(failure, "Incomplete notes snapshot") {
		t.Errorf("a write inside the notes root: %q", failure)
	}
	if failure := call("read", rec{"path": filepath.Join(cwd, "src", "main.go")}); failure != "" {
		t.Errorf("a read outside every notes root failed: %q", failure)
	}
	// A path that cannot be parsed or resolved is the guard's own failure, not the initialization's.
	if failure := call("read", rec{"path": "file://host/x"}); !strings.Contains(failure, `File URL host must be "localhost" or empty`) {
		t.Errorf("a bad file URL: %q", failure)
	}
	loop := filepath.Join(cwd, "loop")
	forkMust(t, os.Symlink(loop, loop))
	if failure := call("read", rec{"path": loop}); !strings.Contains(failure, "ELOOP") {
		t.Errorf("a symlink loop: %q", failure)
	}
}

// With a working session the guards' own failures are the handler's failures.
func TestNoteGuardsReportTheirOwnFailures(t *testing.T) {
	w := newForkWorld(t)
	cwd := w.project("project")
	s := newForkSide(t, cwd, w.session(cwd))
	s.mustStart()
	if failure := guardRaw(s.f, "tool_call", rec{"toolName": "read", "toolCallId": "bad-url", "input": rec{"path": "file://host/x"}}); !strings.Contains(failure, `File URL host must be "localhost" or empty`) {
		t.Errorf("a bad file URL in tool_call: %q", failure)
	}
	// A touch that cannot be appended fails the tool_result hook, as does the notes tool itself.
	failing := s.f.With(withHostError("appendEntry", "append refused"))
	path := filepath.Join(cwd, ".pi", "notes", "f.md")
	guardRaw(s.f, "tool_call", rec{"toolName": "write", "toolCallId": "tw", "input": rec{"path": path, "content": "x"}})
	event := rec{"toolName": "write", "toolCallId": "tw", "input": rec{"path": path, "content": "x"}, "isError": false, "details": nil}
	if failure := guardRaw(failing, "tool_result", event); !strings.Contains(failure, "append refused") {
		t.Errorf("tool_result with a refused append: %q", failure)
	}
	// A late tool_result of a call that has ended records nothing; the notes tool's own results neither.
	before := len(notesTouchEntries(s.f))
	guardRaw(s.f, "tool_result", event)
	if len(notesTouchEntries(s.f)) != before+1 {
		t.Fatal("the running call's tool_result was not recorded")
	}
	s.f.FireAll("tool_execution_end", rec{"toolCallId": "tw", "toolName": "write"})
	guardRaw(s.f, "tool_result", event)
	notesEvent := rec{"toolName": "notes", "toolCallId": "tw", "input": rec{"op": "write", "path": path, "content": "x"}, "isError": false}
	guardRaw(s.f, "tool_call", rec{"toolName": "notes", "toolCallId": "tn", "input": notesEvent["input"]})
	notesEvent["toolCallId"] = "tn"
	guardRaw(s.f, "tool_result", notesEvent)
	if got := len(notesTouchEntries(s.f)); got != before+1 {
		t.Errorf("%d touch entries, want %d: an ended call or the notes tool was recorded", got, before+1)
	}
	// A relative path in a result is resolved against the call's directory.
	rel := filepath.Join(".pi", "notes", "rel.md")
	guardRaw(s.f, "tool_call", rec{"toolName": "write", "toolCallId": "tr", "input": rec{"path": rel, "content": "x"}})
	guardRaw(s.f, "tool_result", rec{"toolName": "write", "toolCallId": "tr", "input": rec{"path": rel, "content": "x"}, "isError": false})
	if got := guardTouches(s, before+1); len(got) != 1 || got[0] != "write rel.md" {
		t.Errorf("relative path recorded %v", got)
	}
}

// The guards' failures while preparing, adopting or resolving a scope are the handler's failures.
func TestNoteGuardsReportFailuresOfPreparingAdoptingAndResolving(t *testing.T) {
	w := newForeignWorld(t)
	own := filepath.Join(w.childDir, "reference.md")
	// The source changes right after the copy: the adoption fails its verification.
	noteAfterCopy = func(src, _ string) {
		if src == w.shared {
			_ = os.WriteFile(w.shared, []byte("source changed after the copy"), 0o644)
		}
	}
	failure := guardRaw(w.child.f, "tool_call", rec{"toolName": "edit", "toolCallId": "c-adopt", "input": rec{"path": own}})
	noteAfterCopy = nil
	forkMatch(t, failure, `Private copy created.*verification failed.*Read the private copy`)
	// A manifest that is not this session's is preserved: no note may be written next to it.
	manifest := filepath.Join(filepath.Dir(w.childDir), "manifest.json")
	forkMust(t, os.WriteFile(manifest, []byte("{}"), 0o644))
	if failure := guardRaw(w.child.f, "tool_call", rec{"toolName": "write", "toolCallId": "c-prep", "input": rec{"path": filepath.Join(w.childDir, "n.md"), "content": "x"}}); !strings.Contains(failure, "Preserving unexpected notes manifest at "+manifest) {
		t.Errorf("an unexpected manifest: %q", failure)
	}
	// A path in an unmanaged fork directory names no scope the session can select.
	bogus := filepath.Join(w.notes, ".forks", "bogus", "notes", "a.md")
	if failure := guardRaw(w.parent.f, "tool_call", rec{"toolName": "read", "toolCallId": "p-bogus", "input": rec{"path": bogus}}); !strings.Contains(failure, "Unknown notes scope "+filepath.Dir(bogus)) {
		t.Errorf("an unmanaged fork directory: %q", failure)
	}
	// A symlink loop cannot be resolved.
	loop := filepath.Join(w.cwd, "loop")
	forkMust(t, os.Symlink(loop, loop))
	if failure := guardRaw(w.parent.f, "tool_call", rec{"toolName": "read", "toolCallId": "p-loop", "input": rec{"path": loop}}); !strings.Contains(failure, "ELOOP") {
		t.Errorf("a symlink loop: %q", failure)
	}
}
