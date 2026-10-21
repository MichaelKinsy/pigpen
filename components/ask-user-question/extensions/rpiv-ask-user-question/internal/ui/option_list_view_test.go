package ui

import (
	"strings"
	"testing"
)

var optionListSelectTheme = WrappingSelectTheme{
	SelectedText: func(s string) string { return s },
	Description:  func(s string) string { return s },
	ScrollInfo:   func(s string) string { return s },
}

func makeOptionListView(items []WrappingSelectItem) *OptionListView {
	return NewOptionListView(OptionListViewConfig{Items: items, Theme: optionListSelectTheme})
}

var optionListSample = []WrappingSelectItem{
	{Kind: KindOption, Label: "Alpha"},
	{Kind: KindOption, Label: "Beta"},
	{Kind: KindOption, Label: "Gamma"},
}

func TestOptionListView(t *testing.T) {
	const f = "view/components/option-list-view"
	tw(t, f, "setProps({selectedIndex}) value is reflected in render() row activation (cursor on row 3)", func(t *testing.T) {
		v := makeOptionListView(optionListSample)
		v.SetProps(OptionListViewProps{SelectedIndex: 2, Focused: true})
		var active string
		for _, l := range v.Render(40) {
			if strings.Contains(l, "Gamma") {
				active = l
				break
			}
		}
		if active == "" || !strings.Contains(active, "❯") {
			t.Fatalf("active row = %q", active)
		}
	})
	tw(t, f, "setProps({focused: false}) hides the active pointer", func(t *testing.T) {
		v := makeOptionListView(optionListSample)
		v.SetProps(OptionListViewProps{SelectedIndex: 0, Focused: false})
		for _, l := range v.Render(40) {
			if strings.HasPrefix(l, "❯") {
				t.Fatalf("line %q", l)
			}
		}
	})
	tw(t, f, "setProps({focused: true}) shows the active pointer at row 0", func(t *testing.T) {
		v := makeOptionListView(optionListSample)
		v.SetProps(OptionListViewProps{SelectedIndex: 0, Focused: true})
		if !strings.Contains(v.Render(40)[0], "❯") {
			t.Fatal("no pointer")
		}
	})
	tw(t, f, "setProps({inputBuffer}) reflects buffer text in inline-input row render", func(t *testing.T) {
		v := makeOptionListView([]WrappingSelectItem{{Kind: KindOption, Label: "Alpha"}, {Kind: KindOther, Label: "Type something."}})
		v.SetProps(OptionListViewProps{SelectedIndex: 1, Focused: true, InputBuffer: "typed"})
		lines := v.Render(40)
		if !anyHas(lines, "typed") || !anyHas(lines, cursorMarker) {
			t.Fatalf("lines = %q", lines)
		}
	})
	tw(t, f, "setProps({confirmed: { index: 1 }}) renders ' ✔' on row 2", func(t *testing.T) {
		v := makeOptionListView(optionListSample)
		v.SetProps(OptionListViewProps{SelectedIndex: 0, Focused: true, Confirmed: &OptionListConfirmed{Index: 1}})
		if !anyHas(v.Render(40), "Beta ✔") {
			t.Fatal("no confirmed mark")
		}
	})
	tw(t, f, "omitting confirmed in setProps clears the marker", func(t *testing.T) {
		v := makeOptionListView(optionListSample)
		v.SetProps(OptionListViewProps{Focused: true, Confirmed: &OptionListConfirmed{Index: 1}})
		v.SetProps(OptionListViewProps{Focused: true})
		if strings.Contains(joinLines(v.Render(40)), "✔") {
			t.Fatal("marker not cleared")
		}
	})
	tw(t, f, "MAX_VISIBLE_OPTIONS is 10", func(t *testing.T) {
		if MaxVisibleOptions != 10 {
			t.Fatal(MaxVisibleOptions)
		}
	})
}
