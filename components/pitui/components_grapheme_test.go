package tui

import (
	"testing"
	"unicode/utf16"
)

func TestEditorGrapheme_LeftRightMovement(t *testing.T) {
	cases := []struct {
		name    string
		text    string
		wantMid int
		wantEnd int
	}{
		{name: "ascii", text: "ab", wantMid: 1, wantEnd: 2},
		{name: "emoji", text: "😊a", wantMid: 2, wantEnd: 3},
		{name: "zwj", text: "👩‍💻a", wantMid: 5, wantEnd: 6},
		{name: "skin-tone", text: "👋🏽a", wantMid: 4, wantEnd: 5},
		{name: "flag", text: "🇺🇸a", wantMid: 4, wantEnd: 5},
		{name: "cjk", text: "界a", wantMid: 1, wantEnd: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEditor()
			e.SetText(tc.text)
			e.cursor = [2]int{0, 0}
			e.HandleInput("\033[C")
			if e.cursor[1] != tc.wantMid {
				t.Fatalf("after right cursor=%d want=%d for %q", e.cursor[1], tc.wantMid, tc.text)
			}
			e.HandleInput("\033[C")
			if e.cursor[1] != tc.wantEnd {
				t.Fatalf("after second right cursor=%d want=%d for %q", e.cursor[1], tc.wantEnd, tc.text)
			}
			e.HandleInput("\033[D")
			if e.cursor[1] != tc.wantMid {
				t.Fatalf("after left cursor=%d want=%d for %q", e.cursor[1], tc.wantMid, tc.text)
			}
		})
	}
}

func TestEditorGrapheme_BackspaceAndDelete(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{name: "emoji", text: "A😊B"},
		{name: "zwj", text: "A👩‍💻B"},
		{name: "skin-tone", text: "A👋🏽B"},
		{name: "flag", text: "A🇺🇸B"},
	}
	for _, tc := range cases {
		t.Run(tc.name+"/backspace", func(t *testing.T) {
			e := newTestEditor()
			e.SetText(tc.text)
			e.cursor = [2]int{0, len(utf16.Encode([]rune(tc.text))) - 1}
			e.HandleInput("\x7f")
			if got := e.GetText(); got != "AB" {
				t.Fatalf("backspace got=%q want=%q", got, "AB")
			}
		})
		t.Run(tc.name+"/delete", func(t *testing.T) {
			e := newTestEditor()
			e.SetText(tc.text)
			e.cursor = [2]int{0, len("A")}
			e.HandleInput("\x1b[3~")
			if got := e.GetText(); got != "AB" {
				t.Fatalf("delete got=%q want=%q", got, "AB")
			}
		})
	}
}

func TestWordWrapLineGraphemeBoundary(t *testing.T) {
	chunks := wordWrapLine("👩‍💻a", 2, nil)
	if len(chunks) != 2 {
		t.Fatalf("len(chunks)=%d want 2 (%v)", len(chunks), chunks)
	}
	if chunks[0].text != "👩‍💻" || chunks[0].startIndex != 0 {
		t.Fatalf("first chunk=%+v", chunks[0])
	}
	if chunks[1].text != "a" || chunks[1].startIndex != 5 {
		t.Fatalf("second chunk=%+v want UTF-16 start=5", chunks[1])
	}
}

func TestEditorWordMovementPunctuationAndCrossLine(t *testing.T) {
	e := newTestEditor()
	e.SetText("foo, bar.\nbaz")
	e.cursor = [2]int{0, len("foo, bar.")}
	e.HandleInput("\x1bb")
	if e.cursor != [2]int{0, len("foo, bar")} {
		t.Fatalf("alt+b from trailing punctuation cursor=%v want [0 %d]", e.cursor, len("foo, bar"))
	}
	e.HandleInput("\x1bb")
	if e.cursor != [2]int{0, len("foo, ")} {
		t.Fatalf("second alt+b cursor=%v want [0 %d]", e.cursor, len("foo, "))
	}
	e.cursor = [2]int{0, len("foo, bar.")}
	e.HandleInput("\x1b[1;3C")
	if e.cursor != [2]int{1, 0} {
		t.Fatalf("alt+right at end of line should move to next line start, got %v", e.cursor)
	}
}
