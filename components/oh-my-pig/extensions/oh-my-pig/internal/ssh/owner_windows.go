//go:build windows

package ssh

import (
	"fmt"
	"os"
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

// AssertOwnerPrivateDir checks that dir is a real directory on Windows.
// ControlMaster is unused on Windows so shared-temp relocation is not taken,
// but callers invoking this directly still reject symlinks and non-directories.
// upstream: packages/coding-agent/src/utils/owner-private-dir.ts:40-80
func AssertOwnerPrivateDir(dir, label string) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s %s is a symlink", label, dir)
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s %s is not a directory", label, dir)
	}
	return nil
}

func currentUID() (uint32, bool) {
	return 0, false
}

func writeOwnerPrivateFile(dir, name string, data []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := AssertOwnerPrivateDir(dir, "SSH cache directory"); err != nil {
		return err
	}
	tmpPath := dir + `\` + fmt.Sprintf("%s.tmp.%s", name, randomTempSuffix())
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return err
	}
	dest := dir + `\` + name
	if err := os.Rename(tmpPath, dest); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}
