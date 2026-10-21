//go:build darwin || freebsd || netbsd || openbsd

package pi_background_tasks

import "syscall"

func statCtimeNs(st *syscall.Stat_t) int64 {
	return int64(st.Ctimespec.Sec)*1e9 + int64(st.Ctimespec.Nsec)
}
