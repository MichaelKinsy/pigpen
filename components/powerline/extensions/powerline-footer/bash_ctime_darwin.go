//go:build darwin

package powerline_footer

import (
	"os"
	"syscall"
)

// fileCtimeNs is the inode change time (Node's ctimeMs), part of a history file's fingerprint.
func fileCtimeNs(fi os.FileInfo) int64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return st.Ctimespec.Nano()
	}
	return fi.ModTime().UnixNano()
}
