package tui

import (
	"strings"
	"testing"
)

// Keys typed at the end of a line extend one buffer. Every string the editor handed out before (Text(), undo snapshots, rendered rows) keeps
// its content after later keys, an undo and typing again, and the text equals what Splice builds key by key.
func TestTypingAtTheEndOfALineKeepsEarlierStrings(t *testing.T) {
	editor := newTestEditor()
	editor.SetText("first line\nsecond")
	var seen []string
	var want []string
	expect := "first line\nsecond"
	typeKey := func(key string) {
		editor.HandleInput(key)
		seen = append(seen, editor.GetText())
		want = append(want, expect)
	}
	for _, key := range []string{"a", "b", " ", "c", "é", "😀", "d"} {
		expect += key
		typeKey(key)
	}
	row := editor.Render(40)[2]
	editor.HandleInput("\x1f") // undo back to the state before the space
	expect = "first line\nsecondab"
	if got := editor.GetText(); got != expect {
		t.Fatalf("after undo: %q, want %q", got, expect)
	}
	for _, key := range []string{"x", "y"} {
		expect += key
		typeKey(key)
	}
	editor.HandleInput("\x7f")
	expect = strings.TrimSuffix(expect, "y") + "z"
	typeKey("z")
	for i, text := range seen {
		if text != want[i] {
			t.Fatalf("text returned after key %d changed to %q, want %q", i, text, want[i])
		}
	}
	if !strings.Contains(row, "secondab cé😀d") {
		t.Fatalf("rendered row changed: %q", row)
	}
	if got := editor.GetText(); got != "first line\nsecondabxz" {
		t.Fatalf("text = %q", got)
	}
}
