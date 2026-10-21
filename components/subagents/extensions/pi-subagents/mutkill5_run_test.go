//go:build unix

package pi_subagents_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// A background run whose fan-out budget cannot be created is refused with that error and no budget line.
func TestBackgroundRunRefusedWhenItsBudgetCannotBeCreated(t *testing.T) {
	e := newAsyncEnv(t)
	fakeChildren(t, nil)
	writeFile(t, filepath.Join(e.temp, "run-fanout-budgets"), "a file where the budget directory belongs")
	h := e.host(t)
	_, failure := h.Tool("subagent", map[string]any{"agent": "helper", "task": "build", "async": true})
	if !strings.Contains(failure, "run-fanout-budgets") || strings.Contains(failure, "Run fan-out:") {
		t.Errorf("failure: %q", failure)
	}
}

// An explicit model needs no inherited parent model, even under an enforced scope that applies to inherited ones.
func TestExplicitModelUnderAnEnforcedScopeWithoutAParentModel(t *testing.T) {
	scope := `{"modelScope":{"enforce":true,"allow":["anthropic/*"]}}`
	args, failure := modelCase(t, "", "", scope, map[string]any{"model": "anthropic/claude-z"})
	eq(t, failure, "", "failure")
	if !strings.Contains(args, "--model anthropic/claude-z") {
		t.Errorf("args: %q", args)
	}
}
