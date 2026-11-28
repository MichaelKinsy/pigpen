//go:build unix

package memory

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const (
	fileLockTimeout      = 10 * time.Second
	fileLockPollInterval = 10 * time.Millisecond
)

// acquireFileLock opens lockPath without following a symlink at the final
// component (O_NOFOLLOW, mode 0600) and acquires an exclusive flock(LOCK_EX)
// bounded by timeout. Releasing unlocks and closes the descriptor without
// removing the permanent lock file.
func acquireFileLock(lockPath string, timeout time.Duration) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, fmt.Errorf("create lock dir for %s: %w", lockPath, err)
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open memory lock %s: %w", lockPath, err)
	}
	_ = f.Chmod(0o600)

	fd := int(f.Fd())
	deadline := time.Now().Add(timeout)
	for {
		err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(fd, syscall.LOCK_UN)
				_ = f.Close()
			}, nil
		}
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			_ = f.Close()
			return nil, fmt.Errorf("lock memory file %s: %w", lockPath, err)
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("timed out waiting for memory lock %s", lockPath)
		}
		time.Sleep(fileLockPollInterval)
	}
}
