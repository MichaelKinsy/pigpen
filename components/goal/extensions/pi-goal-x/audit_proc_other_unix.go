//go:build unix && !linux

package pi_goal_x

import (
	"os/exec"
	"syscall"
)

// configureAuditChild puts the child in its own process group (only Linux has a parent-death signal).
func configureAuditChild(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

func lockAuditThread() func() { return func() {} }
