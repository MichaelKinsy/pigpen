package tintinweb_subagents

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Git worktree isolation: a temporary git worktree gives an agent an isolated copy of the repository. On completion
// an unchanged worktree is removed; one with changes is committed to a branch (`pi-agent-<id>`) that the result names.
// upstream: src/worktree.ts.

type worktreeInfo struct {
	// Path is the worktree directory (the copied repo's root).
	Path string
	// Branch is the branch made for the worktree if it has changes.
	Branch string
	// BaseSHA is the commit the worktree was created from.
	BaseSHA string
	// WorkPath is where the agent works inside the copy: Path for a repo-root cwd, the same subdirectory inside the
	// copy for a deeper one (a monorepo package), so the requested scoping survives isolation.
	WorkPath string
}

type worktreeCleanupResult struct {
	HasChanges bool
	Branch     string // set if changes were committed
	Path       string
}

// execResult is a command's outcome. A command killed by its timeout reports Killed (the exit code may be 0).
type execResult struct {
	Stdout, Stderr string
	Code           int
	Killed         bool
}

// gitFn runs `git args...` in cwd, killed after timeoutMS milliseconds, and reports the outcome, never an error: the
// seam of pi.exec in the original (the tests replace it).
type gitFn func(args []string, cwd string, timeoutMS int) execResult

// osGit is the real gitFn.
func osGit(args []string, cwd string, timeoutMS int) execResult {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMS)*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = cwd
	cmd.WaitDelay = 500 * time.Millisecond // a child of git (a hook, an alias shell) that outlives the kill must not hold the call up
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	res := execResult{Stdout: out.String(), Stderr: errOut.String()}
	var exitErr *exec.ExitError
	switch {
	case ctx.Err() != nil:
		res.Killed = true
	case errors.As(err, &exitErr):
		res.Code = exitErr.ExitCode()
	case err != nil: // git could not be started
		res.Code = 127
		res.Stderr += err.Error()
	}
	return res
}

// worktreeStartupError is the failure of an agent that asked for a worktree and could not get one. upstream: agent-manager.ts.
const worktreeStartupError = "Cannot run with isolation: \"worktree\" — not a git repo, no commits yet, or `git worktree add` failed. " +
	"Initialize git and commit at least once, or omit `isolation`."

// worktreeIsolationDisabled is the project-wide switch (`worktreeIsolation` in subagents.json); the zero value is
// "enabled", the default. It gates the callers; it does not disarm createWorktree itself.
var worktreeIsolationDisabled atomic.Bool

func setWorktreeIsolationEnabled(enabled bool) { worktreeIsolationDisabled.Store(!enabled) }
func isWorktreeIsolationEnabled() bool         { return !worktreeIsolationDisabled.Load() }

// git runs git and returns its trimmed stdout, failing on a kill or a non-zero exit (the original's git()).
func git(run gitFn, cwd string, args []string, timeoutMS int) (string, error) {
	r := run(args, cwd, timeoutMS)
	if r.Killed || r.Code != 0 {
		if msg := strings.TrimSpace(r.Stderr); msg != "" {
			return "", errors.New(msg)
		}
		return "", fmt.Errorf("git %s failed (exit %d)", strings.Join(args, " "), r.Code)
	}
	return strings.TrimSpace(r.Stdout), nil
}

// createWorktree makes a temporary detached worktree at HEAD for an agent, or returns nil when cwd is not in a git
// repository with a commit or `git worktree add` fails.
func createWorktree(run gitFn, cwd, agentID string) *worktreeInfo {
	if _, err := git(run, cwd, []string{"rev-parse", "--is-inside-work-tree"}, 5000); err != nil {
		return nil
	}
	baseSHA, err := git(run, cwd, []string{"rev-parse", "HEAD"}, 5000)
	if err != nil {
		return nil
	}
	// Where cwd sits inside the repo ("" at the root): the agent must work at the same subdirectory in the copy.
	// Both sides are resolved: git prints resolved paths while cwd may arrive through a symlink.
	topLevel, err := git(run, cwd, []string{"rev-parse", "--show-toplevel"}, 5000)
	if err != nil {
		return nil
	}
	top, err1 := filepath.EvalSymlinks(topLevel)
	here, err2 := filepath.EvalSymlinks(cwd)
	if err1 != nil || err2 != nil {
		return nil
	}
	subdir, err := filepath.Rel(top, here)
	if err != nil {
		return nil
	}
	if subdir == "." {
		subdir = ""
	}
	suffix := make([]byte, 4)
	_, _ = rand.Read(suffix)
	path := filepath.Join(os.TempDir(), "pi-agent-"+agentID+"-"+hex.EncodeToString(suffix))
	if _, err := git(run, cwd, []string{"worktree", "add", "--detach", path, "HEAD"}, 30000); err != nil {
		return nil
	}
	work := path
	if subdir != "" {
		work = filepath.Join(path, subdir)
	}
	return &worktreeInfo{Path: path, Branch: "pi-agent-" + agentID, BaseSHA: baseSHA, WorkPath: work}
}

// cleanupWorktree finishes with a worktree after the agent: an unchanged one is removed; changes are committed and
// the HEAD is put on a branch (its name is written to wt.Branch) before the worktree is removed, so the work is
// never reachable only through a removed directory. A failure removes the worktree and reports no changes.
func cleanupWorktree(run gitFn, cwd string, wt *worktreeInfo, description string) worktreeCleanupResult {
	if !exists(wt.Path) {
		return worktreeCleanupResult{}
	}
	res, err := commitAndBranch(run, cwd, wt, description)
	if err != nil {
		removeWorktree(run, cwd, wt.Path) // best effort
		return worktreeCleanupResult{}
	}
	return res
}

func commitAndBranch(run gitFn, cwd string, wt *worktreeInfo, description string) (worktreeCleanupResult, error) {
	status, err := git(run, wt.Path, []string{"status", "--porcelain"}, 10000)
	if err != nil {
		return worktreeCleanupResult{}, err
	}
	if status != "" {
		if _, err := git(run, wt.Path, []string{"add", "-A"}, 10000); err != nil {
			return worktreeCleanupResult{}, err
		}
		// The message is cut to 200 UTF-16 units; argv needs no shell sanitizing.
		if _, err := git(run, wt.Path, []string{"commit", "--no-verify", "-m", "pi-agent: " + cutUTF16(description, 200)}, 10000); err != nil {
			return worktreeCleanupResult{}, err
		}
	} else {
		current, err := git(run, wt.Path, []string{"rev-parse", "HEAD"}, 5000)
		if err != nil {
			return worktreeCleanupResult{}, err
		}
		if current == wt.BaseSHA {
			removeWorktree(run, cwd, wt.Path)
			return worktreeCleanupResult{}, nil
		}
	}
	// A branch at the worktree's HEAD; if the name is taken, a suffix keeps the earlier work.
	name := wt.Branch
	if _, err := git(run, wt.Path, []string{"branch", name}, 5000); err != nil {
		name = wt.Branch + "-" + strconv.FormatInt(time.Now().UnixMilli(), 10)
		if _, err := git(run, wt.Path, []string{"branch", name}, 5000); err != nil {
			return worktreeCleanupResult{}, err
		}
	}
	wt.Branch = name
	removeWorktree(run, cwd, wt.Path) // the branch persists in the main repo
	return worktreeCleanupResult{HasChanges: true, Branch: wt.Branch, Path: wt.Path}, nil
}

// cutUTF16 is JavaScript's s.slice(0, n): at most n UTF-16 units.
func cutUTF16(s string, n int) string {
	units := 0
	for i, r := range s {
		w := 1
		if r > 0xFFFF {
			w = 2
		}
		if units+w > n {
			return s[:i]
		}
		units += w
	}
	return s
}

// removeWorktree force-removes a worktree, or prunes the registrations when that fails.
func removeWorktree(run gitFn, cwd, path string) {
	if _, err := git(run, cwd, []string{"worktree", "remove", "--force", path}, 10000); err != nil {
		_, _ = git(run, cwd, []string{"worktree", "prune"}, 5000)
	}
}

// pruneWorktrees prunes orphaned worktrees (crash recovery); failures are ignored.
func pruneWorktrees(run gitFn, cwd string) {
	_, _ = git(run, cwd, []string{"worktree", "prune"}, 5000)
}

// detectEnvWith is detectEnv through a gitFn: the environment block of an agent that runs in a worktree. upstream: src/env.ts.
func detectEnvWith(run gitFn, cwd string) envInfo {
	env := envInfo{Platform: platformName(runtime.GOOS)}
	r := run([]string{"rev-parse", "--is-inside-work-tree"}, cwd, 5000)
	env.IsGitRepo = r.Code == 0 && !r.Killed && strings.TrimSpace(r.Stdout) == "true"
	if env.IsGitRepo {
		env.Branch = "unknown"
		if b := run([]string{"branch", "--show-current"}, cwd, 5000); b.Code == 0 && !b.Killed {
			env.Branch = strings.TrimSpace(b.Stdout)
		}
	}
	return env
}
