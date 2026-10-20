package ui

import (
	"strings"
	"testing"
)

// submitPickerTheme is the tagged theme of submit-picker.test.ts.
type submitPickerTheme struct{ identityTheme }

func (submitPickerTheme) Fg(color, text string) string {
	return "<" + color + ">" + text + "</" + color + ">"
}
func (submitPickerTheme) Bg(_ string, text string) string { return text }
func (submitPickerTheme) Bold(text string) string         { return "<b>" + text + "</b>" }

func pickerProps(a0, a1 bool) SubmitPickerProps {
	return SubmitPickerProps{Rows: []SubmitPickerRow{{Active: a0}, {Active: a1}}}
}

func makePicker(initial SubmitPickerProps) *SubmitPicker {
	p := NewSubmitPicker(submitPickerTheme{})
	p.SetProps(initial)
	return p
}

func TestSubmitPicker(t *testing.T) {
	const f = "view/components/submit-picker"
	tw(t, f, "naturalHeight is 2 regardless of width and props", func(t *testing.T) {
		p := makePicker(pickerProps(false, false))
		if p.NaturalHeight(80) != 2 || p.NaturalHeight(40) != 2 {
			t.Fatal("naturalHeight != 2")
		}
	})
	tw(t, f, "renders both rows with numbers", func(t *testing.T) {
		lines := makePicker(pickerProps(false, false)).Render(80)
		if len(lines) != 2 {
			t.Fatalf("lines = %q", lines)
		}
		if !strings.Contains(lines[0], "1") || !strings.Contains(lines[0], SubmitLabel) ||
			!strings.Contains(lines[1], "2") || !strings.Contains(lines[1], CancelLabel) {
			t.Fatalf("lines = %q", lines)
		}
	})
	tw(t, f, "active pointer follows props.rows[i].active", func(t *testing.T) {
		p := makePicker(pickerProps(true, false))
		f0 := p.Render(80)
		if !strings.Contains(f0[0], "❯") || strings.Contains(f0[1], "❯") {
			t.Fatalf("f0 = %q", f0)
		}
		p.SetProps(pickerProps(false, true))
		f1 := p.Render(80)
		if strings.Contains(f1[0], "❯") || !strings.Contains(f1[1], "❯") {
			t.Fatalf("f1 = %q", f1)
		}
	})
	tw(t, f, "no active pointer when both rows are inactive", func(t *testing.T) {
		lines := makePicker(pickerProps(false, false)).Render(80)
		if strings.Contains(lines[0], "❯") || strings.Contains(lines[1], "❯") {
			t.Fatalf("lines = %q", lines)
		}
	})
	tw(t, f, "active row is bold-accent", func(t *testing.T) {
		lines := makePicker(pickerProps(false, true)).Render(80)
		if !strings.Contains(lines[1], "<accent>") || !strings.Contains(lines[1], "<b>") {
			t.Fatalf("line = %q", lines[1])
		}
	})
	tw(t, f, "renders the same regardless of completeness — dim styling removed (D1 revised)", func(t *testing.T) {
		lines := makePicker(pickerProps(true, false)).Render(80)
		if strings.Contains(lines[0], "<dim>") || !strings.Contains(lines[0], "<accent>") {
			t.Fatalf("line = %q", lines[0])
		}
	})
}
