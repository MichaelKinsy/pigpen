//go:build unix

package pi_subagents_test

import (
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
