package pi_permission_system

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
)

// mockRealpath replaces realpathSync (the original mocks node:fs) for one test; the default is the identity.
func mockRealpath(t *testing.T, fn func(string) (string, error)) {
	t.Helper()
	old := realpathSync
	realpathSync = fn
	t.Cleanup(func() { realpathSync = old })
}

func identityRealpath(p string) (string, error) { return p, nil }

func errno(code syscall.Errno, p string) error {
	return &fs.PathError{Op: "realpath", Path: p, Err: code}
}

func mockHome(t *testing.T, home string) {
	t.Helper()
	old := homeDir
	homeDir = func() string { return home }
	t.Cleanup(func() { homeDir = old })
}

func strs(v ...string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func eqs(t *testing.T, got []string, want ...string) {
	t.Helper()
	if want == nil {
		want = []string{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func eqv[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

// realTmp is a temp dir with its symlinks resolved (macOS /var is a link), like the original's realpathSync(mkdtempSync(...)).
func realTmp(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func mkSubdir(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func symlink(t *testing.T, dir, name, target string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.Symlink(target, p); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	return p
}

func ptr(s string) *string { return &s }

func derefOr(p *string, def string) string {
	if p == nil {
		return def
	}
	return *p
}
