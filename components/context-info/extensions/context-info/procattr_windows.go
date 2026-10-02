//go:build windows

package contextinfo

import (
	"os/exec"
	"syscall"
)

// hideWindow stops a console window flashing for each git call.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
