//go:build !windows

package pi_subagents

// windowsHost is process.platform === "win32".
func windowsHost() bool { return false }
