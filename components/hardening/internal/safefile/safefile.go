// Package safefile reads a small file the way a credential or a profile must be read: one open, a check on the
// opened descriptor, a bounded read. It never reports the path.
package safefile

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"syscall"
)

var (
	// ErrNotExist means the path (or a directory on it) does not exist.
	ErrNotExist = errors.New("file does not exist")
	// ErrNotRegular means the opened descriptor is not a regular file (a FIFO, a device, a directory, a socket).
	ErrNotRegular = errors.New("file is not a regular file")
	// ErrTooLarge means the file is larger than the cap.
	ErrTooLarge = errors.New("file is larger than the cap")
	// ErrUnreadable means any other failure to open or read the file.
	ErrUnreadable = errors.New("file is unreadable")
)

// Opener opens a path. It exists so a test can count opens.
type Opener func(name string, flag int, perm fs.FileMode) (*os.File, error)

// Read opens path once, checks the descriptor and reads at most max bytes.
func Read(path string, max int64) ([]byte, error) { return ReadWith(os.OpenFile, path, max) }

// ReadWith is Read with an injected opener.
//
// The path is opened with O_NONBLOCK, so a FIFO cannot block the open or the read. Symlinks are followed: a shared
// directory is reached through them. The checks (regular file, size) are on the descriptor, never on the path, so
// there is no stat-then-open window.
func ReadWith(open Opener, path string, max int64) ([]byte, error) {
	f, err := open(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			return nil, ErrNotExist
		}
		return nil, ErrUnreadable
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, ErrUnreadable
	}
	if !info.Mode().IsRegular() {
		return nil, ErrNotRegular
	}
	if info.Size() > max {
		return nil, ErrTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, ErrUnreadable
	}
	if int64(len(data)) > max {
		return nil, ErrTooLarge
	}
	return data, nil
}
