//go:build !windows

package urls

import (
	"errors"
	"strings"
	"syscall"
)

func isSymlinkLoopErr(err error) bool {
	if err == nil {
		return false
	}
	// filepath.EvalSymlinks returns errors.New("EvalSymlinks: too many links")
	// rather than syscall.ELOOP.
	return errors.Is(err, syscall.ELOOP) || strings.Contains(strings.ToLower(err.Error()), "too many links")
}

func isNotDirErr(err error) bool {
	return errors.Is(err, syscall.ENOTDIR)
}
