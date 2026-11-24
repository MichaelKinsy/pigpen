//go:build !windows

package shell

import (
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"time"
)

// defaultShellConfig resolves the system shell on Unix: /bin/bash, then bash on PATH, then sh.
// Mirrors PiG's internal/shellconfig/default_unix.go.
func defaultShellConfig() (shellPath string, shellArgs []string, err error) {
	if _, statErr := os.Stat("/bin/bash"); statErr == nil {
		return "/bin/bash", []string{"-c"}, nil
	}
	if p, lookErr := exec.LookPath("bash"); lookErr == nil && p != "" {
		return p, []string{"-c"}, nil
	}
	return "sh", []string{"-c"}, nil
}

// configureProcessGroup starts the command as the leader of a new session and
// process group so killing -pid reaps the entire descendant tree and prevents
// interactive subshells from taking over /dev/tty.
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// killProcessTreeGraceful sends SIGTERM to the process group, waits up to grace
// for done to close, then escalates to SIGKILL on the process group.
// On macOS, SIGSTOP is sent before SIGKILL to narrow the group-signal race where
// a killed child wakes a waiting shell before the shell receives SIGKILL.
func killProcessTreeGraceful(proc *os.Process, done <-chan struct{}, grace time.Duration) {
	if proc == nil || proc.Pid <= 0 {
		return
	}
	pgid := -proc.Pid
	_ = syscall.Kill(pgid, syscall.SIGTERM)
	if grace > 0 {
		select {
		case <-done:
			return
		case <-time.After(grace):
		}
	}
	killProcessTreeImmediate(proc)
}

// killProcessTreeImmediate sends SIGKILL to the process group, falling back to
// the single process if the group signal fails.
func killProcessTreeImmediate(proc *os.Process) {
	if proc == nil || proc.Pid <= 0 {
		return
	}
	pid := proc.Pid
	if runtime.GOOS == "darwin" {
		_ = syscall.Kill(-pid, syscall.SIGSTOP)
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
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
