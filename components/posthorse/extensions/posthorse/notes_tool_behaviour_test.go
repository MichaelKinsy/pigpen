package posthorse

// Behaviour tests of the notes tool and the note guards that the mutation check (port/mutations/
// NotesToolsLane.json) found unpinned by the twins: every test names the behaviour it holds.

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// notesTouchEntries is the `data` of every posthorse-note-touch entry the extension appended.
func notesTouchEntries(f *fixture) []rec {
	var out []rec
	for _, entry := range f.Appended() {
		if entry["customType"] == "posthorse-note-touch" {
			if data, ok := entry["data"].(map[string]any); ok {
				out = append(out, data)
			}
		}
	}
	return out
}

// notesTouchKinds is the `kind` of every file of the touch entries appended after `from`.
func notesTouchKinds(touches []rec, from int) []string {
	var kinds []string
	for _, touch := range touches[from:] {
		for _, file := range touch["files"].([]any) {
			kinds = append(kinds, file.(map[string]any)["kind"].(string))
		}
	}
	return kinds
}

func TestNotesToolRecordsEveryOperationAsATouch(t *testing.T) {
	dir := notesRealDir(t)
	f := newFixture(t).With(withCwd(dir))
	steps := []struct {
		params rec
		kind   string
	}{
		{rec{"op": "write", "path": "a.md", "content": "one"}, "write"},
		{rec{"op": "write", "path": "a.md", "content": ""}, "clear"},
		{rec{"op": "append", "path": "a.md", "content": "two"}, "write"},
		{rec{"op": "read", "path": "a.md"}, "read"},
	}
	for _, step := range steps {
		before := len(notesTouchEntries(f))
		f.Run("notes", step.params)
		touches := notesTouchEntries(f)
		if len(touches) != before+1 {
			t.Fatalf("%v appended %d touch entries", step.params, len(touches)-before)
		}
		if kinds := notesTouchKinds(touches, before); len(kinds) != 1 || kinds[0] != step.kind {
			t.Errorf("%v recorded %v, want [%s]", step.params, kinds, step.kind)
		}
		last := touches[len(touches)-1]
		if last["toolCallId"] != "id" || last["rootToolCallId"] != "id" {
			t.Errorf("%v: call ids %v / %v", step.params, last["toolCallId"], last["rootToolCallId"])
		}
		file := last["files"].([]any)[0].(map[string]any)
		if file["path"] != "a.md" || file["root"] == "" || file["scope"] != "" {
			t.Errorf("%v: touched file %v", step.params, file)
		}
	}
	// A listing and a search read nothing in particular and record nothing.
	before := len(notesTouchEntries(f))
	f.Run("notes", rec{"op": "list"})
	f.Run("notes", rec{"op": "search", "query": "two"})
	if len(notesTouchEntries(f)) != before {
		t.Error("list/search recorded a touch")
	}
}

func TestNotesToolResultTextsAndErrors(t *testing.T) {
	dir := notesRealDir(t)
	f := newFixture(t).With(withCwd(dir))
	path := filepath.Join(dir, ".pi", "notes", "r.md")
	write := f.Run("notes", rec{"op": "write", "path": "r.md", "content": "x"})
	if write.Text() != "Wrote "+path || write.Details["kind"] != "note-write" {
		t.Errorf("write result %q %v", write.Text(), write.Details)
	}
	appended := f.Run("notes", rec{"op": "append", "path": "r.md", "content": "y"})
	if appended.Text() != "Appended to "+path || appended.Details["kind"] != "note-append" {
		t.Errorf("append result %q %v", appended.Text(), appended.Details)
	}
	if list := f.Run("notes", rec{"op": "list"}); list.Details["kind"] != "notes-list" {
		t.Errorf("list details %v", list.Details)
	}
	if search := f.Run("notes", rec{"op": "search", "query": "x"}); search.Details["kind"] != "notes-search" {
		t.Errorf("search details %v", search.Details)
	}
	refusal := "scope is only valid for read/list/search; writes use this session’s notes scope."
	for _, op := range []string{"write", "append"} {
		_, message := f.RunErr("notes", rec{"op": op, "path": "r.md", "content": "z", "scope": filepath.Join(dir, ".pi", "notes")})
		if message != refusal {
			t.Errorf("%s with a scope: %q", op, message)
		}
	}
	for params, want := range map[string]string{
		"write no path":     `"path" is required for op "write".`,
		"append no path":    `"path" is required for op "append".`,
		"append no content": `"content" is required for op "append".`,
		"read no path":      `"path" is required for op "read".`,
		"search no query":   `"query" is required for op "search".`,
		"write no content":  `"content" is required for op "write" (use "" to clear a note).`,
	} {
		call := map[string]rec{
			"write no path":     {"op": "write", "content": "x"},
			"append no path":    {"op": "append", "content": "x"},
			"append no content": {"op": "append", "path": "r.md"},
			"read no path":      {"op": "read"},
			"search no query":   {"op": "search"},
			"write no content":  {"op": "write", "path": "r.md"},
		}[params]
		if _, message := f.RunErr("notes", call); message != want {
			t.Errorf("%s: %q, want %q", params, message, want)
		}
	}
	// `read .` is a listing; a non-binary read of a missing note says so.
	if got := f.Run("notes", rec{"op": "read", "path": "."}).Text(); !strings.HasPrefix(got, "Notes directory: ") {
		t.Errorf("read . %q", got)
	}
	if _, message := f.RunErr("notes", rec{"op": "read", "path": "missing.md"}); message != `No note at missing.md. Use op "list" to see available notes.` {
		t.Errorf("missing note: %q", message)
	}
	// An empty folder and a folder with no notes name themselves.
	if got := f.Run("notes", rec{"op": "list", "path": "nowhere"}).Text(); got != "Notes directory: "+filepath.Join(dir, ".pi", "notes")+"\n(no notes in nowhere)" {
		t.Errorf("empty folder list %q", got)
	}
	emptyDir := notesRealDir(t)
	empty := newFixture(t).With(withCwd(emptyDir))
	emptyHeader := "Notes directory: " + filepath.Join(emptyDir, ".pi", "notes") + "\n"
	if got := empty.Run("notes", rec{"op": "list"}).Text(); got != emptyHeader+"(no notes yet)" {
		t.Errorf("empty list %q", got)
	}
	if got := empty.Run("notes", rec{"op": "list", "path": ""}).Text(); got != emptyHeader+"(no notes yet)" {
		t.Errorf("empty list with an empty folder %q", got)
	}
}

// registeredNotesTool is the register frame entry of the notes tool as the host receives it.
func registeredNotesTool(t *testing.T) map[string]any {
	t.Helper()
	hostSide, extSide := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- Extension().RunWithConn(extSide) }()
	defer func() {
		_ = hostSide.Close()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Errorf("the registration probe did not stop")
		}
	}()
	for {
		var hdr [4]byte
		if _, err := io.ReadFull(hostSide, hdr[:]); err != nil {
			t.Fatalf("read registration: %v", err)
		}
		data := make([]byte, binary.BigEndian.Uint32(hdr[:]))
		if _, err := io.ReadFull(hostSide, data); err != nil {
			t.Fatalf("read registration: %v", err)
		}
		var frame struct {
			Type     string `json:"type"`
			ID       string `json:"id"`
			Register *struct {
				Tools []map[string]any `json:"tools"`
			} `json:"register"`
		}
		if err := json.Unmarshal(data, &frame); err != nil {
			t.Fatalf("decode registration frame: %v", err)
		}
		switch frame.Type {
		case "call":
			reply, _ := json.Marshal(map[string]any{"type": "call_result", "id": frame.ID, "call_result": map[string]any{"result": map[string]any{}}})
			var out [4]byte
			binary.BigEndian.PutUint32(out[:], uint32(len(reply)))
			_, _ = hostSide.Write(append(out[:], reply...))
		case "register":
			for _, tool := range frame.Register.Tools {
				if tool["name"] == "notes" {
					return tool
				}
			}
			t.Fatal("tool notes is not registered under that name")
		}
	}
}

// The registration text is the original's: name, label, prompt snippet and description are those of
// the unmodified original (testdata/notes_tool_registration.json, read from port/oracle/src/notes.ts).
func TestNotesToolRegistrationMatchesTheOriginal(t *testing.T) {
	raw, err := os.ReadFile("testdata/notes_tool_registration.json")
	if err != nil {
		t.Fatal(err)
	}
	var want map[string]string
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	got := registeredNotesTool(t)
	for field, key := range map[string]string{"name": "name", "label": "label", "description": "description", "promptSnippet": "prompt_snippet"} {
		if got[key] != want[field] {
			t.Errorf("%s:\n got %q\nwant %q", field, got[key], want[field])
		}
	}
}

// appendISOMinute is `new Date(ms).toISOString().slice(0, 16)` (values from Node 24.22.1): an
// expanded year ("+010000-…") leaves only the hour in those 16 characters.
func TestAppendISOMinute(t *testing.T) {
	for _, c := range []struct {
		ms   float64
		want string
	}{
		{253402300800000, "+010000-01-01T00"},
		{253402300799999, "9999-12-31T23:59"},
		{-62198755200000, "-000001-01-01T00"},
		{-62167219200000, "0000-01-01T00:00"},
		{-62167219200001, "-000001-12-31T23"},
		{-1.5, "1969-12-31T23:59"},
		{-0.5, "1970-01-01T00:00"},
		{1.9, "1970-01-01T00:00"},
		{1700000000999.9, "2023-11-14T22:13"},
		{8.64e15, "+275760-09-13T00"},
		{-8.64e15, "-271821-04-20T00"},
	} {
		if got := string(appendISOMinute(nil, c.ms)); got != c.want {
			t.Errorf("appendISOMinute(%v) = %q, want %q", c.ms, got, c.want)
		}
	}
}

// Reading counts UTF-16 units, in the page and in the header: a note of 2-byte characters in a
// directory with a non-ASCII name pages by units, not bytes.
func TestNotesReadPagesByUTF16Units(t *testing.T) {
	root := notesRealDir(t)
	dir := filepath.Join(root, "ünï")
	notesMust(t, os.MkdirAll(dir, 0o755))
	f := newFixture(t).With(withCwd(dir))
	body := strings.Repeat("é", 50_000) + strings.Repeat("😀", 10) // 50,020 units
	f.Run("notes", rec{"op": "write", "path": "u.md", "content": body})
	header := "File: " + filepath.Join(dir, ".pi", "notes", "u.md") + "\n"
	page := 40_000 - u16len(header)
	got := f.Run("notes", rec{"op": "read", "path": "u.md"})
	want := fmt.Sprintf("%s%s\n[chars 0-%d of 50020; continue with offset %d]", header, strings.Repeat("é", page), page, page)
	if got.Text() != want {
		t.Fatalf("first page differs: %d units, want %d", u16len(got.Text()), u16len(want))
	}
	if d := got.Details; d["kind"] != "note-read" || d["headerLength"] != float64(u16len(header)) || d["offset"] != 0.0 || d["end"] != float64(page) || d["total"] != 50020.0 {
		t.Errorf("details %v", d)
	}
	// An offset in the middle of a surrogate pair at the end is still counted in units.
	tail := f.Run("notes", rec{"op": "read", "path": "u.md", "offset": 50_000.0})
	if tail.Text() != header+strings.Repeat("😀", 10) {
		t.Errorf("tail page %q", tail.Text())
	}
	if _, message := f.RunErr("notes", rec{"op": "read", "path": "u.md", "offset": 50_020.0}); message != "Offset 50020 is past the end of u.md (50020 chars)." {
		t.Errorf("offset == total: %q", message)
	}
	// offset 0 on an empty note is a page, not an error.
	f.Run("notes", rec{"op": "write", "path": "e.md", "content": ""})
	if got := f.Run("notes", rec{"op": "read", "path": "e.md", "offset": 0.0}).Text(); got != "File: "+filepath.Join(dir, ".pi", "notes", "e.md")+"\n" {
		t.Errorf("empty note read %q", got)
	}
}

// The page budget refuses before the page is empty: with exactly as many characters as the header
// the read is refused, never answered with an empty page (notes.ts:116-122).
func TestNotesReadRefusesWhenOnlyTheHeaderFits(t *testing.T) {
	base := notesRealDir(t)
	// A directory name that makes the header a multiple of four characters, at least 1,004 long.
	var dir string
	for pad := 0; ; pad++ {
		name := strings.Repeat("d", 200) + strings.Repeat("e", pad)
		dir = filepath.Join(base, name, name, name, name, name)
		if header := "File: " + filepath.Join(dir, ".pi", "notes", "x.md") + "\n"; len(header) >= 1004 && len(header)%4 == 0 {
			break
		}
	}
	notesMust(t, os.MkdirAll(dir, 0o755))
	header := "File: " + filepath.Join(dir, ".pi", "notes", "x.md") + "\n"
	f := newFixture(t).With(withCwd(dir))
	f.Run("notes", rec{"op": "write", "path": "x.md", "content": "x"})
	view := func(tokens int) *fixture {
		return f.With(withUsage(100_000, tokens), withReserve(16_384), withEnabled(false), withCwd(dir)).PageContext()
	}
	// Sweep the used tokens: each token is four characters of page, so one value gives exactly
	// len(header). Every answer is a refusal or the whole note, never an empty page.
	refused, full, after := 0, 0, 0
	for tokens := 98_900; tokens > 96_000 && after < 8; tokens-- {
		v := view(tokens)
		v.Fire("turn_start", rec{})
		res, message := v.RunErr("notes", rec{"op": "read", "path": "x.md"})
		switch {
		case message != "":
			if !strings.Contains(message, "Too little context remains") {
				t.Fatalf("tokens %d: %q", tokens, message)
			}
			refused++
		case res.Text() == header+"x":
			full++
		default:
			t.Fatalf("tokens %d: neither a refusal nor the whole note: %q", tokens, res.Text())
		}
		if full > 0 {
			after++
		}
	}
	if refused == 0 || full == 0 {
		t.Fatalf("the sweep never crossed the boundary (refused %d, answered %d)", refused, full)
	}
}

// A schema integer larger than 2^53 prints as a JavaScript number, exponent form from 1e21 on.
func TestNotesOffsetsPrintAsJavaScriptNumbers(t *testing.T) {
	dir := notesRealDir(t)
	f := newFixture(t).With(withCwd(dir))
	f.Run("notes", rec{"op": "write", "path": "x.md", "content": "x"})
	for offset, text := range map[float64]string{1: "1", 1e21: "1e+21", 1.5e22: "1.5e+22", 1e20: "100000000000000000000"} {
		_, message := f.RunErr("notes", rec{"op": "read", "path": "x.md", "offset": offset})
		if want := "Offset " + text + " is past the end of x.md (1 chars)."; message != want {
			t.Errorf("read at %v: %q, want %q", offset, message, want)
		}
	}
	// A listing never prints the offset: far past its end the answer is the page error.
	for _, offset := range []float64{100, 1e20} {
		_, message := f.RunErr("notes", rec{"op": "list", "offset": offset})
		if message != "Offset is past the end; restart with offset 0." {
			t.Errorf("list at %v: %q", offset, message)
		}
	}
}

// notesSearchTree writes name -> content notes, newest first by their order in the list.
func notesSearchTree(t *testing.T, root string, notes []struct{ name, text string }) {
	t.Helper()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, note := range notes {
		path := filepath.Join(root, ".pi", "notes", note.name)
		notesWrite(t, path, note.text)
		notesTouch(t, path, base.Add(time.Duration(len(notes)-i)*time.Minute))
	}
}

func TestNotesSearchSkipsLargeNotesAndReportsThem(t *testing.T) {
	dir := notesRealDir(t)
	big := strings.Repeat("l", 1024*1024)
	notesSearchTree(t, dir, []struct{ name, text string }{
		{"huge-1.log", "needle " + big},
		{"exact.md", "needle" + big[:1024*1024-6]},   // exactly 1 MiB: searched
		{"mid.md", "needle " + big[:1_030_000-7]},   // 1,030,000 bytes: searched
		{"huge-2.log", "needle " + big},
		{"small.md", "a needle"},
	})
	f := newFixture(t).With(withCwd(dir))
	got := f.Run("notes", rec{"op": "search", "query": "needle"}).Text()
	rows := strings.Split(strings.TrimPrefix(got, "[Not searched: 2 notes over 1 MiB.]\n"), "\n")
	if !strings.HasPrefix(got, "[Not searched: 2 notes over 1 MiB.]\n") {
		t.Fatalf("header %.80q", got)
	}
	if len(rows) != 3 || !strings.HasPrefix(rows[0], "exact.md:1: needle") || !strings.HasPrefix(rows[1], "mid.md:1: needle") || rows[2] != "small.md:1: a needle" {
		t.Errorf("rows %.200q", rows)
	}
	// One large note is "1 note"; none leaves no header at all.
	os.Remove(filepath.Join(dir, ".pi", "notes", "huge-2.log"))
	if got := f.Run("notes", rec{"op": "search", "query": "needle"}).Text(); !strings.HasPrefix(got, "[Not searched: 1 note over 1 MiB.]\nexact.md") {
		t.Errorf("one large note: %.80q", got)
	}
	os.Remove(filepath.Join(dir, ".pi", "notes", "huge-1.log"))
	if got := f.Run("notes", rec{"op": "search", "query": "needle"}).Text(); !strings.HasPrefix(got, "exact.md:1:") {
		t.Errorf("no large note: %.80q", got)
	}
}

func TestNotesSearchNoMatchAndLowerCasedQuery(t *testing.T) {
	dir := notesRealDir(t)
	notesSearchTree(t, dir, []struct{ name, text string }{{"g.md", "οδος"}, {"k.md", "kelvin"}})
	f := newFixture(t).With(withCwd(dir))
	if got := f.Run("notes", rec{"op": "search", "query": "zzz-none"}).Text(); got != `No notes match "zzz-none".` {
		t.Errorf("no match: %q", got)
	}
	long := strings.Repeat("a", 150) + strings.Repeat("b", 150)
	want := `No notes match "` + strings.Repeat("a", 90) + "\n… middle omitted …\n" + strings.Repeat("b", 90) + `".`
	if got := f.Run("notes", rec{"op": "search", "query": long}).Text(); got != want {
		t.Errorf("long no-match query: %q", got)
	}
	// The query is lower-cased as JavaScript does: a capital final sigma becomes ς.
	if got := f.Run("notes", rec{"op": "search", "query": "ΟΔΟΣ"}).Text(); got != "g.md:1: οδος" {
		t.Errorf("sigma query: %q", got)
	}
	if got := f.Run("notes", rec{"op": "search", "query": "\u212aELVIN"}).Text(); got != "k.md:1: kelvin" {
		t.Errorf("kelvin-sign query: %q", got)
	}
}

// The page limit counts UTF-16 units of the rows: exactly 40,000 does not stop the search, 40,001
// does; a row of 2-byte characters counts its units.
func TestNotesSearchStopsAtTheLimitInUnits(t *testing.T) {
	run := func(count, rowLen int, text string) (more bool, total int) {
		dir := notesRealDir(t)
		notes := make([]struct{ name, text string }, count)
		for i := range notes {
			notes[i].name = fmt.Sprintf("note-%03d.md", i)
			notes[i].text = text
		}
		notesSearchTree(t, dir, notes)
		f := newFixture(t).With(withCwd(dir))
		f.Fire("turn_start", rec{})
		res := f.Run("notes", rec{"op": "search", "query": "needle"})
		_, _, total = notesPageOf(t, res.Details)
		more, _ = res.Details["more"].(bool)
		return more, total
	}
	// 221 rows of 180 units: 221*181-1 = 40,000 = the limit: not past it.
	if more, total := run(221, 180, "needle"+strings.Repeat("x", 159)); more || total != 40_000 {
		t.Errorf("221 rows of 180: more=%v total=%d", more, total)
	}
	// 226 rows of 176 units: 226*177-1 = 40,001: one past it.
	if more, _ := run(226, 176, "needle"+strings.Repeat("x", 155)); !more {
		t.Error("226 rows of 176: the search did not stop")
	}
	// 150 rows of 171 units (321 bytes): 25,799 units are under the limit, 48,300 bytes are not.
	if more, total := run(150, 171, "needle"+strings.Repeat("é", 150)); more || total != 150*171+149 {
		t.Errorf("150 rows of é: more=%v total=%d", more, total)
	}
}

func TestNoteGuardsToolPathUnicodeSpaces(t *testing.T) {
	cwd := filepath.Join(t.TempDir(), "w")
	for _, r := range []string{"\u00a0", "\u2000", "\u2005", "\u200a", "\u202f", "\u205f", "\u3000"} {
		got, err := toolPath("read", "a"+r+"b.md", cwd)
		if want := filepath.Join(cwd, "a b.md"); err != nil || got != want {
			t.Errorf("U+%04X: %q, %v; want %q", []rune(r)[0], got, err, want)
		}
	}
	for _, r := range []string{"\u1fff", "\u200b", "\u2028", "\u3001"} {
		if got, _ := toolPath("read", "a"+r+"b.md", cwd); got != filepath.Join(cwd, "a"+r+"b.md") {
			t.Errorf("U+%04X must stay: %q", []rune(r)[0], got)
		}
	}
	// Only one leading @ goes, and ~x is not a home directory.
	for in, want := range map[string]string{"@@x.md": "@x.md", "~x.md": "~x.md", "@~/y.md": "y.md"} {
		got, err := toolPath("write", in, cwd)
		if in == "@~/y.md" {
			want = filepath.Join(os.Getenv("HOME"), want)
		} else {
			want = filepath.Join(cwd, want)
		}
		if err != nil || got != want {
			t.Errorf("toolPath(write, %q) = %q, %v; want %q", in, got, err, want)
		}
	}
	// The three native tools expand; every other tool takes the path literally.
	for _, tool := range []string{"read", "write", "edit"} {
		if got, _ := toolPath(tool, "@x.md", cwd); got != filepath.Join(cwd, "x.md") {
			t.Errorf("%s: %q", tool, got)
		}
	}
	for _, tool := range []string{"apply_patch", "write_files", "replace_text", "codemode"} {
		if got, _ := toolPath(tool, "@x.md", cwd); got != cwd+"/@x.md" {
			t.Errorf("%s: %q", tool, got)
		}
	}
	if got, err := toolPath("read", "file://"+filepath.ToSlash(cwd)+"/f.md", "/other"); err != nil || got != filepath.Join(cwd, "f.md") {
		t.Errorf("file URL: %q, %v", got, err)
	}
	if got, err := toolPath("read", "file:x", cwd); err != nil || got != filepath.Join(cwd, "file:x") {
		t.Errorf("a path that only starts with file: is a file name: %q, %v", got, err)
	}
	if _, err := toolPath("read", "file://host/x", cwd); err == nil {
		t.Error("a bad file URL must be an error")
	}
}

// Notices of the discovery (an unmanaged directory under .forks) close a listing and a full search.
func TestNotesListAndSearchEndWithTheDiscoveryNotices(t *testing.T) {
	dir := notesRealDir(t)
	f := newFixture(t).With(withCwd(dir))
	f.Run("notes", rec{"op": "write", "path": "a.md", "content": "needle"})
	bogus := filepath.Join(dir, ".pi", "notes", ".forks", "bogus")
	notesMust(t, os.MkdirAll(bogus, 0o755))
	notice := "Not indexed: unmanaged or incomplete notes data at " + bogus + "."
	list := strings.Split(f.Run("notes", rec{"op": "list"}).Text(), "\n")
	if list[len(list)-1] != notice || len(list) != 3 || !strings.HasPrefix(list[1], "a.md  6B  ") {
		t.Errorf("list %q", list)
	}
	if got := f.Run("notes", rec{"op": "search", "query": "needle"}).Text(); got != "a.md:1: needle\n"+notice {
		t.Errorf("search %q", got)
	}
}

// A failure to reach the note other than a missing entry is reported as Node words it.
func TestNotesReadStatErrorIsNodeStyle(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads every directory")
	}
	dir := notesRealDir(t)
	f := newFixture(t).With(withCwd(dir))
	locked := filepath.Join(dir, ".pi", "notes", "locked")
	notesMust(t, os.MkdirAll(locked, 0o755))
	notesMust(t, os.WriteFile(filepath.Join(locked, "x.md"), []byte("x"), 0o644))
	notesMust(t, os.Chmod(locked, 0))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	_, message := f.RunErr("notes", rec{"op": "read", "path": "locked/x.md"})
	if want := "EACCES: permission denied, lstat '" + filepath.Join(locked, "x.md") + "'"; message != want {
		t.Errorf("read under an unreadable folder: %q, want %q", message, want)
	}
}

// A note that is a symlink loop fails with Node's ELOOP text.
func TestNotesReadSymlinkLoopIsNodeStyle(t *testing.T) {
	dir := notesRealDir(t)
	f := newFixture(t).With(withCwd(dir))
	root := filepath.Join(dir, ".pi", "notes")
	notesMust(t, os.MkdirAll(root, 0o755))
	notesMust(t, os.Symlink("loop.md", filepath.Join(root, "loop.md")))
	_, message := f.RunErr("notes", rec{"op": "read", "path": "loop.md"})
	if want := "ELOOP: too many symbolic links encountered, stat '" + filepath.Join(root, "loop.md") + "'"; message != want {
		t.Errorf("read of a symlink loop: %q, want %q", message, want)
	}
}
