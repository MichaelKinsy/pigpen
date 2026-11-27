//go:build !windows

package ssh

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// OwnerPrivateDirStat describes metadata inspected by OwnerPrivateDirError.
// upstream: packages/coding-agent/src/utils/owner-private-dir.ts:4-9
type OwnerPrivateDirStat struct {
	IsSymlink bool
	IsDir     bool
	UID       uint32
	Mode      os.FileMode
}

// OwnerPrivateDirError checks whether a directory reused from a shared temp
// base is safe to use: real directory (not a symlink), owned by expectedUID
// (when provided), mode 0700. Returns "" when safe.
// upstream: packages/coding-agent/src/utils/owner-private-dir.ts:17-25
func OwnerPrivateDirError(st OwnerPrivateDirStat, expectedUID *uint32) string {
	if st.IsSymlink {
		return "is a symlink"
	}
	if !st.IsDir {
		return "is not a directory"
	}
	if expectedUID != nil && st.UID != *expectedUID {
		return fmt.Sprintf("is owned by uid %d, not %d", st.UID, *expectedUID)
	}
	if st.Mode&0o777 != 0o700 {
		return fmt.Sprintf("must be mode 0700, got %o", st.Mode&0o777)
	}
	return ""
}

// AssertOwnerPrivateDir hardens a directory pulled from a shared temp base
// using O_NOFOLLOW | O_DIRECTORY and fstat/fchmod on the pinned fd.
// upstream: packages/coding-agent/src/utils/owner-private-dir.ts:40-80
func AssertOwnerPrivateDir(dir, label string) error {
	uid := uint32(os.Getuid())
	fd, err := syscall.Open(dir, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) || errors.Is(err, syscall.ENOTDIR) {
			isSymlink := false
			if fi, lerr := os.Lstat(dir); lerr == nil {
				isSymlink = fi.Mode()&os.ModeSymlink != 0
			}
			if isSymlink {
				return fmt.Errorf("%s %s is a symlink", label, dir)
			}
			return fmt.Errorf("%s %s is not a directory", label, dir)
		}
		return err
	}
	defer syscall.Close(fd)

	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		return err
	}
	if st.Uid == uid && (st.Mode&0o777) != 0o700 {
		if err := syscall.Fchmod(fd, 0o700); err == nil {
			_ = syscall.Fstat(fd, &st)
		}
	}
	reason := OwnerPrivateDirError(OwnerPrivateDirStat{
		IsSymlink: false,
		IsDir:     (st.Mode & syscall.S_IFMT) == syscall.S_IFDIR,
		UID:       st.Uid,
		Mode:      os.FileMode(st.Mode & 0o777),
	}, &uid)
	if reason != "" {
		return fmt.Errorf("%s %s %s", label, dir, reason)
	}
	return nil
}

func currentUID() (uint32, bool) {
	return uint32(os.Getuid()), true
}

// writeOwnerPrivateFile writes data atomically inside an owner-private directory
// using O_CREATE|O_EXCL|O_NOFOLLOW|O_CLOEXEC on a unique temp file and rename.
func writeOwnerPrivateFile(dir, name string, data []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := AssertOwnerPrivateDir(dir, "SSH cache directory"); err != nil {
		return err
	}
	tmpName := fmt.Sprintf("%s.tmp.%s", name, randomTempSuffix())
	tmpPath := dir + "/" + tmpName
	fd, err := syscall.Open(tmpPath, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0o600)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), tmpPath)
	if _, werr := f.Write(data); werr != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return werr
	}
	if cerr := f.Close(); cerr != nil {
		_ = os.Remove(tmpPath)
		return cerr
	}
	dest := dir + "/" + name
	if rerr := os.Rename(tmpPath, dest); rerr != nil {
		_ = os.Remove(tmpPath)
		return rerr
	}
	return nil
}
