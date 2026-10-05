//go:build !linux

package pi_subagents

import "os/exec"

// configureChild: only Linux has a parent-death signal; elsewhere a cancelled call still stops its child (terminate).
func configureChild(cmd *exec.Cmd) {}

func lockThread() func() { return func() {} }
