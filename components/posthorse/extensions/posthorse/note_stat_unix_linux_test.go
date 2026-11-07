//go:build linux

package posthorse

import (
	"syscall"
	"time"
)

func ctimeOf(st *syscall.Stat_t) time.Time { return time.Unix(int64(st.Ctim.Sec), int64(st.Ctim.Nsec)) }
