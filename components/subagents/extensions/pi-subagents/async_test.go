package pi_subagents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The async slice's own tests: the original's texts, the config resolution and
// the run lifecycle (a fake child through SetRunChild's internal equivalent).

// fakeRunChild installs a child runner for one internal test. Its cleanup
// stops the runs first, so a background execute goroutine cannot read runChild
// while the restore writes it.
func fakeRunChild(t *testing.T, f func(done <-chan struct{}, req childRequest) (childResult, error)) {
	t.Helper()
	old := runChild
	t.Cleanup(func() {
		stopAll()
		runChild = old
	})
	runChild = f
}

// isolateTemp points the async artifacts at a test directory.
func isolateTemp(t *testing.T) {
	t.Helper()
	t.Setenv("PI_SUBAGENTS_TEMP_ROOT", tmp(t))
	t.Cleanup(func() { stopAll(); clearRuns() })
	stopAll()
	clearRuns()
}

func TestFormatDuration(t *testing.T) {
	cases := map[int64]string{0: "0ms", 999: "999ms", 1000: "1.0s", 1500: "1.5s", 65000: "1m5s", 3599000: "59m59s", 3600000: "60m0s"}
	for in, want := range cases {
		eq(t, formatDuration(in), want, "formatDuration")
	}
}

func TestFormatTokens(t *testing.T) {
	cases := map[int64]string{0: "0", 999: "999", 1000: "1.0k", 1500: "1.5k", 9999: "10.0k", 10000: "10k", 10500: "11k", 1500000: "1.5M", 2000000: "2.0M"}
	for in, want := range cases {
		eq(t, formatTokens(in), want, "formatTokens")
	}
}

func TestFormatActivityAndModel(t *testing.T) {
	now := int64(100000)
	eq(t, formatActivityLabel(nil, now), "", "no activity")
	eq(t, formatActivityLabel(&now, now), "active now", "now")
	earlier := now - 5000
	eq(t, formatActivityLabel(&earlier, now), "active 5s ago", "5s")
	earlier = now - 125000
	eq(t, formatActivityLabel(&earlier, now), "active 2m ago", "2m")

	eq(t, formatModelThinking("anthropic/claude-3", "high"), "claude-3 · thinking high", "model and level")
	eq(t, formatModelThinking("p/m:low", ""), "m · thinking low", "suffix overrides")
	eq(t, formatModelThinking("p/m", "bogus"), "m", "unknown level dropped")
	eq(t, formatModelThinking("", ""), "", "empty")
	eq(t, formatModelThinking("p/m", "off"), "m · thinking off", "off")
}

func TestFormatParallelOutcomeAndAggregate(t *testing.T) {
	eq(t, formatParallelOutcome([]string{"running", "complete"}, 2, true), "1 agent running · 1/2 done", "running")
	eq(t, formatParallelOutcome([]string{"failed", "complete"}, 2, true), "1/2 done · 1 failed", "failed")
	eq(t, aggregateStepStatus([]string{"complete"}), "complete", "all complete")
	eq(t, aggregateStepStatus([]string{"complete", "failed"}), "failed", "failed wins")
	eq(t, aggregateStepStatus(nil), "pending", "empty")
}

// The started message is the original's formatAsyncStartedMessage, exact.
func TestAsyncStartedMessageIsTheOriginals(t *testing.T) {
	interactive := formatAsyncStartedMessage("Async: helper [id-1]", true)
	want := strings.Join([]string{
		"Async: helper [id-1]",
		"",
		"The async run is detached and running in the background.",
		"You are in an interactive session. Return control to the user now; Pi will wake you through the native completion notification when this subagent completes or needs attention. Do not run sleep/polling loops to wait for this async subagent; it does not need a wait call.",
		"Use bg_wait only for provider, detached, or other background work that lacks a native completion notification.",
		"If the current turn must receive results from work without a native notification before it ends, call blocking bg_wait(); ordinary async subagent runs do not need a wait call because their completion is delivered natively.",
		"Otherwise, continue any independent work or return control to the user. Use subagent({ action: \"status\", id: \"...\" }) for a one-shot status/result or to inspect a blocked/stale run, never as a wait loop.",
	}, "\n")
	eq(t, interactive, want, "interactive guidance")
	headless := formatAsyncStartedMessage("Async: helper [id-1]", false)
	if !strings.Contains(headless, "This is a non-interactive run: Pi auto-drains current-session subagent work at agent_end") {
		t.Errorf("headless guidance: %s", headless)
	}
	if strings.Contains(headless, "You are in an interactive session.") {
		t.Errorf("headless must not carry the interactive line: %s", headless)
	}
}

func TestConfigResolution(t *testing.T) {
	home := withTempHome(t)
	eq(t, resolveAsyncByDefault(subagentConfig{}), true, "async by default")
	f := false
	eq(t, resolveAsyncByDefault(subagentConfig{AsyncByDefault: &f}), false, "opted out")

	// The original's config path and keys.
	writeFile(t, filepath.Join(home, ".pi", "agent", "extensions", "subagent", "config.json"),
		`{"asyncByDefault": false, "timeoutMs": 1234, "maxActiveAsyncRunsPerSession": 2, "waitTool": {"enabled": false, "defaultTimeoutMs": 99}}`)
	cfg := loadSubagentConfig()
	eq(t, resolveAsyncByDefault(cfg), false, "config asyncByDefault")
	eq(t, resolveRunTimeoutMs(cfg), int64(1234), "config timeoutMs")
	eq(t, resolveMaxActiveAsyncRunsPerSession(cfg.MaxActiveAsyncRunsPerSess), 2, "config capacity")
	w := resolveWaitToolConfig(cfg, func(string) string { return "" })
	eq(t, w.Enabled, false, "waitTool.enabled")
	eq(t, w.DefaultTimeout, int64(99), "waitTool.defaultTimeoutMs")

	// Environment overrides the config.
	w = resolveWaitToolConfig(cfg, func(k string) string {
		if k == waitToolEnabledEnv {
			return "true"
		}
		if k == waitToolTimeoutEnv {
			return "500"
		}
		return ""
	})
	eq(t, w.Enabled, true, "env enables")
	eq(t, w.DefaultTimeout, int64(500), "env timeout")

	// A broken config keeps the defaults.
	writeFile(t, filepath.Join(home, ".pi", "agent", "extensions", "subagent", "config.json"), `not json`)
	eq(t, resolveAsyncByDefault(loadSubagentConfig()), true, "broken config keeps defaults")
	eq(t, resolveRunTimeoutMs(loadSubagentConfig()), defaultAsyncTimeoutMs, "default timeout is 30m")

	zero := int64(0)
	eq(t, resolveMaxActiveAsyncRunsPerSession(&zero), 0, "0 means unlimited")
	eq(t, resolveMaxActiveAsyncRunsPerSession(nil), 0, "unset means unlimited")
}

func TestRunIDIsAUUIDV4(t *testing.T) {
	id := newRunID()
	if len(id) != 36 || id[14] != '4' || id[19] != '8' && id[19] != '9' && id[19] != 'a' && id[19] != 'b' {
		t.Errorf("run id %q is not a uuid v4", id)
	}
}

// The whole lifecycle with a fake child: artifacts, states and results.
func TestAsyncRunLifecycle(t *testing.T) {
	isolateTemp(t)
	fakeRunChild(t, func(done <-chan struct{}, req childRequest) (childResult, error) {
		return childResult{Stdout: "the answer"}, nil
	})
	agent := &AgentConfig{Name: "helper"}
	r, err := startDetachedRun(asyncRunRequest{SessionID: "s1", Cwd: t.TempDir(), Agent: agent, Task: "t", TimeoutMs: 60000}, "s1", nil, nil)
	eq(t, err, nil, "start")
	select {
	case <-r.doneCh:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not finish")
	}
	eq(t, r.State, "complete", "state")
	eq(t, r.Success, true, "success")

	st := readStatusFile(r.Dir)
	if st == nil {
		t.Fatal("status.json missing")
	}
	eq(t, st.State, "complete", "persisted state")
	eq(t, st.Mode, "single", "mode")
	eq(t, st.SessionID, "s1", "session")
	if len(st.Steps) != 1 || st.Steps[0].Status != "complete" {
		t.Errorf("steps: %+v", st.Steps)
	}
	if !fileExists(filepath.Join(r.Dir, "output-0.log")) {
		t.Error("output log missing")
	}
	if !fileExists(filepath.Join(r.Dir, "events.jsonl")) {
		t.Error("events.jsonl missing")
	}
	res := readResultFile(r.ID)
	if res == nil {
		t.Fatal("result file missing")
	}
	eq(t, res.Success, true, "result success")
	eq(t, res.Output, "the answer", "result output")
	if !strings.Contains(res.Summary, "the answer") {
		t.Errorf("summary %q", res.Summary)
	}
}

func TestAsyncRunStopsAndTimesOut(t *testing.T) {
	isolateTemp(t)
	started := make(chan struct{})
	fakeRunChild(t, func(done <-chan struct{}, req childRequest) (childResult, error) {
		close(started)
		<-done
		return childResult{Stdout: "partial"}, errCancelled
	})
	agent := &AgentConfig{Name: "slow"}
	r, err := startDetachedRun(asyncRunRequest{SessionID: "s1", Cwd: t.TempDir(), Agent: agent, Task: "t", TimeoutMs: 60000}, "s1", nil, nil)
	eq(t, err, nil, "start")
	<-started
	r.requestStop()
	select {
	case <-r.doneCh:
	case <-time.After(5 * time.Second):
		t.Fatal("stopped run did not finish")
	}
	eq(t, r.State, "stopped", "state")
	eq(t, r.Stopped, true, "stopped flag")
	eq(t, r.Steps[0].Error, stoppedMessage, "the original's stop message")
	res := readResultFile(r.ID)
	if res == nil || res.State != "stopped" || res.Summary != stoppedMessage {
		t.Errorf("result: %+v", res)
	}
}

func TestAsyncRunTimeoutKillsTheChild(t *testing.T) {
	isolateTemp(t)
	started := make(chan struct{})
	fakeRunChild(t, func(done <-chan struct{}, req childRequest) (childResult, error) {
		close(started)
		<-done
		return childResult{}, errCancelled
	})
	agent := &AgentConfig{Name: "slow"}
	r, err := startDetachedRun(asyncRunRequest{SessionID: "s1", Cwd: t.TempDir(), Agent: agent, Task: "t", TimeoutMs: 30}, "s1", nil, nil)
	eq(t, err, nil, "start")
	<-started
	select {
	case <-r.doneCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out run did not finish")
	}
	eq(t, r.State, "failed", "state")
	eq(t, r.TimedOut, true, "timedOut")
	if r.DeadlineAt-r.StartedAt != 30 {
		t.Errorf("deadline %d-%d", r.DeadlineAt, r.StartedAt)
	}
}

func TestAsyncCapacityIsEnforced(t *testing.T) {
	isolateTemp(t)
	home := withTempHome(t)
	writeFile(t, filepath.Join(home, ".pi", "agent", "extensions", "subagent", "config.json"), `{"maxActiveAsyncRunsPerSession": 1}`)
	release := make(chan struct{})
	started := make(chan struct{}, 4)
	fakeRunChild(t, func(done <-chan struct{}, req childRequest) (childResult, error) {
		started <- struct{}{}
		select {
		case <-release:
		case <-done:
		}
		return childResult{Stdout: "ok"}, nil
	})
	agent := &AgentConfig{Name: "w"}
	first, err := startDetachedRun(asyncRunRequest{SessionID: "s1", Cwd: t.TempDir(), Agent: agent, Task: "t", TimeoutMs: 60000}, "s1", nil, nil)
	eq(t, err, nil, "first run")
	<-started
	_, err = startDetachedRun(asyncRunRequest{SessionID: "s1", Cwd: t.TempDir(), Agent: agent, Task: "t", TimeoutMs: 60000}, "s1", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "Active async run capacity exhausted: 1/1 used.") {
		t.Errorf("capacity error: %v", err)
	}
	close(release)
	select {
	case <-first.doneCh:
	case <-time.After(5 * time.Second):
		t.Fatal("first run did not finish")
	}
}

// A run whose runner (this process) is gone is repaired by reconciliation, as
// the original's stale-run reconciler does.
func TestReconcileDiskRunRepairsStaleRunningStatus(t *testing.T) {
	isolateTemp(t)
	dir := filepath.Join(asyncRootDir(), "run-stale")
	writeFile(t, filepath.Join(dir, "status.json"),
		`{"runId":"run-stale","mode":"single","state":"running","startedAt":1000,"lastUpdate":1000,"pid":4242,"steps":[{"agent":"helper","status":"running"}]}`)
	st := reconcileDiskRun(dir)
	if st == nil {
		t.Fatal("no status")
	}
	eq(t, st.State, "failed", "repaired state")
	if !strings.Contains(st.Error, "Async runner process 4242 exited or disappeared before writing a result. Marked run failed by stale-run reconciliation.") {
		t.Errorf("repair message %q", st.Error)
	}
	res := readResultFile("run-stale")
	if res == nil || res.Success {
		t.Errorf("repaired result: %+v", res)
	}
	// A terminal status is not touched again.
	st2 := reconcileDiskRun(dir)
	eq(t, st2.State, "failed", "stays failed")
}

func TestActiveViewsListFormatting(t *testing.T) {
	isolateTemp(t)
	eq(t, formatRunList(nil, "Active async runs", 0), "No active async runs.", "the original's empty list")
	fakeRunChild(t, func(done <-chan struct{}, req childRequest) (childResult, error) {
		<-done
		return childResult{}, errCancelled
	})
	r, err := startDetachedRun(asyncRunRequest{SessionID: "s1", Cwd: t.TempDir(), Agent: &AgentConfig{Name: "w"}, Task: "t", TimeoutMs: 60000}, "s1", nil, nil)
	eq(t, err, nil, "start")
	// the execute goroutine moves the run from queued to running
	deadline := time.Now().Add(5 * time.Second)
	for viewOfRun(r).State == "queued" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	views := activeViews("s1")
	if len(views) != 1 {
		t.Fatalf("views %d", len(views))
	}
	text := formatRunList(views, "Active async runs", nowMs())
	if !strings.HasPrefix(text, "Active async runs: 1") {
		t.Errorf("list heading: %s", text)
	}
	if !strings.Contains(text, "- "+r.ID+" | running |") || !strings.Contains(text, " | single | steps 1 | ") {
		t.Errorf("run header: %s", text)
	}
	if !strings.Contains(text, "  1. w | running") {
		t.Errorf("step line: %s", text)
	}
	if len(activeViews("other-session")) != 0 {
		t.Errorf("session scoping: %v", activeViews("other-session"))
	}
}

func TestRunStatusAndTranscriptTexts(t *testing.T) {
	isolateTemp(t)
	dir := filepath.Join(asyncRootDir(), "run-1")
	writeFile(t, filepath.Join(dir, "status.json"),
		`{"runId":"run-1","mode":"single","state":"running","startedAt":1000,"lastUpdate":2000,"cwd":"/w","steps":[{"agent":"helper","status":"running"}]}`)
	writeFile(t, filepath.Join(dir, "output-0.log"), "line one\nline two\n")
	// readStatusFile, not reconcileDiskRun: without a live run the reconciliation
	// would (correctly) repair this synthetic running status to failed.
	st := readStatusFile(dir)
	if st == nil {
		t.Fatal("status.json not read")
	}
	v := viewOfStatus(st, dir)
	text := formatRunStatus(v)
	for _, want := range []string{
		"Run: run-1",
		"State: running",
		"Mode: single",
		"Progress: steps 1",
		"Started: 1970-01-01T00:00:01.000Z",
		"Updated: 1970-01-01T00:00:02.000Z",
		"Dir: " + dir,
		"Step 1: helper running",
		"Steer running child: subagent({ action: \"steer\", id: \"run-1\", message: \"...\" })",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("status text missing %q:\n%s", want, text)
		}
	}

	// The transcript view tails the child's output artifact.
	tr := runTranscript(v, 0, false, 80, false)
	for _, want := range []string{"Run: run-1", "State: running", "Mode: single", "Step 0: helper running", "Artifacts:", "  Output: " + filepath.Join(dir, "output-0.log"), "Transcript tail from " + filepath.Join(dir, "output-0.log") + ":", "line one", "line two"} {
		if !strings.Contains(tr.text, want) {
			t.Errorf("transcript missing %q:\n%s", want, tr.text)
		}
	}

	// A terminal run gives resume guidance with the original's text.
	st2 := *st
	st2.State = "complete"
	v2 := viewOfStatus(&st2, dir)
	terminal := formatRunStatus(v2)
	if !strings.Contains(terminal, "Resume: unavailable; no child session file was persisted.") {
		t.Errorf("resume guidance: %s", terminal)
	}
	st3 := *st
	st3.State = "stopped"
	st3.Stopped = true
	if !strings.Contains(formatRunStatus(viewOfStatus(&st3, dir)), "Resume: unavailable; stopped runs are not resumable. Start a new run instead.") {
		t.Error("stopped resume guidance")
	}
}

func TestResultFileStatusBranch(t *testing.T) {
	isolateTemp(t)
	writeFile(t, filepath.Join(resultsDir(), "run-2.json"),
		`{"id":"run-2","runId":"run-2","agent":"helper","success":true,"summary":"helper:\nall good","output":"all good","exitCode":0,"state":"complete","mode":"single"}`)
	res := readResultFile("run-2")
	if res == nil {
		t.Fatal("result")
	}
	got := resultFileStatus(res, resultPath("run-2"))
	for _, want := range []string{"Run: run-2", "State: complete", "Result: " + resultPath("run-2"), "Resume: unavailable; no child session file was persisted.", "all good"} {
		if !strings.Contains(got.text, want) {
			t.Errorf("result status missing %q:\n%s", want, got.text)
		}
	}
}

func TestCompletionNoticeFormat(t *testing.T) {
	r := &asyncRun{ID: "id-1", Agent: "helper", State: "complete", Success: true, Output: "did it", Dir: "/tmp/async/id-1"}
	text := buildSingleCompletion(r)
	want := "Background task completed: **helper**\n\n" + "did it" + "\n\nRetention-managed async directory: /tmp/async/id-1"
	eq(t, text, want, "single completion")
	r.State = "failed"
	r.Success = false
	r.Error = "boom"
	if !strings.Contains(buildSingleCompletion(r), "Background task failed: **helper**") {
		t.Errorf("failed notice: %s", buildSingleCompletion(r))
	}
	grouped := buildGroupedCompletion([]*asyncRun{
		{ID: "a", Agent: "one", State: "complete", Success: true, Output: "1", Dir: "/d/a"},
		{ID: "b", Agent: "two", State: "complete", Success: true, Output: "2", Dir: "/d/b"},
	})
	if !strings.HasPrefix(grouped, "Background tasks completed (2): **one**, **two**") {
		t.Errorf("grouped header: %s", grouped)
	}
}

func TestWaitSummaries(t *testing.T) {
	complete := &asyncRun{State: "complete"}
	failed := &asyncRun{State: "failed"}
	eq(t, summarizeTerminal([]*asyncRun{complete, failed}), "1 complete, 1 failed", "counts")
	eq(t, summarizeTerminal(nil), "", "empty")
	eq(t, summarizeTerminalStates([]string{"complete"}), "1 complete", "from state")
}

func TestTempScopeAndPaths(t *testing.T) {
	t.Setenv("PI_SUBAGENTS_TEMP_ROOT", "")
	t.Setenv("USERNAME", "Ada Lovelace")
	eq(t, tempScopeID(os.Getenv), "user-Ada-Lovelace", "sanitized user scope")
	root := tempRoot()
	if !strings.HasSuffix(root, "pi-subagents-user-Ada-Lovelace") {
		t.Errorf("temp root %q", root)
	}
	eq(t, filepath.Base(resultsDir()), "async-subagent-results", "results dir name")
	eq(t, filepath.Base(asyncRootDir()), "async-subagent-runs", "async dir name")
}
