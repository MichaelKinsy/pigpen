package ui

// Binding contexts and selector shapes. upstream: state/selectors/contract.ts.

// BindingContext is the per-apply context the global selectors read.
type BindingContext struct {
	Questions         []QuestionData
	ItemsByTab        [][]WrappingSelectItem
	TotalQuestions    int
	ActiveView        ActiveView
	InputBuffer       string
	InputCursorOffset *int
	ActivePreviewPane StatefulView[PreviewPaneProps]
}

// PerTabBindingContext adds the tab being projected.
type PerTabBindingContext struct {
	BindingContext
	Tab *TabComponents
	I   int
}

// GlobalSelector projects state to the props of a cross-tab component.
type GlobalSelector[P any] func(state QuestionnaireState, ctx BindingContext) P

// PerTabSelector projects state to the props of a per-tab component.
type PerTabSelector[P any] func(state QuestionnaireState, ctx PerTabBindingContext) P
