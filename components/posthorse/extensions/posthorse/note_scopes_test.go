package posthorse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Tables for the TypeBox schemas of note-scopes.ts:9-62 (owner, scope, address, identity, seed,
// binding, touch) and the helpers around them. Acceptance and rejection follow typebox 1.3's
// `Check`: object schemas accept extra properties and any non-array object, require every declared
// key, `Type.Number()` is Number.isFinite, `Type.Literal` is equality, `Type.Union` is anyOf.

const nsTimestamp = "2026-01-02T03:04:05.678Z"
const nsForkScope = ".forks/2026-01-02T03-04-05-678Z_s1/notes"

func nsOwner() rec { return rec{"id": "s1", "timestamp": nsTimestamp} }

func nsAddress(root, scope, path string) rec {
	return rec{"root": root, "scope": scope, "path": path}
}

func nsIdentity() rec {
	return rec{"size": 3.0, "mtimeMs": 1700000000123.5, "ctimeMs": 1700000000124.0, "ino": 42.0, "dev": 7.0}
}

func nsSeed() rec {
	return rec{
		"path":     "a.md",
		"source":   nsAddress("/p/.pi/notes", "", "a.md"),
		"lineage":  nsAddress("/p/.pi/notes", "", "a.md"),
		"digest":   "abc",
		"identity": nsIdentity(),
		"anchors":  []any{"e1", "e2"},
	}
}

func nsProject() rec {
	return rec{"version": 1.0, "kind": "project", "owner": nsOwner(), "root": "/p/.pi/notes", "scope": ""}
}

func nsFork() rec {
	return rec{
		"version": 1.0, "kind": "fork", "owner": nsOwner(), "root": "/p/.pi/notes", "scope": nsForkScope,
		"source": rec{"root": "/p/.pi/notes", "scope": ""}, "seeds": []any{nsSeed()}, "absent": []any{"gone.md"},
	}
}

func nsWith(base rec, edit func(m rec)) rec {
	out := rec{}
	for k, v := range base {
		out[k] = v
	}
	edit(out)
	return out
}

func nsWithSeed(edit func(m rec)) rec {
	return nsWith(nsFork(), func(m rec) { m["seeds"] = []any{nsWith(nsSeed(), edit)} })
}

func TestNoteScopes_parseBindingTable(t *testing.T) {
	cases := []struct {
		name  string
		value any
		ok    bool
	}{
		{"project", nsProject(), true},
		{"project extra properties", nsWith(nsProject(), func(m rec) { m["extra"] = "x"; m["source"] = nil }), true},
		{"project int version", nsWith(nsProject(), func(m rec) { m["version"] = 1 }), true},
		{"fork", nsFork(), true},
		{"fork null source", nsWith(nsFork(), func(m rec) { m["source"] = nil }), true},
		{"fork no seeds no absent", nsWith(nsFork(), func(m rec) { m["seeds"] = []any{}; m["absent"] = []any{} }), true},
		{"fork extra on nested objects", nsWith(nsFork(), func(m rec) {
			m["owner"] = nsWith(nsOwner(), func(o rec) { o["x"] = 1.0 })
			m["source"] = rec{"root": "/p/.pi/notes", "scope": "", "extra": true}
		}), true},
		{"fork source private scope", nsWith(nsFork(), func(m rec) {
			m["source"] = rec{"root": "/p/.pi/notes", "scope": ".forks/x/notes"}
		}), true},
		{"fork seed extra identity property", nsWithSeed(func(s rec) { s["identity"] = nsWith(nsIdentity(), func(i rec) { i["birth"] = 1.0 }) }), true},
		{"fork seed []string anchors", nsWithSeed(func(s rec) { s["anchors"] = []string{"a"} }), true},
		{"fork seed empty anchors", nsWithSeed(func(s rec) { s["anchors"] = []any{} }), true},
		{"fork seed with leaked project binding in source", nsWithSeed(func(s rec) {
			s["source"] = nsWith(nsProject(), func(m rec) { m["path"] = "a.md" })
		}), true},

		{"nil", nil, false},
		{"string", "binding", false},
		{"array", []any{nsProject()}, false},
		{"empty object", rec{}, false},
		{"missing version", nsWith(nsProject(), func(m rec) { delete(m, "version") }), false},
		{"version 2", nsWith(nsProject(), func(m rec) { m["version"] = 2.0 }), false},
		{"version string", nsWith(nsProject(), func(m rec) { m["version"] = "1" }), false},
		{"version 1.5", nsWith(nsProject(), func(m rec) { m["version"] = 1.5 }), false},
		{"missing kind", nsWith(nsProject(), func(m rec) { delete(m, "kind") }), false},
		{"unknown kind", nsWith(nsProject(), func(m rec) { m["kind"] = "global" }), false},
		{"missing owner", nsWith(nsProject(), func(m rec) { delete(m, "owner") }), false},
		{"owner not object", nsWith(nsProject(), func(m rec) { m["owner"] = "s1" }), false},
		{"owner id number", nsWith(nsProject(), func(m rec) { m["owner"] = rec{"id": 1.0, "timestamp": nsTimestamp} }), false},
		{"owner missing timestamp", nsWith(nsProject(), func(m rec) { m["owner"] = rec{"id": "s1"} }), false},
		{"owner empty id", nsWith(nsProject(), func(m rec) { m["owner"] = rec{"id": "", "timestamp": nsTimestamp} }), false},
		{"owner timestamp not a date", nsWith(nsProject(), func(m rec) { m["owner"] = rec{"id": "s1", "timestamp": "yesterday"} }), false},
		{"owner timestamp empty", nsWith(nsProject(), func(m rec) { m["owner"] = rec{"id": "s1", "timestamp": ""} }), false},
		{"root relative", nsWith(nsProject(), func(m rec) { m["root"] = "p/.pi/notes" }), false},
		{"root number", nsWith(nsProject(), func(m rec) { m["root"] = 1.0 }), false},
		{"missing root", nsWith(nsProject(), func(m rec) { delete(m, "root") }), false},
		{"project private scope", nsWith(nsProject(), func(m rec) { m["scope"] = nsForkScope }), false},
		{"project null scope", nsWith(nsProject(), func(m rec) { m["scope"] = nil }), false},
		{"fork scope not the owner's", nsWith(nsFork(), func(m rec) { m["scope"] = ".forks/other/notes" }), false},
		{"fork empty scope", nsWith(nsFork(), func(m rec) { m["scope"] = "" }), false},
		{"fork missing source key", nsWith(nsFork(), func(m rec) { delete(m, "source") }), false},
		{"fork source string", nsWith(nsFork(), func(m rec) { m["source"] = "x" }), false},
		{"fork source relative root", nsWith(nsFork(), func(m rec) { m["source"] = rec{"root": "p", "scope": ""} }), false},
		{"fork source bad scope", nsWith(nsFork(), func(m rec) { m["source"] = rec{"root": "/p", "scope": ".forks/a/b/notes"} }), false},
		{"fork source scope with backslash", nsWith(nsFork(), func(m rec) { m["source"] = rec{"root": "/p", "scope": ".forks/a\\b/notes"} }), false},
		{"fork source scope empty id", nsWith(nsFork(), func(m rec) { m["source"] = rec{"root": "/p", "scope": ".forks//notes"} }), false},
		{"fork source scope trailing text", nsWith(nsFork(), func(m rec) { m["source"] = rec{"root": "/p", "scope": ".forks/a/notes/"} }), false},
		{"fork seeds missing", nsWith(nsFork(), func(m rec) { delete(m, "seeds") }), false},
		{"fork seeds object", nsWith(nsFork(), func(m rec) { m["seeds"] = rec{} }), false},
		{"fork seed not object", nsWith(nsFork(), func(m rec) { m["seeds"] = []any{"a.md"} }), false},
		{"fork absent missing", nsWith(nsFork(), func(m rec) { delete(m, "absent") }), false},
		{"fork absent number", nsWith(nsFork(), func(m rec) { m["absent"] = []any{1.0} }), false},
		{"fork absent not normalized", nsWith(nsFork(), func(m rec) { m["absent"] = []any{"./a.md"} }), false},
		{"fork absent parent segment", nsWith(nsFork(), func(m rec) { m["absent"] = []any{"a/../b.md"} }), false},
		{"fork absent managed path", nsWith(nsFork(), func(m rec) { m["absent"] = []any{".forks/x"} }), false},
		{"fork absent absolute", nsWith(nsFork(), func(m rec) { m["absent"] = []any{"/etc/passwd"} }), false},
		{"fork absent empty", nsWith(nsFork(), func(m rec) { m["absent"] = []any{""} }), false},
		{"fork absent NUL", nsWith(nsFork(), func(m rec) { m["absent"] = []any{"a\x00b"} }), false},
		{"fork absent doubled slash", nsWith(nsFork(), func(m rec) { m["absent"] = []any{"a//b.md"} }), false},
		{"fork absent trailing slash", nsWith(nsFork(), func(m rec) { m["absent"] = []any{"a/"} }), false},
		{"fork absent nested ok", nsWith(nsFork(), func(m rec) { m["absent"] = []any{"a/b.md", "c d.md"} }), true},
		{"fork seed missing path", nsWithSeed(func(s rec) { delete(s, "path") }), false},
		{"fork seed path not normalized", nsWithSeed(func(s rec) { s["path"] = "x/./a.md" }), false},
		{"fork seed missing digest", nsWithSeed(func(s rec) { delete(s, "digest") }), false},
		{"fork seed digest number", nsWithSeed(func(s rec) { s["digest"] = 1.0 }), false},
		{"fork seed missing identity", nsWithSeed(func(s rec) { delete(s, "identity") }), false},
		{"fork seed identity string number", nsWithSeed(func(s rec) { s["identity"] = nsWith(nsIdentity(), func(i rec) { i["size"] = "3" }) }), false},
		{"fork seed identity missing dev", nsWithSeed(func(s rec) { s["identity"] = nsWith(nsIdentity(), func(i rec) { delete(i, "dev") }) }), false},
		{"fork seed identity null ino", nsWithSeed(func(s rec) { s["identity"] = nsWith(nsIdentity(), func(i rec) { i["ino"] = nil }) }), false},
		{"fork seed anchors missing", nsWithSeed(func(s rec) { delete(s, "anchors") }), false},
		{"fork seed anchors number", nsWithSeed(func(s rec) { s["anchors"] = []any{1.0} }), false},
		{"fork seed anchors string", nsWithSeed(func(s rec) { s["anchors"] = "e1" }), false},
		{"fork seed source missing path", nsWithSeed(func(s rec) { s["source"] = rec{"root": "/p/.pi/notes", "scope": ""} }), false},
		{"fork seed source relative root", nsWithSeed(func(s rec) { s["source"] = nsAddress("p", "", "a.md") }), false},
		{"fork seed source bad scope", nsWithSeed(func(s rec) { s["source"] = nsAddress("/p", "notes", "a.md") }), false},
		{"fork seed source path parent", nsWithSeed(func(s rec) { s["source"] = nsAddress("/p", "", "../a.md") }), false},
		{"fork seed lineage path not normalized", nsWithSeed(func(s rec) { s["lineage"] = nsAddress("/p", "", "b/../a.md") }), false},
		{"fork seed lineage private scope", nsWithSeed(func(s rec) { s["lineage"] = nsAddress("/p", ".forks/q/notes", "a.md") }), true},
		{"fork seed source managed path", nsWithSeed(func(s rec) { s["source"] = nsAddress("/p", "", ".forks/a/notes/a.md") }), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseBinding(c.value) != nil; got != c.ok {
				t.Fatalf("parseBinding accepted=%v, want %v", got, c.ok)
			}
		})
	}
}

func nsTouch(edit func(m rec)) rec {
	base := rec{
		"version": 1.0, "owner": nsOwner(), "scope": rec{"root": "/p/.pi/notes", "scope": nsForkScope},
		"rootToolCallId": "r1", "toolCallId": "c1",
		"files": []any{rec{"root": "/p/.pi/notes", "scope": nsForkScope, "path": "a.md", "kind": "write"}},
	}
	if edit != nil {
		edit(base)
	}
	return base
}

func nsTouchFile(edit func(f rec)) rec {
	f := rec{"root": "/p/.pi/notes", "scope": nsForkScope, "path": "a.md", "kind": "write"}
	edit(f)
	return nsTouch(func(m rec) { m["files"] = []any{f} })
}

func TestNoteScopes_touchRecordsTable(t *testing.T) {
	cases := []struct {
		name string
		data any
		ok   bool
	}{
		{"write", nsTouch(nil), true},
		{"empty files", nsTouch(func(m rec) { m["files"] = []any{} }), true},
		{"extra properties", nsTouch(func(m rec) { m["extra"] = 1.0 }), true},
		{"every kind", nsTouch(func(m rec) {
			var files []any
			for _, k := range []string{"read", "write", "clear", "delete", "adopt"} {
				files = append(files, rec{"root": "/p/.pi/notes", "scope": "", "path": k + ".md", "kind": k})
			}
			m["files"] = files
		}), true},
		{"with seed", nsTouchFile(func(f rec) { f["kind"] = "adopt"; f["seed"] = nsSeed() }), true},
		{"seed null", nsTouchFile(func(f rec) { f["seed"] = nil }), false},
		{"seed invalid", nsTouchFile(func(f rec) { f["seed"] = nsWith(nsSeed(), func(s rec) { delete(s, "digest") }) }), false},
		{"seed path not normalized", nsTouchFile(func(f rec) { f["seed"] = nsWith(nsSeed(), func(s rec) { s["path"] = "./a.md" }) }), false},
		{"unknown kind", nsTouchFile(func(f rec) { f["kind"] = "append" }), false},
		{"missing kind", nsTouchFile(func(f rec) { delete(f, "kind") }), false},
		{"file path parent", nsTouchFile(func(f rec) { f["path"] = "../a.md" }), false},
		{"file path absolute", nsTouchFile(func(f rec) { f["path"] = "/a.md" }), false},
		{"file bad scope", nsTouchFile(func(f rec) { f["scope"] = "notes" }), false},
		{"file relative root", nsTouchFile(func(f rec) { f["root"] = "p" }), false},
		{"file missing path", nsTouchFile(func(f rec) { delete(f, "path") }), false},
		{"file not object", nsTouch(func(m rec) { m["files"] = []any{"a.md"} }), false},
		{"files not array", nsTouch(func(m rec) { m["files"] = rec{} }), false},
		{"missing files", nsTouch(func(m rec) { delete(m, "files") }), false},
		{"version 2", nsTouch(func(m rec) { m["version"] = 2.0 }), false},
		{"missing version", nsTouch(func(m rec) { delete(m, "version") }), false},
		{"owner not object", nsTouch(func(m rec) { m["owner"] = nil }), false},
		{"scope relative root", nsTouch(func(m rec) { m["scope"] = rec{"root": "p", "scope": ""} }), false},
		{"scope missing", nsTouch(func(m rec) { delete(m, "scope") }), false},
		{"scope bad", nsTouch(func(m rec) { m["scope"] = rec{"root": "/p", "scope": "x"} }), false},
		{"missing rootToolCallId", nsTouch(func(m rec) { delete(m, "rootToolCallId") }), false},
		{"toolCallId number", nsTouch(func(m rec) { m["toolCallId"] = 1.0 }), false},
		{"array", []any{}, false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			entries := []rec{{"type": "custom", "customType": notesTouchType, "data": c.data}}
			if got := len(touchRecords(entries)) == 1; got != c.ok {
				t.Fatalf("touchRecords accepted=%v, want %v", got, c.ok)
			}
		})
	}
	t.Run("only custom entries of the touch type", func(t *testing.T) {
		for _, entry := range []rec{
			{"type": "message", "customType": notesTouchType, "data": nsTouch(nil)},
			{"type": "custom", "customType": "other", "data": nsTouch(nil)},
			{"type": "custom", "data": nsTouch(nil)},
		} {
			if got := touchRecords([]rec{entry}); len(got) != 0 {
				t.Fatalf("accepted %v", entry)
			}
		}
	})
}

func TestNoteScopes_bindingsOfFiltersEntries(t *testing.T) {
	entries := []rec{
		{"type": "custom", "customType": notesBindingType, "data": nsProject()},
		{"type": "custom", "customType": notesBindingType, "data": rec{"version": 2.0}},
		{"type": "message", "customType": notesBindingType, "data": nsProject()},
		{"type": "custom", "customType": "x", "data": nsProject()},
		{"type": "custom", "customType": notesBindingType, "data": nsFork()},
	}
	got := bindingsOf(entries)
	if len(got) != 2 || got[0].kind != "project" || got[1].kind != "fork" {
		t.Fatalf("bindingsOf = %#v", got)
	}
}

func TestNoteScopes_forkScope(t *testing.T) {
	cases := []struct{ id, timestamp, want string }{
		{"s1", nsTimestamp, nsForkScope},
		{"a b/c", "2020-02-29T23:59:59.999Z", ".forks/2020-02-29T23-59-59-999Z_a%20b%2Fc/notes"},
		{"é😀", "1970-01-01T00:00:00.000Z", ".forks/1970-01-01T00-00-00-000Z_%C3%A9%F0%9F%98%80/notes"},
		{"keep-_.!~*'()", "9999-12-31T23:59:59.999Z", ".forks/9999-12-31T23-59-59-999Z_keep-_.!~*'()/notes"},
	}
	for _, c := range cases {
		got, err := forkScope(noteOwner{c.id, c.timestamp})
		if err != nil || got != c.want {
			t.Errorf("forkScope(%q,%q) = %q, %v; want %q", c.id, c.timestamp, got, err, c.want)
		}
	}
	if _, err := forkScope(noteOwner{"s1", "nope"}); err == nil {
		t.Error("an invalid timestamp must fail like toISOString's RangeError")
	}
}

func TestNoteScopes_isoString(t *testing.T) {
	for ms, want := range map[float64]string{
		0:                  "1970-01-01T00:00:00.000Z",
		-1:                 "1969-12-31T23:59:59.999Z",
		951827696789:       "2000-02-29T12:34:56.789Z",
		253402300799999:    "9999-12-31T23:59:59.999Z",
		253402300800000:    "+010000-01-01T00:00:00.000Z",
		-62167219200000:    "0000-01-01T00:00:00.000Z",
		-62167219200001:    "-000001-12-31T23:59:59.999Z",
		-2203891200001:     "1900-02-28T23:59:59.999Z",
		1792866615042:      "2026-10-24T18:30:15.042Z",
		4107542400000:      "2100-03-01T00:00:00.000Z",
		-11670998400000:    "1600-02-29T00:00:00.000Z",
		1709164800000 + 17: "2024-02-29T00:00:00.017Z",
	} {
		if got := noteISOString(ms); got != want {
			t.Errorf("noteISOString(%v) = %q, want %q", ms, got, want)
		}
	}
}

func TestNoteScopes_noteRelative(t *testing.T) {
	ok := map[string]string{"a.md": "a.md", "a/b.md": "a/b.md", "./a.md": "a.md", "a//b.md": "a/b.md", "a/./b": "a/b", "a/": "a", "  sp ace.md": "  sp ace.md", ".hidden": ".hidden", ".forksx/a": ".forksx/a"}
	for in, want := range ok {
		got, err := noteRelative(filepath.FromSlash(in))
		if err != nil || filepath.ToSlash(got) != want {
			t.Errorf("noteRelative(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := []string{"", ".", "./", "/abs", "a/../b", "..", "../a", "a/..", "a\\..\\b", ".forks", ".forks/x", "./.forks/x", "a\x00b"}
	for _, in := range bad {
		if _, err := noteRelative(in); err == nil {
			t.Errorf("noteRelative(%q) accepted", in)
		}
	}
	if _, err := noteRelative(""); err == nil || err.Error() != `Invalid note path "": use a relative path.` {
		t.Errorf("empty path message: %v", err)
	}
	if _, err := noteRelative("../x"); err == nil || err.Error() != `Invalid note path "../x": must stay outside managed .forks paths and inside its scope.` {
		t.Errorf("escape message: %v", err)
	}
}

func TestNoteScopes_matchNoteDirectory(t *testing.T) {
	dirs := []string{"/p/notes", "/p/notes/deep", "/q/", `C:\r\notes`}
	cases := []struct {
		path, dir, rel string
		ok             bool
	}{
		{"/p/notes/a.md", "/p/notes", "a.md", true},
		{"/p/notes/deep/a.md", "/p/notes/deep", "a.md", true},
		{"/q/x/y.md", "/q/", "x/y.md", true},
		{`C:\r\notes\sub\z.md`, `C:\r\notes`, "sub/z.md", true},
		{"/p/notes", "", "", false},
		{"/p/notes/", "", "", false},
		{"/p/notes/.forks", "", "", false},
		{"/p/notes/.forks/x/notes/a.md", "", "", false},
		{"/p/notes/.forksy/a.md", "/p/notes", ".forksy/a.md", true},
		{"/elsewhere/a.md", "", "", false},
	}
	for _, c := range cases {
		dir, rel, ok := matchNoteDirectory(c.path, dirs)
		sd, sr, sok := matchSortedNoteDirectory(c.path, sortNoteDirectories(dirs))
		if sd != dir || sr != rel || sok != ok {
			t.Errorf("matchSortedNoteDirectory(%q) = %q,%q,%v; matchNoteDirectory = %q,%q,%v", c.path, sd, sr, sok, dir, rel, ok)
		}
		if dir != c.dir || rel != c.rel || ok != c.ok {
			t.Errorf("matchNoteDirectory(%q) = %q,%q,%v; want %q,%q,%v", c.path, dir, rel, ok, c.dir, c.rel, c.ok)
		}
	}
}

func TestNoteScopes_seedsOfAndOwnedPaths(t *testing.T) {
	binding := parseBinding(nsFork())
	if binding == nil {
		t.Fatal("fixture binding rejected")
	}
	other := rec{"id": "other", "timestamp": nsTimestamp}
	touch := func(owner rec, scope string, files ...rec) rec {
		fs := make([]any, len(files))
		for i, f := range files {
			fs[i] = f
		}
		return rec{"type": "custom", "customType": notesTouchType, "data": rec{
			"version": 1.0, "owner": owner, "scope": rec{"root": "/p/.pi/notes", "scope": scope},
			"rootToolCallId": "r", "toolCallId": "c", "files": fs,
		}}
	}
	file := func(scope, path, kind string, seed rec) rec {
		f := rec{"root": "/p/.pi/notes", "scope": scope, "path": path, "kind": kind}
		if seed != nil {
			f["seed"] = seed
		}
		return f
	}
	replaced := nsWith(nsSeed(), func(s rec) { s["digest"] = "new" })
	added := nsWith(nsSeed(), func(s rec) { s["path"] = "b.md"; s["digest"] = "added" })
	entries := []rec{
		touch(nsOwner(), nsForkScope, file(nsForkScope, "a.md", "adopt", replaced), file(nsForkScope, "b.md", "adopt", added), file(nsForkScope, "r.md", "read", nil), file(nsForkScope, "w.md", "write", nil)),
		touch(nsOwner(), "", file("", "elsewhere.md", "write", nil)),
		touch(other, nsForkScope, file(nsForkScope, "foreign.md", "write", nil), file(nsForkScope, "c.md", "adopt", nsWith(nsSeed(), func(s rec) { s["path"] = "c.md" }))),
	}
	seeds := seedsOf(binding, entries)
	if len(seeds) != 2 || seeds[0].path != "a.md" || seeds[0].digest != "new" || seeds[1].path != "b.md" || seeds[1].digest != "added" {
		t.Fatalf("seedsOf keeps first-seen order and last value per path: %#v", seeds)
	}
	owned := ownedPaths(binding, entries)
	for _, want := range []string{"gone.md", "a.md", "b.md", "w.md"} {
		if _, ok := owned[want]; !ok {
			t.Errorf("ownedPaths misses %s: %v", want, owned)
		}
	}
	for _, not := range []string{"r.md", "elsewhere.md", "foreign.md", "c.md"} {
		if _, ok := owned[not]; ok {
			t.Errorf("ownedPaths wrongly has %s", not)
		}
	}
	project := parseBinding(nsProject())
	// A project binding owns only what its own touches wrote in the project scope.
	if got := ownedPaths(project, entries); len(got) != 1 {
		t.Errorf("project ownedPaths = %v, want only elsewhere.md", got)
	} else if _, ok := got["elsewhere.md"]; !ok {
		t.Errorf("project ownedPaths = %v, want only elsewhere.md", got)
	}
}

func TestNoteScopes_jsonKeyOrder(t *testing.T) {
	binding := parseBinding(nsFork())
	got := string(binding.appendJSON(nil))
	want := `{"version":1,"kind":"fork","owner":{"id":"s1","timestamp":"2026-01-02T03:04:05.678Z"},"root":"/p/.pi/notes","scope":".forks/2026-01-02T03-04-05-678Z_s1/notes","source":{"root":"/p/.pi/notes","scope":""},"seeds":[{"path":"a.md","source":{"root":"/p/.pi/notes","scope":"","path":"a.md"},"lineage":{"root":"/p/.pi/notes","scope":"","path":"a.md"},"anchors":["e1","e2"],"digest":"abc","identity":{"size":3,"mtimeMs":1700000000123.5,"ctimeMs":1700000000124,"ino":42,"dev":7}}],"absent":["gone.md"]}`
	if got != want {
		t.Fatalf("binding JSON\n got %s\nwant %s", got, want)
	}
	project := string(parseBinding(nsProject()).appendJSON(nil))
	if project != `{"version":1,"kind":"project","owner":{"id":"s1","timestamp":"2026-01-02T03:04:05.678Z"},"root":"/p/.pi/notes","scope":""}` {
		t.Fatalf("project JSON %s", project)
	}
	record := noteTouchRecord{
		owner: noteOwner{"s1", nsTimestamp}, scope: noteScope{root: "/r", scope: ""}, rootToolCallId: "r", toolCallId: "c",
		files: []noteTouchFile{
			{noteAddress: noteAddress{noteScope{root: "/r"}, "a.md"}, kind: "write"},
			{noteAddress: noteAddress{noteScope{root: "/r"}, "b.md"}, kind: "adopt", seed: &binding.seeds[0]},
		},
	}
	gotTouch := string(record.appendJSON(nil))
	if !strings.HasPrefix(gotTouch, `{"version":1,"owner":{"id":"s1","timestamp":"2026-01-02T03:04:05.678Z"},"scope":{"root":"/r","scope":""},"rootToolCallId":"r","toolCallId":"c","files":[{"root":"/r","scope":"","path":"a.md","kind":"write"},{"root":"/r","scope":"","path":"b.md","kind":"adopt","seed":{"path":"a.md"`) {
		t.Fatalf("touch JSON %s", gotTouch)
	}
}

// A whole binding spread into an address (`{ ...binding, path }`, addressOf and resolveReference
// in the original) is written out in full, in the binding's key order, with path last.
func TestNoteScopes_leakedBindingInAddress(t *testing.T) {
	binding := parseBinding(nsProject())
	address := withPath(binding, "a.md")
	want := `{"version":1,"kind":"project","owner":{"id":"s1","timestamp":"2026-01-02T03:04:05.678Z"},"root":"/p/.pi/notes","scope":"","path":"a.md"}`
	if got := string(address.appendJSON(nil)); got != want {
		t.Fatalf("address JSON %s", got)
	}
	// The leak survives a parse of the written seed.
	seed := nsWith(nsSeed(), func(s rec) { s["source"] = rec{}; s["lineage"] = nsAddress("/p/.pi/notes", "", "a.md") })
	var decoded rec
	if err := nsUnmarshal(want, &decoded); err != nil {
		t.Fatal(err)
	}
	seed["source"] = decoded
	parsed, ok := checkSeed(seed)
	if !ok || parsed.source.leaked() == nil || parsed.source.leaked().kind != "project" {
		t.Fatalf("leaked binding not recovered: %#v %v", parsed.source, ok)
	}
	if got := string(parsed.source.appendJSON(nil)); got != want {
		t.Fatalf("round trip %s", got)
	}
	// A plain scope never leaks.
	plain := withPath(noteScope{root: "/r", scope: ""}, "a.md")
	if got := string(plain.appendJSON(nil)); got != `{"root":"/r","scope":"","path":"a.md"}` {
		t.Fatalf("plain %s", got)
	}
}

func TestNoteScopes_canonicalPathAndNotePath(t *testing.T) {
	root := nsRealTemp(t)
	notes := filepath.Join(root, "notes")
	if err := os.MkdirAll(filepath.Join(notes, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(notes, alias); err != nil {
		t.Skipf("no symlinks: %v", err)
	}
	// Missing tails resolve through their existing ancestors.
	got, err := canonicalPath(filepath.Join(alias, "sub", "new", "deeper.md"))
	if err != nil || got != filepath.Join(notes, "sub", "new", "deeper.md") {
		t.Fatalf("canonicalPath = %q, %v", got, err)
	}
	// A file where a directory is expected is ENOTDIR and falls back like ENOENT.
	if err := os.WriteFile(filepath.Join(notes, "file.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = canonicalPath(filepath.Join(alias, "file.md", "x"))
	if err != nil || got != filepath.Join(notes, "file.md", "x") {
		t.Fatalf("ENOTDIR fallback = %q, %v", got, err)
	}

	project := noteScope{root: notes}
	private := noteScope{root: root, scope: ".forks/2026-01-02T03-04-05-678Z_s1/notes"}
	if err := os.MkdirAll(filepath.Join(root, private.scope), 0o755); err != nil {
		t.Fatal(err)
	}
	path, err := notePath(project, "sub/a.md")
	if err != nil || path != filepath.Join(notes, "sub", "a.md") {
		t.Fatalf("notePath = %q, %v", path, err)
	}
	// A project scope never reaches into its managed .forks directory through an alias.
	if err := os.MkdirAll(filepath.Join(notes, ".forks", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(notes, ".forks", "x"), filepath.Join(notes, "managed")); err != nil {
		t.Fatal(err)
	}
	if _, err := notePath(project, "managed/a.md"); err == nil || !strings.HasPrefix(err.Error(), "Note path leaves its scope through a filesystem alias: ") {
		t.Fatalf("managed alias accepted: %v", err)
	}
	// A private scope never leaves its directory through an alias.
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, private.scope, "out")); err != nil {
		t.Fatal(err)
	}
	if _, err := notePath(private, "out/a.md"); err == nil || !strings.HasPrefix(err.Error(), "Note path leaves its scope through a filesystem alias: ") {
		t.Fatalf("private alias accepted: %v", err)
	}
	if _, err := notePath(private, "ok/a.md"); err != nil {
		t.Fatalf("private note rejected: %v", err)
	}
}

func TestNoteScopes_addressOf(t *testing.T) {
	root := nsRealTemp(t)
	notes := filepath.Join(root, "notes")
	privateRel := ".forks/2026-01-02T03-04-05-678Z_s1/notes"
	private := filepath.Join(notes, filepath.FromSlash(privateRel))
	for _, d := range []string{notes, private} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	scopes := []noteScope{{root: notes, scope: privateRel}, {root: notes}}
	cases := []struct {
		path string
		want noteAddress
		ok   bool
	}{
		{filepath.Join(private, "a.md"), noteAddress{noteScope{root: notes, scope: privateRel}, "a.md"}, true},
		{filepath.Join(private, "x", "a.md"), noteAddress{noteScope{root: notes, scope: privateRel}, "x/a.md"}, true},
		{filepath.Join(notes, "b.md"), noteAddress{noteScope{root: notes}, "b.md"}, true},
		{filepath.Join(notes, "missing", "c.md"), noteAddress{noteScope{root: notes}, "missing/c.md"}, true},
		// The project scope never owns the managed directory.
		{filepath.Join(notes, ".forks", "other", "notes", "d.md"), noteAddress{}, false},
		{filepath.Join(notes, ".forks"), noteAddress{}, false},
		{filepath.Join(root, "unrelated", "e.md"), noteAddress{}, false},
	}
	for _, c := range cases {
		got, ok, err := addressOf(c.path, scopes)
		if err != nil || ok != c.ok || got.root != c.want.root || got.scope != c.want.scope || got.path != filepath.ToSlash(c.want.path) {
			t.Errorf("addressOf(%s) = %+v, %v, %v; want %+v, %v", c.path, got, ok, err, c.want, c.ok)
		}
	}
}

// `{ ...address, kind }` over an address that spread a whole binding: the binding's own `kind` member takes the
// touch kind in place (no duplicate key), path follows the binding's members, a seed comes last.
func TestNoteScopes_touchFileKindReplacesTheLeakedBindingKind(t *testing.T) {
	binding := parseBinding(nsProject())
	file := noteTouchFile{noteAddress: withPath(binding, "x.md"), kind: "read"}
	want := `{"version":1,"kind":"read","owner":{"id":"s1","timestamp":"2026-01-02T03:04:05.678Z"},"root":"/p/.pi/notes","scope":"","path":"x.md"}`
	if got := string(file.appendJSON(nil)); got != want {
		t.Fatalf("touch file JSON\n got %s\nwant %s", got, want)
	}
	fork := parseBinding(nsFork())
	got := string(noteTouchFile{noteAddress: withPath(fork, "x.md"), kind: "write", seed: &fork.seeds[0]}.appendJSON(nil))
	if !strings.HasPrefix(got, `{"version":1,"kind":"write","owner":`) || strings.Count(got, `"kind":`) != 1 ||
		!strings.Contains(got, `"absent":["gone.md"],"path":"x.md","seed":{"path":"a.md"`) {
		t.Fatalf("fork-origin touch file JSON %s", got)
	}
}

func TestNoteScopes_canonicalPathLoopIsANodeELOOP(t *testing.T) {
	root := nsRealTemp(t)
	loop := filepath.Join(root, "loop")
	if err := os.Symlink("loop", loop); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err := canonicalPath(filepath.Join(loop, "x"))
	// realpathSync stats the looping component, not the whole input.
	want := "ELOOP: too many symbolic links encountered, stat '" + loop + "'"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %s", err, want)
	}
}
