//go:build !windows

package pi_goal_x

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

func lockPid(file string) int {
	data, err := os.ReadFile(file)
	if err != nil {
		return 0
	}
	s := string(data)
	i := strings.Index(s, `"pid":`)
	if i < 0 {
		return 0
	}
	rest := s[i+6:]
	end := strings.IndexAny(rest, ",}")
	if end < 0 {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(rest[:end]))
	return n
}
