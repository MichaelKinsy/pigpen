package powerline_footer

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Twins of tests/welcome.test.ts: the pure functions (the lifecycle twins are in welcome_lifecycle_test.go, which drives the
// extension through the fake host).

var loadedCommandsFixture = []slashCommand{
	{"user-prompt", "prompt"}, {"project-prompt", "prompt"}, {"package-prompt", "prompt"},
	{"user-skill", "skill"}, {"project-skill", "skill"}, {"package-skill", "skill"},
}

func welcomeText(lines []string) string { return stripAnsi(strings.Join(lines, "\n")) }

// withWelcomeHome is withTemporaryHome: HOME is a new directory and PI_CODING_AGENT_DIR is unset.
func withWelcomeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	setenv(t, "PI_CODING_AGENT_DIR", nil)
	return home
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func recentNames(t *testing.T, max int) []string {
	t.Helper()
	got, err := getRecentSessions(context.Background(), max)
	if err != nil {
		t.Fatalf("getRecentSessions: %v", err)
	}
	names := []string{}
	for _, s := range got {
		names = append(names, s.Name)
	}
	return names
}

func contains1(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func TestWelcomePureTwins(t *testing.T) {
	tw(t, "welcome", "countLoadedCommands counts effective prompts and skills from every origin", func(t *testing.T) {
		skills, prompts := countLoadedCommands(loadedCommandsFixture)
		eq(t, [2]int{skills, prompts}, [2]int{3, 3}, "skills and prompts")
	})

	tw(t, "welcome", "extension commands are not presented as loaded extensions", func(t *testing.T) {
		ext := []slashCommand{{"one", "extension"}, {"two", "extension"}, {"three", "extension"}}
		skills, prompts := countLoadedCommands(append(append([]slashCommand{}, loadedCommandsFixture...), ext...))
		eq(t, [2]int{skills, prompts}, [2]int{3, 3}, "extension commands are not counted")
		s, p := countLoadedCommands(ext)
		rendered := welcomeText(newWelcomeHeader("Model", "Provider", nil, loadedCounts{0, s, p}, nil).render(96))
		if strings.Contains(strings.ToLower(rendered), "extension") {
			t.Errorf("the header mentions extensions:\n%s", rendered)
		}
	})

	tw(t, "welcome", "welcome renders the initial system prompt token estimate", func(t *testing.T) {
		counts := loadedCounts{1, 1, 1}
		tokens := 1900.0
		rendered := welcomeText(newWelcomeHeader("Model", "Provider", nil, counts, &tokens).render(96))
		if !strings.Contains(rendered, "≈ 1.9k initial prompt tokens") {
			t.Errorf("no estimate in:\n%s", rendered)
		}
		zero, nan := 0.0, math.NaN()
		for name, tok := range map[string]*float64{"undefined": nil, "zero": &zero, "NaN": &nan} {
			out := welcomeText(newWelcomeHeader("Model", "Provider", nil, counts, tok).render(96))
			if strings.Contains(out, "initial prompt tokens") {
				t.Errorf("%s: an estimate is drawn:\n%s", name, out)
			}
		}
	})

	tw(t, "welcome", "getRecentSessions prefers cwd basename from session header", func(t *testing.T) {
		home := withWelcomeHome(t)
		writeFile(t, filepath.Join(home, ".pi", "agent", "sessions", "--Users-nico-dev-encoded-name--", "session.jsonl"),
			`{"cwd":"/Users/nico/dev/my-dashed-project"}`+"\n")
		eq(t, recentNames(t, 1), []string{"my-dashed-project"}, "name")
	})

	tw(t, "welcome", "getRecentSessions falls back to encoded directory when header cwd is unusable", func(t *testing.T) {
		home := withWelcomeHome(t)
		root := filepath.Join(home, ".pi", "agent", "sessions")
		for name, content := range map[string]string{
			"invalid-json": "not-json\n", "missing-cwd": `{"type":"session"}` + "\n", "non-string-cwd": `{"cwd":123}` + "\n",
		} {
			writeFile(t, filepath.Join(root, "--Users-nico-dev-"+name+"--", "session.jsonl"), content)
		}
		names := recentNames(t, 10)
		if !contains1(names, "json") || !contains1(names, "cwd") {
			t.Errorf("names = %q, want json and cwd among them", names)
		}
	})

	tw(t, "welcome", "welcome context discovery respects PI_CODING_AGENT_DIR", func(t *testing.T) {
		home := withWelcomeHome(t)
		root := t.TempDir()
		project, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent-dir")
		t.Setenv("PI_CODING_AGENT_DIR", agentDir)
		for _, d := range []string{project, agentDir, filepath.Join(home, ".pi", "agent")} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		writeFile(t, filepath.Join(agentDir, "AGENTS.md"), "# Agent instructions\n")
		writeFile(t, filepath.Join(home, ".pi", "agent", "AGENTS.md"), "# Should not count\n")
		t.Chdir(project)
		eq(t, discoverLoadedCounts(loadedCommandsFixture), loadedCounts{1, 3, 3}, "counts")
	})

	tw(t, "welcome", "getRecentSessions reads custom agent sessions and existing legacy sessions", func(t *testing.T) {
		home := withWelcomeHome(t)
		agentDir := filepath.Join(t.TempDir(), "agent-dir")
		t.Setenv("PI_CODING_AGENT_DIR", agentDir)
		writeFile(t, filepath.Join(agentDir, "sessions", "--custom--", "session.jsonl"), `{"cwd":"/tmp/custom-project"}`+"\n")
		writeFile(t, filepath.Join(home, ".pi", "sessions", "--legacy--", "session.jsonl"), `{"cwd":"/tmp/legacy-project"}`+"\n")
		names := recentNames(t, 10)
		if !contains1(names, "custom-project") || !contains1(names, "legacy-project") {
			t.Errorf("names = %q", names)
		}
	})

	tw(t, "welcome", "getRecentSessions selects newest distinct projects across a large nested archive", func(t *testing.T) {
		home := withWelcomeHome(t)
		nested := filepath.Join(home, ".pi", "agent", "sessions", "--encoded--", "artifacts", "nested")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		for i := range 1000 {
			file := filepath.Join(nested, jsNumber(float64(i))+".jsonl")
			writeFile(t, file, `{"cwd":"/projects/older"}`+"\n")
			old := now.Add(-86_400_000 * time.Millisecond)
			if err := os.Chtimes(file, old, old); err != nil {
				t.Fatal(err)
			}
		}
		for _, r := range []struct {
			name, header string
			age          time.Duration
		}{
			{"a.jsonl", `{"cwd":"/projects/my-dashed-project"}`, 60 * time.Second},
			{"b.jsonl", `{"cwd":"/other/my-dashed-project"}`, 120 * time.Second},
			{"c.jsonl", `not-json`, 180 * time.Second},
			{"d.jsonl", `{"cwd":"/projects/third"}`, 240 * time.Second},
		} {
			file := filepath.Join(nested, r.name)
			writeFile(t, file, r.header+"\n"+strings.Repeat("ignored body", 1000))
			if err := os.Chtimes(file, now.Add(-r.age), now.Add(-r.age)); err != nil {
				t.Fatal(err)
			}
		}
		got, err := getRecentSessions(context.Background(), 3)
		if err != nil {
			t.Fatal(err)
		}
		eq(t, got, []recentSession{{"my-dashed-project", "1m ago"}, {"nested", "3m ago"}, {"third", "4m ago"}}, "sessions")
	})

	tw(t, "welcome", "getRecentSessions rejects pre-aborted and in-flight discovery without partial results", func(t *testing.T) {
		home := withWelcomeHome(t)
		root := filepath.Join(home, ".pi", "agent", "sessions")
		for i := range 100 {
			writeFile(t, filepath.Join(root, jsNumber(float64(i))+".jsonl"), `{"cwd":"/projects/`+jsNumber(float64(i))+`"}`+"\n")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		got, err := getRecentSessions(ctx, 3)
		if !errors.Is(err, context.Canceled) || got != nil {
			t.Errorf("pre-aborted: got %v, %v; want no result and context.Canceled", got, err)
		}

		running, abort := context.WithCancel(context.Background())
		defer abort()
		// The abort arrives while discovery is in flight: after the directory listing, at the first file it inspects.
		operations := 0
		welcomeFSHook = func(op, _ string) {
			operations++
			if op == "stat" {
				abort()
			}
		}
		t.Cleanup(func() { welcomeFSHook = nil })
		got, err = getRecentSessions(running, 3)
		if !errors.Is(err, context.Canceled) || got != nil {
			t.Errorf("in-flight: got %v, %v; want no result and context.Canceled", got, err)
		}
		if operations == 0 {
			t.Error("the hook never ran: discovery was not in flight")
		}
		welcomeFSHook = nil
		if err := os.RemoveAll(root); err != nil {
			t.Fatal(err)
		}
		eq(t, recentNames(t, 3), []string{}, "after the sessions are gone")
	})

	tw(t, "welcome", "getRecentSessions follows nested directory links without looping", func(t *testing.T) {
		home := withWelcomeHome(t)
		root := filepath.Join(home, ".pi", "agent", "sessions")
		archive := filepath.Join(home, "archive")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(archive, "session.jsonl"), `{"cwd":"/projects/linked"}`+"\n")
		if err := os.Symlink(archive, filepath.Join(root, "linked")); err != nil {
			t.Skipf("no symbolic links here: %v", err)
		}
		if err := os.Symlink(root, filepath.Join(archive, "cycle")); err != nil {
			t.Fatal(err)
		}
		got, err := getRecentSessions(context.Background(), 3)
		if err != nil {
			t.Fatal(err)
		}
		eq(t, got, []recentSession{{"linked", "just now"}}, "sessions")
	})
}

// discoverLoadedCounts counts five context file locations; the original's test covers one.
func TestWelcomeCountsEveryContextFileLocation(t *testing.T) {
	home := withWelcomeHome(t)
	project := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(project)
	files := []string{
		filepath.Join(home, ".pi", "agent", "AGENTS.md"),
		filepath.Join(home, ".claude", "AGENTS.md"),
		filepath.Join(project, "AGENTS.md"),
		filepath.Join(project, ".pi", "AGENTS.md"),
		filepath.Join(project, ".claude", "AGENTS.md"),
	}
	eq(t, discoverLoadedCounts(nil), loadedCounts{}, "nothing yet")
	for i, f := range files {
		writeFile(t, f, "# instructions\n")
		eq(t, discoverLoadedCounts(nil), loadedCounts{ContextFiles: i + 1}, "after "+f)
	}
	// A directory of that name counts too: the original asks existsSync.
	eq(t, os.Remove(filepath.Join(project, "AGENTS.md")), nil, "remove")
	if err := os.Mkdir(filepath.Join(project, "AGENTS.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	eq(t, discoverLoadedCounts(nil).ContextFiles, 5, "a directory exists")
}

func TestWelcomeLeavesTheFirstRepeatOfALinkedDirectory(t *testing.T) {
	home := withWelcomeHome(t)
	root := filepath.Join(home, ".pi", "agent", "sessions")
	archive := filepath.Join(home, "archive")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(archive, "session.jsonl"), `{"cwd":"/projects/linked"}`+"\n")
	if err := os.Symlink(archive, filepath.Join(root, "linked")); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	if err := os.Symlink(root, filepath.Join(archive, "cycle")); err != nil {
		t.Fatal(err)
	}
	var realpaths []string
	welcomeFSHook = func(op, path string) {
		if op == "realpath" {
			realpaths = append(realpaths, path)
		}
	}
	t.Cleanup(func() { welcomeFSHook = nil })
	if _, err := getRecentSessions(context.Background(), 3); err != nil {
		t.Fatal(err)
	}
	// the sessions directory, the linked archive and the link back, which is the first repeat: the legacy directory does not exist
	eq(t, len(realpaths), 4, "directories resolved (agent sessions, linked, cycle, legacy)")
}

func TestWelcomeReadsNoMoreHeadersThanItNeeds(t *testing.T) {
	home := withWelcomeHome(t)
	root := filepath.Join(home, ".pi", "agent", "sessions")
	now := time.Now()
	for i := range 6 {
		file := filepath.Join(root, "d", "f"+jsNumber(float64(i))+".jsonl")
		writeFile(t, file, `{"cwd":"/projects/p`+jsNumber(float64(i))+`"}`+"\n")
		mt := now.Add(-time.Duration(i) * time.Minute)
		if err := os.Chtimes(file, mt, mt); err != nil {
			t.Fatal(err)
		}
	}
	var ops []string
	welcomeFSHook = func(op, path string) { ops = append(ops, op) }
	t.Cleanup(func() { welcomeFSHook = nil })
	count := func(op string) (n int) {
		for _, o := range ops {
			if o == op {
				n++
			}
		}
		return
	}
	if _, err := getRecentSessions(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	eq(t, count("open"), 2, "headers read for two projects")
	ops = nil
	got, err := getRecentSessions(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, got, []recentSession{}, "no sessions")
	eq(t, len(ops), 0, "no filesystem operation for a count of 0")
}

func TestShouldShowStartupWelcomeIsForTheTerminalOnly(t *testing.T) {
	for _, mode := range []string{"rpc", "json", "print", ""} {
		eq(t, shouldShowStartupWelcome("startup", true, mode), false, "mode "+mode)
	}
	eq(t, shouldShowStartupWelcome("new", true, "tui"), false, "new")
}
