package tintinweb_subagents

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Twins of test/worktree.test.ts. Adaptations: pi.exec is the gitFn seam; the original's mock runs git for real and
// reports failure in the result, as osGit does. upstream: src/worktree.ts.

func gitOut(t *testing.T, cwd string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v: %s", args, cwd, err, out)
	}
	return strings.TrimSpace(string(out))
}

func gitTry(cwd string, args ...string) {
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	_, _ = cmd.CombinedOutput()
}

// initGitRepo is a temporary repository with one commit.
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gitOut(t, dir, "init")
	gitOut(t, dir, "config", "user.email", "test@test.com")
	gitOut(t, dir, "config", "user.name", "Test")
	gitOut(t, dir, "config", "commit.gpgsign", "false")
	mustWrite(t, filepath.Join(dir, "README.md"), "# Test repo")
	gitOut(t, dir, "add", "README.md")
	gitOut(t, dir, "commit", "-m", "initial")
	return dir
}

// failingGit answers one git subcommand with a canned failure and runs everything else for real; calls records argv.
func failingGit(match func(args []string) bool, failure execResult) (gitFn, func() [][]string) {
	var mu sync.Mutex
	var calls [][]string
	run := func(args []string, cwd string, timeoutMS int) execResult {
		mu.Lock()
		calls = append(calls, args)
		mu.Unlock()
		if match(args) {
			failure.Stderr = "boom"
			return failure
		}
		return osGit(args, cwd, timeoutMS)
	}
	return run, func() [][]string { mu.Lock(); defer mu.Unlock(); return append([][]string(nil), calls...) }
}

func removeWT(repo, path string) { gitTry(repo, "worktree", "remove", "--force", path) }

func TestWorktreeTwin(t *testing.T) {
	const f = "worktree"
	t.Run("createWorktree", func(t *testing.T) {
		tw(t, f, "creates a worktree in tmpdir", func(t *testing.T) {
			repo := initGitRepo(t)
			wt := createWorktree(osGit, repo, "test-id-1")
			if wt == nil {
				t.Fatal("no worktree")
			}
			defer removeWT(repo, wt.Path)
			eq(t, exists(wt.Path), true)
			eq(t, wt.Branch, "pi-agent-test-id-1")
			eq(t, wt.BaseSHA, gitOut(t, repo, "rev-parse", "HEAD"))
			eq(t, exists(filepath.Join(wt.Path, "README.md")), true)
			if !strings.HasPrefix(wt.Path, filepath.Join(os.TempDir(), "pi-agent-test-id-1-")) {
				t.Fatalf("path %s", wt.Path)
			}
		})
		tw(t, f, "returns undefined for non-git directory", func(t *testing.T) {
			eq(t, createWorktree(osGit, t.TempDir(), "test-id-2") == nil, true)
		})
		tw(t, f, "returns undefined for git repo with no commits", func(t *testing.T) {
			empty := t.TempDir()
			gitOut(t, empty, "init")
			eq(t, createWorktree(osGit, empty, "no-commits") == nil, true)
		})
		tw(t, f, "returns undefined when `git worktree add` reports a non-zero exit", func(t *testing.T) {
			repo := initGitRepo(t)
			run, _ := failingGit(func(a []string) bool { return a[0] == "worktree" && a[1] == "add" }, execResult{Code: 128})
			eq(t, createWorktree(run, repo, "add-fails") == nil, true)
		})
		tw(t, f, "returns undefined when a git call is killed by its timeout", func(t *testing.T) {
			repo := initGitRepo(t)
			run, _ := failingGit(func(a []string) bool { return a[0] == "rev-parse" && a[1] == "HEAD" }, execResult{Code: 0, Killed: true})
			eq(t, createWorktree(run, repo, "timed-out") == nil, true)
		})
		tw(t, f, "workPath equals path when created from the repo root", func(t *testing.T) {
			repo := initGitRepo(t)
			wt := createWorktree(osGit, repo, "root-wp")
			defer removeWT(repo, wt.Path)
			eq(t, wt.WorkPath, wt.Path)
		})
		tw(t, f, "workPath preserves subdirectory scoping (monorepo package cwd)", func(t *testing.T) {
			repo := initGitRepo(t)
			mustMkdir(t, filepath.Join(repo, "packages", "api"))
			mustWrite(t, filepath.Join(repo, "packages", "api", "index.ts"), "export {}")
			gitOut(t, repo, "add", "-A")
			gitOut(t, repo, "commit", "-m", "add package")
			wt := createWorktree(osGit, filepath.Join(repo, "packages", "api"), "subdir-wp")
			if wt == nil {
				t.Fatal("no worktree")
			}
			defer removeWT(repo, wt.Path)
			eq(t, wt.WorkPath, filepath.Join(wt.Path, "packages", "api"))
			eq(t, exists(wt.WorkPath), true)
		})
		tw(t, f, "uses unique paths for multiple worktrees", func(t *testing.T) {
			repo := initGitRepo(t)
			a, b := createWorktree(osGit, repo, "multi-1"), createWorktree(osGit, repo, "multi-2")
			if a == nil || b == nil {
				t.Fatal("no worktree")
			}
			defer removeWT(repo, a.Path)
			defer removeWT(repo, b.Path)
			if a.Path == b.Path {
				t.Fatal("same path")
			}
		})
		tw(t, f, "creates worktrees concurrently — the git calls do not serialize on one another", func(t *testing.T) {
			repo := initGitRepo(t)
			// Both chains must have their first git call in flight at once: a runner that serialized the two
			// creations would never let the second one in while the first waits, and the barrier would time out.
			var started sync.WaitGroup
			started.Add(2)
			var once [2]sync.Once
			var mu sync.Mutex
			next := 0
			ids := map[string]int{}
			timedOut := false
			run := func(args []string, cwd string, timeoutMS int) execResult {
				mu.Lock()
				key := cwd + "|" + strings.Join(args, " ")
				_ = key
				mu.Unlock()
				if args[0] == "rev-parse" && args[1] == "--is-inside-work-tree" {
					mu.Lock()
					n := next
					next++
					mu.Unlock()
					if n < 2 {
						once[n].Do(func() { started.Done() })
						ch := make(chan struct{})
						go func() { started.Wait(); close(ch) }()
						select {
						case <-ch:
						case <-time.After(10 * time.Second):
							mu.Lock()
							timedOut = true
							mu.Unlock()
						}
					}
				}
				return osGit(args, cwd, timeoutMS)
			}
			_ = ids
			var wts [2]*worktreeInfo
			var wg sync.WaitGroup
			for i, id := range []string{"par-1", "par-2"} {
				wg.Add(1)
				go func() { defer wg.Done(); wts[i] = createWorktree(run, repo, id) }()
			}
			wg.Wait()
			if timedOut {
				t.Fatal("the second creation was blocked by the first")
			}
			if wts[0] == nil || wts[1] == nil {
				t.Fatal("no worktree")
			}
			removeWT(repo, wts[0].Path)
			removeWT(repo, wts[1].Path)
		})
	})

	t.Run("cleanupWorktree", func(t *testing.T) {
		branchExists := func(t *testing.T, repo, b string) bool { return gitOut(t, repo, "branch", "--list", b) != "" }
		tw(t, f, "removes worktree when no changes made", func(t *testing.T) {
			repo := initGitRepo(t)
			wt := createWorktree(osGit, repo, "clean-1")
			res := cleanupWorktree(osGit, repo, wt, "test cleanup")
			eq(t, res, worktreeCleanupResult{})
			eq(t, exists(wt.Path), false)
		})
		tw(t, f, "commits changes and creates branch when changes exist", func(t *testing.T) {
			repo := initGitRepo(t)
			wt := createWorktree(osGit, repo, "dirty-1")
			mustWrite(t, filepath.Join(wt.Path, "new-file.txt"), "agent wrote this")
			res := cleanupWorktree(osGit, repo, wt, "added new file")
			eq(t, res.HasChanges, true)
			if !strings.Contains(res.Branch, "pi-agent-dirty-1") || !branchExists(t, repo, res.Branch) {
				t.Fatalf("branch %q", res.Branch)
			}
			if log := gitOut(t, repo, "log", "--oneline", "-1", res.Branch); !strings.Contains(log, "pi-agent: added new file") {
				t.Fatal(log)
			}
			eq(t, res.Path, wt.Path)
		})
		tw(t, f, "commits changes even when a pre-commit hook rejects (--no-verify)", func(t *testing.T) {
			repo := initGitRepo(t)
			mustWrite(t, filepath.Join(repo, ".git", "hooks", "pre-commit"), "#!/bin/sh\nexit 1\n")
			_ = os.Chmod(filepath.Join(repo, ".git", "hooks", "pre-commit"), 0o755)
			wt := createWorktree(osGit, repo, "hooked-1")
			mustWrite(t, filepath.Join(wt.Path, "hooked-file.txt"), "agent wrote this")
			res := cleanupWorktree(osGit, repo, wt, "hook should not block")
			eq(t, res.HasChanges, true)
			eq(t, res.Branch, "pi-agent-hooked-1")
		})
		tw(t, f, "creates branch when worktree is clean but HEAD moved", func(t *testing.T) {
			repo := initGitRepo(t)
			wt := createWorktree(osGit, repo, "committed-1")
			mustWrite(t, filepath.Join(wt.Path, "committed-file.txt"), "agent committed this")
			gitOut(t, wt.Path, "add", "committed-file.txt")
			gitOut(t, wt.Path, "commit", "-m", "agent commit")
			agent := gitOut(t, wt.Path, "rev-parse", "HEAD")
			res := cleanupWorktree(osGit, repo, wt, "already committed")
			eq(t, res.HasChanges, true)
			eq(t, res.Branch, "pi-agent-committed-1")
			eq(t, gitOut(t, repo, "rev-parse", res.Branch), agent)
			eq(t, exists(wt.Path), false)
		})
		tw(t, f, "does not force-overwrite existing branch", func(t *testing.T) {
			repo := initGitRepo(t)
			wt1 := createWorktree(osGit, repo, "conflict-1")
			mustWrite(t, filepath.Join(wt1.Path, "file1.txt"), "first run")
			r1 := cleanupWorktree(osGit, repo, wt1, "first")
			eq(t, r1.Branch, "pi-agent-conflict-1")
			wt2 := createWorktree(osGit, repo, "conflict-1")
			mustWrite(t, filepath.Join(wt2.Path, "file2.txt"), "second run")
			r2 := cleanupWorktree(osGit, repo, wt2, "second")
			eq(t, r2.HasChanges, true)
			if r2.Branch == "pi-agent-conflict-1" || !strings.HasPrefix(r2.Branch, "pi-agent-conflict-1-") {
				t.Fatalf("branch %q", r2.Branch)
			}
			eq(t, wt2.Branch, r2.Branch) // the caller's info names the branch that was made
			list := gitOut(t, repo, "branch", "--list", "pi-agent-conflict-1*")
			if !strings.Contains(list, "pi-agent-conflict-1") || !strings.Contains(list, r2.Branch) {
				t.Fatal(list)
			}
		})
		tw(t, f, "handles already-deleted worktree gracefully", func(t *testing.T) {
			repo := initGitRepo(t)
			wt := createWorktree(osGit, repo, "gone-1")
			_ = os.RemoveAll(wt.Path)
			eq(t, cleanupWorktree(osGit, repo, wt, "already gone").HasChanges, false)
		})
		tw(t, f, "truncates commit message at 200 chars", func(t *testing.T) {
			repo := initGitRepo(t)
			wt := createWorktree(osGit, repo, "long-msg")
			mustWrite(t, filepath.Join(wt.Path, "change.txt"), "something")
			res := cleanupWorktree(osGit, repo, wt, strings.Repeat("x", 300))
			eq(t, res.HasChanges, true)
			eq(t, gitOut(t, repo, "log", "-1", "--format=%s", res.Branch), "pi-agent: "+strings.Repeat("x", 200))
		})
		tw(t, f, "falls back to pruning when `git worktree remove` fails", func(t *testing.T) {
			repo := initGitRepo(t)
			wt := createWorktree(osGit, repo, "remove-fails")
			defer removeWT(repo, wt.Path)
			run, calls := failingGit(func(a []string) bool { return a[0] == "worktree" && a[1] == "remove" }, execResult{Code: 1})
			res := cleanupWorktree(run, repo, wt, "removal fails")
			eq(t, res.HasChanges, false)
			pruned := false
			for _, c := range calls() {
				if c[0] == "worktree" && c[1] == "prune" {
					pruned = true
				}
			}
			eq(t, pruned, true)
		})
	})

	t.Run("pruneWorktrees", func(t *testing.T) {
		tw(t, f, "does not reject on a clean repo", func(t *testing.T) { pruneWorktrees(osGit, initGitRepo(t)) })
		tw(t, f, "does not reject on non-git directory", func(t *testing.T) { pruneWorktrees(osGit, t.TempDir()) })
	})

	t.Run("cleanupWorktree — failure path", func(t *testing.T) {
		tw(t, f, "short-circuits when the worktree directory is already gone", func(t *testing.T) {
			repo := initGitRepo(t)
			wt := createWorktree(osGit, repo, "vanished")
			_ = os.RemoveAll(wt.Path)
			res := cleanupWorktree(osGit, repo, wt, "agent that vanished")
			eq(t, res, worktreeCleanupResult{})
		})
		tw(t, f, "swallows a git failure inside a still-present worktree and reports no changes", func(t *testing.T) {
			repo := initGitRepo(t)
			wt := createWorktree(osGit, repo, "corrupt")
			mustWrite(t, filepath.Join(wt.Path, "work.txt"), "agent output")
			mustWrite(t, filepath.Join(wt.Path, ".git"), "gitdir: /nonexistent/path/that/is/not/a/repo")
			res := cleanupWorktree(osGit, repo, wt, "corrupted agent")
			eq(t, res, worktreeCleanupResult{})
		})
		tw(t, f, "reports no changes when the preservation commit fails", func(t *testing.T) {
			repo := initGitRepo(t)
			wt := createWorktree(osGit, repo, "commit-fails")
			mustWrite(t, filepath.Join(wt.Path, "work.txt"), "agent output")
			run, _ := failingGit(func(a []string) bool { return a[0] == "commit" }, execResult{Code: 1})
			res := cleanupWorktree(run, repo, wt, "commit fails")
			eq(t, res, worktreeCleanupResult{})
		})
		tw(t, f, "creates the branch BEFORE removing the worktree, so a removal failure cannot lose commits", func(t *testing.T) {
			repo := initGitRepo(t)
			wt := createWorktree(osGit, repo, "ordered")
			mustWrite(t, filepath.Join(wt.Path, "work.txt"), "agent output")
			var order []string
			run := func(args []string, cwd string, timeoutMS int) execResult {
				order = append(order, args[0]+" "+args[1])
				return osGit(args, cwd, timeoutMS)
			}
			res := cleanupWorktree(run, repo, wt, "ordered agent")
			eq(t, res.HasChanges, true)
			if gitOut(t, repo, "branch", "--list", res.Branch) == "" {
				t.Fatal("no branch")
			}
			eq(t, exists(wt.Path), false)
			if !strings.Contains(gitOut(t, repo, "ls-tree", "--name-only", res.Branch), "work.txt") {
				t.Fatal("commit not reachable")
			}
			bi, ri := indexOf(order, "branch "+res.Branch), indexOf(order, "worktree remove")
			if bi < 0 || ri < 0 || bi > ri {
				t.Fatalf("branch must precede removal: %v", order)
			}
		})
	})

	t.Run("worktree isolation switch", func(t *testing.T) {
		t.Cleanup(func() { setWorktreeIsolationEnabled(true) })
		tw(t, f, "defaults to enabled", func(t *testing.T) { eq(t, isWorktreeIsolationEnabled(), true) })
		tw(t, f, "round-trips both ways", func(t *testing.T) {
			setWorktreeIsolationEnabled(false)
			eq(t, isWorktreeIsolationEnabled(), false)
			setWorktreeIsolationEnabled(true)
			eq(t, isWorktreeIsolationEnabled(), true)
		})
		tw(t, f, "does not disable createWorktree directly", func(t *testing.T) {
			repo := initGitRepo(t)
			defer setWorktreeIsolationEnabled(true)
			setWorktreeIsolationEnabled(false)
			wt := createWorktree(osGit, repo, "switch-test")
			if wt == nil {
				t.Fatal("no worktree")
			}
			cleanupWorktree(osGit, repo, wt, "switch test")
			pruneWorktrees(osGit, repo)
		})
	})
}

func indexOf(list []string, s string) int {
	for i, e := range list {
		if strings.HasPrefix(e, s) {
			return i
		}
	}
	return -1
}
