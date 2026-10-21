//go:build !windows

package tintinweb_subagents

import "syscall"

// processRunning reports whether a process with this id exists (signal 0). Any error, EPERM included, counts as not
// running, as in the original's `process.kill(pid, 0)` in a try/catch. upstream: src/schedule-store.ts isProcessRunning.
func processRunning(pid int) bool { return syscall.Kill(pid, 0) == nil }
