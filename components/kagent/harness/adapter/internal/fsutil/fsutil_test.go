package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTailBufferKeepsTheLastBytesAndConsumesEveryWrite(t *testing.T) {
	buffer := NewTailBuffer(5)
	for _, chunk := range []string{"abc", "def", "ghi"} {
		if n, err := buffer.Write([]byte(chunk)); n != len(chunk) || err != nil {
			t.Fatalf("Write(%q) = %d, %v", chunk, n, err)
		}
	}
	if got := buffer.String(); got != "efghi" {
		t.Fatalf("tail = %q", got)
	}
	if zero := NewTailBuffer(0); zero.String() != "" {
		t.Fatal("zero-size buffer retained data")
	}
}

func TestReplacePrivateFileIsPrivateAndAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state.json")
	for _, contents := range []string{"before", "after"} {
		if err := ReplacePrivateFile(path, []byte(contents)); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := os.ReadFile(path)
	file, _ := os.Stat(path)
	dir, _ := os.Stat(filepath.Dir(path))
	if string(got) != "after" || file.Mode().Perm() != 0o600 || dir.Mode().Perm() != 0o700 {
		t.Fatalf("contents %q modes %v %v", got, file.Mode().Perm(), dir.Mode().Perm())
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("leftovers: %v", entries)
	}
}

func TestEnsurePrivateDirRejectsSymlinksAndFiles(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "real")
	os.Mkdir(target, 0o755)
	link := filepath.Join(root, "link")
	os.Symlink(target, link)
	file := filepath.Join(root, "file")
	os.WriteFile(file, nil, 0o600)
	for _, path := range []string{link, file} {
		if err := EnsurePrivateDir(path); err == nil {
			t.Errorf("EnsurePrivateDir(%s) succeeded", path)
		}
	}
	if err := EnsurePrivateDir(target); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(target); info.Mode().Perm() != 0o700 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}
}
