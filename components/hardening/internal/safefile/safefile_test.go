package safefile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadRegularFile(t *testing.T) {
	got, err := Read(write(t, "f", "hello"), 16)
	if err != nil || string(got) != "hello" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestReadAtTheCapIsAllowedOneOverIsNot(t *testing.T) {
	if _, err := Read(write(t, "f", strings.Repeat("x", 16)), 16); err != nil {
		t.Fatalf("at the cap: %v", err)
	}
	if _, err := Read(write(t, "f", strings.Repeat("x", 17)), 16); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("one over: %v", err)
	}
}

func TestReadMissingIsNotExist(t *testing.T) {
	if _, err := Read(filepath.Join(t.TempDir(), "nope"), 16); !errors.Is(err, ErrNotExist) {
		t.Fatal(err)
	}
	f := write(t, "f", "x")
	if _, err := Read(filepath.Join(f, "under-a-file"), 16); !errors.Is(err, ErrNotExist) {
		t.Fatalf("a path below a file: %v", err)
	}
}

func TestReadDirectoryIsNotRegular(t *testing.T) {
	if _, err := Read(t.TempDir(), 16); !errors.Is(err, ErrNotRegular) {
		t.Fatal(err)
	}
}

func TestReadFollowsSymlinks(t *testing.T) {
	target := write(t, "target", "linked")
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks unavailable")
	}
	got, err := Read(link, 16)
	if err != nil || string(got) != "linked" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestReadOpensOnceAndErrorsNeverNameThePath(t *testing.T) {
	path := write(t, "secret-name", "x")
	opens := 0
	counting := func(name string, flag int, perm fs.FileMode) (*os.File, error) {
		opens++
		return os.OpenFile(name, flag, perm)
	}
	if _, err := ReadWith(counting, path, 16); err != nil {
		t.Fatal(err)
	}
	if opens != 1 {
		t.Fatalf("opens = %d, want 1", opens)
	}
	_, err := Read(filepath.Join(t.TempDir(), "secret-name"), 16)
	if err == nil || strings.Contains(err.Error(), "secret-name") {
		t.Fatalf("error names the path: %v", err)
	}
}

func TestReadOpenFailureIsUnreadable(t *testing.T) {
	failing := func(string, int, fs.FileMode) (*os.File, error) { return nil, os.ErrPermission }
	if _, err := ReadWith(failing, "/x", 16); !errors.Is(err, ErrUnreadable) {
		t.Fatal(err)
	}
}
