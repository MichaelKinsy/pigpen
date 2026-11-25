//go:build unix

package settings

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

// acquireAdvisoryLock opens lockPath without following a symlink at the final
// component (O_NOFOLLOW, mode 0600) and acquires an exclusive flock(LOCK_EX)
// bounded by timeout. Releasing unlocks and closes the descriptor without
// removing the permanent lock file (T1).
func acquireAdvisoryLock(lockPath string, timeout time.Duration) (func(), error) {
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open config lock %s: %w", lockPath, err)
	}
	// Ensure mode is 0600 on freshly created files (best-effort if pre-existing).
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
			return nil, fmt.Errorf("lock config file %s: %w", lockPath, err)
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("timed out waiting for config lock %s", lockPath)
		}
		time.Sleep(lockPollInterval)
	}
}
