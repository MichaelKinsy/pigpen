//go:build windows

package relay

import (
	"io/fs"
	"os"
	"syscall"
)

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	const processQueryLimitedInformation = 0x1000
	const stillActive = 259
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return err == syscall.ERROR_ACCESS_DENIED
	}
	defer func() { _ = syscall.CloseHandle(h) }()
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return true
	}
	return code == stillActive
}

// On Windows, POSIX permission bits do not encode NTFS ACLs; endpoint files live
// under the user's profile directory (%USERPROFILE%\.pig\agent\browser-relay\endpoints),
// which inherits the user-private NTFS DACL from the profile root.
func endpointDirSafe(dir string) bool {
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}

func checkEndpointFileSafe(_ fs.FileInfo) error {
	return nil
}
