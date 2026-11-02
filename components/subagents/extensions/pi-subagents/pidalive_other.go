//go:build !unix && !windows

package pi_subagents

// pidState is a pid liveness probe result, as the original's PidLiveness.
type pidState int

const (
	pidAlive   pidState = iota
	pidDead
	pidUnknown
)

// pidLiveness has no probe on this platform: every pid is unknown, and reconciliation applies its grace window.
func pidLiveness(pid int) pidState {
	return pidUnknown
}
