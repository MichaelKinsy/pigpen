//go:build windows

package ast

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

// configureProcessGroup is a no-op on Windows; process-tree teardown uses taskkill /F /T.
func configureProcessGroup(cmd *exec.Cmd) {}

func taskkillExePath() string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return filepath.Join(root, "System32", "taskkill.exe")
}

// killProcessTree terminates the child process tree via taskkill /F /T /PID.
func killProcessTree(proc *os.Process, done <-chan struct{}, grace time.Duration) {
	if proc == nil || proc.Pid <= 0 {
		return
	}
	cmd := exec.Command(taskkillExePath(), "/F", "/T", "/PID", strconv.Itoa(proc.Pid))
	if err := cmd.Run(); err != nil {
		_ = proc.Kill()
	}
	if grace > 0 {
		select {
		case <-done:
		case <-time.After(grace):
		}
	}
}
