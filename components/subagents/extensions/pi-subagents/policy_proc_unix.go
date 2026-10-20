//go:build !windows

package pi_subagents

import (
	"errors"
	"syscall"
)

// processAlive is process.kill(pid, 0): the process exists (a permission error means it does).
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
