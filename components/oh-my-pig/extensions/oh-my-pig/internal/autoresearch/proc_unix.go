//go:build !windows

package autoresearch

import (
	"os/exec"
	"syscall"
)

// setProcessGroup starts the benchmark harness in its own process group so
// timeout or user abort can signal and reap the entire child process tree.
// upstream: packages/coding-agent/src/autoresearch/helpers.ts:145-155 (`killTree`)
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
