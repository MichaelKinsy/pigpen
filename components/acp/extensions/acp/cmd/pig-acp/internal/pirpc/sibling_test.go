package pirpc

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// fakeFiles is an isExec that knows exactly the listed paths.
func fakeFiles(paths ...string) func(string) bool {
	return func(p string) bool {
		for _, f := range paths {
			if p == f {
				return true
			}
		}
		return false
	}
}

func TestResolveSibling(t *testing.T) {
	dir := filepath.Join(string(filepath.Separator), "opt", "pig-acp")
	none := fakeFiles()

	t.Run("a pig beside pig-acp wins over PATH", func(t *testing.T) {
		want := filepath.Join(dir, "pig")
		if got := resolve("", "linux", dir, fakeFiles(want)); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
	t.Run("no sibling falls back to the PATH name", func(t *testing.T) {
		if got := resolve("", "linux", dir, none); got != "pig" {
			t.Errorf("got %q, want pig", got)
		}
		if got := resolve("", "windows", dir, none); got != "pig.cmd" {
			t.Errorf("windows got %q, want pig.cmd", got)
		}
	})
	t.Run("unknown own directory falls back to the PATH name", func(t *testing.T) {
		if got := resolve("", "linux", "", fakeFiles("pig")); got != "pig" {
			t.Errorf("got %q, want pig", got)
		}
	})
	t.Run("Windows looks for pig.exe, not pig", func(t *testing.T) {
		if SiblingName("windows") != "pig.exe" || SiblingName("linux") != "pig" || SiblingName("darwin") != "pig" {
			t.Fatalf("names: %q %q %q", SiblingName("windows"), SiblingName("linux"), SiblingName("darwin"))
		}
		want := filepath.Join(dir, "pig.exe")
		if got := resolve("", "windows", dir, fakeFiles(want)); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
		if got := resolve("", "windows", dir, fakeFiles(filepath.Join(dir, "pig"))); got != "pig.cmd" {
			t.Errorf("a bare pig beside a Windows pig-acp must not be used, got %q", got)
		}
	})
	t.Run("an absolute --pig is unchanged even when a sibling exists", func(t *testing.T) {
		abs := filepath.Join(string(filepath.Separator), "usr", "local", "bin", "pig")
		if got := resolve(abs, "linux", dir, fakeFiles(filepath.Join(dir, "pig"))); got != abs {
			t.Errorf("got %q, want %q", got, abs)
		}
	})
	t.Run("a relative --pig resolves against pig-acp's own directory", func(t *testing.T) {
		for override, want := range map[string]string{
			"./pig":       filepath.Join(dir, "pig"),
			"bin/acp":     filepath.Join(dir, "bin", "acp"),
			"../x/piglet": filepath.Join(filepath.Dir(dir), "x", "piglet"),
		} {
			if got := resolve(override, "linux", dir, none); got != want {
				t.Errorf("%q: got %q, want %q", override, got, want)
			}
		}
	})
	t.Run("a bare --pig name is left to PATH", func(t *testing.T) {
		if got := resolve("mypig", "linux", dir, fakeFiles(filepath.Join(dir, "mypig"))); got != "mypig" {
			t.Errorf("got %q, want mypig", got)
		}
	})
}

// TestSiblingOnDisk checks the real file test: a regular executable file counts, a directory
// or (off Windows) a file without an execute bit does not.
func TestSiblingOnDisk(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, SiblingName(runtime.GOOS))
	if isExecutableFile(p) {
		t.Fatal("a missing file counted")
	}
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && isExecutableFile(p) {
		t.Error("a file without an execute bit counted")
	}
	if err := os.Chmod(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if !isExecutableFile(p) {
		t.Error("an executable file did not count")
	}
	d := filepath.Join(dir, "adir")
	if err := os.Mkdir(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if isExecutableFile(d) {
		t.Error("a directory counted")
	}
	if got := resolve("", runtime.GOOS, dir, isExecutableFile); got != p {
		t.Errorf("got %q, want %q", got, p)
	}
}
