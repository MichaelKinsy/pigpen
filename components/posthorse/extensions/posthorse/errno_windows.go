//go:build windows

package posthorse

import "syscall"

// platformErrnoNames maps the Win32 error codes an fs call can return to libuv's names
// (deps/uv/src/win/error.c uv_translate_sys_error). A code libuv does not translate is UNKNOWN.
// Not exercised on Windows (go vet only); the list is the translation table of libuv 1.x.
var platformErrnoNames = map[syscall.Errno]string{
	1: "EISDIR", 2: "ENOENT", 3: "ENOENT", 4: "EMFILE", 5: "EPERM", 6: "EBADF", 8: "ENOMEM", 14: "ENOMEM", 15: "ENOENT",
	17: "EXDEV", 19: "EPERM", 32: "EBUSY", 33: "EBUSY", 39: "ENOSPC", 50: "ENOTSUP", 80: "EEXIST", 82: "ENOSPC",
	87: "EINVAL", 109: "EOF", 111: "ENAMETOOLONG", 112: "ENOSPC", 121: "ETIMEDOUT", 122: "EINVAL", 123: "ENOENT",
	126: "ENOENT", 145: "ENOTEMPTY", 161: "ENOENT", 170: "EBUSY", 183: "EEXIST", 203: "ENOENT", 206: "ENOENT",
	230: "EPIPE", 231: "EBUSY", 232: "EPIPE", 233: "EPIPE", 267: "ENOENT", 277: "ENOSPC", 487: "EFAULT", 534: "ERANGE",
	740: "EACCES", 998: "EACCES", 1004: "EBADF", 1100: "ENOSPC", 1113: "ECHARSET", 1314: "EPERM", 1464: "EINVAL",
	1920: "EACCES", 1921: "ELOOP", 4392: "ENOENT",
}
