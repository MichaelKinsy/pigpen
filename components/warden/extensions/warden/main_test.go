package warden

import (
	"os"
	"path/filepath"
	"testing"
)

// cwd is the project directory of the guard twins (guard.test.ts `before`): a temp directory with
// one existing file.
var cwd string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "warden-guard-")
	if err != nil {
		panic(err)
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	cwd = dir
	if err := os.WriteFile(filepath.Join(cwd, "existing.txt"), []byte("keep me\n"), 0o600); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
