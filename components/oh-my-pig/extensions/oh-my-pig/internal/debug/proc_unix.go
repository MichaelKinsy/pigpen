//go:build !windows

package debug

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// configureProcessGroup starts the child as the leader of a new session so it has
// no controlling terminal and killing -pid reaps the entire descendant tree.
// upstream: packages/coding-agent/src/dap/client.ts:115-128
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// killProcessTree terminates the child process group with SIGKILL, falling back
// to proc.Kill() (which Go guards against already-reaped PIDs).
func killProcessTree(proc *os.Process) {
	if proc == nil || proc.Pid <= 0 {
		return
	}
	pid := proc.Pid
	if runtime.GOOS == "darwin" {
		_ = syscall.Kill(-pid, syscall.SIGSTOP)
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		_ = proc.Kill()
	}
}

// isProcessAlive reports whether a process with pid still exists.
func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// processImageName returns the executable name of a running process, or "" when unreadable.
// upstream: packages/coding-agent/src/dap/config.ts:393-408
func processImageName(ctx context.Context, pid int) string {
	if pid <= 0 {
		return ""
	}
	if runtime.GOOS == "linux" {
		data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(data))
	}
	probeCtx, cancel := context.WithTimeout(ctx, attachProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, "ps", "-o", "comm=", "-p", strconv.Itoa(pid))
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return ""
	}
	image := strings.TrimSpace(out.String())
	if image == "" {
		return ""
	}
	return filepath.Base(image)
}

func useUnixSocketMode() bool {
	return runtime.GOOS == "linux"
}

func isUnixSocketReady(socketPath string) (bool, error) {
	info, err := os.Stat(socketPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return info.Mode()&os.ModeSocket != 0, nil
}

func rejectPromptCommand() string {
	if p, err := exec.LookPath("false"); err == nil && p != "" {
		return p
	}
	return "false"
}
