//go:build !windows

package websearch

import (
	"os/exec"
	"syscall"
)

func hideWindow(*exec.Cmd) {}

// detachProcess starts the command in its own session, as Node's `detached: true` does, so it
// outlives the extension and is not signalled with its process group.
func detachProcess(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
