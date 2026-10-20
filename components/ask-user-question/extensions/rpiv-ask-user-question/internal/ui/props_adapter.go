package ui

import (
	tui "github.com/MichaelKinsy/pigpen/components/pitui"
	"github.com/MichaelKinsy/pigpen/components/pitui/jsstring"
)

// View fan-out. upstream: view/props-adapter.ts.

// invalidatable is the cache-invalidation contract used by the adapter.
type invalidatable interface{ Invalidate() }

// getInputCursorOffset is the cursor as an offset into the text (UTF-16 code units, like JS .length).
func getInputCursorOffset(input *tui.Editor) int {
	lines := input.GetLines()
	line, col := input.GetCursor()
	offset := col
	for i := 0; i < line; i++ {
		if i < len(lines) {
			offset += jsstring.Length(lines[i]) + 1
		} else {
			offset++
		}
	}
	return offset
}

// QuestionnairePropsAdapterConfig is the adapter's constructor config.
type QuestionnairePropsAdapterConfig struct {
	TUI            interface{ RequestRender() }
	Questions      []QuestionData
	ItemsByTab     [][]WrappingSelectItem
	TabsByIndex    []TabComponents
	InlineInput    *tui.Editor
	GlobalBindings []BoundGlobalBinding
	PerTabBindings []BoundPerTabBinding
	// ExtraInvalidatables are renderables not reached by the binding registries (the notes Editor).
	ExtraInvalidatables []invalidatable
}

// QuestionnairePropsAdapter drives every component setter from the canonical state via two binding registries.
type QuestionnairePropsAdapter struct {
	cfg QuestionnairePropsAdapterConfig
}

// NewQuestionnairePropsAdapter is `new QuestionnairePropsAdapter(config)`.
func NewQuestionnairePropsAdapter(config QuestionnairePropsAdapterConfig) *QuestionnairePropsAdapter {
	return &QuestionnairePropsAdapter{cfg: config}
}

// Apply pushes the projection of state into every component and requests a render.
func (a *QuestionnairePropsAdapter) Apply(state QuestionnaireState) {
	totalQuestions := len(a.cfg.Questions)
	activeView := SelectActiveView(state.NotesVisible, state.CurrentTab, totalQuestions)
	paneIndex := SelectActivePreviewPaneIndex(state.CurrentTab, totalQuestions)
	var activePreviewPane StatefulView[PreviewPaneProps]
	if paneIndex < len(a.cfg.TabsByIndex) && a.cfg.TabsByIndex[paneIndex].Preview != nil {
		activePreviewPane = a.cfg.TabsByIndex[paneIndex].Preview
	} else {
		activePreviewPane = a.cfg.TabsByIndex[0].Preview
	}

	off := getInputCursorOffset(a.cfg.InlineInput)
	ctx := BindingContext{
		Questions:         a.cfg.Questions,
		ItemsByTab:        a.cfg.ItemsByTab,
		TotalQuestions:    totalQuestions,
		ActiveView:        activeView,
		InputBuffer:       a.cfg.InlineInput.GetText(),
		InputCursorOffset: &off,
		ActivePreviewPane: activePreviewPane,
	}

	for _, binding := range a.cfg.GlobalBindings {
		binding.Apply(state, ctx)
	}

	for i := range a.cfg.TabsByIndex {
		tabCtx := PerTabBindingContext{BindingContext: ctx, Tab: &a.cfg.TabsByIndex[i], I: i}
		for _, binding := range a.cfg.PerTabBindings {
			binding.Apply(state, tabCtx)
		}
	}

	a.cfg.TUI.RequestRender()
}

// Invalidate invalidates every owned renderable. upstream: invalidate.
func (a *QuestionnairePropsAdapter) Invalidate() {
	for _, b := range a.cfg.GlobalBindings {
		b.Invalidate()
	}
	for _, tab := range a.cfg.TabsByIndex {
		tab.OptionList.Invalidate()
		tab.Preview.Invalidate()
		if tab.MultiSelect != nil {
			tab.MultiSelect.Invalidate()
		}
	}
	for _, x := range a.cfg.ExtraInvalidatables {
		x.Invalidate()
	}
}
