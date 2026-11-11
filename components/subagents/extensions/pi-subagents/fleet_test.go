package pi_subagents

// The fleet inspector and the async agents view must look like the original's (port/oracle/docs/observability.md and its
// src/tui/render.ts, src/runs/background/fleet-view.ts, async-status.ts, run-status.ts). These tests pin the rows and
// text to those shapes.

import (
	"strings"
	"testing"
	"time"
)

// fixture time: the views take `now`, so nothing here needs a clock.
var fleetT = time.Unix(1700000000, 0)

func TestFormattersAreTheOriginals(t *testing.T) {
	eq(t, formatTokens(999), "999", "tokens under 1k")
	eq(t, formatTokens(1500), "1.5k", "tokens 1 decimal")
	eq(t, formatTokens(23500), "24k", "tokens rounded k")
	eq(t, formatTokens(1234567), "1.2M", "tokens M")
	eq(t, formatDuration(42), "42ms", "duration ms")
	eq(t, formatDuration(2000), "2.0s", "duration s")
	eq(t, formatDuration(72000), "1m12s", "duration m")
	eq(t, formatActivityLabel(fleetT.Add(-2*time.Second), fleetT), "active 2s ago", "activity age")
	eq(t, formatActivityLabel(fleetT, fleetT), "active now", "activity now")
	eq(t, formatActivityLabel(time.Time{}, fleetT), "", "no activity")
	eq(t, compactTaskText("  fix   the \n thing  "), "fix the thing", "one-line task")
	eq(t, compactTaskText(strings.Repeat("x", 200)), strings.Repeat("x", 93)+"...", "task preview 96")
}

// The async agents view, one running child: the docs/observability.md example's shape.
func TestAsyncAgentsViewLooksLikeTheOriginal(t *testing.T) {
	r := &runRecord{id: "run-1", mode: "single", name: "worker", task: "Review authentication boundaries",
		cwd: "/work", startedAt: fleetT.Add(-72 * time.Second)}
	s := r.addStep("worker", "Review authentication boundaries", "")
	s.state = stateRunning
	s.startedAt = fleetT.Add(-72 * time.Second)
	s.lastActivity = fleetT.Add(-2 * time.Second)
	eq(t, asyncWidgetRows([]*runRecord{r}, fleetT), []string{
		"async subagent worker · background",
		"● worker · 1m12s",
		"  ● Step 1/1: worker · running",
		"    task: Review authentication boundaries",
		"    ⎿  active 2s ago",
		"    Configure the expand key for live detail",
	}, "async agents view")
}

// A running child nothing was seen from is "thinking…"; the hint line marks a running step.
func TestAsyncAgentsViewRunningWithoutActivity(t *testing.T) {
	r := &runRecord{id: "run-1", mode: "single", name: "scout", task: "t", cwd: "/work", startedAt: fleetT.Add(-time.Second)}
	s := r.addStep("scout", "t", "")
	s.state = stateRunning
	s.startedAt = fleetT.Add(-time.Second)
	eq(t, asyncWidgetRows([]*runRecord{r}, fleetT), []string{
		"async subagent scout · background",
		"● scout · 1.0s",
		"  ● Step 1/1: scout · running",
		"    task: t",
		"    ⎿  thinking…",
		"    Configure the expand key for live detail",
	}, "running, no activity")
}

// A finished single child collapses to its summary row (the original's single-child collapse).
func TestAsyncAgentsViewCollapsesAFinishedChild(t *testing.T) {
	r := &runRecord{id: "run-1", mode: "single", name: "worker", task: "t", cwd: "/work", startedAt: fleetT.Add(-5 * time.Second)}
	s := r.addStep("worker", "t", "")
	s.state = stateComplete
	s.startedAt = fleetT.Add(-5 * time.Second)
	s.endedAt = fleetT
	s.exit = 0
	eq(t, asyncWidgetRows([]*runRecord{r}, fleetT), []string{
		"async subagent worker · background",
		"✓ worker · complete · 5.0s",
	}, "finished child")
}

// A chain run shows its steps in order.
func TestAsyncAgentsViewChainShowsSteps(t *testing.T) {
	r := &runRecord{id: "run-1", mode: "chain", name: "review", task: "check it", cwd: "/work", startedAt: fleetT.Add(-72 * time.Second)}
	a := r.addStep("scout", "check it", "")
	a.state = stateComplete
	a.startedAt = fleetT.Add(-72 * time.Second)
	a.endedAt = fleetT.Add(-60 * time.Second)
	b := r.addStep("worker", "a's answer", "")
	b.state = stateRunning
	b.startedAt = fleetT.Add(-60 * time.Second)
	b.lastActivity = fleetT
	eq(t, asyncWidgetRows([]*runRecord{r}, fleetT), []string{
		"async subagent chain (2) · background",
		"● chain · step 2/2 · 1m12s",
		"  ✓ Step 1/2: scout · complete",
		"    task: check it",
		"  ● Step 2/2: worker · running",
		"    task: a's answer",
		"    ⎿  active now",
		"    Configure the expand key for live detail",
	}, "chain steps")
}

// Several runs at once get the aggregate "Async agents" header.
func TestAsyncAgentsViewAggregateHeader(t *testing.T) {
	mk := func(id, agent string) *runRecord {
		r := &runRecord{id: id, mode: "single", name: agent, task: "t", cwd: "/work", startedAt: fleetT.Add(-3 * time.Second)}
		s := r.addStep(agent, "t", "")
		s.state = stateRunning
		s.startedAt = fleetT.Add(-3 * time.Second)
		return r
	}
	eq(t, asyncWidgetRows([]*runRecord{mk("run-1", "scout"), mk("run-2", "worker")}, fleetT), []string{
		"● Async agents · background",
		"● scout · 3.0s",
		"  ● Step 1/1: scout · running",
		"    task: t",
		"    ⎿  thinking…",
		"    Configure the expand key for live detail",
		"● worker · 3.0s",
		"  ● Step 1/1: worker · running",
		"    task: t",
		"    ⎿  thinking…",
		"    Configure the expand key for live detail",
	}, "Async agents header")
	eq(t, asyncWidgetRows(nil, fleetT), []string(nil), "nothing runs: the widget clears")
}

// The fleet inspector's text: the original's inspectSubagentFleet skeleton.
func TestFleetInspectorLooksLikeTheOriginal(t *testing.T) {
	r := &runRecord{id: "run-1", mode: "single", name: "scout", task: "t", cwd: "/work", startedAt: fleetT.Add(-time.Second)}
	s := r.addStep("scout", "t", "")
	s.state = stateRunning
	s.startedAt = fleetT.Add(-time.Second)
	s.lastActivity = fleetT
	eq(t, fleetText([]*runRecord{r}, fleetT), strings.Join([]string{
		"Subagent fleet: 1 tracked",
		"",
		"Foreground runs:",
		"- run-1 | running | scout | active now",
		`  status: subagent({ action: "status", id: "run-1" })`,
		"  transcript: live in the expanded foreground result; persisted session transcript appears after completion when sessions are enabled.",
		"",
		"Commands:",
		`  Refresh fleet: subagent({ action: "status", view: "fleet" })`,
		`  Tail run transcript: subagent({ action: "status", id: "<run-id>", view: "transcript" })`,
		`  Tail child transcript: subagent({ action: "status", id: "<run-id>", index: 0, view: "transcript" })`,
	}, "\n"), "fleet inspector")
}

// A finished run appears under Recent runs with the paths the port keeps; the empty fleet is the original's text.
func TestFleetInspectorRecentAndEmpty(t *testing.T) {
	r := &runRecord{id: "run-2", mode: "single", name: "worker", task: "t", cwd: "/work", startedAt: fleetT.Add(-5 * time.Second)}
	s := r.addStep("worker", "t", "")
	s.state = stateComplete
	s.startedAt = fleetT.Add(-5 * time.Second)
	s.endedAt = fleetT
	s.exit = 0
	s.session = "C:/sessions/child.jsonl"
	t.Setenv("HOME", "")
	text := fleetText([]*runRecord{r}, fleetT)
	if !strings.Contains(text, "Recent runs:\n- run-2 | complete | worker | 5.0s\n") {
		t.Errorf("recent entry:\n%s", text)
	}
	if !strings.Contains(text, "  session: C:/sessions/child.jsonl\n") {
		t.Errorf("session path:\n%s", text)
	}
	eq(t, fleetText(nil, fleetT), `No active subagent fleet. Background runs that already finished are available through completion notifications or subagent({ action: "status", id: "..." }).`, "empty fleet")
}

// `subagent({ action: "status" })`: the active runs list (formatAsyncRunList).
func TestStatusListLooksLikeTheOriginal(t *testing.T) {
	r := &runRecord{id: "run-1", mode: "single", name: "scout", task: "t", cwd: "/work", startedAt: fleetT.Add(-time.Second)}
	s := r.addStep("scout", "t", "")
	s.state = stateRunning
	s.startedAt = fleetT.Add(-time.Second)
	s.lastActivity = fleetT
	t.Setenv("HOME", "")
	eq(t, statusListText([]*runRecord{r}, fleetT), strings.Join([]string{
		"Active async runs: 1",
		"",
		"- run-1 | running | active now | single | step 1/1 | /work",
		"  1. scout | running | active now",
	}, "\n"), "status list")
	eq(t, statusListText(nil, fleetT), "No active async runs.", "no runs")
}

// `subagent({ action: "status", id })` and `view: "transcript"`: the remembered blocks (run-status.ts).
func TestStatusAndTranscriptBlocksLookLikeTheOriginal(t *testing.T) {
	r := &runRecord{id: "run-1", mode: "single", name: "scout", task: "t", cwd: "/work", startedAt: fleetT.Add(-5 * time.Second)}
	s := r.addStep("scout", "t", "")
	s.state = stateComplete
	s.startedAt = fleetT.Add(-5 * time.Second)
	s.endedAt = fleetT
	s.exit = 0
	s.output = "All good\nsecond line\n"
	s.session = "C:/sessions/child.jsonl"
	eq(t, statusRunText(r), strings.Join([]string{
		"Run: run-1",
		"State: complete",
		"Mode: single",
		"Updated: " + fleetT.UTC().Format(time.RFC3339Nano),
		"Cwd: /work",
		"1. scout complete, exit 0, output: All good",
		"  Session: C:/sessions/child.jsonl",
		"",
		`Status: subagent({ action: "status", id: "run-1" })`,
		`Transcript: subagent({ action: "status", id: "run-1", view: "transcript" })`,
		`Revive: subagent({ action: "resume", id: "run-1", message: "..." })`,
	}, "\n"), "status block")
	eq(t, transcriptText(r, nil, 80), strings.Join([]string{
		"Run: run-1",
		"State: complete",
		"Child: 0 (scout)",
		"Session: C:/sessions/child.jsonl",
		"Result transcript tail:",
		"  All good",
		"  second line",
	}, "\n"), "transcript tail")
	noSession := &runRecord{id: "run-2", mode: "single", name: "scout", task: "t", cwd: "/work", startedAt: fleetT}
	noSession.addStep("scout", "t", "").state = stateRunning
	if !strings.HasSuffix(statusRunText(noSession), "Resume: unavailable; no child session file was persisted.") {
		t.Errorf("no session:\n%s", statusRunText(noSession))
	}
}

// The status action's dispatch: the original's errors and views (run-status.ts: inspectSubagentStatus).
func TestStatusViewDispatch(t *testing.T) {
	fleet.reset()
	t.Cleanup(fleet.reset)
	_, _, errMsg := statusView(map[string]any{"view": "chart"})
	eq(t, errMsg, "Unknown status view: chart. Valid: fleet, transcript.", "unknown view")
	_, _, errMsg = statusView(map[string]any{"id": "run-9"})
	eq(t, errMsg, "Async run 'run-9' was not found in the active session.", "unknown id")
	_, _, errMsg = statusView(map[string]any{"view": "transcript"})
	eq(t, errMsg, "No active async run transcript is available.", "transcript without runs")

	r := fleet.beginRun("single", "scout", "t", "/work")
	s := r.addStep("scout", "t", "")
	s.state = stateRunning
	text, mode, errMsg := statusView(map[string]any{})
	eq(t, errMsg, "", "list runs")
	eq(t, mode, "single", "list mode")
	if !strings.Contains(text, "Active async runs: 1") {
		t.Errorf("list:\n%s", text)
	}
	text, mode, errMsg = statusView(map[string]any{"view": "fleet"})
	eq(t, errMsg, "", "fleet runs")
	eq(t, mode, "management", "fleet mode")
	if !strings.Contains(text, "Subagent fleet: 1 tracked") {
		t.Errorf("fleet:\n%s", text)
	}
	_, _, errMsg = statusView(map[string]any{"id": r.id, "view": "transcript", "index": 3})
	eq(t, errMsg, "Transcript index 3 is out of range for 1 child step.", "index range")
	_, _, errMsg = statusView(map[string]any{"id": r.id, "view": "transcript", "index": 1.5})
	eq(t, errMsg, "Transcript index must be an integer.", "index type")
	text, _, errMsg = statusView(map[string]any{"id": r.id, "view": "transcript"})
	eq(t, errMsg, "", "transcript runs")
	if !strings.Contains(text, "Result transcript tail:") {
		t.Errorf("transcript:\n%s", text)
	}
}

// A child's whole life is visible in the registry: a run, its step, its answer.
func TestRunLifecycleIsTracked(t *testing.T) {
	fleet.reset()
	old := runChild
	t.Cleanup(func() { runChild = old; fleet.reset() })
	runChild = func(done <-chan struct{}, req childRequest) (childResult, error) {
		if req.OnOutput != nil {
			req.OnOutput()
		}
		return childResult{Stdout: "hello"}, nil
	}
	out, err := runAgent(nil, "/work", &AgentConfig{Name: "a", SystemPromptMode: "replace"}, "t", "", nil)
	eq(t, err, nil, "run")
	eq(t, out, "hello", "answer")
	runs := fleet.snapshot()
	if len(runs) != 1 {
		t.Fatalf("runs: %d", len(runs))
	}
	r := runs[0]
	eq(t, r.state(), stateComplete, "run complete")
	eq(t, r.steps[0].output, "hello", "remembered output")
	if r.steps[0].lastActivity.IsZero() {
		t.Error("the child's output is not activity")
	}

	// a failing child and a cancelled one end as failed and stopped
	runChild = func(done <-chan struct{}, req childRequest) (childResult, error) {
		return childResult{Exit: 2, Stderr: "boom"}, nil
	}
	_, err = runAgent(nil, "/work", &AgentConfig{Name: "a"}, "t", "", nil)
	if err == nil {
		t.Error("a failing child must fail")
	}
	eq(t, fleet.snapshot()[1].state(), stateFailed, "failed run")

	runChild = func(done <-chan struct{}, req childRequest) (childResult, error) {
		return childResult{}, errCancelled
	}
	_, err = runAgent(nil, "/work", &AgentConfig{Name: "a"}, "t", "", nil)
	if err == nil {
		t.Error("a cancelled child must be cancelled")
	}
	eq(t, fleet.snapshot()[2].state(), stateStopped, "stopped run")
}

// The widget sink gets the view's rows and clears with none.
func TestWidgetSinkRendersAndClears(t *testing.T) {
	fleet.reset()
	t.Cleanup(func() { fleet.setSink(nil); fleet.reset() })
	var got []string
	fleet.setSink(func(rows []string) { got = rows })
	eq(t, got, []string(nil), "no runs: cleared")

	r := fleet.beginRun("single", "scout", "t", "/work")
	s := r.addStep("scout", "t", "")
	s.state = stateRunning
	fleet.pushWidget()
	if len(got) == 0 || got[0] != "async subagent scout · background" {
		t.Errorf("widget rows: %q", got)
	}

	fleet.setSink(nil)
	got = []string{"sentinel"}
	fleet.pushWidget()
	eq(t, got, []string{"sentinel"}, "no sink: nothing pushed")
}
