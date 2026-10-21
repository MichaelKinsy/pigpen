package ui

import "github.com/MichaelKinsy/pigpen/components/pitui/widthx"

const (
	spActivePointer   = "❯ "
	spInactivePointer = "  "
	spNumberSeparator = ". "
)

const (
	SubmitLabel = "Submit answers"
	CancelLabel = "Cancel"
)

// SubmitPickerRow is the per-row active flag.
type SubmitPickerRow struct{ Active bool }

// SubmitPickerProps is the per-tick projection of SubmitPicker state. The picker is a fixed 2-row structure
// (Submit / Cancel); only the active marker varies per tick. Rows: length always 2, row 0 = Submit, row 1 = Cancel.
type SubmitPickerProps struct {
	Rows []SubmitPickerRow
}

// SubmitPicker is the static 2-row picker rendered on the Submit Tab. Both rows render in normal style at all
// times (partial submission is allowed); NaturalHeight is state-independent and returns a constant 2.
// upstream: view/components/submit-picker.ts.
type SubmitPicker struct {
	theme Theme
	props SubmitPickerProps
}

func NewSubmitPicker(theme Theme) *SubmitPicker {
	return &SubmitPicker{theme: theme, props: SubmitPickerProps{Rows: []SubmitPickerRow{{}, {}}}}
}

func (p *SubmitPicker) SetProps(props SubmitPickerProps) { p.props = props }

func (p *SubmitPicker) HandleInput(string) {}

func (p *SubmitPicker) Invalidate() {}

func (p *SubmitPicker) NaturalHeight(int) int { return 2 }

func (p *SubmitPicker) Render(width int) []string {
	lines := make([]string, 0, 2)
	for i := 0; i < 2; i++ {
		text := T("submit.label", SubmitLabel)
		if i != 0 {
			text = T("submit.cancel", CancelLabel)
		}
		active := i < len(p.props.Rows) && p.props.Rows[i].Active
		pointer := spInactivePointer
		if active {
			pointer = spActivePointer
		}
		number := string(rune('1'+i)) + spNumberSeparator
		var label string
		if active {
			label = p.theme.Fg("accent", p.theme.Bold(text))
		} else {
			label = p.theme.Fg("text", text)
		}
		lines = append(lines, widthx.TruncateToWidth(pointer+number+label, width, "", false))
	}
	return lines
}
