// Package hygiene holds checks that keep recorded fixtures and docs free of developer paths and host names.
package hygiene

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// forbidden matches what must never appear in a published fixture: a home directory path, a developer's name
// and the names of the hosts the fixtures were recorded on. The public GitHub organisation MichaelKinsy (the PiG
// repository URL appears in PiG's own output) is not a developer path, so it is removed from a line before matching.
var forbidden = regexp.MustCompile(`(?i)/home/|/Users/|kinsy|\bxd6[0-9]*\b|\bxd670\b|kagent-proof`)

func forbiddenIn(line string) []int {
	return forbidden.FindStringIndex(strings.ReplaceAll(line, "MichaelKinsy", "MichaelX"))
}

// TestFixturesAndDocsCarryNoDeveloperPathsOrHostNames scans every file under a testdata directory and the
// adapter README. A fixture recorded on a developer machine has to be scrubbed (the session cwd is the in-Actor
// /data/workspace) before it can be committed.
func TestFixturesAndDocsCarryNoDeveloperPathsOrHostNames(t *testing.T) {
	root := filepath.Join("..", "..")
	scanned := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		relative, _ := filepath.Rel(root, path)
		inTestdata := false
		for _, part := range strings.Split(filepath.ToSlash(relative), "/") {
			inTestdata = inTestdata || part == "testdata"
		}
		if !inTestdata && relative != "README.md" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		scanned++
		for number, line := range strings.Split(string(data), "\n") {
			if loc := forbiddenIn(line); loc != nil {
				line = strings.ReplaceAll(line, "MichaelKinsy", "MichaelX")
				excerpt := line[max(0, loc[0]-20):min(len(line), loc[1]+20)]
				t.Errorf("%s:%d contains %q near %q", relative, number+1, line[loc[0]:loc[1]], excerpt)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if scanned < 6 {
		t.Fatalf("scanned only %d files; the walk is not reaching the fixtures", scanned)
	}
}

func TestForbiddenPatternCatchesWhatItShould(t *testing.T) {
	for _, bad := range []string{"/home/someone/x", "/Users/someone", "cwd /tmp/kinsy", "host xd6702", "on xd670 today", "~/kagent-proof/x"} {
		if forbiddenIn(bad) == nil {
			t.Errorf("%q is not caught", bad)
		}
	}
	for _, good := range []string{"/data/workspace", "/run/kagent/pig", "/var/lib/actor", "MichaelPig", "hxd6x", "https://github.com/MichaelKinsy/PiG/tree/main/examples"} {
		if forbiddenIn(good) != nil {
			t.Errorf("%q is flagged", good)
		}
	}
}
