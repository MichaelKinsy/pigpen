package posthorse

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const (
	notesAppendDirEnv    = "POSTHORSE_NOTES_APPEND_DIR"
	notesAppendLetterEnv = "POSTHORSE_NOTES_APPEND_LETTER"
)

// runConcurrentNoteAppends is posthorse.test.ts:4754-4797. Each child process loads the real
// extension on its own fake host and appends 200 records to one shared note as fast as it can.
func runConcurrentNoteAppends(t *testing.T) {
	dir := notesRealDir(t)
	var wg sync.WaitGroup
	failures := make([]string, 2)
	for i, letter := range []string{"A", "B"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			command := exec.Command(os.Args[0], "-test.run=^TestHelperNoteAppends$")
			command.Env = append(os.Environ(), notesAppendDirEnv+"="+dir, notesAppendLetterEnv+"="+letter)
			if output, err := command.CombinedOutput(); err != nil {
				failures[i] = err.Error() + "\n" + string(output)
			}
		}()
	}
	wg.Wait()
	for _, failure := range failures {
		if failure != "" {
			t.Fatal(failure)
		}
	}
	bytes, err := os.ReadFile(filepath.Join(dir, ".pi", "notes", "shared.md"))
	notesMust(t, err)
	lines := strings.Split(string(bytes), "\n")
	if last := lines[len(lines)-1]; last != "" {
		t.Fatalf("file ends with a newline, last line %q", last)
	}
	lines = lines[:len(lines)-1]
	// A torn read of an in-flight append may add a blank separator line; records themselves never merge.
	records := 0
	for _, line := range lines {
		if line == "" {
			continue
		}
		records++
		if line != strings.Repeat("A", 300) && line != strings.Repeat("B", 300) {
			t.Fatalf("a record is not intact: %.60q (%d bytes)", line, len(line))
		}
	}
	if records != 400 {
		t.Fatalf("%d records, want 400", records)
	}
}

// TestHelperNoteAppends is the child of runConcurrentNoteAppends; it does nothing otherwise.
func TestHelperNoteAppends(t *testing.T) {
	dir, letter := os.Getenv(notesAppendDirEnv), os.Getenv(notesAppendLetterEnv)
	if dir == "" {
		return
	}
	f := newFixture(t).With(withCwd(dir))
	for range 200 {
		f.Run("notes", rec{"op": "append", "path": "shared.md", "content": strings.Repeat(letter, 300)})
	}
}
