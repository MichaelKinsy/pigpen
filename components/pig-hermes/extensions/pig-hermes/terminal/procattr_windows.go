//go:build windows

package terminal

import (
	"os/exec"
	"syscall"
)

func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
}

func killProcGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}

func shellCommand(command string) (string, []string) {
	if ps, err := exec.LookPath("powershell.exe"); err == nil && ps != "" {
		return ps, []string{"-NoProfile", "-Command", command}
	}
	return "cmd.exe", []string{"/C", command}
}
