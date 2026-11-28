//go:build !windows

package ast

import (
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"time"
)

// configureProcessGroup starts the child process in its own process group so
// cancellation and session shutdown reap the entire process tree.
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessTree terminates the child process group cleanly, escalating to SIGKILL.
func killProcessTree(proc *os.Process, done <-chan struct{}, grace time.Duration) {
	if proc == nil || proc.Pid <= 0 {
		return
	}
	pid := proc.Pid
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	if grace > 0 {
		select {
		case <-done:
			return
		case <-time.After(grace):
		}
	}
	if runtime.GOOS == "darwin" {
		_ = syscall.Kill(-pid, syscall.SIGSTOP)
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}
