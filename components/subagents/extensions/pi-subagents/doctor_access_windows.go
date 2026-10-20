//go:build windows

package pi_subagents

import (
	"fmt"
	"os"
)

// accessReadWrite is fs.accessSync(path, R_OK | W_OK): Windows has no access(2), a read-only attribute is what denies a write.
func accessReadWrite(path string) error {
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm()&0o200 == 0 {
		return fmt.Errorf("EPERM: operation not permitted, access '%s'", path)
	}
	return nil
}
