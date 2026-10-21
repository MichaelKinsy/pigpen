package ui

import (
	tui "github.com/MichaelKinsy/pigpen/components/pitui"
)

// Pure factory assembling every TUI component. upstream: state/build-questionnaire.ts.

// QuestionnaireBuildConfig configures BuildQuestionnaire. upstream: QuestionnaireBuildConfig.
type QuestionnaireBuildConfig struct {
	TUI           TUI
	Theme         Theme
	Questions     []QuestionData
	ItemsByTab    [][]WrappingSelectItem
	IsMulti       bool
	InitialState  QuestionnaireState
	GetCurrentTab func() int
	// CollapseKey is the resolved collapse key spec ("ctrl+]", "alt+o", or "off").
	CollapseKey string
}

// QuestionnaireBuilt is the lifecycle handle BuildQuestionnaire returns.
type QuestionnaireBuilt struct {
	Adapter     *QuestionnairePropsAdapter
	NotesInput  *tui.Editor
	InlineInput *tui.Editor
	Render      func(width int) []string
	Invalidate  func()
}

type heightComputers struct {
	global  func(width int) int
	current func(width int) int
}

func previewBodyHeights(pane TabPreview) func(width int) TabBodyHeights {
	return func(width int) TabBodyHeights {
		current := pane.NaturalHeight(width)
		return TabBodyHeights{Current: current, Max: max(current, pane.MaxNaturalHeight(width))}
	}
}

func editorTheme(theme Theme) tui.EditorTheme {
	return tui.EditorTheme{
		BorderColor: func(text string) string { return theme.Fg("borderMuted", text) },
		SelectList: tui.SelectListTheme{
			SelectedPrefix: func(text string) string { return theme.Bg("selectedBg", theme.Fg("accent", text)) },
			SelectedText:   func(text string) string { return theme.Bg("selectedBg", theme.Bold(text)) },
			Description:    func(text string) string { return theme.Fg("muted", text) },
			ScrollInfo:     func(text string) string { return theme.Fg("dim", text) },
			NoMatch:        func(text string) string { return theme.Fg("warning", text) },
		},
	}
}

func multiSelectBodyHeights(view TabMultiSelect) func(width int) TabBodyHeights {
	return func(width int) TabBodyHeights {
		h := view.NaturalHeight(width)
		return TabBodyHeights{Current: h, Max: h}
	}
}

func isActiveTab(s QuestionnaireState, ctx PerTabBindingContext) bool {
	return ctx.I == SelectActivePreviewPaneIndex(s.CurrentTab, ctx.TotalQuestions)
}

// BuildQuestionnaire assembles every TUI component, the props adapter, and a lifecycle handle. Initial paint is
// delegated to adapter.Apply(initialState) (called by the session at construction end). upstream: buildQuestionnaire.
func BuildQuestionnaire(config QuestionnaireBuildConfig) QuestionnaireBuilt {
	return newQuestionnaireBuilder(config).build()
}

type questionnaireBuilder struct {
	cfg           QuestionnaireBuildConfig
	selectTheme   WrappingSelectTheme
	markdownTheme *tui.MarkdownTheme
	notesInput    *tui.Editor
	inlineInput   *tui.Editor
}

func newQuestionnaireBuilder(config QuestionnaireBuildConfig) *questionnaireBuilder {
	b := &questionnaireBuilder{cfg: config, markdownTheme: GetMarkdownTheme(config.Theme)}
	b.selectTheme = b.makeSelectTheme()
	textEditorTheme := editorTheme(config.Theme)
	b.notesInput = tui.NewEditor(textEditorTheme)
	b.inlineInput = tui.NewEditor(textEditorTheme)
	// The key router owns confirm/submit semantics; keys reaching these headless editors are text-editing only.
	// Without this, a tui.input.submit match would run submitValue(), which resets the buffer (#156).
	b.notesInput.DisableSubmit = true
	b.inlineInput.DisableSubmit = true
	return b
}

func (b *questionnaireBuilder) getTerminalWidth() int { return b.cfg.TUI.Columns() }
func (b *questionnaireBuilder) getTerminalRows() int  { return b.cfg.TUI.Rows() }

func (b *questionnaireBuilder) build() QuestionnaireBuilt {
	tabs := b.buildTabComponents()
	b.injectGlobalLeftWidth(tabs)
	submitPicker := b.buildSubmitPicker()
	tabBar := b.buildTabBar()
	heights := b.buildHeightComputers(tabs)
	dialog := b.buildDialog(tabs, submitPicker, tabBar, heights)
	globalBindings := b.buildGlobalBindings(dialog, submitPicker, tabBar)
	perTabBindings := b.buildPerTabBindings()
	adapter := b.buildAdapter(tabs, globalBindings, perTabBindings)
	return b.handle(adapter, dialog)
}

func (b *questionnaireBuilder) makeSelectTheme() WrappingSelectTheme {
	t := b.cfg.Theme
	return WrappingSelectTheme{
		SelectedText: func(s string) string { return t.Fg("accent", t.Bold(s)) },
		Description:  func(s string) string { return t.Fg("muted", s) },
		ScrollInfo:   func(s string) string { return t.Fg("dim", s) },
	}
}

func (b *questionnaireBuilder) buildTabComponents() []TabComponents {
	tabs := make([]TabComponents, 0, len(b.cfg.Questions))
	for i, q := range b.cfg.Questions {
		tabs = append(tabs, b.buildTabFor(q, i))
	}
	return tabs
}

func (b *questionnaireBuilder) buildTabFor(question QuestionData, index int) TabComponents {
	var items []WrappingSelectItem
	if index < len(b.cfg.ItemsByTab) {
		items = b.cfg.ItemsByTab[index]
	}
	optionList := NewOptionListView(OptionListViewConfig{Items: items, Theme: b.selectTheme})
	previewBlock := NewPreviewBlockRenderer(PreviewBlockRendererConfig{
		Question: question, Theme: b.cfg.Theme, MarkdownTheme: b.markdownTheme,
	})
	preview := NewPreviewPane(PreviewPaneConfig{
		Question: question, GetTerminalWidth: b.getTerminalWidth, OptionListView: optionList, PreviewBlock: previewBlock,
	})
	var multiSelect TabMultiSelect
	var bodyHeights func(width int) TabBodyHeights
	if question.MultiSelect {
		multiSelect = NewMultiSelectView(b.cfg.Theme, question)
		bodyHeights = multiSelectBodyHeights(multiSelect)
	} else {
		bodyHeights = previewBodyHeights(preview)
	}
	return TabComponents{OptionList: optionList, Preview: preview, MultiSelect: multiSelect, BodyHeights: bodyHeights}
}

// injectGlobalLeftWidth computes the cross-tab max adaptive left width and injects it into each PreviewPane.
func (b *questionnaireBuilder) injectGlobalLeftWidth(tabs []TabComponents) {
	questions := b.cfg.Questions
	itemsByTab := b.cfg.ItemsByTab
	globalLeftWidth := func(paneWidth int) int {
		return CrossTabLeftWidthWithDonation(len(questions), itemsByTab, questions, paneWidth)
	}
	for _, tab := range tabs {
		tab.Preview.SetGlobalLeftWidth(globalLeftWidth)
	}
}

func (b *questionnaireBuilder) buildSubmitPicker() *SubmitPicker {
	if b.cfg.IsMulti {
		return NewSubmitPicker(b.cfg.Theme)
	}
	return nil
}

func (b *questionnaireBuilder) buildTabBar() *TabBar {
	if b.cfg.IsMulti {
		return NewTabBar(b.cfg.Theme)
	}
	return nil
}

func (b *questionnaireBuilder) buildHeightComputers(tabs []TabComponents) heightComputers {
	global := func(width int) int {
		m := 0
		for _, tab := range tabs {
			if h := tab.BodyHeights(width).Max; h > m {
				m = h
			}
		}
		return max(1, m)
	}
	current := func(width int) int {
		idx := min(b.cfg.GetCurrentTab(), len(tabs)-1)
		if idx < 0 || idx >= len(tabs) {
			return 0
		}
		return max(0, tabs[idx].BodyHeights(width).Current)
	}
	return heightComputers{global: global, current: current}
}

func (b *questionnaireBuilder) pickInitialActivePreview(tabs []TabComponents) StatefulView[PreviewPaneProps] {
	idx := SelectActivePreviewPaneIndex(b.cfg.InitialState.CurrentTab, len(b.cfg.Questions))
	if idx >= 0 && idx < len(tabs) {
		return tabs[idx].Preview
	}
	return tabs[0].Preview
}

func (b *questionnaireBuilder) buildDialog(tabs []TabComponents, submitPicker *SubmitPicker, tabBar *TabBar, heights heightComputers) *DialogView {
	var picker tui.Component
	if submitPicker != nil {
		picker = submitPicker
	}
	initial := b.cfg.InitialState
	return NewDialogView(DialogConfig{
		Theme:                b.cfg.Theme,
		Questions:            b.cfg.Questions,
		TabBar:               tabBar,
		NotesInput:           b.notesInput,
		IsMulti:              b.cfg.IsMulti,
		TabsByIndex:          tabs,
		SubmitPicker:         picker,
		GetBodyHeight:        heights.global,
		GetCurrentBodyHeight: heights.current,
		GetTerminalRows:      b.getTerminalRows,
		CollapseKey:          b.cfg.CollapseKey,
	}, DialogProps{State: &initial, ActivePreviewPane: b.pickInitialActivePreview(tabs)})
}

func (b *questionnaireBuilder) buildGlobalBindings(dialog *DialogView, submitPicker *SubmitPicker, tabBar *TabBar) []BoundGlobalBinding {
	out := []BoundGlobalBinding{GlobalBinding(ComponentBinding[DialogProps]{Component: dialog, Select: SelectDialogProps})}
	if submitPicker != nil {
		out = append(out, GlobalBinding(ComponentBinding[SubmitPickerProps]{Component: submitPicker, Select: SelectSubmitPickerProps}))
	}
	if tabBar != nil {
		out = append(out, GlobalBinding(ComponentBinding[TabBarProps]{Component: tabBar, Select: SelectTabBarProps}))
	}
	return out
}

func (b *questionnaireBuilder) buildPerTabBindings() []BoundPerTabBinding {
	return []BoundPerTabBinding{
		PerTabBindingOf(PerTabBinding[OptionListViewProps]{
			Resolve:   func(tab *TabComponents) StatefulView[OptionListViewProps] { return tab.OptionList },
			Predicate: isActiveTab,
			Select:    SelectOptionListProps,
		}),
		PerTabBindingOf(PerTabBinding[PreviewPaneProps]{
			Resolve:   func(tab *TabComponents) StatefulView[PreviewPaneProps] { return tab.Preview },
			Predicate: isActiveTab,
			Select:    SelectPreviewPaneProps,
		}),
		PerTabBindingOf(PerTabBinding[MultiSelectViewProps]{
			Resolve: func(tab *TabComponents) StatefulView[MultiSelectViewProps] {
				if tab.MultiSelect == nil {
					return nil // a nil TabMultiSelect must not become a non-nil StatefulView
				}
				return tab.MultiSelect
			},
			Select: SelectMultiSelectProps,
		}),
	}
}

func (b *questionnaireBuilder) buildAdapter(tabs []TabComponents, globalBindings []BoundGlobalBinding, perTabBindings []BoundPerTabBinding) *QuestionnairePropsAdapter {
	return NewQuestionnairePropsAdapter(QuestionnairePropsAdapterConfig{
		TUI:                 b.cfg.TUI,
		Questions:           b.cfg.Questions,
		ItemsByTab:          b.cfg.ItemsByTab,
		TabsByIndex:         tabs,
		InlineInput:         b.inlineInput,
		GlobalBindings:      globalBindings,
		PerTabBindings:      perTabBindings,
		ExtraInvalidatables: []invalidatable{b.notesInput},
	})
}

func (b *questionnaireBuilder) handle(adapter *QuestionnairePropsAdapter, dialog *DialogView) QuestionnaireBuilt {
	return QuestionnaireBuilt{
		Adapter:     adapter,
		NotesInput:  b.notesInput,
		InlineInput: b.inlineInput,
		Render:      func(w int) []string { return dialog.Render(w) },
		Invalidate:  func() { adapter.Invalidate() },
	}
}
