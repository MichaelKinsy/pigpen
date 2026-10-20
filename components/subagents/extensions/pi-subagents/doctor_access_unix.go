//go:build !windows

package pi_subagents

import (
	"fmt"
	"syscall"
)

// accessReadWrite is fs.accessSync(path, R_OK | W_OK).
func accessReadWrite(path string) error {
	if err := syscall.Access(path, 0x4|0x2); err != nil {
		return fmt.Errorf("EACCES: permission denied, access '%s'", path)
	}
	return nil
}
