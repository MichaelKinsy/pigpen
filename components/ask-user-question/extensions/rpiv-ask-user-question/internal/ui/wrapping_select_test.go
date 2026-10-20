package ui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

var wsIdentityTheme = WrappingSelectTheme{
	SelectedText: func(s string) string { return s },
	Description:  func(s string) string { return s },
	ScrollInfo:   func(s string) string { return s },
}

var wsMarkedTheme = WrappingSelectTheme{
	SelectedText: func(s string) string { return "<S>" + s + "</S>" },
	Description:  func(s string) string { return s },
	ScrollInfo:   func(s string) string { return s },
}

func wsOpt(label string) WrappingSelectItem {
	return WrappingSelectItem{Kind: KindOption, Label: label}
}

func wsOther(label string) WrappingSelectItem {
	return WrappingSelectItem{Kind: KindOther, Label: label}
}

// wsFocusedOther is a one-row `other` select, focused, with the given buffer.
func wsFocusedOther(theme WrappingSelectTheme, buffer string, opts WrappingSelectOptions) *WrappingSelect {
	s := NewWrappingSelect([]WrappingSelectItem{wsOther("pick")}, 1, theme, opts)
	s.SetSelectedIndex(0)
	s.SetFocused(true)
	s.SetInputBuffer(buffer)
	return s
}

func wsRows(n int) []WrappingSelectItem {
	items := make([]WrappingSelectItem, n)
	for i := range items {
		items[i] = wsOpt(fmt.Sprintf("row-%d", i+1))
	}
	return items
}

func wsNoneWider(t *testing.T, lines []string, width int) {
	t.Helper()
	for _, l := range lines {
		if widthx.VisibleWidth(l) > width {
			t.Fatalf("line %q wider than %d", l, width)
		}
	}
}

func wsHas(t *testing.T, line, sub string) {
	t.Helper()
	if !strings.Contains(line, sub) {
		t.Fatalf("line %q does not contain %q", line, sub)
	}
}

func wsHasNot(t *testing.T, line, sub string) {
	t.Helper()
	if strings.Contains(line, sub) {
		t.Fatalf("line %q contains %q", line, sub)
	}
}

func TestWrappingSelect(t *testing.T) {
	const f = "view/components/wrapping-select"
	ab := []WrappingSelectItem{wsOpt("a"), wsOpt("b")}

	tw(t, f, "clamps negative to 0", func(t *testing.T) {
		s := NewWrappingSelect(ab, 10, wsIdentityTheme, WrappingSelectOptions{})
		s.SetSelectedIndex(-5)
		wsHas(t, s.Render(40)[0], "❯ 1. a")
	})
	tw(t, f, "clamps above-max to last", func(t *testing.T) {
		s := NewWrappingSelect(ab, 10, wsIdentityTheme, WrappingSelectOptions{})
		s.SetSelectedIndex(99)
		wsHas(t, s.Render(40)[1], "❯ 2. b")
	})

	items20 := wsRows(20)
	tw(t, f, "renders all items when count <= maxVisible", func(t *testing.T) {
		s := NewWrappingSelect(items20[:3], 10, wsIdentityTheme, WrappingSelectOptions{})
		n := 0
		for _, l := range s.Render(40) {
			if strings.Contains(l, "row-") {
				n++
			}
		}
		if n != 3 {
			t.Fatalf("n = %d", n)
		}
	})
	tw(t, f, "shows scroll indicator when items exceed maxVisible", func(t *testing.T) {
		s := NewWrappingSelect(items20, 5, wsIdentityTheme, WrappingSelectOptions{})
		s.SetSelectedIndex(10)
		if !anyHas(s.Render(40), "(11/20)") {
			t.Fatal("no scroll indicator")
		}
	})
	tw(t, f, "centers window around selectedIndex", func(t *testing.T) {
		s := NewWrappingSelect(items20, 5, wsIdentityTheme, WrappingSelectOptions{})
		s.SetSelectedIndex(10)
		lines := s.Render(40)
		if !anyMatch(lines, regexp.MustCompile(`\brow-9\b`)) || !anyMatch(lines, regexp.MustCompile(`\brow-11\b`)) ||
			anyMatch(lines, regexp.MustCompile(`\brow-1\b`)) {
			t.Fatalf("lines = %q", lines)
		}
	})
	tw(t, f, "returns empty array for zero items", func(t *testing.T) {
		s := NewWrappingSelect(nil, 5, wsIdentityTheme, WrappingSelectOptions{})
		if got := s.Render(40); got == nil || len(got) != 0 {
			t.Fatalf("got %#v", got)
		}
	})

	inlineInputLines := func(lines []string) []string {
		re := regexp.MustCompile(`[a-z0-9]`)
		var out []string
		for _, l := range lines {
			if strings.Contains(l, cursorMarker) || re.MatchString(l) {
				out = append(out, l)
			}
		}
		return out
	}

	tw(t, f, "renders inline input row with cursor when kind:'other' item focused", func(t *testing.T) {
		lines := wsFocusedOther(wsIdentityTheme, "hi", WrappingSelectOptions{}).Render(40)
		wsHas(t, lines[0], "hi")
		wsHas(t, lines[0], cursorMarker)
	})
	tw(t, f, "renders the custom draft (without a cursor) when kind:'other' is not focused", func(t *testing.T) {
		s := NewWrappingSelect([]WrappingSelectItem{wsOther("pick")}, 1, wsIdentityTheme, WrappingSelectOptions{})
		s.SetFocused(false)
		s.SetInputBuffer("draft")
		lines := s.Render(40)
		wsHas(t, lines[0], "draft")
		wsHasNot(t, lines[0], "pick")
		wsHasNot(t, lines[0], cursorMarker)
	})
	tw(t, f, "falls back to the Type something label when the unfocused draft is empty", func(t *testing.T) {
		s := NewWrappingSelect([]WrappingSelectItem{wsOther("pick")}, 1, wsIdentityTheme, WrappingSelectOptions{})
		s.SetFocused(false)
		s.SetInputBuffer("")
		wsHas(t, s.Render(40)[0], "pick")
	})
	tw(t, f, "shows a changed custom draft without the prior answer's confirmation mark", func(t *testing.T) {
		s := NewWrappingSelect([]WrappingSelectItem{wsOther("pick")}, 1, wsIdentityTheme, WrappingSelectOptions{})
		s.SetFocused(false)
		s.SetConfirmedIndex(intp(0), strp("confirmed"))
		s.SetInputBuffer("new draft")
		line := s.Render(40)[0]
		wsHas(t, line, "new draft")
		wsHasNot(t, line, "confirmed")
		wsHasNot(t, line, "✔")
	})
	tw(t, f, "wraps inline input row across multiple lines when input exceeds width", func(t *testing.T) {
		s := wsFocusedOther(wsIdentityTheme, "this is a really long input that exceeds the width", WrappingSelectOptions{})
		lines := s.Render(20)
		wsNoneWider(t, lines, 20)
		if len(inlineInputLines(lines)) <= 1 {
			t.Fatalf("lines = %q", lines)
		}
	})
	tw(t, f, "keeps every line ≤ width when input pushes the row just past width (off-by-one boundary)", func(t *testing.T) {
		wsNoneWider(t, wsFocusedOther(wsIdentityTheme, strings.Repeat("a", 16), WrappingSelectOptions{}).Render(20), 20)
	})
	tw(t, f, "keeps every line ≤ width when input is much longer than width", func(t *testing.T) {
		lines := wsFocusedOther(wsIdentityTheme, strings.Repeat("x", 200), WrappingSelectOptions{}).Render(10)
		wsNoneWider(t, lines, 10)
		if len(inlineInputLines(lines)) <= 2 {
			t.Fatalf("lines = %q", lines)
		}
	})
	tw(t, f, "keeps every line ≤ width when input contains wide (emoji) characters", func(t *testing.T) {
		wsNoneWider(t, wsFocusedOther(wsIdentityTheme, strings.Repeat("😀", 30), WrappingSelectOptions{}).Render(20), 20)
	})
	tw(t, f, "keeps every line ≤ width when number column inflates the prefix", func(t *testing.T) {
		total := 1000
		wsNoneWider(t, wsFocusedOther(wsIdentityTheme, "hello world", WrappingSelectOptions{TotalItemsForNumbering: &total}).Render(12), 12)
	})
	tw(t, f, "renders inline input row within width when input is empty", func(t *testing.T) {
		wsNoneWider(t, wsFocusedOther(wsIdentityTheme, "", WrappingSelectOptions{}).Render(12)[:1], 12)
	})
	tw(t, f, "renders cursor at end-of-buffer when no offset is set (default fallback)", func(t *testing.T) {
		lines := wsFocusedOther(wsIdentityTheme, "this input is long enough that it definitely wraps across multiple lines", WrappingSelectOptions{}).Render(20)
		n := 0
		for _, l := range lines {
			if strings.Contains(l, cursorMarker) {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("cursor lines = %d", n)
		}
		wsHas(t, lines[len(lines)-1], cursorMarker)
	})
	tw(t, f, "renders rowPrefix on first line and aligned continuation whitespace on wrapped lines", func(t *testing.T) {
		lines := wsFocusedOther(wsIdentityTheme, strings.Repeat("a", 60), WrappingSelectOptions{}).Render(20)
		if !strings.HasPrefix(lines[0], "❯ 1. ") {
			t.Fatalf("first = %q", lines[0])
		}
		for _, line := range lines[1:] {
			wsHasNot(t, line, "❯")
			if !strings.HasPrefix(line, "     ") {
				t.Fatalf("continuation %q", line)
			}
		}
	})
	tw(t, f, "applies selectedText theme to every wrapped line of the inline input", func(t *testing.T) {
		lines := wsFocusedOther(wsMarkedTheme, strings.Repeat("a", 60), WrappingSelectOptions{}).Render(20)
		if len(lines) <= 1 {
			t.Fatalf("lines = %q", lines)
		}
		for _, l := range lines {
			if !strings.HasPrefix(l, "<S>") || !strings.HasSuffix(l, "</S>") {
				t.Fatalf("line %q", l)
			}
		}
	})

	cursorAt := func(buffer string, offset *int) *WrappingSelect {
		s := wsFocusedOther(wsIdentityTheme, buffer, WrappingSelectOptions{})
		s.SetInputCursorOffset(offset)
		return s
	}
	tw(t, f, "renders cursor at position 0 (start of buffer)", func(t *testing.T) {
		wsHas(t, cursorAt("hello", intp(0)).Render(40)[0], cursorOn("h")+"ello")
	})
	tw(t, f, "renders cursor mid-string", func(t *testing.T) {
		wsHas(t, cursorAt("hello", intp(2)).Render(40)[0], "he"+cursorOn("l")+"lo")
	})
	tw(t, f, "renders cursor at end of buffer", func(t *testing.T) {
		wsHas(t, cursorAt("hello", intp(5)).Render(40)[0], "hello"+cursorOn(nbsp))
	})
	tw(t, f, "falls back to end-of-buffer when offset is undefined", func(t *testing.T) {
		wsHas(t, cursorAt("hello", nil).Render(40)[0], "hello"+cursorOn(nbsp))
	})
	tw(t, f, "wraps correctly with cursor mid-string on narrow width", func(t *testing.T) {
		lines := cursorAt("abcdefghijk", intp(3)).Render(15)
		wsHas(t, lines[0], "abc"+cursorOn("d")+"efghij")
		wsHas(t, lines[0], cursorMarker)
		wsNoneWider(t, lines, 15)
	})
	tw(t, f, "clamps negative offset to end-of-buffer fallback", func(t *testing.T) {
		wsHas(t, cursorAt("hello", intp(-1)).Render(40)[0], "hello"+cursorOn(nbsp))
	})
	tw(t, f, "clamps offset exceeding buffer length to end-of-buffer fallback", func(t *testing.T) {
		wsHas(t, cursorAt("hello", intp(99)).Render(40)[0], "hello"+cursorOn(nbsp))
	})
	tw(t, f, "renders cursor on emoji without splitting surrogate pair", func(t *testing.T) {
		wsHas(t, cursorAt("hi😀bye", intp(2)).Render(40)[0], "hi"+cursorOn("😀")+"bye")
	})
	tw(t, f, "renders cursor on ZWJ emoji sequence without splitting the cluster", func(t *testing.T) {
		family := "👨‍👩‍👧"
		wsHas(t, cursorAt("a"+family+"b", intp(1)).Render(40)[0], "a"+cursorOn(family)+"b")
	})
	tw(t, f, "renders cursor at correct position with wrapping across multiple lines", func(t *testing.T) {
		lines := cursorAt("this is a very long input that will wrap", intp(5)).Render(20)
		wsHas(t, lines[0], cursorMarker)
		wsNoneWider(t, lines, 20)
	})

	tw(t, f, "pads numbers to width of total count", func(t *testing.T) {
		items := make([]WrappingSelectItem, 12)
		for i := range items {
			items[i] = wsOpt(fmt.Sprintf("r%d", i+1))
		}
		lines := NewWrappingSelect(items, 20, wsIdentityTheme, WrappingSelectOptions{}).Render(40)
		wsHas(t, lines[0], " 1. ")
		wsHas(t, lines[9], "10. ")
	})
	tw(t, f, "uses numberStartOffset for numbering (continues a prior list's sequence)", func(t *testing.T) {
		total := 10
		s := NewWrappingSelect([]WrappingSelectItem{wsOpt("chat")}, 1, wsIdentityTheme, WrappingSelectOptions{NumberStartOffset: 5, TotalItemsForNumbering: &total})
		wsHas(t, s.Render(40)[0], " 6. chat")
	})
	tw(t, f, "setNumbering(offset, total) updates numbering in place (driven by tab switches)", func(t *testing.T) {
		total := 1
		s := NewWrappingSelect([]WrappingSelectItem{wsOpt("chat")}, 1, wsIdentityTheme, WrappingSelectOptions{NumberStartOffset: 0, TotalItemsForNumbering: &total})
		wsHas(t, s.Render(40)[0], "❯ 1. chat")
		s.SetNumbering(3, 4)
		wsHas(t, s.Render(40)[0], "❯ 4. chat")
	})

	tw(t, f, "renders description lines under label", func(t *testing.T) {
		s := NewWrappingSelect([]WrappingSelectItem{{Kind: KindOption, Label: "L", Description: "desc-line"}}, 2, wsIdentityTheme, WrappingSelectOptions{})
		if !anyHas(s.Render(40), "desc-line") {
			t.Fatal("no description")
		}
	})
	tw(t, f, "omits description block when absent", func(t *testing.T) {
		s := NewWrappingSelect([]WrappingSelectItem{wsOpt("L")}, 1, wsIdentityTheme, WrappingSelectOptions{})
		if n := len(s.Render(40)); n != 1 {
			t.Fatalf("n = %d", n)
		}
	})

	ABC := []WrappingSelectItem{wsOpt("Alpha"), wsOpt("Beta"), wsOpt("Gamma")}
	tw(t, f, "renders ` ✔` on the confirmed row in selectedText styling, no pointer", func(t *testing.T) {
		s := NewWrappingSelect(ABC, 10, wsMarkedTheme, WrappingSelectOptions{})
		s.SetSelectedIndex(0)
		s.SetFocused(true)
		s.SetConfirmedIndex(intp(1), nil)
		lines := s.Render(40)
		wsHas(t, lines[0], "❯ 1. Alpha")
		wsHas(t, lines[1], "  2. Beta ✔")
		wsHas(t, lines[1], "<S>")
		wsHas(t, lines[1], "</S>")
		wsHasNot(t, lines[1], "❯")
		if lines[2] != "  3. Gamma" {
			t.Fatalf("line 2 = %q", lines[2])
		}
	})
	tw(t, f, "renders both ❯ and ✔ when cursor lands on the confirmed row (e.g. prior answer was row 0)", func(t *testing.T) {
		s := NewWrappingSelect(ABC[:2], 10, wsMarkedTheme, WrappingSelectOptions{})
		s.SetSelectedIndex(1)
		s.SetFocused(true)
		s.SetConfirmedIndex(intp(1), nil)
		lines := s.Render(40)
		wsHas(t, lines[1], "❯ 2. Beta ✔")
		wsHas(t, lines[1], "<S>")
	})
	tw(t, f, "undefined clears the marker (default behavior preserved)", func(t *testing.T) {
		s := NewWrappingSelect([]WrappingSelectItem{wsOpt("A"), wsOpt("B")}, 10, wsMarkedTheme, WrappingSelectOptions{})
		s.SetConfirmedIndex(intp(1), nil)
		s.SetConfirmedIndex(nil, nil)
		wsHasNot(t, joinLines(s.Render(40)), "✔")
	})
	tw(t, f, "labelOverride replaces the static label (e.g. `Hello ✔` on kind:'other' row)", func(t *testing.T) {
		s := NewWrappingSelect([]WrappingSelectItem{wsOpt("Alpha"), wsOpt("Beta"), wsOther("Type something.")}, 10, wsMarkedTheme, WrappingSelectOptions{})
		s.SetSelectedIndex(0)
		s.SetFocused(true)
		s.SetConfirmedIndex(intp(2), strp("Hello"))
		lines := s.Render(40)
		wsHas(t, lines[2], "Hello ✔")
		wsHasNot(t, lines[2], "Type something.")
		wsHas(t, lines[2], "<S>")
	})
	tw(t, f, "when focused on kind:'other' row, inline-input rendering wins over confirmed marker", func(t *testing.T) {
		s := NewWrappingSelect([]WrappingSelectItem{wsOpt("Alpha"), wsOther("Type something.")}, 10, wsMarkedTheme, WrappingSelectOptions{})
		s.SetSelectedIndex(1)
		s.SetFocused(true)
		s.SetConfirmedIndex(intp(1), strp("Hello"))
		s.SetInputBuffer("World")
		lines := s.Render(40)
		wsHas(t, lines[1], "World")
		wsHas(t, lines[1], cursorMarker)
		wsHasNot(t, lines[1], "✔")
	})
	tw(t, f, "clamps index to valid range", func(t *testing.T) {
		s := NewWrappingSelect([]WrappingSelectItem{wsOpt("A"), wsOpt("B")}, 10, wsMarkedTheme, WrappingSelectOptions{})
		s.SetSelectedIndex(0)
		s.SetFocused(true)
		s.SetConfirmedIndex(intp(99), nil)
		wsHas(t, s.Render(40)[1], "B ✔")
	})
	tw(t, f, "respects width — wrappable label + ` ✔` does not exceed width per line", func(t *testing.T) {
		wrappable := "alpha beta gamma delta epsilon zeta eta theta"
		s := NewWrappingSelect([]WrappingSelectItem{wsOpt("A"), wsOpt(wrappable)}, 10, wsIdentityTheme, WrappingSelectOptions{})
		s.SetSelectedIndex(0)
		s.SetFocused(true)
		s.SetConfirmedIndex(intp(1), nil)
		lines := s.Render(20)
		wsNoneWider(t, lines, 20)
		if !anyHas(lines, "✔") {
			t.Fatal("no mark")
		}
	})

	rng := func(t *testing.T, s *WrappingSelect, w int) (int, int) {
		t.Helper()
		return s.FocusedItemRowRange(w)
	}
	tw(t, f, "returns [0, 0] for empty items", func(t *testing.T) {
		a, b := rng(t, NewWrappingSelect(nil, 10, wsIdentityTheme, WrappingSelectOptions{}), 40)
		if a != 0 || b != 0 {
			t.Fatal(a, b)
		}
	})
	tw(t, f, "returns [0, 1] for single item focused at index 0", func(t *testing.T) {
		a, b := rng(t, NewWrappingSelect([]WrappingSelectItem{wsOpt("A")}, 10, wsIdentityTheme, WrappingSelectOptions{}), 40)
		if a != 0 || b != 1 {
			t.Fatal(a, b)
		}
	})
	tw(t, f, "returns correct range for focused item at index 1", func(t *testing.T) {
		s := NewWrappingSelect([]WrappingSelectItem{wsOpt("A"), wsOpt("B")}, 10, wsIdentityTheme, WrappingSelectOptions{})
		s.SetSelectedIndex(1)
		a, b := rng(t, s, 40)
		if a != 1 || b != 2 {
			t.Fatal(a, b)
		}
	})
	tw(t, f, "accounts for description rows in item before focused", func(t *testing.T) {
		s := NewWrappingSelect([]WrappingSelectItem{
			{Kind: KindOption, Label: "A", Description: "multi-word description text that is moderately long"}, wsOpt("B"),
		}, 10, wsIdentityTheme, WrappingSelectOptions{})
		s.SetSelectedIndex(1)
		a, b := rng(t, s, 40)
		if a <= 1 || b != a+1 {
			t.Fatal(a, b)
		}
	})
	tw(t, f, "accounts for focused item's own description rows", func(t *testing.T) {
		s := NewWrappingSelect([]WrappingSelectItem{wsOpt("A"), {Kind: KindOption, Label: "B", Description: "desc text"}, wsOpt("C")}, 10, wsIdentityTheme, WrappingSelectOptions{})
		s.SetSelectedIndex(1)
		a, b := rng(t, s, 40)
		if a != 1 || b <= a+1 {
			t.Fatal(a, b)
		}
	})
	tw(t, f, "handles items in visible window (windowed)", func(t *testing.T) {
		s := NewWrappingSelect(wsRows(20), 5, wsIdentityTheme, WrappingSelectOptions{})
		s.SetSelectedIndex(10)
		a, b := rng(t, s, 40)
		if a != 2 || b != 3 {
			t.Fatal(a, b)
		}
	})
	tw(t, f, "range matches actual rendered output position", func(t *testing.T) {
		s := NewWrappingSelect([]WrappingSelectItem{
			{Kind: KindOption, Label: "A", Description: "a longer description that may wrap around"}, wsOpt("B"), wsOpt("C"),
		}, 10, wsIdentityTheme, WrappingSelectOptions{})
		s.SetSelectedIndex(1)
		a, b := rng(t, s, 30)
		rendered := s.Render(30)
		wsHas(t, rendered[a], "B")
		wsHasNot(t, rendered[b-1], "C")
	})

	// it.each upstream: the ledger does not list parametrized titles, so this is a plain subtest, not a tw twin.
	t.Run("shouldRenderAsInlineInput is true only for kind 'other' when active", func(t *testing.T) {
		for _, item := range []WrappingSelectItem{wsOpt("opt"), wsOther("Type something."), {Kind: KindNext, Label: "Next"}} {
			s := NewWrappingSelect([]WrappingSelectItem{item}, 1, wsIdentityTheme, WrappingSelectOptions{})
			s.SetSelectedIndex(0)
			s.SetFocused(true)
			hasInputCursor := anyHas(s.Render(20), cursorMarker)
			if hasInputCursor != (item.Kind == KindOther) {
				t.Fatalf("kind %s: cursor = %v", item.Kind, hasInputCursor)
			}
		}
	})
}
