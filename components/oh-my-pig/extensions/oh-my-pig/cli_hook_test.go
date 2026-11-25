package ohmypig

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/ompsession"
)

const hookOmpSession = `{"type":"title","v":1,"title":"T"}
{"type":"session","version":3,"id":"sess-1","timestamp":"2026-11-25T09:00:00Z","cwd":"/p"}
{"type":"message","id":"e1","parentId":null,"timestamp":"t","message":{"role":"user","content":"hi"}}
`

// hookFixture is a home with one omp session of the project at cwd, and the PiG session directory of that project.
func hookFixture(t *testing.T) (home, cwd, src, sessionDir string, getenv func(string) string) {
	t.Helper()
	home = t.TempDir()
	cwd = filepath.Join(home, "proj")
	srcDir := ompsession.SourceDirs(home, cwd)[0]
	if err := os.MkdirAll(srcDir, 0o700); err != nil {
		t.Fatal(err)
	}
	src = filepath.Join(srcDir, "a_sess-1.jsonl")
	if err := os.WriteFile(src, []byte(hookOmpSession), 0o600); err != nil {
		t.Fatal(err)
	}
	getenv = func(string) string { return "" }
	sessionDir, err := ompsession.PigSessionDir("", home, cwd, getenv)
	if err != nil {
		t.Fatal(err)
	}
	return home, cwd, src, sessionDir, getenv
}

func sessionFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".jsonl") {
			names = append(names, entry.Name())
		}
	}
	return names
}

func TestCommandLinesThatResumeFindTheProjectsOmpSessions(t *testing.T) {
	for _, args := range [][]string{{"--resume"}, {"-r"}, {"--continue"}, {"-c", "-p", "go on"}, {"--session", "sess-1"}} {
		home, cwd, _, sessionDir, getenv := hookFixture(t)
		var stderr bytes.Buffer
		got := importOmpSessions(args, home, cwd, getenv, &stderr)
		if !slices.Equal(got, args) {
			t.Errorf("%v: PiG parses %v", args, got)
		}
		if files := sessionFiles(t, sessionDir); !slices.Equal(files, []string{"a_sess-1-omp.jsonl"}) {
			t.Errorf("%v: session directory holds %v (stderr %q)", args, files, stderr.String())
		}
	}
}

func TestCommandLinesThatStartANewSessionReadNothing(t *testing.T) {
	for _, args := range [][]string{nil, {"-p", "hello"}, {"--continue", "--no-session"}, {"--model", "x"}} {
		home, cwd, _, sessionDir, getenv := hookFixture(t)
		var stderr bytes.Buffer
		if got := importOmpSessions(args, home, cwd, getenv, &stderr); !slices.Equal(got, args) {
			t.Errorf("%v: PiG parses %v", args, got)
		}
		if files := sessionFiles(t, sessionDir); len(files) != 0 || stderr.Len() != 0 {
			t.Errorf("%v: session directory holds %v, stderr %q", args, files, stderr.String())
		}
	}
}

func TestAnOmpSessionFileNamedOnTheCommandLineIsOpenedAsItsTranslation(t *testing.T) {
	home, cwd, src, sessionDir, getenv := hookFixture(t)
	other := filepath.Join(home, "elsewhere.jsonl")
	if err := os.WriteFile(other, []byte(hookOmpSession), 0o600); err != nil {
		t.Fatal(err)
	}
	pig := filepath.Join(home, "pig.jsonl")
	if err := os.WriteFile(pig, []byte(`{"type":"session","version":3,"id":"p","timestamp":"t","cwd":"/p"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	for _, tc := range []struct{ arg, want string }{
		{src, filepath.Join(sessionDir, "a_sess-1-omp.jsonl")},
		{"~/elsewhere.jsonl", filepath.Join(sessionDir, "elsewhere-omp.jsonl")},
		{pig, pig},           // a PiG session is PiG's to open
		{"sess-1", "sess-1"}, // an id is PiG's to resolve
		{"missing.jsonl", "missing.jsonl"},
	} {
		got := importOmpSessions([]string{"--fork", tc.arg, "-p", "x"}, home, cwd, getenv, &stderr)
		if want := []string{"--fork", tc.want, "-p", "x"}; !slices.Equal(got, want) {
			t.Errorf("--fork %s: PiG parses %v, want %v", tc.arg, got, want)
		}
	}
	// --session-dir names where PiG looks, so the translations go there.
	custom := filepath.Join(home, "custom")
	got := importOmpSessions([]string{"--session-dir", custom, "--session", src}, home, cwd, getenv, &stderr)
	if want := filepath.Join(custom, "a_sess-1-omp.jsonl"); got[3] != want {
		t.Errorf("--session-dir: PiG opens %s, want %s", got[3], want)
	}
}
