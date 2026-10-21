package ui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

func msQuestion(options ...OptionData) QuestionData {
	if options == nil {
		options = []OptionData{{Label: "FE"}, {Label: "BE"}, {Label: "DB"}}
	}
	return QuestionData{Question: "areas?", Header: "H", Options: options, MultiSelect: true}
}

func msView(q QuestionData, props MultiSelectViewProps) *MultiSelectView {
	v := NewMultiSelectView(identityTheme{}, q)
	v.SetProps(props)
	return v
}

func msRaw(l string) string { return stripSGR(l) }

func msRange(t *testing.T, v *MultiSelectView, w, wantStart, wantEnd int) {
	t.Helper()
	s, e := v.FocusedItemRowRange(w)
	if s != wantStart || e != wantEnd {
		t.Fatalf("range = [%d, %d], want [%d, %d]", s, e, wantStart, wantEnd)
	}
}

func msPlain(rows []MultiSelectRow, other MultiSelectOtherRowProps, nextActive bool) MultiSelectViewProps {
	return MultiSelectViewProps{Rows: rows, Other: other, NextActive: nextActive, NextLabel: "Next"}
}

func TestMultiSelectView(t *testing.T) {
	const f = "view/components/multi-select-view"
	noOver := multiSelectPropsOverrides{}

	tw(t, f, "renders one row per option + a 'Type something.' row + a trailing Next sentinel", func(t *testing.T) {
		q := msQuestion()
		lines := msView(q, vwMultiSelectProps(q, noOver)).Render(80)
		if len(lines) != 5 {
			t.Fatalf("lines = %q", lines)
		}
		for i, want := range []string{"FE", "BE", "DB", "Type something.", "Next"} {
			if !strings.Contains(lines[i], want) {
				t.Fatalf("line %d = %q, want %q", i, lines[i], want)
			}
		}
	})
	tw(t, f, "separates the checkbox from the label by exactly ONE space", func(t *testing.T) {
		q := msQuestion()
		raw := msRaw(msView(q, vwMultiSelectProps(q, noOver)).Render(80)[0])
		if !regexp.MustCompile(`\[[ ✔]\] FE`).MatchString(raw) {
			t.Fatalf("raw = %q", raw)
		}
	})
	tw(t, f, "focused=false suppresses the active-row pointer (no doubled cursor)", func(t *testing.T) {
		q := msQuestion()
		m := msView(q, vwMultiSelectProps(q, multiSelectPropsOverrides{optionIndex: 1}))
		if !strings.HasPrefix(msRaw(m.Render(80)[1]), "❯ ") {
			t.Fatal("no pointer on selected row")
		}
		m.SetProps(vwMultiSelectProps(q, multiSelectPropsOverrides{optionIndex: 1, blurred: true}))
		for _, l := range m.Render(80) {
			if strings.HasPrefix(msRaw(l), "❯ ") {
				t.Fatalf("pointer on blurred pane: %q", l)
			}
		}
		m.SetProps(vwMultiSelectProps(q, multiSelectPropsOverrides{optionIndex: 1}))
		if !strings.HasPrefix(msRaw(m.Render(80)[1]), "❯ ") {
			t.Fatal("no pointer after refocus")
		}
	})
	tw(t, f, "renders description on continuation line when present", func(t *testing.T) {
		q := msQuestion(OptionData{Label: "FE", Description: "front-end"}, OptionData{Label: "BE"})
		lines := msView(q, vwMultiSelectProps(q, noOver)).Render(80)
		if len(lines) != 5 || !strings.Contains(lines[1], "front-end") ||
			!strings.Contains(lines[3], "Type something.") || !strings.Contains(lines[4], "Next") {
			t.Fatalf("lines = %q", lines)
		}
	})
	tw(t, f, "active option uses ACTIVE_POINTER and accent styling", func(t *testing.T) {
		q := msQuestion()
		lines := msView(q, vwMultiSelectProps(q, multiSelectPropsOverrides{optionIndex: 1})).Render(80)
		if !strings.Contains(lines[1], "❯ ") || strings.HasPrefix(lines[0], "❯ ") {
			t.Fatalf("lines = %q", lines)
		}
	})
	tw(t, f, "checked options render [✔]; unchecked render [ ]", func(t *testing.T) {
		q := msQuestion()
		lines := msView(q, vwMultiSelectProps(q, multiSelectPropsOverrides{checkedIndices: map[int]bool{0: true, 2: true}})).Render(80)
		if !strings.Contains(lines[0], "[✔]") || !strings.Contains(lines[1], "[ ]") || !strings.Contains(lines[2], "[✔]") {
			t.Fatalf("lines = %q", lines)
		}
	})
	tw(t, f, "row 1 inactive unchecked renders as '  1. [ ] LABEL'", func(t *testing.T) {
		q := msQuestion()
		raw := msRaw(msView(q, vwMultiSelectProps(q, multiSelectPropsOverrides{optionIndex: 1})).Render(80)[0])
		if !regexp.MustCompile(`^ {2}1\. \[ \] FE`).MatchString(raw) {
			t.Fatalf("raw = %q", raw)
		}
	})
	tw(t, f, "row 2 active checked renders as '❯ 2. [✔] LABEL'", func(t *testing.T) {
		q := msQuestion()
		raw := msRaw(msView(q, vwMultiSelectProps(q, multiSelectPropsOverrides{optionIndex: 1, checkedIndices: map[int]bool{1: true}})).Render(80)[1])
		if !regexp.MustCompile(`^❯ 2\. \[✔\] BE`).MatchString(raw) {
			t.Fatalf("raw = %q", raw)
		}
	})
	tw(t, f, "description continuation indents to col 2 (CC parity, not prefixVisibleWidth)", func(t *testing.T) {
		q := msQuestion(OptionData{Label: "FE", Description: "this is an extremely long description that should wrap across multiple lines when rendered at narrow widths"}, OptionData{Label: "BE"})
		lines := msView(q, vwMultiSelectProps(q, noOver)).Render(40)
		for i := 1; i < len(lines)-1; i++ {
			raw := msRaw(lines[i])
			if !strings.HasPrefix(raw, "  ") || strings.HasPrefix(raw, "   ") {
				t.Fatalf("line %d = %q", i, raw)
			}
		}
	})
	tw(t, f, "renders props.nextLabel verbatim on the trailing sentinel row", func(t *testing.T) {
		q := msQuestion()
		lines := msView(q, vwMultiSelectProps(q, multiSelectPropsOverrides{nextLabel: "Submit"})).Render(80)
		last := lines[len(lines)-1]
		if !strings.Contains(last, "Submit") || strings.Contains(last, "Next") {
			t.Fatalf("last = %q", last)
		}
	})
	tw(t, f, "setProps mutates props visible to next render (active row moves)", func(t *testing.T) {
		q := msQuestion()
		m := msView(q, vwMultiSelectProps(q, multiSelectPropsOverrides{optionIndex: 0}))
		if !strings.Contains(m.Render(80)[0], "❯ ") {
			t.Fatal("no pointer on row 0")
		}
		m.SetProps(vwMultiSelectProps(q, multiSelectPropsOverrides{optionIndex: 2}))
		lines := m.Render(80)
		if strings.HasPrefix(lines[0], "❯ ") || !strings.Contains(lines[2], "❯ ") {
			t.Fatalf("lines = %q", lines)
		}
	})

	tw(t, f, "naturalHeight(w) === render(w).length across widths and fixtures", func(t *testing.T) {
		fixtures := []QuestionData{
			msQuestion(),
			msQuestion(OptionData{Label: "FE", Description: "front-end"}, OptionData{Label: "BE", Description: "back-end"}, OptionData{Label: "DB", Description: "DB"}),
			msQuestion(OptionData{Label: "FE", Description: "this is an extremely long description that should wrap across multiple lines when rendered at narrow widths to verify line counting"}, OptionData{Label: "BE", Description: "BE"}),
			msQuestion(OptionData{Label: strings.Repeat("x", 200), Description: "long"}, OptionData{Label: "BE", Description: "BE"}),
		}
		for _, q := range fixtures {
			m := msView(q, vwMultiSelectProps(q, noOver))
			for _, w := range []int{20, 40, 80, 120} {
				if m.NaturalHeight(w) != len(m.Render(w)) {
					t.Fatalf("width %d: %d vs %d", w, m.NaturalHeight(w), len(m.Render(w)))
				}
			}
		}
	})
	tw(t, f, "is props-independent (theme/question/width only)", func(t *testing.T) {
		q := msQuestion(OptionData{Label: "FE", Description: "front-end work"}, OptionData{Label: "BE", Description: "back-end"}, OptionData{Label: "DB", Description: "database tasks"})
		a := msView(q, vwMultiSelectProps(q, multiSelectPropsOverrides{optionIndex: 0}))
		b := msView(q, vwMultiSelectProps(q, multiSelectPropsOverrides{optionIndex: 2, checkedIndices: map[int]bool{0: true, 1: true}}))
		for _, w := range []int{20, 40, 80, 120} {
			if a.NaturalHeight(w) != b.NaturalHeight(w) {
				t.Fatalf("width %d differs", w)
			}
		}
	})

	two := func() QuestionData { return msQuestion(OptionData{Label: "A"}, OptionData{Label: "B"}) }
	idleOther := MultiSelectOtherRowProps{}
	tw(t, f, "returns correct range for active option with description", func(t *testing.T) {
		q := msQuestion(OptionData{Label: "A"}, OptionData{Label: "B", Description: "a longer description that might wrap"}, OptionData{Label: "C"})
		v := msView(q, msPlain([]MultiSelectRow{{}, {Active: true}, {}}, idleOther, false))
		s, e := v.FocusedItemRowRange(80)
		if s != 1 || e <= s {
			t.Fatalf("range [%d, %d]", s, e)
		}
	})
	tw(t, f, "returns [0, 1] for first item active with no description", func(t *testing.T) {
		msRange(t, msView(two(), msPlain([]MultiSelectRow{{Active: true}, {}}, idleOther, false)), 80, 0, 1)
	})
	tw(t, f, "returns range for Next sentinel when nextActive", func(t *testing.T) {
		q := msQuestion(OptionData{Label: "A"})
		msRange(t, msView(q, msPlain([]MultiSelectRow{{}}, idleOther, true)), 80, 2, 3)
	})
	tw(t, f, "returns [0, 0] when no row is active", func(t *testing.T) {
		q := msQuestion(OptionData{Label: "A"})
		msRange(t, msView(q, msPlain([]MultiSelectRow{{}}, idleOther, false)), 80, 0, 0)
	})
	tw(t, f, "range matches actual rendered output position", func(t *testing.T) {
		q := msQuestion(OptionData{Label: "A"}, OptionData{Label: "B", Description: "a description"})
		v := msView(q, msPlain([]MultiSelectRow{{}, {Active: true}}, idleOther, false))
		s, e := v.FocusedItemRowRange(80)
		rendered := v.Render(80)
		if !strings.Contains(rendered[s], "B") || e > len(rendered) {
			t.Fatalf("range [%d, %d] rendered %q", s, e, rendered)
		}
	})

	otherActiveInput := func(q QuestionData, buffer string) MultiSelectViewProps {
		return vwMultiSelectProps(q, multiSelectPropsOverrides{optionIndex: len(q.Options), inputMode: true, inputBuffer: buffer})
	}
	tw(t, f, "renders the other row after options, numbered N+1, box always [ ] muted", func(t *testing.T) {
		q := msQuestion()
		lines := msView(q, vwMultiSelectProps(q, noOver)).Render(80)
		if !regexp.MustCompile(`^ {2}4\. \[ \] Type something\.`).MatchString(msRaw(lines[3])) || strings.Contains(lines[3], "[✔]") {
			t.Fatalf("line = %q", lines[3])
		}
	})
	tw(t, f, "keeps the draft visible on the other row while another option has focus", func(t *testing.T) {
		q := msQuestion()
		other := msView(q, vwMultiSelectProps(q, multiSelectPropsOverrides{optionIndex: 0, inputBuffer: "draft answer"})).Render(80)[3]
		if !strings.Contains(other, "draft answer") || strings.Contains(other, "Type something.") || strings.Contains(other, cursorMarker) {
			t.Fatalf("other = %q", other)
		}
	})
	tw(t, f, "renders the inline cursor (not a static label) when other.active && other.inputMode", func(t *testing.T) {
		q := msQuestion()
		var cursorLines []string
		for _, l := range msView(q, otherActiveInput(q, "my answer")).Render(80) {
			if strings.Contains(l, cursorMarker) {
				cursorLines = append(cursorLines, l)
			}
		}
		if len(cursorLines) != 1 || !strings.Contains(cursorLines[0], "my answer") {
			t.Fatalf("cursor lines = %q", cursorLines)
		}
	})
	tw(t, f, "wraps long custom answers and reports their rendered height", func(t *testing.T) {
		q := msQuestion()
		m := msView(q, otherActiveInput(q, strings.Repeat("x", 200)))
		for _, w := range []int{20, 40, 80} {
			lines := m.Render(w)
			if len(lines) <= 5 || m.NaturalHeight(w) != len(lines) {
				t.Fatalf("width %d: %d lines, height %d", w, len(lines), m.NaturalHeight(w))
			}
			for _, l := range lines {
				if widthx.VisibleWidth(l) > w {
					t.Fatalf("width %d: %q", w, l)
				}
			}
		}
	})
	tw(t, f, "renders explicit line breaks and expands the focused row range", func(t *testing.T) {
		q := msQuestion()
		m := msView(q, otherActiveInput(q, "first\nsecond"))
		lines := m.Render(80)
		if !anyHas(lines, "first") || !anyHas(lines, "second") {
			t.Fatalf("lines = %q", lines)
		}
		s, e := m.FocusedItemRowRange(80)
		if e-s != 2 {
			t.Fatalf("range [%d, %d]", s, e)
		}
	})
	tw(t, f, "focusedItemRowRange covers the other row ([row, row+1]) then Next", func(t *testing.T) {
		v := msView(two(), msPlain([]MultiSelectRow{{}, {}}, MultiSelectOtherRowProps{Active: true, InputMode: true, InputBuffer: "x"}, false))
		msRange(t, v, 80, 2, 3)
	})
	tw(t, f, "nextActive shifts to options.length + 1 (Next sits below the other row)", func(t *testing.T) {
		q := msQuestion(OptionData{Label: "A"})
		msRange(t, msView(q, msPlain([]MultiSelectRow{{}}, idleOther, true)), 80, 2, 3)
	})
	tw(t, f, "number column fits N+1 (the other row's number) without truncation", func(t *testing.T) {
		opts := make([]OptionData, 9)
		for i := range opts {
			opts[i] = OptionData{Label: "o" + string(rune('1'+i))}
		}
		q := msQuestion(opts...)
		raw := msRaw(msView(q, vwMultiSelectProps(q, noOver)).Render(80)[9])
		if !regexp.MustCompile(`10\. \[ \] Type something\.`).MatchString(raw) {
			t.Fatalf("raw = %q", raw)
		}
	})

	tw(t, f, "every emitted line satisfies visibleWidth(line) <= width", func(t *testing.T) {
		q := msQuestion(OptionData{Label: strings.Repeat("x", 200), Description: strings.Repeat("y", 200)}, OptionData{Label: "BE", Description: "back-end"})
		m := msView(q, vwMultiSelectProps(q, noOver))
		for _, w := range []int{20, 40, 80, 120} {
			for _, l := range m.Render(w) {
				if widthx.VisibleWidth(l) > w {
					t.Fatalf("width %d: %q", w, l)
				}
			}
		}
	})
}
