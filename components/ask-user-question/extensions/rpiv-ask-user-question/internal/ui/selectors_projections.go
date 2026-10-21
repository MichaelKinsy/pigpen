package ui

import "fmt"

// Per-component props projections. upstream: state/selectors/projections.ts.

func emptyMultiSelectProps(ctx PerTabBindingContext) MultiSelectViewProps {
	return MultiSelectViewProps{
		Rows: []MultiSelectRow{},
		Other: MultiSelectOtherRowProps{
			Active: false, InputMode: false, InputBuffer: ctx.InputBuffer, InputCursorOffset: ctx.InputCursorOffset,
		},
		NextActive: false,
		NextLabel:  DisplayLabel(KindNext),
	}
}

func nextLabelFor(ctx PerTabBindingContext) string {
	if ctx.I == len(ctx.Questions)-1 {
		return MultiSubmitLabel
	}
	return DisplayLabel(KindNext)
}

// SelectMultiSelectProps is selectMultiSelectProps.
func SelectMultiSelectProps(state QuestionnaireState, ctx PerTabBindingContext) MultiSelectViewProps {
	if ctx.I < 0 || ctx.I >= len(ctx.Questions) {
		return emptyMultiSelectProps(ctx)
	}
	question := ctx.Questions[ctx.I]
	focused := ctx.ActiveView == ActiveViewOptions
	rows := make([]MultiSelectRow, 0, len(question.Options))
	for i := range question.Options {
		rows = append(rows, MultiSelectRow{Checked: state.MultiSelectChecked[i], Active: focused && i == state.OptionIndex})
	}
	otherActive := focused && state.OptionIndex == len(question.Options)
	nextActive := focused && state.OptionIndex == len(question.Options)+1
	return MultiSelectViewProps{
		Rows: rows,
		Other: MultiSelectOtherRowProps{
			Active: otherActive, InputMode: state.InputMode, InputBuffer: ctx.InputBuffer, InputCursorOffset: ctx.InputCursorOffset,
		},
		NextActive: nextActive,
		NextLabel:  nextLabelFor(ctx),
	}
}

// SelectOptionListProps is selectOptionListProps.
func SelectOptionListProps(state QuestionnaireState, ctx PerTabBindingContext) OptionListViewProps {
	var items []WrappingSelectItem
	if ctx.I >= 0 && ctx.I < len(ctx.ItemsByTab) {
		items = ctx.ItemsByTab[ctx.I]
	}
	return OptionListViewProps{
		SelectedIndex:     state.OptionIndex,
		Focused:           ctx.ActiveView == ActiveViewOptions,
		InputBuffer:       ctx.InputBuffer,
		InputCursorOffset: ctx.InputCursorOffset,
		Confirmed:         SelectConfirmedIndicator(ctx.Questions, state.CurrentTab, state.Answers, items),
	}
}

// SelectSubmitPickerProps is selectSubmitPickerProps.
func SelectSubmitPickerProps(state QuestionnaireState, ctx BindingContext) SubmitPickerProps {
	focused := ctx.ActiveView == ActiveViewSubmit
	return SubmitPickerProps{Rows: []SubmitPickerRow{
		{Active: focused && state.SubmitChoiceIndex == 0},
		{Active: focused && state.SubmitChoiceIndex == 1},
	}}
}

// SelectPreviewPaneProps is selectPreviewPaneProps.
func SelectPreviewPaneProps(state QuestionnaireState, ctx PerTabBindingContext) PreviewPaneProps {
	return PreviewPaneProps{
		NotesVisible:  state.NotesVisible,
		SelectedIndex: state.OptionIndex,
		Focused:       ctx.ActiveView == ActiveViewOptions,
		InputMode:     state.InputMode,
	}
}

// SelectTabBarProps is selectTabBarProps.
func SelectTabBarProps(state QuestionnaireState, ctx BindingContext) TabBarProps {
	tabs := make([]TabBarTab, 0, len(ctx.Questions))
	for i, q := range ctx.Questions {
		label := q.Header
		if len(label) == 0 {
			label = fmt.Sprintf("Q%d", i+1)
		}
		_, answered := state.Answers[i]
		tabs = append(tabs, TabBarTab{Label: label, Answered: answered, Active: i == state.CurrentTab})
	}
	return TabBarProps{
		Tabs: tabs,
		Submit: TabBarSubmit{
			Active:      state.CurrentTab == len(ctx.Questions),
			AllAnswered: len(state.Answers) == len(ctx.Questions) && len(ctx.Questions) > 0,
		},
	}
}

// SelectDialogProps is selectDialogProps.
func SelectDialogProps(state QuestionnaireState, ctx BindingContext) DialogProps {
	s := state
	return DialogProps{State: &s, ActivePreviewPane: ctx.ActivePreviewPane}
}
