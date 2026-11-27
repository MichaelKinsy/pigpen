//go:build !windows

package media

import (
	"os/exec"
	"syscall"
)

func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	// Negative PID targets the entire process group created by Setpgid.
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
