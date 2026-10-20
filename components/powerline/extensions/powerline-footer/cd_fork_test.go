package powerline_footer

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// forkSessionFrom against what Pi's SessionManager.forkFrom wrote for the same source file (testdata/cdfork/forked.jsonl,
// recorded on the pinned Pi build 1402973a: `SessionManager.forkFrom(source, "/target/dir", dir)`; source-after.jsonl is the
// source as Pi left it: its last line got the newline it lacked).
func TestForkSessionFromMatchesPi(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the recorded file names a posix target directory")
	}
	root := t.TempDir()
	source := filepath.Join(root, "source.jsonl")
	data, err := os.ReadFile("testdata/cdfork/source.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, data, 0o644); err != nil {
		t.Fatal(err)
	}
	sessions := filepath.Join(root, "sessions")
	now := time.Date(2026, 10, 20, 14, 39, 28, 230_000_000, time.UTC)

	path, err := forkSessionFrom(source, "/target/dir", sessions, now)
	if err != nil {
		t.Fatal(err)
	}

	if dir := filepath.Dir(path); dir != filepath.Join(sessions, "--target-dir--") {
		t.Errorf("directory %q", dir)
	}
	name := filepath.Base(path)
	if !regexp.MustCompile(`^2026-10-20T14-39-28-230Z_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\.jsonl$`).MatchString(name) {
		t.Errorf("file name %q", name)
	}
	got, _ := os.ReadFile(path)
	want, _ := os.ReadFile("testdata/cdfork/forked.jsonl")
	gotLines, wantLines := strings.Split(string(got), "\n"), strings.Split(string(want), "\n")
	if len(gotLines) != len(wantLines) {
		t.Fatalf("%d lines, Pi wrote %d:\n%s", len(gotLines), len(wantLines), got)
	}
	for i := 1; i < len(wantLines); i++ {
		if gotLines[i] != wantLines[i] {
			t.Errorf("line %d\n got %s\nwant %s", i+1, gotLines[i], wantLines[i])
		}
	}
	id := strings.TrimSuffix(name[strings.Index(name, "_")+1:], ".jsonl")
	if wantHeader := `{"type":"session","version":3,"id":"` + id + `","timestamp":"2026-10-20T14:39:28.230Z","cwd":"/target/dir","parentSession":"` + source + `"}`; gotLines[0] != wantHeader {
		t.Errorf("header\n got %s\nwant %s", gotLines[0], wantHeader)
	}
	if wantHeaderShape := regexp.MustCompile(`^\{"type":"session","version":3,"id":"[^"]+","timestamp":"[^"]+","cwd":"/target/dir","parentSession":"[^"]+"\}$`); !wantHeaderShape.MatchString(wantLines[0]) {
		t.Errorf("Pi's header has another shape: %s", wantLines[0])
	}
	after, _ := os.ReadFile(source)
	wantAfter, _ := os.ReadFile("testdata/cdfork/source-after.jsonl")
	if string(after) != string(wantAfter) {
		t.Errorf("source after the fork differs from Pi's (the missing last newline is added)")
	}
}

func TestForkSessionFromRefusesWhatPiRefuses(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	for name, content := range map[string]string{
		"missing":     "",
		"empty":       "",
		"headerless":  `{"type":"message","id":"a"}` + "\n",
		"idless":      `{"type":"session","version":3}` + "\n",
		"not json":    "hello\n",
		"blank lines": "\n\n",
	} {
		path := filepath.Join(root, name+".jsonl")
		if name != "missing" {
			_ = os.WriteFile(path, []byte(content), 0o644)
		}
		_, err := forkSessionFrom(path, "/x", filepath.Join(root, "sessions"), now)
		if err == nil || err.Error() != "Cannot fork: source session file is empty or invalid: "+path {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(root, "sessions")); len(entries) != 0 {
		t.Errorf("a refused fork created %v", entries)
	}
}

func TestSessionDirNameAndBasename(t *testing.T) {
	for cwd, want := range map[string]string{"/a/b": "--a-b--", `C:\Users\x`: "--C--Users-x--", "/": "----", "": "----", "//x": "---x--"} {
		if got := sessionDirName(cwd); got != want {
			t.Errorf("sessionDirName(%q) = %q, want %q", cwd, got, want)
		}
	}
	for p, want := range map[string]string{"/a/b": "b", "/a/b/": "b", "/": "", "a": "a", "": ""} {
		if got := nodeBasename(p); got != want {
			t.Errorf("nodeBasename(%q) = %q, want %q", p, got, want)
		}
	}
}

func TestNewUUIDv7(t *testing.T) {
	// The generator is monotonic over the whole process (pi-ai's uuidv7 too): start from a clean state.
	uuidMu.Lock()
	uuidState.lastTimestamp, uuidState.seeded = 0, false
	uuidMu.Unlock()
	ms := time.Date(2026, 10, 20, 14, 39, 28, 230_000_000, time.UTC).UnixMilli()
	a, err := newUUIDv7(ms)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := newUUIDv7(ms)
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(a) || a >= b {
		t.Errorf("ids %q %q must be UUIDv7 and increase within one millisecond", a, b)
	}
	if hex := fmt.Sprintf("%012x", ms); a[:13] != hex[:8]+"-"+hex[8:] {
		t.Errorf("the first 48 bits are the millisecond clock: %q", a)
	}
}
