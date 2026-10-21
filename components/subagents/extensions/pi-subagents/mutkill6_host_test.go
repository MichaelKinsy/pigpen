//go:build unix

package pi_subagents_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestARunWhoseBudgetCannotBeClaimedFailsWithoutABudgetLine(t *testing.T) {
	e := newAsyncEnv(t)
	calls := fakeChildren(t, nil)
	if err := os.WriteFile(filepath.Join(e.temp, "run-fanout-budgets"), []byte("a file"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := e.host(t)
	for _, async := range []bool{false, true} {
		_, failure := h.Tool("subagent", map[string]any{"agent": "helper", "task": "build", "async": async})
		if !strings.Contains(failure, "run-fanout-budgets") || strings.Contains(failure, "Run fan-out:") {
			t.Errorf("async %v: %q", async, failure)
		}
	}
	eq(t, len(*calls), 0, "no child started")
}

func TestAnExplicitModelNeedsNoParentUnderAScopeThatAlsoAllowsInherit(t *testing.T) {
	scope := `{"modelScope":{"enforce":true,"allow":["inherit","anthropic/*"]}}`
	args, failure := modelCase(t, "", "", scope, map[string]any{"model": "anthropic/claude-y"})
	eq(t, failure, "", "failure")
	if !strings.Contains(args+" ", " --model anthropic/claude-y ") {
		t.Errorf("child args: %q", args)
	}
}
