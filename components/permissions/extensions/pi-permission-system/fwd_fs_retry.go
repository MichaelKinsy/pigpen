package pi_permission_system

import (
	"errors"
	"io/fs"
	"runtime"
	"syscall"
	"time"
)

// Port of src/authority/transient-fs-retry.ts: survive a file lock that is about to clear.
//
// On Windows a rename or mkdir fails with EPERM/EBUSY/EACCES while an antivirus scanner or indexer holds a transient handle on a
// path; the forwarded-permission writes that ride on those calls would be lost (#914). A few attempts a few milliseconds apart is
// enough for the common case.

// ErrnoCoder is implemented by an error that carries a Node-style errno code ("EPERM", "ENOENT", ...). Errors from the os package
// are classified by FsErrnoCode without implementing it.
type ErrnoCoder interface{ ErrnoCode() string }

// FsErrnoCode is the Node errno code an error carries, or "" when it carries none: an explicit ErrnoCode, else the code libuv
// gives the underlying errno (Windows native error numbers are mapped as libuv maps them).
func FsErrnoCode(err error) string {
	if err == nil {
		return ""
	}
	var coder ErrnoCoder
	if errors.As(err, &coder) {
		return coder.ErrnoCode()
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		if runtime.GOOS == "windows" {
			// libuv uv_translate_sys_error: ERROR_ACCESS_DENIED -> EPERM; sharing and lock violations -> EBUSY;
			// ERROR_DIR_NOT_EMPTY -> ENOTEMPTY; the file/path-not-found family -> ENOENT.
			switch uintptr(errno) {
			case 5:
				return "EPERM"
			case 32, 33:
				return "EBUSY"
			case 145:
				return "ENOTEMPTY"
			case 2, 3:
				return "ENOENT"
			}
		}
		switch errno {
		case syscall.EPERM:
			return "EPERM"
		case syscall.EACCES:
			return "EACCES"
		case syscall.EBUSY:
			return "EBUSY"
		case syscall.ENOTEMPTY:
			return "ENOTEMPTY"
		case syscall.ENOENT:
			return "ENOENT"
		case syscall.ENOTDIR:
			return "ENOTDIR"
		case syscall.EEXIST:
			return "EEXIST"
		case syscall.EISDIR:
			return "EISDIR"
		case syscall.EROFS:
			return "EROFS"
		case syscall.ENOSPC:
			return "ENOSPC"
		case syscall.EINVAL:
			return "EINVAL"
		case syscall.ELOOP:
			return "ELOOP"
		case syscall.ENAMETOOLONG:
			return "ENAMETOOLONG"
		case syscall.EMFILE:
			return "EMFILE"
		case syscall.EIO:
			return "EIO"
		}
	}
	if errors.Is(err, fs.ErrNotExist) {
		return "ENOENT"
	}
	return ""
}

// transientFsErrorCodes are the errno values a transient lock produces on an otherwise-valid operation. The set is the platform
// gate: a POSIX host that produces one of these is in the same situation a Windows host is.
var transientFsErrorCodes = map[string]bool{"EPERM": true, "EBUSY": true, "EACCES": true}

// defaultRetryDelaysMs is three retries, 60 ms of blocking at worst: well under one poll tick and far under the grace a forwarding
// child waits out.
var defaultRetryDelaysMs = []int{10, 20, 30}

// TransientFsRetryOptions are the tuning seams, injected so a unit test asserts the backoff without sleeping.
type TransientFsRetryOptions struct {
	// DelaysMs is the delay before each retry; nil selects the default {10, 20, 30}, an empty slice means no retry.
	DelaysMs []int
	// Sleep blocks for ms; nil sleeps.
	Sleep func(ms int)
}

// TransientFsRetryRecord is how an operation that eventually succeeded got there.
type TransientFsRetryRecord struct {
	// Attempts made, counting the first: 1 when it succeeded outright.
	Attempts int
	// Code is the transient errno that forced the retries, "" (null) when there were none.
	Code string
}

// RetryOnTransientFsError runs operation, retrying while a transient filesystem lock rejects it. An error outside the transient set
// is returned immediately with no sleep; an exhausted budget returns the last error.
func RetryOnTransientFsError(operation func() error, options *TransientFsRetryOptions) (TransientFsRetryRecord, error) {
	delays := defaultRetryDelaysMs
	sleep := func(ms int) { time.Sleep(time.Duration(ms) * time.Millisecond) }
	if options != nil {
		if options.DelaysMs != nil {
			delays = options.DelaysMs
		}
		if options.Sleep != nil {
			sleep = options.Sleep
		}
	}
	attempts, code := 0, ""
	for {
		err := operation()
		if err == nil {
			return TransientFsRetryRecord{Attempts: attempts + 1, Code: code}, nil
		}
		transient := FsErrnoCode(err)
		if !transientFsErrorCodes[transient] || attempts >= len(delays) {
			return TransientFsRetryRecord{}, err
		}
		code = transient
		sleep(delays[attempts])
		attempts++
	}
}

// NodeFsError is an operating-system error worded as Node words an fs error: "<CODE>: <description>, <syscall> '<path>'". It keeps
// the wrapped error for errors.Is and the errno code for FsErrnoCode, so only the message changes: the review log records
// `error.message` of the original's fs call, and a log reader should not see two dialects.
type NodeFsError struct {
	Err  error
	Call string
	Path string
	// Dest is the second path of a rename.
	Dest string
}

var nodeErrnoText = map[string]string{
	"ENOENT":       "no such file or directory",
	"EACCES":       "permission denied",
	"ENOTDIR":      "not a directory",
	"EEXIST":       "file already exists",
	"EISDIR":       "illegal operation on a directory",
	"EPERM":        "operation not permitted",
	"EROFS":        "read-only file system",
	"ENOSPC":       "no space left on device",
	"ENOTEMPTY":    "directory not empty",
	"EBUSY":        "resource busy or locked",
	"EINVAL":       "invalid argument",
	"ELOOP":        "too many symbolic links encountered",
	"ENAMETOOLONG": "name too long",
	"EMFILE":       "too many open files",
	"EIO":          "i/o error",
}

func (e *NodeFsError) Error() string {
	text, known := nodeErrnoText[FsErrnoCode(e.Err)]
	if !known {
		return e.Err.Error()
	}
	if e.Dest != "" {
		return FsErrnoCode(e.Err) + ": " + text + ", " + e.Call + " '" + e.Path + "' -> '" + e.Dest + "'"
	}
	return FsErrnoCode(e.Err) + ": " + text + ", " + e.Call + " '" + e.Path + "'"
}

// Unwrap exposes the operating-system error.
func (e *NodeFsError) Unwrap() error { return e.Err }

// ErrnoCode implements ErrnoCoder.
func (e *NodeFsError) ErrnoCode() string { return FsErrnoCode(e.Err) }

// wrapFsError words an operating-system error (one that carries an errno) as Node would, and leaves any other error as it is.
func wrapFsError(err error, call, path string) error {
	return wrapFsError2(err, call, path, "")
}

func wrapFsError2(err error, call, path, dest string) error {
	var errno syscall.Errno
	if err == nil || !errors.As(err, &errno) {
		return err
	}
	var coder ErrnoCoder
	if errors.As(err, &coder) {
		return err
	}
	return &NodeFsError{Err: err, Call: call, Path: path, Dest: dest}
}
