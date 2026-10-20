package tui

import (
	"slices"
	"testing"
)

func TestMarkdownChalkResetAndNewlineBoundaries(t *testing.T) {
	got := ansiSpan("\x1b[1m", "\x1b[22m", "a\x1b[22mb\nc")
	want := "\x1b[1ma\x1b[22m\x1b[1mb\x1b[22m\n\x1b[1mc\x1b[22m"
	if got != want {
		t.Fatalf("nested decoration=%q, want %q", got, want)
	}
}
func TestMarkdownMatchingCodeSpanRuns(t *testing.T) {
	for _, tt := range []struct{ source, want string }{
		{"`one` ``a`b`` ```c``d``` and `` `edge` ``", "one a`b c``d and `edge`"},
		{"before `incomplete", "before `incomplete"},
		{"before `one\ntwo` after", "before one two after"},
	} {
		got := mdPlain(testMDWithOptions(tt.source, 0, 0, mdUpstreamTheme(), nil, nil).Render(80))
		if !slices.Equal(got, []string{tt.want}) {
			t.Fatalf("%q => %q, want %q", tt.source, got, tt.want)
		}
	}
}
