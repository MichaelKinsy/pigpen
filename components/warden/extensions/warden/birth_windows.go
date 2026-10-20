//go:build windows

package warden

import (
	"io/fs"
	"syscall"
)

// birthtimeMs is the file's creation time in milliseconds.
func birthtimeMs(_ string, info fs.FileInfo) float64 {
	if d, ok := info.Sys().(*syscall.Win32FileAttributeData); ok {
		return float64(d.CreationTime.Nanoseconds()) / 1e6
	}
	return 0
}

// fileDevIno has no stable pair without opening a handle; the birth time still separates a replaced path.
func fileDevIno(fs.FileInfo) (uint64, uint64) { return 0, 0 }
