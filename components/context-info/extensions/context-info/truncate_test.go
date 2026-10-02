package contextinfo

import (
	"strings"
	"testing"
)

// A user's narrow pane (114 columns) panicked pig with "Rendered line 11 exceeds terminal width (128 > 114)":
// the footer's first segment (cwd and git branch) is never dropped by fitSegments, so a long path alone was
// wider than the terminal. Every footer line must fit the reported width, cut the way Pi's truncateToWidth cuts:
// by visible columns, keeping styling, ending in an ellipsis.
func TestFooterLineNeverExceedsTheRenderWidth(t *testing.T) {
	cwd := "~/" + strings.Repeat("deeply/nested/project/", 6) + "app" // far wider than 114
	gitPart := " " + dim("│") + " " + accent("Git:") + " " + muted("feature/some-very-long-branch-name") + " " + dim("│")
	segs := []string{muted(cwd) + gitPart, accent("Context:") + " " + muted("12.3%/200k"), accent("Model:") + " " + muted("m")}
	if w := visibleWidth(segs[0]); w <= 114 {
		t.Fatalf("test setup: first segment is only %d wide", w)
	}
	for _, width := range []int{114, 80, 40, 10, 2, 1} {
		got := fitSegments(segs, "  "+dim("│")+" ", width)
		if w := visibleWidth(got); w > width {
			t.Errorf("width %d: line is %d wide: %q", width, w, got)
		}
	}
}

func TestTruncateToWidthCutsByColumnsKeepsStylingAndEndsWithAnEllipsis(t *testing.T) {
	styled := accent("Context:") + " " + muted("abcdefghijklmnopqrstuvwxyz")
	got := truncateToWidth(styled, 12)
	if w := visibleWidth(got); w != 12 {
		t.Errorf("width = %d, want 12: %q", w, got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("no ellipsis: %q", got)
	}
	if strings.Count(got, ansiReset) == 0 || !strings.Contains(got[:strings.Index(got, "…")], ansiReset) {
		t.Errorf("styling must be closed before the ellipsis: %q", got)
	}
	// Whole fits are returned as they are, and an unknown width (0) never cuts.
	if got := truncateToWidth(styled, visibleWidth(styled)); got != styled {
		t.Errorf("a line that fits changed: %q", got)
	}
	if got := truncateToWidth(styled, 0); got != styled {
		t.Errorf("width 0 must not cut: %q", got)
	}
	// A double-width rune is never split: 3 columns hold one wide rune plus the ellipsis.
	wide := truncateToWidth("日本語のパス", 4)
	if visibleWidth(wide) > 4 || !strings.HasSuffix(wide, "…") {
		t.Errorf("wide runes: %q is %d wide", wide, visibleWidth(wide))
	}
}
