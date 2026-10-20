package ui

import "github.com/MichaelKinsy/pigpen/components/pitui/jsstring"

// Pure reducer. upstream: state/state-reducer.ts.

// ApplyContext holds the session-lifetime constants. upstream: ApplyContext.
type ApplyContext struct {
	Questions  []QuestionData
	ItemsByTab [][]WrappingSelectItem
}

// Effect kinds. upstream: Effect union.
const (
	EffectSetInputBuffer        = "set_input_buffer"
	EffectClearInputBuffer      = "clear_input_buffer"
	EffectOpenInputEditor       = "open_input_editor"
	EffectSetNotesValue         = "set_notes_value"
	EffectSetNotesFocused       = "set_notes_focused"
	EffectForwardNotesKeystroke = "forward_notes_keystroke"
	EffectSetOverlayHidden      = "set_overlay_hidden"
	EffectDone                  = "done"
)

// Effect is a declarative side effect emitted by Reduce. Which fields are meaningful depends on Kind:
// Value (set_input_buffer, open_input_editor, set_notes_value), Focused (set_notes_focused), Data
// (forward_notes_keystroke), Hidden (set_overlay_hidden), Result (done).
type Effect struct {
	Kind    string
	Value   string
	Focused bool
	Data    string
	Hidden  bool
	Result  QuestionnaireResult
}

// ApplyResult is what Reduce returns. upstream: ApplyResult.
type ApplyResult struct {
	State   QuestionnaireState
	Effects []Effect
}

func orderedAnswers(state QuestionnaireState, questions []QuestionData) []QuestionAnswer {
	out := []QuestionAnswer{}
	for i := range questions {
		if a, ok := state.Answers[i]; ok {
			out = append(out, a)
		}
	}
	return out
}

func syncMultiSelectFromAnswers(answers map[int]QuestionAnswer, questions []QuestionData, tab int) map[int]bool {
	if tab < 0 || tab >= len(questions) || !questions[tab].MultiSelect {
		return map[int]bool{}
	}
	q := questions[tab]
	saved, has := answers[tab]
	var labels []string
	if has {
		labels = saved.Selected
	}
	indices := map[int]bool{}
	for i, o := range q.Options {
		for _, l := range labels {
			if l == o.Label {
				indices[i] = true
				break
			}
		}
	}
	return indices
}

func copyAnswers(m map[int]QuestionAnswer) map[int]QuestionAnswer {
	out := make(map[int]QuestionAnswer, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	return out
}

func copyStrings(m map[int]string) map[int]string {
	out := make(map[int]string, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	return out
}

func copySet(m map[int]bool) map[int]bool {
	out := make(map[int]bool, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	return out
}

func persistMultiSelectAnswer(state QuestionnaireState, ctx ApplyContext) map[int]QuestionAnswer {
	if state.CurrentTab < 0 || state.CurrentTab >= len(ctx.Questions) || !ctx.Questions[state.CurrentTab].MultiSelect {
		return state.Answers
	}
	q := ctx.Questions[state.CurrentTab]
	selected := []string{}
	for i, o := range q.Options {
		if state.MultiSelectChecked[i] {
			selected = append(selected, o.Label)
		}
	}
	out := copyAnswers(state.Answers)
	if len(selected) == 0 {
		delete(out, state.CurrentTab)
		return out
	}
	out[state.CurrentTab] = QuestionAnswer{
		QuestionIndex: state.CurrentTab,
		Question:      q.Question,
		Kind:          "multi",
		Answer:        nil,
		Selected:      selected,
		HasSelected:   true,
		Notes:         state.NotesByTab[state.CurrentTab],
	}
	return out
}

// notesValueFor is the note text to seed the editor/draft with for a tab: the side-band store is authoritative.
func notesValueFor(state QuestionnaireState, tab int) string {
	if v, ok := state.NotesByTab[tab]; ok {
		return v
	}
	return state.Answers[tab].Notes
}

func customDraftValueFor(state QuestionnaireState, tab int) string {
	if draft, ok := state.CustomDraftsByTab[tab]; ok {
		return draft
	}
	answer, ok := state.Answers[tab]
	if ok && answer.Kind == "custom" && answer.Answer != nil {
		return *answer.Answer
	}
	return ""
}

func setCustomDraft(state QuestionnaireState, tab int, value string) map[int]string {
	drafts := copyStrings(state.CustomDraftsByTab)
	drafts[tab] = value
	return drafts
}

func withoutCustomDraft(state QuestionnaireState, tab int) map[int]string {
	if _, ok := state.CustomDraftsByTab[tab]; !ok {
		return state.CustomDraftsByTab
	}
	drafts := copyStrings(state.CustomDraftsByTab)
	delete(drafts, tab)
	return drafts
}

func switchTabResult(state QuestionnaireState, nextTab int, ctx ApplyContext) ApplyResult {
	notesValue := notesValueFor(state, nextTab)
	transitioned := state
	transitioned.CurrentTab = nextTab
	transitioned.OptionIndex = 0
	transitioned.InputMode = false
	transitioned.NotesVisible = false
	transitioned.SubmitChoiceIndex = 0
	transitioned.MultiSelectChecked = syncMultiSelectFromAnswers(state.Answers, ctx.Questions, nextTab)
	transitioned.NotesDraft = notesValue
	return ApplyResult{
		State: transitioned,
		Effects: []Effect{
			{Kind: EffectSetNotesFocused, Focused: false},
			{Kind: EffectSetNotesValue, Value: notesValue},
			{Kind: EffectSetInputBuffer, Value: customDraftValueFor(state, nextTab)},
		},
	}
}

// doneFor lifts the Submit-tab global note (the len(questions) pseudo-index of NotesByTab) into the result.
func doneFor(state QuestionnaireState, ctx ApplyContext, cancelled bool) ApplyResult {
	result := QuestionnaireResult{
		Answers:    orderedAnswers(state, ctx.Questions),
		Cancelled:  cancelled,
		GlobalNote: state.NotesByTab[len(ctx.Questions)],
	}
	return ApplyResult{State: state, Effects: []Effect{{Kind: EffectDone, Result: result}}}
}

func navHandler(state QuestionnaireState, action QuestionnaireAction, ctx ApplyContext) ApplyResult {
	var items []WrappingSelectItem
	if state.CurrentTab >= 0 && state.CurrentTab < len(ctx.ItemsByTab) {
		items = ctx.ItemsByTab[state.CurrentTab]
	}
	inputMode := false
	if action.NextIndex >= 0 && action.NextIndex < len(items) {
		inputMode = RowIntentMetaTable[items[action.NextIndex].Kind].ActivatesInputMode
	}
	customDraftsByTab := state.CustomDraftsByTab
	if state.InputMode {
		customDraftsByTab = setCustomDraft(state, state.CurrentTab, action.InputValue)
	}
	next := state
	next.OptionIndex = action.NextIndex
	next.InputMode = inputMode
	next.CustomDraftsByTab = customDraftsByTab
	if !inputMode {
		return ApplyResult{State: next, Effects: []Effect{}}
	}
	return ApplyResult{State: next, Effects: []Effect{{Kind: EffectSetInputBuffer, Value: customDraftValueFor(next, state.CurrentTab)}}}
}

func confirmHandler(state QuestionnaireState, action QuestionnaireAction, ctx ApplyContext) ApplyResult {
	answer := action.Answer
	if answer.Kind == "option" && answer.Answer != nil && *answer.Answer != "" {
		if answer.QuestionIndex >= 0 && answer.QuestionIndex < len(ctx.Questions) {
			for _, o := range ctx.Questions[answer.QuestionIndex].Options {
				if o.Label == *answer.Answer {
					if len(o.Preview) > 0 {
						answer.Preview = o.Preview
					}
					break
				}
			}
		}
	}
	if pending := state.NotesByTab[answer.QuestionIndex]; len(pending) > 0 {
		answer.Notes = pending
	}
	answers := copyAnswers(state.Answers)
	answers[answer.QuestionIndex] = answer
	// Custom free-text on a multi-select tab is mutually exclusive with checkbox selections.
	isCustomMulti := answer.Kind == "custom" && answer.QuestionIndex >= 0 && answer.QuestionIndex < len(ctx.Questions) &&
		ctx.Questions[answer.QuestionIndex].MultiSelect
	customDraftsByTab := state.CustomDraftsByTab
	if answer.Kind == "custom" {
		customDraftsByTab = withoutCustomDraft(state, answer.QuestionIndex)
	}
	next := state
	next.Answers = answers
	next.CustomDraftsByTab = customDraftsByTab
	if isCustomMulti {
		next.MultiSelectChecked = map[int]bool{}
	}
	if action.AutoAdvanceTab != nil {
		return switchTabResult(next, *action.AutoAdvanceTab, ctx)
	}
	return doneFor(next, ctx, false)
}

func toggleHandler(state QuestionnaireState, action QuestionnaireAction, ctx ApplyContext) ApplyResult {
	checked := copySet(state.MultiSelectChecked)
	if checked[action.Index] {
		delete(checked, action.Index)
	} else {
		checked[action.Index] = true
	}
	intermediate := state
	intermediate.MultiSelectChecked = checked
	intermediate.Answers = persistMultiSelectAnswer(intermediate, ctx)
	return ApplyResult{State: intermediate, Effects: []Effect{}}
}

func multiConfirmHandler(state QuestionnaireState, action QuestionnaireAction, ctx ApplyContext) ApplyResult {
	if state.CurrentTab < 0 || state.CurrentTab >= len(ctx.Questions) {
		return ApplyResult{State: state, Effects: []Effect{}}
	}
	q := ctx.Questions[state.CurrentTab]
	answers := copyAnswers(state.Answers)
	answers[state.CurrentTab] = QuestionAnswer{
		QuestionIndex: state.CurrentTab,
		Question:      q.Question,
		Kind:          "multi",
		Answer:        nil,
		Selected:      action.Selected,
		HasSelected:   true,
		Notes:         state.NotesByTab[state.CurrentTab],
	}
	synced := state
	synced.Answers = answers
	synced.MultiSelectChecked = syncMultiSelectFromAnswers(answers, ctx.Questions, state.CurrentTab)
	if action.AutoAdvanceTab != nil {
		return switchTabResult(synced, *action.AutoAdvanceTab, ctx)
	}
	return doneFor(synced, ctx, false)
}

func notesEnterHandler(state QuestionnaireState, _ QuestionnaireAction, _ ApplyContext) ApplyResult {
	value := notesValueFor(state, state.CurrentTab)
	next := state
	next.NotesVisible = true
	next.NotesDraft = value
	return ApplyResult{State: next, Effects: []Effect{
		{Kind: EffectSetNotesValue, Value: value},
		{Kind: EffectSetNotesFocused, Focused: true},
	}}
}

func notesExitHandler(state QuestionnaireState, _ QuestionnaireAction, _ ApplyContext) ApplyResult {
	trimmed := jsstring.Trim(state.NotesDraft)
	notes := copyStrings(state.NotesByTab)
	answers := copyAnswers(state.Answers)
	if len(trimmed) == 0 {
		delete(notes, state.CurrentTab)
		if prev, ok := answers[state.CurrentTab]; ok && prev.Notes != "" {
			prev.Notes = ""
			answers[state.CurrentTab] = prev
		}
	} else {
		notes[state.CurrentTab] = trimmed
		if prev, ok := answers[state.CurrentTab]; ok {
			prev.Notes = trimmed
			answers[state.CurrentTab] = prev
		}
	}
	next := state
	next.NotesByTab = notes
	next.Answers = answers
	next.NotesVisible = false
	return ApplyResult{State: next, Effects: []Effect{{Kind: EffectSetNotesFocused, Focused: false}}}
}

// Reduce is the pure reducer: (state, action, ctx) -> (state, effects). upstream: reduce.
func Reduce(state QuestionnaireState, action QuestionnaireAction, ctx ApplyContext) ApplyResult {
	switch action.Kind {
	case ActionNav:
		return navHandler(state, action, ctx)
	case ActionInputClear:
		next := state
		next.CustomDraftsByTab = setCustomDraft(state, state.CurrentTab, "")
		return ApplyResult{State: next, Effects: []Effect{{Kind: EffectClearInputBuffer}}}
	case ActionInputEdit:
		return ApplyResult{State: state, Effects: []Effect{{Kind: EffectOpenInputEditor, Value: action.Value}}}
	case ActionInputReplace:
		next := state
		next.CustomDraftsByTab = setCustomDraft(state, state.CurrentTab, action.Value)
		return ApplyResult{State: next, Effects: []Effect{{Kind: EffectSetInputBuffer, Value: action.Value}}}
	case ActionTabSwitch:
		return switchTabResult(state, action.NextTab, ctx)
	case ActionConfirm:
		return confirmHandler(state, action, ctx)
	case ActionToggle:
		return toggleHandler(state, action, ctx)
	case ActionMultiConfirm:
		return multiConfirmHandler(state, action, ctx)
	case ActionCancel:
		return doneFor(state, ctx, true)
	case ActionNotesEnter:
		return notesEnterHandler(state, action, ctx)
	case ActionNotesExit:
		return notesExitHandler(state, action, ctx)
	case ActionNotesForward:
		return ApplyResult{State: state, Effects: []Effect{{Kind: EffectForwardNotesKeystroke, Data: action.Data}}}
	case ActionSubmit:
		return doneFor(state, ctx, false)
	case ActionSubmitNav:
		next := state
		next.SubmitChoiceIndex = action.NextIndex
		return ApplyResult{State: next, Effects: []Effect{}}
	case ActionToggleCollapsed:
		next := state
		next.Collapsed = !state.Collapsed
		return ApplyResult{State: next, Effects: []Effect{{Kind: EffectSetOverlayHidden, Hidden: !state.Collapsed}}}
	default: // ActionIgnore
		return ApplyResult{State: state, Effects: []Effect{}}
	}
}
