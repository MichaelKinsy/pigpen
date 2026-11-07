//go:build !linux && !android && !openbsd && !dragonfly && !darwin && !freebsd && !netbsd && !windows

package posthorse

import (
	"io/fs"
	"time"
)

// Platforms without a stat record the standard library exposes: the modification time stands in
// for the change time and the inode and device are unknown, so identities never match across
// processes and the digest is recomputed.
func platformStat(_ string, info fs.FileInfo) (ctime time.Time, ino, dev float64) {
	return info.ModTime(), 0, 0
}

func accessTime(info fs.FileInfo) time.Time { return info.ModTime() }
