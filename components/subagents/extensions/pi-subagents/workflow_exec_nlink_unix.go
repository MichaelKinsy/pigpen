//go:build unix

package pi_subagents

import (
	"os"
	"syscall"
)

// hardLinkCount is stat.nlink.
func hardLinkCount(st os.FileInfo) uint64 {
	if s, ok := st.Sys().(*syscall.Stat_t); ok {
		return uint64(s.Nlink)
	}
	return 1
}
