package rpiv_todo

import "testing"

// The text helpers stand in for pi-tui's (the Go SDK has none); these pin their contract against
// pi-tui utils.js:1018-1139 (truncateToWidth) and the cell widths the overlay relies on.
func TestVisibleWidth(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"ascii", "hello", 5},
		{"ansi is free", "\x1b[38;5;5mhello\x1b[39m", 5},
		{"osc is free", "a\x1b]8;;http://x\x07b\x1b]8;;\x07", 2},
		{"wide CJK", "日本", 4},
		{"combining mark", "e\u0301", 1},
		{"box drawing", "├─ x", 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { eq(t, visibleWidth(c.in), c.want, c.in) })
	}
}

func TestTruncateToWidth(t *testing.T) {
	const reset = "\x1b[0m"
	t.Run("text that fits is unchanged", func(t *testing.T) {
		eq(t, truncateToWidth("hello", 5, "…"), "hello", "fits")
		eq(t, truncateToWidth("\x1b[2mhi\x1b[22m", 2, "…"), "\x1b[2mhi\x1b[22m", "ansi fits")
	})
	t.Run("a cut keeps the prefix, resets, adds the ellipsis and resets", func(t *testing.T) {
		eq(t, truncateToWidth("hello world", 8, "…"), "hello w"+reset+"…"+reset, "cut")
	})
	t.Run("a cut keeps the escape sequences before the kept text", func(t *testing.T) {
		eq(t, truncateToWidth("\x1b[2mhello world\x1b[22m", 6, "…"), "\x1b[2mhello"+reset+"…"+reset, "ansi cut")
	})
	t.Run("a wide rune that does not fit is dropped whole", func(t *testing.T) {
		eq(t, truncateToWidth("日本語", 4, "…"), "日"+reset+"…"+reset, "wide cut")
	})
	t.Run("degenerate widths", func(t *testing.T) {
		eq(t, truncateToWidth("hello", 0, "…"), "", "zero width")
		eq(t, truncateToWidth("", 5, "…"), "", "empty")
		eq(t, truncateToWidth("hello", 1, "…"), "…", "only the ellipsis fits")
	})
}

func TestWrapText(t *testing.T) {
	t.Run("short text is one line", func(t *testing.T) { eq(t, wrapText("todo + x", 40), []string{"todo + x"}, "lines") })
	t.Run("breaks at spaces and keeps every word", func(t *testing.T) {
		eq(t, wrapText("aaa bbb ccc", 7), []string{"aaa bbb", "ccc"}, "lines")
	})
	t.Run("a styled span carries across the break", func(t *testing.T) {
		got := wrapText("\x1b[2maaa bbb ccc\x1b[22m", 7)
		eq(t, got, []string{"\x1b[2maaa bbb\x1b[0m", "\x1b[2mccc\x1b[22m"}, "lines")
	})
	t.Run("a word longer than the width is cut", func(t *testing.T) {
		eq(t, wrapText("abcdefghij", 4), []string{"abcd", "efgh", "ij"}, "lines")
	})
	t.Run("a hard line break stays", func(t *testing.T) { eq(t, wrapText("a\nb", 10), []string{"a", "b"}, "lines") })
}
