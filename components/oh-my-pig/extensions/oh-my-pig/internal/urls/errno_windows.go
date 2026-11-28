//go:build windows

package urls

import (
	"errors"
	"strings"
	"syscall"
)

func isSymlinkLoopErr(err error) bool {
	// ERROR_CANT_RESOLVE_FILENAME (1921) on Windows for symlink loops.
	return errors.Is(err, syscall.Errno(1921)) || strings.Contains(strings.ToLower(err.Error()), "too many links")
}

func isNotDirErr(err error) bool {
	// ERROR_PATH_NOT_FOUND (3) or ERROR_DIRECTORY (267).
	return errors.Is(err, syscall.Errno(267)) || strings.Contains(strings.ToLower(err.Error()), "not a directory")
}
