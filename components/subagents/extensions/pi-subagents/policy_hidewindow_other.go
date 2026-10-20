//go:build !windows

package pi_subagents

import "os/exec"

// hideWindow is windowsHide: true of the original's child processes (nothing off Windows).
func hideWindow(*exec.Cmd) {}
