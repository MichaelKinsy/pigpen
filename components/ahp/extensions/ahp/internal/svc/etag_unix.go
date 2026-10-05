//go:build unix

package svc

import (
	"fmt"
	"os"
	"strconv"
	"syscall"
	"time"
)

func msOf(sec, nsec int64) string {
	return strconv.FormatFloat(float64(sec)*1e3+float64(nsec)/1e6, 'f', -1, 64)
}

// etagOf is an opaque change token: device, inode, size and both timestamps, so a write, a
// replacement or a metadata change all invalidate it.
func etagOf(info os.FileInfo) string {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		mt, ct := statTimes(st)
		return fmt.Sprintf("%d-%d-%d-%s-%s", st.Dev, st.Ino, st.Size, msOf(mt[0], mt[1]), msOf(ct[0], ct[1]))
	}
	return fmt.Sprintf("0-0-%d-%d", info.Size(), info.ModTime().UnixNano())
}

// ctimeOf is the inode change time (the birth time is not portable).
func ctimeOf(info os.FileInfo) time.Time {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		_, ct := statTimes(st)
		return time.Unix(ct[0], ct[1])
	}
	return info.ModTime()
}
