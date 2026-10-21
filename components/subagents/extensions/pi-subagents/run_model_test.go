//go:build unix

package pi_subagents_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The model of a single run is resolved against the session's model registry before its child starts (subagent-executor.ts
// runSinglePath, async-execution.ts), and every run's result carries its run fan-out budget (withRunFanoutBudget). Found by the
// pigeq scenarios run-gate-validation and run-public-validation: the original answers an agent whose model the registry does not
// know with "Unknown subagent model ..." (a background run: behind its budget line); the port had started the child.

func modelHost(t *testing.T, e asyncEnv) *Host {
	t.Helper()
	models := []any{map[string]any{"provider": "anthropic", "id": "claude-y", "api": "anthropic-messages", "type": "chat", "name": "Claude Y", "reasoning": true, "contextWindow": 200000.0, "maxTokens": 8192.0}}
	return StartHost(t, Extension(), HostOptions{Cwd: e.cwd, OnCallValue: func(method string, _ map[string]any) (any, string) {
		switch method {
		case "getAvailableOfType":
			return models, ""
		case "getSessionFile":
			return map[string]any{"sessionFile": "/sessions/parent-a.jsonl"}, ""
		case "getSessionID":
			return map[string]any{"sessionId": "parent-a"}, ""
		}
		return map[string]any{}, ""
	}})
}

func TestARunWithAnUnknownModelFailsBeforeItsChildStarts(t *testing.T) {
	e := newAsyncEnv(t)
	calls := fakeChildren(t, nil)
	h := modelHost(t, e)
	_, failure := h.Tool("subagent", map[string]any{"agent": "helper", "task": "build", "model": "claude-x", "async": false})
	eq(t, failure, "Unknown subagent model 'claude-x' in the active Pi model registry.", "foreground: thrown, no budget line")
	eq(t, len(*calls), 0, "no foreground child")
	_, failure = h.Tool("subagent", map[string]any{"agent": "helper", "task": "build", "model": "claude-x"})
	eq(t, failure, "Run fan-out: 1/64 used, 63 remaining\nUnknown subagent model 'claude-x' in the active Pi model registry.", "background: a failed launch result behind its budget line")
	entries, _ := os.ReadDir(filepath.Join(e.temp, "async-subagent-runs"))
	eq(t, len(entries), 0, "no background run was created")
}

func TestARunStartsItsChildWithTheResolvedModelAndCarriesItsFanoutBudget(t *testing.T) {
	e := newAsyncEnv(t)
	calls := fakeChildren(t, map[string]string{"build": "built it"})
	h := modelHost(t, e)
	raw, failure := h.Tool("subagent", map[string]any{"agent": "helper", "task": "build", "model": "claude-y", "async": false})
	eq(t, failure, "", "failure")
	text := toolText(t, raw)
	if !strings.HasPrefix(text, "Run fan-out: 1/64 used, 63 remaining\nbuilt it") {
		t.Errorf("result: %q", text)
	}
	if !strings.Contains(string(raw), `"runFanoutBudget":{"used":1,"limit":64,"remaining":63}`) {
		t.Errorf("details: %s", raw)
	}
	eq(t, len(*calls), 1, "child")
	args := strings.Join((*calls)[0].Args, " ")
	if !strings.Contains(args, "--model anthropic/claude-y") {
		t.Errorf("child args: %s", args)
	}
	raw, failure = h.Tool("subagent", map[string]any{"agent": "helper", "task": "build", "model": "claude-y"})
	eq(t, failure, "", "background failure")
	if !strings.HasPrefix(toolText(t, raw), "Run fan-out: 1/64 used, 63 remaining\nAsync: helper [") {
		t.Errorf("background launch: %q", toolText(t, raw))
	}
}
