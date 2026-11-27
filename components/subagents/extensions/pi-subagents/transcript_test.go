package pi_subagents

// The child's live transcript and progress facts: the session JSONL a pinned child writes while it runs, parsed into
// the original's transcript events (its fleet-transcript.ts) and AgentProgress facts (its execution.ts).

import (
	"strings"
	"testing"
	"time"
)

// transcriptFixture is a child session: a task, one turn with a tool call and result, and the answer.
const transcriptFixture = `{"type":"session","id":"c1","version":3,"timestamp":"2024-01-01T00:00:00Z","cwd":"/w"}
{"type":"message","id":"m1","timestamp":"2024-01-01T00:00:01Z","message":{"role":"user","content":[{"type":"text","text":"Task: find it"}]}}
{"type":"message","id":"m2","timestamp":"2024-01-01T00:00:02Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"hmm"},{"type":"toolCall","id":"call_1","name":"read","arguments":{"path":"a.txt"}}],"stopReason":"toolUse","model":"p/m","usage":{"totalTokens":100}}}
{"type":"message","id":"m3","timestamp":"2024-01-01T00:00:03Z","message":{"role":"toolResult","toolCallId":"call_1","toolName":"read","content":[{"type":"text","text":"file body"}],"isError":false,"durationMs":900}}
{"type":"message","id":"m4","timestamp":"2024-01-01T00:00:04Z","message":{"role":"assistant","content":[{"type":"text","text":"found it"}],"stopReason":"stop","model":"p/m","usage":{"totalTokens":150}}}
{"type":"message","id":"m5","timestamp":"2024-01-01T00:00:05Z","message":{"role":"user","content":[{"type":"text","text":"thanks"}]}}
`

func TestSessionTranscriptEvents(t *testing.T) {
	dir := tmp(t)
	path := dir + "/1_c1.jsonl"
	writeFile(t, path, transcriptFixture)
	tr := readSessionTranscript(path)
	eq(t, tr.truncated, false, "not truncated")
	eq(t, tr.turns, 2, "two assistant messages are two turns")
	eq(t, tr.tokensKnown, true, "usage was reported")
	eq(t, tr.tokens, int64(250), "tokens sum")
	eq(t, tr.model, "p/m", "model")
	if len(tr.events) != 3 {
		t.Fatalf("events: %#v", tr.events)
	}
	e := tr.events
	eq(t, e[0].kind, "tool", "kind")
	eq(t, e[0].name, "read", "tool name")
	eq(t, e[0].args, "a.txt", "args preview")
	eq(t, e[0].path, "a.txt", "current path")
	eq(t, e[0].status, "complete", "the result completed the call")
	eq(t, e[0].output, "file body", "output")
	eq(t, e[0].endedAt, time.Date(2024, 1, 1, 0, 0, 3, 0, time.UTC), "ended at the result's entry")
	eq(t, e[1].kind, "assistant", "kind")
	eq(t, e[1].text, "found it", "assistant text")
	eq(t, e[1].model, "p/m", "assistant model")
	eq(t, e[2].kind, "user", "kind")
	eq(t, e[2].text, "thanks", "a supervisor message after the first response")
}

func TestSessionTranscriptSkipsMalformedAndIncompleteRecords(t *testing.T) {
	lines := []string{
		`{"type":"message","timestamp":"2024-01-01T00:00:01Z","message":{"role":"user","content":[{"type":"text","text":"Task: t"}]}}`,
		`{"type":"message","timestamp":"2024-01-01T00:00:02Z","message":{"role":"assistant","content":[{"type":"text","text":"on it"}],"stopReason":"stop"}}`,
		`not json`,
		`{"type":"usage","id":"u1"}`,
		`{"type":"message","timestamp":"2024-01-01T00:00:03Z","message":{"role":"user","content":[{"type":"text","text":"go on"}`,
	}
	tr := parseSessionEntries(lines)
	if len(tr.events) != 1 {
		t.Fatalf("events: %#v", tr.events)
	}
	eq(t, tr.events[0].text, "on it", "only the well-formed message is an event")
}

func TestSessionTranscriptLiveReadDropsThePartialTail(t *testing.T) {
	dir := tmp(t)
	path := dir + "/1_c1.jsonl"
	// the fixture with a record being written: its last line is cut mid-JSON
	writeFile(t, path, transcriptFixture+`{"type":"message","id":"m6","timest`)
	tr := readSessionTranscript(path)
	eq(t, len(tr.events), 3, "the half-written record is not an event yet")
}

func TestSessionTranscriptUserBeforeFirstResponseIsTheTask(t *testing.T) {
	lines := []string{
		`{"type":"message","message":{"role":"user","content":[{"type":"text","text":"Task: t"}]}}`,
		`{"type":"message","message":{"role":"user","content":[{"type":"text","text":"duplicated task"}]}}`,
		`{"type":"message","message":{"role":"assistant","content":[{"type":"text","text":"same"}],"stopReason":"stop"}}`,
		`{"type":"message","message":{"role":"assistant","content":[{"type":"text","text":"same"}],"stopReason":"stop"}}`,
	}
	tr := parseSessionEntries(lines)
	eq(t, len(tr.events), 1, "the task and a duplicated message are not events")
	eq(t, tr.events[0].text, "same", "the message")
}

func TestSessionTranscriptToolErrorAndNotice(t *testing.T) {
	lines := []string{
		`{"type":"message","timestamp":"2024-01-01T00:00:01Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"c1","name":"bash","arguments":{"command":"make it"}}],"stopReason":"toolUse"}}`,
		`{"type":"message","timestamp":"2024-01-01T00:00:02Z","message":{"role":"toolResult","toolCallId":"c1","toolName":"bash","content":[{"type":"text","text":"Permission denied\nstack omitted"}],"isError":true}}`,
		`{"type":"message","message":{"role":"assistant","content":[],"stopReason":"error","errorMessage":"Request failed"}}`,
	}
	tr := parseSessionEntries(lines)
	eq(t, len(tr.events), 2, "the tool and a notice")
	eq(t, tr.events[0].status, "error", "tool status")
	eq(t, tr.events[0].errText, "Permission denied", "the error is its first line")
	eq(t, tr.events[0].output, "Permission denied\nstack omitted", "the output is kept")
	eq(t, tr.events[0].args, "make it", "the bash preview is its command")
	eq(t, tr.events[0].path, "", "make has no file")
	eq(t, tr.events[1].kind, "notice", "a failed request is a notice")
	eq(t, tr.events[1].text, "Request failed", "the notice text")
}

func TestTranscriptFactsProgress(t *testing.T) {
	tr := readSessionTranscript(mustWrite(t, transcriptFixture))
	f := factsFromTranscript(tr)
	eq(t, f.turnCount, 2, "turns")
	eq(t, f.toolCount, 1, "tools")
	eq(t, f.currentTool, "", "no tool is running")
	eq(t, f.model, "p/m", "model")
	eq(t, f.tokensKnown, true, "tokens known")
	eq(t, f.tokens, int64(250), "tokens")
	eq(t, f.recentTools, []progressTool{{Tool: "read", Args: "a.txt", EndMs: time.Date(2024, 1, 1, 0, 0, 3, 0, time.UTC).UnixMilli()}}, "recent tools")
	eq(t, f.recentOutput, []string{"file body", "found it"}, "recent output lines")

	// a call whose result has not landed is the current tool (execution.ts: latestActiveToolCall)
	tr = parseSessionEntries([]string{
		`{"type":"message","timestamp":"2024-01-01T00:00:01Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"c1","name":"write","arguments":{"path":"b.txt"}}],"stopReason":"toolUse"}}`,
		`{"type":"message","timestamp":"2024-01-01T00:00:02Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"c2","name":"bash","arguments":{"command":"go test ./..."}}],"stopReason":"toolUse"}}`,
	})
	f = factsFromTranscript(tr)
	eq(t, f.currentTool, "bash", "the latest running call")
	eq(t, f.currentToolArgs, "go test ./...", "its args")
	eq(t, f.currentToolStartedAt, time.Date(2024, 1, 1, 0, 0, 2, 0, time.UTC).UnixMilli(), "its start")
}

func TestTranscriptTailLinesLookLikeTheOriginal(t *testing.T) {
	tr := readSessionTranscript(mustWrite(t, transcriptFixture))
	body, truncated := transcriptTailLines(tr, 80)
	eq(t, truncated, false, "truncated")
	eq(t, body, strings.Join([]string{
		"Tool: read (complete)",
		`{"path":"a.txt"}`,
		"file body",
		"Assistant: found it",
		"Supervisor: thanks",
	}, "\n"), "the flat line grammar (formatLiveForegroundTranscript)")
	body, _ = transcriptTailLines(tr, 3)
	eq(t, body, strings.Join([]string{
		"file body",
		"Assistant: found it",
		"Supervisor: thanks",
	}, "\n"), "the tail bound cuts the head")
	_, truncated = transcriptTailLines(tr, 3)
	eq(t, truncated, true, "a cut tail says so")
}

func TestProgressRecordOfTheOriginalShape(t *testing.T) {
	dir := tmp(t)
	mustWriteAt(t, dir+"/1_c1.jsonl", transcriptFixture)
	s := &stepRecord{
		agent: "scout", task: "find it", thinking: "high",
		state: stateRunning, startedAt: time.Unix(1700000000, 0), lastActivity: time.Unix(1700000005, 0),
		output: "partial answer", sessionDir: dir, sessionID: "c1",
	}
	rec := progressRecordOf(s, 0, time.Unix(1700000010, 0), "p/m")
	eq(t, rec.Index, 0, "index")
	eq(t, rec.Agent, "scout", "agent")
	eq(t, rec.Status, "running", "status")
	eq(t, rec.Task, "find it", "task")
	eq(t, rec.Thinking, "high", "thinking")
	eq(t, rec.LastActivityAt, time.Unix(1700000005, 0).UnixMilli(), "last activity")
	eq(t, rec.DurationMs, int64(10000), "duration")
	eq(t, *rec.TurnCount, 2, "turns")
	eq(t, rec.ToolCount, 1, "tools")
	eq(t, *rec.Tokens, int64(250), "tokens")
	eq(t, rec.Model, "p/m", "model")
	eq(t, rec.RecentOutput, []string{"file body", "found it"}, "recent output from the transcript")

	// without a transcript the live output is the only progress there is
	rec = progressRecordOf(&stepRecord{agent: "scout", task: "t", state: stateComplete, output: "a\nb\n"}, 0, fleetNow(), "")
	eq(t, rec.Status, "completed", "status")
	eq(t, rec.RecentOutput, []string{"a", "b"}, "recent output from the answer")
	eq(t, rec.TurnCount == nil, true, "no turns to report")
	eq(t, rec.Tokens == nil, true, "no usage to report")
	eq(t, rec.Model, "", "no model to report")

	detached := detachedProgressRecord(&AgentConfig{Name: "scout"}, "t", "")
	eq(t, detached.Status, "detached", "an async launch is detached")
	eq(t, detached.Agent, "scout", "agent")
	eq(t, detached.RecentTools, []progressTool{}, "empty arrays, not null")
	eq(t, detached.RecentOutput, []string{}, "empty arrays, not null")
}

// mustWrite stores a child's session transcript in a fresh directory and returns its path.
func mustWrite(t *testing.T, body string) string {
	t.Helper()
	return mustWriteAt(t, tmp(t)+"/1_c1.jsonl", body)
}

func mustWriteAt(t *testing.T, path, body string) string {
	t.Helper()
	writeFile(t, path, body)
	return path
}
