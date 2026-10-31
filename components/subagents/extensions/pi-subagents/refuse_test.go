package pi_subagents_test

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"

	sub "github.com/MichaelKinsy/pigpen/pi-subagents"
)

// Everything the port does not run is refused with one message that names the original (refuse.go), and nothing runs.
// These are the port's own tests: the original has no such refusals.

const refusalHead = "@pi-in-go/pigpen-subagents (Go port) does not support "
const refusalTail = " yet. Install the original extension, npm:pi-subagents@0.73.1, which runs unchanged in PiG."

func refusedHost(t *testing.T) (*Host, *childCalls) {
	t.Helper()
	calls := fakeChildren(t, nil)
	cwd := tmp(t)
	withTempHome(t)
	writeFile(t, filepath.Join(cwd, ".pi", "agents", "helper.md"), agentMD("helper", "Helps", "You help."))
	return StartHost(t, Extension(), HostOptions{Cwd: cwd}), calls
}

func wantRefusal(t *testing.T, failure, feature string) {
	t.Helper()
	eq(t, failure, refusalHead+feature+refusalTail, "refusal")
}

func TestRefusalMessage(t *testing.T) {
	eq(t, sub.Refusal("x"), refusalHead+"x"+refusalTail, "the exact text")
}

// every parameter of the original's schema that a run does not honor is refused, one test case each
func TestEveryUnportedParameterIsRefused(t *testing.T) {
	honored := map[string]bool{"agent": true, "task": true, "agentScope": true, "cwd": true, "model": true, "async": true, "action": true,
		// Ported run parameters: they run or answer with their own value error, never the refusal.
		"timeoutMs": true, "maxRuntimeMs": true, "output": true, "outputMode": true,
		"isolation": true, "worktree": true, "baseRef": true}
	special := map[string]bool{"workflow": true, "workflowScript": true, "workflowScriptPath": true}
	names := sub.ToolParamNames()
	sort.Strings(names)
	h, calls := refusedHost(t)
	checked := 0
	for _, name := range names {
		if honored[name] || special[name] {
			continue
		}
		checked++
		t.Run(name, func(t *testing.T) {
			_, failure := h.Tool("subagent", map[string]any{"agent": "helper", "task": "t", name: "x"})
			wantRefusal(t, failure, "the `"+name+"` option")
		})
	}
	if checked < 40 {
		t.Errorf("only %d parameters checked", checked)
	}
	eq(t, calls.len(), 0, "no child started")
}

func TestSeveralUnportedParametersAreNamedTogether(t *testing.T) {
	h, calls := refusedHost(t)
	_, failure := h.Tool("subagent", map[string]any{"agent": "helper", "task": "t", "gate": "go test", "acceptance": "checked"})
	wantRefusal(t, failure, "the `acceptance`, `gate` options")
	eq(t, calls.len(), 0, "no child started")
}

func TestAsyncRunsRunInBackgroundAndInvalidValuesAreRefused(t *testing.T) {
	h, calls := refusedHost(t)
	raw, failure := h.Tool("subagent", map[string]any{"agent": "helper", "task": "t", "async": true})
	eq(t, failure, "", "async: true runs in the background")
	if !strings.Contains(string(raw), "Async: helper [") || !strings.Contains(string(raw), "The async run is detached and running in the background.") {
		t.Errorf("started message: %s", raw)
	}
	waitFor(t, func() bool { return calls.len() == 1 }, "the background child starts")
	_, failure = h.Tool("subagent", map[string]any{"agent": "helper", "task": "t", "async": "yes"})
	wantRefusal(t, failure, "async (background) runs (`async: yes`)")
	_, failure = h.Tool("subagent", map[string]any{"agent": "helper", "task": "t", "async": false})
	eq(t, failure, "", "async false is the port's foreground run")
	waitFor(t, func() bool { return calls.len() == 2 }, "the foreground child ran")
}

func TestWorkflowRunsAreRefused(t *testing.T) {
	h, calls := refusedHost(t)
	cases := []struct {
		params  map[string]any
		feature string
	}{
		{map[string]any{"workflowScript": "return 1", "async": true}, "workflow runs (`workflowScript`)"},
		{map[string]any{"workflowScriptPath": "w.js"}, "workflow runs (`workflowScriptPath`)"},
		{map[string]any{"workflow": "review", "args": map[string]any{"task": "x"}}, "workflow runs (`workflow`)"},
		{map[string]any{"workflowScript": "return 1", "workflowScriptPath": "w.js"}, "workflow runs (`workflowScript`, `workflowScriptPath`)"},
		{map[string]any{"action": "validate", "workflowScript": "return 1"}, "workflow runs (`workflowScript`)"},
		{map[string]any{"agent": "helper", "task": "t", "workflowScript": "return 1"}, "workflow runs (`workflowScript`)"},
	}
	for i, c := range cases {
		_, failure := h.Tool("subagent", c.params)
		t.Logf("case %d", i)
		wantRefusal(t, failure, c.feature)
	}
	eq(t, calls.len(), 0, "nothing ran")
}

func TestParallelAndChainRunsAreRefused(t *testing.T) {
	h, calls := refusedHost(t)
	for _, name := range []string{"tasks", "chain", "parallel", "concurrency", "chainDir", "chainName"} {
		t.Run(name, func(t *testing.T) {
			_, failure := h.Tool("subagent", map[string]any{name: []any{map[string]any{"agent": "helper", "task": "t"}}})
			wantRefusal(t, failure, "parallel and chain runs (`"+name+"`)")
		})
	}
	_, failure := h.Tool("subagent", map[string]any{"tasks": []any{map[string]any{"agent": "helper", "task": "t"}}, "async": true, "concurrency": 2})
	wantRefusal(t, failure, "parallel and chain runs (`tasks`, `concurrency`)")
	eq(t, calls.len(), 0, "nothing ran")
}

func TestManagementActionsThePortDoesNotRunAreRefused(t *testing.T) {
	h, _ := refusedHost(t)
	actions := []string{"doctor", "resume", "debug.run", "validate", "schedule.create", "single", "append-step", "unheard-of"}
	for _, a := range actions {
		t.Run(a, func(t *testing.T) {
			_, failure := h.Tool("subagent", map[string]any{"action": a, "agent": "helper"})
			wantRefusal(t, failure, "the `"+a+"` action")
		})
	}
	_, failure := h.Tool("subagent", map[string]any{"action": "guide", "topic": 5})
	if !strings.Contains(failure, refusalHead) && !strings.Contains(failure, "topic") {
		t.Errorf("guide with a non-string topic: %q", failure)
	}
	_, failure = h.Tool("subagent", map[string]any{"action": "get", "agent": "helper", "config": map[string]any{}})
	wantRefusal(t, failure, "the `config` option")
	_, failure = h.Tool("subagent", map[string]any{"action": "list", "agent": "helper"})
	wantRefusal(t, failure, "the `agent` option")
	raw, failure := h.Tool("subagent", map[string]any{"action": "list", "agentScope": "both"})
	eq(t, failure, "", "list still works")
	if !strings.Contains(string(raw), "helper") {
		t.Errorf("list: %s", raw)
	}
	_, failure = h.Tool("subagent", map[string]any{"action": "get", "agent": "helper"})
	eq(t, failure, "", "get still works")
}

func TestTaskAndActionErrorsAreTheOriginals(t *testing.T) {
	h, calls := refusedHost(t)
	_, failure := h.Tool("subagent", map[string]any{})
	eq(t, failure, "Execution requires either { agent, task? } for one child, a named workflow resource, or a non-empty workflowScript or workflowScriptPath for orchestration.", "nothing to run")
	_, failure = h.Tool("subagent", map[string]any{"agent": "  ", "task": "t"})
	eq(t, failure, "Structured single-child execution requires agent to be a non-empty string.", "blank agent")
	_, failure = h.Tool("subagent", map[string]any{"agent": "helper", "task": 5})
	eq(t, failure, "Structured single-child task must be a string when provided.", "task type")
	_, failure = h.Tool("subagent", map[string]any{"action": "  "})
	eq(t, failure, "action must be a non-empty management/control action, or omit action and use workflowScript.", "blank action")
	_, failure = h.Tool("subagent", map[string]any{"agent": "helper"})
	wantRefusal(t, failure, "a run without `task`")
	eq(t, calls.len(), 0, "no child started")
}

func TestUnportedToolsRefuse(t *testing.T) {
	h, _ := refusedHost(t)
	_, failure := h.Tool("bg_wait", map[string]any{})
	if strings.Contains(failure, refusalHead) {
		t.Errorf("bg_wait still refuses: %q", failure)
	}
	// The supervisor channel answers from the port's own channel.
	raw, failure := h.Tool("subagent_supervisor", map[string]any{"action": "status"})
	eq(t, failure, "", "supervisor status")
	if !strings.Contains(string(raw), "Native supervisor channel active.") {
		t.Errorf("supervisor: %s", raw)
	}
}

// The original's slash commands whose action the port refuses are not registered: a command the port
// lacks is not offered, and the README says so. The portable ones are registered (slash.go).
func TestTheOriginalsOtherCommandsAreNotRegistered(t *testing.T) {
	h, _ := refusedHost(t)
	for _, name := range []string{"subagent-cost", "subagents-doctor", "subagents-watchdog", "subagents-detach", "subagents-refine", "prompt-workflow"} {
		if h.cmds[name] {
			t.Errorf("/%s is registered", name)
		}
	}
	for _, name := range []string{"subagents", "run", "subagents-guide", "subagents-models", "subagents-stop", "subagents-steer", "subagents-fleet", "run-chain", "subagents-profiles", "subagents-load-profile"} {
		if !h.cmds[name] {
			t.Errorf("/%s is missing", name)
		}
	}
}

// a registered action must accept its registered parameters: removing one
// from the registration is a defect the tool call must expose
func TestListCapabilitiesIsRegisteredThroughTheTool(t *testing.T) {
	h, _ := refusedHost(t)
	raw, failure := h.Tool("subagent", map[string]any{"action": "list", "capabilities": true})
	eq(t, failure, "", "list with capabilities")
	text := resultContent(t, raw)
	if !strings.Contains(text, "capabilities") {
		t.Errorf("no capabilities text: %s", text)
	}
}

// a JSON null is an unset parameter, not an option the port ignores
func TestNullParametersAreUnset(t *testing.T) {
	h, calls := refusedHost(t)
	_, failure := h.Tool("subagent", map[string]any{"agent": "helper", "task": "t", "output": nil, "acceptance": nil, "workflowScript": nil, "async": false})
	eq(t, failure, "", "nulls are ignored")
	eq(t, calls.len(), 1, "it ran")
}
