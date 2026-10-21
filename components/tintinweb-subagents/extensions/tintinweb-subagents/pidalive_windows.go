//go:build windows

package tintinweb_subagents

import "syscall"

const (
	processQueryLimitedInformation = 0x1000
	stillActive                    = 259
)

// processRunning reports whether a process with this id exists and has not exited. upstream: src/schedule-store.ts isProcessRunning.
func processRunning(pid int) bool {
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)
	var code uint32
	return syscall.GetExitCodeProcess(h, &code) == nil && code == stillActive
}
