package ui

import (
	"testing"

	tui "github.com/MichaelKinsy/pigpen/components/pitui"
)

func TestDialogBuilder(t *testing.T) {
	const f = "view/dialog-builder"
	tw(t, f, "DynamicBorder renders exactly 1 row (topFixed assumes 1)", func(t *testing.T) {
		border := tui.NewDynamicBorder(func(s string) string { return identityTheme{}.Fg("accent", s) })
		if n := len(border.Render(80)); n != 1 {
			t.Fatalf("rows = %d", n)
		}
	})
	tw(t, f, "TabBar renders exactly 2 rows (topFixed assumes 2 when isMulti)", func(t *testing.T) {
		bar := NewTabBar(identityTheme{})
		bar.SetProps(TabBarProps{
			Tabs:   []TabBarTab{{Label: "H1", Active: true}, {Label: "H2"}},
			Submit: TabBarSubmit{},
		})
		if n := len(bar.Render(80)); n != 2 {
			t.Fatalf("rows = %d", n)
		}
	})
}
