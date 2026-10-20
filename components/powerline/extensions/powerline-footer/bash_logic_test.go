package powerline_footer

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Own tests of the bash mode logic beyond the upstream twins: every branch of history.ts, completion.ts, transcript.ts,
// shell-session.ts and the pure parts of editor.ts.

// A fake git for the git-ref completions: the test binary re-executed under the name "git" (the skill's fake external CLI).
func init() {
	if os.Getenv("PL_BASH_FAKE_GIT") == "" {
		return
	}
	if log := os.Getenv("PL_BASH_FAKE_GIT_LOG"); log != "" {
		if f, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			_, _ = f.WriteString(strings.Join(os.Args[1:], " ") + "\n")
			_ = f.Close()
		}
	}
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "branch":
			if n, err := strconv.Atoi(os.Getenv("PL_BASH_FAKE_GIT_BRANCHES")); err == nil {
				for i := 0; i < n; i++ {
					_, _ = os.Stdout.WriteString("many-" + strconv.Itoa(1000+i) + "\n")
				}
				os.Exit(0)
			}
			_, _ = os.Stdout.WriteString("main\n  feature\n\nfeature-two\n")
		case "tag":
			_, _ = os.Stdout.WriteString("v1\nfeature\n")
		}
	}
	os.Exit(0)
}

func useFakeGit(t *testing.T) (logPath string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake git is a symlink to the test binary")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Symlink(exe, filepath.Join(dir, "git")); err != nil {
		t.Fatal(err)
	}
	logPath = filepath.Join(dir, "calls.log")
	setenv(t, "PATH", s(dir))
	setenv(t, "PL_BASH_FAKE_GIT", s("1"))
	setenv(t, "PL_BASH_FAKE_GIT_LOG", s(logPath))
	return logPath
}

func TestBashProjectHistory(t *testing.T) {
	cwd, _ := bashTestEnv(t, nil)
	if got := readProjectHistory(cwd); len(got) != 0 {
		t.Fatalf("history of a new project = %v", got)
	}
	appendProjectHistory(cwd, "  ", cwd) // blank commands are ignored
	if _, err := os.Stat(projectHistoryPath(cwd)); err == nil {
		t.Fatal("a blank command created the history file")
	}
	now := int64(1_000)
	old := clock
	clock = func() int64 { now++; return now }
	t.Cleanup(func() { clock = old })
	appendProjectHistory(cwd, "git status", "/a")
	appendProjectHistory(cwd, "  git stash \n", "/b")
	appendProjectHistory(cwd, "git status", "/c")
	got := readProjectHistory(cwd)
	eq(t, got, []persistedHistoryEntry{
		{Command: "git status", Cwd: "/c", Timestamp: 1003},
		{Command: "git stash", Cwd: "/b", Timestamp: 1002},
	}, "newest first, repeated command moved, trimmed")

	raw, err := os.ReadFile(projectHistoryPath(cwd))
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"version\": 1,\n  \"entries\": [\n    {\n      \"command\": \"git status\",\n      \"cwd\": \"/c\",\n      \"timestamp\": 1003\n    },\n    {\n      \"command\": \"git stash\",\n      \"cwd\": \"/b\",\n      \"timestamp\": 1002\n    }\n  ]\n}\n"
	eq(t, string(raw), want, "file text")

	for i := 0; i < 600; i++ {
		appendProjectHistory(cwd, "cmd "+bashItoa(i), cwd)
		if want := min(i+3, 500); len(readProjectHistory(cwd)) != want { // two commands were there before the loop, "cmd 0" makes three
			t.Fatalf("after %d more appends: %d entries, want %d", i+1, len(readProjectHistory(cwd)), want)
		}
	}
	eq(t, len(readProjectHistory(cwd)), 500, "history is capped at 500")
	eq(t, readProjectHistory(cwd)[0].Command, "cmd 599", "newest of the cap")
}

func TestBashProjectHistoryReadsAndCaches(t *testing.T) {
	cwd, _ := bashTestEnv(t, nil)
	path := projectHistoryPath(cwd)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var debug []string
	old := bashDebug
	bashDebug = func(format string, args ...any) { debug = append(debug, format) }
	t.Cleanup(func() { bashDebug = old })

	write(`{"version":1,"entries":[
	  {"command":" old ","cwd":" /x ","timestamp":10},
	  {"command":"new","cwd":"/y","timestamp":30},
	  {"command":"","cwd":"/y","timestamp":40},
	  {"command":"nocwd","cwd":"","timestamp":40},
	  {"command":"zero","cwd":"/y","timestamp":0},
	  {"command":"str","cwd":"/y","timestamp":"5"},
	  {"cwd":"/y","timestamp":5},
	  [1], null, 7,
	  {"command":"mid","cwd":"/y","timestamp":20}]}`)
	got := readProjectHistory(cwd)
	eq(t, got, []persistedHistoryEntry{{"new", "/y", 30}, {"mid", "/y", 20}, {"old", "/x", 10}}, "normalized and sorted newest first")

	write(`[]`)
	eq(t, len(readProjectHistory(cwd)), 0, "an array document")
	write(`{"entries":"no"}`)
	eq(t, len(readProjectHistory(cwd)), 0, "entries that are not an array")
	eq(t, len(debug), 0, "no diagnostics for valid documents")
	write(`{not json`)
	eq(t, len(readProjectHistory(cwd)), 0, "malformed json")
	eq(t, len(debug), 1, "malformed json is reported once")

	// The cache serves an unchanged file and notices a changed one.
	write(`{"entries":[{"command":"a","cwd":"/","timestamp":1}]}`)
	eq(t, readProjectHistory(cwd)[0].Command, "a", "first read")
	write(`{"entries":[{"command":"bb","cwd":"/","timestamp":1}]}`) // different size
	eq(t, readProjectHistory(cwd)[0].Command, "bb", "changed file is read again")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	eq(t, len(readProjectHistory(cwd)), 0, "a removed file")
}

func TestBashProjectKey(t *testing.T) {
	for cwd, want := range map[string]string{
		"/Users/me/proj": "Users-me-proj", "/": "root", "": "root", `C:\work\app`: "C:-work-app", "//a//b//": "a-b", "rel/dir/": "rel-dir",
	} {
		eq(t, bashProjectKey(cwd), want, "bashProjectKey("+cwd+")")
	}
}

func TestBashGlobalHistoryFormats(t *testing.T) {
	home := t.TempDir()
	setenv(t, "HOME", s(home))
	setenv(t, "USERPROFILE", s(home))
	setenv(t, "HISTFILE", nil)
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	eq(t, readGlobalShellHistory("/usr/bin/fish"), []string(nil), "no history file")

	write(".bash_history", "  ls  \n\ncd /tmp\r\n")
	eq(t, readGlobalShellHistory("/bin/bash"), []string{"cd /tmp", "ls"}, "bash: trimmed, blank lines dropped, newest first")
	eq(t, readGlobalShellHistory("/bin/sh"), []string{"cd /tmp", "ls"}, "an unknown shell reads .bash_history")

	write(".zsh_history", ": 1:0;echo a;b\n:broken\n: 2:0;  \nplain\n")
	eq(t, readGlobalShellHistory("/usr/bin/ZSH"), []string{"plain", "echo a;b"}, "zsh: the command keeps its own semicolons; a lone ':' line has none")

	write(".local/share/fish/fish_history", "- cmd: git status\n  when: 1\n- cmd:   ls -la  \n  when: 2\n  - cmd: nested\n- cmd:\n- other: x\n")
	// The \s after "cmd:" crosses the line break, so an empty "- cmd:" takes the next line as its command (the original does too).
	fishWant := []string{"- other: x", "nested", "ls -la", "git status"}
	eq(t, readGlobalShellHistory("/usr/bin/fish"), fishWant, "fish: cmd entries, newest first")

	// HISTFILE wins for bash and zsh, not for fish.
	other := filepath.Join(home, "elsewhere")
	if err := os.WriteFile(other, []byte("from histfile\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	setenv(t, "HISTFILE", s(other))
	eq(t, readGlobalShellHistory("/bin/zsh"), []string{"from histfile"}, "HISTFILE for zsh")
	eq(t, readGlobalShellHistory("/usr/bin/fish"), fishWant, "fish ignores HISTFILE")
}

func TestBashMatchHistoryEntries(t *testing.T) {
	entries := []string{" git a ", "git b", "git a", "", "ls", "git c"}
	eq(t, matchHistoryEntries(entries, " git ", 2), []string{"git a", "git b"}, "prefix is trimmed, entries deduplicated, limit applies")
	eq(t, matchHistoryEntries(entries, "x", 5), []string{}, "no match")
	eq(t, matchHistoryEntries(nil, "", 5), []string{}, "no entries")
}

func TestBashTranscriptStore(t *testing.T) {
	store := newBashTranscriptStore(bashTranscriptLimits{MaxLines: 100, MaxBytes: 20})
	record := store.startCommand("a", "echo", "/w")
	eq(t, record.CwdAtStart, "/w", "cwd at start")
	if record.ExitCode != nil || record.FinishedAt != nil {
		t.Fatal("a new command has no exit code")
	}
	store.appendOutput("missing", "ignored")
	store.appendOutput("a", "")
	store.appendOutput("a", "h\u00e9\r\n\n") // 2 bytes for é, CR removed: "hé", "", ""
	snap := store.getSnapshot()
	eq(t, snap.Commands[0].Output, []string{"h\u00e9", "", ""}, "lines")
	eq(t, snap.Commands[0].OutputBytes, 4+1+1, "bytes: UTF-8 length plus a break per line")
	eq(t, snap.TotalLines, 3, "total lines")
	store.appendOutput("a", "\r")
	eq(t, store.getSnapshot().Commands[0].Output[3], "", "a chunk of only a carriage return is one empty line")

	// A snapshot is a copy.
	snap = store.getSnapshot()
	snap.Commands[0].Output[0] = "changed"
	eq(t, store.getSnapshot().Commands[0].Output[0], "h\u00e9", "snapshot output copy")

	store.finishCommand("missing", 1)
	store.finishCommand("a", 3)
	rec := store.getSnapshot().Commands[0]
	if rec.ExitCode == nil || *rec.ExitCode != 3 || rec.FinishedAt == nil {
		t.Fatalf("finished record = %+v", rec)
	}

	// Byte limit: the oldest commands go until the newest alone remains.
	store.startCommand("b", "second", "/w")
	store.appendOutput("b", "0123456789012345678901234")
	snap = store.getSnapshot()
	eq(t, len(snap.Commands), 1, "commands after exceeding the byte limit")
	eq(t, snap.Commands[0].ID, "b", "kept")
	eq(t, snap.TruncatedCommands, 1, "truncated count")
	eq(t, snap.TotalBytes, 26, "bytes of the kept command")
	store.appendOutput("a", "late output for a dropped command")
	eq(t, store.getSnapshot().TotalBytes, 26, "a dropped command takes no output")

	store.clear()
	eq(t, store.getSnapshot(), bashTranscriptSnapshot{Commands: []bashCommandRecord{}}, "cleared")
}

func TestBashTokenizer(t *testing.T) {
	eq(t, tokenizeBeforeCursor(`git commit -m "a b" c\ d 'e f'`), []string{"git", "commit", "-m", `"a b"`, `c\ d`, `'e f'`}, "quotes and escapes keep their text")
	eq(t, tokenizeBeforeCursor(`  a   b `), []string{"a", "b"}, "runs of blanks")
	eq(t, tokenizeBeforeCursor("a\u00a0b\u3000c"), []string{"a", "b", "c"}, "JavaScript whitespace splits")
	eq(t, tokenizeBeforeCursor(`echo "open`), []string{"echo", `"open`}, "an open quote runs to the end")
	tc := getTokenContext("git checkout ma", 15)
	eq(t, []any{tc.token, tc.tokenStart, tc.tokenEnd, tc.tokenIndex}, []any{"ma", 13, 15, 2}, "token context at the end")
	tc = getTokenContext("git  che", 4)
	eq(t, []any{tc.token, tc.tokenStart, tc.tokenEnd, tc.tokenIndex, tc.afterCursor}, []any{"", 4, 4, 0, " che"}, "cursor in blanks")
	tc = getTokenContext("cd d\u00e9v rest", len("cd d\u00e9"))
	eq(t, []any{tc.token, tc.tokenStart, tc.tokenEnd}, []any{"d\u00e9v", 3, len("cd d\u00e9v")}, "token extends to the right of the cursor")
}

func TestBashCompareLocale(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"a", "A", -1}, {"A", "b", -1}, {"B", "a", 1}, {"a", "a", 0}, {"a", "ab", -1}, {"_", "a", -1}, {"1", "a", -1},
		{"a-b", "a b", 1}, {"My Folder/", "dev/", 1}, {"dev/", "Dev/", -1}, {"z", "\u00e9", 1},
		{"\u00e9", "f", -1}, {"e", "\u00e9", -1}, {"\u00e9", "E", 1}, {"\u00e9", "\u00c9", -1}, {"\u00c9", "\u00e9", 1},
	}
	for _, c := range cases {
		if got := compareLocale(c.a, c.b); got != c.want {
			t.Errorf("compareLocale(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
	// Recorded from Node 24 (ICU): ["x","b2","B","a_b","a-b","a.b","a b","aB","ab","Ab","a1","a$","a~"].sort(localeCompare).
	order := []string{"a b", "a_b", "a-b", "a.b", "a~", "a$", "a1", "ab", "aB", "Ab", "B", "b2", "x"}
	for i := 0; i+1 < len(order); i++ {
		if compareLocale(order[i], order[i+1]) >= 0 {
			t.Errorf("%q must sort before %q", order[i], order[i+1])
		}
	}
}

func TestBashUniqueByReplacement(t *testing.T) {
	in := []extendedCompletionItem{
		{Label: "b", Replacement: "b", Score: 40, StartCol: 1, EndCol: 2},
		{Label: "a", Replacement: "a", Score: 40, StartCol: 1, EndCol: 2},
		{Label: "b", Replacement: "b", Score: 50, StartCol: 1, EndCol: 2, Source: "git"},
		{Label: "b", Replacement: "b", Score: 41, StartCol: 2, EndCol: 2},
		{Label: "c", Replacement: "c", Score: 50, StartCol: 1, EndCol: 2},
	}
	got := uniqueByReplacement(in)
	var order []string
	for _, item := range got {
		order = append(order, item.Label+":"+bashItoa(item.Score)+":"+item.Source)
	}
	eq(t, order, []string{"b:50:git", "c:50:", "b:41:", "a:40:"}, "best per range and replacement, by score then label")
}

func TestBashPathSuggestions(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	setenv(t, "HOME", s(home))
	for _, d := range []string{"src", "scripts", "My Dir"} {
		bashMustMkdir(t, filepath.Join(cwd, d))
	}
	for _, f := range []string{"setup.sh", "$weird&name", ".hidden"} {
		if err := os.WriteFile(filepath.Join(cwd, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bashMustMkdir(t, filepath.Join(home, "docs"))
	bashMustMkdir(t, filepath.Join(cwd, "src", "inner"))

	type row struct {
		Label, Replacement string
		Score              int
	}
	rows := func(token string) []row {
		var out []row
		for _, item := range getPathSuggestions(token, cwd) {
			out = append(out, row{item.Label, item.Replacement, item.Score})
		}
		return out
	}
	eq(t, getPathSuggestions("", cwd), []extendedCompletionItem(nil), "empty token")
	eq(t, rows("s"), []row{{"scripts/", "scripts/", 44}, {"setup.sh", "setup.sh", 40}, {"src/", "src/", 44}}, "prefix s, in directory order")
	eq(t, rows("M"), []row{{"My Dir/", `My\ Dir/`, 44}}, "escaped space")
	eq(t, rows("$"), []row{{"$weird&name", `\$weird\&name`, 40}}, "escaped shell characters")
	eq(t, rows("."), []row{{".hidden", ".hidden", 40}}, "hidden entries need a dot")
	eq(t, rows("src/"), []row{{"src/inner/", "src/inner/", 44}}, "inside a directory")
	eq(t, rows("src/i"), []row{{"src/inner/", "src/inner/", 44}}, "inside a directory with a prefix")
	eq(t, rows("~/d"), []row{{"~/docs/", "~/docs/", 44}}, "home expansion keeps the typed prefix")
	eq(t, rows("nothere/x"), []row(nil), "a missing directory")
	abs := getPathSuggestions(filepath.ToSlash(cwd)+"/src/", "/unused")
	eq(t, len(abs), 1, "absolute directory")
	eq(t, abs[0].Label, filepath.ToSlash(cwd)+"/src/inner/", "absolute label")

	big := t.TempDir()
	for i := 0; i < 130; i++ {
		if err := os.WriteFile(filepath.Join(big, "f"+bashItoa(1000+i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	eq(t, len(getPathSuggestions("f", big)), 100, "at most 100 entries")
}

func TestBashEscapeShellPath(t *testing.T) {
	eq(t, escapeShellPath("a b\"c'd`e$f&g|h;i<j>k(l)m[n]o{p}q?r!s*t\\u"), "a\\ b\\\"c\\'d\\`e\\$f\\&g\\|h\\;i\\<j\\>k\\(l\\)m\\[n\\]o\\{p\\}q\\?r\\!s\\*t\\\\u", "specials")
	eq(t, escapeShellPath("plain-name_1.txt"), "plain-name_1.txt", "nothing to escape")
	eq(t, escapeShellPath("a\u00a0b"), "a\\\u00a0b", "JavaScript whitespace")
}

func TestBashJSPathJoin(t *testing.T) {
	eq(t, jsPathJoin("/home/u", "dev/"), "/home/u/dev/", "trailing slash kept")
	eq(t, jsPathJoin("/home/u/", "a/../b"), "/home/u/b", "normalized")
	eq(t, jsPathJoin("", "x"), "x", "empty base")
	eq(t, jsPathJoin("", ""), ".", "nothing")
	eq(t, jsPathJoin("/", ""), "/", "root")
}

func TestBashGitCompletions(t *testing.T) {
	logPath := useFakeGit(t)
	histfile := ""
	cwd, _ := bashTestEnv(t, &histfile)
	bashWantGhost(t, bashGhost(t, "git checkout fe", cwd, "/bin/zsh"), "git checkout feature", "git")
	bashWantGhost(t, bashGhost(t, "git checkout m", cwd, "/bin/zsh"), "git checkout main", "git")
	calls, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"branch --format=%(refname:short)", "tag --list"} {
		if !strings.Contains(string(calls), want) {
			t.Errorf("git calls %q lack %q", calls, want)
		}
	}

	// Only the ref-taking subcommands ask git; others complete from the file system alone.
	before := len(calls)
	eq(t, bashGhost(t, "git commit fe", cwd, "/bin/zsh"), (*ghostSuggestion)(nil), "git commit has no ref completion")
	after, _ := os.ReadFile(logPath)
	eq(t, len(after), before, "git was not asked for commit")

	// The refs go through tokenContext: the subcommand list is offered at the second word, filtered by prefix.
	bashWantGhost(t, bashGhost(t, "git ch", cwd, "/bin/zsh"), "git checkout", "git")
	// Upstream quirk kept: with a trailing space the token is empty and tokenIndex still 1, so subcommands are offered.
	bashWantGhost(t, bashGhost(t, "git checkout ", cwd, "/bin/zsh"), "git checkout add", "git")

	// A cancelled context yields nothing.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	eq(t, (&bashCompletionEngine{}).getGhostSuggestion(ctx, "git checkout fe", cwd, "/bin/zsh"), (*ghostSuggestion)(nil), "cancelled")

	// A ref beats a path of the same prefix (git 50, file 40, directory 44).
	bashMustMkdir(t, filepath.Join(cwd, "fe-dir"))
	bashWantGhost(t, bashGhost(t, "git checkout fe", cwd, "/bin/zsh"), "git checkout feature", "git")

	// At most 100 refs are offered.
	setenv(t, "PL_BASH_FAKE_GIT_BRANCHES", s("150"))
	tc := getTokenContext("git checkout many", len("git checkout many"))
	eq(t, len(getGitSuggestions(context.Background(), tc, cwd)), 100, "ref cap")

	// Not a git repository state: no git at all.
	setenv(t, "PATH", s(t.TempDir()))
	bashWantGhost(t, bashGhost(t, "git checkout fe", cwd, "/bin/zsh"), "git checkout fe-dir/", "path") // only the directory is left
}

func TestBashGhostEdgeCases(t *testing.T) {
	cwd, _ := bashTestEnv(t, nil)
	appendProjectHistory(cwd, "docker ps", cwd)
	bashWantGhost(t, bashGhost(t, "   ", cwd, "/bin/zsh"), "   docker ps", "project-history")
	bashWantGhost(t, bashGhost(t, "docker", cwd, "/bin/zsh"), "docker ps", "project-history")
	// Argument position: project history beats everything, a quoted history entry does not match a shorter prefix.
	bashWantGhost(t, bashGhost(t, "docker p", cwd, "/bin/zsh"), "docker ps", "project-history")
	eq(t, bashGhost(t, "nomatch zz", cwd, "/bin/zsh"), (*ghostSuggestion)(nil), "no deterministic candidate")
	// A path candidate equal to the typed text is no ghost.
	bashMustMkdir(t, filepath.Join(cwd, "dev"))
	eq(t, bashGhost(t, "cd dev/", cwd, "/bin/zsh"), (*ghostSuggestion)(nil), "nothing left to complete")
	if err := os.WriteFile(filepath.Join(cwd, "x.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	eq(t, bashGhost(t, "cat x.txt", cwd, "/bin/zsh"), (*ghostSuggestion)(nil), "a candidate equal to the typed word is no ghost")
	// Command position falls back to the curated list only for the stems.
	bashWantGhost(t, bashGhost(t, "cd", cwd, "/bin/zsh"), "cd ..", "path")
	bashWantGhost(t, bashGhost(t, "  gi", cwd, "/bin/zsh"), "git status", "git")
}

func TestBashOneOffContextShouldTrigger(t *testing.T) {
	p := newOneOffBashAutocompleteProvider()
	for _, c := range []struct {
		lines       []string
		line, col   int
		want        bool
		description string
	}{
		{[]string{"!git"}, 0, 1, true, "right behind a single bang"},
		{[]string{"!!git"}, 0, 1, false, "between the bangs"},
		{[]string{"!!git"}, 0, 2, true, "behind a double bang"},
		{[]string{"hello", "!git"}, 1, 4, false, "second line"},
		{[]string{"!git", "x"}, 1, 1, false, "second line below a bang line"},
		{[]string{"git"}, 0, 3, false, "no bang"},
		{[]string{}, 0, 0, false, "no lines"},
	} {
		got, err := p.ShouldTriggerFileCompletion(sdk.Context{}, c.lines, c.line, c.col)
		if err != nil || got != c.want {
			t.Errorf("%s: trigger = %v, %v, want %v", c.description, got, err, c.want)
		}
	}
}

func TestBashApplyExtendedCompletion(t *testing.T) {
	item := extendedCompletionItem{Replacement: "dev/", StartCol: 3, EndCol: 4}
	eq(t, applyExtendedCompletion([]string{"cd d"}, 0, item), sdk.AutocompleteCompletion{Lines: []string{"cd dev/"}, CursorLine: 0, CursorCol: 7}, "plain")
	// Columns count UTF-16 units: the emoji is two.
	eq(t, applyExtendedCompletion([]string{"\U0001F642 d"}, 0, extendedCompletionItem{Replacement: "dev", StartCol: 3, EndCol: 4}),
		sdk.AutocompleteCompletion{Lines: []string{"\U0001F642 dev"}, CursorLine: 0, CursorCol: 6}, "UTF-16 columns")
	// The range is clamped to the line.
	eq(t, applyExtendedCompletion([]string{"ab"}, 0, extendedCompletionItem{Replacement: "X", StartCol: 9, EndCol: 12}),
		sdk.AutocompleteCompletion{Lines: []string{"abX"}, CursorLine: 0, CursorCol: 3}, "clamped")
	eq(t, applyExtendedCompletion([]string{"abcdef"}, 0, extendedCompletionItem{Replacement: "X", StartCol: 4, EndCol: 2}),
		sdk.AutocompleteCompletion{Lines: []string{"abcdXef"}, CursorLine: 0, CursorCol: 5}, "an end before the start inserts")
	eq(t, applyExtendedCompletion([]string{"ab"}, 0, extendedCompletionItem{Replacement: "\u00e9\U0001F642", StartCol: 2, EndCol: 2}),
		sdk.AutocompleteCompletion{Lines: []string{"ab\u00e9\U0001F642"}, CursorLine: 0, CursorCol: 5}, "the cursor counts UTF-16 units of the replacement")
	eq(t, applyExtendedCompletion([]string{"ab"}, 0, extendedCompletionItem{Replacement: "X", StartCol: -5, EndCol: -1}),
		sdk.AutocompleteCompletion{Lines: []string{"Xab"}, CursorLine: 0, CursorCol: 1}, "negative")
	lines := []string{"one", "two"}
	out := applyExtendedCompletion(lines, 1, extendedCompletionItem{Replacement: "2", StartCol: 2, EndCol: 3})
	eq(t, lines, []string{"one", "two"}, "the input is not modified")
	eq(t, out.Lines, []string{"one", "tw2"}, "other lines kept")

	for name, p := range map[string]*sdk.AutocompleteProvider{"bash": newBashAutocompleteProvider()} {
		got, err := p.ShouldTriggerFileCompletion(sdk.Context{}, []string{"cd d"}, 0, 4)
		if err != nil || got {
			t.Errorf("%s provider triggers file completion: %v, %v", name, got, err)
		}
	}
	modeAware := newModeAwareAutocompleteProvider(nil, newBashAutocompleteProvider(), newOneOffBashAutocompleteProvider(), func() bool { return true })
	if got, err := modeAware.ShouldTriggerFileCompletion(sdk.Context{}, []string{"cd d"}, 0, 4); err != nil || got {
		t.Errorf("in bash mode the bash provider decides: %v, %v", got, err)
	}

	// Through the SDK shape an item cannot carry a range: both providers reject it as upstream does a foreign item.
	_, err := newBashAutocompleteProvider().ApplyCompletion(sdk.Context{}, []string{"x"}, 0, 1, sdk.AutocompleteItem{Value: "v"}, "")
	if err == nil || err.Error() != "Expected an extended completion item for bash autocomplete" {
		t.Errorf("bash provider error = %v", err)
	}
	_, err = newOneOffBashAutocompleteProvider().ApplyCompletion(sdk.Context{}, []string{"x"}, 0, 1, sdk.AutocompleteItem{Value: "v"}, "")
	if err == nil || err.Error() != "Expected an extended completion item for one-off bash autocomplete" {
		t.Errorf("one-off provider error = %v", err)
	}
}

func TestBashModeAwareProvider(t *testing.T) {
	type calls struct{ suggest, apply, trigger int }
	named := func(name string, trigger *bool, c *calls, chars ...string) *sdk.AutocompleteProvider {
		p := &sdk.AutocompleteProvider{
			TriggerCharacters: chars,
			GetSuggestions: func(sdk.Context, []string, int, int, bool) (*sdk.AutocompleteSuggestions, error) {
				c.suggest++
				return &sdk.AutocompleteSuggestions{Prefix: name}, nil
			},
			ApplyCompletion: func(_ sdk.Context, lines []string, line, col int, _ sdk.AutocompleteItem, _ string) (sdk.AutocompleteCompletion, error) {
				c.apply++
				return sdk.AutocompleteCompletion{Lines: []string{name}, CursorLine: line, CursorCol: col}, nil
			},
		}
		if trigger != nil {
			want := *trigger
			p.ShouldTriggerFileCompletion = func(sdk.Context, []string, int, int) (bool, error) { c.trigger++; return want, nil }
		}
		return p
	}
	yes, no := true, false
	var def, bash, oneOff calls
	active := false
	provider := newModeAwareAutocompleteProvider(named("default", &no, &def, "@", "ab", "/"), named("bash", &no, &bash, "!", "@"), named("oneoff", nil, &oneOff), func() bool { return active })
	eq(t, provider.TriggerCharacters, []string{"@", "/", "!"}, "trigger characters: single characters, once each")

	prefix := func(lines []string) string {
		got, err := provider.GetSuggestions(sdk.Context{}, lines, 0, len(lines[0]), false)
		if err != nil || got == nil {
			t.Fatalf("suggestions = %v, %v", got, err)
		}
		return got.Prefix
	}
	eq(t, prefix([]string{"st"}), "default", "default outside bash mode")
	active = true
	eq(t, prefix([]string{"st"}), "bash", "bash provider in bash mode")
	active = false

	// The one-off provider here has no file-trigger hook, so upstream's supportsShouldTriggerFileCompletion is false.
	eq(t, prefix([]string{"!x"}), "default", "a one-off provider without the hook is not used")
	oneOffHook := named("oneoff", &yes, &oneOff)
	provider = newModeAwareAutocompleteProvider(named("default", &no, &def), named("bash", &no, &bash), oneOffHook, func() bool { return active })
	eq(t, prefix([]string{"!x"}), "oneoff", "one-off provider inside a bang command")
	eq(t, prefix([]string{"x"}), "oneoff", "the stub hook says yes for any line")

	// applyCompletion follows the same routing.
	apply := func() string {
		out, err := provider.ApplyCompletion(sdk.Context{}, []string{"x"}, 0, 1, sdk.AutocompleteItem{}, "")
		if err != nil {
			t.Fatal(err)
		}
		return out.Lines[0]
	}
	eq(t, apply(), "oneoff", "apply: one-off")
	active = true
	eq(t, apply(), "bash", "apply: bash")
	active = false

	noDefault := newModeAwareAutocompleteProvider(nil, newBashAutocompleteProvider(), newOneOffBashAutocompleteProvider(), func() bool { return false })
	got, err := noDefault.GetSuggestions(sdk.Context{}, []string{"x"}, 0, 1, false)
	if got != nil || err != nil {
		t.Errorf("no default provider: %v, %v", got, err)
	}
	out, err := noDefault.ApplyCompletion(sdk.Context{}, []string{"x"}, 0, 1, sdk.AutocompleteItem{}, "")
	eq(t, out, sdk.AutocompleteCompletion{Lines: []string{"x"}, CursorLine: 0, CursorCol: 1}, "no default provider: apply leaves the editor alone")
	if err != nil {
		t.Fatal(err)
	}

	// shouldTriggerFileCompletion routing.
	trig := func(p *sdk.AutocompleteProvider, lines []string, col int) bool {
		got, err := p.ShouldTriggerFileCompletion(sdk.Context{}, lines, 0, col)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	eq(t, trig(noDefault, []string{"x"}, 1), false, "no default provider, no one-off match")
	eq(t, trig(noDefault, []string{"!x"}, 1), true, "one-off match")
	active = false
	bashOn := true
	p2 := newModeAwareAutocompleteProvider(named("default", &yes, &def), &sdk.AutocompleteProvider{GetSuggestions: noSuggestions}, nil, func() bool { return bashOn })
	eq(t, trig(p2, []string{"x"}, 1), true, "bash mode with a provider lacking the hook triggers")
	bashOn = false
	eq(t, trig(p2, []string{"x"}, 1), true, "default provider's hook")
	p3 := newModeAwareAutocompleteProvider(&sdk.AutocompleteProvider{GetSuggestions: noSuggestions}, nil, nil, func() bool { return false })
	eq(t, trig(p3, []string{"x"}, 1), false, "default provider without the hook")
}

func TestBashStripShellAnsiAndQuote(t *testing.T) {
	eq(t, stripShellAnsi("\x1b[31mred\x1b[0m \x1b]0;title\x07x \x1b]8;;u\x1b\\y\x01\x7f\ttab\n"), "red x y\ttab\n", "CSI, OSC, controls")
	eq(t, stripShellAnsi("\x1b[?25h\x1b[1;2Hz"), "z", "CSI with private marker")
	eq(t, quoteShellArg("it's"), `'it'"'"'s'`, "single quote")
	eq(t, quoteShellArg("/tmp/x y"), `'/tmp/x y'`, "plain")
	for _, c := range []struct {
		in   string
		n    int
		okay bool
	}{{"0", 0, true}, {"12abc", 12, true}, {" -3", -3, true}, {"+7", 7, true}, {"", 0, false}, {"x", 0, false}, {"-", 0, false}} {
		n, ok := jsParseInt10(c.in)
		if n != c.n || ok != c.okay {
			t.Errorf("jsParseInt10(%q) = %d, %v, want %d, %v", c.in, n, ok, c.n, c.okay)
		}
	}
}

func TestBashShellInitScripts(t *testing.T) {
	for shell, mustHave := range map[string][]string{
		"fish": {"function __pi_eval", "source $__pi_file", "echo \"__PI_READY__:$PWD\""},
		"bash": {"__pi_eval() {", "source \"$__pi_file\"", "printf '%s:%s\\n' '__PI_READY__' \"$PWD\""},
		"zsh":  {"function __pi_eval() {", "builtin source \"$__pi_file\"", "print -r -- \"__PI_READY__:$PWD\""},
		"sh":   {"builtin source", "print -r --"},
	} {
		script := getShellInitScript(shell)
		for _, want := range mustHave {
			if !strings.Contains(script, want) {
				t.Errorf("%s init script lacks %q", shell, want)
			}
		}
		for _, marker := range []string{"__PI_CMD_START__", "__PI_CMD_DONE__"} {
			if !strings.Contains(script, marker) {
				t.Errorf("%s init script lacks %s", shell, marker)
			}
		}
	}
}

// A session fed output directly: the marker protocol without a process.
func TestBashShellSessionHandleChunk(t *testing.T) {
	store := newBashTranscriptStore(bashTranscriptLimits{MaxLines: 100, MaxBytes: 1 << 20})
	var changes, successes atomic.Int32
	var succeeded []string
	session, err := newManagedShellSession("/usr/bin/ZSH", "/start", store, func() { changes.Add(1) }, func(command, cwd string) {
		successes.Add(1)
		succeeded = append(succeeded, command+"@"+cwd)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.dispose()
	eq(t, session.State().ShellName, "zsh", "shell name is the lowercased base name")

	session.handleChunk("rc noise\n__PI_REA")
	eq(t, session.State().Ready, false, "output before the ready marker is dropped")
	session.handleChunk("DY__:/home/u\r\n")
	st := session.State()
	eq(t, []any{st.Ready, st.Cwd}, []any{true, "/home/u"}, "ready, with the shell's own cwd")

	// output with no current command is ignored
	session.handleChunk("stray\n")
	store.startCommand("cmd-1", "ls", "/home/u")
	session.handleChunk("__PI_CMD_START__:cmd-1:/home/work\n\x1b[32mfile\x1b[0m   \nsecond\n__PI_CMD_DONE__:cmd-1:0:/home/done\n")
	snap := store.getSnapshot()
	eq(t, snap.Commands[0].Output, []string{"file", "second"}, "output lines, ANSI stripped, right-trimmed")
	if snap.Commands[0].ExitCode == nil || *snap.Commands[0].ExitCode != 0 {
		t.Fatalf("exit code = %v", snap.Commands[0].ExitCode)
	}
	st = session.State()
	eq(t, []any{st.Running, st.Cwd, *st.LastExitCode}, []any{false, "/home/done", 0}, "state after the done marker")
	eq(t, succeeded, []string{"ls@/home/done"}, "success callback with the command and the new cwd")

	// A failing command and a garbled exit code.
	store.startCommand("cmd-2", "false", "/home/done")
	session.handleChunk("__PI_CMD_START__:cmd-2:/home/done\n__PI_CMD_DONE__:cmd-2:1:/home/done\n")
	eq(t, successes.Load(), int32(1), "no success callback for a non-zero exit")
	store.startCommand("cmd-3", "weird", "/home/done")
	session.handleChunk("__PI_CMD_START__:cmd-3:/home/done\n__PI_CMD_DONE__:cmd-3:oops:/x\n")
	eq(t, *session.State().LastExitCode, 1, "an unparsable exit code is 1")
	session.handleChunk("__PI_CMD_DONE__:cmd-3:12abc\n")
	eq(t, *session.State().LastExitCode, 12, "parseInt takes the leading digits")
	eq(t, session.State().Cwd, "/x", "a missing cwd field keeps the cwd")

	// The cwd after a colon is cut at the colon, as upstream splits on every ":".
	session.handleChunk("__PI_CMD_START__:cmd-4:/with:colon\n")
	eq(t, session.State().Cwd, "/with", "cwd with a colon")
	if changes.Load() == 0 {
		t.Error("onStateChange never ran")
	}
}

func TestBashShellSessionLifecycle(t *testing.T) {
	shellPath := resolveManagedShellPath()
	if shellPath == "" {
		t.Skip("requires zsh or bash")
	}
	store := newBashTranscriptStore(bashTranscriptLimits{MaxLines: 100, MaxBytes: 1 << 20})
	var changed atomic.Int32
	var success []string
	cwd := t.TempDir()
	session, err := newManagedShellSession(shellPath, cwd, store, func() { changed.Add(1) }, func(command, dir string) { success = append(success, command) })
	if err != nil {
		t.Fatal(err)
	}
	defer session.dispose()
	ctx := context.Background()

	// runCommand starts the shell by itself; a command that is still running refuses the next one.
	if err := session.runCommand(ctx, "sleep 0.4; echo first; echo oops >&2; exit_code_check() { return 3; }; exit_code_check"); err != nil {
		t.Fatal(err)
	}
	err = session.runCommand(ctx, "echo second")
	if err == nil || err.Error() != "Shell command already running" {
		t.Fatalf("second command while running = %v", err)
	}
	waitForShellCommand(t, session)
	snap := store.getSnapshot()
	eq(t, len(snap.Commands), 1, "the refused command was not recorded")
	if snap.Commands[0].ExitCode == nil || *snap.Commands[0].ExitCode != 3 {
		t.Fatalf("exit code = %v, want 3", snap.Commands[0].ExitCode)
	}
	out := strings.Join(snap.Commands[0].Output, "|")
	if !strings.Contains(out, "first") || !strings.Contains(out, "oops") {
		t.Errorf("stdout and stderr both reach the transcript, got %q", out)
	}
	eq(t, len(success), 0, "a failing command is no success")
	eq(t, *session.State().LastExitCode, 3, "last exit code")

	if err := session.runCommand(ctx, "echo fine"); err != nil {
		t.Fatal(err)
	}
	waitForShellCommand(t, session)
	eq(t, success, []string{"echo fine"}, "success callback")

	// Command files are removed by the shell and the directory by dispose.
	dir := session.tempDir
	session.dispose()
	if _, err := os.Stat(dir); err == nil {
		t.Error("dispose left the command directory behind")
	}
	// interrupt and dispose without a process are no-ops.
	session.interrupt()
	session.dispose()
}

func TestBashShellSessionStartFailures(t *testing.T) {
	store := newBashTranscriptStore(bashTranscriptLimits{MaxLines: 10, MaxBytes: 1000})
	missing := filepath.Join(t.TempDir(), "no-such-shell")
	session, err := newManagedShellSession(missing, t.TempDir(), store, func() {}, func(string, string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer session.dispose()
	err = session.ensureReady(context.Background())
	if err == nil || err.Error() != "spawn "+missing+" ENOENT" {
		t.Errorf("a missing shell: %v", err)
	}

	if runtime.GOOS == "windows" {
		return
	}
	// A shell that exits at once: the close handler reports it.
	session, err = newManagedShellSession("/bin/sh", t.TempDir(), store, func() {}, func(string, string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer session.dispose()
	session.shellPath = "/usr/bin/false"
	err = session.ensureReady(context.Background())
	if err == nil || err.Error() != "Shell failed to start (exit 1)" {
		t.Errorf("a shell that exits at once: %v", err)
	}
	eq(t, session.State().Ready, false, "not ready")

	// A caller that stops waiting leaves the shell starting; a disposed session releases its waiters.
	session, err = newManagedShellSession("/bin/cat", t.TempDir(), store, func() {}, func(string, string) {})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := session.ensureReady(ctx); err != context.DeadlineExceeded {
		t.Errorf("a shell that never reports ready: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- session.ensureReady(context.Background()) }()
	time.Sleep(50 * time.Millisecond)
	session.dispose()
	select {
	case err := <-done:
		if err == nil {
			t.Error("a disposed session released its waiter without an error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a disposed session left a waiter blocked")
	}
}
