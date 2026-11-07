//go:build windows

package posthorse

import (
	"io/fs"
	"syscall"
	"time"
)

// platformStat reads the volume serial and file index through the handle, as libuv does. The
// change time Node reports (NtQueryInformationFile ChangeTime) is not reachable from the standard
// library: the last write time stands in for it, so an identity recorded by Node and one recorded
// here never compare equal on Windows and the digest is recomputed.
func platformStat(path string, info fs.FileInfo) (ctime time.Time, ino, dev float64) {
	ctime = info.ModTime()
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return ctime, 0, 0
	}
	handle, err := syscall.CreateFile(name, 0, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE, nil,
		syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return ctime, 0, 0
	}
	defer syscall.CloseHandle(handle)
	var data syscall.ByHandleFileInformation
	if syscall.GetFileInformationByHandle(handle, &data) != nil {
		return ctime, 0, 0
	}
	return ctime, float64(uint64(data.FileIndexHigh)<<32 | uint64(data.FileIndexLow)), float64(data.VolumeSerialNumber)
}

// accessTime is the atime of a stat result (Stats.atimeMs).
func accessTime(info fs.FileInfo) time.Time {
	if data, ok := info.Sys().(*syscall.Win32FileAttributeData); ok {
		return time.Unix(0, data.LastAccessTime.Nanoseconds())
	}
	return info.ModTime()
}
