package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

func TestUpstreamEditorScrollIndicators(t *testing.T) {
	for _, tc := range []struct {
		name  string
		width int
	}{
		// packages/tui/test/editor.test.ts:707.
		{"centers scroll indicators on wide borders", 40},
		// packages/tui/test/editor.test.ts:720.
		{"keeps truncated scroll indicators within width and preserves their color (issue #6962)", 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newUpstreamWrappingEditor()
			borderColor := func(text string) string { return "\x1b[35m" + text + "\x1b[39m" }
			if tc.width == 10 {
				e.theme.BorderColor = borderColor
			}
			text := make([]string, 20)
			for i := range text {
				text[i] = fmt.Sprintf("line %d", i)
			}
			e.SetText(strings.Join(text, "\n"))
			renderEditor(e, tc.width)
			for range 10 {
				e.HandleInput("\x1b[A")
			}
			lines := renderEditor(e, tc.width)
			top, bottom := lines[0], lines[len(lines)-1]
			if tc.width == 40 {
				for i, row := range []string{top, bottom} {
					label := []string{" ↑ 9 more ", " ↓ 4 more "}[i]
					want := strings.Repeat("─", 15) + label + strings.Repeat("─", 15)
					if got := widthx.StripAnsi(row); got != want {
						t.Errorf("border = %q, want %q", got, want)
					}
				}
			} else {
				for i, row := range []string{top, bottom} {
					plain := widthx.StripAnsi(row)
					if prefix := []string{"─── ↑", "─── ↓"}[i]; !strings.HasPrefix(plain, prefix) {
						t.Errorf("border = %q, want prefix %q", plain, prefix)
					}
					if want := borderColor(plain); row != want {
						t.Errorf("colored border = %q, want %q", row, want)
					}
					// Strengthen the original prefix check with the exact Pi :276-295 truncation bytes.
					if want := []string{"─── ↑ 9...", "─── ↓ 4..."}[i]; plain != want {
						t.Errorf("truncated border = %q, want %q", plain, want)
					}
				}
				assertWrappingWidths(t, lines, tc.width)
			}
		})
	}
}
