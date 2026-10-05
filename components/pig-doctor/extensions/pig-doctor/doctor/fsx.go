package doctor

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// readHook is called for every file the doctor reads. Tests use it to prove
// that a credential file is never opened.
var readHook func(path string)

// renameFn is os.Rename; tests replace it to exercise the cross-device fallback.
var renameFn = os.Rename

// errCrossDevice is what rename returns when source and destination are on different devices.
var errCrossDevice error = syscall.EXDEV

var errProtected = errors.New("refusing to read a protected file")

// protectedName reports whether a file name holds credentials or trust and
// permission state. The doctor never reads, copies, moves or deletes these,
// except that the credentials group deletes credential copies (never reading them).
func protectedName(base string) bool {
	b := strings.ToLower(base)
	for _, stem := range []string{"auth.json", "oauth.json", "trust.json"} {
		if b == stem || strings.HasPrefix(b, stem+".") {
			return true
		}
	}
	if strings.HasPrefix(b, "models") {
		return true
	}
	return strings.HasSuffix(b, ".pem") || strings.HasSuffix(b, ".key")
}

// credentialName is the subset of protected names that are credentials.
func credentialName(base string) bool {
	b := strings.ToLower(base)
	for _, stem := range []string{"auth.json", "oauth.json"} {
		if b == stem || strings.HasPrefix(b, stem+".") {
			return !strings.HasSuffix(b, ".lock")
		}
	}
	return false
}

// readFileGuarded is the only way the doctor reads file content.
func readFileGuarded(path string) ([]byte, error) {
	if protectedName(filepath.Base(path)) {
		return nil, fmt.Errorf("%w: %s", errProtected, path)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file (symlinks are not followed)", path)
	}
	if readHook != nil {
		readHook(path)
	}
	return os.ReadFile(path)
}

// readSmall reads at most limit trailing bytes of a regular file.
func readTail(path string, limit int64) ([]byte, error) {
	if protectedName(filepath.Base(path)) {
		return nil, fmt.Errorf("%w: %s", errProtected, path)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if readHook != nil {
		readHook(path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if info.Size() > limit {
		if _, err := f.Seek(info.Size()-limit, io.SeekStart); err != nil {
			return nil, err
		}
	}
	return io.ReadAll(f)
}

func isSymlink(info fs.FileInfo) bool { return info.Mode()&fs.ModeSymlink != 0 }

// lstatDir lists a directory; entries are never followed.
func readDir(dir string) ([]fs.DirEntry, error) {
	return os.ReadDir(dir)
}

// treeStats walks root without following symlinks.
type treeStats struct {
	Bytes    int64
	Files    int
	Symlinks int
	Newest   int64 // unix nanoseconds of the newest modification time
	Sessions bool  // a "sessions" directory with content exists directly inside
	Creds    []string
	Locks    []string
}

func walkTree(root string) treeStats {
	var st treeStats
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return st
	}
	if !rootInfo.IsDir() {
		st.Bytes = rootInfo.Size()
		st.Files = 1
		st.Newest = rootInfo.ModTime().UnixNano()
		return st
	}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		if t := info.ModTime().UnixNano(); t > st.Newest {
			st.Newest = t
		}
		rel, _ := filepath.Rel(root, p)
		switch {
		case isSymlink(info):
			st.Symlinks++
		case info.IsDir():
			if rel == "sessions" {
				if es, err := os.ReadDir(p); err == nil && len(es) > 0 {
					st.Sessions = true
				}
			}
		default:
			st.Files++
			st.Bytes += info.Size()
			if credentialName(d.Name()) {
				st.Creds = append(st.Creds, rel)
			}
			if strings.HasSuffix(d.Name(), ".lock") {
				st.Locks = append(st.Locks, p)
			}
		}
		return nil
	})
	return st
}

// within reports whether path is root or below it (lexically).
func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func strictlyWithin(root, path string) bool { return path != root && within(root, path) }

// noSymlinkBelow verifies that no component of path below root is a symlink and
// that root itself resolves to a real directory: the path stays inside the PiG
// home for real, not just lexically.
func noSymlinkBelow(root, path string) error {
	if path != filepath.Clean(path) {
		return fmt.Errorf("%s is not a clean absolute path", path)
	}
	if !within(root, path) {
		return fmt.Errorf("%s is outside %s", path, root)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	rel, _ := filepath.Rel(root, path)
	cur := realRoot
	if rel == "." {
		return nil
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if err != nil {
			return err
		}
		if isSymlink(info) {
			return fmt.Errorf("%s is a symlink; symlinks are never followed", cur)
		}
	}
	return nil
}

// errSourceRemains: a cross-device move copied everything but could not remove
// all of the source. The copy is complete, so the move must still be recorded.
var errSourceRemains = errors.New("copied to the backup, but the source could not be removed completely")

// movePath renames src to dst, falling back to a faithful copy plus removal when
// they are on different devices. The copy never follows symlinks and refuses
// protected files, so a credential is never copied.
func movePath(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("%s already exists", dst)
	}
	err := renameFn(src, dst)
	if err == nil {
		return nil
	}
	if !errors.Is(err, errCrossDevice) {
		return err
	}
	if err := copyTree(src, dst); err != nil {
		_ = os.RemoveAll(dst)
		return err
	}
	if err := os.RemoveAll(src); err != nil {
		return fmt.Errorf("%w: %v", errSourceRemains, err)
	}
	return nil
}

func copyTree(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	switch {
	case isSymlink(info):
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(target, dst)
	case info.IsDir():
		if err := os.MkdirAll(dst, info.Mode().Perm()|0o700); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyTree(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		return os.Chmod(dst, info.Mode().Perm())
	case info.Mode().IsRegular():
		if protectedName(filepath.Base(src)) {
			return fmt.Errorf("%w: %s", errProtected, src)
		}
		in, err := os.Open(src)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
		return os.Chtimes(dst, info.ModTime(), info.ModTime())
	default:
		return fmt.Errorf("%s: unsupported file type", src)
	}
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
