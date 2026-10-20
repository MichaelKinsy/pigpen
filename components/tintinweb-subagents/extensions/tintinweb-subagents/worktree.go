package tintinweb_subagents

import "sync/atomic"

// Git worktree isolation. upstream: src/worktree.ts.

type worktreeInfo struct {
	Path     string // the copied repo's root
	Branch   string
	BaseSHA  string
	WorkPath string // where the agent works inside the copy
}

type worktreeCleanupResult struct {
	HasChanges bool
	Branch     string
	Path       string
}

type execResult struct {
	Stdout, Stderr string
	Code           int
	Killed         bool
}

// gitFn runs `git args...` in cwd with a timeout in milliseconds and reports the result, never an error.
type gitFn func(args []string, cwd string, timeoutMS int) execResult

var worktreeIsolationDisabled atomic.Bool

func setWorktreeIsolationEnabled(enabled bool) {}
func isWorktreeIsolationEnabled() bool         { return false }

func createWorktree(run gitFn, cwd, agentID string) *worktreeInfo { return nil }
func cleanupWorktree(run gitFn, cwd string, wt *worktreeInfo, description string) worktreeCleanupResult {
	return worktreeCleanupResult{}
}
func pruneWorktrees(run gitFn, cwd string)                      {}
func osGit(args []string, cwd string, timeoutMS int) execResult { return execResult{} }
