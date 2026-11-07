package posthorse

// Cases of note_tool_effects.go and node_paths.go that the mutation check found unpinned. The URL and
// path values are those of Node 24.22.1 (fileURLToPath, path.win32.resolve, path.posix.normalize).

import (
	"reflect"
	"testing"
)

func TestNoteOperationsParseOnlyWholeHeaders(t *testing.T) {
	patch := func(body string) string { return "*** Begin Patch\n" + body + "\n*** End Patch" }
	for _, c := range []struct {
		name  string
		input any
		want  []noteOperation
	}{
		{"no space after the stars", patch("***Update File: a"), []noteOperation{}},
		{"another character instead of the space", patch("***XUpdate File: a"), []noteOperation{}},
		{"no space after the colon", patch("*** Update File:a"), []noteOperation{}},
		{"another character instead of the space after the colon", patch("*** Update File:Xa"), []noteOperation{}},
		{"an empty path", patch("*** Update File: "), []noteOperation{}},
		{"header after U+2028", "x\u2028*** Add File: a\n", []noteOperation{{path: "a", action: "create"}}},
		{"header after U+2029", "x\u2029*** Add File: a\n", []noteOperation{{path: "a", action: "create"}}},
		{"header after a bare CR", "x\r*** Add File: a\n", []noteOperation{{path: "a", action: "create"}}},
		{"a move with no update before it", "\n*** Move to: x", []noteOperation{}},
		{"a patch that is not a string", rec{"input": 5.0}, []noteOperation{}},
		{"no input at all", rec{}, []noteOperation{}},
		{"two moves after one update", patch("*** Update File: a\n*** Move to: b\n*** Move to: c"), []noteOperation{{path: "a", action: "delete"}, {path: "b", action: "create"}}},
	} {
		got := noteOperations("apply_patch", c.input)
		if r, ok := c.input.(rec); ok {
			got = noteOperations("apply_patch", r)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestNoteOperationsOfEachTool(t *testing.T) {
	for _, c := range []struct {
		name  string
		tool  string
		input any
		want  []noteOperation
	}{
		{"batch skips entries without a path", "write_files", rec{"files": []any{5.0, rec{"content": "x"}, rec{"path": 3.0}, rec{"path": "a"}}}, []noteOperation{{path: "a", action: "write"}}},
		{"replace_text is always an edit", "replace_text", rec{"files": []any{rec{"path": "a", "content": ""}}}, []noteOperation{{path: "a", action: "edit"}}},
		{"notes write", "notes", rec{"op": "write", "path": "a", "content": "x", "scope": "/s"}, []noteOperation{{path: "a", action: "write", scope: "/s", hasScope: true}}},
		{"notes clear", "notes", rec{"op": "write", "path": "a", "content": ""}, []noteOperation{{path: "a", action: "clear"}}},
		{"notes append", "notes", rec{"op": "append", "path": "a", "content": "x"}, []noteOperation{{path: "a", action: "append"}}},
		{"notes read", "notes", rec{"op": "read", "path": "a"}, []noteOperation{{path: "a", action: "read"}}},
		{"notes list has no note", "notes", rec{"op": "list", "path": "a"}, []noteOperation{}},
		{"no path", "write", rec{"content": "x"}, []noteOperation{}},
		{"notes with no path", "notes", rec{"op": "read"}, []noteOperation{}},
		{"native read", "read", rec{"path": "a"}, []noteOperation{{path: "a", action: "read"}}},
		{"native edit", "edit", rec{"path": "a"}, []noteOperation{{path: "a", action: "edit"}}},
		{"a preview flag", "replace_text", rec{"preview": true, "files": []any{rec{"path": "a"}}}, []noteOperation{}},
	} {
		if got := noteOperations(c.tool, c.input); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestVerifiedNoteOperationsReceipts(t *testing.T) {
	write := rec{"files": []any{rec{"path": "/n/a.md", "content": "x"}}}
	for _, c := range []struct {
		name    string
		tool    string
		input   any
		details any
		want    []noteOperation
	}{
		{"a file moved away is deleted", "write_files", write,
			rec{"modifiedFiles": []any{"/n/a.md"}, "files": []any{rec{"path": "/n/a.md", "moveTo": "/n/b.md", "operation": "move"}}},
			[]noteOperation{{path: "/n/a.md", action: "delete"}}},
		{"a file moved to is found by its destination and cleared", "write_files", write,
			rec{"modifiedFiles": []any{"/n/b.md"}, "files": []any{rec{"path": "/n/a.md", "moveTo": "/n/b.md", "bytesAfter": 0.0}}},
			[]noteOperation{{path: "/n/b.md", action: "clear"}}},
		{"a receipt without the list of modified files keeps the operations", "apply_patch", rec{"input": "*** Begin Patch\n*** Add File: /n/a.md\n+x\n*** End Patch"},
			rec{"other": 1.0}, []noteOperation{{path: "/n/a.md", action: "create"}}},
		{"an empty file entry keeps the requested action", "write_files", write,
			rec{"modifiedFiles": []any{"/n/a.md"}, "files": []any{rec{"path": "/n/a.md"}}}, []noteOperation{{path: "/n/a.md", action: "write"}}},
	} {
		if got := verifiedNoteOperations(c.tool, c.input, noteResult{details: c.details}); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestNormalizeStringAndDecodeEdges(t *testing.T) {
	// path.posix.normalize values with a relative path (the root may be left).
	for in, want := range map[string]string{"a/../../b": "../b", "../../a": "../../a", "./a/./b/..": "a", "a/..": ""} {
		if got := normalizeString(in, true, '/', isPosixSep); got != want {
			t.Errorf("normalizeString(%q, true) = %q, want %q", in, got, want)
		}
	}
	if got := normalizeString("/../a", false, '/', isPosixSep); got != "a" {
		t.Errorf("normalizeString(/../a, false) = %q", got)
	}
	// decodeURIComponent("%4") throws; a truncated escape must not read past the string.
	if _, err := decodeURIComponentJS("%4"); err == nil || err.Error() != "URI malformed" {
		t.Errorf("decode %%4: %v", err)
	}
	if _, err := decodeURIComponentJS("ab%"); err == nil {
		t.Error("decode ab%")
	}
	for in, want := range map[string]string{"file:///a/%2e/b": "/a/b", "file:///a/%2E/b": "/a/b", "file:///a/./b": "/a/b", "file:///a/.%2e/b": "/b", "file:///a/%2e%2E/b": "/b"} {
		if got, err := fileURLToPathOS(in, false); err != nil || got != want {
			t.Errorf("fileURLToPath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	// Drive letters compare without case: c: and C: are one device.
	for in, want := range map[string]string{"c:x": `c:\w\x`, "c:\\x": `c:\x`} {
		if got := win32Resolve(`C:\w`, in); got != want {
			t.Errorf("win32Resolve(C:\\w, %q) = %q, want %q", in, got, want)
		}
	}
}

func TestNodeJoinAndUppercaseScheme(t *testing.T) {
	for _, c := range []struct{ a, b, want string }{
		{"/h/./u", "/a/../x", "/h/u/x"},
		{"", "", "."},
		{"", "/x", "/x"},
		{"/h", "", "/h"},
		{"/h//u/", "x", "/h/u/x"},
	} {
		if got := nodeJoin2(c.a, c.b); got != c.want {
			t.Errorf("nodeJoin2(%q, %q) = %q, want %q", c.a, c.b, got, c.want)
		}
	}
	// A URL scheme is case-insensitive.
	if got, err := fileURLToPathOS("FILE:///a", false); err != nil || got != "/a" {
		t.Errorf("FILE:///a = %q, %v", got, err)
	}
}
