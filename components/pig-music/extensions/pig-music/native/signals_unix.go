//go:build !windows

package native

import (
	"os"
	"syscall"
)

var shutdownSignals = []os.Signal{syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP}
