package tintinweb_subagents

import (
	"bufio"
	"math"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The transcript stream against Pi's own event order, the memory an agent's record keeps, what a stopped or failed
// agent still reports, the schema file, and the rounding of toFixed.

// piTurnEvents are the events the host emitted for one prompt in a run under Pi 1402973a: the "host" channel of
// port/golden/obs-agent-file-output-transcript.jsonl, copied here so the mutation runs (which copy only this
// directory) can read it; TestPiTurnEventsAreTheGoldensHostChannel keeps the copy honest. The golden shows Pi's
// session putting a system message first: `{"role":"system","content":""}`.
func piTurnEvents(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	must(t, err)
	defer f.Close()
	var out []map[string]any
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var line map[string]any
		must(t, json.Unmarshal(sc.Bytes(), &line))
		if data, ok := line["data"].(map[string]any); ok && line["ch"] == "host" { // a golden line
			out = append(out, data)
		} else if line["ch"] == nil && line["kind"] == nil { // a testdata line is the event itself
			out = append(out, line)
		}
	}
	must(t, sc.Err())
	return out
}

func TestPiTurnEventsAreTheGoldensHostChannel(t *testing.T) {
	golden := filepath.Join("..", "..", "port", "golden", "obs-agent-file-output-transcript.jsonl")
	if _, err := os.Stat(golden); err != nil {
		t.Skip("the golden is not next to this directory (a copy of the module)")
	}
	eq(t, piTurnEvents(t, filepath.Join("testdata", "pi-1402973a-turn-events.jsonl")), piTurnEvents(t, golden))
}

func TestTranscriptOfPisEventOrderMatchesTheOriginal(t *testing.T) {
	// streamToOutputFile starts at index 1 of session.messages. Under Pi the array is [system, user, assistant]
	// (the golden's order), so the original writes the user prompt a second time, then the assistant message, and
	// never the system message at index 0.
	events := piTurnEvents(t, filepath.Join("testdata", "pi-1402973a-turn-events.jsonl"))
	kinds := []any{}
	for _, e := range events {
		kinds = append(kinds, e["type"])
	}
	eq(t, kinds, []any{"agent_start", "turn_start", "message_end", "message_end", "message_end", "turn_end", "agent_end"})
	eq(t, events[2]["message"].(map[string]any)["role"], "system")

	path := filepath.Join(t.TempDir(), "agent.output")
	writeInitialEntry(path, "agent-1", "hello", "/work")
	stream := newTranscriptStream(path, "agent-1", "/work", 1)
	for _, e := range events {
		stream.onEvent(e)
	}
	stream.close()
	entries := readEntries(t, path)
	var got []any
	for _, e := range entries {
		got = append(got, []any{e["type"], e["message"].(map[string]any)["role"]})
	}
	eq(t, got, []any{[]any{"user", "user"}, []any{"user", "user"}, []any{"assistant", "assistant"}})
	eq(t, entries[1]["message"], events[3]["message"]) // the prompt again, as the session holds it
	eq(t, entries[2]["message"], events[4]["message"])
}

func TestALaterSystemMessageIsATranscriptEntryOfTypeToolResult(t *testing.T) {
	// The original types every role that is neither assistant nor user as toolResult, a system message included.
	s, path := streamFixture(t, 1, user("do the thing"))
	s.push(obj{"role": "system", "content": "reminder"}, assistant("ok"))
	s.fire(obj{"type": "turn_end"})
	entries := readEntries(t, path)
	eq(t, len(entries), 3)
	eq(t, entries[1]["type"], "toolResult")
	eq(t, entries[1]["message"], any(obj{"role": "system", "content": "reminder"}))
	eq(t, entries[2]["type"], "assistant")
}

func TestTheStreamKeepsOnlyWhatIsNotWrittenYet(t *testing.T) {
	s, path := streamFixture(t, 1, user("go"))
	for range 500 {
		s.push(assistant(strings.Repeat("x", 1000)))
		s.fire(obj{"type": "turn_end"})
		eq(t, len(s.stream.tail), 0)
	}
	eq(t, len(readEntries(t, path)), 501)
	s.push(assistant("tail"))
	eq(t, len(s.stream.tail), 1)
	s.stream.close()
	eq(t, len(s.stream.tail), 0)
	s.push(assistant("late")) // after the close nothing is kept
	eq(t, len(s.stream.tail), 0)
}

func TestACompactionDropsWhatWasSeenSinceTheFlush(t *testing.T) {
	s, _ := streamFixture(t, 1, user("go"))
	s.fire(obj{"type": "compaction_start", "reason": "manual"})
	s.push(assistant("old array"))
	eq(t, len(s.stream.tail), 1)
	s.fire(obj{"type": "compaction_end", "reason": "manual", "aborted": false, "result": obj{"summary": "s"}})
	eq(t, len(s.stream.tail), 0)
}

func TestARecordLetsGoOfItsTranscriptAfterTheRun(t *testing.T) {
	r := startObsRig(t, obsWorkspace(t, "", ""))
	n := r.fleet.count()
	id := idFrom(t, r.must("Agent", obj{"prompt": "go", "description": "d", "subagent_type": "general-purpose", "run_in_background": true}))
	c := r.fleet.wait(t, n+1)[n]
	rec := r.app.mgr.get(id)
	c.spec.Hooks.Event(obj{"type": "message_end", "message": user("go")})
	c.spec.Hooks.Event(obj{"type": "message_end", "message": assistant("answer")})
	c.finish("done")
	<-rec.done
	r.app.mgr.mu.Lock()
	kept := rec.transcript
	r.app.mgr.mu.Unlock()
	eq(t, kept == nil, true)
	eq(t, rec.OutputFile != "", true)
	entries := readEntries(t, rec.OutputFile)
	eq(t, len(entries), 2) // the initial entry, and the unflushed tail written by the final flush
	eq(t, strings.Contains(string(jsonLine(entries[1])), "answer"), true)
}

func TestTheConversationKeptIsBounded(t *testing.T) {
	var msgs []map[string]any
	for range 40 {
		msgs = append(msgs, assistant(strings.Repeat("a", 100_000)))
	}
	got := conversationOf(msgs)
	eq(t, len(got) <= maxConversationBytes+len("[earlier messages omitted]\n\n"), true)
	eq(t, strings.HasPrefix(got, "[earlier messages omitted]\n\n[Assistant]: "), true)
	eq(t, strings.HasSuffix(got, strings.Repeat("a", 100_000)), true)
	small := conversationOf([]map[string]any{user("hi"), assistant("yo")})
	eq(t, small, "[User]: hi\n\n[Assistant]: yo")
}

// a child that has said something and then stalls or dies still has a conversation, and a stopped one a context percent.
func TestAStoppedAgentKeepsItsConversationAndContextPercent(t *testing.T) {
	useFakePig(t, "hang-partial")
	settled := make(chan *agentRecord, 1)
	m := newManager(startRPCChild, func(r *agentRecord) { settled <- r })
	r := m.spawn("general-purpose", "d", "", childSpec{Prompt: "the task", Cwd: t.TempDir()}, true)
	<-r.started
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		c, _ := r.child.(*rpcChild)
		m.mu.Unlock()
		if c != nil {
			if conv, _ := c.snapshot(); strings.Contains(conv, "listing") {
				break
			}
		}
		sleepMs(10)
	}
	eq(t, m.abort(r), true)
	<-r.done
	eq(t, r.Status, statusStopped)
	m.mu.Lock()
	defer m.mu.Unlock()
	eq(t, r.Conversation, "[User]: the task\n\n[Assistant]: partial findings\n\n[Tool Calls]:\n  Tool: bash\n\n[Tool Result (bash)]: listing")
	if r.ContextPercent == nil {
		t.Fatal("no context percent")
	}
	eq(t, *r.ContextPercent, 10.4)
	eq(t, <-settled == r, true)
}

func TestAFailedAgentKeepsItsConversation(t *testing.T) {
	useFakePig(t, "die-partial")
	m := newManager(startRPCChild, nil)
	r := m.spawn("general-purpose", "d", "", childSpec{Prompt: "the task", Cwd: t.TempDir()}, true)
	<-r.done
	m.mu.Lock()
	defer m.mu.Unlock()
	eq(t, r.Status, statusError)
	eq(t, strings.HasPrefix(r.Conversation, "[User]: the task\n\n[Assistant]: partial findings"), true)
	eq(t, r.ContextPercent == nil, true) // the child died before it could be asked
}

func TestAChildReadsItsSchemaFromAPrivateFileOfAnyLength(t *testing.T) {
	schema := `{"type":"object","description":"` + strings.Repeat("x", 300_000) + `"}` // far past an environment string's cap
	seen := filepath.Join(t.TempDir(), "seen")
	useFakePig(t, "structured")
	t.Setenv("FAKE_PIG_SCHEMA", seen)
	c, err := startRPCChild(t.Context(), childSpec{Prompt: "p", Cwd: t.TempDir(), Schema: schema})
	eq(t, err, nil)
	_, _ = c.wait()
	b, _ := os.ReadFile(seen)
	eq(t, string(b), schema)
	c.close() // an in-memory conversation outlives its run: the process ends when the manager closes it
	for _, kv := range os.Environ() {
		eq(t, strings.HasPrefix(kv, structuredSchemaFileEnv+"="), false) // the parent's own environment is untouched
	}
	// The file is gone once the child has exited.
	matches, _ := filepath.Glob(filepath.Join(os.TempDir(), "pi-subagents-schema-*.json"))
	for i := 0; i < 200 && len(matches) > 0; i++ {
		sleepMs(10)
		matches, _ = filepath.Glob(filepath.Join(os.TempDir(), "pi-subagents-schema-*.json"))
	}
	eq(t, len(matches), 0)
}

func TestTheSchemaFileIsPrivate(t *testing.T) {
	path, err := writeSchemaFile(`{"type":"object"}`)
	eq(t, err, nil)
	defer os.Remove(path)
	st, err := os.Stat(path)
	eq(t, err, nil)
	if st.Mode().Perm()&0o077 != 0 && os.Getenv("GOOS") != "windows" && filepath.Separator == '/' {
		t.Fatalf("mode %v", st.Mode())
	}
}

func TestJSToFixedRoundsATieToTheLargerNeighbor(t *testing.T) {
	// JavaScript: (1.25).toFixed(1) is "1.3" and (0.03125).toFixed(4) is "0.0313"; Go's %f rounds these ties to even.
	for _, c := range []struct {
		in     float64
		digits int
		want   string
	}{
		{1.25, 1, "1.3"}, {math.Copysign(0, -1), 1, "0.0"}, {0.1234, 2, "0.12"}, {99.999, 2, "100.00"}, {0.03125, 4, "0.0313"}, {2.5, 0, "3"}, {0.5, 0, "1"}, {1.35, 1, "1.4"}, {1.005, 2, "1.00"},
		{0, 1, "0.0"}, {0.04, 1, "0.0"}, {0.05, 1, "0.1"}, {12345.678, 2, "12345.68"}, {0.0001, 4, "0.0001"}, {-1.25, 1, "-1.3"},
	} {
		eq(t, jsToFixed(c.in, c.digits), c.want)
	}
	eq(t, formatCost(0.03125), "~$0.0313")
	eq(t, formatTokens(1250), "1.3k token")
	eq(t, formatMs(1250), "1.3s")
	eq(t, jsToFixed(1.25, 1), "1.3")
}

func TestACostWithNoTokensIsStillReported(t *testing.T) {
	// toReportedUsage: nothing spent means nothing reported, and a price alone is something.
	eq(t, toReportedUsage(lifetimeUsage{}) == nil, true)
	got := toReportedUsage(lifetimeUsage{Cost: 0.002})
	if got == nil {
		t.Fatal("a cost with no tokens was dropped")
	}
	eq(t, got.Cost.Total, 0.002)
	eq(t, got.TotalTokens, 0)
	var pool pendingUsagePool
	pool.add(lifetimeUsage{Cost: 0.002})
	eq(t, pool.drain() != nil, true)
	eq(t, pool.drain() == nil, true)
}

func TestSettingsOfThisSliceShareTheSettingsReader(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", dir)
	must(t, os.WriteFile(filepath.Join(dir, "subagents.json"), []byte(`{"outputTranscript": false, "reportUsage": true, "showCost": "yes"}`), 0o644))
	cwd := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(cwd, ".pi"), 0o755))
	must(t, os.WriteFile(filepath.Join(cwd, ".pi", "subagents.json"), []byte(`{"outputTranscript": true}`), 0o644))
	s := loadSettings(cwd)
	eq(t, s.OutputTranscript != nil && *s.OutputTranscript, true) // the project file wins
	eq(t, s.ReportUsage != nil && *s.ReportUsage, true)           // the global file counts
	eq(t, s.ShowCost == nil, true)                                // a value of the wrong type is dropped
}

func TestWithoutASessionIdTheAgentRunsWithoutATranscript(t *testing.T) {
	r := startObsRigWith(t, obsWorkspace(t, "", ""), "no session")
	n := r.fleet.count()
	id := idFrom(t, r.must("Agent", obj{"prompt": "go", "description": "d", "subagent_type": "general-purpose", "run_in_background": true}))
	r.fleet.wait(t, n+1)[n].finish("done")
	rec := r.app.mgr.get(id)
	<-rec.done
	eq(t, rec.OutputFile, "")
	eq(t, rec.Status, statusCompleted)
}

func TestAnAbortedCompactionKeepsWhatWasSeenEvenWithAResult(t *testing.T) {
	s, _ := streamFixture(t, 1, user("go"))
	s.fire(obj{"type": "compaction_start", "reason": "manual"})
	s.push(assistant("kept"))
	s.fire(obj{"type": "compaction_end", "reason": "manual", "aborted": true, "result": obj{"summary": "s"}})
	eq(t, len(s.stream.tail), 1)
	s.fire(obj{"type": "compaction_end", "reason": "manual", "aborted": false, "errorMessage": "boom"})
	eq(t, len(s.stream.tail), 1)
}
