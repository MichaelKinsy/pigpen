//go:build windows

package shell

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// defaultShellConfig resolves bash on Windows: Git Bash in standard ProgramFiles
// locations, then bash.exe on PATH. Mirrors PiG's internal/shellconfig/default_windows.go.
func defaultShellConfig() (shellPath string, shellArgs []string, err error) {
	var searched []string
	for _, base := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")} {
		if base == "" {
			continue
		}
		p := base + `\Git\bin\bash.exe`
		searched = append(searched, "  "+p)
		if _, statErr := os.Stat(p); statErr == nil {
			return p, []string{"-c"}, nil
		}
	}
	if p, lookErr := exec.LookPath("bash.exe"); lookErr == nil && p != "" {
		return p, []string{"-c"}, nil
	}
	return "", nil, fmt.Errorf(
		"No bash shell found. Options:\n"+
			"  1. Install Git for Windows: https://git-scm.com/download/win\n"+
			"  2. Add your bash to PATH (Cygwin, MSYS2, etc.)\n\n"+
			"Searched Git Bash in:\n%s", strings.Join(searched, "\n"))
}

// configureProcessGroup is a no-op on Windows; process-tree cleanup uses taskkill /F /T.
func configureProcessGroup(cmd *exec.Cmd) {}

func taskkillExePath() string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return filepath.Join(root, "System32", "taskkill.exe")
}

// killProcessTreeGraceful terminates the process tree via System32\taskkill.exe /F /T /PID.
func killProcessTreeGraceful(proc *os.Process, done <-chan struct{}, grace time.Duration) {
	killProcessTreeImmediate(proc)
	if grace > 0 {
		select {
		case <-done:
		case <-time.After(grace):
		}
	}
}

// killProcessTreeImmediate runs System32\taskkill.exe /F /T /PID <pid> and falls
// back to proc.Kill() if taskkill is unavailable.
func killProcessTreeImmediate(proc *os.Process) {
	if proc == nil || proc.Pid <= 0 {
		return
	}
	cmd := exec.Command(taskkillExePath(), "/F", "/T", "/PID", strconv.Itoa(proc.Pid))
	if err := cmd.Run(); err != nil {
		_ = proc.Kill()
	}
}

// processExitCode returns the process exit code on Windows.
func processExitCode(state *os.ProcessState) int {
	if state == nil {
		return -1
	}
	return state.ExitCode()
}

// isProcessAlive reports whether a process with pid exists on Windows.
func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	return err == nil && p != nil
}

// forceKillPID terminates a single pid on Windows.
func forceKillPID(pid int) {
	if pid <= 0 {
		return
	}
	if p, err := os.FindProcess(pid); err == nil && p != nil {
		_ = p.Kill()
	}
}
