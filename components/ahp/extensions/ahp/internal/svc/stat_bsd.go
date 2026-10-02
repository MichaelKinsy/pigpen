//go:build unix && !linux

package svc

import "syscall"

func statTimes(st *syscall.Stat_t) (mtime, ctime [2]int64) {
	return [2]int64{int64(st.Mtimespec.Sec), int64(st.Mtimespec.Nsec)}, [2]int64{int64(st.Ctimespec.Sec), int64(st.Ctimespec.Nsec)}
}
