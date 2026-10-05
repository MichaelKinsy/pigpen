//go:build !unix

package pi_subagents

import "os"

// terminate stops a cancelled child (no SIGTERM here).
func terminate(p *os.Process) { _ = p.Kill() }
