package posthorse

// Layer-1 tests of note_guards.go and note_tool_effects.go beyond the twinned upstream case:
// toolPath, the batch tools against a foreign note, verified receipts.

import (
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestNoteGuardsToolPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cwd := filepath.Join(t.TempDir(), "work")
	cases := []struct {
		tool, path, want string
	}{
		// Native core tools expand @ and ~ and unicode spaces (note-guards.ts:30-41).
		{"read", "@notes/a.md", filepath.Join(cwd, "notes", "a.md")},
		{"edit", "~/x.md", filepath.Join(home, "x.md")},
		{"write", "~", home},
		{"read", "a\u00a0b\u2003c.md", filepath.Join(cwd, "a b c.md")},
		{"write", "../up.md", filepath.Join(filepath.Dir(cwd), "up.md")},
		{"read", "file://" + filepath.ToSlash(filepath.Join(cwd, "u%20v.md")), filepath.Join(cwd, "u v.md")},
		// pi-apply-edits treats them literally (note-guards.ts:26-29).
		{"apply_patch", "@notes/a.md", cwd + "/@notes/a.md"},
		{"write_files", "~/x.md", cwd + "/~/x.md"},
		{"replace_text", "a/../b.md", cwd + "/a/../b.md"},
	}
	for _, c := range cases {
		if filepath.Separator == '\\' {
			break // the literal branch resolves on Windows (note-guards.ts:26-28); the table is POSIX
		}
		got, err := toolPath(c.tool, c.path, cwd)
		if err != nil || got != c.want {
			t.Errorf("toolPath(%q, %q) = %q, %v; want %q", c.tool, c.path, got, err, c.want)
		}
	}
	if filepath.Separator != '\\' {
		abs := filepath.Join(t.TempDir(), "n.md")
		if got, err := toolPath("apply_patch", abs, cwd); err != nil || got != abs {
			t.Errorf("an absolute literal path is kept: %q, %v", got, err)
		}
		if _, err := toolPath("read", "file://example.com/x", cwd); err == nil {
			t.Error("a file URL with a remote host must fail like fileURLToPath")
		}
		if _, err := toolPath("read", "file:///a%2Fb", cwd); err == nil {
			t.Error("an encoded slash in a file URL must fail like fileURLToPath")
		}
	}
}

// TestNoteGuardsBatchToolsProtectForeignNotes: replace_text, write_files and apply_patch
// calls on a note another scope owns copy it once, then redirect or refuse with the exact text of
// note-guards.ts:79-95 and 96-127.
func TestNoteGuardsBatchToolsProtectForeignNotes(t *testing.T) {
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
	childDirectory := filepath.Dir(forkFilePath(t, selected))
	forkMatch(t, child.notes(rec{"op": "read", "path": "reference.md"}), `^Read-only live reference:`)
	shared := filepath.Join(cwd, ".pi", "notes", "reference.md")
	copied := filepath.Join(childDirectory, "reference.md")

	blocked := func(tool string, args rec) string {
		t.Helper()
		reason := child.begin(child.nextID(tool), tool, args, nil)
		if reason == "" {
			t.Fatalf("%s %v was not blocked", tool, args)
		}
		return reason
	}
	// The first existing-note edit creates the private copy (note-store.ts guardReference) ...
	want := "Private copy created at " + copied + "; the source was not edited. Read the private copy, then retry the edit on that path."
	if got := blocked("replace_text", rec{"files": []any{rec{"path": shared, "edits": []any{}}}}); got != want {
		t.Fatalf("replace_text: %q, want %q", got, want)
	}
	if got := forkRead(t, copied); got != "original reference" {
		t.Fatalf("private copy = %q", got)
	}
	// ... later ones find it already there.
	own := "This session writes only " + childDirectory + ". Read " + copied + ", then retry there; the foreign path was not edited."
	for name, call := range map[string]struct {
		tool string
		args rec
	}{
		"write_files overwrite": {"write_files", rec{"files": []any{rec{"path": shared, "content": "x"}}}},
		"write_files clear":     {"write_files", rec{"files": []any{rec{"path": shared, "content": ""}}}},
		"apply_patch update":    {"apply_patch", rec{"input": "*** Begin Patch\n*** Update File: " + shared + "\n@@\n-a\n+b\n*** End Patch"}},
		"apply_patch delete":    {"apply_patch", rec{"input": "*** Begin Patch\n*** Delete File: " + shared + "\n*** End Patch"}},
		"apply_patch move":      {"apply_patch", rec{"input": "*** Begin Patch\n*** Update File: " + shared + "\n*** Move to: " + shared + ".moved\n*** End Patch"}},
	} {
		if got := blocked(call.tool, call.args); got != own {
			t.Errorf("%s: %q, want %q", name, got, own)
		}
	}
	// Creating a foreign path is not an existing edit: nothing is copied and the receipt says so.
	created := "The foreign note was not changed. This session writes " + copied + ". Retry explicitly there; creation semantics were not changed."
	for name, call := range map[string]struct {
		tool string
		args rec
	}{
		"write_files create": {"write_files", rec{"files": []any{rec{"path": shared, "mode": "create", "content": "x"}}}},
		"apply_patch add":    {"apply_patch", rec{"input": "*** Begin Patch\n*** Add File: " + shared + "\n+x\n*** End Patch"}},
	} {
		if got := blocked(call.tool, call.args); got != created {
			t.Errorf("%s: %q, want %q", name, got, created)
		}
	}
	// A native read of the foreign note redirects to a labelled notes read (note-guards.ts:86-88).
	redirect := "This is another notes scope. Use notes read with " + jsQuoteObject(cwd) + " for a labeled read-only comparison, or omit scope to read this session’s copy."
	if got := blocked("read", rec{"path": shared}); got != redirect {
		t.Fatalf("read: %q, want %q", got, redirect)
	}
	// A preview never writes, so it is never blocked (note-tool-effects.ts:99-101).
	for _, args := range []rec{
		{"preview": true, "files": []any{rec{"path": shared, "content": "x"}}},
	} {
		if reason := child.begin(child.nextID("write_files"), "write_files", args, nil); reason != "" {
			t.Errorf("a preview was blocked: %q", reason)
		}
	}
	if got := forkRead(t, shared); got != "original reference" {
		t.Fatalf("the source changed: %q", got)
	}
}

// jsQuoteObject is the labelled-read selector the redirect prints for the project scope.
func jsQuoteObject(cwd string) string {
	return `{"scope":` + jsQuote(filepath.Join(cwd, ".pi", "notes")) + `,"path":"reference.md"}`
}

func TestVerifiedNoteOperations(t *testing.T) {
	source, destination := "/n/source.md", "/n/copied.md"
	move := "*** Begin Patch\n*** Update File: " + source + "\n*** Move to: " + destination + "\n@@\n-old\n+new\n*** End Patch"
	receipt := rec{
		"modifiedFiles": []any{destination},
		"files": []any{
			rec{"path": source, "moveTo": destination, "operation": "move", "status": "failed"},
			rec{"path": "/n/uncertain.md", "operation": "edit", "status": "uncertain"},
		},
	}
	cases := []struct {
		name   string
		tool   string
		input  any
		result noteResult
		want   []noteOperation
	}{
		{"a failed move keeps only the committed destination", "apply_patch", rec{"input": move}, noteResult{isError: true, details: receipt},
			[]noteOperation{{path: destination, action: "create"}}},
		{"a move reported for the path itself is a delete", "apply_patch", rec{"input": move},
			noteResult{details: rec{"modifiedFiles": []any{source}, "files": []any{rec{"path": source, "moveTo": destination, "operation": "move"}}}},
			[]noteOperation{{path: source, action: "delete"}}},
		{"bytesAfter 0 is a clear", "write_files", rec{"files": []any{rec{"path": "a.md", "content": "x"}}},
			noteResult{details: rec{"modifiedFiles": []any{"a.md"}, "files": []any{rec{"path": "a.md", "bytesAfter": 0.0}}}},
			[]noteOperation{{path: "a.md", action: "clear"}}},
		{"an unknown modified path defaults to edit", "replace_text", rec{"files": []any{}},
			noteResult{details: rec{"modifiedFiles": []any{"b.md", 3.0}}},
			[]noteOperation{{path: "b.md", action: "edit"}}},
		{"an empty modifiedFiles list verifies nothing", "write_files", rec{"files": []any{rec{"path": "a.md", "content": "x"}}},
			noteResult{details: rec{"modifiedFiles": []any{}}}, []noteOperation{}},
		{"a preview verifies nothing", "write_files", rec{"files": []any{rec{"path": "a.md", "content": "x"}}},
			noteResult{details: rec{"preview": true, "modifiedFiles": []any{"a.md"}}}, []noteOperation{}},
		{"without a receipt an error verifies nothing", "write", rec{"path": "a.md", "content": "x"},
			noteResult{isError: true}, []noteOperation{}},
		{"without a receipt success keeps the requested operations", "write", rec{"path": "a.md", "content": ""},
			noteResult{}, []noteOperation{{path: "a.md", action: "clear"}}},
		{"notes keeps its scope", "notes", rec{"op": "read", "path": "a.md", "scope": "/s"},
			noteResult{}, []noteOperation{{path: "a.md", action: "read", scope: "/s", hasScope: true}}},
	}
	for _, c := range cases {
		if got := verifiedNoteOperations(c.tool, c.input, c.result); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
	for action, kind := range map[string]string{
		"read": "read", "write": "full-write", "create": "full-write", "edit": "partial-write", "append": "partial-write",
		"clear": "clear", "create-empty": "clear", "delete": "clear",
	} {
		if got := noteOperationKind(action); got != kind {
			t.Errorf("noteOperationKind(%q) = %q, want %q", action, got, kind)
		}
	}
	for name, want := range map[string]string{"server:read": "read", "ns/edit": "edit", "a:b/c": "c", "plain": "plain", "": ""} {
		if got := bareToolName(name); got != want {
			t.Errorf("bareToolName(%q) = %q, want %q", name, got, want)
		}
	}
	for _, input := range []any{"*** Update File: a\n", rec{"input": 3}, nil} {
		if got := noteOperations("preview_patch", input); len(got) != 0 {
			t.Errorf("preview_patch: %v", got)
		}
	}
	if got := noteOperations("apply_patch", "*** Add File: x\u2028*** Delete File: y\n"); !reflect.DeepEqual(got, []noteOperation{{path: "x\u2028*** Delete File: y", action: "create"}}) {
		// [^\r\n]+ swallows U+2028, so the second header does not start a match of its own.
		t.Errorf("U+2028 inside a header path: %+v", got)
	}
	if got := noteOperations("apply_patch", "x\u2028*** Add File: a\n"); len(got) != 1 {
		// A multiline `^` also matches after U+2028.
		t.Errorf("a header after U+2028: %+v", got)
	}
}

// referenceMatchingLines is notes.ts:202-214 written the plain way, without the ASCII paths.
func referenceMatchingLines(name, text, query string) []string {
	if !strings.Contains(jsToLowerCase(text), query) {
		return []string{}
	}
	rows := []string{}
	for index, line := range strings.Split(text, "\n") {
		trimmed := jsTrim(line)
		if match := u16index(jsToLowerCase(trimmed), query, 0); match != -1 {
			rows = append(rows, name+":"+strconv.Itoa(index+1)+": "+excerptAround(trimmed, match, 50, 200))
		}
	}
	return rows
}

// TestMatchingLinesAgainstReference: the ASCII shortcuts of matchingLines change nothing.
func TestMatchingLinesAgainstReference(t *testing.T) {
	texts := []string{
		"", "needle", "a Needle here\nno\n  NEEDLE\t\n\r\nlast needle",
		"ünïcode Needle\nplain needle\nİstanbul needle\nΣΑΣ needle ΟΔΟΣ",
		"K needle (Kelvin sign) \u212a\nneedle\u00a0\n\u00a0 needle \u2028 x",
		strings.Repeat("x", 300) + " needle " + strings.Repeat("y", 300),
		"emoji 😀 needle 😀\nsecond\r\nthird needle\r",
		"line\n\n\nneedle\n",
	}
	queries := []string{"needle", "n", "e\n", "ünïcode", "i̇stanbul", "σας", "k needle", "😀 needle", "x needle y", "zzz", " "}
	for _, text := range texts {
		for _, query := range queries {
			query = jsToLowerCase(query)
			got, want := matchingLines("n.md", text, query), referenceMatchingLines("n.md", text, query)
			if len(got) == 0 && len(want) == 0 {
				continue
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("text %q query %q:\n got %q\nwant %q", text, query, got, want)
			}
		}
	}
}
