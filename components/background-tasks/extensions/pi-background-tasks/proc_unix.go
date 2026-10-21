//go:build !windows

package pi_background_tasks

import (
	"os"
	"os/exec"
	"syscall"
)

// Process-group and process helpers for POSIX hosts (Linux and macOS).

// hideWindow is a no-op off Windows.
func hideWindow(cmd *exec.Cmd) {}

// detachProcess starts the child in its own session, so it is its own process group leader: a signal to -pid reaches the
// task and everything it started, and the death of Pi's terminal does not reach it. Node's `detached: true` is setsid(2).
func detachProcess(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
}

// signalName maps a signal to Node's name for it.
func signalName(s syscall.Signal) string {
	switch s {
	case syscall.SIGHUP:
		return "SIGHUP"
	case syscall.SIGINT:
		return "SIGINT"
	case syscall.SIGQUIT:
		return "SIGQUIT"
	case syscall.SIGKILL:
		return "SIGKILL"
	case syscall.SIGPIPE:
		return "SIGPIPE"
	case syscall.SIGALRM:
		return "SIGALRM"
	case syscall.SIGTERM:
		return "SIGTERM"
	case syscall.SIGABRT:
		return "SIGABRT"
	case syscall.SIGSEGV:
		return "SIGSEGV"
	case syscall.SIGUSR1:
		return "SIGUSR1"
	case syscall.SIGUSR2:
		return "SIGUSR2"
	}
	return s.String()
}

// exitSignalName is the signal that ended the process, or "" for a normal exit.
func exitSignalName(ee *exec.ExitError) string {
	if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return signalName(ws.Signal())
	}
	return ""
}

// errnoName is the Node error code of an errno.
func errnoName(e syscall.Errno) string {
	switch e {
	case syscall.ENOENT:
		return "ENOENT"
	case syscall.EACCES:
		return "EACCES"
	case syscall.EPERM:
		return "EPERM"
	case syscall.EAGAIN:
		return "EAGAIN"
	case syscall.ENOEXEC:
		return "ENOEXEC"
	case syscall.ENOMEM:
		return "ENOMEM"
	case syscall.ENOTDIR:
		return "ENOTDIR"
	case syscall.E2BIG:
		return "E2BIG"
	case syscall.EMFILE:
		return "EMFILE"
	case syscall.ENFILE:
		return "ENFILE"
	}
	return e.Error()
}

// processAlive is `process.kill(pid, 0)`: true when the kernel reports the pid alive, or EPERM (alive but foreign).
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// killProcess sends sig to one pid; group kills the process group of the task (-pid), as the original does on POSIX.
// The error carries ESRCH when the target is gone.
func killProcess(pid int, sig string, group bool) error {
	var s syscall.Signal
	switch sig {
	case "SIGKILL":
		s = syscall.SIGKILL
	default:
		s = syscall.SIGTERM
	}
	target := pid
	if group {
		target = -pid
	}
	return syscall.Kill(target, s)
}

// isNoSuchProcess reports whether err is ESRCH.
func isNoSuchProcess(err error) bool { return err == syscall.ESRCH }

// errnoCodeAndText is libuv's code and description for an errno, as Node prints them.
func errnoCodeAndText(e syscall.Errno) (string, string) {
	switch e {
	case syscall.ENOENT:
		return "ENOENT", "no such file or directory"
	case syscall.EACCES:
		return "EACCES", "permission denied"
	case syscall.EPERM:
		return "EPERM", "operation not permitted"
	case syscall.EISDIR:
		return "EISDIR", "illegal operation on a directory"
	case syscall.ENOTDIR:
		return "ENOTDIR", "not a directory"
	case syscall.ENOSPC:
		return "ENOSPC", "no space left on device"
	case syscall.EROFS:
		return "EROFS", "read-only file system"
	case syscall.EEXIST:
		return "EEXIST", "file already exists"
	case syscall.EMFILE:
		return "EMFILE", "too many open files"
	case syscall.EIO:
		return "EIO", "i/o error"
	}
	return "", ""
}

// fileIdentity is the inode and the change time of a file, which a cached log tail is keyed on.
func fileIdentity(info os.FileInfo) (ino uint64, ctimeNs int64) {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Ino), statCtimeNs(st)
	}
	return 0, 0
}

// ownedByThisUser reports whether the file belongs to the current user.
func ownedByThisUser(info os.FileInfo) bool {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return int(st.Uid) == os.Getuid()
	}
	return true
}
