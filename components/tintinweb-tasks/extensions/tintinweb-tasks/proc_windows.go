//go:build windows

package tintinweb_tasks

import "os"

// isProcessRunning reports whether pid can be opened. upstream: task-store.ts:73-75.
func isProcessRunning(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	p.Release()
	return true
}
