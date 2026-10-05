//go:build windows

package native

import (
	"context"
	"os"
	"os/exec"
	"syscall"
)

const (
	createNewProcessGroup = 0x00000200
	detachedProcess       = 0x00000008
)

// detach starts the child without a console and in a group of its own, so that
// closing the terminal that started it does not stop it.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup | detachedProcess, HideWindow: true}
}

func killTree(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func killPID(pid int) {
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}

func pidAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Windows FindProcess opens the process; a missing one fails above.
	_ = p.Release()
	return true
}

// lockFile: on Windows the daemon's named pipe is the lock (a second pipe of the
// same name cannot be created), so starters do not need one.
func lockFile(ctx context.Context, path string) (func(), error) { return func() {}, nil }

func tryLockFile(path string) (func(), bool, error) { return func() {}, true, nil }
