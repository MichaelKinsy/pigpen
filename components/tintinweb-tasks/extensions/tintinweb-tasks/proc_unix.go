//go:build !windows

package tintinweb_tasks

import "syscall"

// isProcessRunning asks the local process table whether pid exists (process.kill(pid, 0); any error, EPERM
// included, reads as not running, as in the original). upstream: task-store.ts:73-75.
func isProcessRunning(pid int) bool { return syscall.Kill(pid, 0) == nil }
