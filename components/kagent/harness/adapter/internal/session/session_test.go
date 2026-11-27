package session

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/components/kagent/harness/adapter/internal/journal"
)

var fixedNow = time.Date(2026, 11, 7, 1, 40, 0, 123_000_000, time.UTC)

// parkedFixture copies the golden session recorded with the PiG v0.7.0 release
// (the file a parked approval leaves) into a temp dir.
func parkedFixture(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("testdata/basket-v0.7.0-parked.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "2026-11-07T01-35-56-101Z_11111111-2222-4333-8444-555555555555.jsonl")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const (
	goldenAssistant = "2cabb1af"
	goldenCall      = "call_e0646e2186c9e3207c49"
)

func lines(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte{'\n'}) {
		var entry map[string]any
		if json.Unmarshal(line, &entry) == nil {
			out = append(out, entry)
		}
	}
	return out
}

func approved(text string) []Result {
	return []Result{{CallID: goldenCall, Name: "bash", Out: TextOutcome(text, false)}}
}

func TestGoldenParkedSessionShape(t *testing.T) {
	file, err := Scan(parkedFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	tail, err := file.ParkedTail()
	if err != nil {
		t.Fatal(err)
	}
	if file.Version != 3 || tail.ID != goldenAssistant || tail.StopReason != "toolUse" || len(tail.Calls) != 1 || tail.Calls[0].ID != goldenCall || tail.Calls[0].Name != "bash" {
		t.Fatalf("tail = %+v version %d", tail, file.Version)
	}
	var args map[string]any
	if err := json.Unmarshal(tail.Calls[0].Arguments, &args); err != nil || args["command"] == nil {
		t.Fatalf("arguments = %s", tail.Calls[0].Arguments)
	}
}

func TestInjectAppendsOneEntryInPiGsShape(t *testing.T) {
	path := parkedFixture(t)
	before, _ := os.ReadFile(path)
	n, err := Inject(path, goldenAssistant, approved("ADAPTER-EXECUTED-RESULT"), fixedNow)
	if err != nil || n != 1 {
		t.Fatalf("Inject = %d, %v", n, err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.HasPrefix(after, before) {
		t.Fatal("inject rewrote existing session bytes")
	}
	entries := lines(t, path)
	got := entries[len(entries)-1]
	message := got["message"].(map[string]any)
	if got["type"] != "message" || got["parentId"] != goldenAssistant || got["timestamp"] != "2026-11-07T01:40:00.123Z" ||
		message["role"] != "toolResult" || message["toolCallId"] != goldenCall || message["toolName"] != "bash" || message["isError"] != false ||
		message["timestamp"] != float64(fixedNow.UnixMilli()) {
		t.Fatalf("entry = %v", got)
	}
	content := message["content"].([]any)[0].(map[string]any)
	if content["type"] != "text" || content["text"] != "ADAPTER-EXECUTED-RESULT" {
		t.Fatalf("content = %v", content)
	}
	if id, _ := got["id"].(string); len(id) != 8 {
		t.Fatalf("id = %q", id)
	}
	// the real PiG toolResult entry in the full golden file has the same keys
	full := lines(t, "testdata/basket-v0.7.0.jsonl")
	real := full[6]["message"].(map[string]any)
	for key := range message {
		if _, ok := real[key]; !ok && key != "timestamp" {
			t.Errorf("injected key %q is not in PiG's own toolResult entry", key)
		}
	}
	// the new tail is readable and follows the chain
	file, err := Scan(path)
	if err != nil || len(file.After) != 1 || file.After[0].ParentID != goldenAssistant {
		t.Fatalf("scan = %+v, %v", file, err)
	}
}

func TestInjectIsIdempotent(t *testing.T) {
	path := parkedFixture(t)
	for i, want := range []int{1, 0, 0} {
		n, err := Inject(path, goldenAssistant, approved("x"), fixedNow)
		if err != nil || n != want {
			t.Fatalf("call %d = %d, %v want %d", i, n, err, want)
		}
	}
	if got := len(lines(t, path)); got != 7 {
		t.Fatalf("%d lines, want 7 (6 + one result)", got)
	}
}

// batchFixture rewrites the golden assistant entry to carry three calls.
func batchFixture(t *testing.T) (string, []Result) {
	t.Helper()
	path := parkedFixture(t)
	data, _ := os.ReadFile(path)
	rows := bytes.Split(bytes.TrimRight(data, "\n"), []byte{'\n'})
	var assistant map[string]any
	if err := json.Unmarshal(rows[5], &assistant); err != nil {
		t.Fatal(err)
	}
	message := assistant["message"].(map[string]any)
	message["content"] = []any{
		map[string]any{"type": "toolCall", "id": "c1", "name": "echo", "arguments": map[string]any{}},
		map[string]any{"type": "toolCall", "id": "c2", "name": "mcp__kb__delete_all", "arguments": map[string]any{"x": 1}},
		map[string]any{"type": "toolCall", "id": "c3", "name": "echo", "arguments": map[string]any{}},
	}
	rows[5], _ = json.Marshal(assistant)
	if err := os.WriteFile(path, append(bytes.Join(rows, []byte{'\n'}), '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	notRun := TextOutcome(NotExecutedText(journal.KindApproval), true)
	return path, []Result{
		{CallID: "c1", Name: "echo", Out: notRun},
		{CallID: "c2", Name: "mcp__kb__delete_all", Out: TextOutcome("deleted", false)},
		{CallID: "c3", Name: "echo", Out: notRun},
	}
}

func TestInjectBatchReportsSiblingsNotExecutedInCallOrder(t *testing.T) {
	path, results := batchFixture(t)
	n, err := Inject(path, goldenAssistant, results, fixedNow)
	if err != nil || n != 3 {
		t.Fatalf("Inject = %d, %v", n, err)
	}
	entries := lines(t, path)[6:]
	parent := goldenAssistant
	for i, id := range []string{"c1", "c2", "c3"} {
		message := entries[i]["message"].(map[string]any)
		if message["toolCallId"] != id || entries[i]["parentId"] != parent {
			t.Fatalf("entry %d = %v, parent %s", i, entries[i], parent)
		}
		parent = entries[i]["id"].(string)
	}
	sibling := entries[0]["message"].(map[string]any)
	text := sibling["content"].([]any)[0].(map[string]any)["text"].(string)
	if sibling["isError"] != true || !strings.Contains(text, "Not executed") || !strings.Contains(text, "approval") {
		t.Fatalf("sibling = %v", sibling)
	}
	if entries[1]["message"].(map[string]any)["isError"] != false {
		t.Fatal("the resolved call must not be an error")
	}
	if !strings.Contains(NotExecutedText(journal.KindAskUser), "question") {
		t.Fatal("ask_user text must say question")
	}
}

// TestInjectSurvivesACrashAtEveryByteOffset is the crash-window test for the
// session write: the append is cut after every byte, then retried.
func TestInjectSurvivesACrashAtEveryByteOffset(t *testing.T) {
	_, results := batchFixture(t)
	reference, _ := batchFixture(t)
	if _, err := Inject(reference, goldenAssistant, results, fixedNow); err != nil {
		t.Fatal(err)
	}
	full, _ := os.ReadFile(reference)
	start := bytes.Index(full, []byte(`"toolCallId":"c1"`))
	start = bytes.LastIndex(full[:start], []byte{'\n'}) + 1
	for cut := start; cut <= len(full); cut++ {
		path, results := batchFixture(t)
		original, _ := os.ReadFile(path)
		if err := os.WriteFile(path, append(append([]byte{}, original...), full[start:cut]...), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Inject(path, goldenAssistant, results, fixedNow); err != nil {
			t.Fatalf("cut %d: retry failed: %v", cut, err)
		}
		file, err := Scan(path)
		if err != nil {
			t.Fatalf("cut %d: %v", cut, err)
		}
		seen := map[string]int{}
		for _, entry := range file.After {
			seen[entry.ToolCallID]++
		}
		if len(file.After) != 3 || seen["c1"] != 1 || seen["c2"] != 1 || seen["c3"] != 1 {
			t.Fatalf("cut %d: results = %v (%d entries)", cut, seen, len(file.After))
		}
		if _, err := Inject(path, goldenAssistant, results, fixedNow); err != nil {
			t.Fatalf("cut %d: second retry: %v", cut, err)
		}
		if again, _ := Scan(path); len(again.After) != 3 {
			t.Fatalf("cut %d: not idempotent: %d entries", cut, len(again.After))
		}
	}
}

func TestInjectFailsClosed(t *testing.T) {
	mutate := func(t *testing.T, edit func(rows [][]byte) [][]byte) string {
		t.Helper()
		path := parkedFixture(t)
		data, _ := os.ReadFile(path)
		rows := edit(bytes.Split(bytes.TrimRight(data, "\n"), []byte{'\n'}))
		if err := os.WriteFile(path, append(bytes.Join(rows, []byte{'\n'}), '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	tests := []struct {
		name string
		edit func(rows [][]byte) [][]byte
		id   string
	}{
		{"header version 2", func(rows [][]byte) [][]byte {
			rows[0] = bytes.Replace(rows[0], []byte(`"version":3`), []byte(`"version":2`), 1)
			return rows
		}, goldenAssistant},
		{"header version 4", func(rows [][]byte) [][]byte {
			rows[0] = bytes.Replace(rows[0], []byte(`"version":3`), []byte(`"version":4`), 1)
			return rows
		}, goldenAssistant},
		{"unknown assistant entry", func(rows [][]byte) [][]byte { return rows }, "deadbeef"},
		{"file moved on with a user message", func(rows [][]byte) [][]byte {
			return append(rows, []byte(`{"type":"message","id":"aaaaaaaa","parentId":"`+goldenAssistant+`","timestamp":"2026-11-07T01:36:00.000Z","message":{"role":"user","content":[{"type":"text","text":"hi"}],"timestamp":1}}`))
		}, goldenAssistant},
		{"foreign toolResult after the assistant", func(rows [][]byte) [][]byte {
			return append(rows, []byte(`{"type":"message","id":"aaaaaaaa","parentId":"`+goldenAssistant+`","timestamp":"2026-11-07T01:36:00.000Z","message":{"role":"toolResult","toolCallId":"other","toolName":"x","content":[],"isError":false,"timestamp":1}}`))
		}, goldenAssistant},
		{"result with the wrong parent", func(rows [][]byte) [][]byte {
			return append(rows, []byte(`{"type":"message","id":"aaaaaaaa","parentId":"zzzz","timestamp":"2026-11-07T01:36:00.000Z","message":{"role":"toolResult","toolCallId":"`+goldenCall+`","toolName":"bash","content":[],"isError":false,"timestamp":1}}`))
		}, goldenAssistant},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := mutate(t, tc.edit)
			before, _ := os.ReadFile(path)
			if _, err := Inject(path, tc.id, approved("x"), fixedNow); err == nil {
				t.Fatal("Inject succeeded")
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Fatal("a refused inject modified the file")
			}
		})
	}
}

func TestInjectRejectsMissingOrForeignResults(t *testing.T) {
	path, results := batchFixture(t)
	if _, err := Inject(path, goldenAssistant, results[:2], fixedNow); err == nil {
		t.Fatal("a missing result for an unresolved call was accepted")
	}
	if _, err := Inject(path, goldenAssistant, append(results, Result{CallID: "nope"}), fixedNow); err == nil {
		t.Fatal("a result for a call outside the message was accepted")
	}
	if n := len(lines(t, path)); n != 6 {
		t.Fatalf("file changed: %d lines", n)
	}
}

func TestScanToleratesATornTail(t *testing.T) {
	path := parkedFixture(t)
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"type":"message","id":"abcd`)
	f.Close()
	file, err := Scan(path)
	if err != nil || !file.NeedsNewline {
		t.Fatalf("scan = %+v, %v", file, err)
	}
	if _, err := file.ParkedTail(); err != nil {
		t.Fatalf("a torn fragment must not hide the assistant tail: %v", err)
	}
}

func TestScanRejectsBrokenFiles(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"empty":     "",
		"no header": `{"type":"message","id":"a","parentId":null}` + "\n",
		"torn head": `{"type":"session","ver`,
	} {
		path := filepath.Join(dir, name+".jsonl")
		os.WriteFile(path, []byte(content), 0o600)
		if _, err := Scan(path); err == nil {
			t.Errorf("%s: Scan succeeded", name)
		}
	}
}

func TestParkedTailRefusesOtherShapes(t *testing.T) {
	full, err := Scan("testdata/basket-v0.7.0.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := full.ParkedTail(); err == nil {
		t.Fatal("a finished session is not a parked tail")
	}
}

func TestFindAssertsExactlyOneFile(t *testing.T) {
	dir := t.TempDir()
	id := "11111111-2222-4333-8444-555555555555"
	if _, found, err := Find(dir, id); found || err != nil {
		t.Fatalf("empty dir = %v, %v", found, err)
	}
	first := filepath.Join(dir, "2026-11-07T01-35-56-101Z_"+id+".jsonl")
	os.WriteFile(first, []byte("{}\n"), 0o600)
	if path, found, err := Find(dir, id); !found || err != nil || path != first {
		t.Fatalf("one file = %q, %v, %v", path, found, err)
	}
	os.WriteFile(filepath.Join(dir, "2026-11-07T02-00-00-000Z_"+id+".jsonl"), []byte("{}\n"), 0o600)
	if _, _, err := Find(dir, id); err == nil || !strings.Contains(err.Error(), "forked") {
		t.Fatalf("two files = %v", err)
	}
	os.WriteFile(filepath.Join(dir, "x_other.jsonl"), []byte("{}\n"), 0o600)
	if _, _, err := Find(dir, "other"); err != nil {
		t.Fatalf("other id: %v", err)
	}
}

func TestValidID(t *testing.T) {
	for id, ok := range map[string]bool{
		"11111111-2222-4333-8444-555555555555": true, "a": true, "a.b_c-d": true, "A1": true,
		"": false, "-a": false, "a-": false, "a/b": false, "..": false, "a b": false, "é": false, strings.Repeat("a", 129): false, "a;b": false,
	} {
		if err := ValidID(id); (err == nil) != ok {
			t.Errorf("ValidID(%q) = %v, want ok=%v", id, err, ok)
		}
	}
}
