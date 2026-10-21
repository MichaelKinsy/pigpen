//go:build !unix

package pi_goal_x

import (
	"os"
	"os/exec"
)

// There are no process groups or SIGTERM here: stopping the child kills it.
func configureAuditChild(cmd *exec.Cmd) {}
func lockAuditThread() func()           { return func() {} }
func terminateAuditChild(p *os.Process) { _ = p.Kill() }
func killAuditChildGroup(p *os.Process) { _ = p.Kill() }
