//go:build windows

package tunnel

import "syscall"

// Windows reports no signals for an exited child.
func signalName(s syscall.Signal) string { return s.String() }
