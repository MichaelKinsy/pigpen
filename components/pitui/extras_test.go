package tui

import (
	"strings"
	"testing"
)

func TestContainerTextSpacerBorder(t *testing.T) {
	c := NewContainer()
	sp := NewSpacer(2)
	c.AddChild(sp)
	c.AddChild(NewText("hi", 1, 0, nil))
	c.AddChild(NewDynamicBorder(func(s string) string { return "<" + s + ">" }))
	got := c.Render(6)
	want := []string{"", "", " hi   ", "<──────>"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q want %q", got, want)
	}
	c.RemoveChild(sp)
	if len(c.Render(6)) != 2 {
		t.Fatal("RemoveChild failed")
	}
	c.Clear()
	if len(c.Render(6)) != 0 {
		t.Fatal("Clear failed")
	}
	if got := NewText("   ", 1, 1, nil).Render(5); len(got) != 0 {
		t.Fatalf("blank text renders %q", got)
	}
	if got := NewText("x", 1, 1, func(s string) string { return "[" + s + "]" }).Render(3); strings.Join(got, "|") != "[   ]|[ x ]|[   ]" {
		t.Fatalf("bg text %q", got)
	}
	if got := NewDynamicBorder(func(s string) string { return s }).Render(0); got[0] != "─" {
		t.Fatalf("width 0 border %q", got)
	}
}

func TestKeysAPI(t *testing.T) {
	if !MatchesKey("\t", KeyTab) || !MatchesKey("\x1b[Z", KeyShift("tab")) || !MatchesKey("\x1b[C", KeyRight) || !MatchesKey("\x1b[D", KeyLeft) {
		t.Fatal("MatchesKey")
	}
	if !IsKeyRepeat("\x1b[97;1:2u") || IsKeyRepeat("\x1b[200~:2u") || !IsKeyRelease("\x1b[97;1:3u") {
		t.Fatal("repeat/release")
	}
}

func TestNewKeybindingsExternalEditor(t *testing.T) {
	var kb Keybindings = NewKeybindings(nil)
	if !kb.Matches("\x07", KBAppEditorExternal) || !kb.Matches("\r", "tui.input.submit") {
		t.Fatal("defaults")
	}
	kb = NewKeybindings(map[string][]string{KBAppEditorExternal: {"ctrl+e"}})
	if kb.Matches("\x07", KBAppEditorExternal) || !kb.Matches("\x05", KBAppEditorExternal) {
		t.Fatal("override")
	}
}

func TestEditorHeadlessQuestionnairePaths(t *testing.T) {
	e := newTestEditor()
	e.DisableSubmit = true
	submitted := false
	e.OnSubmit = func(string) { submitted = true }
	e.HandleInput("a")
	e.HandleInput("😀")
	e.HandleInput("\r")
	if submitted || e.GetText() != "a😀" {
		t.Fatalf("DisableSubmit: submitted=%v text=%q", submitted, e.GetText())
	}
	if line, col := e.GetCursor(); line != 0 || col != 3 {
		t.Fatalf("cursor (%d,%d), want UTF-16 (0,3)", line, col)
	}
	e.HandleInput("\x1b[106;5u") // ctrl+j newline
	e.HandleInput("b")
	if got := e.GetLines(); len(got) != 2 || got[1] != "b" {
		t.Fatalf("lines %q", got)
	}
	e.HandleInput("\x1f") // undo
	if e.GetText() != "a😀\n" {
		t.Fatalf("undo: %q", e.GetText())
	}
	e.HandleInput("\x1b[200~" + strings.Repeat("x\n", 11) + "\x1b[201~")
	if !strings.Contains(e.GetText(), "[paste #1 +12 lines]") || !strings.Contains(e.GetExpandedText(), "x\nx\n") {
		t.Fatalf("paste marker/expansion: %q", e.GetText())
	}
	e.SetText("")
	if e.GetExpandedText() != "" {
		t.Fatal("SetText must clear paste map")
	}
}

func TestEditorRenderFocusedCursor(t *testing.T) {
	e := NewEditor(EditorTheme{BorderColor: func(s string) string { return "<" + s + ">" }})
	e.Focused = true
	e.SetText("ab")
	rows := e.Render(6)
	if rows[0] != "<──────>" || rows[2] != "<──────>" {
		t.Fatalf("borders %q", rows)
	}
	want := "ab" + CursorMarker + FakeCursorStart + " " + FakeCursorEnd + "   "
	if rows[1] != want {
		t.Fatalf("row %q want %q", rows[1], want)
	}
	if got := ResolveFakeCursors(rows, false)[1]; got != "ab"+CursorMarker+"\x1b[7m \x1b[27m   " {
		t.Fatalf("resolved %q", got)
	}
	if got := ResolveFakeCursors(rows, true)[1]; got != "ab"+CursorMarker+" "+"   " {
		t.Fatalf("hardware resolved %q", got)
	}
}
