//go:build unix

package pi_goal_x

import (
	"syscall"
	"time"
)

const osSIGTERM = syscall.SIGTERM

func processGone(pid int) bool {
	for range 200 {
		if syscall.Kill(pid, 0) != nil {
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	return false
}
