//go:build darwin

package posthorse

import "syscall"

// platformErrnoNames are macOS's errnos with a libuv name (the same names as Linux's table; libuv has no
// ENONET, EUNATCH or EREMOTEIO there). EDQUOT, ESTALE and the other errnos libuv does not name are UNKNOWN.
var platformErrnoNames = map[syscall.Errno]string{
	syscall.EPERM: "EPERM", syscall.ENOENT: "ENOENT", syscall.ESRCH: "ESRCH", syscall.EINTR: "EINTR", syscall.EIO: "EIO",
	syscall.ENXIO: "ENXIO", syscall.E2BIG: "E2BIG", syscall.ENOEXEC: "ENOEXEC", syscall.EBADF: "EBADF",
	syscall.EAGAIN: "EAGAIN", syscall.ENOMEM: "ENOMEM", syscall.EACCES: "EACCES", syscall.EFAULT: "EFAULT",
	syscall.EBUSY: "EBUSY", syscall.EEXIST: "EEXIST", syscall.EXDEV: "EXDEV", syscall.ENODEV: "ENODEV",
	syscall.ENOTDIR: "ENOTDIR", syscall.EISDIR: "EISDIR", syscall.EINVAL: "EINVAL", syscall.ENFILE: "ENFILE",
	syscall.EMFILE: "EMFILE", syscall.ENOTTY: "ENOTTY", syscall.ETXTBSY: "ETXTBSY", syscall.EFBIG: "EFBIG",
	syscall.ENOSPC: "ENOSPC", syscall.ESPIPE: "ESPIPE", syscall.EROFS: "EROFS", syscall.EMLINK: "EMLINK",
	syscall.EPIPE: "EPIPE", syscall.ERANGE: "ERANGE", syscall.ENAMETOOLONG: "ENAMETOOLONG", syscall.ENOSYS: "ENOSYS",
	syscall.ENOTEMPTY: "ENOTEMPTY", syscall.ELOOP: "ELOOP", syscall.ENODATA: "ENODATA", syscall.EPROTO: "EPROTO",
	syscall.EOVERFLOW: "EOVERFLOW", syscall.EILSEQ: "EILSEQ", syscall.ENOTSOCK: "ENOTSOCK",
	syscall.EDESTADDRREQ: "EDESTADDRREQ", syscall.EMSGSIZE: "EMSGSIZE", syscall.EPROTOTYPE: "EPROTOTYPE",
	syscall.ENOPROTOOPT: "ENOPROTOOPT", syscall.EPROTONOSUPPORT: "EPROTONOSUPPORT",
	syscall.ESOCKTNOSUPPORT: "ESOCKTNOSUPPORT", syscall.ENOTSUP: "ENOTSUP", syscall.EAFNOSUPPORT: "EAFNOSUPPORT",
	syscall.EADDRINUSE: "EADDRINUSE", syscall.EADDRNOTAVAIL: "EADDRNOTAVAIL", syscall.ENETDOWN: "ENETDOWN",
	syscall.ENETUNREACH: "ENETUNREACH", syscall.ECONNABORTED: "ECONNABORTED", syscall.ECONNRESET: "ECONNRESET",
	syscall.ENOBUFS: "ENOBUFS", syscall.EISCONN: "EISCONN", syscall.ENOTCONN: "ENOTCONN", syscall.ESHUTDOWN: "ESHUTDOWN",
	syscall.ETIMEDOUT: "ETIMEDOUT", syscall.ECONNREFUSED: "ECONNREFUSED", syscall.EHOSTDOWN: "EHOSTDOWN",
	syscall.EHOSTUNREACH: "EHOSTUNREACH", syscall.EALREADY: "EALREADY", syscall.ECANCELED: "ECANCELED",
}
