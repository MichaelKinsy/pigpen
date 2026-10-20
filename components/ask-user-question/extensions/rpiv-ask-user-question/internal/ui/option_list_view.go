package ui

// MaxVisibleOptions is the maximum number of option rows visible in the WrappingSelect window. Lifted here from
// preview-pane so the cap travels with the option-list owner. upstream: view/components/option-list-view.ts.
const MaxVisibleOptions = 10

// OptionListViewConfig is the construction-time config of an OptionListView.
type OptionListViewConfig struct {
	Items []WrappingSelectItem
	Theme WrappingSelectTheme
}

// OptionListConfirmed is the optional previously-confirmed indicator. LabelOverride nil is absent.
type OptionListConfirmed struct {
	Index         int
	LabelOverride *string
}

// OptionListViewProps is the per-tick projection of OptionListView state. InputBuffer is part of the props bag:
// the session-owned inline input supplies its current text here per tick.
type OptionListViewProps struct {
	SelectedIndex int
	Focused       bool
	InputBuffer   string
	// InputCursorOffset nil is `inputCursorOffset` absent (end-of-buffer fallback).
	InputCursorOffset *int
	// Confirmed nil is omitted: no marker is drawn.
	Confirmed *OptionListConfirmed
}

// OptionListView is the sole owner of the option list's interactive state. It wraps a single WrappingSelect;
// SetProps is the only mutator and render output reflects the last props received.
type OptionListView struct {
	list *WrappingSelect
}

func NewOptionListView(config OptionListViewConfig) *OptionListView {
	total := len(config.Items)
	return &OptionListView{list: NewWrappingSelect(config.Items, min(len(config.Items), MaxVisibleOptions), config.Theme, WrappingSelectOptions{
		NumberStartOffset:      0,
		TotalItemsForNumbering: &total,
	})}
}

func (v *OptionListView) SetProps(props OptionListViewProps) {
	v.list.SetSelectedIndex(props.SelectedIndex)
	v.list.SetFocused(props.Focused)
	if props.Confirmed != nil {
		idx := props.Confirmed.Index
		v.list.SetConfirmedIndex(&idx, props.Confirmed.LabelOverride)
	} else {
		v.list.SetConfirmedIndex(nil, nil)
	}
	v.list.SetInputBuffer(props.InputBuffer)
	v.list.SetInputCursorOffset(props.InputCursorOffset)
}

func (v *OptionListView) HandleInput(string) {}

func (v *OptionListView) Invalidate() { v.list.Invalidate() }

func (v *OptionListView) Render(width int) []string { return v.list.Render(width) }

func (v *OptionListView) FocusedItemRowRange(width int) (int, int) {
	return v.list.FocusedItemRowRange(width)
}
