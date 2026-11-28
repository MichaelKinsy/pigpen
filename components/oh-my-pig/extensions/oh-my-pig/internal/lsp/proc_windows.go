//go:build windows

package lsp

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	winProcessQueryLimitedInfo = 0x1000
	winStillActive             = 259
)

func configureProcessGroup(cmd *exec.Cmd) {}

func configureCmdCancel(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			killProcessTree(cmd.Process, nil, 0)
		}
		return nil
	}
	cmd.WaitDelay = 500 * time.Millisecond
}

func taskkillExePath() string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return filepath.Join(root, "System32", "taskkill.exe")
}

func killProcessTree(proc *os.Process, done <-chan struct{}, grace time.Duration) {
	if proc == nil || proc.Pid <= 0 {
		return
	}
	cmd := exec.Command(taskkillExePath(), "/F", "/T", "/PID", strconv.Itoa(proc.Pid))
	if err := cmd.Run(); err != nil {
		_ = proc.Kill()
	}
	if grace > 0 && done != nil {
		select {
		case <-done:
		case <-time.After(grace):
		}
	}
}

// isProcessAlive checks whether pid is still running on Windows via OpenProcess
// and GetExitCodeProcess (STILL_ACTIVE == 259).
func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := syscall.OpenProcess(winProcessQueryLimitedInfo, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == winStillActive
}

func specialFileKind(info os.FileInfo) string {
	mode := info.Mode()
	switch {
	case mode&os.ModeNamedPipe != 0:
		return "FIFO"
	case mode&os.ModeSocket != 0:
		return "socket"
	case mode&os.ModeCharDevice != 0:
		return "character device"
	case mode&os.ModeDevice != 0:
		return "block device"
	default:
		return ""
	}
}

func sameInode(a, b os.FileInfo) bool {
	return os.SameFile(a, b)
}

func formatRenameStatError(err error) string {
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) {
		return "unknown filesystem error"
	}
	code := "EIO"
	switch {
	case errors.Is(pathErr.Err, os.ErrPermission):
		code = "EACCES"
	case errors.Is(pathErr.Err, os.ErrNotExist):
		code = "ENOENT"
	case errors.Is(pathErr.Err, os.ErrExist):
		code = "EEXIST"
	}
	op := strings.ReplaceAll(pathErr.Op, "\t", " ")
	if op != "" {
		return code + " during " + op
	}
	return code
}
