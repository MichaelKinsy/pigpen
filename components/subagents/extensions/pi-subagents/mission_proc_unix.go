//go:build !windows

package pi_subagents

import (
	"os/exec"
)

func missionPrepareCmd(*exec.Cmd) {}
