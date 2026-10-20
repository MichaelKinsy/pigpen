//go:build linux

package warden

import (
	"io/fs"
	"runtime"
	"syscall"
	"unsafe"
)

// sysStatx is the statx(2) system call number per architecture (the stdlib `syscall` package does not name it); an
// architecture not listed reports no birth time, which keeps the scratch exemption off.
var sysStatx = map[string]uintptr{"amd64": 332, "arm64": 291, "riscv64": 291, "386": 383, "arm": 397, "ppc64": 383, "ppc64le": 383, "s390x": 379, "loong64": 291}

// statxBtime reads a file's birth time with statx(2), the way libuv does for Node's `birthtimeMs`; 0 where the file
// system reports none. Only the stdlib `syscall` package is used.
func birthtimeMs(path string, _ fs.FileInfo) float64 {
	const (
		atSymlinkNofoll = 0x100
		statxBtime      = 0x800
	)
	nr, ok := sysStatx[runtime.GOARCH]
	if !ok {
		return 0
	}
	fdcwd := -100     // AT_FDCWD
	var buf [256]byte // struct statx is 256 bytes
	p, err := syscall.BytePtrFromString(path)
	if err != nil {
		return 0
	}
	_, _, errno := syscall.Syscall6(nr, uintptr(fdcwd), uintptr(unsafe.Pointer(p)), uintptr(atSymlinkNofoll), uintptr(statxBtime), uintptr(unsafe.Pointer(&buf[0])), 0)
	if errno != 0 {
		return 0
	}
	// stx_mask is the first uint32; stx_btime is a statx_timestamp {int64 sec; uint32 nsec; int32 pad} at offset 80.
	mask := *(*uint32)(unsafe.Pointer(&buf[0]))
	if mask&statxBtime == 0 {
		return 0
	}
	sec := *(*int64)(unsafe.Pointer(&buf[80]))
	nsec := *(*uint32)(unsafe.Pointer(&buf[88]))
	return float64(sec)*1000 + float64(nsec)/1e6
}

func fileDevIno(info fs.FileInfo) (uint64, uint64) {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Dev), uint64(st.Ino)
	}
	return 0, 0
}
