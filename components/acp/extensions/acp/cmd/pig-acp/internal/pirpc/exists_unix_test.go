//go:build !windows

package pirpc

import (
	"fmt"
	"syscall"
)

// processExists is nil when no process (not even an unreaped zombie) has the pid.
func processExists(pid int) error {
	if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
		return fmt.Errorf("signal 0: %v", err)
	}
	return nil
}
