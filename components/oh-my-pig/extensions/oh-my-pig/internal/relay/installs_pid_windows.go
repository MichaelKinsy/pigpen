//go:build windows

package relay

import "os"

func pidAlive(pid int) bool {
	_, err := os.FindProcess(pid)
	return err == nil
}
