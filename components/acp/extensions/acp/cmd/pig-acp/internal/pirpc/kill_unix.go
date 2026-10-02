//go:build !windows

package pirpc

import (
	"os/exec"
	"syscall"
	"time"
)

// terminate asks the child to stop (SIGTERM, like the original) and kills it if it lingers.
func terminate(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	proc := cmd.Process
	time.AfterFunc(5*time.Second, func() { _ = proc.Kill() })
}
