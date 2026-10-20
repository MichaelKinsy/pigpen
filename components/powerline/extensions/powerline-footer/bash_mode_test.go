package powerline_footer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Tests of bash_editor_logic.go (the pure parts of the bash editor), bash_mode.go (the mode, its commands, the transcript widget
// rows and the segment data) and bash_segment.go.

func TestBashDroppedPathText(t *testing.T) {
	for _, c := range []struct {
		name, in string
		want     string
		ok       bool
	}{
		{"finder drop in a bracketed paste", "\x1b[200~file:///Users/nico/Desktop/Screen%20Shot%202026-05-08.png\x1b[201~", "/Users/nico/Desktop/Screen Shot 2026-05-08.png", true},
		{"two file uris on separate lines", "\x1b[200~file:///Users/nico/Pictures/Finder%20Image.png\nfile:///Users/nico/Desktop/Capture.png\x1b[201~", "/Users/nico/Pictures/Finder Image.png /Users/nico/Desktop/Capture.png", true},
		{"an escaped path pasted", "\x1b[200~/Users/nico/Documents/Project\\ Folder\x1b[201~", "/Users/nico/Documents/Project\\ Folder", true},
		{"a home path typed", "~/notes.txt", "~/notes.txt", true},
		{"a relative path", "./a/b", "./a/b", true},
		{"a parent path", "../a", "../a", true},
		{"surrounding line breaks", "\r\n/tmp/x\n", "/tmp/x", true},
		{"a comment line among uris", "#c\nfile:///a", "/a", true},
		{"localhost host", "file://localhost/etc/hosts", "/etc/hosts", true},
		{"other host", "file://server/share", "", false},
		{"encoded slash", "file:///a%2Fb", "", false},
		{"bad escape", "file:///a%zz", "", false},
		{"invalid utf-8 after decoding", "file:///a%ff", "", false},
		{"a uri among words", "file:///a hello", "", false},
		{"a single character", "/", "", false},
		{"plain text", "hello", "", false},
		{"a path with a line break inside", "/a\n/b", "", false},
		{"a control character", "/a\x01b", "", false},
		{"an unfinished paste", "\x1b[200~/tmp/x", "", false},
		{"a paste followed by more", "\x1b[200~/tmp/x\x1b[201~y", "", false},
		{"empty", "", "", false},
	} {
		got, ok := droppedPathTextFromInput(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("%s: droppedPathTextFromInput(%q) = %q, %v, want %q, %v", c.name, c.in, got, ok, c.want, c.ok)
		}
	}
	// Cases recorded from Node's fileURLToPath for the URL parser's own rewriting.
	for in, want := range map[string]string{
		"file:///a/./b": "/a/b", "file:///a/../b": "/b", "file:///..": "/", "file://": "/", "file:///a/b/..": "/a/", "file:///a?x#y": "/a",
		"file:///%41": "/A", "file:///a%20b": "/a b", "file://LOCALHOST/x": "/x", "file:///a\\b": "/a/b",
	} {
		got, ok := fileURLToPath(in)
		if !ok || got != want {
			t.Errorf("fileURLToPath(%q) = %q, %v, want %q", in, got, ok, want)
		}
	}
}

func TestBashBracketedPasteContent(t *testing.T) {
	got, ok := bracketedPasteContent("\x1b[200~a\nb\x1b[201~")
	eq(t, []any{got, ok}, []any{"a\nb", true}, "complete paste")
	for _, in := range []string{"x\x1b[200~a\x1b[201~", "\x1b[200~a", "\x1b[200~a\x1b[201~tail", "plain"} {
		if _, ok := bracketedPasteContent(in); ok {
			t.Errorf("bracketedPasteContent(%q) accepted", in)
		}
	}
}

func TestIsPrintableInput(t *testing.T) {
	for in, want := range map[string]bool{
		"a": true, " ": true, "\u6f22": true, "\U0001F642": true, "\x1f": false, "\x7f": false, "\u0085": false, "\u009f": false, "\u00a0": true,
		"": false, "ab": false, "\x1b[A": false, "\n": false,
	} {
		if got := isPrintableInput(in); got != want {
			t.Errorf("isPrintableInput(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestIsCommandUndoShortcut(t *testing.T) {
	for _, in := range []string{"\x1b[122;9u", "\x1b[122;9:1u", "\x1b[122;9:2u", "\x1b[27;9;122~"} {
		if !isCommandUndoShortcut(in) {
			t.Errorf("%q is Command+Z", in)
		}
	}
	for _, in := range []string{"\x1b[122;9:3u", "\x1b[122;5u", "\x1a", "z", ""} {
		if isCommandUndoShortcut(in) {
			t.Errorf("%q is not Command+Z", in)
		}
	}
}

func TestAcceptGhostSuggestion(t *testing.T) {
	g := &ghostSuggestion{Value: "git status", Source: bashSourceProjectHistory}
	for _, c := range []struct {
		name       string
		ghost      *ghostSuggestion
		text       string
		line, col  int
		want       string
		wantAccept bool
	}{
		{"accepted", g, "git st", 0, 6, "git status", true},
		{"empty prompt", g, "", 0, 0, "git status", true},
		{"no ghost", nil, "git", 0, 3, "", false},
		{"cursor inside", g, "git st", 0, 3, "", false},
		{"cursor on another line", g, "git st", 1, 6, "", false},
		{"multiline text", g, "git st\nx", 0, 8, "", false},
		{"ghost equals the text", g, "git status", 0, 10, "", false},
		{"ghost does not extend the text", g, "ls", 0, 2, "", false},
		{"astral text counts UTF-16 columns", &ghostSuggestion{Value: "\U0001F642a"}, "\U0001F642", 0, 2, "\U0001F642a", true},
		{"astral text with a byte column", &ghostSuggestion{Value: "\U0001F642a"}, "\U0001F642", 0, 4, "", false},
	} {
		got, ok := acceptGhostSuggestion(c.ghost, c.text, c.line, c.col)
		if ok != c.wantAccept || got != c.want {
			t.Errorf("%s: = %q, %v, want %q, %v", c.name, got, ok, c.want, c.wantAccept)
		}
	}
}

func TestRenderGhostLine(t *testing.T) {
	rows := []string{"top", "git st", "bottom"}
	g := &ghostSuggestion{Value: "git status", Source: bashSourceProjectHistory}
	got := renderGhostLine(rows, 20, "git st", g, 0, 6, true)
	want := "git st\x1b[7m \x1b[0m\x1b[38;5;244matus" + strings.Repeat(" ", 20-6-1-4) + "\x1b[0m" // pi-tui pads the suffix inside its colour
	if got[1] != want {
		t.Errorf("row = %q, want %q", got[1], want)
	}
	eq(t, rows[1], "git st", "the input rows are not modified")
	eq(t, got[0]+got[2], "topbottom", "other rows untouched")
	eq(t, visibleWidth(got[1]), 20, "the row is as wide as the editor")

	// A suffix wider than the room is cut (and padded to the room).
	got = renderGhostLine(rows, 10, "git st", g, 0, 6, true)
	eq(t, got[1], "git st\x1b[7m \x1b[0m\x1b[38;5;244matu\x1b[0m\x1b[0m\x1b[0m", "cut suffix")
	for name, c := range map[string]struct {
		width       int
		text        string
		ghost       *ghostSuggestion
		line, col   int
		shell       bool
		lines       []string
		description string
	}{
		"not a shell context": {20, "git st", g, 0, 6, false, rows, ""},
		"no ghost":            {20, "git st", nil, 0, 6, true, rows, ""},
		"multiline":           {20, "a\nb", &ghostSuggestion{Value: "a\nbc"}, 0, 3, true, rows, ""},
		"cursor elsewhere":    {20, "git st", g, 0, 3, true, rows, ""},
		"ghost not extending": {20, "ls", g, 0, 2, true, rows, ""},
		"equal":               {20, "git status", g, 0, 10, true, rows, ""},
		"too few rows":        {20, "git st", g, 0, 6, true, rows[:2], ""},
		"no room":             {7, "git st", g, 0, 6, true, rows, ""},
	} {
		if out := renderGhostLine(c.lines, c.width, c.text, c.ghost, c.line, c.col, c.shell); out[1] != c.lines[1] || len(out) != len(c.lines) {
			t.Errorf("%s: rows changed: %q", name, out)
		}
	}
}

func TestShellHistoryBrowser(t *testing.T) {
	entries := func(prefix string) []string {
		if prefix == "git" {
			return []string{"git stash", "git status", "git diff"}
		}
		return nil
	}
	b := newShellHistoryBrowser()
	step := b.navigate(-1, "git", entries)
	eq(t, *step.SetText, "git stash", "first up")
	eq(t, step.ClearGhost, true, "up clears the ghost")
	eq(t, b.draft, "git", "the typed text becomes the draft")
	step = b.navigate(-1, "git stash", entries) // the editor now shows the first entry; the draft stays the prefix
	eq(t, *step.SetText, "git status", "second up")
	b.navigate(-1, "", entries)
	step = b.navigate(-1, "", entries)
	eq(t, *step.SetText, "git diff", "up stops at the oldest")
	eq(t, b.index, 2, "index")
	step = b.navigate(1, "", entries)
	eq(t, *step.SetText, "git status", "down")
	step = b.navigate(1, "", entries)
	eq(t, *step.SetText, "git stash", "down to the first entry")
	step = b.navigate(1, "", entries)
	eq(t, *step.SetText, "git", "down past the first entry restores the draft")
	eq(t, step.ScheduleGhost, true, "restoring the draft refreshes the ghost")
	eq(t, step.ClearGhost, false, "and does not clear it")
	eq(t, b.index, -1, "index reset")

	b.reset()
	eq(t, []any{b.index, len(b.items), b.draft}, []any{-1, 0, ""}, "reset")
	step = b.navigate(-1, "zzz", entries)
	eq(t, step.Notify, "No shell history matches", "nothing to browse")
	if step.SetText != nil {
		t.Error("no text change without matches")
	}
	eq(t, b.draft, "zzz", "the draft is still taken (as upstream)")
}

func TestGhostUpdaterCoalescesUpdates(t *testing.T) {
	var mu sync.Mutex
	var resolved []string
	var renders atomic.Int32
	u := newGhostUpdater(func(ctx context.Context, text string) (*ghostSuggestion, error) {
		mu.Lock()
		resolved = append(resolved, text)
		mu.Unlock()
		return &ghostSuggestion{Value: text + "!", Source: bashSourceGit}, nil
	}, func() { renders.Add(1) })
	u.debounce = 40 * time.Millisecond

	u.schedule(true, "g")
	u.schedule(true, "gi")
	u.schedule(true, "git")
	mu.Lock()
	eq(t, len(resolved), 0, "nothing resolved inside the debounce window")
	mu.Unlock()
	bashWaitFor(t, func() bool { return u.current() != nil })
	mu.Lock()
	eq(t, resolved, []string{"git"}, "three quick updates resolve once, for the last text")
	mu.Unlock()
	eq(t, u.current().Value, "git!", "ghost")
	eq(t, renders.Load(), int32(1), "one render request")

	// clear drops the ghost and a pending update.
	u.schedule(true, "x")
	u.clear()
	time.Sleep(120 * time.Millisecond)
	eq(t, u.current(), (*ghostSuggestion)(nil), "cleared")
	mu.Lock()
	eq(t, len(resolved), 1, "a cleared update is never resolved")
	mu.Unlock()

	// A newer update cancels the context of the one in flight and its result is dropped.
	release := make(chan struct{})
	started := make(chan struct{})
	var cancelled atomic.Bool
	slow := newGhostUpdater(func(ctx context.Context, text string) (*ghostSuggestion, error) {
		if text == "slow" {
			close(started)
			<-release
			cancelled.Store(ctx.Err() != nil)
			return &ghostSuggestion{Value: "stale"}, nil
		}
		return &ghostSuggestion{Value: "fresh"}, nil
	}, nil)
	slow.debounce = 5 * time.Millisecond
	slow.schedule(true, "slow")
	<-started
	slow.schedule(true, "fast")
	close(release)
	bashWaitFor(t, func() bool { return slow.current() != nil })
	eq(t, slow.current().Value, "fresh", "the newer result wins")
	bashWaitFor(t, cancelled.Load)

	// A resolver error is reported and leaves the ghost alone.
	var reported atomic.Int32
	old := bashDebug
	bashDebug = func(string, ...any) { reported.Add(1) }
	t.Cleanup(func() { bashDebug = old })
	failing := newGhostUpdater(func(context.Context, string) (*ghostSuggestion, error) { return nil, os.ErrInvalid }, nil)
	failing.debounce = time.Millisecond
	failing.schedule(true, "x")
	bashWaitFor(t, func() bool { return reported.Load() == 1 })
	eq(t, failing.current(), (*ghostSuggestion)(nil), "no ghost after an error")
}

func bashWaitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in 5s")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestFastASCIIEdits(t *testing.T) {
	long := strings.Repeat("x", 5000)
	got, ok := fastASCIIBackspace(long, 5000)
	eq(t, []any{got == long[:4999], ok}, []any{true, true}, "backspace at the end of a long ASCII line")
	for name, c := range map[string]struct {
		line string
		col  int
	}{
		"short line":     {strings.Repeat("x", 1199), 1199},
		"cursor inside":  {long, 4000},
		"wide neighbour": {long[:4998] + "x\u6f22", 4999},
		"control":        {long[:4998] + "x\t", 5000},
	} {
		if _, ok := fastASCIIBackspace(c.line, c.col); ok {
			t.Errorf("backspace fast path taken: %s", name)
		}
	}
	got, ok = fastASCIIForwardDelete(long, 10)
	eq(t, []any{len(got), ok}, []any{4999, true}, "forward delete in a long line")
	for name, c := range map[string]struct {
		line string
		col  int
	}{
		"at the end":      {long, 5000},
		"short":           {"abc", 1},
		"before wide":     {long[:10] + "\u6f22" + long[:2000], 9},
		"on a wide char":  {long[:10] + "\u6f22" + long[:2000], 10},
		"after wide char": {long[:10] + "\u6f22" + long[:2000], 11},
	} {
		if _, ok := fastASCIIForwardDelete(c.line, c.col); ok {
			t.Errorf("forward delete fast path taken: %s", name)
		}
	}
	col, ok := fastASCIIHorizontalMove(long, 5000, -1)
	eq(t, []any{col, ok}, []any{4999, true}, "left at the end")
	col, ok = fastASCIIHorizontalMove(long, 0, 1)
	eq(t, []any{col, ok}, []any{1, true}, "right at the start")
	for name, c := range map[string]struct {
		line     string
		col, dir int
	}{
		"left at the start": {long, 0, -1}, "right at the end": {long, 5000, 1}, "short line": {"abc", 1, 1},
		"mixed boundary": {long[:10] + "\U0001F642" + long[:2000], 9, 1},
	} {
		if _, ok := fastASCIIHorizontalMove(c.line, c.col, c.dir); ok {
			t.Errorf("horizontal move fast path taken: %s", name)
		}
	}
}

// bashModeFixture is a bashMode over a real shell with recorded notifications.
type bashModeFixture struct {
	mode    *bashMode
	mu      sync.Mutex
	notes   []string
	renders atomic.Int32
	cwd     string
}

func newBashModeFixture(t *testing.T) *bashModeFixture {
	t.Helper()
	shell := resolveManagedShellPath()
	if shell == "" {
		t.Skip("requires zsh or bash")
	}
	cwd, err := filepath.EvalSymlinks(t.TempDir()) // the shell reports the physical directory
	if err != nil {
		t.Fatal(err)
	}
	f := &bashModeFixture{cwd: cwd}
	bashTestEnv(t, nil)
	f.mode = newBashMode(bashModeEnv{
		Cwd:                 func() string { return f.cwd },
		ShellPath:           func() string { return shell },
		RequestStatusRender: func() { f.renders.Add(1) },
		Notify: func(message, level string) {
			f.mu.Lock()
			f.notes = append(f.notes, level+": "+message)
			f.mu.Unlock()
		},
	}, bashTranscriptLimits{MaxLines: 100, MaxBytes: 1 << 20})
	t.Cleanup(f.mode.sessionShutdown)
	return f
}

func (f *bashModeFixture) notifications() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.notes...)
}

func (f *bashModeFixture) wait(t *testing.T) {
	t.Helper()
	bashWaitFor(t, func() bool { return !f.mode.isShellRunning() })
}

func TestBashModeToggle(t *testing.T) {
	f := newBashModeFixture(t)
	ctx := context.Background()
	var dismissed, refreshed int
	f.mode.editor = bashEditorHooks{dismissBashModeUi: func() { dismissed++ }, refreshGhostSuggestion: func() { refreshed++ }}

	f.mode.handleBashModeCommand(ctx, "")
	shellName := f.mode.segmentData().Name
	eq(t, f.mode.isActive(), true, "toggled on")
	eq(t, f.notifications(), []string{"info: Bash mode enabled (" + shellName + ")"}, "enabled notification names the shell")
	eq(t, []int{dismissed, refreshed}, []int{1, 1}, "editor hooks on enable")

	f.mode.handleBashModeCommand(ctx, " ON ")
	eq(t, len(f.notifications()), 1, "on while on does nothing")
	f.mode.handleBashModeCommand(ctx, "bogus")
	eq(t, f.notifications()[1], "warning: Usage: /bash-mode [on|off|toggle]", "usage")
	eq(t, f.mode.isActive(), true, "usage leaves the mode")

	f.mode.handleBashModeCommand(ctx, "off")
	eq(t, f.mode.isActive(), false, "off")
	eq(t, f.notifications()[2], "info: Bash mode disabled", "disabled notification")
	eq(t, []int{dismissed, refreshed}, []int{2, 1}, "editor hooks on disable: dismiss only")
	f.mode.handleBashModeCommand(ctx, "off")
	eq(t, len(f.notifications()), 3, "off while off does nothing")
	if f.renders.Load() == 0 {
		t.Error("the bar was never asked to repaint")
	}
	f.mode.handleBashModeCommand(ctx, "toggle")
	eq(t, f.mode.isActive(), true, "toggle on again, reusing the shell")
}

func TestBashRunShellCommandWhileRunning(t *testing.T) {
	f := newBashModeFixture(t)
	ctx := context.Background()
	f.mode.setBashModeActive(ctx, true)
	f.mode.runShellCommand(ctx, "sleep 0.5; echo done")
	eq(t, f.mode.isShellRunning(), true, "running")
	data := f.mode.segmentData()
	eq(t, []any{data.Active, data.Running}, []any{true, true}, "segment data while running")

	f.mode.runShellCommand(ctx, "echo no")
	notes := f.notifications()
	eq(t, notes[len(notes)-1], "error: Failed to run shell command: Shell command already running", "a second command is refused with a notification")

	f.mode.setBashModeActive(ctx, false)
	notes = f.notifications()
	eq(t, notes[len(notes)-1], "warning: Wait for the current shell command to finish before leaving bash mode", "leaving while running")
	eq(t, f.mode.isActive(), true, "still active")
	f.wait(t)
	f.mode.setBashModeActive(ctx, false)
	eq(t, f.mode.isActive(), false, "left once idle")
}

func TestBashModeHistoryRecording(t *testing.T) {
	f := newBashModeFixture(t)
	ctx := context.Background()
	f.mode.setBashModeActive(ctx, true)
	f.mode.runShellCommand(ctx, "true")
	f.wait(t)
	f.mode.runShellCommand(ctx, "false")
	f.wait(t)
	bashWaitFor(t, func() bool { return len(readProjectHistory(f.cwd)) == 1 })
	history := readProjectHistory(f.cwd)
	eq(t, history[0].Command, "true", "only a successful command is recorded")
	eq(t, f.mode.shellHistoryEntries("tr")[0], "true", "the history source offers it")
	eq(t, f.mode.shellHistoryEntries("fa"), []string{}, "a failed command is not offered")

	// The ghost for a one-off bang line carries the bang prefix back.
	g := f.mode.resolveGhostSuggestion(ctx, "!tr")
	eq(t, g, &ghostSuggestion{Value: "!true", Source: bashSourceProjectHistory}, "one-off ghost")
	g = f.mode.resolveGhostSuggestion(ctx, "!!tr")
	eq(t, g, &ghostSuggestion{Value: "!!true", Source: bashSourceProjectHistory}, "double-bang ghost")
	g = f.mode.resolveGhostSuggestion(ctx, "tr")
	eq(t, g, &ghostSuggestion{Value: "true", Source: bashSourceProjectHistory}, "plain ghost")
	eq(t, f.mode.resolveGhostSuggestion(ctx, "!zzz"), (*ghostSuggestion)(nil), "no ghost, no bang")
}

func TestBashResetCommand(t *testing.T) {
	f := newBashModeFixture(t)
	ctx := context.Background()
	f.mode.handleBashResetCommand(ctx)
	eq(t, f.notifications(), []string{"info: Bash session reset"}, "reset outside bash mode starts no shell")
	eq(t, f.mode.segmentData().Name, "", "no shell")

	f.mode.setBashModeActive(ctx, true)
	f.mode.runShellCommand(ctx, "echo hi")
	f.wait(t)
	before := f.mode.transcript.getSnapshot()
	eq(t, len(before.Commands), 1, "one command")
	f.mode.handleBashResetCommand(ctx)
	eq(t, len(f.mode.transcript.getSnapshot().Commands), 0, "transcript cleared")
	eq(t, f.mode.isActive(), true, "the mode survives a reset")
	data := f.mode.segmentData()
	if data.Name == "" || data.Running {
		t.Errorf("a new idle shell is expected after the reset, got %+v", data)
	}
	notes := f.notifications()
	eq(t, notes[len(notes)-1], "info: Bash session reset", "notification")
}

func TestBashModeStartFailure(t *testing.T) {
	f := newBashModeFixture(t)
	ctx := context.Background()
	missing := filepath.Join(t.TempDir(), "nosuchshell")
	f.mode.env.ShellPath = func() string { return missing }
	f.mode.setBashModeActive(ctx, true)
	eq(t, f.mode.isActive(), false, "not active")
	notes := f.notifications()
	eq(t, notes[len(notes)-1], "error: Failed to start shell session: spawn "+missing+" ENOENT", "start failure")
	eq(t, f.mode.segmentData().Name, "", "the failed session is dropped")

	f.mode.runShellCommand(ctx, "echo x")
	notes = f.notifications()
	eq(t, notes[len(notes)-1], "error: Failed to run shell command: spawn "+missing+" ENOENT", "run failure")

	// A reset in bash mode that cannot restart the shell leaves the mode.
	f.mode.sessionShutdown() // the broken session of the failed start is kept until the mode drops it, as upstream
	f.mode.env.ShellPath = func() string { return resolveManagedShellPath() }
	f.mode.setBashModeActive(ctx, true)
	f.mode.env.ShellPath = func() string { return missing }
	f.mode.handleBashResetCommand(ctx)
	eq(t, f.mode.isActive(), false, "reset failure leaves bash mode")
	notes = f.notifications()
	eq(t, notes[len(notes)-1], "error: Failed to restart shell session: spawn "+missing+" ENOENT", "restart failure")
}

func TestBashModeLifecycle(t *testing.T) {
	f := newBashModeFixture(t)
	ctx := context.Background()
	f.mode.setBashModeActive(ctx, true)
	f.mode.runShellCommand(ctx, "echo hi")
	f.wait(t)
	oldTranscript := f.mode.transcript

	f.mode.powerlineDisabled()
	eq(t, f.mode.isActive(), false, "disabled powerline leaves bash mode")
	eq(t, len(oldTranscript.getSnapshot().Commands), 0, "and clears the transcript")

	f.mode.setBashModeActive(ctx, true)
	f.mode.sessionStart(bashTranscriptLimits{MaxLines: 5, MaxBytes: 50})
	eq(t, f.mode.isActive(), false, "session start leaves bash mode")
	eq(t, f.mode.segmentData().Name, "", "and drops the shell")
	eq(t, f.mode.limits, bashTranscriptLimits{MaxLines: 5, MaxBytes: 50}, "limits are read again")
	if f.mode.transcript == oldTranscript {
		t.Error("session start must create a new transcript")
	}
	f.mode.setBashModeActive(ctx, true)
	f.mode.sessionShutdown()
	eq(t, f.mode.isActive(), false, "shutdown leaves bash mode")
	eq(t, f.mode.shellCwd(), f.cwd, "without a shell the session's cwd")
}

func TestBashModeShellCwdTracking(t *testing.T) {
	f := newBashModeFixture(t)
	ctx := context.Background()
	child := filepath.Join(f.cwd, "child")
	bashMustMkdir(t, child)
	f.mode.setBashModeActive(ctx, true)
	f.mode.runShellCommand(ctx, "cd child")
	f.wait(t)
	eq(t, f.mode.shellCwd(), child, "the shell's cwd follows cd")
	eq(t, f.mode.segmentData().Cwd, child, "segment data")
}

type fakeBashTheme struct{}

func (fakeBashTheme) Fg(token, text string) (string, error) {
	return "<" + token + ">" + text + "</>", nil
}

func TestRenderBashTranscriptLines(t *testing.T) {
	var transcript *bashTranscriptStore
	mode := newBashMode(bashModeEnv{
		Cwd: func() string { return "/w" }, ShellPath: func() string { return "/bin/zsh" },
		RequestStatusRender: func() {}, Notify: func(string, string) {},
	}, bashTranscriptLimits{MaxLines: 1000, MaxBytes: 1 << 20})
	transcript = mode.transcript
	th := fakeBashTheme{}

	lines, err := mode.renderBashTranscriptLines(40, th)
	eq(t, []any{lines, err}, []any{[]string(nil), error(nil)}, "nothing outside bash mode")
	mode.active = true
	lines, _ = mode.renderBashTranscriptLines(40, th)
	eq(t, len(lines), 0, "nothing without commands")

	transcript.startCommand("1", "echo   one\n two", "/w")
	transcript.appendOutput("1", "o1\no2\no3\no4\no5\no6\no7")
	transcript.finishCommand("1", 0)
	transcript.startCommand("2", "false", "/w")
	transcript.finishCommand("2", 7)
	transcript.startCommand("3", "sleep 9", "/w")
	lines, err = mode.renderBashTranscriptLines(40, th)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, lines, []string{
		" <accent>$</> echo one two <dim>(</><success>ok</><dim>)</>",
		"   o2", "   o3", "   o4", "   o5", "   o6", "   o7",
		" <accent>$</> false <dim>(</><error>exit 7</><dim>)</>",
		" <accent>$</> sleep 9 <dim>(</><accent>running</><dim>)</>",
	}, "last six output lines per command, status by exit code")

	// The prompt glyph follows the shell: fish uses ">".
	mode.session = &managedShellSession{state: shellSessionState{ShellName: "fish"}}
	lines, _ = mode.renderBashTranscriptLines(40, th)
	eq(t, lines[0], " <accent>></> echo one two <dim>(</><success>ok</><dim>)</>", "fish prompt")
	mode.session = nil

	// Long commands and output are cut to the width.
	transcript.clear()
	transcript.startCommand("4", strings.Repeat("c", 100), "/w")
	transcript.appendOutput("4", strings.Repeat("o", 100))
	lines, _ = mode.renderBashTranscriptLines(20, th)
	eq(t, visibleWidth(strings.NewReplacer("<accent>", "", "<dim>", "", "</>", "").Replace(lines[0])) <= 20+len(" (running)")+0, true, "command cut")
	eq(t, lines[1], "   "+truncateToWidth(strings.Repeat("o", 100), 17, "\u2026"), "output cut to width-3")

	// More than four commands: only the last four, and a notice for truncated ones.
	transcript.clear()
	small := newBashMode(mode.env, bashTranscriptLimits{MaxLines: 2, MaxBytes: 1 << 20})
	small.active = true
	for i := 1; i <= 6; i++ {
		id := bashItoa(i)
		small.transcript.startCommand(id, "cmd"+id, "/w")
		small.transcript.appendOutput(id, "out")
		small.transcript.finishCommand(id, 0)
	}
	lines, _ = small.renderBashTranscriptLines(40, th)
	eq(t, lines[0], " <dim>\u2026 4 earlier commands truncated</>", "truncation notice")
	one := newBashMode(mode.env, bashTranscriptLimits{MaxLines: 1, MaxBytes: 1 << 20})
	one.active = true
	one.transcript.startCommand("a", "a", "/w")
	one.transcript.appendOutput("a", "1\n2")
	one.transcript.startCommand("b", "b", "/w")
	one.transcript.appendOutput("b", "x\ny")
	lines, _ = one.renderBashTranscriptLines(40, th)
	eq(t, lines[0], " <dim>\u2026 1 earlier command truncated</>", "singular notice")

	// At most 16 rows, the newest.
	big := newBashMode(mode.env, bashTranscriptLimits{MaxLines: 1000, MaxBytes: 1 << 20})
	big.active = true
	for i := 1; i <= 4; i++ {
		id := bashItoa(i)
		big.transcript.startCommand(id, "cmd"+id, "/w")
		big.transcript.appendOutput(id, "1\n2\n3\n4\n5\n6")
	}
	lines, _ = big.renderBashTranscriptLines(40, th)
	eq(t, len(lines), 16, "sixteen rows")
	eq(t, lines[15], "   6", "the newest row last")
}

func TestShellModeSegment(t *testing.T) {
	setenv(t, "POWERLINE_NERD_FONTS", s("0"))
	ctx := &segmentContext{Theme: fakeBashTheme{}, Colors: colorScheme{}, Options: segmentOptions{}}
	eq(t, shellModeSegment(ctx), renderedSegment{}, "hidden outside bash mode")
	ctx.Shell = shellSegmentData{Active: true, Running: false, Name: "zsh", Cwd: "/home/u/proj"}
	got := shellModeSegment(ctx)
	eq(t, got.Visible, true, "visible")
	if !strings.Contains(got.Content, "zsh"+sepDot+"idle"+sepDot+"proj") {
		t.Errorf("content %q", got.Content)
	}
	ctx.Shell = shellSegmentData{Active: true, Running: true}
	if got := shellModeSegment(ctx); !strings.Contains(got.Content, "shell"+sepDot+"run") || strings.Contains(got.Content, "proj") {
		t.Errorf("no shell yet: %q", got.Content)
	}
	ctx.Shell = shellSegmentData{Active: true, Name: "fish", Cwd: "/"}
	eq(t, shellModeSegment(ctx).Content, "<accent>fish"+sepDot+"idle</>", "the root has no base name")

	// The path segment shows the shell's directory in bash mode.
	ctx = &segmentContext{Theme: fakeBashTheme{}, Colors: colorScheme{}, CWD: "/project/main", Options: segmentOptions{}}
	if got := pathSegment(ctx); !strings.Contains(got.Content, "main") {
		t.Errorf("path without bash mode: %q", got.Content)
	}
	ctx.Shell = shellSegmentData{Active: true, Name: "zsh", Cwd: "/elsewhere/deep"}
	if got := pathSegment(ctx); !strings.Contains(got.Content, "deep") || strings.Contains(got.Content, "main") {
		t.Errorf("path in bash mode: %q", got.Content)
	}
	ctx.Shell = shellSegmentData{Name: "zsh", Cwd: "/elsewhere/deep"}
	if got := pathSegment(ctx); !strings.Contains(got.Content, "main") {
		t.Errorf("an inactive shell does not move the path: %q", got.Content)
	}
}

func TestBashShellPathDefault(t *testing.T) {
	setenv(t, "SHELL", nil)
	eq(t, defaultShellPath(), "/bin/sh", "default shell")
	setenv(t, "SHELL", s("/usr/bin/fish"))
	eq(t, defaultShellPath(), "/usr/bin/fish", "SHELL")
	setenv(t, "SHELL", s(""))
	eq(t, defaultShellPath(), "/bin/sh", "an empty SHELL is unset")
}
