package pi_subagents

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func fanoutOf(t *testing.T, limit int) *runFanout {
	t.Helper()
	withTempHome(t)
	t.Setenv("PI_SUBAGENTS_TEMP_ROOT", t.TempDir())
	desc, err := createRunFanoutBudget("annotate-run", limit)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := claimRunFanoutBatch(desc, []string{"single"}); err != nil {
		t.Fatal(err)
	}
	return &runFanout{desc: desc}
}

func TestFanoutAnnotation(t *testing.T) {
	f := fanoutOf(t, 3)
	// a cancelled run keeps its own error and carries no budget line
	res, err := f.annotate("partial", errCancelled)
	eq(t, res, any("partial"), "the cancelled result")
	eq(t, errors.Is(err, errCancelled), true, "the cancellation survives")
	// a failed run carries the line before its message
	_, err = f.annotate(nil, errors.New("boom"))
	eq(t, err.Error(), "Run fan-out: 1/3 used, 2 remaining\nboom", "a failed run")
	// a tool result carries the line and the details
	details := map[string]any{}
	out, err := f.annotate(sdk.ToolResult{Content: "body", Details: details}, nil)
	eq(t, err, nil, "a result")
	tr := out.(sdk.ToolResult)
	eq(t, tr.Content, "Run fan-out: 1/3 used, 2 remaining\nbody", "the result's text")
	eq(t, marshalAny(details["runFanoutBudget"]), `{"used":1,"limit":3,"remaining":2}`, "the result's details")
	// a budget that cannot be read leaves the result and the error as they are
	broken := &runFanout{}
	same := sdk.ToolResult{Content: "body"}
	out, err = broken.annotate(same, nil)
	eq(t, err, nil, "unreadable budget, result")
	eq(t, out.(sdk.ToolResult).Content, "body", "unreadable budget, text")
	failure := errors.New("boom")
	_, err = broken.annotate(nil, failure)
	eq(t, err, failure, "unreadable budget, error")
}

func TestAsyncStartedMessageOfANonInteractiveRun(t *testing.T) {
	text := formatAsyncStartedMessage("Started", false)
	if !strings.Contains(text, "This is a non-interactive run: Pi auto-drains current-session subagent work at agent_end") || strings.Contains(text, "You are in an interactive session") {
		t.Errorf("non-interactive: %s", text)
	}
	if !strings.Contains(formatAsyncStartedMessage("Started", true), "You are in an interactive session") {
		t.Error("interactive guidance missing")
	}
}

// The status budget names a capacity without a limit unlimited and releases the slots of runs that ended with proof.
func TestStatusBudgetCapacity(t *testing.T) {
	withTempHome(t)
	t.Setenv("PI_SUBAGENTS_TEMP_ROOT", t.TempDir())
	lines, _, capacity := statusBudget("session-s")
	if !strings.HasSuffix(lines, "Active async capacity: 0/unlimited used") {
		t.Errorf("lines: %q", lines)
	}
	eq(t, marshalAny(capacity), `{"used":0,"limit":0}`, "capacity details")
	asyncDir := filepath.Join(t.TempDir(), "run-a")
	h := mustAcquire(t, "session-s", lim(5), "run-a", "runner", asyncDir, CapacityOptions{})
	eq(t, h.MarkStarted("runner-a"), nil, "start")
	lines, _, _ = statusBudget("session-s")
	if !strings.HasSuffix(lines, "Active async capacity: 1/unlimited used") {
		t.Errorf("a running run: %q", lines)
	}
	capWriteJSON(t, filepath.Join(asyncDir, "status.json"), map[string]any{"runId": "run-a", "sessionId": "session-s", "mode": "single", "state": "complete", "startedAt": 100})
	capWriteJSON(t, filepath.Join(asyncDir, "process-terminal.json"), observedProofJSON("run-a", "runner-a"))
	lines, _, _ = statusBudget("session-s")
	if !strings.HasSuffix(lines, "Active async capacity: 0/unlimited used") {
		t.Errorf("an ended run: %q", lines)
	}
}

func TestLaunchBindingCarriesTheTaskDigest(t *testing.T) {
	task := "do the work"
	got := projectLaunchBinding(launchBindingInput{Task: &task})
	eq(t, got["taskDigest"], any(stableJSONDigest(task)), "the digest of the task")
	if _, has := projectLaunchBinding(launchBindingInput{})["taskDigest"]; has {
		t.Error("a launch without a task has no digest")
	}
}

func TestBindingJSONPropertyLimit(t *testing.T) {
	build := func(n int) map[string]any {
		m := map[string]any{}
		for i := 0; i < n; i++ {
			m[strings.Repeat("k", 1)+string(rune('a'+i%26))+strings.Repeat("x", i/26)] = float64(i)
		}
		return m
	}
	props := 0
	_, err := canonicalizeBindingJSON(build(maxExtensionBindingsProperties), "b", 0, map[uintptr]bool{}, &props)
	eq(t, err, nil, "exactly the limit")
	props = 0
	_, err = canonicalizeBindingJSON(build(maxExtensionBindingsProperties+1), "b", 0, map[uintptr]bool{}, &props)
	hasMatch(t, err, `extensionBindings exceeds 256 total properties`)
}

func TestRefinementOverlayNeedsAValidBaseAndGuidance(t *testing.T) {
	withTempHome(t)
	dir := tmp(t)
	a := &AgentConfig{Name: "worker", Source: SourceProject, FilePath: filepath.Join(dir, ".pi", "agents", "worker.md"), SystemPrompt: "base"}
	path, err := getAgentRefinementPath(dir, "worker")
	if err != nil {
		t.Fatal(err)
	}
	write := func(source, current string) string {
		f := newRefinementFile(a, 1, "2026-08-27T16:23:00.000Z")
		f.BaseSource, f.Current = source, current
		if err := writeRefinementFile(path, f); err != nil {
			t.Fatal(err)
		}
		return appendAgentRefinementOverlay("Base", dir, "worker")
	}
	if got := write("project", "Check the report."); !strings.Contains(got, "Check the report.") {
		t.Errorf("a valid refinement: %s", got)
	}
	eq(t, write("nowhere", "Check the report."), "Base", "an unknown base source")
	eq(t, write("project", "  \n "), "Base", "guidance of blanks only")
}

// A duration alone is a heartbeat, not visible progress.
func TestDelegationProgressIgnoresTheDuration(t *testing.T) {
	a := obj("currentTool", "read", "durationMs", float64(10))
	eq(t, sameDelegationProgress(a, obj("currentTool", "read", "durationMs", float64(20))), true, "only the duration changed")
	eq(t, sameDelegationProgress(a, obj("currentTool", "edit", "durationMs", float64(10))), false, "the tool changed")
}
