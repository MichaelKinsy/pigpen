//go:build !windows

package tintinweb_subagents

import (
	"os/exec"
	"syscall"
	"time"
)

// setProcessGroup puts the child in its own process group, so a stop reaches what it started.
func setProcessGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

// terminate sends SIGTERM to the child's group and SIGKILL five seconds later if it is still there.
func terminate(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	go func() {
		time.Sleep(5 * time.Second)
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}()
}
