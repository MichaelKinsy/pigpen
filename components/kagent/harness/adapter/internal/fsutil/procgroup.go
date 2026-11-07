//go:build unix

package fsutil

import (
	"os"
	"os/exec"
	"syscall"
)

// ConfigureProcessGroup puts the child in its own process group so tools it
// started can be signaled with it.
func ConfigureProcessGroup(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// TerminateProcessGroup sends SIGTERM to the child's process group.
func TerminateProcessGroup(process *os.Process) error {
	return syscall.Kill(-process.Pid, syscall.SIGTERM)
}

// KillProcessGroup sends SIGKILL to the child's process group.
func KillProcessGroup(process *os.Process) error {
	return syscall.Kill(-process.Pid, syscall.SIGKILL)
}
