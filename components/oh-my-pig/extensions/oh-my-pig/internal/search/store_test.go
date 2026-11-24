package search

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/hashline"
)

// A grep hit is an anchor for edit: grep records the lines it displays in the store the file tools check.
func TestGrepRecordsDisplayedLinesInTheSharedStore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("one\nneedle two\nthree\nfour\nneedle five\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := runToolText(t, runGrep, dir, map[string]any{"pattern": "needle", "path": path})
	tag := hashline.FileHash("one\nneedle two\nthree\nfour\nneedle five\n")
	if want := "#" + tag; !contains(out, want) {
		t.Fatalf("grep output lacks the tag %s:\n%s", want, out)
	}
	snap, ok := hashline.Default().ByHash(path, tag)
	if !ok {
		t.Fatalf("the shared store has no snapshot %s for the file grep displayed", tag)
	}
	// Lines 2 and 5 were displayed; the others were not, so edit refuses an anchor there once.
	if !snap.SeenLines[2] || !snap.SeenLines[5] || snap.SeenLines[3] || len(snap.SeenLines) != 2 {
		t.Fatalf("seen lines = %v, want exactly 2 and 5", snap.SeenLines)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
