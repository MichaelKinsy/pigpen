//go:build windows

package pi_background_tasks

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// Process helpers for Windows. The original does not detach on Windows and kills the shell's pid with process.kill; a Go
// port that only did that would leave every grandchild running, so the kill walks the tree with `taskkill /T`, as Pi's own
// bash tool does.

// hideWindow keeps a probe from flashing a console window.
func hideWindow(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
}

// detachProcess keeps the child attached, as the original's `detached: process.platform !== "win32"` does, but hides its window.
func detachProcess(cmd *exec.Cmd) { hideWindow(cmd) }

func exitSignalName(ee *exec.ExitError) string { return "" }

func errnoName(e syscall.Errno) string { return e.Error() }

// processAlive reports whether pid names a live process.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = p.Release()
	return true
}

// killProcess ends the process tree rooted at pid: `taskkill /PID n /T /F`. Windows has no graceful SIGTERM for a console
// process tree, so the TERM and the KILL of the original are both a forced tree kill; group is ignored.
func killProcess(pid int, sig string, group bool) error {
	cmd := exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F")
	hideWindow(cmd)
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
}

func isNoSuchProcess(err error) bool { return false }

func errnoCodeAndText(e syscall.Errno) (string, string) {
	switch e {
	case syscall.ERROR_FILE_NOT_FOUND, syscall.ERROR_PATH_NOT_FOUND:
		return "ENOENT", "no such file or directory"
	case syscall.ERROR_ACCESS_DENIED:
		return "EPERM", "operation not permitted"
	}
	return "", ""
}

func fileIdentity(info os.FileInfo) (ino uint64, ctimeNs int64) { return 0, 0 }

func ownedByThisUser(info os.FileInfo) bool { return true }
