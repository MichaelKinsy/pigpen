//go:build !windows

package pi_permission_system

import (
	"errors"
	"syscall"
)

// isRunningProcess is whether pid names a running process. Signal 0 performs the permission and existence checks without
// delivering anything. EPERM means the process exists under another user: reported as alive, the direction that falls back to the
// timeout rather than abandoning a request someone may still answer.
func isRunningProcess(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
