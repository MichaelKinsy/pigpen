//go:build windows

package settings

import (
	"fmt"
	"os"
	"syscall"
	"time"
	"unsafe"
)

var (
	kernel32DLL      = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = kernel32DLL.NewProc("LockFileEx")
	procUnlockFileEx = kernel32DLL.NewProc("UnlockFileEx")
)

const (
	winLockfileFailImmediately = 0x00000001
	winLockfileExclusiveLock   = 0x00000002
	winErrorLockViolation      = syscall.Errno(33)
)

// acquireAdvisoryLock acquires a Windows LockFileEx exclusive byte-range lock
// on lockPath bounded by timeout, refusing symlinks at the final component and
// keeping the lock file permanent (T1).
func acquireAdvisoryLock(lockPath string, timeout time.Duration) (func(), error) {
	if info, err := os.Lstat(lockPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("open config lock %s: refusing to follow symlink", lockPath)
		}
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open config lock %s: %w", lockPath, err)
	}

	h := syscall.Handle(f.Fd())
	var ov syscall.Overlapped
	deadline := time.Now().Add(timeout)
	for {
		r1, _, callErr := procLockFileEx.Call(
			uintptr(h),
			uintptr(winLockfileExclusiveLock|winLockfileFailImmediately),
			0,
			1,
			0,
			uintptr(unsafe.Pointer(&ov)),
		)
		if r1 != 0 {
			return func() {
				var uov syscall.Overlapped
				_, _, _ = procUnlockFileEx.Call(
					uintptr(h),
					0,
					1,
					0,
					uintptr(unsafe.Pointer(&uov)),
				)
				_ = f.Close()
			}, nil
		}
		if callErr != winErrorLockViolation {
			_ = f.Close()
			return nil, fmt.Errorf("lock config file %s: %w", lockPath, callErr)
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("timed out waiting for config lock %s", lockPath)
		}
		time.Sleep(lockPollInterval)
	}
}
