//go:build !linux && !darwin && !windows

package posthorse

import "syscall"

// platformErrnoNames covers the errnos the fs calls of files.go can return on the other Unix systems, which are
// not a Pigpen target; anything else reports as UNKNOWN.
var platformErrnoNames = map[syscall.Errno]string{
	syscall.EPERM: "EPERM", syscall.ENOENT: "ENOENT", syscall.EIO: "EIO", syscall.ENXIO: "ENXIO", syscall.EBADF: "EBADF",
	syscall.EAGAIN: "EAGAIN", syscall.ENOMEM: "ENOMEM", syscall.EACCES: "EACCES", syscall.EBUSY: "EBUSY",
	syscall.EEXIST: "EEXIST", syscall.EXDEV: "EXDEV", syscall.ENODEV: "ENODEV", syscall.ENOTDIR: "ENOTDIR",
	syscall.EISDIR: "EISDIR", syscall.EINVAL: "EINVAL", syscall.ENFILE: "ENFILE", syscall.EMFILE: "EMFILE",
	syscall.ETXTBSY: "ETXTBSY", syscall.EFBIG: "EFBIG", syscall.ENOSPC: "ENOSPC", syscall.EROFS: "EROFS",
	syscall.EMLINK: "EMLINK", syscall.ENAMETOOLONG: "ENAMETOOLONG", syscall.ENOTEMPTY: "ENOTEMPTY", syscall.ELOOP: "ELOOP",
}
