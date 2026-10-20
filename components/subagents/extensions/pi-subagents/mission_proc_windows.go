//go:build windows

package pi_subagents

import (
	"os/exec"
	"syscall"
)

func missionPrepareCmd(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true} }
