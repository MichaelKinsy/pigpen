//go:build !windows

package lsp

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// configureProcessGroup starts the child in its own process group so teardown
// kills the server/checker and any descendant subprocesses it spawned.
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// configureCmdCancel configures cmd to run in its own process group and kill
// the whole group on context cancellation, with a short WaitDelay so lingering
// grandchildren holding pipes cannot block cmd.Run indefinitely.
func configureCmdCancel(cmd *exec.Cmd) {
	configureProcessGroup(cmd)
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			killProcessTree(cmd.Process, nil, 0)
		}
		return nil
	}
	cmd.WaitDelay = 500 * time.Millisecond
}

// killProcessTree terminates the process group, escalating from SIGTERM to
// SIGKILL if it does not exit within grace.
func killProcessTree(proc *os.Process, done <-chan struct{}, grace time.Duration) {
	if proc == nil || proc.Pid <= 0 {
		return
	}
	pid := proc.Pid
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	if grace > 0 && done != nil {
		select {
		case <-done:
			return
		case <-time.After(grace):
		}
	}
	if runtime.GOOS == "darwin" {
		_ = syscall.Kill(-pid, syscall.SIGSTOP)
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
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

// formatRenameStatError returns a path-free filesystem error code + syscall
// matching packages/coding-agent/src/lsp/tool.ts:formatRenameStatError.
func formatRenameStatError(err error) string {
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) {
		return "unknown filesystem error"
	}
	code := errnoCodeName(pathErr.Err)
	if code == "" {
		return "unknown filesystem error"
	}
	op := strings.ReplaceAll(pathErr.Op, "\t", " ")
	if op != "" {
		return code + " during " + op
	}
	return code
}

func errnoCodeName(err error) string {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return ""
	}
	switch errno {
	case syscall.EACCES:
		return "EACCES"
	case syscall.EPERM:
		return "EPERM"
	case syscall.ENOENT:
		return "ENOENT"
	case syscall.ENOTDIR:
		return "ENOTDIR"
	case syscall.EISDIR:
		return "EISDIR"
	case syscall.ELOOP:
		return "ELOOP"
	case syscall.EIO:
		return "EIO"
	case syscall.EEXIST:
		return "EEXIST"
	case syscall.EINVAL:
		return "EINVAL"
	case syscall.ENAMETOOLONG:
		return "ENAMETOOLONG"
	case syscall.EXDEV:
		return "EXDEV"
	case syscall.EROFS:
		return "EROFS"
	default:
		return strings.ToUpper(strings.ReplaceAll(errno.Error(), " ", "_"))
	}
}
