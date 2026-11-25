//go:build darwin

package settings

import (
	"os"
	"syscall"
)

func platformStatExtras(info os.FileInfo) (dev, ino uint64, ctimeNs int64) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st == nil {
		return 0, 0, 0
	}
	return uint64(st.Dev), uint64(st.Ino), st.Ctimespec.Nano()
}
