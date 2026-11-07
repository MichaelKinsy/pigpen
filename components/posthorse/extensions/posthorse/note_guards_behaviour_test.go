package posthorse

// Behaviour tests of note_guards.go found missing by the mutation check: what each file tool's
// result records, the call tree of nested calls, and the exact read-only header.

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// guardCall runs one file-tool call of the model through the guard hooks (tool_call, tool_result,
// tool_execution_end) on side s: blocked is the reason of a block, "" when the call may run.
func guardCall(s *forkSide, id, tool string, input rec, isError bool, details any, extra rec) (blocked string) {
	s.t.Helper()
	if reason := s.begin(id, tool, input, extra); reason != "" {
		return reason
	}
	event := rec{"toolName": tool, "toolCallId": id, "input": input, "isError": isError, "details": details}
	for k, v := range extra {
		event[k] = v
	}
	s.f.Fire("tool_result", event)
	s.f.FireAll("tool_execution_end", rec{"toolCallId": id, "toolName": tool})
	return ""
}

// guardTouches is every file touch recorded after the first `from` entries, as "kind path".
func guardTouches(s *forkSide, from int) []string {
	var out []string
	for _, touch := range notesTouchEntries(s.f)[from:] {
		for _, file := range touch["files"].([]any) {
			entry := file.(map[string]any)
			out = append(out, entry["kind"].(string)+" "+entry["path"].(string))
		}
	}
	return out
}

func TestNoteGuardsRecordWhatEachFileToolDid(t *testing.T) {
	w := newForkWorld(t)
	cwd := w.project("project")
	s := newForkSide(t, cwd, w.session(cwd))
	s.mustStart()
	notes := filepath.Join(cwd, ".pi", "notes")
	own := func(name string) string { return filepath.Join(notes, name) }
	patch := func(lines ...string) rec {
		return rec{"input": "*** Begin Patch\n" + strings.Join(lines, "\n") + "\n*** End Patch"}
	}
	elsewhere := filepath.Join(cwd, "src", "main.go") // not a note: never recorded
	cases := []struct {
		name    string
		tool    string
		input   rec
		isError bool
		details any
		want    []string
	}{
		{"native read", "read", rec{"path": own("a.md")}, false, nil, []string{"read a.md"}},
		{"native write", "write", rec{"path": own("a.md"), "content": "x"}, false, nil, []string{"write a.md"}},
		{"native clear", "write", rec{"path": own("a.md"), "content": ""}, false, nil, []string{"clear a.md"}},
		{"native edit", "edit", rec{"path": own("a.md")}, false, nil, []string{"write a.md"}},
		{"native edit failed", "edit", rec{"path": own("a.md")}, true, nil, nil},
		{"not a note", "write", rec{"path": elsewhere, "content": "x"}, false, nil, nil},
		{"write_files overwrite", "write_files", rec{"files": []any{rec{"path": own("a.md"), "content": "x"}}}, false, nil, []string{"write a.md"}},
		{"write_files clear", "write_files", rec{"files": []any{rec{"path": own("a.md"), "content": ""}}}, false, nil, []string{"clear a.md"}},
		{"write_files create", "write_files", rec{"files": []any{rec{"path": own("n.md"), "mode": "create", "content": "x"}}}, false, nil, []string{"write n.md"}},
		{"write_files create empty", "write_files", rec{"files": []any{rec{"path": own("n.md"), "mode": "create", "content": ""}}}, false, nil, []string{"clear n.md"}},
		{"replace_text", "replace_text", rec{"files": []any{rec{"path": own("a.md")}, rec{"path": elsewhere}}}, false, nil, []string{"write a.md"}},
		{"apply_patch add and update", "apply_patch", patch("*** Add File: "+own("n.md"), "+x", "*** Update File: "+own("a.md"), "@@"), false, nil, []string{"write n.md", "write a.md"}},
		{"apply_patch delete", "apply_patch", patch("*** Delete File: " + own("a.md")), false, nil, []string{"delete a.md"}},
		{"apply_patch move", "apply_patch", patch("*** Update File: "+own("a.md"), "*** Move to: "+own("b.md")), false, nil, []string{"delete a.md", "write b.md"}},
		{"failed patch keeps committed paths", "apply_patch", patch("*** Update File: "+own("a.md"), "@@", "*** Update File: "+own("b.md"), "@@"), true,
			rec{"modifiedFiles": []any{own("a.md")}}, []string{"write a.md"}},
		{"receipt clear", "write_files", rec{"files": []any{rec{"path": own("a.md"), "content": "x"}}}, false,
			rec{"modifiedFiles": []any{own("a.md")}, "files": []any{rec{"path": own("a.md"), "bytesAfter": 0.0}}}, []string{"clear a.md"}},
		{"receipt delete", "write_files", rec{"files": []any{rec{"path": own("a.md"), "content": "x"}}}, false,
			rec{"modifiedFiles": []any{own("a.md")}, "files": []any{rec{"path": own("a.md"), "operation": "delete"}}}, []string{"delete a.md"}},
		{"preview", "write_files", rec{"files": []any{rec{"path": own("a.md"), "content": "x"}}}, false, rec{"preview": true}, nil},
		{"preview input", "write_files", rec{"preview": true, "files": []any{rec{"path": own("a.md"), "content": "x"}}}, false, nil, nil},
		{"failed write_files without receipt", "write_files", rec{"files": []any{rec{"path": own("a.md"), "content": "x"}}}, true, nil, nil},
	}
	for i, c := range cases {
		before := len(notesTouchEntries(s.f))
		id := "call-" + strings.ReplaceAll(c.name, " ", "-")
		if reason := guardCall(s, id, c.tool, c.input, c.isError, c.details, nil); reason != "" {
			t.Fatalf("%d %s was blocked: %q", i, c.name, reason)
		}
		if got := guardTouches(s, before); !reflect.DeepEqual(got, c.want) && !(len(got) == 0 && len(c.want) == 0) {
			t.Errorf("%s recorded %v, want %v", c.name, got, c.want)
		}
	}
	// The notes tool records itself: the tool_result hook never double-records it.
	before := len(notesTouchEntries(s.f))
	s.notes(rec{"op": "write", "path": "self.md", "content": "x"})
	if got := guardTouches(s, before); !reflect.DeepEqual(got, []string{"write self.md"}) {
		t.Errorf("notes write recorded %v", got)
	}
}

// A nested call belongs to its parent's tree until it ends; a call that names an ended call as
// its parent starts a tree of its own under that id (note-store.ts startCall/finishCall).
func TestNoteGuardsCallTreeOfNestedCalls(t *testing.T) {
	w := newForkWorld(t)
	cwd := w.project("project")
	s := newForkSide(t, cwd, w.session(cwd))
	s.mustStart()
	path := filepath.Join(cwd, ".pi", "notes", "t.md")
	root := func(id string) string {
		touches := notesTouchEntries(s.f)
		for i := len(touches) - 1; i >= 0; i-- {
			if touches[i]["toolCallId"] == id {
				return touches[i]["rootToolCallId"].(string)
			}
		}
		t.Fatalf("no touch of %s", id)
		return ""
	}
	s.f.Fire("tool_call", rec{"toolName": "codemode", "toolCallId": "parent", "input": rec{"code": "…"}})
	if reason := guardCall(s, "child", "write", rec{"path": path, "content": "x"}, false, nil, rec{"parentToolCallId": "parent"}); reason != "" {
		t.Fatal(reason)
	}
	if got := root("child"); got != "parent" {
		t.Errorf("a nested call roots at %q, want parent", got)
	}
	// "child" has ended: a call under it is rooted at "child", not at "parent".
	if reason := guardCall(s, "grandchild", "write", rec{"path": path, "content": "y"}, false, nil, rec{"parentToolCallId": "child"}); reason != "" {
		t.Fatal(reason)
	}
	if got := root("grandchild"); got != "child" {
		t.Errorf("a call under an ended call roots at %q, want child", got)
	}
	// A call that is still running keeps its root for later children.
	s.f.Fire("tool_call", rec{"toolName": "write", "toolCallId": "running", "input": rec{"path": path, "content": "z"}, "parentToolCallId": "parent"})
	if reason := guardCall(s, "sibling", "write", rec{"path": path, "content": "w"}, false, nil, rec{"parentToolCallId": "running"}); reason != "" {
		t.Fatal(reason)
	}
	if got := root("sibling"); got != "parent" {
		t.Errorf("a call under a running nested call roots at %q, want parent", got)
	}
}

// A fork's read-only reference says exactly how to read it (notes.ts:124-126), and a foreign read
// through a file tool is redirected with the same selector.
func TestNotesReadOnlyReferenceHeaderIsExact(t *testing.T) {
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
	got := child.notes(rec{"op": "read", "path": "reference.md"})
	want := "Read-only live reference: notes read " + jsQuoteObject(cwd) + "\noriginal reference"
	if got != want {
		t.Fatalf("reference read:\n got %q\nwant %q", got, want)
	}
}
