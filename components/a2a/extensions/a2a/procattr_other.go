//go:build !windows

package a2aext

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// configureProcess puts the worker in its own process group so a tool it started
// (a shell, for instance) dies with it.
func configureProcess(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

func terminateTree(p *os.Process) error { return ignoreGone(syscall.Kill(-p.Pid, syscall.SIGTERM)) }

func killTree(p *os.Process) error { return ignoreGone(syscall.Kill(-p.Pid, syscall.SIGKILL)) }

func ignoreGone(err error) error {
	if errors.Is(err, syscall.ESRCH) || errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}
