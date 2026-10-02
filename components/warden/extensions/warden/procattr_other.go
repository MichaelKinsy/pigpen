//go:build !windows

package warden

import "os/exec"

// hideWindow is a no-op off Windows.
func hideWindow(*exec.Cmd) {}
