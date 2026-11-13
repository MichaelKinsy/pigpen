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

// AttachProcessGroup is called once after the child started. The process group
// of a unix child exists from its first instruction, so there is nothing to do.
func AttachProcessGroup(*os.Process) error { return nil }

// TerminateProcessGroup sends SIGTERM to the child's process group.
func TerminateProcessGroup(process *os.Process) error {
	return syscall.Kill(-process.Pid, syscall.SIGTERM)
}

// KillProcessGroup sends SIGKILL to the child's process group.
func KillProcessGroup(process *os.Process) error {
	return syscall.Kill(-process.Pid, syscall.SIGKILL)
}
