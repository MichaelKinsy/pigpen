//go:build !windows

package posthorse

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const notesAppendFaultEnv = "POSTHORSE_NOTES_APPEND_FAULT_DIR"

// An append that hits the file size limit (`ulimit -f 2`) fails with Node's descriptor error, which
// names no path ("EFBIG: file too large, write"). The records before it stay (a partial record may follow, as
// with Node's write loop).
func TestNotesAppendFileSizeFailure(t *testing.T) {
	dir := notesRealDir(t)
	command := exec.Command("/bin/bash", "-c", `ulimit -f 2; exec "$@"`, "note-append", os.Args[0], "-test.run=^TestHelperNoteAppendFailure$")
	command.Dir = dir
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "TMPDIR=" + dir, notesAppendFaultEnv + "=" + dir}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("child failed: %v\n%s", err, output)
	}
	if got, err := os.ReadFile(filepath.Join(dir, ".pi", "notes", "log.md")); err != nil || !strings.HasPrefix(string(got), "first\n") {
		t.Fatalf("the note after the failed append: %.40q, %v", got, err)
	}
}

// TestHelperNoteAppendFailure is the child of TestNotesAppendFileSizeFailure; it does nothing otherwise.
func TestHelperNoteAppendFailure(t *testing.T) {
	dir := os.Getenv(notesAppendFaultEnv)
	if dir == "" {
		return
	}
	f := newFixture(t).With(withCwd(dir))
	f.Run("notes", rec{"op": "append", "path": "log.md", "content": "first"})
	_, failure := f.RunErr("notes", rec{"op": "append", "path": "log.md", "content": strings.Repeat("N", 8192)})
	if failure != "EFBIG: file too large, write" {
		t.Fatalf("append error %q", failure)
	}
}
