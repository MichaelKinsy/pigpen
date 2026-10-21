package ui

import tui "github.com/MichaelKinsy/pigpen/components/pitui"

// Keystroke dispatcher. upstream: state/key-router.ts.

const (
	keybindUp             = "tui.select.up"
	keybindDown           = "tui.select.down"
	keybindConfirm        = "tui.select.confirm"
	keybindSubmit         = "tui.input.submit"
	keybindCancel         = "tui.select.cancel"
	keybindNewLine        = "tui.input.newLine"
	keybindEditorUp       = "tui.editor.cursorUp"
	keybindEditorDown     = "tui.editor.cursorDown"
	keybindClear          = "tui.editor.deleteToLineStart"
	keybindExternalEditor = "app.editor.external"

	notesActivateKey = "n"
	spaceKey         = " "
)

// Action kinds. upstream: QuestionnaireAction union.
const (
	ActionNav             = "nav"
	ActionInputClear      = "input_clear"
	ActionInputEdit       = "input_edit"
	ActionInputReplace    = "input_replace"
	ActionTabSwitch       = "tab_switch"
	ActionConfirm         = "confirm"
	ActionToggle          = "toggle"
	ActionMultiConfirm    = "multi_confirm"
	ActionCancel          = "cancel"
	ActionNotesEnter      = "notes_enter"
	ActionNotesExit       = "notes_exit"
	ActionSubmit          = "submit"
	ActionSubmitNav       = "submit_nav"
	ActionNotesForward    = "notes_forward"
	ActionToggleCollapsed = "toggle_collapsed"
	ActionIgnore          = "ignore"
)

// QuestionnaireAction is the discriminated union of dispatcher actions. Which fields are meaningful depends on
// Kind: nav (NextIndex, InputValue), input_edit/input_replace (Value), tab_switch (NextTab), confirm (Answer,
// AutoAdvanceTab), toggle (Index), multi_confirm (Selected, AutoAdvanceTab), submit_nav (NextIndex 0|1),
// notes_forward (Data). AutoAdvanceTab nil is the original's absent/undefined.
type QuestionnaireAction struct {
	Kind           string
	NextIndex      int
	InputValue     string
	Value          string
	NextTab        int
	Answer         QuestionAnswer
	AutoAdvanceTab *int
	Index          int
	Selected       []string
	Data           string
}

// isConfirm: confirm has two semantic sources, tui.select.confirm and tui.input.submit (see the original, #156).
func isConfirm(kb tui.Keybindings, data string) bool {
	return kb.Matches(data, keybindConfirm) || kb.Matches(data, keybindSubmit)
}

// WrapTab wraps index into [0,total). upstream: wrapTab.
func WrapTab(index, total int) int {
	if total <= 0 {
		return 0
	}
	return ((index % total) + total) % total
}

// AllAnswered reports whether every question has an answer. upstream: allAnswered.
func AllAnswered(state QuestionnaireState, runtime QuestionnaireRuntime) bool {
	if len(runtime.Questions) == 0 {
		return false
	}
	for i := range runtime.Questions {
		if _, ok := state.Answers[i]; !ok {
			return false
		}
	}
	return true
}

func totalTabs(runtime QuestionnaireRuntime) int {
	if runtime.IsMulti {
		return len(runtime.Questions) + 1
	}
	return 1
}

func computeAutoAdvanceTab(state QuestionnaireState, runtime QuestionnaireRuntime) *int {
	if !runtime.IsMulti {
		return nil
	}
	n := len(runtime.Questions)
	if state.CurrentTab < n-1 {
		v := state.CurrentTab + 1
		return &v
	}
	return &n
}

func questionAt(runtime QuestionnaireRuntime, tab int) (QuestionData, bool) {
	if tab < 0 || tab >= len(runtime.Questions) {
		return QuestionData{}, false
	}
	return runtime.Questions[tab], true
}

func buildSingleSelectAnswer(state QuestionnaireState, runtime QuestionnaireRuntime) (QuestionAnswer, bool) {
	q, ok := questionAt(runtime, state.CurrentTab)
	if !ok {
		return QuestionAnswer{}, false
	}
	item := runtime.CurrentItem
	if state.InputMode {
		label := runtime.InputBuffer
		a := QuestionAnswer{QuestionIndex: state.CurrentTab, Question: q.Question, Kind: "custom"}
		if len(label) > 0 {
			a.Answer = &label
		}
		return a, true
	}
	if item == nil {
		return QuestionAnswer{}, false
	}
	if item.Kind == KindOther || item.Kind == KindNext {
		return QuestionAnswer{}, false
	}
	label := item.Label
	return QuestionAnswer{QuestionIndex: state.CurrentTab, Question: q.Question, Kind: "option", Answer: &label}, true
}

func buildMultiSelected(state QuestionnaireState, runtime QuestionnaireRuntime) []string {
	out := []string{}
	q, ok := questionAt(runtime, state.CurrentTab)
	if !ok {
		return out
	}
	for i, o := range q.Options {
		if state.MultiSelectChecked[i] {
			out = append(out, o.Label)
		}
	}
	return out
}

func tabSwitchAction(data string, state QuestionnaireState, runtime QuestionnaireRuntime) (QuestionnaireAction, bool) {
	if !runtime.IsMulti {
		return QuestionnaireAction{}, false
	}
	total := totalTabs(runtime)
	if tui.MatchesKey(data, "tab") || tui.MatchesKey(data, "right") {
		return QuestionnaireAction{Kind: ActionTabSwitch, NextTab: WrapTab(state.CurrentTab+1, total)}, true
	}
	if tui.MatchesKey(data, "shift+tab") || tui.MatchesKey(data, "left") {
		return QuestionnaireAction{Kind: ActionTabSwitch, NextTab: WrapTab(state.CurrentTab-1, total)}, true
	}
	return QuestionnaireAction{}, false
}

func nextNavOnDown(state QuestionnaireState, runtime QuestionnaireRuntime) QuestionnaireAction {
	return QuestionnaireAction{Kind: ActionNav, NextIndex: WrapTab(state.OptionIndex+1, max(1, len(runtime.Items))), InputValue: runtime.InputBuffer}
}

func prevNavOnUp(state QuestionnaireState, runtime QuestionnaireRuntime) QuestionnaireAction {
	return QuestionnaireAction{Kind: ActionNav, NextIndex: WrapTab(state.OptionIndex-1, max(1, len(runtime.Items))), InputValue: runtime.InputBuffer}
}

// routeCollapsed: while collapsed, swallow every keystroke except cancel.
func routeCollapsed(kb tui.Keybindings, data string) QuestionnaireAction {
	if kb.Matches(data, keybindCancel) {
		return QuestionnaireAction{Kind: ActionCancel}
	}
	return QuestionnaireAction{Kind: ActionIgnore}
}

func routeNotesMode(kb tui.Keybindings, data string) QuestionnaireAction {
	if kb.Matches(data, keybindCancel) {
		return QuestionnaireAction{Kind: ActionNotesExit}
	}
	if kb.Matches(data, keybindNewLine) {
		return QuestionnaireAction{Kind: ActionNotesForward, Data: data}
	}
	if isConfirm(kb, data) {
		return QuestionnaireAction{Kind: ActionNotesExit}
	}
	return QuestionnaireAction{Kind: ActionNotesForward, Data: data}
}

func routeInputMode(kb tui.Keybindings, data string, state QuestionnaireState, runtime QuestionnaireRuntime) QuestionnaireAction {
	// Newline takes precedence over confirmation if one key is bound to both.
	if kb.Matches(data, keybindNewLine) {
		return QuestionnaireAction{Kind: ActionIgnore}
	}
	if isConfirm(kb, data) {
		answer, ok := buildSingleSelectAnswer(state, runtime)
		if !ok {
			return QuestionnaireAction{Kind: ActionIgnore}
		}
		return QuestionnaireAction{Kind: ActionConfirm, Answer: answer, AutoAdvanceTab: computeAutoAdvanceTab(state, runtime)}
	}
	if kb.Matches(data, keybindClear) {
		return QuestionnaireAction{Kind: ActionInputClear}
	}
	if kb.Matches(data, keybindExternalEditor) {
		return QuestionnaireAction{Kind: ActionInputEdit, Value: runtime.InputBuffer}
	}
	if kb.Matches(data, keybindCancel) {
		return QuestionnaireAction{Kind: ActionCancel}
	}
	if kb.Matches(data, keybindEditorUp) && runtime.CanMoveInputUp {
		return QuestionnaireAction{Kind: ActionIgnore}
	}
	if kb.Matches(data, keybindEditorDown) && runtime.CanMoveInputDown {
		return QuestionnaireAction{Kind: ActionIgnore}
	}
	if kb.Matches(data, keybindUp) {
		return prevNavOnUp(state, runtime)
	}
	if kb.Matches(data, keybindDown) {
		return nextNavOnDown(state, runtime)
	}
	return QuestionnaireAction{Kind: ActionIgnore}
}

func routeSubmitTab(kb tui.Keybindings, data string, state QuestionnaireState, runtime QuestionnaireRuntime) QuestionnaireAction {
	if kb.Matches(data, keybindCancel) {
		return QuestionnaireAction{Kind: ActionCancel}
	}
	if tab, ok := tabSwitchAction(data, state, runtime); ok {
		return tab
	}
	if kb.Matches(data, keybindUp) || kb.Matches(data, keybindDown) {
		delta := -1
		if kb.Matches(data, keybindDown) {
			delta = 1
		}
		next := WrapTab(state.SubmitChoiceIndex+delta, 2)
		if next != 1 {
			next = 0
		}
		return QuestionnaireAction{Kind: ActionSubmitNav, NextIndex: next}
	}
	if isConfirm(kb, data) {
		// Submit always submits; Cancel always cancels. The warning header is informational only.
		if state.SubmitChoiceIndex == 1 {
			return QuestionnaireAction{Kind: ActionCancel}
		}
		return QuestionnaireAction{Kind: ActionSubmit}
	}
	// Global note (#182): `n` on the Submit tab opens the notes editor for the whole questionnaire.
	if data == notesActivateKey {
		return QuestionnaireAction{Kind: ActionNotesEnter}
	}
	return QuestionnaireAction{Kind: ActionIgnore}
}

func routeMultiSelectTab(kb tui.Keybindings, data string, state QuestionnaireState, runtime QuestionnaireRuntime) QuestionnaireAction {
	var focusedMeta *RowIntentMeta
	if runtime.CurrentItem != nil && runtime.CurrentItem.Kind != "" {
		m := RowIntentMetaTable[runtime.CurrentItem.Kind]
		focusedMeta = &m
	}
	blocksToggle := focusedMeta != nil && focusedMeta.BlocksMultiToggle
	activatesInput := focusedMeta != nil && focusedMeta.ActivatesInputMode
	autoSubmits := focusedMeta != nil && focusedMeta.AutoSubmitsInMulti
	// Space toggles the focused row's checkbox, suppressed on Next and on the inline-input row.
	if data == spaceKey {
		if blocksToggle || activatesInput {
			return QuestionnaireAction{Kind: ActionIgnore}
		}
		return QuestionnaireAction{Kind: ActionToggle, Index: state.OptionIndex}
	}
	if isConfirm(kb, data) {
		if activatesInput {
			return QuestionnaireAction{Kind: ActionIgnore}
		}
		if !autoSubmits {
			return QuestionnaireAction{Kind: ActionToggle, Index: state.OptionIndex}
		}
		return QuestionnaireAction{
			Kind:           ActionMultiConfirm,
			Selected:       buildMultiSelected(state, runtime),
			AutoAdvanceTab: computeAutoAdvanceTab(state, runtime),
		}
	}
	if kb.Matches(data, keybindCancel) {
		return QuestionnaireAction{Kind: ActionCancel}
	}
	return QuestionnaireAction{Kind: ActionIgnore}
}

func routeSingleSelectTab(kb tui.Keybindings, data string, state QuestionnaireState, runtime QuestionnaireRuntime) QuestionnaireAction {
	if isConfirm(kb, data) {
		answer, ok := buildSingleSelectAnswer(state, runtime)
		if !ok {
			return QuestionnaireAction{Kind: ActionIgnore}
		}
		return QuestionnaireAction{Kind: ActionConfirm, Answer: answer, AutoAdvanceTab: computeAutoAdvanceTab(state, runtime)}
	}
	if kb.Matches(data, keybindCancel) {
		return QuestionnaireAction{Kind: ActionCancel}
	}
	return QuestionnaireAction{Kind: ActionIgnore}
}

// RouteKey maps one keystroke to an action. upstream: routeKey.
func RouteKey(data string, state QuestionnaireState, runtime QuestionnaireRuntime) QuestionnaireAction {
	kb := runtime.Keybindings

	// Collapse/expand toggle is intercepted at the top so it works from every inner state. "off" disables it, and
	// so does an empty spec (the original's missing/non-string key at the runtime boundary).
	if runtime.CollapseKey != "" && runtime.CollapseKey != "off" && tui.MatchesKey(data, runtime.CollapseKey) {
		return QuestionnaireAction{Kind: ActionToggleCollapsed}
	}

	if state.Collapsed {
		return routeCollapsed(kb, data)
	}
	if state.NotesVisible {
		return routeNotesMode(kb, data)
	}
	if state.InputMode {
		return routeInputMode(kb, data, state, runtime)
	}
	if runtime.IsMulti && state.CurrentTab == len(runtime.Questions) {
		return routeSubmitTab(kb, data, state, runtime)
	}

	if tab, ok := tabSwitchAction(data, state, runtime); ok {
		return tab
	}

	q, ok := questionAt(runtime, state.CurrentTab)
	if !ok {
		return QuestionnaireAction{Kind: ActionIgnore}
	}

	// Universal `n` activation (FR-1): the notes editor opens on every question tab.
	if data == notesActivateKey {
		return QuestionnaireAction{Kind: ActionNotesEnter}
	}

	if kb.Matches(data, keybindUp) {
		return prevNavOnUp(state, runtime)
	}
	if kb.Matches(data, keybindDown) {
		return nextNavOnDown(state, runtime)
	}

	if q.MultiSelect {
		return routeMultiSelectTab(kb, data, state, runtime)
	}
	return routeSingleSelectTab(kb, data, state, runtime)
}
