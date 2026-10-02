//go:build windows

package warden

import (
	"os/exec"
	"syscall"
)

// hideWindow keeps a console window from flashing for each git call.
func hideWindow(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true} }
