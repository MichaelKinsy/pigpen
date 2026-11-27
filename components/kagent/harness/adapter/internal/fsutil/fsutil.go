// Package fsutil holds the small private-file and process helpers the adapter
// needs. kagent keeps equivalent helpers in an internal package that other
// modules cannot import, so these are written separately.
package fsutil

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// TailBuffer retains the last limit bytes written to it and reports every
// write as consumed, so a chatty child never blocks on a full diagnostic pipe.
type TailBuffer struct {
	mu    sync.Mutex
	data  []byte
	limit int
}

// NewTailBuffer returns an empty buffer that keeps the final limit bytes.
func NewTailBuffer(limit int) *TailBuffer { return &TailBuffer{limit: max(limit, 0)} }

// Write implements io.Writer.
func (b *TailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, p...)
	if len(b.data) > b.limit {
		b.data = append(b.data[:0], b.data[len(b.data)-b.limit:]...)
	}
	return len(p), nil
}

// String returns a copy of the retained tail.
func (b *TailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}

// EnsurePrivateDir creates path when needed, rejects symlinks and
// non-directories, and enforces owner-only permissions.
func EnsurePrivateDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create private directory %q: %w", path, err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect private directory %q: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("private path %q is not a directory", path)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("secure private directory %q: %w", path, err)
	}
	return nil
}

// ReplacePrivateFile atomically replaces path with owner-only contents: write a
// temporary file beside it, fsync, rename, then fsync the directory so the
// rename itself is durable.
func ReplacePrivateFile(path string, contents []byte) (returnErr error) {
	directory := filepath.Dir(path)
	if err := EnsurePrivateDir(directory); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary file for %q: %w", path, err)
	}
	temporaryPath := temporary.Name()
	closed, renamed := false, false
	defer func() {
		if !closed {
			returnErr = errors.Join(returnErr, temporary.Close())
		}
		if !renamed {
			if err := os.Remove(temporaryPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
				returnErr = errors.Join(returnErr, fmt.Errorf("remove temporary file %q: %w", temporaryPath, err))
			}
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("secure temporary file for %q: %w", path, err)
	}
	if _, err := temporary.Write(contents); err != nil {
		return fmt.Errorf("write temporary file for %q: %w", path, err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync temporary file for %q: %w", path, err)
	}
	closed = true
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary file for %q: %w", path, err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace private file %q: %w", path, err)
	}
	renamed = true
	return SyncDir(directory)
}

// SyncDir fsyncs a directory so a rename or create inside it is durable.
func SyncDir(directory string) error {
	handle, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("open directory %q for sync: %w", directory, err)
	}
	syncErr := handle.Sync()
	closeErr := handle.Close()
	if syncErr != nil {
		return fmt.Errorf("sync directory %q: %w", directory, syncErr)
	}
	return closeErr
}
