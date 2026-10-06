package tintinweb_tasks

import "time"

// SetRPCTimeouts shortens the spawn and stop RPC timeouts (30 s and 10 s in the original) for a test and
// returns the function that restores them. The original's tests advance fake timers instead.
func SetRPCTimeouts(spawn, stop time.Duration) func() {
	ps, pt := spawnTimeout, stopTimeout
	spawnTimeout, stopTimeout = spawn, stop
	return func() { spawnTimeout, stopTimeout = ps, pt }
}
