package ui

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

func inlineOpts(buffer string, cursorOffset *int, contentWidth int) RenderInlineInputOptions {
	return RenderInlineInputOptions{
		Buffer:             buffer,
		CursorOffset:       cursorOffset,
		RowPrefix:          "❯ 1. ",
		ContinuationPrefix: "     ",
		ContentWidth:       contentWidth,
		SelectedText:       func(s string) string { return s },
	}
}

func TestInlineInput(t *testing.T) {
	const f = "view/components/inline-input"
	tw(t, f, "renders the cursor on a grapheme without splitting it", func(t *testing.T) {
		lines := RenderInlineInputRow(inlineOpts("hi😀bye", intp(2), 40))
		if !strings.Contains(lines[0], "hi"+cursorOn("😀")+"bye") {
			t.Fatalf("got %q", lines[0])
		}
	})
	tw(t, f, "uses a visible cursor cell at end-of-buffer", func(t *testing.T) {
		lines := RenderInlineInputRow(inlineOpts("hello", nil, 40))
		if !strings.Contains(lines[0], "hello"+cursorOn(nbsp)) {
			t.Fatalf("got %q", lines[0])
		}
	})
	tw(t, f, "wraps long logical lines within the available width", func(t *testing.T) {
		lines := RenderInlineInputRow(inlineOpts(strings.Repeat("a", 60), intp(60), 15))
		if len(lines) <= 1 {
			t.Fatalf("lines = %d", len(lines))
		}
		if !strings.HasPrefix(lines[0], "❯ 1. ") {
			t.Fatalf("got %q", lines[0])
		}
		for _, l := range lines {
			if widthx.VisibleWidth(l) > 20 {
				t.Fatalf("too wide: %q", l)
			}
		}
	})
	tw(t, f, "preserves explicit newlines and draws the cursor at a logical line end", func(t *testing.T) {
		lines := RenderInlineInputRow(inlineOpts("first\nsecond", intp(5), 40))
		if len(lines) != 2 {
			t.Fatalf("lines = %q", lines)
		}
		if !strings.Contains(lines[0], "first"+cursorOn(nbsp)) || !strings.Contains(lines[1], "second") {
			t.Fatalf("got %q", lines)
		}
	})
}
