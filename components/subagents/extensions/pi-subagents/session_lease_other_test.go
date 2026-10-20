//go:build !unix

package pi_subagents

import "testing"

func leasePidAlive(pid int) bool        { return false }
func leaseKillPid(pid int, hard bool)   {}
func leaseSpawnWriter(t *testing.T) int { t.Skip("POSIX writers only"); return 0 }
