//go:build windows

package websearch

import (
	"os/exec"
	"syscall"
)

func hideWindow(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true} }

// detachProcess is not used on Windows: the browser is started with `cmd /c start` there.
func detachProcess(*exec.Cmd) {}
