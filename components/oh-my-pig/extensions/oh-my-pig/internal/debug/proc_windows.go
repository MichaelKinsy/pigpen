//go:build windows

package debug

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

// configureProcessGroup is a no-op on Windows; teardown uses taskkill /F /T.
func configureProcessGroup(cmd *exec.Cmd) {}

func taskkillExePath() string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return filepath.Join(root, "System32", "taskkill.exe")
}

// killProcessTree terminates the child process tree via taskkill /F /T /PID.
func killProcessTree(proc *os.Process) {
	if proc == nil || proc.Pid <= 0 {
		return
	}
	cmd := exec.Command(taskkillExePath(), "/F", "/T", "/PID", strconv.Itoa(proc.Pid))
	if err := cmd.Run(); err != nil {
		_ = proc.Kill()
	}
}

// isProcessAlive reports whether a process with pid exists on Windows.
func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	return err == nil && p != nil
}

// processImageName returns empty on Windows (matching upstream config.ts:398).
func processImageName(_ context.Context, _ int) string {
	return ""
}

func useUnixSocketMode() bool {
	return false
}

func isUnixSocketReady(_ string) (bool, error) {
	return false, nil
}

func rejectPromptCommand() string {
	return "false"
}
