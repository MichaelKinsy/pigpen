package ui

import (
	"strconv"
	"strings"

	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

const (
	msActivePointer   = "❯ "
	msInactivePointer = "  "
	msChecked         = "[✔]"
	msUnchecked       = "[ ]"
	msNumberSeparator = ". "
	msBoxLabelGap     = " "
	// CC parity: description continuation indents to col 2 (past the pointer slot), NOT to the full prefix
	// column. Wrap width still uses prefixVisibleWidth so NaturalHeight matches Render.
	msContinuationIndent = "  "
)

const MultiSubmitLabel = "Submit"

// MultiSelectRow is the per-option row state.
type MultiSelectRow struct {
	Checked bool
	Active  bool
}

// MultiSelectOtherRowProps is the "Type something." row state.
type MultiSelectOtherRowProps struct {
	// Active: the row is the focused row (optionIndex == len(options)).
	Active bool
	// InputMode is state.inputMode: true once the row has focus and keystrokes append to the buffer.
	InputMode bool
	// InputBuffer is the live inline-input buffer.
	InputBuffer string
	// InputCursorOffset nil is undefined.
	InputCursorOffset *int
}

type MultiSelectViewProps struct {
	Rows       []MultiSelectRow
	Other      MultiSelectOtherRowProps
	NextActive bool
	NextLabel  string
}

type multiSelectLayout struct {
	lines        []string
	focusedStart int
	focusedEnd   int
}

// MultiSelectView renders the multi-select option list (one row per option: pointer, checkbox and label, plus
// wrapped continuation lines per description). NaturalHeight(width) is the rendered height for the current
// props; it grows when the custom-answer editor contains logical or visually wrapped lines. One width-keyed layout
// supplies rendering, height and focused-row measurement; SetProps and Invalidate discard that cache.
// upstream: view/components/multi-select-view.ts.
type MultiSelectView struct {
	theme    Theme
	question QuestionData
	props    MultiSelectViewProps

	cacheWidth  int
	cachedValid bool
	cached      multiSelectLayout
}

func NewMultiSelectView(theme Theme, question QuestionData) *MultiSelectView {
	return &MultiSelectView{
		theme:    theme,
		question: question,
		props: MultiSelectViewProps{
			Rows:      []MultiSelectRow{},
			Other:     MultiSelectOtherRowProps{},
			NextLabel: DisplayLabel("next"),
		},
	}
}

func (v *MultiSelectView) SetProps(props MultiSelectViewProps) {
	v.props = props
	v.cachedValid = false
}

func (v *MultiSelectView) HandleInput(string) {}

func (v *MultiSelectView) Invalidate() { v.cachedValid = false }

func (v *MultiSelectView) Render(width int) []string { return v.layout(width).lines }

func (v *MultiSelectView) FocusedItemRowRange(width int) (int, int) {
	l := v.layout(width)
	return l.focusedStart, l.focusedEnd
}

func (v *MultiSelectView) NaturalHeight(width int) int { return len(v.layout(width).lines) }

func (v *MultiSelectView) layout(width int) *multiSelectLayout {
	if v.cachedValid && v.cacheWidth == width {
		return &v.cached
	}
	build := multiSelectLayout{lines: []string{}}
	contentWidth := max(1, width-v.prefixVisibleWidth())
	numberWidth := len(strconv.Itoa(max(1, len(v.question.Options)+1)))

	v.appendOptionRows(&build, width, contentWidth, numberWidth)

	otherStart := len(build.lines)
	build.lines = append(build.lines, v.renderOtherRow(contentWidth, numberWidth)...)
	if v.props.Other.Active {
		build.focusedStart, build.focusedEnd = otherStart, len(build.lines)
	}

	v.appendNextRow(&build, width)

	v.cached = build
	v.cacheWidth = width
	v.cachedValid = true
	return &v.cached
}

func (v *MultiSelectView) appendOptionRows(build *multiSelectLayout, width, contentWidth, numberWidth int) {
	for i, opt := range v.question.Options {
		if i >= len(v.props.Rows) {
			continue
		}
		row := v.props.Rows[i]
		start := len(build.lines)
		pointer := msInactivePointer
		if row.Active {
			pointer = v.theme.Fg("accent", msActivePointer)
		}
		// Checked and active rows share the accent hue, matching the dialog's selection rhythm.
		var box string
		if row.Checked {
			box = v.theme.Fg("accent", msChecked)
		} else {
			box = v.theme.Fg("muted", msUnchecked)
		}
		label := widthx.TruncateToWidth(opt.Label, contentWidth, "…", false)
		styledLabel := label
		if row.Active {
			styledLabel = v.theme.Fg("accent", v.theme.Bold(label))
		}
		number := padStartSpaces(strconv.Itoa(i+1), numberWidth)
		build.lines = append(build.lines,
			widthx.TruncateToWidth(pointer+number+msNumberSeparator+box+msBoxLabelGap+styledLabel, width, "", false))
		if opt.Description != "" {
			for _, segment := range widthx.WrapTextWithAnsi(opt.Description, contentWidth) {
				build.lines = append(build.lines, msContinuationIndent+v.theme.Fg("muted", segment))
			}
		}
		if row.Active {
			build.focusedStart, build.focusedEnd = start, len(build.lines)
		}
	}
}

func (v *MultiSelectView) appendNextRow(build *multiSelectLayout, width int) {
	nextStart := len(build.lines)
	nextPointer := msInactivePointer
	nextLabel := v.props.NextLabel
	if v.props.NextActive {
		nextPointer = v.theme.Fg("accent", msActivePointer)
		nextLabel = v.theme.Fg("accent", v.theme.Bold(v.props.NextLabel))
	}
	build.lines = append(build.lines, widthx.TruncateToWidth(nextPointer+nextLabel, width, "", false))
	if v.props.NextActive {
		build.focusedStart, build.focusedEnd = nextStart, len(build.lines)
	}
}

func (v *MultiSelectView) renderOtherRow(contentWidth, numberWidth int) []string {
	other := v.props.Other
	pointer := msInactivePointer
	if other.Active {
		pointer = v.theme.Fg("accent", msActivePointer)
	}
	box := v.theme.Fg("muted", msUnchecked)
	number := padStartSpaces(strconv.Itoa(len(v.question.Options)+1), numberWidth)
	rowPrefix := pointer + number + msNumberSeparator + box + msBoxLabelGap
	continuationPrefix := strings.Repeat(" ", widthx.VisibleWidth(rowPrefix))
	selectedText := func(text string) string { return v.theme.Fg("accent", v.theme.Bold(text)) }

	if other.Active && other.InputMode {
		return RenderInlineInputRow(RenderInlineInputOptions{
			Buffer:             other.InputBuffer,
			CursorOffset:       other.InputCursorOffset,
			RowPrefix:          rowPrefix,
			ContinuationPrefix: continuationPrefix,
			ContentWidth:       contentWidth,
			SelectedText:       selectedText,
		})
	}

	text := other.InputBuffer
	if text == "" {
		text = DisplayLabel("other")
	}
	wrapped := widthx.WrapTextWithAnsi(text, contentWidth)
	out := make([]string, len(wrapped))
	for index, segment := range wrapped {
		prefix := continuationPrefix
		if index == 0 {
			prefix = rowPrefix
		}
		line := prefix + segment
		if other.Active {
			line = selectedText(line)
		}
		out[index] = line
	}
	return out
}

// prefixVisibleWidth is the canonical prefix width for OPTION rows: INACTIVE_POINTER + numberWidth digits +
// NUMBER_SEPARATOR + UNCHECKED + BOX_LABEL_GAP. State-independent because the active/inactive pointers share a
// visible width, as do CHECKED/UNCHECKED, and numberWidth is constant per question. The number column fits
// len(options)+1 so the "Type something." row's number is never clipped.
func (v *MultiSelectView) prefixVisibleWidth() int {
	numberWidth := len(strconv.Itoa(max(1, len(v.question.Options)+1)))
	return widthx.VisibleWidth(msInactivePointer) + numberWidth +
		widthx.VisibleWidth(msNumberSeparator+msUnchecked+msBoxLabelGap)
}
