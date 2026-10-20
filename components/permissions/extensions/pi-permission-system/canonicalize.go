package pi_permission_system

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Port of src/path/canonicalize-path.ts.

// realpathSync is Node's fs.realpathSync (the original mocks it): the symlink-resolved absolute path, or an error.
var realpathSync = func(p string) (string, error) {
	if p == "" {
		return "", &fs.PathError{Op: "realpath", Path: p, Err: syscall.ENOENT}
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// existsSync is Node's fs.existsSync: access(F_OK), following symlinks.
var existsSync = func(p string) bool {
	if p == "" {
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}

// isENOENTorENOTDIR is `code === "ENOENT" || code === "ENOTDIR"` of the caught error.
func isENOENTorENOTDIR(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ENOTDIR)
}

// CanonicalizePath resolves symlinks in an absolute path, best-effort: it tries realpath from the full path down to the root,
// re-appending the non-existent tail to the first ancestor that resolves. It returns the input unchanged when no ancestor
// resolves or when an error other than ENOENT/ENOTDIR (EACCES, ELOOP) is met, so callers fall back to lexical containment.
func CanonicalizePath(absolutePath string, flavor PathFlavor) string {
	if absolutePath == "" {
		return absolutePath
	}
	impl := flavor.impl()
	root := impl.Root(absolutePath)
	rest := absolutePath[len(root):]
	var parts []string
	for _, s := range strings.Split(rest, impl.Sep) {
		if s != "" {
			parts = append(parts, s)
		}
	}
	for i := len(parts); i >= 0; i-- {
		candidate := root + strings.Join(parts[:i], impl.Sep)
		real, err := realpathSync(candidate)
		if err != nil {
			if !isENOENTorENOTDIR(err) {
				return absolutePath
			}
			continue
		}
		tail := parts[i:]
		if len(tail) == 0 {
			return real
		}
		return impl.Join(append([]string{real}, tail...)...)
	}
	return absolutePath
}
