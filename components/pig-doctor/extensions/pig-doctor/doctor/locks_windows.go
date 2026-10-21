//go:build windows

package doctor

import (
	"errors"
	"os"
	"syscall"
	"time"
	"unsafe"
)

// Lock probing on Windows is LockFileEx, the byte-range lock PiG takes (one byte at offset 0, exclusive, failing at once).
// Only the standard library is used: kernel32 is reached through syscall.

const (
	lockfileFailImmediately = 0x1
	lockfileExclusiveLock   = 0x2

	errSharingViolation = syscall.Errno(32) // ERROR_SHARING_VIOLATION: another process has the file open without sharing
	errLockViolation    = syscall.Errno(33) // ERROR_LOCK_VIOLATION: another handle holds the byte range
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = kernel32.NewProc("LockFileEx")
	procUnlockFileEx = kernel32.NewProc("UnlockFileEx")
)

func lockFileEx(h syscall.Handle) error {
	var overlapped syscall.Overlapped
	r, _, err := procLockFileEx.Call(uintptr(h), lockfileExclusiveLock|lockfileFailImmediately, 0, 1, 0, uintptr(unsafe.Pointer(&overlapped)))
	if r == 0 {
		return err
	}
	return nil
}

func unlockFileEx(h syscall.Handle) {
	var overlapped syscall.Overlapped
	_, _, _ = procUnlockFileEx.Call(uintptr(h), 0, 1, 0, uintptr(unsafe.Pointer(&overlapped)))
}

var errReparsePoint = errors.New("the lock file is a link; it is not followed")

// openLockFile opens the lock file itself, not what a link there points to (FILE_FLAG_OPEN_REPARSE_POINT, then a refusal when the
// result is a reparse point), sharing it with every other opener so that the lock decides, not the share mode.
func openLockFile(path string, access uint32, create bool) (syscall.Handle, error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	disposition := uint32(syscall.OPEN_EXISTING)
	if create {
		disposition = syscall.OPEN_ALWAYS
	}
	h, err := syscall.CreateFile(name, access, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE, nil,
		disposition, syscall.FILE_ATTRIBUTE_NORMAL|syscall.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return 0, err
	}
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(h, &info); err != nil {
		_ = syscall.CloseHandle(h)
		return 0, err
	}
	if info.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		_ = syscall.CloseHandle(h)
		return 0, errReparsePoint
	}
	return h, nil
}

// lockHeld reports whether another process holds path. A regular lock file is probed with a non-blocking exclusive LockFileEx
// that is released at once; nothing is created or written. A file another process has open without sharing is held. A lock
// directory counts as held while it is fresh.
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
	h, err := openLockFile(path, syscall.GENERIC_READ, false)
	if errors.Is(err, errSharingViolation) || errors.Is(err, errLockViolation) {
		return true, nil
	}
	if err != nil {
		return true, err
	}
	defer syscall.CloseHandle(h)
	err = lockFileEx(h)
	if err == nil {
		unlockFileEx(h)
		return false, nil
	}
	if errors.Is(err, errLockViolation) || errors.Is(err, syscall.ERROR_IO_PENDING) {
		return true, nil
	}
	return true, err
}

// acquireLock takes an exclusive LockFileEx on path, creating it if needed, and returns its release. A lock file that did not
// exist before is removed on release (after the handle is closed: Windows cannot delete an open file that was not opened with
// FILE_SHARE_DELETE by every opener, and the lock goes with the handle).
func acquireLock(path string) (func(), error) {
	_, statErr := os.Lstat(path)
	created := errors.Is(statErr, os.ErrNotExist)
	h, err := openLockFile(path, syscall.GENERIC_READ|syscall.GENERIC_WRITE, true)
	if err != nil {
		return nil, err
	}
	if err := lockFileEx(h); err != nil {
		_ = syscall.CloseHandle(h)
		return nil, err
	}
	return func() {
		unlockFileEx(h)
		_ = syscall.CloseHandle(h)
		if created {
			_ = os.Remove(path) // leave no residue where PiG had no lock file
		}
	}, nil
}
