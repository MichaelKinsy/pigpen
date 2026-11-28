//go:build windows

package settings

import (
	"os"
	"syscall"
)

// On Windows, Win32FileAttributeData provides CreationTime and LastWriteTime;
// FileIndex requires an extra handle open (GetFileInformationByHandle), so we
// record CreationTime + LastWriteTime cheaply and document the Windows
// file-index omission in UPSTREAM.md (T6).
func platformStatExtras(info os.FileInfo) (dev, ino uint64, ctimeNs int64) {
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok || data == nil {
		return 0, 0, 0
	}
	return 0, 0, data.CreationTime.Nanoseconds()
}
