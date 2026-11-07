package posthorse

// Tests of the ripgrep prefilter's contract (session-files.ts:136-186) with fake rg executables:
// the argument list and working directory, exit 1 (no match), a failing run (every file stays a
// candidate), the managed binary of getAgentDir() before PATH, and the unavailable binary. POSIX
// shells only: the fake is a shell script.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func hlFakeRG(t *testing.T, dir, script string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rg"), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// hlHitSources runs an all-session search for "needle" and returns the ids of the entries hit.
func hlHitSources(t *testing.T, dir string) []string {
	t.Helper()
	matches, err := historyMatches(context.Background(), hlSource(t, dir, ""),
		historyParams{op: "search", query: "needle", hasQuery: true, all: true},
		func(message string) error { return errors.New(message) })
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, hit := range matches.hits {
		names = append(names, strings.SplitN(hit.id, "@", 2)[0])
	}
	return names
}

func TestRipgrepPrefilterContract(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake rg is a shell script")
	}
	dir := t.TempDir()
	hlArchive(t, filepath.Join(dir, "a.jsonl"), fxLine(hlMsg("in-a", nil, "", hlUser("a needle"))))
	hlArchive(t, filepath.Join(dir, "b.jsonl"), fxLine(hlMsg("in-b", nil, "", hlUser("b needle"))))
	hlTouch(t, filepath.Join(dir, "a.jsonl"), 2_000)
	hlTouch(t, filepath.Join(dir, "b.jsonl"), 1_000)
	t.Setenv("PIG_EXT_HOST_INFO", "")

	t.Run("arguments and working directory", func(t *testing.T) {
		bin, log := t.TempDir(), filepath.Join(t.TempDir(), "args.log")
		hlFakeRG(t, bin, `pwd > "`+log+`"; printf '%s\n' "$@" >> "`+log+`"; exit 1`)
		t.Setenv("PATH", bin)
		if got := hlHitSources(t, dir); len(got) != 0 {
			t.Errorf("exit 1 means no file matches, got hits %v", got)
		}
		data, err := os.ReadFile(log)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"--files-with-matches", "--null", "--fixed-strings", "--ignore-case", "--no-messages", "--regexp", "needle", "--", "a.jsonl", "b.jsonl"}
		got := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
		if !strings.HasSuffix(got[0], filepath.Base(dir)) || !equalStrings(got[1:], want) {
			t.Errorf("rg invoked as %q, want cwd %s and %q", got, dir, want)
		}
	})

	t.Run("only the listed files are searched", func(t *testing.T) {
		bin := t.TempDir()
		hlFakeRG(t, bin, `printf 'a.jsonl\0'`)
		t.Setenv("PATH", bin)
		if got := hlHitSources(t, dir); !equalStrings(got, []string{"in-a"}) {
			t.Errorf("hits = %v, want only in-a", got)
		}
	})

	t.Run("a failing run keeps every file a candidate", func(t *testing.T) {
		bin := t.TempDir()
		hlFakeRG(t, bin, `printf 'a.jsonl\0'; exit 2`)
		t.Setenv("PATH", bin)
		if got := hlHitSources(t, dir); !equalStrings(got, []string{"in-a", "in-b"}) {
			t.Errorf("hits = %v, want both files", got)
		}
	})

	t.Run("the managed binary of the agent directory comes before PATH", func(t *testing.T) {
		agent, bin := t.TempDir(), t.TempDir()
		hlFakeRG(t, filepath.Join(agent, "bin"), `printf 'b.jsonl\0'`)
		hlFakeRG(t, bin, `printf 'a.jsonl\0'`)
		t.Setenv("PATH", bin)
		t.Setenv("PIG_EXT_HOST_INFO", `{"agentDir":`+fxStringify(agent)+`}`)
		if got := hlHitSources(t, dir); !equalStrings(got, []string{"in-b"}) {
			t.Errorf("hits = %v, want only in-b from the managed binary", got)
		}
	})

	t.Run("a missing managed binary falls back to PATH", func(t *testing.T) {
		agent, bin := t.TempDir(), t.TempDir()
		hlFakeRG(t, bin, `printf 'a.jsonl\0'`)
		t.Setenv("PATH", bin)
		t.Setenv("PIG_EXT_HOST_INFO", `{"agentDir":`+fxStringify(agent)+`}`)
		if got := hlHitSources(t, dir); !equalStrings(got, []string{"in-a"}) {
			t.Errorf("hits = %v, want only in-a from PATH", got)
		}
	})

	t.Run("no ripgrep at all scans every file", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		t.Setenv("PIG_EXT_HOST_INFO", `{"agentDir":`+fxStringify(t.TempDir())+`}`)
		if got := hlHitSources(t, dir); !equalStrings(got, []string{"in-a", "in-b"}) {
			t.Errorf("hits = %v, want both files", got)
		}
	})

	t.Run("an aborted search stops with the reason", func(t *testing.T) {
		bin := t.TempDir()
		hlFakeRG(t, bin, `sleep 5`)
		t.Setenv("PATH", bin)
		reason := errors.New("stop now")
		ctx, cancel := context.WithCancelCause(context.Background())
		cancel(reason)
		_, err := historyMatches(ctx, hlSource(t, dir, ""),
			historyParams{op: "search", query: "needle", hasQuery: true, all: true},
			func(message string) error { return errors.New(message) })
		if !errors.Is(err, reason) {
			t.Errorf("err = %v, want the abort reason", err)
		}
	})
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
