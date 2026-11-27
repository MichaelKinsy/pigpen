//go:build unix

package fsutil

import (
	"errors"
	"syscall"
)

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func groupGone(err error) bool { return errors.Is(err, syscall.ESRCH) }
