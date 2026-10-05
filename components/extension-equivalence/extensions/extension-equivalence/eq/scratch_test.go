package eq

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func scratchDirs(t *testing.T, tmp string) []string {
	t.Helper()
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "eq-out") {
			out = append(out, e.Name())
		}
	}
	return out
}

// A run or check that names no --out writes its traces to a private directory. When every scenario passed
// nobody needs them, so the directory must not pile up in TMPDIR; when one failed, its paths are in the
// result (for pigeq diff), so it stays.
func TestUnnamedOutDirIsRemovedAfterAPassAndKeptAfterAFailure(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	cfg := Config{}

	dir, release, err := cfg.scratchOut()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.jsonl"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	release([]Result{{Scenario: "a", Pass: true}}, nil)
	if left := scratchDirs(t, tmp); len(left) != 0 {
		t.Errorf("a passing run left %v", left)
	}

	dir, release, err = cfg.scratchOut()
	if err != nil {
		t.Fatal(err)
	}
	release([]Result{{Scenario: "a", Pass: true}, {Scenario: "b", Pass: false, Traces: map[string]string{"pig-go": filepath.Join(dir, "b.jsonl")}}}, nil)
	if left := scratchDirs(t, tmp); len(left) != 1 {
		t.Errorf("a failing run must keep its traces, left %v", left)
	}

	// An infrastructure error leaves no result that points into the directory.
	_, release, err = cfg.scratchOut()
	if err != nil {
		t.Fatal(err)
	}
	release(nil, os.ErrClosed)
	if left := scratchDirs(t, tmp); len(left) != 1 {
		t.Errorf("an errored run must remove its directory, left %v", left)
	}

	// A directory the caller named is theirs: never removed.
	named := filepath.Join(tmp, "mine")
	cfg = Config{Out: named}
	_, release, err = cfg.scratchOut()
	if err != nil {
		t.Fatal(err)
	}
	release([]Result{{Scenario: "a", Pass: true}}, nil)
	if _, err := os.Stat(named); err != nil {
		t.Errorf("--out directory was removed: %v", err)
	}
}
