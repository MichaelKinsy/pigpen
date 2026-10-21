//go:build !windows

package pirpc

import (
	"os/exec"
	"syscall"
	"time"
)

// termGrace is how long a child may take to stop after SIGTERM before it is killed.
const termGrace = 5 * time.Second

// terminate asks the child to stop (SIGTERM, like the original) and kills it if it lingers.
func terminate(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	proc := cmd.Process
	time.AfterFunc(termGrace, func() { _ = proc.Kill() })
}
