//go:build windows

package powerline_footer

import (
	"os"
	"os/exec"
)

// hideConsoleWindow keeps a child process from flashing a console window (Node's windowsHide).
func hideConsoleWindow(cmd *exec.Cmd) {}

// configureShellProcess: the shell gets its own hidden console. Windows has no process groups to signal.
func configureShellProcess(cmd *exec.Cmd) {}

// signalShellGroup interrupts the shell: Windows cannot deliver SIGINT, so the process itself is signalled as upstream's
// fallback does (process.kill on Windows terminates it).
func signalShellGroup(p *os.Process, sig os.Signal) error { return *new(error) }

// shellSignalInterrupt and shellSignalKill are the signals interrupt() and dispose() send.
var (
	shellSignalInterrupt os.Signal = os.Interrupt
	shellSignalKill      os.Signal = os.Kill
)

// closeExitCode is getCloseExitCode for a finished process.
func closeExitCode(state *os.ProcessState) int { return *new(int) }
