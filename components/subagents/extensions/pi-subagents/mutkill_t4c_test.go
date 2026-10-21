//go:build unix

package pi_subagents_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// The resume action accepts the index of the child to revive.
func TestResumeAcceptsAChildIndex(t *testing.T) {
	e := newAsyncEnv(t)
	h := e.host(t)
	id, _ := launch(t, h, map[string]any{})
	eventually(t, "the first notice", func() bool { return len(sentNotices(h)) > 0 })
	raw, failure := h.Tool("subagent", map[string]any{"action": "resume", "id": id, "index": float64(0), "message": "now polish it"})
	eq(t, failure, "", "resume with an index")
	if !strings.Contains(toolText(t, raw), "Revived helper run "+id+" from its persisted session as async run ") {
		t.Errorf("resume text: %s", raw)
	}
}

// A run that carries an explicit model and no parent session model, under an enforced scope that mixes "inherit" with patterns,
// starts: the unresolved-inherit refusal is for a run that needs the parent's model.
func TestRunModelExplicitUnderAMixedInheritScopeWithoutAParent(t *testing.T) {
	scope := `{"modelScope":{"enforce":true,"allow":["inherit","anthropic/*"]}}`
	args, failure := modelCase(t, "", "", scope, map[string]any{"model": "anthropic/claude-w"})
	eq(t, failure, "", "explicit model, mixed inherit scope, no parent")
	if !strings.Contains(args, "--model anthropic/claude-w") {
		t.Errorf("args: %q", args)
	}
}

// A schedule action's own argument checks answer before the refusal of the unported action.
func TestScheduleArgumentsAreCheckedBeforeTheRefusal(t *testing.T) {
	h, _ := refusedHost(t)
	_, failure := h.Tool("subagent", map[string]any{"action": "schedule.create", "workflowScript": "return 1;"})
	eq(t, failure, "schedule.create requires exactly one trigger: at or every.", "no trigger")
}

// A background run whose budget cannot be created fails with that error.
func TestBackgroundRunFailsWhenTheBudgetCannotBeCreated(t *testing.T) {
	e := newAsyncEnv(t)
	h := e.host(t)
	blocker := filepath.Join(t.TempDir(), "file")
	writeFile(t, blocker, "x")
	t.Setenv("PI_SUBAGENTS_TEMP_ROOT", filepath.Join(blocker, "sub"))
	_, failure := h.Tool("subagent", map[string]any{"agent": "helper", "task": "t", "async": true})
	if !strings.Contains(failure, "not a directory") {
		t.Errorf("failure: %q", failure)
	}
}
