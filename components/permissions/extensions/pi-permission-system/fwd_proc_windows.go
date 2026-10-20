//go:build windows

package pi_permission_system

import "syscall"

const (
	processQueryLimitedInformation = 0x1000
	stillActive                    = 259
)

// isRunningProcess is whether pid names a running process: a handle that can be opened and whose exit code is STILL_ACTIVE. Access
// denied means the process exists under another account and reads as alive, as EPERM does elsewhere.
func isRunningProcess(pid int) bool {
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return err == syscall.ERROR_ACCESS_DENIED
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}
