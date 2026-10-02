//go:build unix

package doctor

import (
	"errors"
	"os"
	"syscall"
	"time"
)

// dirLockFresh is how recent a lock directory's modification must be for the
// lock to count as held (lock directories carry no owner we could probe).
const dirLockFresh = time.Minute

// lockHeld reports whether another process holds path. A regular lock file is
// probed with a non-blocking flock that is released at once; nothing is created
// or written. A lock directory counts as held while it is fresh.
func lockHeld(path string, now time.Time) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	if info.IsDir() {
		return now.Sub(info.ModTime()) < dirLockFresh, nil
	}
	if !info.Mode().IsRegular() {
		return true, nil
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return true, err
	}
	defer f.Close()
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		return false, nil
	}
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return true, nil
	}
	return true, err
}

// acquireLock takes an exclusive flock on path, creating it if needed, and returns its release.
// A lock file that did not exist before is removed on release.
func acquireLock(path string) (func(), error) {
	_, statErr := os.Lstat(path)
	created := errors.Is(statErr, os.ErrNotExist)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		if created {
			_ = os.Remove(path) // leave no residue where PiG had no lock file
		}
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
