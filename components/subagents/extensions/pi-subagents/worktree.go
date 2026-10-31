package pi_subagents

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Worktree isolation (src/runs/shared/worktree.ts): a run can execute in a
// managed git worktree on its own branch. The original's allocation carries
// transaction and handoff records for lanes and cleanup; this port allocates
// the worktree, runs the child inside it and keeps it (the original's cleanup
// is plan-only too), with the original's validation texts.

const defaultWorktreeBranchPrefix = "pi-subagents/"

func init() {
	registerRunParams("isolation", "worktree", "baseRef")
	registerAction("worktree.cleanup", []string{"mode"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		return manageOutcome(worktreeCleanupAction(ctx.Cwd(), params))
	})
}

// normalizeWorktreeBaseRef is the original's baseRef validation.
func normalizeWorktreeBaseRef(value string) (string, string) {
	if jsTrim(value) == "" {
		return "HEAD", ""
	}
	trimmed := jsTrim(value)
	if !validGitRef(trimmed) || fullCommitIDPattern.MatchString(trimmed) || revisionExpressionPattern.MatchString(trimmed) {
		return "", "baseRef must be a valid Git ref: use HEAD or a supported named ref (for example, refs/heads/main). Full 40/64-character commit IDs and revision expressions are unsupported."
	}
	return trimmed, ""
}

var (
	gitRefComponentPattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	fullCommitIDPattern          = regexp.MustCompile(`^[0-9a-fA-F]{40}$|^[0-9a-fA-F]{64}$`)
	revisionExpressionPattern    = regexp.MustCompile(`\.\.|\^|~|@`)
)

// validGitRef is the original's validGitRef: slash-separated valid components.
func validGitRef(ref string) bool {
	if ref == "" || strings.HasPrefix(ref, "-") || strings.HasSuffix(ref, ".lock") {
		return false
	}
	for _, part := range strings.Split(ref, "/") {
		if !gitRefComponentPattern.MatchString(part) {
			return false
		}
	}
	return true
}

// resolveWorktreeBranchPrefix is the original's normalizeWorktreeBranchPrefix.
func resolveWorktreeBranchPrefix(value string) (string, string) {
	prefix := defaultWorktreeBranchPrefix
	if value != "" {
		prefix = jsTrim(value)
	}
	if prefix == "" {
		return "", "worktree branch prefix cannot be empty"
	}
	if !validGitRef(prefix + "task") {
		return "", "worktree branch prefix is not a valid Git ref namespace"
	}
	return prefix, ""
}

// runGit runs one git command in dir and returns its trimmed stdout.
func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		if ee, isExit := err.(*exec.ExitError); isExit {
			return strings.TrimSpace(string(ee.Stderr)), err
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// worktreeAllocation is one run's worktree: where it lives and its branch.
type worktreeAllocation struct {
	Path   string
	Branch string
}

// allocateRunWorktree checks the repository (the original's texts), creates a
// worktree on a new branch from baseRef, and returns it. The caller keeps it:
// the original's cleanup is plan-only, and a lane may merge it later.
func allocateRunWorktree(cwd, runID, baseRef, prefix string) (*worktreeAllocation, string) {
	if _, err := runGit(cwd, "rev-parse", "--is-inside-work-tree"); err != nil {
		return nil, "worktree isolation requires a git repository"
	}
	dirty, err := runGit(cwd, "status", "--porcelain")
	if err != nil {
		return nil, "worktree isolation requires a clean git working tree. Commit or stash changes first."
	}
	if dirty != "" {
		return nil, "worktree isolation requires a clean git working tree. Commit or stash changes first."
	}
	base, refErr := normalizeWorktreeBaseRef(baseRef)
	if refErr != "" {
		return nil, refErr
	}
	if prefix == "" {
		prefix = defaultWorktreeBranchPrefix
	}
	if _, err := runGit(cwd, "rev-parse", "--verify", base+"^{commit}"); err != nil {
		return nil, fmt.Sprintf("baseRef '%s' could not be resolved to a commit", base)
	}
	branch := prefix + "run-" + runID
	sanitized := strings.NewReplacer("/", "-", "\\", "-").Replace(branch)
	dir := filepath.Join(tempRoot(), "worktrees", sanitized)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return nil, fmt.Sprintf("Worktree setup failed: %s", err.Error())
	}
	if _, err := runGit(cwd, "worktree", "add", "-b", branch, dir, base); err != nil {
		msg := strings.TrimSpace(fmt.Sprint(err))
		return nil, fmt.Sprintf("Worktree setup failed: %s", msg)
	}
	return &worktreeAllocation{Path: dir, Branch: branch}, ""
}

// worktreeCleanupAction is `subagent({action:"worktree.cleanup", mode})`: the
// original plans removals but refuses to apply them ("plan only").
func worktreeCleanupAction(cwd string, params map[string]any) manageResult {
	mode, _ := params["mode"].(string)
	if mode == "apply" || mode == "applied" {
		return manageResult{"worktree.cleanup currently supports mode='plan' only; apply/removal is not available yet.", true}
	}
	if mode != "" && mode != "plan" {
		return manageResult{fmt.Sprintf("Unknown worktree cleanup mode: %s. Valid: plan, apply.", mode), true}
	}
	if _, err := runGit(cwd, "rev-parse", "--is-inside-work-tree"); err != nil {
		return manageResult{text: "worktree cleanup plan requires a git repository"}
	}
	out, err := runGit(cwd, "worktree", "list", "--porcelain")
	if err != nil {
		return manageResult{text: "worktree cleanup plan requires a git repository"}
	}
	lines := []string{"Worktree cleanup plan (plan only; apply/removal is not available yet.)", ""}
	current := ""
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "worktree ") {
			current = strings.TrimPrefix(line, "worktree ")
			continue
		}
		if strings.HasPrefix(line, "branch ") {
			branch := strings.TrimPrefix(line, "branch refs/heads/")
			if strings.HasPrefix(branch, defaultWorktreeBranchPrefix) {
				lines = append(lines, fmt.Sprintf("- %s · branch %s", current, branch))
			}
		}
	}
	if len(lines) == 2 {
		lines = append(lines, "- (no pi-subagents worktrees registered)")
	}
	return manageResult{text: strings.Join(lines, "\n")}
}
