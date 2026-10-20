package tui

import (
	"slices"
	"testing"
)

// A default text color function supplied by the caller may read more than its argument, so a component with one reuses no
// inline rendering.
func TestStreamedMarkdownWithAForeignTextColorCallsItAgain(t *testing.T) {
	mdCaseSetup(t)
	generation := "1"
	style := &DefaultTextStyle{Color: func(s string) string { return "<" + generation + ">" + s }}
	streamed := testMDWithOptions("", 0, 0, nil, style, nil)
	text := "first paragraph\n\nsecond"
	for _, step := range []string{"1", "2"} {
		generation = step
		text += " more"
		streamed.SetText(text)
		if got, want := streamed.Render(60), testMDWithOptions(text, 0, 0, nil, style, nil).Render(60); !slices.Equal(got, want) {
			t.Fatalf("generation %s: got %q, want %q", step, got, want)
		}
	}
}
