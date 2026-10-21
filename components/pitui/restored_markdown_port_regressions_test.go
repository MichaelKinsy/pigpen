package tui

import (
	"slices"
	"strings"
	"testing"
)

func TestMarkdownBackgroundCallbackOrder(t *testing.T) {
	var calls []string
	md := testMDWithOptions("alpha\nbeta", 1, 2, mdUpstreamTheme(), &DefaultTextStyle{BgColor: func(s string) string { calls = append(calls, s); return s }}, nil)
	lines := md.Render(12)
	want := []string{" alpha      ", " beta       ", strings.Repeat(" ", 12), strings.Repeat(" ", 12)}
	if !slices.Equal(calls, want) {
		t.Fatalf("background calls=%q, want %q", calls, want)
	}
	if len(lines) != 6 {
		t.Fatalf("rows=%d, want 6", len(lines))
	}
}
