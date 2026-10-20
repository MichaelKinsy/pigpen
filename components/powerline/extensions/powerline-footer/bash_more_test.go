package powerline_footer

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// More tests of the bash mode logic: each pins a behavior of the original that the upstream cases do not reach.

// A cached history file is read again when only its permissions changed (history.ts:11, the mode and ctime of the fingerprint).
func TestBashHistoryFingerprintSeesPermissionChanges(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	histfile := ": 1:0;git status\n"
	_, path := bashTestEnv(t, &histfile)
	eq(t, readGlobalShellHistory("/bin/zsh"), []string{"git status"}, "first read")
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	if f, err := os.Open(path); err == nil {
		_ = f.Close()
		t.Skip("permissions are not enforced for this user")
	}
	old := bashDebug
	bashDebug = func(string, ...any) {}
	t.Cleanup(func() { bashDebug = old })
	eq(t, readGlobalShellHistory("/bin/zsh"), []string(nil), "unreadable after chmod 000, not served from the cache")
}

// The newest global match gets the biggest boost, down to one (completion.ts:322). Upstream does not sort again after boosting, so
// the boost never changes which ghost wins; the scores are what the function produces.
func TestBashGlobalHistoryBoostScores(t *testing.T) {
	item := func(replacement string) extendedCompletionItem {
		return extendedCompletionItem{Replacement: replacement, StartCol: 2, EndCol: 2, Score: 52}
	}
	items := []extendedCompletionItem{item("a"), item("b"), item("c"), item("d"), item("e"), item("f"), item("zz")}
	matches := []string{"x a", "x b", "x c", "x d", "x e", "x f"}
	got := boostValidatedItemsFromGlobalHistory("x ", items, matches)
	var scores []int
	for _, it := range got {
		scores = append(scores, it.Score)
	}
	eq(t, scores, []int{56, 55, 54, 53, 53, 53, 52}, "boosts 4, 3, 2, 1, 1, 1 and none for an unlisted candidate")
	eq(t, items[0].Score, 52, "the input items are not modified")
	eq(t, boostValidatedItemsFromGlobalHistory("x ", nil, matches), []extendedCompletionItem(nil), "no items")
	eq(t, boostValidatedItemsFromGlobalHistory("x ", items, nil)[0].Score, 52, "no matches")

	histfile := strings.Join([]string{": 1:0;git cherry-pick", ": 2:0;git ch1"}, "\n")
	cwd, _ := bashTestEnv(t, &histfile)
	wantGhost(t, ghost(t, "git ch", cwd, "/bin/zsh"), "git checkout", "git")
}

// Only history entries for the same command head can complete a command-position stem (completion.ts:289).
func TestBashGlobalGitHistoryNeedsTheGitHead(t *testing.T) {
	histfile := ": 1:0;git stash\n: 2:0;gitk --all\n"
	cwd, _ := bashTestEnv(t, &histfile)
	wantGhost(t, ghost(t, "gi", cwd, "/bin/zsh"), "git stash", "global-history")
	histfile = ": 2:0;gitk --all\n"
	if err := os.WriteFile(os.Getenv("HISTFILE"), []byte(histfile), 0o644); err != nil {
		t.Fatal(err)
	}
	wantGhost(t, ghost(t, "gi", cwd, "/bin/zsh"), "git status", "git")
}

func TestBashGitRefSubcommands(t *testing.T) {
	logPath := useFakeGit(t)
	histfile := ""
	cwd, _ := bashTestEnv(t, &histfile)
	for _, sub := range []string{"checkout", "switch", "merge", "rebase", "branch", "show", "diff"} {
		_ = os.Remove(logPath)
		wantGhost(t, ghost(t, "git "+sub+" fea", cwd, "/bin/zsh"), "git "+sub+" feature", "git")
		calls, _ := os.ReadFile(logPath)
		if !strings.Contains(string(calls), "tag --list") {
			t.Errorf("git %s did not ask for refs", sub)
		}
	}
	// "feature" is a branch and a tag; two branches match "feature".
	wantGhost(t, ghost(t, "git checkout feature-", cwd, "/bin/zsh"), "git checkout feature-two", "git")
	// Not git: no ref completion even for a ref-taking word.
	eq(t, ghost(t, "gix checkout fea", cwd, "/bin/zsh"), (*ghostSuggestion)(nil), "other command")
}

func TestBashCloseExitCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX signals")
	}
	for _, c := range []struct {
		script string
		want   int
	}{
		{"exit 7", 7}, {"kill -INT $$", 130}, {"kill -TERM $$", 143}, {"kill -KILL $$", 137}, {"kill -HUP $$", 1},
	} {
		cmd := exec.Command("/bin/sh", "-c", c.script)
		_ = cmd.Run()
		if got := closeExitCode(cmd.ProcessState); got != c.want {
			t.Errorf("%s: exit code %d, want %d", c.script, got, c.want)
		}
	}
}

func TestBashShellSessionEnvironmentAndInterruptWhileIdle(t *testing.T) {
	shellPath := resolveManagedShellPath()
	if shellPath == "" {
		t.Skip("requires zsh or bash")
	}
	store := newBashTranscriptStore(bashTranscriptLimits{MaxLines: 100, MaxBytes: 1 << 20})
	session, err := newManagedShellSession(shellPath, t.TempDir(), store, func() {}, func(string, string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer session.dispose()
	ctx := context.Background()
	if err := session.ensureReady(ctx); err != nil {
		t.Fatal(err)
	}

	// An interrupt while no command runs does nothing: the shell survives it.
	session.interrupt()
	time.Sleep(150 * time.Millisecond)
	eq(t, session.State().Ready, true, "the idle shell survived interrupt()")

	// The shell's environment carries the two switches that keep shell frameworks quiet.
	if err := session.runCommand(ctx, "echo \"$DISABLE_AUTO_UPDATE/$DISABLE_UPDATE_PROMPT\""); err != nil {
		t.Fatal(err)
	}
	waitForShellCommand(t, session)
	out := store.getSnapshot().Commands[0].Output
	eq(t, out, []string{"true/true"}, "environment")

	// dispose kills the shell process.
	session.mu.Lock()
	pid := session.cmd.Process.Pid
	session.mu.Unlock()
	session.dispose()
	deadline := time.Now().Add(3 * time.Second)
	for syscall.Kill(pid, 0) == nil && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if syscall.Kill(pid, 0) == nil {
		// a zombie answers signal 0 until it is reaped: wait for the close handler
		waitFor(t, func() bool { return !session.State().Ready })
	}
	eq(t, session.State().Ready, false, "not ready after dispose")
}

func TestBashShellSessionIDsAreDistinct(t *testing.T) {
	shellPath := resolveManagedShellPath()
	if shellPath == "" {
		t.Skip("requires zsh or bash")
	}
	store := newBashTranscriptStore(bashTranscriptLimits{MaxLines: 100, MaxBytes: 1 << 20})
	session, err := newManagedShellSession(shellPath, t.TempDir(), store, func() {}, func(string, string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer session.dispose()
	ctx := context.Background()
	for _, cmd := range []string{"echo one", "echo two"} {
		if err := session.runCommand(ctx, cmd); err != nil {
			t.Fatal(err)
		}
		waitForShellCommand(t, session)
	}
	snap := store.getSnapshot()
	eq(t, []string{snap.Commands[0].ID, snap.Commands[1].ID}, []string{"cmd-1", "cmd-2"}, "command ids")
	eq(t, snap.Commands[0].Output, []string{"one"}, "first output")
	eq(t, snap.Commands[1].Output, []string{"two"}, "second output")
}

func TestBashShellSessionReadyLine(t *testing.T) {
	store := newBashTranscriptStore(bashTranscriptLimits{MaxLines: 100, MaxBytes: 1 << 20})
	session, err := newManagedShellSession("/bin/bash", "/start", store, func() {}, func(string, string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer session.dispose()
	session.handleChunk("__PI_READY__:\n")
	st := session.State()
	eq(t, []any{st.Ready, st.Cwd}, []any{true, "/start"}, "a ready line without a directory keeps the starting directory")
	session.handleChunk("__PI_CMD_START__::/x\n")
	eq(t, session.State().Cwd, "/x", "a start marker with an empty id still sets the cwd")
}

func TestBashGhostDebounceIsFiftyMilliseconds(t *testing.T) {
	eq(t, ghostUpdateDebounce, 50*time.Millisecond, "GHOST_UPDATE_DEBOUNCE_MS")
	eq(t, fastASCIILineColumnThreshold, 1200, "FAST_ASCII_LINE_COLUMN_THRESHOLD")
}

func TestBashTranscriptLimitBoundaries(t *testing.T) {
	// A transcript exactly at its limits keeps every command (the limits are exceeded, not reached).
	store := newBashTranscriptStore(bashTranscriptLimits{MaxLines: 4, MaxBytes: 14})
	store.startCommand("a", "a", "/")
	store.appendOutput("a", "ab\ncd") // 2 lines, 6 bytes
	store.startCommand("b", "b", "/")
	store.appendOutput("b", "ef\ngh") // 4 lines, 12 bytes
	eq(t, len(store.getSnapshot().Commands), 2, "at the line limit")
	store.appendOutput("b", "ij") // 5 lines, 15 bytes: over both
	eq(t, len(store.getSnapshot().Commands), 1, "over the limits")

	bytesOnly := newBashTranscriptStore(bashTranscriptLimits{MaxLines: 100, MaxBytes: 12})
	bytesOnly.startCommand("a", "a", "/")
	bytesOnly.appendOutput("a", "abcde") // 6 bytes
	bytesOnly.startCommand("b", "b", "/")
	bytesOnly.appendOutput("b", "fghij") // 12 bytes
	eq(t, len(bytesOnly.getSnapshot().Commands), 2, "at the byte limit")
	bytesOnly.appendOutput("b", "k")
	eq(t, len(bytesOnly.getSnapshot().Commands), 1, "over the byte limit")
}

func TestBashProjectKeyFilePath(t *testing.T) {
	cwd, _ := bashTestEnv(t, nil)
	eq(t, filepath.Base(projectHistoryPath("/a/b/")), "a-b.json", "history file name")
	eq(t, filepath.Dir(projectHistoryPath(cwd)), filepath.Join(getAgentDir(), "powerline-footer", "bash-history"), "history directory")
}
