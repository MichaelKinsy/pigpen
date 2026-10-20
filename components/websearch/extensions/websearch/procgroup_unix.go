//go:build unix

package websearch

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

// setProcessGroup runs the command in its own process group so git and gh helpers cannot survive a
// timeout or a cancellation and keep reading from the host terminal.
func setProcessGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

// terminateProcessTree is terminateProcessTree of github-extract.ts: SIGTERM to the group, and
// SIGKILL to the group after the grace, because a credential helper may ignore SIGTERM.
func terminateProcessTree(cmd *exec.Cmd) {
	pid := cmd.Process.Pid
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		_ = cmd.Process.Signal(syscall.SIGTERM)
	}
	time.AfterFunc(processKillGrace, func() {
		if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
			_ = cmd.Process.Kill()
		}
	})
}

// processGone is processNonexistenceProven: kill(pid, 0) fails with ESRCH.
func processGone(pid int) bool { return syscall.Kill(pid, 0) == syscall.ESRCH }

// openNoFollow opens a file without following a symlink at the final component.
func openNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
}
