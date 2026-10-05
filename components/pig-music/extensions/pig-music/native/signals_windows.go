//go:build windows

package native

import "os"

var shutdownSignals = []os.Signal{os.Interrupt}
