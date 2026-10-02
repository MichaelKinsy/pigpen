//go:build windows

package a2aext

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

const createNewProcessGroup = 0x00000200

// configureProcess hides the console window and starts a new process group.
func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNewProcessGroup}
}

func terminateTree(p *os.Process) error { return killTree(p) }

func killTree(p *os.Process) error {
	if err := p.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}
