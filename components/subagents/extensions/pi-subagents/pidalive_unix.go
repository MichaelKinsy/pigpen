//go:build unix

package pi_subagents

import (
	"errors"
	"syscall"
)

// pidState is a pid liveness probe result, as the original's PidLiveness.
type pidState int

const (
	pidAlive   pidState = iota
	pidDead
	pidUnknown
)

// pidLiveness probes whether pid exists, the way the original's checkPidLiveness calls kill(pid, 0). EPERM means the
// process exists but is not ours to signal; the original reports that as unknown, and so does this.
func pidLiveness(pid int) pidState {
	if pid <= 0 {
		return pidUnknown
	}
	err := syscall.Kill(pid, 0)
	switch {
	case err == nil:
		return pidAlive
	case errors.Is(err, syscall.ESRCH):
		return pidDead
	case errors.Is(err, syscall.EPERM):
		return pidUnknown
	}
	return pidUnknown
}
