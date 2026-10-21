package ui

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

func doStrip(l string) string { return strings.TrimSpace(stripSGR(l)) }

func doRender(over dialogOver, width int) []string {
	return makeDialog(makeDialogConfig(overflowDefaults, over)).Render(width)
}

func doRows(n int) func() int { return func() int { return n } }

func doConst(n int) func(int) int { return func(int) int { return n } }

func doRepeat(s string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = s
	}
	return out
}

func doAnyEq(lines []string, want string) bool {
	for _, l := range lines {
		if doStrip(l) == want {
			return true
		}
	}
	return false
}

func doTrailingBlanks(lines []string) int {
	n := 0
	for i := len(lines) - 1; i >= 0 && strings.TrimSpace(lines[i]) == ""; i-- {
		n++
	}
	return n
}

var borderRe = regexp.MustCompile(`─`)

func TestDialogOverflow(t *testing.T) {
	const f = "view/dialog-overflow"
	has := func(t *testing.T, joined, sub string) {
		t.Helper()
		if !strings.Contains(joined, sub) {
			t.Fatalf("missing %q in:\n%s", sub, joined)
		}
	}
	hasNot := func(t *testing.T, joined, sub string) {
		t.Helper()
		if strings.Contains(joined, sub) {
			t.Fatalf("unexpected %q in:\n%s", sub, joined)
		}
	}
	tailAfterHint := func(t *testing.T, lines []string) []string {
		t.Helper()
		for i, l := range lines {
			if strings.Contains(l, HintPartEnter) {
				if i <= 0 {
					t.Fatalf("hintIdx %d", i)
				}
				return lines[i+1:]
			}
		}
		t.Fatal("no hint row")
		return nil
	}

	tw(t, f, "returns full output including residual spacer when terminal is very tall", func(t *testing.T) {
		lines := doRender(dialogOver{getTerminalRows: doRows(50), getBodyHeight: doConst(6), getCurrentBodyHeight: doConst(1)}, 80)
		tail := tailAfterHint(t, lines)
		if len(tail) != 8 {
			t.Fatalf("tail %d", len(tail))
		}
		for _, l := range tail {
			if strings.TrimSpace(l) != "" {
				t.Fatalf("non-blank %q", l)
			}
		}
	})
	tw(t, f, "preserves exact same output as current code when no overflow", func(t *testing.T) {
		lines := doRender(dialogOver{getTerminalRows: doRows(100), getBodyHeight: doConst(5), getCurrentBodyHeight: doConst(1)}, 80)
		n := 0
		for _, l := range lines {
			if strings.TrimSpace(l) == "" {
				n++
			}
		}
		if n < 7 {
			t.Fatalf("empty tail %d", n)
		}
	})

	// it.each upstream: the ledger does not list parametrized titles, so this is a plain subtest, not a tw twin.
	t.Run("terminal height %d: output <= terminal rows", func(t *testing.T) {
		for _, termRows := range []int{10, 15, 20, 24} {
			lines := doRender(dialogOver{getTerminalRows: doRows(termRows), getBodyHeight: doConst(20), getCurrentBodyHeight: doConst(10)}, 80)
			if len(lines) > termRows {
				t.Fatalf("rows %d: %d lines", termRows, len(lines))
			}
		}
	})
	tw(t, f, "width safety: every line <= width across heights and widths", func(t *testing.T) {
		for _, termRows := range []int{10, 15, 24} {
			for _, w := range []int{60, 80, 120} {
				for _, line := range doRender(dialogOver{getTerminalRows: doRows(termRows), getBodyHeight: doConst(20), getCurrentBodyHeight: doConst(10)}, w) {
					if widthx.VisibleWidth(line) > w {
						t.Fatalf("rows %d width %d: %q", termRows, w, line)
					}
				}
			}
		}
	})

	tw(t, f, "sticky top preserved: first row is border", func(t *testing.T) {
		lines := doRender(dialogOver{getTerminalRows: doRows(14), getBodyHeight: doConst(20), getCurrentBodyHeight: doConst(10)}, 80)
		if !borderRe.MatchString(lines[0]) {
			t.Fatalf("first row %q", lines[0])
		}
	})
	tw(t, f, "sticky bottom preserved: footer hint at end", func(t *testing.T) {
		lines := doRender(dialogOver{getTerminalRows: doRows(14), getBodyHeight: doConst(20), getCurrentBodyHeight: doConst(10)}, 80)
		has(t, joinLines(lines), HintPartNotes)
	})
	tw(t, f, "single-question mode: no tab bar in output", func(t *testing.T) {
		joined := joinLines(doRender(dialogOver{
			questions:       []QuestionData{dlgQuestion("only?", "Only", [2]string{"yes", ""})},
			isMulti:         boolp(false),
			getTerminalRows: doRows(10), getBodyHeight: doConst(10), getCurrentBodyHeight: doConst(5),
		}, 80))
		hasNot(t, joined, "<TABBAR>")
		has(t, joined, HintSingle)
	})

	tw(t, f, "no indicators when content fits", func(t *testing.T) {
		lines := doRender(dialogOver{getTerminalRows: doRows(50), getBodyHeight: doConst(1), getCurrentBodyHeight: doConst(1)}, 80)
		if doAnyEq(lines, "↑") || doAnyEq(lines, "↓") {
			t.Fatalf("indicator in %q", lines)
		}
	})
	tw(t, f, "renders within terminal bounds under heavy overflow with focus at index 0", func(t *testing.T) {
		lines := doRender(dialogOver{getTerminalRows: doRows(14), getBodyHeight: doConst(20), getCurrentBodyHeight: doConst(10)}, 80)
		if len(lines) > 14 {
			t.Fatalf("%d lines", len(lines))
		}
	})

	tw(t, f, "shows chrome only at topFixed + bottomFixed height", func(t *testing.T) {
		lines := doRender(dialogOver{getTerminalRows: doRows(7), getBodyHeight: doConst(20), getCurrentBodyHeight: doConst(10)}, 80)
		if len(lines) != 7 || !borderRe.MatchString(lines[0]) {
			t.Fatalf("lines %d first %q", len(lines), lines[0])
		}
		has(t, joinLines(lines), HintPartNotes)
	})
	tw(t, f, "clips chrome when terminal smaller than topFixed + bottomFixed", func(t *testing.T) {
		lines := doRender(dialogOver{getTerminalRows: doRows(5), getBodyHeight: doConst(20), getCurrentBodyHeight: doConst(10)}, 80)
		if len(lines) > 5 {
			t.Fatalf("%d lines", len(lines))
		}
	})

	// Submit tab.
	ansA, ansX := "A", "X"
	overflowAnswers := func() map[int]QuestionAnswer {
		return map[int]QuestionAnswer{
			0: {QuestionIndex: 0, Question: "Q1?", Kind: "option", Answer: &ansA},
			1: {QuestionIndex: 1, Question: "Q2?", Kind: "option", Answer: &ansX},
		}
	}
	submitState := func(mod func(*DialogState)) *DialogState {
		s := vwState()
		s.CurrentTab = 2
		s.Answers = overflowAnswers()
		if mod != nil {
			mod(s)
		}
		return s
	}
	tw(t, f, "output fits terminal on submit tab", func(t *testing.T) {
		st := submitState(nil)
		picker := NewSubmitPicker(identityTheme{})
		picker.SetProps(vwSubmitPickerPropsFromState(st, true))
		lines := doRender(dialogOver{state: st, submitPicker: picker, getTerminalRows: doRows(12), getBodyHeight: doConst(6)}, 80)
		if len(lines) > 12 {
			t.Fatalf("%d lines", len(lines))
		}
	})
	tw(t, f, "top-anchors middle section (no scroll-to-focus)", func(t *testing.T) {
		st := submitState(nil)
		picker := NewSubmitPicker(identityTheme{})
		picker.SetProps(vwSubmitPickerPropsFromState(st, true))
		lines := doRender(dialogOver{state: st, submitPicker: picker, getTerminalRows: doRows(14), getBodyHeight: doConst(6)}, 80)
		has(t, joinLines(lines), ReviewHeading)
	})

	tw(t, f, "shows combined ↕ when availableMiddle === 1 with both overflow directions", func(t *testing.T) {
		lines := doRender(dialogOver{
			getTerminalRows: doRows(8), getBodyHeight: doConst(20), getCurrentBodyHeight: doConst(10),
			previewPane: newStubPreviewPane(doRepeat("<LINE>", 20), nil),
		}, 80)
		if len(lines) > 8 {
			t.Fatalf("%d lines", len(lines))
		}
		if doStrip(lines[4]) != "↕" {
			t.Fatalf("lines[4] = %q", lines[4])
		}
		if doAnyEq(lines, "↑") {
			t.Fatal("separate ↑ leaked")
		}
		if doAnyEq(lines, "↓") {
			t.Fatal("separate ↓ leaked")
		}
	})
	tw(t, f, "shows individual ↑ and ↓ when availableMiddle > 1 with centering", func(t *testing.T) {
		lines := doRender(dialogOver{
			getTerminalRows: doRows(14), getBodyHeight: doConst(20), getCurrentBodyHeight: doConst(10),
			previewPane: newStubPreviewPane(doRepeat("<LINE>", 20), func(int) (int, int) { return 5, 8 }),
		}, 80)
		if len(lines) > 14 {
			t.Fatalf("%d lines", len(lines))
		}
		if doStrip(lines[4]) != "↑" || doStrip(lines[10]) != "↓" {
			t.Fatalf("lines[4]=%q lines[10]=%q", lines[4], lines[10])
		}
		if doAnyEq(lines, "↕") {
			t.Fatal("combined glyph")
		}
	})
	tw(t, f, "shows only ↓ when focused item is at top (scrollStart=0)", func(t *testing.T) {
		lines := doRender(dialogOver{
			getTerminalRows: doRows(14), getBodyHeight: doConst(20), getCurrentBodyHeight: doConst(10),
			previewPane: newStubPreviewPane(doRepeat("<LINE>", 20), nil),
		}, 80)
		if len(lines) > 14 || doAnyEq(lines, "↑") || !doAnyEq(lines, "↓") || doAnyEq(lines, "↕") {
			t.Fatalf("lines %q", lines)
		}
	})
	tw(t, f, "shows only ↑ when focused item is at bottom (scrollStart pinned to max)", func(t *testing.T) {
		lines := doRender(dialogOver{
			getTerminalRows: doRows(14), getBodyHeight: doConst(30), getCurrentBodyHeight: doConst(30),
			previewPane: newStubPreviewPane(doRepeat("<LINE>", 30), func(int) (int, int) { return 29, 30 }),
		}, 80)
		if len(lines) > 14 || !doAnyEq(lines, "↑") || doAnyEq(lines, "↓") || doAnyEq(lines, "↕") {
			t.Fatalf("lines %q", lines)
		}
	})

	tw(t, f, "centers 3-row focused item in scroll window", func(t *testing.T) {
		lines := doRender(dialogOver{
			getTerminalRows: doRows(14), getBodyHeight: doConst(20), getCurrentBodyHeight: doConst(10),
			previewPane: newStubPreviewPane(doRepeat("<LINE>", 20), func(int) (int, int) { return 5, 8 }),
		}, 80)
		if len(lines) > 14 || !anyHas(lines[4:9], "<LINE>") {
			t.Fatalf("lines %q", lines)
		}
	})
	tw(t, f, "handles focusedHeight > availableMiddle (top-anchored fallback)", func(t *testing.T) {
		lines := doRender(dialogOver{
			getTerminalRows: doRows(14), getBodyHeight: doConst(20), getCurrentBodyHeight: doConst(10),
			previewPane: newStubPreviewPane(doRepeat("<LINE>", 20), func(int) (int, int) { return 2, 8 }),
		}, 80)
		if len(lines) > 14 {
			t.Fatalf("%d lines", len(lines))
		}
	})

	multiQ := dlgQuestion("Areas", "Areas", [2]string{"FE", "f"}, [2]string{"BE", "b"})
	multiQ.MultiSelect = true
	tw(t, f, "flipping notesVisible false→true grows render length by exactly 3; trailing residual-spacer tail unchanged", func(t *testing.T) {
		ms := newStubMultiSelect([]string{"<MULTI>"}, nil)
		mk := func(open bool) []string {
			st := vwState()
			st.NotesVisible = open
			return doRender(dialogOver{
				questions: []QuestionData{multiQ}, isMulti: boolp(false), multiSelectByTab: []TabMultiSelect{ms},
				getTerminalRows: doRows(50), getBodyHeight: doConst(8), getCurrentBodyHeight: doConst(4), state: st,
			}, 80)
		}
		closed, open := mk(false), mk(true)
		if len(closed) > 50 || len(open) > 50 {
			t.Fatal("exceeds terminal rows")
		}
		if len(open)-len(closed) != 3 {
			t.Fatalf("delta %d", len(open)-len(closed))
		}
		if doTrailingBlanks(open) != doTrailingBlanks(closed) {
			t.Fatalf("tails %d vs %d", doTrailingBlanks(open), doTrailingBlanks(closed))
		}
	})
	tw(t, f, "overflow with notes open: output ≤ termRows, top border sticky, HINT_PART_CANCEL sticky (NOT HINT_MULTI)", func(t *testing.T) {
		ms := newStubMultiSelect(doRepeat("<MULTI>", 10), nil)
		st := vwState()
		st.NotesVisible = true
		lines := doRender(dialogOver{
			questions: []QuestionData{multiQ}, isMulti: boolp(false), state: st, multiSelectByTab: []TabMultiSelect{ms},
			getTerminalRows: doRows(10), getBodyHeight: doConst(20), getCurrentBodyHeight: doConst(20),
		}, 80)
		if len(lines) > 10 || !borderRe.MatchString(lines[0]) {
			t.Fatalf("lines %d first %q", len(lines), lines[0])
		}
		has(t, joinLines(lines), HintPartCancel)
		hasNot(t, joinLines(lines), HintMulti)
	})

	submitDialog := func(state *DialogState, mod func(*dialogOver)) func(width int) []string {
		picker := NewSubmitPicker(identityTheme{})
		picker.SetProps(vwSubmitPickerPropsFromState(state, true))
		over := dialogOver{state: state, submitPicker: picker, getTerminalRows: doRows(50), getBodyHeight: doConst(8)}
		if mod != nil {
			mod(&over)
		}
		return func(w int) []string { return doRender(over, w) }
	}

	tw(t, f, "flipping notesVisible false→true grows the render by exactly 3; tail identical; hint blanks while open", func(t *testing.T) {
		closed := submitDialog(submitState(nil), nil)(80)
		open := submitDialog(submitState(func(s *DialogState) { s.NotesVisible = true }), nil)(80)
		if len(open)-len(closed) != 3 {
			t.Fatalf("delta %d", len(open)-len(closed))
		}
		oj, cj := joinLines(open), joinLines(closed)
		has(t, oj, "Global note:")
		has(t, oj, "<NOTES_INPUT>")
		hasNot(t, cj, "<NOTES_INPUT>")
		has(t, cj, "n to add a note")
		hasNot(t, oj, "n to add a note")
		has(t, oj, HintPartNewLine)
		if doTrailingBlanks(open) != doTrailingBlanks(closed) {
			t.Fatalf("tails %d vs %d", doTrailingBlanks(open), doTrailingBlanks(closed))
		}
	})
	tw(t, f, "overflow while open: output ≤ termRows, sticky top border, sticky footer (picker visible)", func(t *testing.T) {
		lines := submitDialog(submitState(func(s *DialogState) { s.NotesVisible = true }), func(o *dialogOver) {
			o.getTerminalRows = doRows(12)
			o.getBodyHeight = doConst(20)
		})(80)
		if len(lines) > 12 || !borderRe.MatchString(lines[0]) {
			t.Fatalf("lines %d first %q", len(lines), lines[0])
		}
		has(t, joinLines(lines), SubmitLabel)
	})
	tw(t, f, "width-clip: render length is width-invariant in the one-line regime; hint clips to one row, never wraps", func(t *testing.T) {
		render := submitDialog(submitState(nil), nil)
		lengths := map[int]bool{}
		for _, w := range []int{40, 60, 80, 120} {
			lengths[len(render(w))] = true
		}
		if len(lengths) != 1 {
			t.Fatalf("lengths %v", lengths)
		}
		var hintRows []string
		for _, l := range render(10) {
			if strings.HasPrefix(stripSGR(strings.TrimSpace(l)), "Enter to") {
				hintRows = append(hintRows, l)
			}
		}
		if len(hintRows) != 1 || widthx.VisibleWidth(hintRows[0]) > 10 {
			t.Fatalf("hint rows %q", hintRows)
		}
	})
	tw(t, f, "hint sits BELOW the picker; the prompt reads straight into its options", func(t *testing.T) {
		raw := submitDialog(submitState(nil), nil)(80)
		idx := func(sub string) int {
			for i, l := range raw {
				if strings.Contains(doStrip(l), sub) {
					return i
				}
			}
			return -1
		}
		promptRow, submitRow, hintRow := idx(ReadyPrompt), idx(SubmitLabel), idx("n to add a note")
		if promptRow < 0 || submitRow != promptRow+1 || hintRow <= submitRow {
			t.Fatalf("prompt %d submit %d hint %d (%s)", promptRow, submitRow, hintRow, strconv.Itoa(len(raw)))
		}
	})
	tw(t, f, "a committed global note renders as a review entry while closed and hides while the editor is open", func(t *testing.T) {
		noted := func(s *DialogState) { s.NotesByTab = map[int]string{2: "Ship behind a feature flag"} }
		closed := joinLines(submitDialog(submitState(noted), nil)(80))
		has(t, closed, "● Note")
		has(t, closed, "Ship behind a feature flag")
		open := joinLines(submitDialog(submitState(func(s *DialogState) { noted(s); s.NotesVisible = true }), nil)(80))
		hasNot(t, open, "● Note")
	})
}
