//go:build !windows

package superpowers_tools

import (
	"os"
	"syscall"
)

// singleLink: the original refuses a file with more than one hard link (stat.nlink !== 1).
func singleLink(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return !ok || st.Nlink == 1
}
