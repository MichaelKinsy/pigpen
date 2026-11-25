package settings

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// In-process per-path mutex + OS advisory file lock on a permanent lock file (T1).
//
// In-process concurrency is serialized by one sync.Mutex per resolved file path.
// Cross-process concurrency uses an OS advisory lock (flock LOCK_EX on Unix,
// LockFileEx on Windows) on a permanent sibling file (`<writePath>.lock`),
// released by closing the descriptor (never removed, no PID/age guessing).

const (
	lockAcquireLimit = 10 * time.Second
	lockPollInterval = 15 * time.Millisecond
)

var (
	pathLocksMu sync.Mutex
	pathLocks   = map[string]*sync.Mutex{}
)

func mutexForPath(resolvedPath string) *sync.Mutex {
	clean := filepath.Clean(resolvedPath)
	pathLocksMu.Lock()
	defer pathLocksMu.Unlock()
	m, ok := pathLocks[clean]
	if !ok {
		m = &sync.Mutex{}
		pathLocks[clean] = m
	}
	return m
}

// withConfigFileLock acquires both the per-path in-process mutex and the OS
// advisory lock on `<writePath>.lock`, then runs fn.
func withConfigFileLock(writePath string, fn func() error) error {
	mu := mutexForPath(writePath)
	mu.Lock()
	defer mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(writePath), 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	lockPath := writePath + ".lock"
	release, err := acquireAdvisoryLock(lockPath, lockAcquireLimit)
	if err != nil {
		return err
	}
	defer release()
	return fn()
}
