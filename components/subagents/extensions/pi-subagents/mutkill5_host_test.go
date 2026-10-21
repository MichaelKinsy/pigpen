//go:build unix

package pi_subagents_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The scope of a model of an explicit request, without a parent model: an enforced scope that allows "inherit" beside other
// patterns is satisfied by the explicit model, so the missing parent is no error.
func TestAnExplicitModelNeedsNoParentUnderAMixedInheritScope(t *testing.T) {
	scope := `{"modelScope":{"enforce":true,"allow":["inherit","other/*"]}}`
	args, failure := modelCase(t, "", "", scope, map[string]any{"model": "other/claude-y"})
	eq(t, failure, "", "explicit model, no parent")
	if !strings.Contains(args+" ", " --model other/claude-y ") {
		t.Errorf("args: %q", args)
	}
	_, failure = modelCase(t, "", "", scope, nil)
	eq(t, failure, "Cannot enforce subagent model scope (modelScope): 'inherit' requires a current parent session model.", "no model at all")
}

func TestASingleRunNeedsAnAgentName(t *testing.T) {
	e := newAsyncEnv(t)
	calls := fakeChildren(t, nil)
	h := StartHost(t, Extension(), HostOptions{Cwd: e.cwd})
	_, failure := h.Tool("subagent", map[string]any{"agent": "  ", "task": "build", "async": false})
	eq(t, failure, "Structured single-child execution requires agent to be a non-empty string.", "blank agent")
	eq(t, len(*calls), 0, "no child")
}

func TestAScheduleCallIsCheckedBeforeAnythingElse(t *testing.T) {
	e := newAsyncEnv(t)
	h := StartHost(t, Extension(), HostOptions{Cwd: e.cwd})
	_, failure := h.Tool("subagent", map[string]any{"action": "schedule.create", "at": "2031-01-01T00:00:00Z", "every": "1h", "workflowScript": "return 1"})
	eq(t, failure, "schedule.create requires exactly one trigger: at or every.", "both triggers")
}

// A background launch that fails after it took its capacity slot gives the slot back.
func TestAFailedBackgroundLaunchReleasesItsCapacitySlot(t *testing.T) {
	e := newAsyncEnv(t)
	t.Setenv("FAKE_PIG_SLEEP", "1")
	writeFile(t, filepath.Join(os.Getenv("HOME"), ".pi", "agent", "extensions", "subagent", "config.json"), `{"maxActiveAsyncRunsPerSession": 1}`)
	h := e.host(t)
	_, failure := h.Tool("subagent", map[string]any{"agent": "helper", "task": "build", "cwd": "no-such-directory"})
	if !strings.Contains(failure, "Failed to start async run '") {
		t.Fatalf("launch with a missing directory: %q", failure)
	}
	launch(t, h, map[string]any{})
}

// Ending the session removes the async widget, whatever the session's runs are doing.
func TestSessionShutdownRemovesTheAsyncWidget(t *testing.T) {
	e := newAsyncEnv(t)
	t.Setenv("FAKE_PIG_SLEEP", "60")
	h := e.host(t)
	h.FireAll("session_start", map[string]any{"reason": "startup"})
	launch(t, h, map[string]any{})
	eventually(t, "the widget is drawn", func() bool { return asyncWidgetDrawn(h) > 0 })
	before := asyncWidgetCleared(h)
	h.FireAll("session_shutdown", nil)
	eq(t, asyncWidgetCleared(h) > before, true, "the widget was removed")
}

// A background run whose budget cannot be created is a failed call: the run does not start.
func TestABackgroundRunWithoutABudgetDoesNotStart(t *testing.T) {
	e := newAsyncEnv(t)
	writeFile(t, filepath.Join(e.temp, "run-fanout-budgets"), "in the way")
	h := e.host(t)
	_, failure := h.Tool("subagent", map[string]any{"agent": "helper", "task": "build"})
	if !strings.Contains(failure, "run-fanout-budgets") || strings.Contains(failure, "Async: ") {
		t.Errorf("failure: %q", failure)
	}
	entries, _ := os.ReadDir(filepath.Join(e.temp, "async-subagent-runs"))
	eq(t, len(entries), 0, "no run was created")
}
