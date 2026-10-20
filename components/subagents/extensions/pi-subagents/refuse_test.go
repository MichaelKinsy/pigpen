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

func refusedHost(t *testing.T) (*Host, *[]sub.ChildRequest) {
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
	// ported by the run policy (policy_run.go): twins and behavior tests are in policy_run_test.go
	honored := map[string]bool{"agent": true, "task": true, "agentScope": true, "cwd": true, "model": true, "async": true, "action": true,
		"output": true, "outputMode": true, "artifacts": true, "acceptance": true, "gate": true, "agentContract": true, "timeoutMs": true, "maxRuntimeMs": true, "includeProgress": true, "mission": true, "missionId": true}
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
	eq(t, len(*calls), 0, "no child started")
}

func TestSeveralUnportedParametersAreNamedTogether(t *testing.T) {
	h, calls := refusedHost(t)
	_, failure := h.Tool("subagent", map[string]any{"agent": "helper", "task": "t", "outputSchema": true, "toolBudget": "checked"})
	wantRefusal(t, failure, "the `outputSchema`, `toolBudget` options")
	eq(t, len(*calls), 0, "no child started")
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
		{map[string]any{"workflowScript": "return 1", "action": nil}, "workflow runs (`workflowScript`)"},
		{map[string]any{"agent": "helper", "task": "t", "workflowScript": "return 1"}, "workflow runs (`workflowScript`)"},
	}
	for i, c := range cases {
		_, failure := h.Tool("subagent", c.params)
		t.Logf("case %d", i)
		wantRefusal(t, failure, c.feature)
	}
	eq(t, len(*calls), 0, "nothing ran")
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
	eq(t, len(*calls), 0, "nothing ran")
}

func TestUnportedManagementAndControlActionsAreRefused(t *testing.T) {
	h, _ := refusedHost(t)
	actions := []string{"debug.run", "schedule.create", "append-step"}
	for _, a := range actions {
		t.Run(a, func(t *testing.T) {
			_, failure := h.Tool("subagent", map[string]any{"action": a, "agent": "helper"})
			wantRefusal(t, failure, "the `"+a+"` action")
		})
	}
	for _, a := range []string{"single", "unheard-of"} { // not actions of the original: its recovery text, not a refusal
		_, failure := h.Tool("subagent", map[string]any{"action": a, "agent": "helper"})
		if !strings.HasPrefix(failure, "Unknown action: "+a+".") {
			t.Errorf("%s: %q", a, failure)
		}
	}
	_, failure := h.Tool("subagent", map[string]any{"action": "get", "agent": "helper", "capabilities": true})
	wantRefusal(t, failure, "the `capabilities` option")
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
	eq(t, len(*calls), 0, "no child started")
}

// The supervisor channel is ported (supervisor_channel.go): its tool answers instead of refusing.
func TestSupervisorToolAnswers(t *testing.T) {
	h, _ := refusedHost(t)
	raw, failure := h.Tool("subagent_supervisor", map[string]any{"action": "pending"})
	eq(t, failure, "", "failure")
	if !strings.Contains(string(raw), "No pending supervisor requests.") {
		t.Errorf("pending: %s", raw)
	}
}

// The original's other slash commands are not registered: a command the port lacks is not offered, and the README says so.
func TestTheOriginalsOtherCommandsAreNotRegistered(t *testing.T) {
	h, _ := refusedHost(t)
	for _, name := range []string{"run", "subagent-cost", "subagents-fleet", "subagents-stop", "subagents-watchdog"} {
		if h.cmds[name] {
			t.Errorf("/%s is registered", name)
		}
	}
	if !h.cmds["subagents-steer"] {
		t.Error("/subagents-steer is not registered")
	}
	if !h.cmds["run-chain"] {
		t.Error("the port's own /run-chain is gone")
	}
	for _, name := range []string{"subagents", "subagents-models", "subagents-profiles", "subagents-load-profile", "subagents-refresh-provider-models", "subagents-generate-profiles", "subagents-check-profile", "subagents-doctor", "subagents-guide", "subagents-refine"} {
		if !h.cmds[name] {
			t.Errorf("/%s is not registered", name)
		}
	}
}

// a JSON null is an unset parameter, not an option the port ignores
func TestNullParametersAreUnset(t *testing.T) {
	h, calls := refusedHost(t)
	_, failure := h.Tool("subagent", map[string]any{"agent": "helper", "task": "t", "output": nil, "acceptance": nil, "workflowScript": nil, "async": false})
	eq(t, failure, "", "nulls are ignored")
	eq(t, len(*calls), 1, "it ran")
}
