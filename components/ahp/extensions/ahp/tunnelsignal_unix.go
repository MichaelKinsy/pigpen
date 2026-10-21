//go:build !windows

package ahp

import (
	"os"
	"syscall"
)

var stopSignal os.Signal = syscall.SIGTERM
