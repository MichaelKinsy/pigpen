//go:build !windows

package changeset

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
)

// openNoFollow opens a regular file without following a final symlink or blocking on a FIFO.
func openNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
}

func isGoneOrLink(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ELOOP) || errors.Is(err, syscall.ENXIO)
}
