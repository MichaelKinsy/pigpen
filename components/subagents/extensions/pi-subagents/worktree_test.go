package pi_subagents

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Worktree isolation: the original's checks, a real worktree on a new branch,
// and the plan-only cleanup action.

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestWorktreeBaseRefValidation(t *testing.T) {
	// The original's accepted and refused refs.
	for _, ok := range []string{"", "HEAD", "refs/heads/main", "main"} {
		if _, errMsg := normalizeWorktreeBaseRef(ok); errMsg != "" {
			t.Errorf("baseRef %q refused: %s", ok, errMsg)
		}
	}
	want := "baseRef must be a valid Git ref: use HEAD or a supported named ref (for example, refs/heads/main). Full 40/64-character commit IDs and revision expressions are unsupported."
	for _, bad := range []string{"0123456789012345678901234567890123456789", strings.Repeat("a", 64), "main~1", "HEAD..other", "bad name"} {
		if _, errMsg := normalizeWorktreeBaseRef(bad); errMsg != want {
			t.Errorf("baseRef %q: got %q", bad, errMsg)
		}
	}
	// The branch prefix keeps the original's default and validation.
	p, errMsg := resolveWorktreeBranchPrefix("")
	eq(t, p, "pi-subagents/", "default prefix")
	eq(t, errMsg, "", "default ok")
	_, errMsg = resolveWorktreeBranchPrefix("bad prefix!")
	eq(t, errMsg, "worktree branch prefix is not a valid Git ref namespace", "invalid prefix")
}

func TestAllocateRunWorktree(t *testing.T) {
	isolateTemp(t)
	// A non-repository is refused with the original's text.
	plain := t.TempDir()
	if _, errMsg := allocateRunWorktree(plain, "id1", "", ""); errMsg != "worktree isolation requires a git repository" {
		t.Errorf("non-repo: %s", errMsg)
	}
	// A real repository: clean tree allocates a worktree on a prefixed branch.
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "init")
	writeFile(t, filepath.Join(repo, "a.txt"), "one")
	gitIn(t, repo, "add", "a.txt")
	gitIn(t, repo, "commit", "-m", "one")
	alloc, errMsg := allocateRunWorktree(repo, "id2", "HEAD", "")
	if errMsg != "" {
		t.Fatalf("allocate: %s", errMsg)
	}
	if !strings.HasPrefix(alloc.Branch, "pi-subagents/run-") {
		t.Errorf("branch: %s", alloc.Branch)
	}
	if !isDir(alloc.Path) {
		t.Errorf("worktree missing: %s", alloc.Path)
	}
	// The worktree carries the committed state.
	if _, err := os.Stat(filepath.Join(alloc.Path, "a.txt")); err != nil {
		t.Errorf("worktree content: %v", err)
	}
	// A dirty tree is refused with the original's text.
	writeFile(t, filepath.Join(repo, "a.txt"), "two")
	if _, errMsg := allocateRunWorktree(repo, "id3", "", ""); errMsg != "worktree isolation requires a clean git working tree. Commit or stash changes first." {
		t.Errorf("dirty: %s", errMsg)
	}
	gitIn(t, repo, "checkout", "--", "a.txt")
	// An unresolvable baseRef keeps the original's text.
	if _, errMsg := allocateRunWorktree(repo, "id4", "no-such-ref", ""); errMsg != "baseRef 'no-such-ref' could not be resolved to a commit" {
		t.Errorf("bad baseRef: %s", errMsg)
	}
}

func TestWorktreeCleanupIsPlanOnly(t *testing.T) {
	isolateTemp(t)
	// Apply is refused with the original's plan-only text.
	r := worktreeCleanupAction(t.TempDir(), map[string]any{"mode": "apply"})
	eq(t, r.text, "worktree.cleanup currently supports mode='plan' only; apply/removal is not available yet.", "apply refusal")
	// The plan lists this repo's pi-subagents worktrees.
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "init")
	writeFile(t, filepath.Join(repo, "a.txt"), "one")
	gitIn(t, repo, "add", "a.txt")
	gitIn(t, repo, "commit", "-m", "one")
	alloc, errMsg := allocateRunWorktree(repo, "plan1", "", "")
	eq(t, errMsg, "", "allocate")
	plan := worktreeCleanupAction(repo, map[string]any{})
	if !strings.HasPrefix(plan.text, "Worktree cleanup plan (plan only; apply/removal is not available yet.)") {
		t.Errorf("plan heading:\n%s", plan.text)
	}
	if !strings.Contains(plan.text, alloc.Branch) {
		t.Errorf("plan missing the branch:\n%s", plan.text)
	}
}
