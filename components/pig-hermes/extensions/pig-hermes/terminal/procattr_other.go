//go:build !windows

package terminal

import (
	"os/exec"
	"syscall"
)

func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
}

func killProcGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return nil
	}
	// Kill entire process group first; fall back to direct process kill.
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		return cmd.Process.Kill()
	}
	return nil
}

func shellCommand(command string) (string, []string) {
	if bashPath, err := exec.LookPath("bash"); err == nil && bashPath != "" {
		return bashPath, []string{"-c", command}
	}
	return "/bin/sh", []string{"-c", command}
}
