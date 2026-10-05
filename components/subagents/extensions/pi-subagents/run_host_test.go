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
	eq(t, req.Args[:4], []string{"--no-extensions", "--print", "--model", "p/m:low"}, "model override, its thinking")
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
