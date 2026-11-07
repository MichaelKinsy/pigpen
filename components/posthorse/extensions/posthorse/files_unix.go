//go:build !windows

package posthorse

import (
	"io/fs"
	"os"
	"syscall"
)

// matchOwnerAndMode gives the staged file the owner and permission bits of the note it replaces
// (files.ts:104-111): fstat, chown only when the owner differs, then chmod to mode & 0o777. Each
// failure is the Node fd error of its call (no path): fstat, fchown or fchmod.
func matchOwnerAndMode(file *os.File, previous fs.FileInfo) error {
	want, ok := previous.Sys().(*syscall.Stat_t)
	if ok {
		staged, err := file.Stat()
		if err != nil {
			return nodeFsError(err, "fstat", "")
		}
		if have, ok := staged.Sys().(*syscall.Stat_t); ok && (have.Uid != want.Uid || have.Gid != want.Gid) {
			if err := file.Chown(int(want.Uid), int(want.Gid)); err != nil {
				return nodeFsError(err, "fchown", "")
			}
		}
	}
	if err := file.Chmod(previous.Mode() & 0o777); err != nil {
		return nodeFsError(err, "fchmod", "")
	}
	return nil
}

// symlinkJunction is `symlinkSync(target, path, "junction")`: the type argument only matters on Windows.
func symlinkJunction(target, path string) error { return os.Symlink(target, path) }
