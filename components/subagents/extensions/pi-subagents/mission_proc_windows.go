//go:build windows

package pi_subagents

import (
	"errors"
	"os/exec"
	"syscall"
)

// stillActive is the exit code GetExitCodeProcess reports for a running process.
const stillActive = 259

// missionProcessAlive reports whether a process with this pid is running (an access error means it exists).
func missionProcessAlive(pid int) bool {
	h, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		return errors.Is(err, syscall.ERROR_ACCESS_DENIED)
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if syscall.GetExitCodeProcess(h, &code) != nil {
		return true
	}
	return code == stillActive
}

func missionPrepareCmd(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true} }
