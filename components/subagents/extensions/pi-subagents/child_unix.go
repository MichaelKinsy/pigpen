//go:build unix

package pi_subagents

import (
	"os"
	"syscall"
)

// terminate asks a cancelled child to stop (pig ends its own tools and extensions on SIGTERM).
func terminate(p *os.Process) { _ = p.Signal(syscall.SIGTERM) }
