package pi_subagents

// The three run options the port honors — `timeoutMs` / `maxRuntimeMs`, `checkpointBeforeDeadlineMs` and `output` —
// against the original's rules and texts (its subagent-executor.ts, subagent-runner.ts and single-output.ts). Written
// before the code in runopts.go, like the rest of the running addition.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunTimeoutParamIsTheOriginals(t *testing.T) {
	for _, c := range []struct {
		what   string
		params map[string]any
		want   int
		err    string
	}{
		// the single-agent default: 30 minutes, the original's DEFAULT_FOREGROUND_TIMEOUT_MS
		{"no timeout set", map[string]any{}, defaultRunTimeoutMs, ""},
		{"a JSON null is unset", map[string]any{"timeoutMs": nil, "maxRuntimeMs": nil}, defaultRunTimeoutMs, ""},
		{"the call's timeoutMs", map[string]any{"timeoutMs": 5_000}, 5_000, ""},
		{"a JSON number", map[string]any{"timeoutMs": float64(5_000)}, 5_000, ""},
		{"the alias maxRuntimeMs", map[string]any{"maxRuntimeMs": 7_000}, 7_000, ""},
		{"the aliases agree", map[string]any{"timeoutMs": 5_000, "maxRuntimeMs": 5_000}, 5_000, ""},
		{"zero", map[string]any{"timeoutMs": 0}, 0, "timeoutMs must be a positive integer."},
		{"negative", map[string]any{"timeoutMs": -1}, 0, "timeoutMs must be a positive integer."},
		{"fractional", map[string]any{"timeoutMs": 1.5}, 0, "timeoutMs must be a positive integer."},
		{"a string", map[string]any{"timeoutMs": "5000"}, 0, "timeoutMs must be a positive integer."},
		{"the alias zero", map[string]any{"maxRuntimeMs": 0}, 0, "maxRuntimeMs must be a positive integer."},
		{"the alias too large", map[string]any{"maxRuntimeMs": 2_147_483_648}, 0, "maxRuntimeMs must be a positive integer no larger than 2147483647."},
		{"too large", map[string]any{"timeoutMs": 2_147_483_648}, 0, "timeoutMs must be a positive integer no larger than 2147483647."},
		{"the aliases disagree", map[string]any{"timeoutMs": 5_000, "maxRuntimeMs": 6_000}, 0, "timeoutMs and maxRuntimeMs are aliases; provide only one value or use the same value for both."},
		// each value is validated before the aliases are compared
		{"an invalid value with an alias", map[string]any{"timeoutMs": "x", "maxRuntimeMs": 6_000}, 0, "timeoutMs must be a positive integer."},
	} {
		got, msg := runTimeoutParam(c.params)
		eq(t, []any{got, msg}, []any{c.want, c.err}, c.what)
	}
}

func TestCheckpointParamIsBoundedLikeTheOriginal(t *testing.T) {
	for _, c := range []struct {
		params map[string]any
		want   int
		err    string
	}{
		{map[string]any{}, 0, ""},
		{map[string]any{"checkpointBeforeDeadlineMs": nil}, 0, ""},
		{map[string]any{"checkpointBeforeDeadlineMs": 5_000}, 5_000, ""},
		{map[string]any{"checkpointBeforeDeadlineMs": 2_147_483_647}, 2_147_483_647, ""},
		{map[string]any{"checkpointBeforeDeadlineMs": 0}, 0, "checkpointBeforeDeadlineMs must be a positive integer no larger than 2147483647."},
		{map[string]any{"checkpointBeforeDeadlineMs": -1}, 0, "checkpointBeforeDeadlineMs must be a positive integer no larger than 2147483647."},
		{map[string]any{"checkpointBeforeDeadlineMs": 1.5}, 0, "checkpointBeforeDeadlineMs must be a positive integer no larger than 2147483647."},
		{map[string]any{"checkpointBeforeDeadlineMs": "5000"}, 0, "checkpointBeforeDeadlineMs must be a positive integer no larger than 2147483647."},
		{map[string]any{"checkpointBeforeDeadlineMs": 2_147_483_648}, 0, "checkpointBeforeDeadlineMs must be a positive integer no larger than 2147483647."},
	} {
		got, msg := checkpointParam(c.params)
		eq(t, []any{got, msg}, []any{c.want, c.err}, "checkpoint of the call")
	}
}

func TestOutputParamNormalizesLikeTheOriginal(t *testing.T) {
	for _, c := range []struct {
		what   string
		params map[string]any
		agent  string // the agent's own `output` frontmatter
		want   string
		err    string
	}{
		// the agent's own output is the default (its rawOutput)
		{"the agent's own default", map[string]any{}, "context.md", "context.md", ""},
		{"no agent default", map[string]any{}, "", "", ""},
		{"a JSON null is unset", map[string]any{"output": nil}, "context.md", "context.md", ""},
		{"true takes the agent's default", map[string]any{"output": true}, "context.md", "context.md", ""},
		{"true with no default", map[string]any{"output": true}, "", "", ""},
		{"false turns it off", map[string]any{"output": false}, "context.md", "", ""},
		{"the call's path wins", map[string]any{"output": "findings.md"}, "context.md", "findings.md", ""},
		{"the string false", map[string]any{"output": "false"}, "context.md", "", ""},
		{"the string true", map[string]any{"output": "true"}, "context.md", "context.md", ""},
		{"an empty string clears the default", map[string]any{"output": ""}, "context.md", "", ""},
		{"a number", map[string]any{"output": 5}, "", "", "output must be a string or a boolean when provided."},
		{"an array", map[string]any{"output": []any{"x.md"}}, "", "", "output must be a string or a boolean when provided."},
	} {
		got, msg := outputParam(c.params, c.agent)
		eq(t, []any{got, msg}, []any{c.want, c.err}, c.what)
	}
}

func TestSingleOutputPathLandsUnderTheRunsArtifacts(t *testing.T) {
	session := filepath.Join("home", "sessions", "s1")
	// the original's resolveSingleRunOutputBaseDir: <artifacts>/outputs/<run id>, session-scoped when the session has
	// a directory, the temp artifacts root otherwise
	relative := singleOutputPath("notes/report.md", "run-7", session)
	eq(t, relative, filepath.Join(session, "subagent-artifacts", "outputs", "run-7", "notes", "report.md"), "relative, session-scoped")
	headless := singleOutputPath("report.md", "run-7", "")
	eq(t, headless, filepath.Join(os.TempDir(), "pi-subagents", "artifacts", "outputs", "run-7", "report.md"), "relative, no session directory")
	abs := filepath.Join(t.TempDir(), "where.md")
	eq(t, singleOutputPath(abs, "run-7", session), filepath.Clean(abs), "absolute as written")
	eq(t, singleOutputPath("", "run-7", session), "", "no output")
	eq(t, singleOutputPath("false", "run-7", session), "", "output: false")
	eq(t, singleOutputPath("true", "run-7", session), "", "output: true without an agent default")
	// one path segment, whatever the id looks like
	eq(t, singleOutputPath("r.md", "odd id", session),
		filepath.Join(session, "subagent-artifacts", "outputs", "odd_id", "r.md"), "the run id is sanitized")
}

func TestSanitizeRunPathSegment(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"run-1", "run-1"},
		{"  run 1  ", "run_1"},
		{"a/b\\c", "a_b_c"},
		{"_edge_", "edge"},
		{"", "unknown"},
		{"///", "unknown"},
		{strings.Repeat("x", 200), strings.Repeat("x", 120)},
		{strings.Repeat("_", 200), "unknown"},
	} {
		eq(t, sanitizeRunPathSegment(c.in), c.want, "segment "+c.in[:min(len(c.in), 20)])
	}
}

func TestOutputInstructionIsTheOriginals(t *testing.T) {
	writeCapable := &AgentConfig{Name: "a", Tools: []string{"read", "bash"}}
	eq(t, injectSingleOutputInstruction("do it", "/w/out.md", writeCapable),
		"do it\n\n---\n**Output:**\n"+
			"Write your findings to exactly this path: /w/out.md\n"+
			"This path is authoritative for this run.\n"+
			"Ignore any other output filename or output path mentioned elsewhere, including output destinations in the base agent prompt, system prompt, or task instructions.",
		"a child that can write is told the path")
	readOnly := &AgentConfig{Name: "a", Tools: []string{"read", "grep", "find", "ls"}}
	eq(t, hasOutputWriteCapability(readOnly), false, "read-only tools")
	if !strings.Contains(injectSingleOutputInstruction("do it", "/w/out.md", readOnly),
		"Return the complete artifact in your final response.\nThe runtime will persist it to exactly this path: /w/out.md\n"+
			"Do not call contact_supervisor merely because no write-capable tool is available.") {
		t.Errorf("the read-only wording: %q", injectSingleOutputInstruction("do it", "/w/out.md", readOnly))
	}
	eq(t, hasOutputWriteCapability(&AgentConfig{Name: "a"}), true, "no tools declared")
	eq(t, hasOutputWriteCapability(&AgentConfig{Name: "a", Tools: []string{}}), false, "an empty list is read-only")
	eq(t, hasOutputWriteCapability(&AgentConfig{Name: "a", Tools: []string{"read"}, McpDirectTools: []string{"x"}}), true, "one MCP tool")
	eq(t, injectSingleOutputInstruction("do it", "", readOnly), "do it", "no path, no instruction")
}

func TestOutputReferenceText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.md")
	answer := "the findings"
	display := persistOutput(path, answer, captureOutputSnapshot(path), 0)
	absolute, _ := filepath.Abs(path)
	eq(t, display, "the findings\n\nOutput saved to: "+absolute+" (12 B, 1 line). Read this file if needed.", "the reference line")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, string(body), answer, "the file holds the answer, not the reference")

	// bytes and lines, as single-output.ts formats them
	eq(t, formatOutputBytes(1023), "1023 B", "bytes")
	eq(t, formatOutputBytes(2048), "2.0 KB", "kilobytes")
	eq(t, formatOutputBytes(1024*1024), "1.0 MB", "megabytes")
	eq(t, countOutputLines(""), 0, "empty")
	eq(t, countOutputLines("one"), 1, "no trailing break")
	eq(t, countOutputLines("one\n"), 1, "a trailing break")
	eq(t, countOutputLines("one\ntwo\r\nthree\r"), 3, "every break style")
	eq(t, lineWord(1), "line", "singular")
	eq(t, lineWord(2), "lines", "plural")
}

func TestPersistOutputKeepsWhatTheChildWrote(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.md")
	before := captureOutputSnapshot(path) // nothing there yet
	if err := os.WriteFile(path, []byte("written by the child"), 0o644); err != nil {
		t.Fatal(err)
	}
	display := persistOutput(path, "the stdout answer", before, 0)
	if !strings.HasPrefix(display, "written by the child\n\nOutput saved to: ") {
		t.Errorf("the child's file is the answer: %q", display)
	}
	body, _ := os.ReadFile(path)
	eq(t, string(body), "written by the child", "the child's file is not overwritten")
}

func TestPersistOutputOnlyRunsForASuccessfulChild(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.md")
	before := captureOutputSnapshot(path)
	eq(t, persistOutput(path, "half an answer", before, 1), "half an answer", "a failed child gets no output file")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the file exists after a failure: %v", err)
	}
	eq(t, persistOutput("", "answer", before, 0), "answer", "no output path")
	// a save failure is reported in the answer, as the original's Output file error line
	blocking := filepath.Join(dir, "blocking")
	if err := os.MkdirAll(blocking, 0o755); err != nil {
		t.Fatal(err)
	}
	display := persistOutput(blocking, "answer", captureOutputSnapshot(blocking), 0)
	if !strings.Contains(display, "\n\nOutput file error: "+blocking+"\n") {
		t.Errorf("the save error: %q", display)
	}
}

// The checkpoint is scheduled `lead` before the deadline, and never less than a second after launch: the original's
// "does not schedule a checkpoint less than one second after launch".
func TestCheckpointDelayIsTheOriginalsGuard(t *testing.T) {
	eq(t, checkpointDelay(2*time.Second, 500*time.Millisecond), 1_500*time.Millisecond, "scheduled")
	eq(t, checkpointDelay(2*time.Second, time.Second), time.Second, "exactly a second after launch is scheduled")
	eq(t, checkpointDelay(2*time.Second, 1_500*time.Millisecond), time.Duration(0), "under a second after launch is not")
	eq(t, checkpointDelay(3*time.Second, 1_500*time.Millisecond), 1_500*time.Millisecond, "scheduled with a longer deadline")
	eq(t, checkpointDelay(800*time.Millisecond, 100*time.Millisecond), time.Duration(0), "a short lead is not")
	eq(t, checkpointDelay(time.Second, time.Millisecond), time.Duration(0), "the guard is on the launch, not the lead")
	eq(t, checkpointMessage(30),
		"Deadline checkpoint from the runner: this run is killed in about 30 seconds. Finish the current tool call only, "+
			"then stop and reply with a handoff: changed files, build/test state, remaining work, and commit/PR state. Do not start new work.",
		"the original's steer text")
}

// A run with a deadline ends at it, with the original's terminal timeout text — not as a cancellation.
func TestDeadlineEndsTheRunWithTheTimeoutText(t *testing.T) {
	fleet.reset()
	t.Cleanup(fleet.reset)
	blockingRunner(t)
	run := fleet.beginRun("single", "scout", "t", "/work")
	run.setLaunchOptions(150, 0, "")
	_, st, err := runAgent(nil, "/work", &AgentConfig{Name: "scout"}, "t", "", nil, run)
	if err == nil || err.Error() != "Subagent timed out after 150ms." {
		t.Errorf("error: %v", err)
	}
	eq(t, st.state, stateFailed, "the step failed")
	eq(t, run.state(), stateFailed, "the run failed")
	eq(t, run.didTimeout(), true, "the deadline fired")
	// a run with no deadline of its own (a chain's, a resume's) is untouched by the default
	plain := fleet.beginRun("single", "scout", "t", "/work")
	plain.setLaunchOptions(0, 0, "")
	plain.armDeadline()
	eq(t, []any{plain.deadlineArmed, plain.deadlineTimer != nil}, []any{false, false}, "no deadline, no timer")
}

// The async single run's pre-deadline checkpoint steer: the original's text, into the running child's turn at the
// host's next safe point, with the run's deadline still killing it (its deadline-checkpoint.test.ts).
func TestAsyncDeadlineCheckpointSteersTheRunningChild(t *testing.T) {
	fleet.reset()
	t.Cleanup(fleet.reset)
	t.Cleanup(closeSessionLink)
	blockingRunner(t)
	startSessionLink("checkpoint-link")
	applied := make(chan string, 4)
	go serveParentLink("checkpoint-link", "child-9", func(op string, req map[string]any) error {
		if op == "steer" {
			message, _ := req["message"].(string)
			applied <- message
		}
		return nil
	})
	run := fleet.beginRun("single", "scout", "t", "/work")
	run.setLaunchOptions(2_000, 1_000, "") // the steer is due 1s after launch, the deadline 2s after
	result := make(chan error, 1)
	go func() {
		_, _, err := runAgent(nil, "/work", &AgentConfig{Name: "scout"}, "t", "", nil, run)
		result <- err
	}()
	waitState(t, run, stateRunning)
	run.steps[0].pinSession("d", "child-9")
	deadline := time.Now().Add(3 * time.Second)
	for sendToChild("child-9", map[string]any{"op": "ping"}) != nil {
		if time.Now().After(deadline) {
			t.Fatalf("the child never connected to the parent link: %v", sendToChild("child-9", map[string]any{"op": "ping"}))
		}
		time.Sleep(2 * time.Millisecond)
	}
	select {
	case message := <-applied:
		for _, want := range []string{
			"Deadline checkpoint from the runner",
			"this run is killed in about 1 seconds",
			"Finish the current tool call only",
			"changed files, build/test state, remaining work, and commit/PR state",
			"Do not start new work",
		} {
			if !strings.Contains(message, want) {
				t.Errorf("checkpoint %q is missing %q", message, want)
			}
		}
	case <-time.After(4 * time.Second):
		t.Fatal("the checkpoint steer never arrived")
	}
	eq(t, len(run.steps), 1, "the steer joined the running turn, it did not queue another")
	select {
	case err := <-result:
		if err == nil || err.Error() != "Subagent timed out after 2000ms." {
			t.Errorf("the deadline still kills the run: %v", err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("the deadline never fired")
	}
}

// A run that settles keeps no timer, and the notification of a run killed by its deadline is the original's failure.
func TestDeadlineTimersStopWhenTheRunSettles(t *testing.T) {
	fleet.reset()
	t.Cleanup(fleet.reset)
	notices := captureCompletions(t)
	answerRunner(t, "all good")
	run := fleet.beginRun("single", "scout", "t", "/work")
	run.setLaunchOptions(60_000, 30_000, "")
	startAsyncRunOn(run, "/work", &AgentConfig{Name: "scout"}, "t", "")
	waitState(t, run, stateComplete)
	// the step settles before runAgent returns, and its defer stops the timers a moment later
	deadline := time.Now().Add(2 * time.Second)
	for !runTimersStopped(run) && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	eq(t, runTimersStopped(run), true, "the settled run stopped its timers")
	eq(t, len(run.steps), 1, "no checkpoint turn")

	blockingRunner(t)
	late := fleet.beginRun("single", "scout", "t", "/work")
	late.setLaunchOptions(120, 0, "")
	startAsyncRunOn(late, "/work", &AgentConfig{Name: "scout"}, "t", "")
	waitState(t, late, stateFailed)
	got := waitNotices(t, notices, 2)
	if !strings.Contains(got[1].content, "Background task failed: **scout**\n\nSubagent timed out after 120ms.") {
		t.Errorf("the timeout notice: %q", got[1].content)
	}
	eq(t, got[1].display, true, "a failure is displayed")
}

// runTimersStopped reads the run's timer state under its lock (the run goroutine writes it).
func runTimersStopped(run *runRecord) bool {
	run.ctrlMu.Lock()
	defer run.ctrlMu.Unlock()
	return run.deadlineStopped
}
