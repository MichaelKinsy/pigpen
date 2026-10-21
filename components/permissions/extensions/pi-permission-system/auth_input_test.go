package pi_permission_system

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

// Tests of LineInput, the port of pi-tui's Input. The differential (auth_oracle_test.go) compares it with the original's through the
// dialog; these pin the behaviours a reason depends on.

func typed(in *LineInput, keys ...string) *LineInput {
	for _, k := range keys {
		in.HandleInput(k)
	}
	return in
}

func TestLineInput(t *testing.T) {
	t.Run("types, deletes, moves and undoes", func(t *testing.T) {
		in := typed(NewLineInput(nil), "a", "b", "c", "\x1b[D", "X", "\x7f", "\x1b[3~")
		eq(t, in.Value(), "ab", "value")
		typed(in, "\x1f") // ctrl+- undoes the last edit
		eq(t, in.Value(), "abc", "after undo")
	})
	t.Run("a wide or combining grapheme is one backspace", func(t *testing.T) {
		in := typed(NewLineInput(nil), "a", "e\u0301", "\U0001F600", "\x7f", "\x7f")
		eq(t, in.Value(), "a", "value")
	})
	t.Run("kill to line start and yank", func(t *testing.T) {
		in := typed(NewLineInput(nil), "h", "e", "l", "l", "o", "\x15") // ctrl+u
		eq(t, in.Value(), "", "after kill")
		typed(in, "\x19") // ctrl+y
		eq(t, in.Value(), "hello", "after yank")
	})
	t.Run("a bracketed paste drops newlines and expands tabs", func(t *testing.T) {
		in := typed(NewLineInput(nil), "\x1b[200~a\r\nb\tc\x1b[201~")
		eq(t, in.Value(), "ab    c", "value")
	})
	t.Run("a paste split across chunks is buffered until its end marker", func(t *testing.T) {
		in := typed(NewLineInput(nil), "\x1b[200~par", "tial")
		eq(t, in.Value(), "", "before the end marker")
		typed(in, "\x1b[201~tail")
		eq(t, in.Value(), "partialtail", "after")
	})
	t.Run("control characters are not inserted", func(t *testing.T) {
		in := typed(NewLineInput(nil), "a", "\x00", "\x01\x02", "\x7f\x7f\x7f", "b")
		_ = in
		eq(t, typed(NewLineInput(nil), "a", "\x00", "b").Value(), "ab", "value")
	})
	t.Run("submit and escape reach their callbacks", func(t *testing.T) {
		in := NewLineInput(nil)
		var submitted []string
		escaped := 0
		in.OnSubmit = func(v string) { submitted = append(submitted, v) }
		in.OnEscape = func() { escaped++ }
		typed(in, "x", "\r", "\n", "\x1b")
		eq(t, submitted, []string{"x", "x"}, "submitted")
		eq(t, escaped, 1, "escaped")
	})
	t.Run("a Kitty shift-letter press types the capital (PiG divergence D185; the original types the base letter)", func(t *testing.T) {
		eq(t, typed(NewLineInput(nil), "\x1b[97;2u").Value(), "A", "value")
	})
	t.Run("a Kitty plain press types the letter", func(t *testing.T) {
		eq(t, typed(NewLineInput(nil), "\x1b[97u").Value(), "a", "value")
	})
	t.Run("renders exactly one row, scrolled around the cursor, never wider than the width", func(t *testing.T) {
		in := NewLineInput(nil)
		in.Focused = true
		typed(in, strings.Repeat("q", 200))
		for _, w := range []int{1, 2, 3, 10, 40} {
			lines := in.Render(w)
			eq(t, len(lines), 1, "rows")
			if widthx.VisibleWidth(lines[0]) > w {
				t.Errorf("width %d: %q", w, lines[0])
			}
		}
		if !strings.Contains(in.Render(40)[0], widthx.CursorMarker) {
			t.Error("a focused input emits the cursor marker")
		}
		in.Focused = false
		if strings.Contains(in.Render(40)[0], widthx.CursorMarker) {
			t.Error("an unfocused input must not")
		}
	})
}
