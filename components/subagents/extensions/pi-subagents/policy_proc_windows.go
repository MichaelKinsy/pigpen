//go:build windows

package pi_subagents

import "os"

// processAlive reports whether the process exists (os.FindProcess opens it on Windows).
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = p.Release()
	return true
}
