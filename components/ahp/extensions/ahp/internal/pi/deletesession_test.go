package pi_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/ahp/internal/pi"
	"github.com/MichaelKinsy/pigpen/ahp/internal/twin"
)

// Twins of upstream test/delete-session.test.ts. The fake trash is a POSIX shell script, so these
// run on Unix only (the deletion code itself is portable).

func fakeTrash(t *testing.T, root, behavior string) (bin, calls string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake trash command is a POSIX shell script")
	}
	bin = filepath.Join(root, "bin")
	calls = filepath.Join(root, "trash-calls.json")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	script := []string{"#!/bin/sh",
		// Record the arguments as a JSON array.
		`{ printf '['; sep=""; for a in "$@"; do printf '%s"%s"' "$sep" "$a"; sep=","; done; printf ']'; } > ` + calls,
		`for a in "$@"; do target="$a"; done`,
	}
	if behavior == "remove" || behavior == "remove-and-fail" {
		script = append(script, `rm -f -- "$target"`)
	}
	if behavior == "fail" || behavior == "remove-and-fail" {
		script = append(script, "exit 1")
	}
	if err := os.WriteFile(filepath.Join(bin, "trash"), []byte(strings.Join(script, "\n")+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, calls
}

func write(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("session\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool { _, err := os.Lstat(path); return err == nil }

func TestSessionFileDeletion(t *testing.T) {
	twin.Run(t, "delete-session", "treats an already-missing file as success", func(t *testing.T) {
		got := pi.DeleteSessionFile(filepath.Join(t.TempDir(), "session.jsonl"))
		if !reflect.DeepEqual(got, pi.DeleteResult{OK: true, Method: "missing"}) {
			t.Fatalf("got %+v", got)
		}
	})

	twin.Run(t, "delete-session", "prefers trash and guards a path that looks like an option", func(t *testing.T) {
		root := t.TempDir()
		bin, calls := fakeTrash(t, root, "remove")
		previous, _ := os.Getwd()
		if err := os.Chdir(root); err != nil {
			t.Fatal(err)
		}
		defer os.Chdir(previous)
		write(t, "-session.jsonl")
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		if got := pi.DeleteSessionFile("-session.jsonl"); !reflect.DeepEqual(got, pi.DeleteResult{OK: true, Method: "trash"}) {
			t.Fatalf("got %+v", got)
		}
		if exists("-session.jsonl") {
			t.Fatal("the file remains")
		}
		raw, _ := os.ReadFile(calls)
		var args []string
		if err := json.Unmarshal(raw, &args); err != nil || !reflect.DeepEqual(args, []string{"--", "-session.jsonl"}) {
			t.Fatalf("trash arguments = %s (%v)", raw, err)
		}
	})

	twin.Run(t, "delete-session", "falls back to unlink when trash exits successfully but leaves the file", func(t *testing.T) {
		root := t.TempDir()
		bin, _ := fakeTrash(t, root, "leave")
		target := filepath.Join(root, "session.jsonl")
		write(t, target)
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		if got := pi.DeleteSessionFile(target); !reflect.DeepEqual(got, pi.DeleteResult{OK: true, Method: "unlink"}) {
			t.Fatalf("got %+v", got)
		}
		if exists(target) {
			t.Fatal("the file remains")
		}
	})

	twin.Run(t, "delete-session", "recognizes trash success even when the command exits nonzero", func(t *testing.T) {
		root := t.TempDir()
		bin, _ := fakeTrash(t, root, "remove-and-fail")
		target := filepath.Join(root, "session.jsonl")
		write(t, target)
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		if got := pi.DeleteSessionFile(target); !reflect.DeepEqual(got, pi.DeleteResult{OK: true, Method: "trash"}) {
			t.Fatalf("got %+v", got)
		}
		if exists(target) {
			t.Fatal("the file remains")
		}
	})

	twin.Run(t, "delete-session", "falls back to unlink when trash is unavailable or fails", func(t *testing.T) {
		for _, behavior := range []string{"missing", "fail"} {
			root := t.TempDir()
			var bin string
			if behavior == "fail" {
				bin, _ = fakeTrash(t, root, "fail")
			} else {
				bin = filepath.Join(root, "empty-bin")
				if err := os.Mkdir(bin, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			target := filepath.Join(root, "session.jsonl")
			write(t, target)
			t.Setenv("PATH", bin)
			if got := pi.DeleteSessionFile(target); !reflect.DeepEqual(got, pi.DeleteResult{OK: true, Method: "unlink"}) {
				t.Fatalf("%s: got %+v", behavior, got)
			}
			if exists(target) {
				t.Fatalf("%s: the file remains", behavior)
			}
		}
	})

	twin.Run(t, "delete-session", "returns an unlink error instead of claiming deletion", func(t *testing.T) {
		root := t.TempDir()
		target := filepath.Join(root, "directory.jsonl")
		if err := os.Mkdir(target, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", root)
		got := pi.DeleteSessionFile(target)
		if got.OK || got.Method != "unlink" || !strings.Contains(strings.ToLower(got.Error), "director") {
			t.Fatalf("got %+v", got)
		}
		if !exists(target) {
			t.Fatal("the directory was removed")
		}
	})
}
