//go:build !windows

package powerline_footer

import (
	"os"
	"os/exec"
	"syscall"
)

// hideConsoleWindow is a no-op outside Windows.
func hideConsoleWindow(cmd *exec.Cmd) {}

// configureShellProcess: the shell leads a new session, so its process group (itself and what it runs) can be signalled whole
// (upstream: spawn with detached: true).
func configureShellProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// signalShellGroup signals the shell's process group, falling back to the process alone when the group is gone.
// upstream: shell-session.ts:196 (process.kill(-pid, sig) then process.kill(sig)).
func signalShellGroup(p *os.Process, sig os.Signal) error {
	if s, ok := sig.(syscall.Signal); ok {
		if err := syscall.Kill(-p.Pid, s); err == nil {
			return nil
		}
	}
	return p.Signal(sig)
}

var (
	shellSignalInterrupt os.Signal = syscall.SIGINT
	shellSignalKill      os.Signal = syscall.SIGKILL
)

// closeExitCode is getCloseExitCode: the exit status, or 128+signal for the three signals upstream names (SIGINT 130, SIGTERM
// 143, SIGKILL 137), and 1 for anything else. upstream: shell-session.ts:23.
func closeExitCode(state *os.ProcessState) int {
	if ws, ok := state.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		switch ws.Signal() {
		case syscall.SIGINT:
			return 130
		case syscall.SIGTERM:
			return 143
		case syscall.SIGKILL:
			return 137
		}
		return 1
	}
	return state.ExitCode()
}
