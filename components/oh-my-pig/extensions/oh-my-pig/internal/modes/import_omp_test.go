package modes

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTranslateOmpSessionFile_FullAndActiveTail(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "omp.jsonl")
	lines := []string{
		`{"type":"title","v":1,"title":"My OMP Session","source":"auto","updatedAt":"2026-11-25T10:00:00Z","pad":"   "}`,
		`{"type":"session","version":3,"id":"sess-1","timestamp":"2026-11-25T09:00:00Z","cwd":"/tmp/proj","title":"My OMP Session"}`,
		`{"type":"model_change","id":"e1","parentId":null,"timestamp":"2026-11-25T09:00:01Z","model":"github-copilot/gemini-4-neon"}`,
		`{"type":"thinking_level_change","id":"e2","parentId":"e1","timestamp":"2026-11-25T09:00:02Z","thinkingLevel":"high"}`,
		`{"type":"model_usage","id":"u1","parentId":"e2","timestamp":"2026-11-25T09:00:03Z","purpose":"title"}`,
		`{"type":"title_change","id":"e3","parentId":"u1","timestamp":"2026-11-25T09:00:04Z","title":"Renamed Title"}`,
		`{"type":"message","id":"e4","parentId":"e3","timestamp":"2026-11-25T09:00:05Z","message":{"role":"user","content":"old prompt"}}`,
		`{"type":"ttsr_injection","id":"t1","parentId":"e4","timestamp":"2026-11-25T09:00:06Z","injectedRules":["go-join-hostport"]}`,
		`{"type":"message","id":"e5","parentId":"t1","timestamp":"2026-11-25T09:00:07Z","message":{"role":"developer","content":"rule reminder"}}`,
		`{"type":"message","id":"e6","parentId":"e5","timestamp":"2026-11-25T09:00:08Z","message":{"role":"custom","customType":"eval-state-context","content":"kernel alive"}}`,
		`{"type":"compaction","id":"c1","parentId":"e6","timestamp":"2026-11-25T09:01:00Z","summary":"Compacted history","firstKeptEntryId":"e5","tokensBefore":50000}`,
		`{"type":"message","id":"e7","parentId":"c1","timestamp":"2026-11-25T09:02:00Z","message":{"role":"user","content":"latest prompt"}}`,
	}
	if err := os.WriteFile(src, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// 1. Full mode
	fullDst := filepath.Join(dir, "full.jsonl")
	resFull, err := translateOmpSessionFile(src, fullDst, ompImportOptions{Full: true})
	if err != nil {
		t.Fatalf("full translate failed: %v", err)
	}
	if resFull.SessionID != "sess-1" || resFull.Title != "Renamed Title" {
		t.Fatalf("unexpected metadata: %+v", resFull)
	}
	if resFull.DroppedCount != 3 { // title preamble + model_usage + ttsr_injection
		t.Fatalf("expected 3 dropped, got %d", resFull.DroppedCount)
	}
	if resFull.ConvertedCount != 4 { // model_change + title_change + developer + custom
		t.Fatalf("expected 4 converted, got %d", resFull.ConvertedCount)
	}

	fullRecords := readJSONL(t, fullDst)
	if fullRecords[0]["type"] != "session" {
		t.Fatalf("expected session header first, got %v", fullRecords[0]["type"])
	}
	// Verify parentId splicing across dropped model_usage (u1) and ttsr_injection (t1)
	byID := make(map[string]map[string]any)
	for _, r := range fullRecords[1:] {
		if id, _ := r["id"].(string); id != "" {
			byID[id] = r
		}
	}
	if byID["e1"]["provider"] != "github-copilot" || byID["e1"]["modelId"] != "gemini-4-neon" {
		t.Fatalf("model_change not split: %v", byID["e1"])
	}
	if byID["e3"]["type"] != "session_info" || byID["e3"]["parentId"] != "e2" || byID["e3"]["name"] != "Renamed Title" {
		t.Fatalf("title_change not converted/spliced: %v", byID["e3"])
	}
	if byID["e5"]["type"] != "custom_message" || byID["e5"]["parentId"] != "e4" || byID["e5"]["customType"] != "omp:developer" {
		t.Fatalf("developer message not converted/spliced: %v", byID["e5"])
	}
	if byID["e6"]["type"] != "custom_message" || byID["e6"]["customType"] != "eval-state-context" {
		t.Fatalf("custom message not converted: %v", byID["e6"])
	}

	// 2. Default active-tail mode (from firstKeptEntryId = e5)
	tailDst := filepath.Join(dir, "tail.jsonl")
	resTail, err := translateOmpSessionFile(src, tailDst, ompImportOptions{})
	if err != nil {
		t.Fatalf("tail translate failed: %v", err)
	}
	if resTail.TrimmedPrefix == 0 {
		t.Fatalf("expected trimmed prefix > 0, got %+v", resTail)
	}
	tailRecords := readJSONL(t, tailDst)
	// Header + 3 carried-forward meta entries (e3, e1, e2) + e5, e6, c1, e7 = 8 lines
	if len(tailRecords) != 8 {
		t.Fatalf("expected 8 lines in active tail, got %d", len(tailRecords))
	}
	// Verify every entry in tail forms a single valid chain rooted at nil
	prev := any(nil)
	for i, r := range tailRecords[1:] {
		if r["parentId"] != prev {
			t.Fatalf("line %d (%v): expected parentId=%v, got %v", i+1, r["id"], prev, r["parentId"])
		}
		prev = r["id"]
	}
}

func readJSONL(t *testing.T, path string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("invalid jsonl line: %v", err)
		}
		out = append(out, m)
	}
	return out
}
