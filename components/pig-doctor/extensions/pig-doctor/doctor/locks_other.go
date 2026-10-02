//go:build !unix

package doctor

import (
	"errors"
	"time"
)

var errLockUnsupported = errors.New("lock probing is not supported on this platform")

// lockHeld cannot probe here; callers treat an error as "held" and refuse to change anything.
func lockHeld(path string, now time.Time) (bool, error) { return true, errLockUnsupported }

func acquireLock(path string) (func(), error) { return nil, errLockUnsupported }
