//go:build linux

package pi_background_tasks

import "syscall"

func statCtimeNs(st *syscall.Stat_t) int64 { return int64(st.Ctim.Sec)*1e9 + int64(st.Ctim.Nsec) }
