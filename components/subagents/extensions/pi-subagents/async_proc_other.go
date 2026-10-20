//go:build !unix

package pi_subagents

import (
	"os"
	"os/exec"
	"syscall"
)

func readPidNamespaceScope() string { return "" }

func defaultKill(pid, signal int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return errNoSuchProcess
	}
	if signal == 0 {
		// Windows has no existence probe signal: FindProcess succeeded, the handle is the proof.
		_ = p.Release()
		return nil
	}
	if err := p.Kill(); err != nil {
		return errNoSuchProcess
	}
	return nil
}

// detachBackground hides the runner's console window (a new console would steal the user's terminal).
func detachBackground(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

func signalGroup(pgid, signal int) error { return defaultKill(pgid, signal) }

const (
	signalTerm = 15
	signalKill = 9
)

func groupHasMembers(pgid int) bool { return defaultKill(pgid, 0) == nil }

const backgroundSupported = false
