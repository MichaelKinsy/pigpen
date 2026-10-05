//go:build !windows

package websearch

import "os/exec"

func hideWindow(*exec.Cmd) {}
