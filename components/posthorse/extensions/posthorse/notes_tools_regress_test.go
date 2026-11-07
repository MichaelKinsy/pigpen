package posthorse

// Regression tests of lane C2 (notes.go, note_guards.go, note_tool_effects.go) from the adversarial
// review (port/review/notestools.md). Each states the value the TypeScript original produces,
// measured with Node 24.22.1 / TypeBox 1.3.27 on xd6702.

import (
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// Regression (review N-finding): notes.ts:300-340 (Type.Object ...): the registered `parameters` must be TypeBox's
// JSON, member order included. notesSchema() is a plain map, which the SDK writes alphabetically.
func TestNotesToolsSchemaBytes(t *testing.T) {
	const want = `{"type":"object","required":["op"],"properties":{"op":{"anyOf":[{"type":"string","const":"list"},{"type":"string","const":"read"},{"type":"string","const":"write"},{"type":"string","const":"append"},{"type":"string","const":"search"}],"description":"Operation to perform"},"path":{"type":"string","description":"Note or folder path relative to the notes directory"},"scope":{"type":"string","description":"Absolute scope selector from list/search; only for read/list/search. Omit for current notes and project-wide discovery."},"content":{"type":"string","description":"Full file content (write) or text to add (append)"},"query":{"type":"string","description":"Substring to find in notes (search)"},"offset":{"type":"integer","description":"Character offset for list/read/search (default 0)","minimum":0}}}`
	tools, _ := fxCaptureRegistration(t, Extension())
	for _, tool := range tools {
		if tool.Name == "notes" {
			if got := string(tool.Parameters); got != want {
				t.Fatalf("notes parameters on the wire:\n got %s\nwant %s", got, want)
			}
			return
		}
	}
	t.Fatal("notes tool not registered")
}

// Regression (review N-finding): notes.ts:97 statSync(path, { throwIfNoEntry: false }) is undefined for ENOTDIR
// (Node 24: `statENOTDIR undefined`), so the TS answers "No note at ...".
func TestNotesToolsReadUnderFileNote(t *testing.T) {
	dir := notesRealDir(t)
	f := newFixture(t).With(withCwd(dir))
	f.Run("notes", rec{"op": "write", "path": "a", "content": "x"})
	_, message := f.RunErr("notes", rec{"op": "read", "path": "a/b.md"})
	if want := `No note at a/b.md. Use op "list" to see available notes.`; message != want {
		t.Fatalf("read under a file note: %q, want %q", message, want)
	}
}

// Regression (review N-finding): notes.ts:150 / 182 mkdirSync(dirname(path), { recursive: true }) on an existing
// file throws EEXIST (Node 24: "EEXIST: file already exists, mkdir '<dir>'"); os.MkdirAll gives ENOTDIR.
func TestNotesToolsWriteUnderFileNote(t *testing.T) {
	dir := notesRealDir(t)
	f := newFixture(t).With(withCwd(dir))
	f.Run("notes", rec{"op": "write", "path": "a", "content": "x"})
	parent := filepath.Join(dir, ".pi", "notes", "a")
	want := "EEXIST: file already exists, mkdir '" + parent + "'"
	for _, op := range []string{"write", "append"} {
		_, message := f.RunErr("notes", rec{"op": op, "path": "a/b.md", "content": "y"})
		if message != want {
			t.Errorf("%s under a file note: %q, want %q", op, message, want)
		}
	}
}

// Regression (review N-finding): notes.ts:183 existsSync(dir) is true and readFileSync(dir, "utf8") throws
// "EISDIR: illegal operation on a directory, read" (no path; Node 24 probe).
func TestNotesToolsAppendToFolder(t *testing.T) {
	dir := notesRealDir(t)
	f := newFixture(t).With(withCwd(dir))
	f.Run("notes", rec{"op": "write", "path": "folder/x.md", "content": "x"})
	_, message := f.RunErr("notes", rec{"op": "append", "path": "folder", "content": "y"})
	if want := "EISDIR: illegal operation on a directory, read"; message != want {
		t.Fatalf("append to a folder: %q, want %q", message, want)
	}
}

// Regression (review N-finding): notes.ts:113 / 260 print `offset` as a JS number (2**60 -> "1152921504606847000",
// 1e20 -> "100000000000000000000"); notesParamsOf truncates the float64 to int (exact digits,
// and an implementation-defined value for 1e20 that is negative on amd64).
func TestNotesToolsHugeOffset(t *testing.T) {
	dir := notesRealDir(t)
	f := newFixture(t).With(withCwd(dir))
	f.Run("notes", rec{"op": "write", "path": "x.md", "content": "x"})
	_, message := f.RunErr("notes", rec{"op": "read", "path": "x.md", "offset": math.Pow(2, 60)})
	if want := "Offset 1152921504606847000 is past the end of x.md (1 chars)."; message != want {
		t.Errorf("read at 2**60: %q, want %q", message, want)
	}
	_, message = f.RunErr("notes", rec{"op": "search", "query": "x", "offset": 1e20})
	if want := "Notes search pages end before offset 1000000; search for something more specific."; message != want {
		t.Errorf("search at 1e20: %q, want %q", message, want)
	}
}

// Regression (review N-finding): note-guards.ts:153 `{ ...address, kind }` and notes.ts:127 `{ ...target.address,
// kind: "read" }` overwrite the binding's own `kind` in place (JSON.stringify of
// {...binding, path, kind:"read"}); noteTouchFile.appendJSON appends a second "kind" member.
func TestNotesToolsTouchFileKind(t *testing.T) {
	binding := &noteBinding{kind: "project", owner: noteOwner{"o", "2026-01-01T00:00:00.000Z"}, root: "/r", scope: ""}
	scopes := []noteScope{binding.scopeOf(), {root: binding.root}}
	address, ok, err := addressOf("/r/x.md", scopes)
	if err != nil || !ok {
		t.Fatalf("addressOf: %v %v", ok, err)
	}
	got := string(noteTouchFile{noteAddress: address, kind: "read"}.appendJSON(nil))
	want := `{"version":1,"kind":"read","owner":{"id":"o","timestamp":"2026-01-01T00:00:00.000Z"},"root":"/r","scope":"","path":"x.md"}`
	if got != want {
		t.Fatalf("touch file JSON:\n got %s\nwant %s", got, want)
	}
}

// Regression (review N-finding): note-guards.ts:35 fileURLToPath = WHATWG URL + decodeURIComponent (Node 24 probe):
// "file:///a%zz" and "file:///a%ff" throw "URI malformed", "file://LOCALHOST/x" is "/x",
// "file:///a\b" is "/a/b".
func TestNotesToolsFileURL(t *testing.T) {
	if filepath.Separator == '\\' {
		t.Skip("POSIX table")
	}
	cwd := "/work"
	for _, c := range []struct{ in, want, err string }{
		{"file:///a%zz", "", "URI malformed"},
		{"file:///a%ff", "", "URI malformed"},
		{"file://LOCALHOST/x", "/x", ""},
		{`file:///a\b`, "/a/b", ""},
	} {
		got, err := toolPath("read", c.in, cwd)
		message := ""
		if err != nil {
			message = err.Error()
		}
		if got != c.want || message != c.err {
			t.Errorf("toolPath(read, %q) = %q, %q; want %q, %q", c.in, got, message, c.want, c.err)
		}
	}
}

// Regression (review N-finding): note-guards.ts:166-176 evaluates toolPath only inside `store.roots().some(...)`:
// with no roots it never parses the path and the handler returns undefined (no error).
func TestNotesToolsGuardNoRootsSkipsToolPath(t *testing.T) {
	store := createNotesStore()
	inside, err := operationInsideRoots(store, "read", []noteOperation{{path: "file://host/x", action: noteActionRead}}, "/work")
	if err != nil || inside {
		t.Fatalf("no roots: inside=%v err=%v; want false, nil", inside, err)
	}
}


// Regression (review N-finding): notes.ts:188 appendFileSync writes a JS string as UTF-8, a lone surrogate as
// U+FFFD (EF BF BD), as `write` does through publishFile/jsUTF8Bytes; appendRecord writes the
// SDK's WTF-8 bytes (ED A0 80) verbatim. The SDK decodes the argument "a\ud800" to that WTF-8 form.
func TestNotesToolsAppendLoneSurrogate(t *testing.T) {
	var lone string
	if err := sdkjson.Unmarshal([]byte(`"a\ud800"`), &lone); err != nil || lone != "a\xed\xa0\x80" {
		t.Fatalf("SDK decode of a lone surrogate: % x, %v", lone, err)
	}
	dir := t.TempDir()
	write, appended := filepath.Join(dir, "w.md"), filepath.Join(dir, "a.md")
	if err := publishFile(nil, write, lone); err != nil {
		t.Fatal(err)
	}
	if err := appendRecord(appended, lone); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{write: "a\xef\xbf\xbd", appended: "a\xef\xbf\xbd\n"} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("%s bytes % x, want % x", filepath.Base(path), got, want)
		}
	}
}

// matchingLines against the TS function run verbatim under Node 24.22.1 (V8/ICU toLowerCase,
// trim, split, indexOf, excerptAround): nodeMatchingLinesTS is that output.
func TestNotesToolsMatchingLinesNode(t *testing.T) {
	var cases []struct {
		T    string   `json:"t"`
		Q    string   `json:"q"`
		Rows []string `json:"rows"`
	}
	if err := sdkjson.Unmarshal([]byte(nodeMatchingLinesTS), &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		got := matchingLines("n", c.T, jsToLowerCase(c.Q))
		if len(got) == 0 && len(c.Rows) == 0 {
			continue
		}
		if strings.Join(got, "\x00") != strings.Join(c.Rows, "\x00") {
			t.Errorf("matchingLines(%+q, %+q):\n got %+q\nwant %+q", c.T, c.Q, got, c.Rows)
		}
	}
}

const nodeMatchingLinesTS = `[{"t":"xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx\u0130\u0130\u0130 NEEDLE tttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttt","q":"needle","rows":["n:1: \u2026xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx\u0130\u0130\u0130 NEEDLE tttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttttt\u2026"]},{"t":"\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130\u0130needlezzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz","q":"NEEDLE","rows":["n:1: \u2026zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz\u2026"]},{"t":"\u039f\u0394\u039f\u03a3","q":"\u03bf\u03b4\u03bf\u03c2","rows":["n:1: \u039f\u0394\u039f\u03a3"]},{"t":"\u03bf\u03b4\u03bf\u03c2","q":"\u039f\u0394\u039f\u03a3","rows":["n:1: \u03bf\u03b4\u03bf\u03c2"]},{"t":"\u039f\u0394\u039f\u03a3 \u039a\u0391\u0399","q":"\u03bf\u03b4\u03bf\u03c2","rows":["n:1: \u039f\u0394\u039f\u03a3 \u039a\u0391\u0399"]},{"t":"\u0391\u03a3'\u0392","q":"\u03b1\u03c2'\u03b2","rows":[]},{"t":"\u0391\u03a3'","q":"\u03b1\u03c2'","rows":["n:1: \u0391\u03a3'"]},{"t":"\u03a3","q":"\u03c3","rows":["n:1: \u03a3"]},{"t":"\ufeff  needle  \ufeff","q":"needle","rows":["n:1: needle"]},{"t":"a\r\nNEEDLE\r\n","q":"needle","rows":["n:2: NEEDLE"]},{"t":"\u2028needle\u2029x","q":"needle","rows":["n:1: needle\u2029x"]},{"t":"\u212aelvin","q":"kelvin","rows":["n:1: \u212aelvin"]},{"t":"kelvin","q":"\u212a","rows":["n:1: kelvin"]},{"t":"\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00NEEDLE","q":"needle","rows":["n:1: \u2026\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00NEEDLE"]},{"t":"\u1e9e STRASSE","q":"\u00df","rows":["n:1: \u1e9e STRASSE"]},{"t":"\ua7cb","q":"\u0264","rows":["n:1: \ua7cb"]},{"t":"\u1c89","q":"\u1c8a","rows":["n:1: \u1c89"]},{"t":"\u0130","q":"i\u0307","rows":["n:1: \u0130"]},{"t":"\u0130","q":"i","rows":["n:1: \u0130"]},{"t":"\u03a3\u0391\n\u0392\u03a3","q":"\u03b2\u03c2","rows":["n:2: \u0392\u03a3"]},{"t":"x\u0345\u03a3","q":"\u03c3","rows":[]},{"t":"A\u03a3\u0345","q":"\u03c2","rows":["n:1: A\u03a3\u0345"]},{"t":"","q":"","rows":["n:1: "]},{"t":"a\n\nb","q":"","rows":["n:1: a","n:2: ","n:3: b"]}]`

// Regression (review N-finding): notes.ts:203 `text.toLowerCase().includes(query)` compares UTF-16 units, so a lone
// high surrogate query ("\ud83d", WTF-8 in Go) is found inside "😀" and the line matches at unit 1
// (TS: ["n:1: x😀"]); the Go precheck strings.Contains compares bytes and returns no rows.
func TestNotesToolsLoneSurrogateQuery(t *testing.T) {
	var query string
	if err := sdkjson.Unmarshal([]byte(`"\ud83d"`), &query); err != nil {
		t.Fatal(err)
	}
	got := matchingLines("n", "x\U0001F600", jsToLowerCase(query))
	if want := []string{"n:1: x\U0001F600"}; strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("lone surrogate query: %+q, want %+q", got, want)
	}
}

// PERF: notes.ts:203-209 use V8's indexOf/includes (Boyer-Moore family, near-linear). The ASCII
// fast path asciiIndexFold is a naive O(len(hay)·len(needle)) scan: a 1 MiB note of "a" and a
// query of 4,096 "a" + "b" costs ~4·10^9 byte compares per line scan and again for the whole text.
func BenchmarkNotesToolsASCIIFoldWorst(b *testing.B) {
	text := strings.Repeat("a", 1<<20)
	query := strings.Repeat("a", 4096) + "b"
	b.Run("matchingLines", func(b *testing.B) {
		for b.Loop() {
			matchingLines("n", text, query)
		}
	})
	b.Run("stdlibIndexOfLowered", func(b *testing.B) {
		for b.Loop() {
			_ = strings.Contains(strings.ToLower(text), query)
		}
	})
}

// Regression (review N-finding): note-scopes.ts:111-124 canonicalPath rethrows realpathSync's Node error; for a
// symlink loop Node 24 says "ELOOP: too many symbolic links encountered, stat '<path>'". The guard
// (note-guards.ts:52 -> addressOf) reaches it for any native read/write/edit of such a path, and Pi
// shows it as "Extension failed, blocking execution: Error: <message>".
func TestNotesToolsCanonicalPathLoop(t *testing.T) {
	if filepath.Separator == '\\' {
		t.Skip("POSIX symlink loop")
	}
	dir := notesRealDir(t)
	loop := filepath.Join(dir, "loop")
	if err := os.Symlink(loop, loop); err != nil {
		t.Fatal(err)
	}
	_, err := canonicalPath(loop)
	want := "ELOOP: too many symbolic links encountered, stat '" + loop + "'"
	if err == nil || err.Error() != want {
		t.Fatalf("canonicalPath(loop) error %v, want %q", err, want)
	}
}

// nodePathCase is one row of testdata/node_path_cases.json: the value Node 24.22.1 returns for
// fileURLToPath(in, {windows}) (k "url"), path.win32.resolve(cwd, in) (k "win") or
// path.posix.resolve(cwd, in) (k "posix"). An "err" row carries the error message.
type nodePathCase struct {
	K       string   `json:"k"`
	In      string   `json:"in"`
	Cwd     string   `json:"cwd"`
	Windows bool     `json:"windows"`
	Out     any      `json:"out"`
	_       struct{} `json:"-"`
}

// Regression (review N7, N13): fileURLToPath and path.resolve on both platforms.
func TestNodePathCases(t *testing.T) {
	raw, err := os.ReadFile("testdata/node_path_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []nodePathCase
	if err := sdkjson.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 100 {
		t.Fatalf("only %d cases", len(cases))
	}
	for _, c := range cases {
		switch c.K {
		case "url":
			got, err := fileURLToPathOS(c.In, c.Windows)
			row := c.Out.([]any)
			want := row[1].(string)
			if !c.Windows {
				want = strings.ReplaceAll(want, "on linux", "on "+runtime.GOOS)
			}
			if row[0] == "err" {
				if err == nil || err.Error() != want {
					t.Errorf("fileURLToPath(%+q, windows=%v) = %+q, %v; want error %q", c.In, c.Windows, got, err, want)
				}
			} else if err != nil || got != want {
				t.Errorf("fileURLToPath(%+q, windows=%v) = %+q, %v; want %+q", c.In, c.Windows, got, err, want)
			}
		case "win":
			if got := win32Resolve(c.Cwd, c.In); got != c.Out.(string) {
				t.Errorf("win32Resolve(%q, %q) = %q; want %q", c.Cwd, c.In, got, c.Out)
			}
		case "posix":
			if got := posixResolve(c.Cwd, c.In); got != c.Out.(string) {
				t.Errorf("posixResolve(%q, %q) = %q; want %q", c.Cwd, c.In, got, c.Out)
			}
		default:
			t.Fatalf("unknown case kind %q", c.K)
		}
	}
}

// Regression (review N14): os.homedir() returns $HOME verbatim when it is set, even empty
// (Node 24 probe `homedirEmptyHOME ""`), so `~/x` never throws; path.join("", "") is ".".
func TestNotesToolsHomeEmpty(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("USERPROFILE on Windows")
	}
	t.Setenv("HOME", "")
	home, err := nodeHomedir()
	if err != nil || home != "" {
		t.Fatalf("nodeHomedir with HOME=\"\": %q, %v", home, err)
	}
	for in, want := range map[string]string{"~/x": "/x", "~": "/work"} {
		got, err := toolPath("read", in, "/work")
		if err != nil || got != want {
			t.Errorf("toolPath(read, %q) with HOME=\"\" = %q, %v; want %q", in, got, err, want)
		}
	}
	t.Setenv("HOME", "/h/./u")
	if got, _ := toolPath("write", "~/a/../x", "/work"); got != "/h/u/x" {
		t.Errorf("toolPath(write, ~/a/../x) = %q", got)
	}
}

// Regression (review N11): the case-insensitive scan of a 1 MiB note stays linear on repetitive
// input (the first Go version was O(n*m): 3 s here; Node takes 16 ms).
func TestNotesToolsASCIIFoldLinear(t *testing.T) {
	text := strings.Repeat("a", 1<<20)
	query := strings.Repeat("a", 4096) + "b"
	start := time.Now()
	if rows := matchingLines("n", text, query); len(rows) != 0 {
		t.Fatalf("rows %v", rows)
	}
	if took := time.Since(start); took > 500*time.Millisecond {
		t.Fatalf("matchingLines took %v", took)
	}
	mixed := strings.Repeat("aA", 1<<19)
	start = time.Now()
	matchingLines("n", mixed, strings.Repeat("a", 4096)+"b")
	if took := time.Since(start); took > 500*time.Millisecond {
		t.Fatalf("matchingLines with capitals took %v", took)
	}
}
