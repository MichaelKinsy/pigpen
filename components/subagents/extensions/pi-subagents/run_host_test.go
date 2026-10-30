package pi_subagents_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	sub "github.com/MichaelKinsy/pigpen/pi-subagents"
)

// The running addition through the host boundary (review rev-port-popular-5): the tool and the command with a fake
// child runner (export_test.go).

var (
	eq           = sub.Eq
	writeFile    = sub.WriteFile
	tmp          = sub.Tmp
	agentMD      = sub.AgentMD
	withTempHome = sub.WithTempHome
	Extension    = sub.Extension
)

func fakeChildren(t *testing.T, outputs map[string]string) *[]sub.ChildRequest {
	t.Helper()
	var mu sync.Mutex
	var got []sub.ChildRequest
	sub.SetRunChild(t, func(done <-chan struct{}, req sub.ChildRequest) (sub.ChildResult, error) {
		mu.Lock()
		got = append(got, req)
		mu.Unlock()
		task := strings.TrimPrefix(req.Stdin, "Task: ")
		for k, v := range outputs {
			if strings.Contains(task, k) {
				return sub.ChildResult{Stdout: v}, nil
			}
		}
		return sub.ChildResult{Stdout: "ok"}, nil
	})
	return &got
}

func TestNestedCallsAreBlockedBeforeAnyChild(t *testing.T) {
	calls := fakeChildren(t, nil)
	t.Setenv("PIG_SUBAGENT_DEPTH", "2")
	cwd := tmp(t)
	writeFile(t, filepath.Join(cwd, ".pi", "agents", "helper.md"), agentMD("helper", "Helps", "You help."))
	writeFile(t, filepath.Join(cwd, ".pi", "chains", "c.chain.md"), "---\nname: c\ndescription: C\n---\n\n## helper\n\nGo\n")
	h := StartHost(t, Extension(), HostOptions{Cwd: cwd})
	_, failure := h.Tool("subagent", map[string]any{"agent": "helper", "task": "t"})
	if !strings.HasPrefix(failure, "Nested subagent call blocked (depth=2, max=2).") {
		t.Errorf("tool: %q", failure)
	}
	h.Command("run-chain", "c go")
	notes := h.CallsTo("ui.notify")
	if len(notes) == 0 || !strings.HasPrefix(fmt.Sprint(notes[len(notes)-1].Args["message"]), "Nested subagent call blocked") {
		t.Errorf("command: %v", notes)
	}
	eq(t, len(*calls), 0, "no child started")
}

func TestSingleRunThroughTheTool(t *testing.T) {
	calls := fakeChildren(t, map[string]string{"build": "built it"})
	cwd := tmp(t)
	writeFile(t, filepath.Join(cwd, ".pi", "agents", "helper.md"), "---\nname: helper\ndescription: Helps\nthinking: high\n---\n\nYou help.\n")
	writeFile(t, filepath.Join(cwd, "sub", "x"), "")
	home := withTempHome(t)
	writeFile(t, filepath.Join(home, ".pi", "agent", "agents", "mine.md"), agentMD("mine", "Mine", "Mine."))
	h := StartHost(t, Extension(), HostOptions{Cwd: cwd})

	raw, failure := h.Tool("subagent", map[string]any{"agent": "helper", "task": "build", "cwd": "sub", "model": "p/m:low"})
	eq(t, failure, "", "failure")
	if !strings.Contains(string(raw), "built it") {
		t.Errorf("result %s", raw)
	}
	req := (*calls)[0]
	eq(t, req.Cwd, filepath.Join(cwd, "sub"), "a relative cwd is the session's")
	eq(t, req.Args[:3], []string{"--print", "--model", "p/m:low"}, "model override, its thinking")
	eq(t, req.Stdin, "Task: build", "stdin")

	_, failure = h.Tool("subagent", map[string]any{"agent": "helper", "task": "t", "cwd": "nope"})
	if !strings.HasPrefix(failure, "Subagent launch aborted: cwd does not exist: ") {
		t.Errorf("bad cwd: %q", failure)
	}
	_, failure = h.Tool("subagent", map[string]any{"agent": "mine", "task": "t", "agentScope": "project"})
	if !strings.HasPrefix(failure, "Unknown agent 'mine'") {
		t.Errorf("agentScope: %q", failure)
	}
	_, failure = h.Tool("subagent", map[string]any{"agent": "mine", "task": "t", "agentScope": "user"})
	eq(t, failure, "", "a user agent in user scope")
	eq(t, len(*calls), 2, "children")
}

func TestChainThroughTheCommand(t *testing.T) {
	calls := fakeChildren(t, map[string]string{"the request": "step one", "step one": "step two"})
	cwd := tmp(t)
	withTempHome(t)
	writeFile(t, filepath.Join(cwd, ".pi", "agents", "a.md"), agentMD("a", "A", "A."))
	writeFile(t, filepath.Join(cwd, ".pi", "agents", "b.md"), agentMD("b", "B", "B."))
	writeFile(t, filepath.Join(cwd, ".pi", "chains", "ab.chain.md"), "---\nname: ab\ndescription: AB\n---\n\n## a\n\n{task}\n\n## b\n\n{previous}\n")
	h := StartHost(t, Extension(), HostOptions{Cwd: cwd})
	eq(t, h.Command("run-chain", "  ab   the request  "), "", "command")
	eq(t, len(*calls), 2, "children")
	eq(t, (*calls)[0].Stdin, "Task: the request", "first step")
	eq(t, (*calls)[1].Stdin, "Task: step one", "second step")
	notes := h.CallsTo("ui.notify")
	eq(t, fmt.Sprint(notes[len(notes)-1].Args["message"]), "step two", "the chain's answer")
}

func TestExtensionsThroughTheTool(t *testing.T) {
	calls := fakeChildren(t, nil)
	cwd := tmp(t)
	home := withTempHome(t)
	writeFile(t, filepath.Join(home, ".pi", "agent", "settings.json"), `{"subagents": {"defaultExtensions": ["/provider.ts"]}}`)
	writeFile(t, filepath.Join(cwd, ".pi", "agents", "def.md"), agentMD("def", "Def", "Def."))
	writeFile(t, filepath.Join(cwd, ".pi", "agents", "none.md"), "---\nname: none\ndescription: None\nextensions:\nsubagentOnlyExtensions: /child.ts\n---\n")
	h := StartHost(t, Extension(), HostOptions{Cwd: cwd})

	_, failure := h.Tool("subagent", map[string]any{"agent": "def", "task": "t"})
	eq(t, failure, "", "failure")
	args := (*calls)[0].Args
	eq(t, args[len(args)-3:], []string{"--no-extensions", "--extension", "/provider.ts"}, "the settings default")

	_, failure = h.Tool("subagent", map[string]any{"agent": "none", "task": "t"})
	eq(t, failure, "", "failure")
	args = (*calls)[1].Args
	eq(t, args[len(args)-3:], []string{"--no-extensions", "--extension", "/child.ts"}, "the agent's own empty list, plus child-only")
	notes := h.CallsTo("ui.notify")
	if len(notes) != 1 || notes[0].Args["level"] != "warning" || !strings.HasPrefix(fmt.Sprint(notes[0].Args["message"]), "extensions: [] override for agent 'none'") {
		t.Errorf("warning: %v", notes)
	}

	raw, failure := h.Tool("subagent", map[string]any{"action": "get", "agent": "def"})
	eq(t, failure, "", "get")
	if !strings.Contains(string(raw), `Extensions: /provider.ts`) {
		t.Errorf("get shows the default: %s", raw)
	}

	writeFile(t, filepath.Join(home, ".pi", "agent", "settings.json"), `{"subagents": {"defaultExtensions": [1]}}`)
	_, failure = h.Tool("subagent", map[string]any{"agent": "def", "task": "t"})
	if !strings.Contains(failure, "have invalid 'defaultExtensions'") {
		t.Errorf("invalid settings: %q", failure)
	}
	eq(t, len(*calls), 2, "no child for invalid settings")
}

func TestModelSettingsThroughTheTool(t *testing.T) {
	calls := fakeChildren(t, nil)
	cwd := tmp(t)
	home := withTempHome(t)
	writeFile(t, filepath.Join(home, ".pi", "agent", "settings.json"), `{"subagents": {"defaultModel": "p/default", "agentOverrides": {"def": {"model": "p/role", "thinking": "off"}}}}`)
	writeFile(t, filepath.Join(cwd, ".pi", "agents", "def.md"), agentMD("def", "Def", "Def."))
	writeFile(t, filepath.Join(cwd, ".pi", "agents", "other.md"), agentMD("other", "Other", "Other."))
	h := StartHost(t, Extension(), HostOptions{Cwd: cwd})

	_, failure := h.Tool("subagent", map[string]any{"agent": "def", "task": "t"})
	eq(t, failure, "", "failure")
	eq(t, (*calls)[0].Args[:5], []string{"--print", "--model", "p/role", "--thinking", "off"}, "the role's override")
	_, failure = h.Tool("subagent", map[string]any{"agent": "def", "task": "t", "model": "p/call"})
	eq(t, failure, "", "failure")
	eq(t, (*calls)[1].Args[:5], []string{"--print", "--model", "p/call", "--thinking", "off"}, "a per-call model wins")
	_, failure = h.Tool("subagent", map[string]any{"agent": "other", "task": "t"})
	eq(t, failure, "", "failure")
	eq(t, (*calls)[2].Args[:3], []string{"--print", "--model", "p/default"}, "the default for an agent without its own")

	raw, failure := h.Tool("subagent", map[string]any{"action": "get", "agent": "def"})
	eq(t, failure, "", "get")
	if !strings.Contains(string(raw), `Model: p/role`) {
		t.Errorf("get shows the override: %s", raw)
	}

	writeFile(t, filepath.Join(home, ".pi", "agent", "settings.json"), `{"subagents": {"defaultModel": 1}}`)
	_, failure = h.Tool("subagent", map[string]any{"agent": "def", "task": "t"})
	if !strings.Contains(failure, "have invalid 'defaultModel'") {
		t.Errorf("invalid settings: %q", failure)
	}
	eq(t, len(*calls), 3, "no child for invalid settings")
}
