//go:build !windows

package contextinfo

import "os/exec"

func hideWindow(*exec.Cmd) {}
