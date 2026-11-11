package readashboard

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDecodeToolEventOnlyREA(t *testing.T) {
	if _, ok := decodeToolEvent(map[string]any{"toolName": "bash", "toolCallId": "1"}); ok {
		t.Fatal("bash accepted")
	}
	if _, ok := decodeToolEvent(map[string]any{"toolName": "mcp__other__x", "toolCallId": "1"}); ok {
		t.Fatal("another server's tool accepted")
	}
	if _, ok := decodeToolEvent(map[string]any{"toolName": "mcp__rea__", "toolCallId": "1"}); ok {
		t.Fatal("empty tool name accepted")
	}
	ev, ok := decodeToolEvent(map[string]any{
		"toolName": "mcp__rea__inspect_artifact", "toolCallId": "c/1", "parentToolCallId": "c",
		"args": map[string]any{"path": "/bin/ls"}, "isError": true, "durationMs": 42.0,
	})
	if !ok || ev.Tool != "inspect_artifact" || ev.ID != "c/1" || ev.Parent != "c" || !ev.IsError || ev.DurationMs == nil || *ev.DurationMs != 42 {
		t.Fatalf("event = %+v ok=%v", ev, ok)
	}
}

func TestStoreStartUpdateEnd(t *testing.T) {
	s := newStore()
	start := fixedNow
	ev := toolEvent{ID: "a", Tool: "inspect_artifact", Args: map[string]any{"path": "/bin/ls", "detail": "summary"}}
	s.start(ev, start)
	calls := s.snapshot()
	if len(calls) != 1 || calls[0].Status != statusRunning || calls[0].Target != "/bin/ls" || calls[0].Args != "path=/bin/ls detail=summary" {
		t.Fatalf("after start: %+v", calls)
	}

	// An update changes nothing but must not duplicate the call.
	s.update(ev, start.Add(time.Second))
	if len(s.snapshot()) != 1 {
		t.Fatalf("update duplicated the call")
	}

	ms := int64(1500)
	ev.Result, ev.DurationMs = textResult(resultJSON), &ms
	entry := s.end(ev, start.Add(2*time.Second))
	c := s.snapshot()[0]
	if c.Status != statusOK || c.Duration != 1500*time.Millisecond || !c.Counted || c.Evidence != 2 || c.Limitations != 2 {
		t.Fatalf("after end: %+v", c)
	}
	if !entry.OK || entry.DurationMs != 1500 || entry.Evidence == nil || *entry.Evidence != 2 || entry.Limitations == nil || *entry.Limitations != 2 {
		t.Fatalf("entry = %+v", entry)
	}
	if got := c.EvidenceSample; len(got) != 2 || got[0] != "ev_aaa" {
		t.Fatalf("evidence sample = %v", got)
	}
}

func TestStoreNestedCallKeepsParent(t *testing.T) {
	s := newStore()
	s.start(toolEvent{ID: "p/1", Parent: "p", Tool: "open_binary", Args: map[string]any{"path": "/x"}}, fixedNow)
	s.start(toolEvent{ID: "p/2", Parent: "p", Tool: "list_strings", Args: map[string]any{"path": "/x"}}, fixedNow)
	calls := s.snapshot()
	if len(calls) != 2 || !calls[0].nested() || calls[1].Parent != "p" {
		t.Fatalf("calls = %+v", calls)
	}
	if running, _, _ := counts(calls); running != 2 {
		t.Fatalf("running = %d", running)
	}
}

func TestStoreErrorEnd(t *testing.T) {
	s := newStore()
	ev := toolEvent{ID: "e", Tool: "decompile", IsError: true, Result: textResult(`{"error":{"code":"no_session","message":"open a binary first"}}`)}
	s.start(ev, fixedNow)
	entry := s.end(ev, fixedNow.Add(time.Second))
	c := s.snapshot()[0]
	if c.Status != statusError || c.Error != "open a binary first" || c.Counted {
		t.Fatalf("call = %+v", c)
	}
	if entry.OK || entry.Error != "open a binary first" || entry.Evidence != nil {
		t.Fatalf("entry = %+v", entry)
	}
	if _, _, failed := counts(s.snapshot()); failed != 1 {
		t.Fatal("failed count")
	}
}

func TestStoreEndWithoutStartUsesDuration(t *testing.T) {
	s := newStore()
	ms := int64(2000)
	s.end(toolEvent{ID: "late", Tool: "x", Args: map[string]any{"path": "/a"}, DurationMs: &ms, Result: textResult("{}")}, fixedNow)
	c := s.snapshot()[0]
	if c.Status != statusOK || !c.Started.Equal(fixedNow.Add(-2*time.Second)) || c.Duration != 2*time.Second || c.Target != "/a" {
		t.Fatalf("call = %+v", c)
	}
}

func TestStoreBoundsTheTimelineButKeepsRunningCalls(t *testing.T) {
	s := newStore()
	s.start(toolEvent{ID: "running", Tool: "x"}, fixedNow)
	for i := range maxCalls + 10 {
		ev := toolEvent{ID: "c" + strings.Repeat("x", 1) + string(rune('A'+i%26)) + time.Duration(i).String(), Tool: "x"}
		s.start(ev, fixedNow)
		s.end(ev, fixedNow)
	}
	calls := s.snapshot()
	if len(calls) > maxCalls {
		t.Fatalf("%d calls", len(calls))
	}
	if calls[0].ID != "running" {
		t.Fatalf("the running call was dropped: first = %s", calls[0].ID)
	}
}

func TestEntryIsBounded(t *testing.T) {
	long := strings.Repeat("é", 5000)
	c := call{ID: "i", Tool: "t", Args: long, Target: long, Status: statusError, Error: long, Counted: true,
		EvidenceSample: []string{long}, LimitationSample: []string{long}}
	// Samples are cut when summarized; entry() bounds args, target and error itself.
	e := c.entry()
	if n := len([]rune(e.Args)); n > maxArgsLen {
		t.Fatalf("args %d runes", n)
	}
	if n := len([]rune(e.Target)); n > maxTargetLen {
		t.Fatalf("target %d runes", n)
	}
	if n := len([]rune(e.Error)); n > maxErrorLen {
		t.Fatalf("error %d runes", n)
	}
	sum := summarizeResult(textResult(`{"limitations":["` + strings.Repeat("l", 1000) + `","b","c","d","e"],"evidence":["1","2","3","4"]}`))
	if len(sum.LimitationSample) != maxSamples || len(sum.EvidenceSample) != maxSamples || sum.Limitations != 5 || sum.Evidence != 4 {
		t.Fatalf("summary = %+v", sum)
	}
	if n := len([]rune(sum.LimitationSample[0])); n > maxSampleLen {
		t.Fatalf("sample %d runes", n)
	}
}

func rawEntry(id, customType string, data any) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"type": "custom", "id": id, "customType": customType, "data": data})
	return raw
}

func TestRestoreRebuildsTimelineFromEntries(t *testing.T) {
	a := call{ID: "a", Tool: "open_binary", Target: "/bin/ls", Status: statusOK, Started: fixedNow, Duration: time.Second}
	b := call{ID: "b", Parent: "p", Tool: "inspect_artifact", Target: "/bin/ls", Status: statusError, Error: "boom", Counted: true, Evidence: 1, Limitations: 3,
		LimitationSample: []string{"one"}, Started: fixedNow.Add(time.Second)}
	entries := []json.RawMessage{
		json.RawMessage(`{"type":"message","id":"m1"}`),
		rawEntry("e1", entryType, a.entry()),
		rawEntry("e2", "other.type", a.entry()),
		rawEntry("e3", entryType, b.entry()),
		rawEntry("e4", entryType, a.entry()), // the same call id again
		rawEntry("e5", entryType, map[string]any{"v": 1}),
		json.RawMessage(`not json`),
	}
	s := newStore()
	s.restore(entries, nil)
	calls := s.snapshot()
	if len(calls) != 2 {
		t.Fatalf("restored %d calls: %+v", len(calls), calls)
	}
	if calls[0].ID != "a" || !calls[0].Restored || calls[0].Status != statusOK || calls[0].Counted || !calls[0].Started.Equal(fixedNow) {
		t.Fatalf("a = %+v", calls[0])
	}
	if calls[1].Status != statusError || !calls[1].Counted || calls[1].Evidence != 1 || calls[1].Limitations != 3 || calls[1].Parent != "p" || calls[1].LimitationSample[0] != "one" || calls[1].Error != "boom" {
		t.Fatalf("b = %+v", calls[1])
	}
}

func TestRestoreFiltersByBranchAndKeepsRunningCalls(t *testing.T) {
	a := call{ID: "a", Tool: "x", Status: statusOK}
	b := call{ID: "b", Tool: "x", Status: statusOK}
	entries := []json.RawMessage{rawEntry("e1", entryType, a.entry()), rawEntry("e2", entryType, b.entry())}
	s := newStore()
	s.start(toolEvent{ID: "live", Tool: "y"}, fixedNow)
	s.restore(entries, func(id string) bool { return id == "e2" })
	calls := s.snapshot()
	if len(calls) != 2 || calls[0].ID != "b" || calls[1].ID != "live" || calls[1].Status != statusRunning {
		t.Fatalf("calls = %+v", calls)
	}
	// Restoring again replaces, not duplicates.
	s.restore(entries, nil)
	if got := s.snapshot(); len(got) != 3 {
		t.Fatalf("second restore: %+v", got)
	}
	// A running call that finished and was persisted is not kept twice.
	s.end(toolEvent{ID: "live", Tool: "y", Result: textResult("{}")}, fixedNow)
	live := s.snapshot()[2]
	s.restore([]json.RawMessage{rawEntry("e9", entryType, live.entry())}, nil)
	if got := s.snapshot(); len(got) != 1 || got[0].ID != "live" {
		t.Fatalf("after persisted: %+v", got)
	}
}

func TestExtractTarget(t *testing.T) {
	for _, tc := range []struct {
		name string
		args map[string]any
		want string
	}{
		{"path", map[string]any{"path": "/bin/ls", "detail": "x"}, "/bin/ls"},
		{"priority", map[string]any{"url": "https://a.test", "path": "/p"}, "/p"},
		{"input_path", map[string]any{"input_path": " /a/b "}, "/a/b"},
		{"url", map[string]any{"url": "https://example.test/app.js"}, "https://example.test/app.js"},
		{"application", map[string]any{"application": "/Applications/Foo.app"}, "/Applications/Foo.app"},
		{"fallback path-like", map[string]any{"zzz": "/opt/x", "aaa": 3.0, "mode": "fast"}, "/opt/x"},
		{"fallback url-like", map[string]any{"endpoint": "wss://host/x"}, "wss://host/x"},
		{"windows path", map[string]any{"thing": `C:\tools\a.exe`}, `C:\tools\a.exe`},
		{"none", map[string]any{"limit": 5.0, "mode": "fast"}, ""},
		{"empty string skipped", map[string]any{"path": "  ", "file": "/f"}, "/f"},
		{"nil", nil, ""},
	} {
		if got := extractTarget(tc.args); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestSummarizeArgs(t *testing.T) {
	got := summarizeArgs(map[string]any{"limit": 5.0, "path": "/bin/ls", "items": []any{1, 2}, "opts": map[string]any{"a": 1}, "on": true})
	if got != "path=/bin/ls items=[2 items] limit=5 on=true opts={1 keys}" {
		t.Fatalf("got %q", got)
	}
	if n := len([]rune(summarizeArgs(map[string]any{"path": strings.Repeat("a", 1000)}))); n > maxArgsLen {
		t.Fatalf("%d runes", n)
	}
}

func TestSummarizeResultShapes(t *testing.T) {
	// structuredContent wins and is unwrapped from {result: …}.
	sum := summarizeResult(map[string]any{
		"content":           []any{map[string]any{"type": "text", "text": "ignored"}},
		"structuredContent": map[string]any{"result": map[string]any{"evidence": []any{map[string]any{"evidence_id": "e1"}}, "limitations": []any{map[string]any{"message": "m1"}}}},
	})
	if !sum.Counted || sum.Evidence != 1 || sum.Limitations != 1 || sum.EvidenceSample[0] != "e1" || sum.LimitationSample[0] != "m1" {
		t.Fatalf("structured: %+v", sum)
	}
	// A bare Evidence bundle, as `inspect-artifact --json` prints.
	sum = summarizeResult(textResult(string(readTestdata(t, "inspect-artifact.json"))))
	if !sum.Counted || sum.Evidence != 1 || sum.Limitations != 1 || !strings.HasPrefix(sum.EvidenceSample[0], "ev_") {
		t.Fatalf("bundle: %+v", sum)
	}
	// A result with neither field, a result the host cut in the middle, and no result at all have no counts.
	for _, r := range []any{textResult(`{"result":{"path":"/x"}}`), textResult(`{"evidence":[{"a":1}` + "\n… [20000 bytes omitted] …\n"), nil, "string"} {
		if got := summarizeResult(r); got.Counted {
			t.Fatalf("%v counted: %+v", r, got)
		}
	}
	// An error object gives the message.
	if got := summarizeResult(textResult(`{"error":"Analysis failed","code":"x"}`)); got.Error != "Analysis failed" {
		t.Fatalf("error = %q", got.Error)
	}
}

func TestTargetsAreDistinctAndMostRecentFirst(t *testing.T) {
	d, _ := newTestDashboard(t)
	addCalls(d)
	ts := targets(d.store.snapshot())
	if len(ts) != 2 {
		t.Fatalf("targets = %+v", ts)
	}
	if ts[0].Value != "/Applications/Foo.app" || ts[0].Count != 1 || ts[1].Value != "/bin/ls" || ts[1].Count != 2 {
		t.Fatalf("targets = %+v", ts)
	}
	if !ts[1].local() || ts[1].Tools[0] != "open_binary" {
		t.Fatalf("local/tools: %+v", ts[1])
	}
	if (target{Value: "https://a.test/x"}).local() || (target{Value: "-rf"}).local() {
		t.Fatal("url or option counted as local")
	}
}

func TestToolPrefixHelpers(t *testing.T) {
	if family("inspect_artifact") != "inspect" || family("capabilities") != "capabilities" {
		t.Fatal("family")
	}
	if toolLabel("inspect_artifact") != "inspect/inspect_artifact" || toolLabel("capabilities") != "capabilities" {
		t.Fatal("toolLabel")
	}
}
