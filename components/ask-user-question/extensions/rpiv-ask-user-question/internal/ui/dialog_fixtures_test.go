package ui

// Shared harness of dialog-container.test.ts and dialog-overflow.test.ts: their stubs and makeConfig/makeDialog.
// The two files differ only in the default getCurrentBodyHeight (container: the rendered height of the current
// tab's body; overflow: constant 1), selected by dialogDefaults.

import (
	tui "github.com/MichaelKinsy/pigpen/components/pitui"
)

// stubComponent is stubComponent(lines) of the dialog tests.
type stubComponent struct{ lines []string }

func (s *stubComponent) Render(int) []string { return s.lines }
func (s *stubComponent) Invalidate()         {}
func (s *stubComponent) HandleInput(string)  {}

// stubPreviewPane is stubPreviewPane(lines, rowRange?): a stub standing in for PreviewPane.
type stubPreviewPane struct {
	stubComponent
	rowRange func(w int) (int, int)
}

func newStubPreviewPane(lines []string, rowRange func(w int) (int, int)) *stubPreviewPane {
	if rowRange == nil {
		rowRange = func(int) (int, int) { return 0, 1 }
	}
	return &stubPreviewPane{stubComponent{lines}, rowRange}
}

func (s *stubPreviewPane) SetProps(PreviewPaneProps)        {}
func (s *stubPreviewPane) SetGlobalLeftWidth(func(int) int) {}
func (s *stubPreviewPane) NaturalHeight(int) int            { return len(s.lines) }
func (s *stubPreviewPane) MaxNaturalHeight(int) int         { return len(s.lines) }
func (s *stubPreviewPane) FocusedItemRowRange(w int) (int, int) {
	return s.rowRange(w)
}

// stubMultiSelect is stubMultiSelect(lines, rowRange?).
type stubMultiSelect struct {
	stubComponent
	rowRange func(w int) (int, int)
}

func newStubMultiSelect(lines []string, rowRange func(w int) (int, int)) *stubMultiSelect {
	if rowRange == nil {
		rowRange = func(int) (int, int) { return 0, 0 }
	}
	return &stubMultiSelect{stubComponent{lines}, rowRange}
}

func (s *stubMultiSelect) SetProps(MultiSelectViewProps)        {}
func (s *stubMultiSelect) NaturalHeight(int) int                { return len(s.lines) }
func (s *stubMultiSelect) FocusedItemRowRange(w int) (int, int) { return s.rowRange(w) }

// stubOptionList is stubOptionList().
type stubOptionList struct{ stubComponent }

func (s *stubOptionList) SetProps(OptionListViewProps) {}

// dialogDefaults selects the default getCurrentBodyHeight of makeConfig.
type dialogDefaults int

const (
	containerDefaults dialogDefaults = iota
	overflowDefaults
)

// dialogOver is MakeConfigOverrides. Zero values mean "not given", except where noted.
type dialogOver struct {
	questions            []QuestionData
	state                *DialogState
	previewPane          *stubPreviewPane
	tabBar               tui.Component
	notesInput           tui.Component
	isMulti              *bool
	submitPicker         tui.Component
	getBodyHeight        func(int) int
	getCurrentBodyHeight func(int) int
	getTerminalRows      func() int
	collapseKey          string
	multiSelectByTab     []TabMultiSelect
	theme                Theme
	initialProps         *DialogProps
}

type dialogParts struct {
	config       DialogConfig
	initialProps DialogProps
}

func boolp(b bool) *bool { return &b }

func dlgQuestion(question, header string, options ...[2]string) QuestionData {
	q := QuestionData{Question: question, Header: header}
	for _, o := range options {
		q.Options = append(q.Options, OptionData{Label: o[0], Description: o[1]})
	}
	return q
}

func makeDialogConfig(defaults dialogDefaults, over dialogOver) dialogParts {
	questions := over.questions
	if questions == nil {
		questions = []QuestionData{
			dlgQuestion("Q1?", "H1", [2]string{"A", "a"}, [2]string{"B", "b"}),
			dlgQuestion("Q2?", "H2", [2]string{"X", "x"}, [2]string{"Y", "y"}),
		}
	}
	state := over.state
	if state == nil {
		state = vwState()
	}
	previewPane := over.previewPane
	if previewPane == nil {
		previewPane = newStubPreviewPane([]string{"<PREVIEW>"}, nil)
	}
	tabsByIndex := make([]TabComponents, len(questions))
	for i := range questions {
		tc := TabComponents{
			OptionList:  &stubOptionList{stubComponent{[]string{"<OPTION_LIST>"}}},
			Preview:     previewPane,
			BodyHeights: func(int) TabBodyHeights { return TabBodyHeights{} },
		}
		if i < len(over.multiSelectByTab) {
			tc.MultiSelect = over.multiSelectByTab[i]
		}
		tabsByIndex[i] = tc
	}
	theme := over.theme
	if theme == nil {
		theme = identityTheme{}
	}
	tabBar := over.tabBar
	if tabBar == nil {
		tabBar = &stubComponent{[]string{"<TABBAR>", ""}}
	}
	notesInput := over.notesInput
	if notesInput == nil {
		notesInput = &stubComponent{[]string{"<NOTES_INPUT>"}}
	}
	isMulti := len(questions) > 1
	if over.isMulti != nil {
		isMulti = *over.isMulti
	}
	getBodyHeight := over.getBodyHeight
	if getBodyHeight == nil {
		getBodyHeight = func(int) int { return 1 }
	}
	getCurrent := over.getCurrentBodyHeight
	if getCurrent == nil {
		if defaults == overflowDefaults {
			getCurrent = func(int) int { return 1 }
		} else {
			getCurrent = func(w int) int {
				idx := state.CurrentTab
				if idx >= 0 && idx < len(questions) && questions[idx].MultiSelect && tabsByIndex[idx].MultiSelect != nil {
					return len(tabsByIndex[idx].MultiSelect.Render(w))
				}
				return len(previewPane.Render(w))
			}
		}
	}
	getRows := over.getTerminalRows
	if getRows == nil {
		getRows = func() int { return 24 }
	}
	collapseKey := over.collapseKey
	if collapseKey == "" {
		collapseKey = "ctrl+]"
	}
	config := DialogConfig{
		Theme:                theme,
		Questions:            questions,
		TabBar:               tabBar,
		NotesInput:           notesInput,
		IsMulti:              isMulti,
		TabsByIndex:          tabsByIndex,
		SubmitPicker:         over.submitPicker,
		GetBodyHeight:        getBodyHeight,
		GetCurrentBodyHeight: getCurrent,
		GetTerminalRows:      getRows,
		CollapseKey:          collapseKey,
	}
	initialProps := DialogProps{State: state, ActivePreviewPane: previewPane}
	if over.initialProps != nil {
		initialProps = *over.initialProps
	}
	return dialogParts{config, initialProps}
}

func makeDialog(parts dialogParts) *DialogView {
	return NewDialogView(parts.config, parts.initialProps)
}
