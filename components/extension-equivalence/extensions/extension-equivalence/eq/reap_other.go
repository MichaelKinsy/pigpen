//go:build !linux

package eq

// killRun does nothing off Linux: there is no /proc to find a worker that left the process group.
// A worker that escaped its group on macOS or Windows still outlives its mutant's run.
func killRun(marker string) {}
