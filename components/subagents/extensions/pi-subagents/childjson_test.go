package pi_subagents

// The JSON-mode child turns the child's event stream into the original's live observability: activity lines for
// output-<n>.log, mirrored events for events.jsonl, and the final assistant message as the answer, with print mode's
// exit statuses mapped from the stream's terminal signals.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestChildJSONHandlerStreamsActivityAndAnswer(t *testing.T) {
	events := []string{}
	lines := []string{}
	h := newChildJSONHandler(childEventSink{
		OnEvent: func(event map[string]any) {
			typ, _ := event["type"].(string)
			events = append(events, typ)
		},
		OnOutputLine: func(line string) { lines = append(lines, line) },
	})
	h.onLine([]byte(`{"type":"session","version":3,"id":"s"}`))
	h.onLine([]byte(`not json at all`))
	h.onLine([]byte(`{"type":"tool_execution_start","toolCallId":"c1","toolName":"bash","args":{"command":"echo hi"}}`))
	h.onLine([]byte(`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"Hello"}}`))
	h.onLine([]byte(`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"line one\nline two"}],"stopReason":"stop"}}`))

	eq(t, strings.Join(events, ","),
		"session,tool_execution_start,message_update,message_end",
		"every parsed event reaches the sink (the persist filter lives in appendChildEvent)")
	eq(t, strings.Join(lines, ","),
		"bash: echo hi,line one,line two",
		"the original's tool line and message lines")
	eq(t, h.answer, "line one\nline two", "the last assistant message is the answer")
	eq(t, h.sawAssistant, true, "an assistant message was seen")
	eq(t, h.stopReason, "stop", "the stop reason is kept")
}

func TestRunAgentJSONAnswerFromEvents(t *testing.T) {
	isolateTemp(t)
	withTempHome(t)
	fakeRunChild(t, func(done <-chan struct{}, req childRequest) (childResult, error) {
		if req.OnLine == nil {
			t.Error("the json child gets an event sink")
		}
		req.OnLine([]byte(`{"type":"session","version":3}`))
		req.OnLine([]byte(`{"type":"tool_execution_start","toolName":"read","args":{"path":"src/a.ts"}}`))
		req.OnLine([]byte(`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"done"}],"stopReason":"stop"}}`))
		return childResult{}, nil
	})
	out, err := runAgentJSON(make(chan struct{}), t.TempDir(), &AgentConfig{Name: "worker"}, "do it", "", "", childEventSink{})
	eq(t, err, nil, "a settled run has no error")
	eq(t, out, "done", "the final assistant message is the answer")
}

func TestRunAgentJSONMapsTerminalSignals(t *testing.T) {
	isolateTemp(t)
	withTempHome(t)
	// Print mode turns a bad final message into exit 1; json mode exits 0, so the handler maps it.
	fakeRunChild(t, func(done <-chan struct{}, req childRequest) (childResult, error) {
		req.OnLine([]byte(`{"type":"session"}`))
		req.OnLine([]byte(`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"half"}],"stopReason":"error"}}`))
		return childResult{}, nil
	})
	out, err := runAgentJSON(make(chan struct{}), t.TempDir(), &AgentConfig{Name: "worker"}, "t", "", "", childEventSink{})
	var exit *childExit
	if !errors.As(err, &exit) {
		t.Fatalf("err = %v, want a child exit", err)
	}
	eq(t, exit.code, 1, "print mode's exit code for a failed final message")
	eq(t, out, "half", "the partial answer is kept")
}

func TestRunAgentJSONKeepsPrintAnswerWithoutEvents(t *testing.T) {
	isolateTemp(t)
	withTempHome(t)
	// A child that produced no JSON events (a non-JSON child) keeps the print-mode answer on stdout.
	fakeRunChild(t, func(done <-chan struct{}, req childRequest) (childResult, error) {
		return childResult{Stdout: "plain answer"}, nil
	})
	out, err := runAgentJSON(make(chan struct{}), t.TempDir(), &AgentConfig{Name: "worker"}, "t", "", "", childEventSink{})
	eq(t, err, nil, "no error")
	eq(t, out, "plain answer", "stdout is the answer without events")
}

// An async run mirrors its child's events into the run artifacts and streams activity into the log the transcript
// view tails, so progress is visible while the run is live; the final answer replaces that log at completion, as the
// original's runner writes its final output over the live tail.
func TestAsyncRunMirrorsChildEventsAndActivity(t *testing.T) {
	isolateTemp(t)
	release := make(chan struct{})
	t.Cleanup(func() { // release the child even if the live-log check fails first
		select {
		case <-release:
		default:
			close(release)
		}
	})
	fakeRunChild(t, func(done <-chan struct{}, req childRequest) (childResult, error) {
		req.OnLine([]byte(`{"type":"tool_execution_start","toolCallId":"c1","toolName":"bash","args":{"command":"ls"}}`))
		req.OnLine([]byte(`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"x"}}`))
		<-release // hold the child "working" so the test can watch the live log
		req.OnLine([]byte(`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"answer"}],"usage":{"input":300,"output":100,"cacheRead":0},"stopReason":"stop"}}`))
		return childResult{}, nil
	})
	r, err := startDetachedRun(asyncRunRequest{SessionID: "s1", Cwd: t.TempDir(), Agent: &AgentConfig{Name: "w"}, Task: "t", TimeoutMs: 60000}, "s1", nil, nil)
	eq(t, err, nil, "start")
	// While the child works, the output log carries the tool activity line.
	deadline := time.Now().Add(5 * time.Second)
	for {
		live, _ := os.ReadFile(filepath.Join(r.Dir, "output-0.log"))
		if strings.Contains(string(live), "bash: ls") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no live activity in the output log: %q", live)
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(release)
	select {
	case <-r.doneCh:
	case <-time.After(5 * time.Second):
		t.Fatal("the run did not finish")
	}
	eq(t, r.Output, "answer", "the event-stream answer")
	eq(t, r.TokensSpent, int64(400), "child usage accumulated from the stream")
	st := readStatusFile(r.Dir)
	if st == nil || st.TotalTokens != 400 {
		t.Errorf("status totalTokens: %+v", st)
	}

	outputBytes, err := os.ReadFile(filepath.Join(r.Dir, "output-0.log"))
	eq(t, err, nil, "the output log exists at completion")
	eq(t, string(outputBytes), "answer", "the final answer replaces the live tail, as the original's runner writes")

	eventsBytes, err := os.ReadFile(filepath.Join(r.Dir, "events.jsonl"))
	eq(t, err, nil, "events exist")
	events := string(eventsBytes)
	if !strings.Contains(events, `"type":"tool_execution_start"`) {
		t.Errorf("child event not mirrored: %s", events)
	}
	if !strings.Contains(events, `"subagentRunId":"`+r.ID+`"`) || !strings.Contains(events, `"subagentSource":"child"`) {
		t.Errorf("child event annotations missing: %s", events)
	}
	if strings.Contains(events, "message_update") {
		t.Errorf("message_update must not be persisted: %s", events)
	}
}
