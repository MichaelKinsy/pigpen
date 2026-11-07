//go:build linux || android || openbsd || dragonfly

package posthorse

import (
	"io/fs"
	"syscall"
	"time"
)

func platformStat(_ string, info fs.FileInfo) (ctime time.Time, ino, dev float64) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return info.ModTime(), 0, 0
	}
	return time.Unix(int64(st.Ctim.Sec), int64(st.Ctim.Nsec)), float64(st.Ino), float64(st.Dev)
}

// accessTime is the atime of a stat result (Stats.atimeMs).
func accessTime(info fs.FileInfo) time.Time {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return time.Unix(int64(st.Atim.Sec), int64(st.Atim.Nsec))
	}
	return info.ModTime()
}
