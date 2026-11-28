//go:build !windows

package ssh

import (
	"os/exec"
	"syscall"
)

// setProcessGroup isolates the child ssh process into its own process group
// (pgid == pid) so timeout/cancellation kills the entire subtree including
// any descendants holding stdout/stderr open.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
