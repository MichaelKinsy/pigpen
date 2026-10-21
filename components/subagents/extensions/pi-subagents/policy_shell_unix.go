//go:build !windows

package pi_subagents

import "os/exec"

// shellCommand runs command through the shell as Node's spawn(command, {shell: true}) does (/bin/sh -c).
func shellCommand(command string) *exec.Cmd { return exec.Command(posixShell, "-c", command) }
