//go:build windows

package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"
)

const (
	fileLockTimeout      = 10 * time.Second
	fileLockPollInterval = 10 * time.Millisecond
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

// acquireFileLock acquires a Windows LockFileEx exclusive byte-range lock
// on lockPath bounded by timeout, refusing symlinks at the final component and
// keeping the lock file permanent.
func acquireFileLock(lockPath string, timeout time.Duration) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, fmt.Errorf("create lock dir for %s: %w", lockPath, err)
	}
	if info, err := os.Lstat(lockPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("open memory lock %s: refusing to follow symlink", lockPath)
		}
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open memory lock %s: %w", lockPath, err)
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
			return nil, fmt.Errorf("lock memory file %s: %w", lockPath, callErr)
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("timed out waiting for memory lock %s", lockPath)
		}
		time.Sleep(fileLockPollInterval)
	}
}
