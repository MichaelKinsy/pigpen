//go:build !windows

package pi_background_tasks

import "syscall"

var errGone error = syscall.ESRCH
