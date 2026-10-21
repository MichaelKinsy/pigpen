//go:build unix

package pi_goal_x

import (
	"os"
	"syscall"
)

// terminateAuditChild asks a child pig to stop; pig ends its own tools and extensions on SIGTERM.
func terminateAuditChild(p *os.Process) { _ = p.Signal(syscall.SIGTERM) }

// killAuditChildGroup kills the child and everything it started: the child leads its own process group (configureAuditChild).
func killAuditChildGroup(p *os.Process) {
	if syscall.Kill(-p.Pid, syscall.SIGKILL) != nil {
		_ = p.Kill()
	}
}
