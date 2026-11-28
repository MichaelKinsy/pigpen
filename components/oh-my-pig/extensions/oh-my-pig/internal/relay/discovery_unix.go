//go:build !windows

package relay

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
)

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || !errors.Is(err, syscall.ESRCH)
}

// endpointDirSafe verifies that the endpoints directory is a real directory owned
// by the current user and not readable/writable/searchable by group or other.
func endpointDirSafe(dir string) bool {
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	if info.Mode().Perm()&0o077 != 0 {
		return false
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		if int(st.Uid) != os.Getuid() {
			return false
		}
	}
	return true
}

// checkEndpointFileSafe verifies that an endpoint token file is owned by the current
// user and has no group/other permission bits (must be 0600 or stricter).
func checkEndpointFileSafe(info fs.FileInfo) error {
	if info.Mode().Perm()&0o077 != 0 {
		return ErrRelayTokenUnsafePermissions
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		if int(st.Uid) != os.Getuid() {
			return ErrRelayTokenUnsafePermissions
		}
	}
	return nil
}
