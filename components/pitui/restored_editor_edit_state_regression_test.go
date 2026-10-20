package tui

import (
	"slices"
	"strings"
	"testing"
)

// upstream: packages/tui/src/components/editor.ts:1112-1125,1036-1061
// setText clears valid paste IDs even if the visible text is unchanged. A render cache must not retain atomic wrapping from the former registry.
func TestEditorSetTextSameMarkerInvalidatesAtomicWrap(t *testing.T) {
	e := newTestEditor()
	e.theme.BorderColor = func(s string) string { return s }
	e.HandleInput("prefix")
	e.HandleInput("\x1b[200~" + strings.TrimSuffix(strings.Repeat("line\n", 20), "\n") + "\x1b[201~")
	e.HandleInput("suffix")
	renderEditor(e, 15)
	text := e.GetText()
	e.SetText(text)
	plain := newTestEditor()
	plain.theme.BorderColor = e.theme.BorderColor
	plain.SetText(text)
	if got, want := renderEditor(e, 15), renderEditor(plain, 15); !slices.Equal(got, want) {
		t.Fatalf("same-text SetText retained registered wrapping:\ngot %q\nwant %q", got, want)
	}
}
