//go:build !windows

package warden

import (
	"os"
	"syscall"
)

// statDevIno is the device and inode of a file, the identity Node's stat reports as dev and ino.
func statDevIno(fi os.FileInfo) (uint64, uint64) {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Dev), uint64(st.Ino)
	}
	return 0, 0
}
