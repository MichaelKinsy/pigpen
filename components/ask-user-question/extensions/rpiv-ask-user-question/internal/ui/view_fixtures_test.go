package ui

// Fixtures of the view-layer twins: test-fixtures.ts helpers used by view/components and view/dialog-* tests,
// plus small pointer helpers and a recording theme (the vi.spyOn(theme, ...) of the original tests).

import (
	"regexp"
	"strings"
)

const (
	cursorMarker = "\x1b_pi:c\x07"
	nbsp         = "\u00a0"
)

func cursorOn(ch string) string { return cursorMarker + "\x1b[7m" + ch + "\x1b[27m" }

var ansiSGR = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripSGR(s string) string { return ansiSGR.ReplaceAllString(s, "") }

func intp(n int) *int { return &n }

func strp(s string) *string { return &s }

func joinLines(lines []string) string { return strings.Join(lines, "\n") }

func anyHas(lines []string, sub string) bool {
	for _, l := range lines {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

func anyMatch(lines []string, re *regexp.Regexp) bool {
	for _, l := range lines {
		if re.MatchString(l) {
			return true
		}
	}
	return false
}

// recordedCall is one Fg or Bg call of a recordingTheme.
type recordedCall struct{ Color, Text string }

// recordingTheme is makeTheme() with vi.spyOn on fg and bg: identity styling that records every call.
type recordingTheme struct {
	identityTheme
	fgCalls []recordedCall
	bgCalls []recordedCall
}

func (r *recordingTheme) Fg(color, text string) string {
	r.fgCalls = append(r.fgCalls, recordedCall{color, text})
	return text
}

func (r *recordingTheme) Bg(color, text string) string {
	r.bgCalls = append(r.bgCalls, recordedCall{color, text})
	return text
}

func (r *recordingTheme) calledFg(color, containing string) bool {
	for _, c := range r.fgCalls {
		if c.Color == color && strings.Contains(c.Text, containing) {
			return true
		}
	}
	return false
}

func (r *recordingTheme) calledBg(color, containing string) bool {
	for _, c := range r.bgCalls {
		if c.Color == color && strings.Contains(c.Text, containing) {
			return true
		}
	}
	return false
}

// multiSelectPropsOverrides is MultiSelectPropsOverrides of test-fixtures.ts. Pointer fields are the optional ones.
type multiSelectPropsOverrides struct {
	optionIndex       int
	checkedIndices    map[int]bool
	blurred           bool // focused: false (the TypeScript default is true)
	nextLabel         string
	inputBuffer       string
	inputCursorOffset *int
	inputMode         bool
}

// vwMultiSelectProps is vwMultiSelectProps of test-fixtures.ts.
func vwMultiSelectProps(question QuestionData, over multiSelectPropsOverrides) MultiSelectViewProps {
	focused := !over.blurred
	rows := make([]MultiSelectRow, len(question.Options))
	for i := range question.Options {
		rows[i] = MultiSelectRow{Checked: over.checkedIndices[i], Active: focused && i == over.optionIndex}
	}
	otherActive := focused && over.optionIndex == len(question.Options)
	nextActive := focused && over.optionIndex == len(question.Options)+1
	nextLabel := over.nextLabel
	if nextLabel == "" {
		nextLabel = "Next"
	}
	return MultiSelectViewProps{
		Rows: rows,
		Other: MultiSelectOtherRowProps{
			Active:            otherActive,
			InputMode:         over.inputMode && otherActive,
			InputBuffer:       over.inputBuffer,
			InputCursorOffset: over.inputCursorOffset,
		},
		NextActive: nextActive,
		NextLabel:  nextLabel,
	}
}

// vwState is vwState of test-fixtures.ts; set fields on the result to override.
func vwState() *QuestionnaireState {
	return &QuestionnaireState{
		Answers:            map[int]QuestionAnswer{},
		MultiSelectChecked: map[int]bool{},
		CustomDraftsByTab:  map[int]string{},
		NotesByTab:         map[int]string{},
	}
}

// vwMultiSelectPropsFromState is vwMultiSelectPropsFromState of test-fixtures.ts.
func vwMultiSelectPropsFromState(question QuestionData, state *QuestionnaireState, focused bool) MultiSelectViewProps {
	rows := make([]MultiSelectRow, len(question.Options))
	for i := range question.Options {
		rows[i] = MultiSelectRow{Checked: state.MultiSelectChecked[i], Active: focused && i == state.OptionIndex}
	}
	return MultiSelectViewProps{
		Rows: rows,
		Other: MultiSelectOtherRowProps{
			Active:    focused && state.OptionIndex == len(question.Options),
			InputMode: state.InputMode,
		},
		NextActive: focused && state.OptionIndex == len(question.Options)+1,
		NextLabel:  "Next",
	}
}

// vwSubmitPickerPropsFromState is vwSubmitPickerPropsFromState of test-fixtures.ts.
func vwSubmitPickerPropsFromState(state *QuestionnaireState, focused bool) SubmitPickerProps {
	return SubmitPickerProps{Rows: []SubmitPickerRow{
		{Active: focused && state.SubmitChoiceIndex == 0},
		{Active: focused && state.SubmitChoiceIndex == 1},
	}}
}
