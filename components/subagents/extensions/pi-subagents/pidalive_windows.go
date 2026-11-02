//go:build windows

package pi_subagents

import "syscall"

// pidState is a pid liveness probe result, as the original's PidLiveness.
type pidState int

const (
	pidAlive   pidState = iota
	pidDead
	pidUnknown
)

// Windows process-probe results: no such process, and a process that exists but whose handle is refused.
const (
	processQueryLimitedInformation = 0x1000
	errWindowsInvalidParameter     = syscall.Errno(87)
	errWindowsAccessDenied         = syscall.Errno(5)
)

// pidLiveness probes whether pid exists, the way the original's checkPidLiveness calls kill(pid, 0) on unix. An open
// handle means alive; ERROR_INVALID_PARAMETER means no such process; access denied means the process exists but is
// protected (the original reports EPERM as unknown, but on Windows it still proves existence).
func pidLiveness(pid int) pidState {
	if pid <= 0 {
		return pidUnknown
	}
	handle, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err == nil {
		_ = syscall.CloseHandle(handle)
		return pidAlive
	}
	if errno, ok := err.(syscall.Errno); ok {
		switch errno {
		case errWindowsInvalidParameter:
			return pidDead
		case errWindowsAccessDenied:
			return pidAlive
		}
	}
	return pidUnknown
}
