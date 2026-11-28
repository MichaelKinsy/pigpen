//go:build !windows

package github

import (
	"os/exec"
	"syscall"
)

// setProcessGroup starts the child in a new session (detached from any
// controlling terminal so ssh/git/gh cannot prompt on /dev/tty) with pgid == pid.
// upstream: crates/vcs/src/git/cli.rs:241-244 (detach_from_terminal)
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
