//go:build !windows

package shell

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// defaultShellConfig resolves bash on Unix: bash on PATH first (so modern bash
// 5.x from Homebrew/Nix is chosen ahead of macOS's /bin/bash 3.2), then
// /bin/bash, then sh. Both Homebrew bash and /bin/bash support bash features
// including /dev/tcp needed by CommandPreamble.
func defaultShellConfig() (shellPath string, shellArgs []string, err error) {
	if p, lookErr := exec.LookPath("bash"); lookErr == nil && p != "" {
		return p, []string{"-c"}, nil
	}
	if _, statErr := os.Stat("/bin/bash"); statErr == nil {
		return "/bin/bash", []string{"-c"}, nil
	}
	return "sh", []string{"-c"}, nil
}

// isBashShell reports whether shellPath resolves to a bash binary (and thus
// supports bash syntax such as shopt, declare, compgen, and /dev/tcp).
func isBashShell(shellPath string) bool {
	base := strings.ToLower(filepath.Base(shellPath))
	return base == "bash" || base == "bash.exe" || strings.HasPrefix(base, "bash-")
}

// configureProcessGroup starts the command as the leader of a new session and
// process group so killing -pid reaps the entire descendant tree and prevents
// interactive subshells from taking over /dev/tty.
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// attachProcessGroup is a no-op on Unix (Setsid already established PGID == pid).
func attachProcessGroup(_ int) {}

// killProcessGroupByPID sends SIGTERM to process group -pgid, waits up to grace
// while any process in that group remains alive (checked via kill(-pgid, 0)),
// and ALWAYS escalates to SIGKILL on -pgid if any member of the group survived.
//
// Unlike waiting only on cmd.Wait(), polling the process group ensures that
// TERM-ignoring descendants (e.g. `trap "" TERM; sleep 60`) are still SIGKILLed
// after the shell itself exits on SIGTERM.
func killProcessGroupByPID(pgid int, done <-chan struct{}, grace time.Duration) {
	if pgid <= 0 {
		return
	}
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	if grace > 0 {
		deadline := time.Now().Add(grace)
		// Wait until either the grace expires or BOTH the shell has exited AND
		// no process remains in the process group.
		shellExited := false
		for time.Now().Before(deadline) {
			if !shellExited && done != nil {
				select {
				case <-done:
					shellExited = true
				default:
				}
			}
			if (shellExited || done == nil) && !isProcessGroupAlive(pgid) {
				return
			}
			time.Sleep(25 * time.Millisecond)
		}
	}
	killProcessGroupImmediate(pgid)
}

// killProcessTreeGraceful sends SIGTERM to the process group and escalates to
// SIGKILL on the group after grace if any process in the group is still alive.
func killProcessTreeGraceful(proc *os.Process, done <-chan struct{}, grace time.Duration) {
	if proc == nil || proc.Pid <= 0 {
		return
	}
	killProcessGroupByPID(proc.Pid, done, grace)
}

// killProcessGroupImmediate sends SIGKILL to process group -pgid.
// Every spawned shell starts as its own session/process-group leader via
// `Setsid: true`; when `kill(-pgid, SIGKILL)` returns ESRCH the entire group
// (including the leader) is already gone, so we never fall back to bare
// `kill(pgid, SIGKILL)` which could signal a reused PID after the shell was
// reaped by `cmd.Wait()`.
func killProcessGroupImmediate(pgid int) {
	if pgid <= 0 {
		return
	}
	if runtime.GOOS == "darwin" {
		_ = syscall.Kill(-pgid, syscall.SIGSTOP)
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}

// killProcessTreeImmediate sends SIGKILL to the process group led by proc.Pid.
func killProcessTreeImmediate(proc *os.Process) {
	if proc == nil || proc.Pid <= 0 {
		return
	}
	killProcessGroupImmediate(proc.Pid)
}

// isProcessGroupAlive reports whether any process in process group pgid exists.
func isProcessGroupAlive(pgid int) bool {
	if pgid <= 0 {
		return false
	}
	err := syscall.Kill(-pgid, 0)
	return err == nil || err == syscall.EPERM
}

// processExitCode reports the shell's exit code, mapping signal termination to
// 128 + signal number to match POSIX shell and PiG conventions.
func processExitCode(state *os.ProcessState) int {
	if state == nil {
		return -1
	}
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal())
	}
	return state.ExitCode()
}

// isProcessAlive reports whether a process with pid still exists.
func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// forceKillPID sends SIGKILL to a single pid (used as test cleanup fallback).
func forceKillPID(pid int) {
	if pid > 0 {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

// statePipeSupported reports whether the OS supports passing a state capture
// pipe via exec.Cmd.ExtraFiles (fd 3 on Unix). On Unix, baseline and post-run
// state dumps stream directly to Go memory over a pipe so inherited environment
// secrets never touch disk.
const statePipeSupported = true

// attachStatePipe attaches writeEnd as fd 3 (`ExtraFiles[0]`) on Unix.
func attachStatePipe(cmd *exec.Cmd, writeEnd *os.File) {
	cmd.ExtraFiles = append(cmd.ExtraFiles, writeEnd)
}
