//go:build darwin

package warden

import (
	"io/fs"
	"syscall"
)

// birthtimeMs is the file's creation time in milliseconds (Stat_t.Birthtimespec).
func birthtimeMs(_ string, info fs.FileInfo) float64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return float64(st.Birthtimespec.Sec)*1000 + float64(st.Birthtimespec.Nsec)/1e6
	}
	return 0
}

func fileDevIno(info fs.FileInfo) (uint64, uint64) {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Dev), uint64(st.Ino)
	}
	return 0, 0
}
