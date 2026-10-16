//go:build windows

package mpv

import (
	"context"
	"os/exec"
	"syscall"
)

// Windows is out of scope for pig-music: unix sockets and Setsid assume a
// Unix-like system. These stubs keep the package compiling.
func detach(cmd *exec.Cmd) {}

func kill(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func lockFile(ctx context.Context, path string) (func(), error) { return func() {}, nil }

// processAlive reports whether a process with the pid runs: one whose handle opens and that has not exited (one owned by
// another user, whose handle cannot be opened, counts as running).
func processAlive(pid int) bool {
	const queryLimitedInformation, stillActive = 0x1000, 259 // PROCESS_QUERY_LIMITED_INFORMATION, STILL_ACTIVE
	if pid <= 0 {
		return false
	}
	h, err := syscall.OpenProcess(queryLimitedInformation, false, uint32(pid))
	if err != nil {
		return err == syscall.ERROR_ACCESS_DENIED
	}
	defer syscall.CloseHandle(h)
	var code uint32
	return syscall.GetExitCodeProcess(h, &code) == nil && code == stillActive
}
