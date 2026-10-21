package powerline_footer

import (
	"context"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Twins and checks of BashModeEditor.handleInput over a fake default editor with a one-line buffer.

type textBase struct {
	fakeBase
	text     string
	setTexts []string
	inserted []string
}

func (b *textBase) HandleInput(data string) error {
	b.input = append(b.input, data)
	switch {
	case data == "\x7f":
		if b.text != "" {
			b.text = b.text[:len(b.text)-1]
		}
	case isPrintableInput(data):
		b.text += data
	}
	return nil
}
func (b *textBase) GetText() (string, error)         { return b.text, nil }
func (b *textBase) GetExpandedText() (string, error) { return b.text, nil }
func (b *textBase) GetLines() ([]string, error)      { return strings.Split(b.text, "\n"), nil }
func (b *textBase) GetCursor() (sdk.EditorCursor, error) {
	lines := strings.Split(b.text, "\n")
	last := lines[len(lines)-1]
	return sdk.EditorCursor{Line: len(lines) - 1, Col: utf16Len(last)}, nil
}
func (b *textBase) SetText(text string) error {
	b.text = text
	b.setTexts = append(b.setTexts, text)
	return nil
}
func (b *textBase) InsertTextAtCursor(text string) error {
	b.text += text
	b.inserted = append(b.inserted, text)
	return nil
}

type bashRig struct {
	base                *textBase
	editor              *bashModeEditor
	active, running     bool
	completions         bool
	notes               []string
	submitted           []string
	exited, interrupted int
	resolved            []string
	ghostFor            map[string]*ghostSuggestion
	history             []string
}

func newBashRig(active bool) *bashRig {
	r := &bashRig{base: &textBase{}, active: active, completions: true, ghostFor: map[string]*ghostSuggestion{}}
	r.editor = newBashModeEditor(r.base, bashEditorEnv{
		active:        func() bool { return r.active },
		running:       func() bool { return r.running },
		completions:   func() bool { return r.completions },
		boundaryStart: func() *string { return new("super+shift+up") },
		boundaryEnd:   func() *string { return new("super+shift+down") },
		notify:        func(m, l string) { r.notes = append(r.notes, l+": "+m) },
		submit:        func(c string) { r.submitted = append(r.submitted, c) },
		exitMode:      func() { r.exited++ },
		interrupt:     func() { r.interrupted++ },
		resolveGhost: func(_ context.Context, text string) *ghostSuggestion {
			r.resolved = append(r.resolved, text)
			return r.ghostFor[text]
		},
		historyEntries: func(prefix string) []string { return r.history },
	})
	return r
}

func (r *bashRig) key(t *testing.T, data string) {
	t.Helper()
	if err := r.editor.handleInput(data); err != nil {
		t.Fatal(err)
	}
}

func (r *bashRig) setGhost(value string) {
	r.editor.mu.Lock()
	r.editor.ghost = &ghostSuggestion{Value: value, Source: bashSourceProjectHistory}
	r.editor.mu.Unlock()
}

func (r *bashRig) ghost() *ghostSuggestion {
	r.editor.mu.Lock()
	defer r.editor.mu.Unlock()
	return r.editor.ghost
}

func TestBashEditorTwins(t *testing.T) {
	tw(t, "bash-mode", "bash editor Tab accepts the current ghost suggestion without opening autocomplete", func(t *testing.T) {
		r := newBashRig(true)
		r.base.text = "git st"
		r.setGhost("git status")
		r.key(t, "\t")
		if r.base.text != "git status" {
			t.Errorf("text %q, want the ghost accepted", r.base.text)
		}
		if len(r.base.input) != 0 {
			t.Errorf("Tab reached the default editor (autocomplete): %q", r.base.input)
		}
	})
	tw(t, "bash-mode", "bash editor escape exits bash mode", func(t *testing.T) {
		r := newBashRig(true)
		r.key(t, "\x1b")
		if r.exited != 1 || r.interrupted != 0 || len(r.base.input) != 0 {
			t.Errorf("exited %d interrupted %d input %q", r.exited, r.interrupted, r.base.input)
		}
	})
	tw(t, "bash-mode", "bash editor right arrow accepts an empty-prompt ghost suggestion without submitting", func(t *testing.T) {
		r := newBashRig(true)
		r.setGhost("git status")
		r.key(t, "\x1b[C")
		if r.base.text != "git status" || len(r.submitted) != 0 {
			t.Errorf("text %q submitted %q", r.base.text, r.submitted)
		}
	})
	tw(t, "bash-mode", "bash editor right arrow accepts ghost text for one-off bang commands", func(t *testing.T) {
		r := newBashRig(false)
		r.base.text = "!git st"
		r.setGhost("!git status")
		r.key(t, "\x1b[C")
		if r.base.text != "!git status" {
			t.Errorf("text %q", r.base.text)
		}
	})
	tw(t, "bash-mode", "bash editor enter does not accept ghost text while a shell command is running", func(t *testing.T) {
		r := newBashRig(true)
		r.running = true
		r.base.text = "git st"
		r.setGhost("git status")
		r.key(t, "\r")
		if len(r.notes) != 1 || r.notes[0] != "warning: Shell command already running" || len(r.submitted) != 0 || r.base.text != "git st" {
			t.Errorf("notes %q submitted %q text %q", r.notes, r.submitted, r.base.text)
		}
	})
	tw(t, "bash-mode", "bash editor enter submits the typed command without accepting ghost text", func(t *testing.T) {
		r := newBashRig(true)
		r.base.text = "git diff"
		r.setGhost("git diff --staged")
		r.key(t, "\r")
		if len(r.submitted) != 1 || r.submitted[0] != "git diff" {
			t.Errorf("submitted %q", r.submitted)
		}
	})
	tw(t, "bash-mode", "one-off bang submit does not accept ghost text before submitting", func(t *testing.T) {
		r := newBashRig(false)
		r.base.text = "!git diff"
		r.setGhost("!git diff --staged")
		r.key(t, "\r")
		if len(r.base.input) != 1 || r.base.input[0] != "\r" || r.base.text != "!git diff" {
			t.Errorf("the submit must reach the default editor once, untouched: input %q text %q", r.base.input, r.base.text)
		}
	})
	tw(t, "bash-mode", "bash editor submit clears the prompt and refreshes the empty ghost suggestion", func(t *testing.T) {
		r := newBashRig(true)
		r.base.text = "git status"
		r.editor.history.index, r.editor.history.items, r.editor.history.draft = 3, []string{"git status"}, "git st"
		r.key(t, "\r")
		if len(r.submitted) != 1 || r.submitted[0] != "git status" {
			t.Errorf("submitted %q", r.submitted)
		}
		if r.base.text != "" || len(r.base.setTexts) != 1 {
			t.Errorf("the prompt was not cleared: %q %q", r.base.text, r.base.setTexts)
		}
		if len(r.resolved) == 0 || r.resolved[len(r.resolved)-1] != "" {
			t.Errorf("the empty ghost was not refreshed: %q", r.resolved)
		}
		if r.editor.history.index != -1 || len(r.editor.history.items) != 0 || r.editor.history.draft != "" {
			t.Errorf("history browse not reset: %+v", r.editor.history)
		}
	})
	tw(t, "bash-mode", "bash editor command-z resets shell history and updates ghost state", func(t *testing.T) {
		r := newBashRig(true)
		r.key(t, "a")
		r.key(t, "\x7f")
		r.editor.history.index, r.editor.history.items, r.editor.history.draft = 0, []string{"git status"}, "git"
		r.key(t, "\x1b[122;9u")
		if r.editor.history.index != -1 || len(r.editor.history.items) != 0 || r.editor.history.draft != "" {
			t.Errorf("history browse not reset: %+v", r.editor.history)
		}
		if r.base.input[len(r.base.input)-1] != keyUndo {
			t.Errorf("the default editor was not told to undo: %q", r.base.input)
		}
		if len(r.resolved) == 0 {
			t.Error("the ghost was not refreshed after the undo")
		}
		plain := newBashRig(false)
		plain.key(t, "z")
		plain.setGhost("stale")
		plain.key(t, "\x1b[122;9u")
		if plain.ghost() != nil {
			t.Error("a stale ghost survived the undo outside bash mode")
		}
	})
	tw(t, "bash-mode", "bash editor does not submit pasted multiline input while bracketed paste is active", func(t *testing.T) {
		r := newBashRig(true)
		r.editor.inPaste = true
		r.base.text = "echo a"
		r.key(t, "\r")
		if len(r.submitted) != 0 || len(r.base.input) != 1 || r.base.input[0] != "\r" {
			t.Errorf("submitted %q, default editor input %q", r.submitted, r.base.input)
		}
	})
	tw(t, "bash-mode", "bash editor refreshes shell ghost state after a bracketed paste completes", func(t *testing.T) {
		r := newBashRig(true)
		r.editor.inPaste = true
		r.base.text = "git status"
		r.editor.history.index, r.editor.history.items, r.editor.history.draft = 3, []string{"git status"}, "git"
		r.key(t, "\x1b[201~") // the end marker is what clears isInPaste
		if len(r.base.input) != 1 || len(r.resolved) != 1 || r.resolved[0] != "git status" {
			t.Errorf("default editor input %q, ghost refreshes %q", r.base.input, r.resolved)
		}
		if r.editor.inPaste || r.editor.history.index != -1 {
			t.Errorf("paste state %v, history %+v", r.editor.inPaste, r.editor.history)
		}
	})
	tw(t, "bash-mode", "bash editor inserts Finder file drops as path strings", func(t *testing.T) {
		r := newBashRig(false)
		r.key(t, "\x1b[200~file:///Users/nico/Desktop/Screen%20Shot%202026-05-08.png\x1b[201~")
		if r.base.text != "/Users/nico/Desktop/Screen Shot 2026-05-08.png" {
			t.Errorf("text %q", r.base.text)
		}
		r.key(t, " ")
		r.key(t, "\x1b[200~/Users/nico/Documents/Project\\ Folder\x1b[201~")
		if r.base.text != "/Users/nico/Desktop/Screen Shot 2026-05-08.png /Users/nico/Documents/Project\\ Folder" {
			t.Errorf("text %q", r.base.text)
		}
		shell := newBashRig(true)
		shell.key(t, "\x1b[200~file:///Users/nico/Pictures/Finder%20Image.png\nfile:///Users/nico/Desktop/Capture.png\x1b[201~")
		if shell.base.text != "/Users/nico/Pictures/Finder Image.png /Users/nico/Desktop/Capture.png" {
			t.Errorf("text %q", shell.base.text)
		}
		if len(shell.resolved) != 1 {
			t.Errorf("the ghost was scheduled %d times, want once", len(shell.resolved))
		}
	})
}

func TestBashEditorOutsideBashModeLeavesKeysToTheDefaultEditor(t *testing.T) {
	r := newBashRig(false)
	r.completions = false
	for _, key := range []string{"\x1b", "\x03", "\x1b[A", "\x1b[B", "\r", "\t", "\x1b[C", "a"} {
		r.key(t, key)
	}
	if len(r.base.input) != 8 || r.exited+r.interrupted+len(r.submitted) != 0 {
		t.Errorf("default editor input %q", r.base.input)
	}
	if len(r.resolved) != 0 {
		t.Errorf("ghost work with completions off: %q", r.resolved)
	}
}

func TestBashEditorInterruptsARunningCommandOnCtrlC(t *testing.T) {
	r := newBashRig(true)
	r.key(t, "\x03") // not running: the default editor gets it
	if r.interrupted != 0 || len(r.base.input) != 1 {
		t.Errorf("interrupted %d input %q", r.interrupted, r.base.input)
	}
	r.running = true
	r.key(t, "\x03")
	if r.interrupted != 1 || len(r.base.input) != 1 {
		t.Errorf("interrupted %d input %q", r.interrupted, r.base.input)
	}
}

func TestBashEditorBrowsesShellHistory(t *testing.T) {
	r := newBashRig(true)
	r.history = []string{"git status", "git diff"}
	r.base.text = "git"
	r.key(t, "\x1b[A")
	if r.base.text != "git status" {
		t.Fatalf("up: %q", r.base.text)
	}
	r.key(t, "\x1b[A")
	if r.base.text != "git diff" {
		t.Fatalf("up again: %q", r.base.text)
	}
	r.key(t, "\x1b[B")
	r.key(t, "\x1b[B")
	if r.base.text != "git" {
		t.Errorf("down to the draft: %q", r.base.text)
	}
	r.history = nil
	r.base.text = "zzz"
	r.editor.resetHistory()
	r.key(t, "\x1b[A")
	if got := r.notes[len(r.notes)-1]; got != "info: No shell history matches" {
		t.Errorf("note %q", got)
	}
}

func TestBashEditorEmptyCommandIsNotSubmitted(t *testing.T) {
	r := newBashRig(true)
	r.base.text = "   "
	r.key(t, "\r")
	if len(r.submitted) != 0 || len(r.base.setTexts) != 0 {
		t.Errorf("submitted %q setTexts %q", r.submitted, r.base.setTexts)
	}
}

func TestBashEditorBoundaryShortcutsSendTheDefaultEditorsDocumentKeys(t *testing.T) {
	r := newBashRig(false)
	r.key(t, "\x1b[1;10A") // super+shift+up, the default editorStart
	r.key(t, "\x1b[1;10B")
	if len(r.base.input) != 2 || r.base.input[0] != keyDocumentStart || r.base.input[1] != keyDocumentEnd {
		t.Errorf("input %q", r.base.input)
	}
}

func TestBashEditorGhostFollowsTypingInBashMode(t *testing.T) {
	r := newBashRig(true)
	r.ghostFor["gi"] = &ghostSuggestion{Value: "git status", Source: bashSourceProjectHistory}
	r.key(t, "g")
	r.key(t, "i")
	if g := r.ghost(); g == nil || g.Value != "git status" {
		t.Fatalf("ghost %+v after %q", g, r.resolved)
	}
	rows := []string{"top", "gi", "bottom"}
	got := r.editor.renderLines(rows, 30)
	if !strings.Contains(got[1], "\x1b[38;5;244mt status") {
		t.Errorf("ghost row %q", got[1])
	}
	// A key that is not an edit keeps it; leaving the mode drops it.
	r.active = false
	r.key(t, "\x1b[D")
	if r.ghost() != nil {
		t.Error("the ghost survived outside the shell context")
	}
}

func TestBashEditorGhostIsNotDrawnWhenTheCursorIsInsideTheText(t *testing.T) {
	r := newBashRig(true)
	r.base.text = "git st\nmore"
	r.setGhost("git status")
	rows := []string{"top", "git st", "bottom"}
	if got := r.editor.renderLines(rows, 30); got[1] != "git st" {
		t.Errorf("row %q", got[1])
	}
}

func TestBashEditorBracketedPasteWaitsForItsEnd(t *testing.T) {
	r := newBashRig(true)
	r.key(t, "\x1b[200~line one\n")
	if len(r.resolved) != 0 {
		t.Errorf("the ghost was refreshed in the middle of a paste: %q", r.resolved)
	}
	r.key(t, "line two\x1b[201~")
	if len(r.resolved) != 1 {
		t.Errorf("the ghost was refreshed %d times after the paste, want once", len(r.resolved))
	}
}

func TestBashEditorDismissClearsHistoryAndGhost(t *testing.T) {
	r := newBashRig(true)
	r.setGhost("x")
	r.editor.history.index = 2
	r.editor.dismissBashModeUi()
	if r.ghost() != nil || r.editor.history.index != -1 {
		t.Errorf("ghost %v history %+v", r.ghost(), r.editor.history)
	}
}

func TestPiKeyDefaults(t *testing.T) {
	for _, c := range []struct {
		data, id string
		want     bool
	}{
		{"\x1b", "app.interrupt", true}, {"\x03", "app.clear", true}, {"\r", "tui.input.submit", true}, {"\x1b[13;2u", "tui.input.newLine", true},
		{"\r", "tui.input.newLine", false}, {"\x7f", "tui.editor.deleteCharBackward", true}, {"\x1b[C", "tui.editor.cursorRight", true},
		{"\x06", "tui.editor.cursorRight", true}, {"\x1b[A", "tui.editor.cursorUp", true}, {"\t", "tui.input.tab", true},
		{"a", "tui.input.tab", false}, {"\x15", "tui.editor.deleteToLineStart", true}, {"\x0b", "tui.editor.deleteToLineEnd", true},
	} {
		if got := piKeyMatches(c.data, c.id); got != c.want {
			t.Errorf("piKeyMatches(%q, %s) = %v, want %v", c.data, c.id, got, c.want)
		}
	}
}
