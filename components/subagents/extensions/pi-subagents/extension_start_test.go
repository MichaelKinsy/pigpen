package pi_subagents_test

import (
	"fmt"
	"strings"
	"testing"
)

// Session start keeps the subagent tool out of the model's tools until subagents_enable and asks git for the repository root, as
// the original does while it starts (the pigeq scenarios record both).
func TestSessionStartDeselectsTheToolAndProbesGit(t *testing.T) {
	withTempHome(t)
	cwd := tmp(t)
	h := StartHost(t, Extension(), HostOptions{Cwd: cwd, OnCall: func(method string, _ map[string]any) (map[string]any, string) {
		return map[string]any{"tools": []any{"read", "subagent", "subagents_enable"}}, ""
	}})
	h.FireAll("session_start", map[string]any{"reason": "startup"})
	var probed, deselected bool
	for _, c := range h.Calls() {
		switch c.Method {
		case "exec":
			if fmt.Sprint(c.Args["command"], c.Args["args"]) == fmt.Sprint("git", []any{"-C", cwd, "rev-parse", "--show-toplevel"}) {
				probed = true
			}
		case "setActiveTools":
			if names := fmt.Sprint(c.Args["names"], c.Args["tools"]); !strings.Contains(names, "subagent ") && !strings.Contains(names, "subagent]") {
				deselected = true
			}
		}
	}
	if !probed {
		t.Errorf("no git probe in %v", h.Calls())
	}
	if !deselected {
		t.Errorf("the subagent tool stays selected: %v", h.Calls())
	}
}

func TestToolBoundaryOfListAndSingleRuns(t *testing.T) {
	withTempHome(t)
	cwd := tmp(t)
	writeFile(t, cwd+"/.pi/agents/helper.md", agentMD("helper", "Helps", "You help."))
	fakeChildren(t, nil)
	h := StartHost(t, Extension(), HostOptions{Cwd: cwd})
	raw, failure := h.Tool("subagent", map[string]any{"action": "list", "capabilities": true})
	eq(t, failure, "", "list capabilities")
	if !strings.Contains(toolText(t, raw), "Executable agents (capabilities):") {
		t.Errorf("list: %s", raw)
	}
	if !strings.Contains(string(raw), `"mode":"management"`) || !strings.Contains(string(raw), `"results":[]`) {
		t.Errorf("a management result carries mode and an empty results list: %s", raw)
	}
	_, failure = h.Tool("subagent", map[string]any{"agent": "helper", "task": "t", "worktree": true})
	if !strings.Contains(failure, "does not support") {
		t.Errorf("worktree: %q", failure)
	}
	for _, agent := range []any{5, "   "} {
		_, failure = h.Tool("subagent", map[string]any{"agent": agent, "task": "t", "async": false})
		eq(t, failure, "Structured single-child execution requires agent to be a non-empty string.", fmt.Sprint("agent ", agent))
	}
}
