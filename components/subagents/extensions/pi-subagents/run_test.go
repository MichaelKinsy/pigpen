package pi_subagents

import (
	"errors"
	"strings"
	"testing"
)

// Running is an addition of the Go port, not a port of the original's runtime (which spawns Pi child sessions through
// its executor and scripted workflows): these tests are the port's own, written before the code.

func TestBuildChildArgs(t *testing.T) {
	a := &AgentConfig{Name: "helper", Model: "claude-x", Thinking: "high", Tools: []string{"read", "grep"}, SystemPromptMode: "append", SystemPrompt: "You help."}
	// The task is not an argument (it travels on stdin: run_safety_test.go).
	eq(t, buildChildArgs(a, ""), []string{"--no-extensions", "--print", "--model", "claude-x", "--thinking", "high", "--tools", "read,grep", "--append-system-prompt", "You help."}, "append mode")
	a.SystemPromptMode = "replace"
	eq(t, buildChildArgs(a, "other/model"), []string{"--no-extensions", "--print", "--model", "other/model", "--thinking", "high", "--tools", "read,grep", "--system-prompt", "You help."}, "replace mode, model override wins")
	eq(t, buildChildArgs(&AgentConfig{Name: "bare", SystemPromptMode: "replace", Thinking: false}, ""), []string{"--no-extensions", "--print", "--thinking", "off"}, "bare agent, thinking off")
	eq(t, buildChildArgs(&AgentConfig{Name: "inherit", Model: "inherit", SystemPromptMode: "replace"}, ""), []string{"--no-extensions", "--print"}, "inherit means no model flag")
}

func TestChainTask(t *testing.T) {
	steps := []ChainStep{{Agent: "a"}, {Agent: "b"}, {Agent: "c", Task: "Review {previous} for {task}"}}
	eq(t, chainTask(steps, 0, "the request", ""), "the request", "first step defaults to {task}")
	eq(t, chainTask(steps, 1, "the request", "out-a"), "out-a", "later steps default to {previous}")
	eq(t, chainTask(steps, 2, "the request", "out-b"), "Review out-b for the request", "templates substitute both")
}

func fakeRunner(t *testing.T, outputs map[string]string, failOn string) (calls *[][]string) {
	t.Helper()
	old := runChild
	t.Cleanup(func() { runChild = old })
	var got [][]string
	runChild = func(done <-chan struct{}, req childRequest) (childResult, error) {
		got = append(got, req.Args)
		task := strings.TrimPrefix(req.Stdin, "Task: ")
		if failOn != "" && strings.Contains(task, failOn) {
			return childResult{Stdout: "boom", Exit: 3}, nil
		}
		for k, v := range outputs {
			if strings.Contains(task, k) {
				return childResult{Stdout: v}, nil
			}
		}
		return childResult{}, errors.New("unexpected task " + task)
	}
	return &got
}

func TestRunChainThreadsOutput(t *testing.T) {
	agents := []AgentConfig{{Name: "a", SystemPromptMode: "replace"}, {Name: "b", SystemPromptMode: "replace"}}
	chain := &ChainConfig{Name: "ab", Steps: []ChainStep{{Agent: "a"}, {Agent: "b", Task: "Check {previous}"}}}
	calls := fakeRunner(t, map[string]string{"build it": "built", "Check built": "checked"}, "")
	var progress []string
	out, err := runChain(nil, ".", chain, agents, "build it", func(s string) { progress = append(progress, s) })
	if err != nil {
		t.Fatal(err)
	}
	eq(t, out, "checked", "final output")
	eq(t, len(*calls), 2, "two children")
	eq(t, progress, []string{"[1/2] a", "[2/2] b"}, "progress")
}

func TestRunChainStopsOnFailureAndChecksAgentsFirst(t *testing.T) {
	agents := []AgentConfig{{Name: "a", SystemPromptMode: "replace"}}
	calls := fakeRunner(t, map[string]string{"x": "ok"}, "")
	_, err := runChain(nil, ".", &ChainConfig{Name: "bad", Steps: []ChainStep{{Agent: "a"}, {Agent: "ghost"}}}, agents, "x", func(string) {})
	hasMatch(t, err, "Unknown agent 'ghost' in step 2 of chain 'bad'")
	eq(t, len(*calls), 0, "no child runs when a step names an unknown agent")

	calls = fakeRunner(t, nil, "fail-me")
	agents = append(agents, AgentConfig{Name: "b", SystemPromptMode: "replace"})
	_, err = runChain(nil, ".", &ChainConfig{Name: "stop", Steps: []ChainStep{{Agent: "a"}, {Agent: "b"}}}, agents, "fail-me", func(string) {})
	hasMatch(t, err, "step 1 (a) exited with code 3: boom")
	eq(t, len(*calls), 1, "the chain stops at the failing step")
}

func TestRunAgentReportsExit(t *testing.T) {
	fakeRunner(t, map[string]string{"hi": "hello"}, "")
	out, err := runAgent(nil, ".", &AgentConfig{Name: "a", SystemPromptMode: "replace"}, "hi", "")
	if err != nil || out != "hello" {
		t.Errorf("out %q err %v", out, err)
	}
	fakeRunner(t, nil, "bad")
	_, err = runAgent(nil, ".", &AgentConfig{Name: "a", SystemPromptMode: "replace"}, "bad", "")
	hasMatch(t, err, "exited with code 3")
}
