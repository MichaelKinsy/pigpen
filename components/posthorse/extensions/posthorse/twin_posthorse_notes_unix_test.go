//go:build !windows

package posthorse

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// notesFaultEnv names the directory the re-executed test binary writes notes in.
const notesFaultEnv = "POSTHORSE_NOTES_FAULT_DIR"

// runNoteWriteFailure is posthorse.test.ts:3336-3385: a real EFBIG (`ulimit -f 2`) while a
// note is replaced must leave the old note and no temporary file. The test binary is
// re-executed under the limit and runs TestHelperNoteWriteFailure.
func runNoteWriteFailure(t *testing.T) {
	dir := notesRealDir(t)
	notesDir := filepath.Join(dir, ".pi", "notes")
	path := filepath.Join(notesDir, "durable.md")
	notesWrite(t, path, "original checkpoint")
	command := exec.Command("/bin/bash", "-c", `ulimit -f 2; exec "$@"`, "note-publication", os.Args[0], "-test.run=^TestHelperNoteWriteFailure$")
	command.Dir = dir
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "TMPDIR=" + dir, notesFaultEnv + "=" + dir}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("child failed: %v\n%s", err, output)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "original checkpoint" {
		t.Fatalf("note after the failed write: %q, %v", got, err)
	}
	entries, err := os.ReadDir(notesDir)
	notesMust(t, err)
	if len(entries) != 1 || entries[0].Name() != "durable.md" {
		t.Fatalf("notes directory after the failed write: %v", entries)
	}
}

// TestHelperNoteWriteFailure is the child of runNoteWriteFailure; it does nothing otherwise.
func TestHelperNoteWriteFailure(t *testing.T) {
	dir := os.Getenv(notesFaultEnv)
	if dir == "" {
		return
	}
	// Go ignores SIGXFSZ, so publication surfaces EFBIG through the returned error; the TS child
	// needs a SIGXFSZ handler for the same effect.
	f := newFixture(t).With(withCwd(dir))
	_, failure := f.RunErr("notes", rec{"op": "write", "path": "durable.md", "content": strings.Repeat("N", 8192)})
	if !strings.Contains(failure, "EFBIG") {
		t.Fatalf("write error %q, want EFBIG", failure)
	}
}
