//go:build unix

package pi_subagents_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The model a single run's child starts with (run_model.go), the run's fan-out budget (run_fanout.go) and the checks of its
// arguments: each case is a behavior the pigeq scenarios record from the original, pinned here without a model or a pig.

func registryModels(ids ...string) []any {
	var out []any
	for _, id := range ids {
		provider, name, _ := strings.Cut(id, "/")
		out = append(out, map[string]any{"provider": provider, "id": name, "api": "anthropic-messages", "type": "chat", "name": name, "reasoning": true, "contextWindow": 200000.0, "maxTokens": 8192.0})
	}
	return out
}

// modelCase runs the agent "helper" (agentModel is its `model:` line, "" for none) in the foreground under a parent model and a
// registry, with the given subagents settings, and returns the child's arguments and the tool's failure text.
func modelCase(t *testing.T, agentModel, parent, settings string, params map[string]any) (string, string) {
	t.Helper()
	e := newAsyncEnv(t)
	calls := fakeChildren(t, nil)
	line := ""
	if agentModel != "" {
		line = "model: " + agentModel + "\n"
	}
	writeFile(t, filepath.Join(e.cwd, ".pi", "agents", "helper.md"), "---\nname: helper\ndescription: Helps\n"+line+"---\n\nYou help.\n")
	if settings != "" {
		writeFile(t, filepath.Join(e.cwd, ".pi", "settings.json"), `{"subagents":`+settings+`}`)
	}
	models := registryModels("other/claude-y", "anthropic/claude-y", "anthropic/claude-z", "anthropic/claude-w")
	h := StartHost(t, Extension(), HostOptions{Cwd: e.cwd, OnCallValue: func(method string, _ map[string]any) (any, string) {
		if method == "getAvailableOfType" {
			return models, ""
		}
		return map[string]any{}, ""
	}})
	if parent != "" {
		provider, id, _ := strings.Cut(parent, "/")
		h.write(map[string]any{"type": "notify", "notify": map[string]any{"method": "state_update", "args": map[string]any{"state": map[string]any{"model": map[string]any{"id": id, "provider": provider}}}}})
	}
	args := map[string]any{"agent": "helper", "task": "build", "async": false}
	for k, v := range params {
		args[k] = v
	}
	_, failure := h.Tool("subagent", args)
	if len(*calls) == 0 {
		return "", failure
	}
	return strings.Join((*calls)[0].Args, " "), failure
}

func TestRunModelResolution(t *testing.T) {
	has := func(args, want string) {
		t.Helper()
		if !strings.Contains(args+" ", " --model "+want+" ") {
			t.Errorf("want --model %s in %q", want, args)
		}
	}
	args, failure := modelCase(t, "", "anthropic/claude-z", "", nil)
	eq(t, failure, "", "inherited failure")
	has(args, "anthropic/claude-z")
	args, _ = modelCase(t, "", "", "", nil)
	if strings.Contains(args, "--model") {
		t.Errorf("no model anywhere: %q", args)
	}
	args, _ = modelCase(t, "", "anthropic/claude-z", "", map[string]any{"model": "claude-y"})
	has(args, "anthropic/claude-y") // the bare name resolves to the parent's provider, not the first match
	args, _ = modelCase(t, "claude-w", "anthropic/claude-z", "", nil)
	has(args, "anthropic/claude-w")
	args, _ = modelCase(t, "claude-w", "anthropic/claude-z", "", map[string]any{"model": "claude-y"})
	has(args, "anthropic/claude-y")
	args, _ = modelCase(t, "inherit", "anthropic/claude-z", "", nil)
	has(args, "anthropic/claude-z")
	args, _ = modelCase(t, "claude-w", "", "", map[string]any{"model": "other/claude-y"})
	has(args, "other/claude-y")
}

func TestRunModelScope(t *testing.T) {
	scope := `{"modelScope":{"enforce":true,"allow":["anthropic/*"]}}`
	_, failure := modelCase(t, "", "anthropic/claude-z", scope, map[string]any{"model": "other/claude-y"})
	eq(t, failure, "Model 'other/claude-y' is outside the configured subagent model scope (modelScope). Allowed patterns: anthropic/*.", "explicit, outside the scope")
	args, failure := modelCase(t, "", "other/claude-y", scope, nil)
	eq(t, failure, "", "an inherited model outside a non-strict scope only warns")
	if !strings.Contains(args, "--model other/claude-y") {
		t.Errorf("inherited: %q", args)
	}
	strict := `{"modelScope":{"enforce":true,"strict":true,"allow":["anthropic/*"]}}`
	_, failure = modelCase(t, "", "other/claude-y", strict, nil)
	eq(t, failure, "Model 'other/claude-y' is outside the configured subagent model scope (modelScope). Allowed patterns: anthropic/*.", "inherited, strict")
	perAgent := `{"modelScope":{"agents":{"helper":{"enforce":true,"allow":["anthropic/claude-w"]}}}}`
	_, failure = modelCase(t, "", "anthropic/claude-z", perAgent, map[string]any{"model": "anthropic/claude-y"})
	eq(t, failure, "Model 'anthropic/claude-y' is outside the configured subagent model scope (modelScope.agents.helper). Allowed patterns: anthropic/claude-w.", "the agent's own scope")
	_, failure = modelCase(t, "", "anthropic/claude-z", "", map[string]any{"model": "claude-q"})
	eq(t, failure, "Unknown subagent model 'claude-q' in the active Pi model registry.", "unknown")
}

func TestRunFanoutBudgetFollowsTheConfiguredLimit(t *testing.T) {
	e := newAsyncEnv(t)
	fakeChildren(t, nil)
	writeFile(t, filepath.Join(os.Getenv("HOME"), ".pi", "agent", "extensions", "subagent", "config.json"), `{"maxSubagentSpawnsPerRun": 5}`)
	h := StartHost(t, Extension(), HostOptions{Cwd: e.cwd})
	raw, failure := h.Tool("subagent", map[string]any{"agent": "helper", "task": "build", "async": false})
	eq(t, failure, "", "failure")
	if !strings.HasPrefix(toolText(t, raw), "Run fan-out: 1/5 used, 4 remaining\n") || !strings.Contains(string(raw), `"runFanoutBudget":{"used":1,"limit":5,"remaining":4}`) {
		t.Errorf("result: %s", raw)
	}
	raw, failure = h.Tool("subagent", map[string]any{"agent": "helper", "task": "build"})
	eq(t, failure, "", "background failure")
	if !strings.HasPrefix(toolText(t, raw), "Run fan-out: 1/5 used, 4 remaining\n") || !strings.Contains(string(raw), `"runFanoutBudget":{"used":1,"limit":5,"remaining":4}`) {
		t.Errorf("background result: %s", raw)
	}
}

func TestMissionArgumentsAreCheckedOnEveryPath(t *testing.T) {
	e := newAsyncEnv(t)
	calls := fakeChildren(t, nil)
	h := StartHost(t, Extension(), HostOptions{Cwd: e.cwd})
	for _, c := range []struct {
		extra map[string]any
		want  string
	}{
		{map[string]any{"missionId": "m1", "mission": map[string]any{"title": "T"}}, "Use missionId or mission, not both"},
		{map[string]any{"missionId": 5}, ""},
		{map[string]any{"missionId": "not-a-mission"}, ""},
		{map[string]any{"mission": "text"}, ""},
	} {
		for _, async := range []bool{false, true} {
			args := map[string]any{"agent": "helper", "task": "build", "async": async}
			for k, v := range c.extra {
				args[k] = v
			}
			_, failure := h.Tool("subagent", args)
			if failure == "" || strings.Contains(failure, "does not support") {
				t.Errorf("%v async=%v: %q", c.extra, async, failure)
			}
			if c.want != "" {
				eq(t, failure, c.want, fmt.Sprint(c.extra, async))
			}
		}
	}
	_, failure := h.Tool("subagent", map[string]any{"agent": "helper", "task": "build", "async": false, "mission": false})
	eq(t, failure, "", "mission: false is no mission")
	eq(t, len(*calls), 1, "children")
}

func TestStatusCarriesTheBudgetLinesAndDetails(t *testing.T) {
	e := newAsyncEnv(t)
	t.Setenv("FAKE_PIG_SLEEP", "1")
	writeFile(t, filepath.Join(os.Getenv("HOME"), ".pi", "agent", "extensions", "subagent", "config.json"), `{"maxActiveAsyncRunsPerSession": 2}`)
	h := e.host(t)
	id, _ := launch(t, h, map[string]any{"async": true})
	raw, failure := h.Tool("subagent", map[string]any{"action": "status", "id": id})
	eq(t, failure, "", "status")
	if !strings.Contains(toolText(t, raw), "Spawn budget: unlimited\nActive async capacity: 1/2 used\n") {
		t.Errorf("lines: %s", toolText(t, raw))
	}
	for _, want := range []string{`"spawnBudget":{"used":`, `"configuredLimit":null`, `"limit":null`, `"grantHistory":[]`, `"activeAsyncCapacity":{"used":1,"limit":2}`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("details lack %s: %s", want, raw)
		}
	}
	_, failure = h.Tool("subagent", map[string]any{"action": "status", "id": "no-such-run"})
	if !strings.Contains(failure, "Spawn budget: unlimited\nActive async capacity: ") {
		t.Errorf("failed status: %q", failure)
	}
}

func TestBackgroundCapacityRefusalCarriesNoBudgetLine(t *testing.T) {
	e := newAsyncEnv(t)
	t.Setenv("FAKE_PIG_SLEEP", "2")
	writeFile(t, filepath.Join(os.Getenv("HOME"), ".pi", "agent", "extensions", "subagent", "config.json"), `{"maxActiveAsyncRunsPerSession": 1}`)
	h := e.host(t)
	launch(t, h, map[string]any{"async": true})
	_, failure := h.Tool("subagent", map[string]any{"agent": "helper", "task": "again", "async": true})
	eq(t, failure, "Active async run capacity exhausted: 1/1 used.", "second run")
}
