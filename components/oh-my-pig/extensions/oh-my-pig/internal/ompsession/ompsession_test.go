package ompsession

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ompSession is a session as omp writes it: the title record, the header, entries PiG has no counterpart for
// between the others, omp's roles and model name, and a snapcompact compaction that kept the entries from e5 on.
var ompSession = []string{
	`{"type":"title","v":1,"title":"My OMP Session","source":"auto","pad":"   "}`,
	`{"type":"session","version":3,"id":"sess-1","timestamp":"2026-11-25T09:00:00Z","cwd":"/tmp/proj","title":"My OMP Session","titleSource":"auto"}`,
	`{"type":"model_change","id":"e1","parentId":null,"timestamp":"t1","model":"github-copilot/gemini-4-neon"}`,
	`{"type":"thinking_level_change","id":"e2","parentId":"e1","timestamp":"t2","thinkingLevel":"high"}`,
	`{"type":"model_usage","id":"u1","parentId":"e2","timestamp":"t3","model":"x"}`,
	`{"type":"title_change","id":"e3","parentId":"u1","timestamp":"t4","title":"Renamed Title"}`,
	`{"type":"message","id":"e4","parentId":"e3","timestamp":"t5","message":{"role":"user","content":"old prompt","timestamp":1794332058446123}}`,
	`{"type":"ttsr_injection","id":"t1","parentId":"e4","timestamp":"t6","injectedRules":["r"]}`,
	`{"type":"message","id":"e5","parentId":"t1","timestamp":"t7","message":{"role":"developer","content":"rule <reminder>"}}`,
	`{"type":"message","id":"e6","parentId":"e5","timestamp":"t8","message":{"role":"custom","customType":"eval-state-context","content":"kernel alive"}}`,
	`{"type":"compaction","id":"c1","parentId":"e6","timestamp":"t9","summary":"HISTORY\n","firstKeptEntryId":"e5","tokensBefore":50000,"method":"snapcompact","preserveData":{"snapcompact":{"frames":[{"data":"blob:x"}],"text":"¶user:old prompt█¶think:\u000ethought\u000f"}}}`,
	`{"type":"message","id":"e7","parentId":"c1","timestamp":"t10","message":{"role":"user","content":"latest prompt"}}`,
}

func writeOmpSession(t *testing.T, dir string, lines []string) string {
	t.Helper()
	path := filepath.Join(dir, "2026-11-25T09-00-00-000Z_sess-1.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readSession(t *testing.T, path string) (records []map[string]any, raw []string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("line %q: %v", line, err)
		}
		records, raw = append(records, record), append(raw, line)
	}
	return records, raw
}

func byID(records []map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, record := range records {
		if id, _ := record["id"].(string); id != "" && record["type"] != "session" {
			out[id] = record
		}
	}
	return out
}

func TestTranslateFullWritesEveryEntryInPigForm(t *testing.T) {
	dir := t.TempDir()
	src := writeOmpSession(t, dir, ompSession)
	dst := filepath.Join(dir, "out.jsonl")
	res, err := Translate(src, dst, Options{Full: true})
	if err != nil {
		t.Fatal(err)
	}
	records, raw := readSession(t, dst)
	if info, _ := os.Stat(dst); info.Size() != res.Bytes {
		t.Errorf("Bytes = %d, the file holds %d", res.Bytes, info.Size())
	}
	if res.SessionID != "sess-1" || res.Title != "Renamed Title" {
		t.Errorf("result = %+v", res)
	}
	if got, want := raw[0], `{"type":"session","version":3,"id":"sess-1","timestamp":"2026-11-25T09:00:00Z","cwd":"/tmp/proj"}`; got != want {
		t.Errorf("header = %s\nwant     %s", got, want)
	}
	entries := byID(records)
	for _, id := range []string{"u1", "t1"} {
		if _, ok := entries[id]; ok {
			t.Errorf("entry %s has no PiG counterpart and was written", id)
		}
	}
	if e := entries["e1"]; e["provider"] != "github-copilot" || e["modelId"] != "gemini-4-neon" || e["model"] != nil {
		t.Errorf("model_change = %v", e)
	}
	// The children of the dropped entries hang from the entries the dropped ones hung from.
	if e := entries["e3"]; e["type"] != "session_info" || e["name"] != "Renamed Title" || e["parentId"] != "e2" {
		t.Errorf("title_change = %v", e)
	}
	if e := entries["e5"]; e["type"] != "custom_message" || e["customType"] != "omp:developer" || e["content"] != "rule <reminder>" || e["parentId"] != "e4" {
		t.Errorf("developer message = %v", e)
	}
	if e := entries["e6"]; e["type"] != "custom_message" || e["customType"] != "eval-state-context" || e["content"] != "kernel alive" {
		t.Errorf("custom message = %v", e)
	}
	// The archive's text follows the summary as plain text: line ends for omp's glyph, no reasoning markers.
	if e := entries["c1"]; e["summary"] != "HISTORY\n¶user:old prompt\n¶think:thought" || e["preserveData"] != nil || e["firstKeptEntryId"] != "e5" {
		t.Errorf("compaction = %v", e)
	}
	// An entry that needs no change is omp's record, byte for byte.
	if !strings.Contains(strings.Join(raw, "\n"), ompSession[6]) || !strings.Contains(strings.Join(raw, "\n"), ompSession[11]) {
		t.Errorf("unchanged entries were rewritten:\n%s", strings.Join(raw, "\n"))
	}
	// A rewritten entry keeps what it does not change: the text is not HTML-escaped.
	if !strings.Contains(strings.Join(raw, "\n"), `"rule <reminder>"`) {
		t.Errorf("the developer message's text was escaped:\n%s", strings.Join(raw, "\n"))
	}
}

func TestTranslateStartsAtTheEntriesTheLatestCompactionKept(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "out.jsonl")
	if _, err := Translate(writeOmpSession(t, dir, ompSession), dst, Options{}); err != nil {
		t.Fatal(err)
	}
	records, _ := readSession(t, dst)
	var ids []string
	var parent any
	for _, record := range records[1:] {
		// One chain from the root: the title, model and thinking level set before the kept entries, then those.
		if record["parentId"] != parent {
			t.Errorf("entry %v hangs from %v, want %v", record["id"], record["parentId"], parent)
		}
		parent = record["id"]
		ids = append(ids, record["id"].(string))
	}
	if got, want := strings.Join(ids, " "), "e3 e1 e2 e5 e6 c1 e7"; got != want {
		t.Errorf("entries = %s, want %s", got, want)
	}
	if e := byID(records)["c1"]; e["firstKeptEntryId"] != "e5" {
		t.Errorf("compaction = %v", e)
	}
}

func TestTranslateKeepsLargeIntegersAndSkipsALineBeingWritten(t *testing.T) {
	dir := t.TempDir()
	lines := append([]string(nil), ompSession[:7]...)
	// e4 hangs from the title change: rewriting its parent must not round its 16-digit timestamp.
	lines = append(lines, `{"type":"message","id":"e8","parentId":"u1","timestamp":"t","message":{"role":"user","content":"x","timestamp":9007199254740993}}`)
	path := writeOmpSession(t, dir, lines)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"type":"message","id":"e9","parentId":"e8","mess`)
	_ = f.Close()
	dst := filepath.Join(dir, "out.jsonl")
	if _, err := Translate(path, dst, Options{Full: true}); err != nil {
		t.Fatal(err)
	}
	_, raw := readSession(t, dst)
	last := raw[len(raw)-1]
	if !strings.Contains(last, `"id":"e8"`) || !strings.Contains(last, `9007199254740993`) || !strings.Contains(last, `"parentId":"e2"`) {
		t.Errorf("last entry = %s", last)
	}
}

func TestMirrorTranslatesOncePerSourceStateAndNeverReplacesAContinuedSession(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "proj")
	srcDir := SourceDirs(home, cwd)[0]
	if err := os.MkdirAll(srcDir, 0o700); err != nil {
		t.Fatal(err)
	}
	src := writeOmpSession(t, srcDir, ompSession)
	old := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(src, old, old); err != nil {
		t.Fatal(err)
	}
	sessionDir := filepath.Join(home, "sessions")

	m := OpenMirror(sessionDir)
	if n, problems := m.SyncAll(home, cwd); n != 1 || len(problems) != 0 {
		t.Fatalf("first sync: %d translated, problems %v", n, problems)
	}
	if err := m.Save(); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(sessionDir, "2026-11-25T09-00-00-000Z_sess-1-omp.jsonl")
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	// PiG's --continue takes the newest file: an imported session is as old as omp left it.
	if !info.ModTime().Equal(old) {
		t.Errorf("translation time = %v, want the source's %v", info.ModTime(), old)
	}
	entries, _ := os.ReadDir(sessionDir)
	for _, entry := range entries {
		if entry.Name() != filepath.Base(dst) && strings.HasSuffix(entry.Name(), ".jsonl") {
			t.Errorf("%s would be listed as a session", entry.Name())
		}
	}

	// A second program start reads the index: nothing is translated.
	m = OpenMirror(sessionDir)
	if n, problems := m.SyncAll(home, cwd); n != 0 || len(problems) != 0 {
		t.Fatalf("unchanged source: %d translated, problems %v", n, problems)
	}

	// omp wrote more: the translation follows.
	grown := append(append([]string(nil), ompSession...), `{"type":"message","id":"e8","parentId":"e7","timestamp":"t11","message":{"role":"assistant","content":[]}}`)
	writeOmpSession(t, srcDir, grown)
	m = OpenMirror(sessionDir)
	if n, _ := m.SyncAll(home, cwd); n != 1 {
		t.Fatalf("changed source: %d translated, want 1", n)
	}
	if err := m.Save(); err != nil {
		t.Fatal(err)
	}
	if records, _ := readSession(t, dst); records[len(records)-1]["id"] != "e8" {
		t.Errorf("the translation does not hold the new entry")
	}

	// PiG appended a turn to the translation, then omp wrote more too: the PiG session is left as it is.
	f, err := os.OpenFile(dst, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"type":"message","id":"p1","parentId":"e8","timestamp":"t12","message":{"role":"user","content":"continued in PiG"}}` + "\n")
	_ = f.Close()
	writeOmpSession(t, srcDir, append(grown, `{"type":"message","id":"e9","parentId":"e8","timestamp":"t13","message":{"role":"user","content":"continued in omp"}}`))
	m = OpenMirror(sessionDir)
	n, problems := m.SyncAll(home, cwd)
	if n != 0 || len(problems) != 1 {
		t.Fatalf("continued translation: %d translated, problems %v", n, problems)
	}
	if records, _ := readSession(t, dst); records[len(records)-1]["id"] != "p1" {
		t.Errorf("the continued session was replaced")
	}
	// The same state of the source is not reported again.
	if err := m.Save(); err != nil {
		t.Fatal(err)
	}
	if n, problems := OpenMirror(sessionDir).SyncAll(home, cwd); n != 0 || len(problems) != 0 {
		t.Errorf("second start: %d translated, problems %v", n, problems)
	}
}

func TestPigSessionDir(t *testing.T) {
	env := func(vars map[string]string) func(string) string {
		return func(name string) string { return vars[name] }
	}
	for _, tc := range []struct {
		name, flag string
		vars       map[string]string
		want       string
	}{
		{"default", "", nil, "/home/u/.pig/agent/sessions/--home-u-work-proj--"},
		{"agent directory", "", map[string]string{"PIG_CODING_AGENT_DIR": "~/agent"}, "/home/u/agent/sessions/--home-u-work-proj--"},
		{"session directory", "", map[string]string{"PIG_CODING_AGENT_SESSION_DIR": "/s", "PIG_CODING_AGENT_DIR": "/a"}, "/s"},
		{"flag", "/flag", map[string]string{"PIG_CODING_AGENT_SESSION_DIR": "/s"}, "/flag"},
	} {
		got, err := PigSessionDir(tc.flag, "/home/u", "/home/u/work/proj", env(tc.vars))
		if err != nil || filepath.ToSlash(got) != tc.want {
			t.Errorf("%s: %q, %v; want %q", tc.name, got, err, tc.want)
		}
	}
	if _, err := PigSessionDir("", "/home/u", "/p", env(map[string]string{"PIG_USE_PI_DIRS": "1"})); err == nil {
		t.Error("Pi's shared directories: want an error")
	}
}
