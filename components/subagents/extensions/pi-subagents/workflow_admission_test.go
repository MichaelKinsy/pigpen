package pi_subagents

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

// Twin of test/unit/workflow-worktree-admission.test.ts.
func TestWorkflowWorktreeAdmissionTwins(t *testing.T) {
	const f = "workflow-worktree-admission"
	tw(t, f, "rechecks effective relative sources after changes without applying fresh defaults to retained resumes", func(t *testing.T) {
		root := tmp(t)
		repo := filepath.Join(root, "repo")
		if err := os.Mkdir(repo, 0o755); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("git", "init", repo).CombinedOutput(); err != nil {
			t.Fatalf("git init: %v %s", err, out)
		}
		// Admission deliberately does not resolve HEAD: base refs remain allocation-time checks.
		writeFile(t, filepath.Join(repo, ".pi", "subagents", "runtime.json"), "{}")
		ctx := context.Background()
		calls := []WorkflowAdmissionCall{
			{Key: "a", Params: obj(t, `{"agent":"worker","task":"Inspect"}`)},
			{Key: "b", Params: obj(t, `{"agent":"worker","task":"Inspect","cwd":"./repo"}`)},
		}
		defaults := obj(t, `{"cwd":"repo","worktree":true}`)
		if err := preflightWorkflowWorktrees(ctx, WorkflowWorktreeAdmission{CtxCwd: root, WorkflowDefaults: defaults, Calls: calls}); err != nil {
			t.Fatalf("clean source: %v", err)
		}
		writeFile(t, filepath.Join(repo, "untracked.txt"), "dirty")
		err := preflightWorkflowWorktrees(ctx, WorkflowWorktreeAdmission{CtxCwd: root, WorkflowDefaults: defaults, Calls: calls})
		if err == nil || !regexp.MustCompile(`a.*b.*clean git working tree`).MatchString(err.Error()) {
			t.Fatalf("dirty source: %v", err)
		}
		retained := []WorkflowAdmissionCall{{Key: "retained", Params: obj(t, `{"resume":"original-run","task":"Continue"}`)}}
		if err := preflightWorkflowWorktrees(ctx, WorkflowWorktreeAdmission{CtxCwd: root, WorkflowDefaults: obj(t, `{"worktree":true}`), Calls: retained}); err != nil {
			t.Fatalf("retained resume: %v", err)
		}
	})
}
