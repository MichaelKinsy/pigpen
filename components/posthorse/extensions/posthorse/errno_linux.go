//go:build linux

package posthorse

import "syscall"

// platformErrnoNames are Linux's errnos with a libuv name (uv.h UV_ERRNO_MAP minus the non-errno names),
// numbered as Node reports them on Linux (util.getSystemErrorMap, negated).
var platformErrnoNames = map[syscall.Errno]string{
	1: "EPERM", 2: "ENOENT", 3: "ESRCH", 4: "EINTR", 5: "EIO", 6: "ENXIO", 7: "E2BIG", 8: "ENOEXEC", 9: "EBADF",
	11: "EAGAIN", 12: "ENOMEM", 13: "EACCES", 14: "EFAULT", 16: "EBUSY", 17: "EEXIST", 18: "EXDEV", 19: "ENODEV",
	20: "ENOTDIR", 21: "EISDIR", 22: "EINVAL", 23: "ENFILE", 24: "EMFILE", 25: "ENOTTY", 26: "ETXTBSY", 27: "EFBIG",
	28: "ENOSPC", 29: "ESPIPE", 30: "EROFS", 31: "EMLINK", 32: "EPIPE", 34: "ERANGE", 36: "ENAMETOOLONG",
	38: "ENOSYS", 39: "ENOTEMPTY", 40: "ELOOP", 61: "ENODATA", 64: "ENONET", 49: "EUNATCH", 71: "EPROTO",
	75: "EOVERFLOW", 84: "EILSEQ", 88: "ENOTSOCK", 89: "EDESTADDRREQ", 90: "EMSGSIZE", 91: "EPROTOTYPE",
	92: "ENOPROTOOPT", 93: "EPROTONOSUPPORT", 94: "ESOCKTNOSUPPORT", 95: "ENOTSUP", 97: "EAFNOSUPPORT",
	98: "EADDRINUSE", 99: "EADDRNOTAVAIL", 100: "ENETDOWN", 101: "ENETUNREACH", 103: "ECONNABORTED",
	104: "ECONNRESET", 105: "ENOBUFS", 106: "EISCONN", 107: "ENOTCONN", 108: "ESHUTDOWN", 110: "ETIMEDOUT",
	111: "ECONNREFUSED", 112: "EHOSTDOWN", 113: "EHOSTUNREACH", 114: "EALREADY", 121: "EREMOTEIO", 125: "ECANCELED",
}
