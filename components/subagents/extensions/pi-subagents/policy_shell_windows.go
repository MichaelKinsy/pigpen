//go:build windows

package pi_subagents

import (
	"os"
	"os/exec"
	"syscall"
)

// shellCommand runs command through cmd.exe as Node's spawn(command, {shell: true}) does (`cmd.exe /d /s /c "<command>"`).
func shellCommand(command string) *exec.Cmd {
	shell := os.Getenv("ComSpec")
	if shell == "" {
		shell = "cmd.exe"
	}
	cmd := exec.Command(shell)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CmdLine: `/d /s /c "` + command + `"`}
	return cmd
}
