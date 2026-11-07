//go:build darwin

package posthorse

import (
	"syscall"
	"time"
)

func ctimeOf(st *syscall.Stat_t) time.Time {
	return time.Unix(int64(st.Ctimespec.Sec), int64(st.Ctimespec.Nsec))
}
