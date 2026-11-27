//go:build windows

package fsutil

import "golang.org/x/sys/windows"

// alive reports whether pid is a running process.
func alive(pid int) bool {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	event, err := windows.WaitForSingleObject(handle, 0)
	return err == nil && event == uint32(windows.WAIT_TIMEOUT)
}

// groupGone is false: KillProcessGroup never reports a group that is gone.
func groupGone(error) bool { return false }
