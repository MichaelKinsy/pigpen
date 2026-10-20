//go:build !windows

package pi_subagents

import (
	"errors"
	"os/exec"
	"syscall"
)

// missionProcessAlive is process.kill(pid, 0): the process exists (EPERM means it exists and is not ours).
func missionProcessAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func missionPrepareCmd(*exec.Cmd) {}
