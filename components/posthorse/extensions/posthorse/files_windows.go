//go:build windows

package posthorse

import (
	"io/fs"
	"os"
)

// matchOwnerAndMode applies the permission bits of the replaced note; Windows has no owner to carry over.
func matchOwnerAndMode(file *os.File, previous fs.FileInfo) error {
	if err := file.Chmod(previous.Mode() & 0o777); err != nil {
		return nodeFsError(err, "fchmod", "")
	}
	return nil
}

// symlinkJunction is `symlinkSync(target, path, "junction")`. Go's standard library cannot create a
// junction; a directory symlink is the closest call (it needs the symlink privilege, and a failure
// is optional by the caller, files.ts:298-304).
func symlinkJunction(target, path string) error { return os.Symlink(target, path) }
