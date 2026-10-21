package ui

import (
	"reflect"
	"sort"
	"testing"
)

func optAns(idx int, question, label string) QuestionAnswer {
	return QuestionAnswer{QuestionIndex: idx, Question: question, Kind: "option", Answer: strp(label)}
}

func setKeys(m map[int]bool) []int {
	out := []int{}
	for k, v := range m {
		if v {
			out = append(out, k)
		}
	}
	sort.Ints(out)
	return out
}

func eqv(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v\nwant %#v", got, want)
	}
}

func hasEffect(effects []Effect, kind string) bool {
	for _, e := range effects {
		if e.Kind == kind {
			return true
		}
	}
	return false
}

func twoItems() [][]WrappingSelectItem { return [][]WrappingSelectItem{itemsRegular, itemsRegular} }

func TestStateReducer(t *testing.T) {
	const f = "state/state-reducer"
	noEffects := []Effect{}

	// reduce — nav
	tw(t, f, "regular nav keeps the active draft buffer intact", func(t *testing.T) {
		r := Reduce(makeQuestionnaireState(), QuestionnaireAction{Kind: ActionNav, NextIndex: 1, InputValue: ""}, makeApplyContext())
		eqv(t, r.State.OptionIndex, 1)
		eqv(t, r.State.InputMode, false)
		eqv(t, r.Effects, noEffects)
	})
	tw(t, f, "nav onto kind:'other' row with prior kind:'custom' answer restores the buffer", func(t *testing.T) {
		answers := map[int]QuestionAnswer{0: {QuestionIndex: 0, Question: "Pick one", Kind: "custom", Answer: strp("Hello")}}
		ctx := makeApplyContext(func(c *ApplyContext) { c.ItemsByTab = [][]WrappingSelectItem{itemsWithOther} })
		r := Reduce(makeQuestionnaireState(func(s *QuestionnaireState) { s.Answers = answers }), QuestionnaireAction{Kind: ActionNav, NextIndex: 2}, ctx)
		eqv(t, r.State.InputMode, true)
		eqv(t, r.Effects, []Effect{{Kind: EffectSetInputBuffer, Value: "Hello"}})
	})
	tw(t, f, "nav onto kind:'other' row with no draft resets the buffer", func(t *testing.T) {
		ctx := makeApplyContext(func(c *ApplyContext) { c.ItemsByTab = [][]WrappingSelectItem{itemsWithOther} })
		r := Reduce(makeQuestionnaireState(), QuestionnaireAction{Kind: ActionNav, NextIndex: 2}, ctx)
		eqv(t, r.State.InputMode, true)
		eqv(t, r.Effects, []Effect{{Kind: EffectSetInputBuffer, Value: ""}})
	})
	tw(t, f, "nav back onto kind:'other' restores the in-flight draft ahead of a confirmed answer", func(t *testing.T) {
		answers := map[int]QuestionAnswer{0: {QuestionIndex: 0, Question: "Pick one", Kind: "custom", Answer: strp("confirmed")}}
		ctx := makeApplyContext(func(c *ApplyContext) { c.ItemsByTab = [][]WrappingSelectItem{itemsWithOther} })
		state := makeQuestionnaireState(func(s *QuestionnaireState) { s.Answers = answers; s.CustomDraftsByTab = map[int]string{0: "draft"} })
		r := Reduce(state, QuestionnaireAction{Kind: ActionNav, NextIndex: 2}, ctx)
		eqv(t, r.Effects, []Effect{{Kind: EffectSetInputBuffer, Value: "draft"}})
	})
	tw(t, f, "an explicitly cleared draft does not resurrect a confirmed custom answer", func(t *testing.T) {
		answers := map[int]QuestionAnswer{0: {QuestionIndex: 0, Question: "Pick one", Kind: "custom", Answer: strp("confirmed")}}
		ctx := makeApplyContext(func(c *ApplyContext) { c.ItemsByTab = [][]WrappingSelectItem{itemsWithOther} })
		state := makeQuestionnaireState(func(s *QuestionnaireState) { s.Answers = answers; s.CustomDraftsByTab = map[int]string{0: ""} })
		r := Reduce(state, QuestionnaireAction{Kind: ActionNav, NextIndex: 2}, ctx)
		eqv(t, r.Effects, []Effect{{Kind: EffectSetInputBuffer, Value: ""}})
	})
	tw(t, f, "snapshots the live input value when navigation leaves the custom row", func(t *testing.T) {
		state := makeQuestionnaireState(func(s *QuestionnaireState) { s.OptionIndex = 2; s.InputMode = true })
		ctx := makeApplyContext(func(c *ApplyContext) { c.ItemsByTab = [][]WrappingSelectItem{itemsWithOther} })
		r := Reduce(state, QuestionnaireAction{Kind: ActionNav, NextIndex: 1, InputValue: "draft"}, ctx)
		eqv(t, r.State.CustomDraftsByTab[0], "draft")
	})

	// reduce — tab_switch
	tw(t, f, "emits set_notes_focused(false) + set_notes_value", func(t *testing.T) {
		ctx := makeApplyContext(func(c *ApplyContext) {
			c.Questions = []QuestionData{makeQuestion(), makeQuestion()}
			c.ItemsByTab = twoItems()
		})
		r := Reduce(makeQuestionnaireState(), QuestionnaireAction{Kind: ActionTabSwitch, NextTab: 1}, ctx)
		eqv(t, r.State.CurrentTab, 1)
		eqv(t, r.State.OptionIndex, 0)
		eqv(t, r.State.NotesVisible, false)
		eqv(t, r.Effects, []Effect{
			{Kind: EffectSetNotesFocused, Focused: false},
			{Kind: EffectSetNotesValue, Value: ""},
			{Kind: EffectSetInputBuffer, Value: ""},
		})
	})
	tw(t, f, "rehydrates the target question's custom draft without leaking the current tab", func(t *testing.T) {
		ctx := makeApplyContext(func(c *ApplyContext) {
			c.Questions = []QuestionData{makeQuestion(), makeQuestion()}
			c.ItemsByTab = twoItems()
		})
		state := makeQuestionnaireState(func(s *QuestionnaireState) { s.CustomDraftsByTab = map[int]string{0: "first", 1: "second"} })
		r := Reduce(state, QuestionnaireAction{Kind: ActionTabSwitch, NextTab: 1}, ctx)
		found := false
		for _, e := range r.Effects {
			if reflect.DeepEqual(e, Effect{Kind: EffectSetInputBuffer, Value: "second"}) {
				found = true
			}
		}
		if !found {
			t.Fatalf("%+v", r.Effects)
		}
	})

	// reduce — confirm
	tw(t, f, "regular option without preview emits done with the answer", func(t *testing.T) {
		action := QuestionnaireAction{Kind: ActionConfirm, Answer: optAns(0, "Pick one", "A")}
		r := Reduce(makeQuestionnaireState(), action, makeApplyContext())
		eqv(t, *r.State.Answers[0].Answer, "A")
		eqv(t, r.Effects, []Effect{{Kind: EffectDone, Result: QuestionnaireResult{Answers: []QuestionAnswer{r.State.Answers[0]}, Cancelled: false}}})
	})
	tw(t, f, "makes the confirmed custom answer authoritative by removing its draft", func(t *testing.T) {
		action := QuestionnaireAction{Kind: ActionConfirm, Answer: QuestionAnswer{QuestionIndex: 0, Question: "Pick one", Kind: "custom", Answer: strp("latest")}}
		state := makeQuestionnaireState(func(s *QuestionnaireState) { s.CustomDraftsByTab = map[int]string{0: "stale"} })
		r := Reduce(state, action, makeApplyContext())
		if _, has := r.State.CustomDraftsByTab[0]; has {
			t.Fatal("draft kept")
		}
		eqv(t, *r.State.Answers[0].Answer, "latest")
	})
	tw(t, f, "regular option matching a preview-bearing option augments answer.preview", func(t *testing.T) {
		questions := []QuestionData{makeQuestion(func(q *QuestionData) {
			q.Options = []OptionData{{Label: "A", Description: "a", Preview: "code"}, {Label: "B", Description: "b"}}
		})}
		action := QuestionnaireAction{Kind: ActionConfirm, Answer: optAns(0, "Pick one", "A")}
		r := Reduce(makeQuestionnaireState(), action, makeApplyContext(func(c *ApplyContext) { c.Questions = questions }))
		eqv(t, r.State.Answers[0].Preview, "code")
	})
	tw(t, f, "merges pendingNotes from notesByTab into the confirmed answer", func(t *testing.T) {
		action := QuestionnaireAction{Kind: ActionConfirm, Answer: optAns(0, "Pick one", "A")}
		state := makeQuestionnaireState(func(s *QuestionnaireState) { s.NotesByTab = map[int]string{0: "  side note  "} })
		r := Reduce(state, action, makeApplyContext())
		eqv(t, r.State.Answers[0].Notes, "  side note  ")
	})
	tw(t, f, "autoAdvanceTab dispatches a tab_switch result instead of done", func(t *testing.T) {
		one := 1
		action := QuestionnaireAction{Kind: ActionConfirm, Answer: optAns(0, "Pick one", "A"), AutoAdvanceTab: &one}
		ctx := makeApplyContext(func(c *ApplyContext) {
			c.Questions = []QuestionData{makeQuestion(), makeQuestion()}
			c.ItemsByTab = twoItems()
		})
		r := Reduce(makeQuestionnaireState(), action, ctx)
		eqv(t, r.State.CurrentTab, 1)
		eqv(t, hasEffect(r.Effects, EffectSetNotesFocused), true)
		eqv(t, hasEffect(r.Effects, EffectDone), false)
	})

	// reduce — toggle
	tw(t, f, "toggles index 0 on then off and persists into answers", func(t *testing.T) {
		ctx := makeApplyContext(func(c *ApplyContext) { c.Questions = []QuestionData{makeQuestion(multiQ)} })
		r1 := Reduce(makeQuestionnaireState(), QuestionnaireAction{Kind: ActionToggle, Index: 0}, ctx)
		eqv(t, r1.State.MultiSelectChecked[0], true)
		eqv(t, r1.State.Answers[0].Selected, []string{"A"})
		r2 := Reduce(r1.State, QuestionnaireAction{Kind: ActionToggle, Index: 0}, ctx)
		eqv(t, r2.State.MultiSelectChecked[0], false)
		_, has := r2.State.Answers[0]
		eqv(t, has, false)
	})

	tw(t, f, "multiSelectChecked is reconstructed from answers on tab-back", func(t *testing.T) {
		questions := []QuestionData{makeQuestion(multiQ), makeQuestion()}
		ctx := makeApplyContext(func(c *ApplyContext) { c.Questions = questions; c.ItemsByTab = twoItems() })
		s := makeQuestionnaireState()
		s = Reduce(s, QuestionnaireAction{Kind: ActionToggle, Index: 0}, ctx).State
		s = Reduce(s, QuestionnaireAction{Kind: ActionToggle, Index: 1}, ctx).State
		eqv(t, setKeys(s.MultiSelectChecked), []int{0, 1})
		eqv(t, s.Answers[0].Selected, []string{"A", "B"})
		s = Reduce(s, QuestionnaireAction{Kind: ActionTabSwitch, NextTab: 1}, ctx).State
		eqv(t, setKeys(s.MultiSelectChecked), []int{})
		s = Reduce(s, QuestionnaireAction{Kind: ActionTabSwitch, NextTab: 0}, ctx).State
		eqv(t, setKeys(s.MultiSelectChecked), []int{0, 1})
	})

	tw(t, f, "persists answer + multiSelectChecked from action.selected", func(t *testing.T) {
		ctx := makeApplyContext(func(c *ApplyContext) { c.Questions = []QuestionData{makeQuestion(multiQ)} })
		r := Reduce(makeQuestionnaireState(), QuestionnaireAction{Kind: ActionMultiConfirm, Selected: []string{"A", "B"}}, ctx)
		eqv(t, r.State.Answers[0].Selected, []string{"A", "B"})
		eqv(t, setKeys(r.State.MultiSelectChecked), []int{0, 1})
		eqv(t, hasEffect(r.Effects, EffectDone), true)
	})

	tw(t, f, "cancel emits done with cancelled: true", func(t *testing.T) {
		r := Reduce(makeQuestionnaireState(), QuestionnaireAction{Kind: ActionCancel}, makeApplyContext())
		eqv(t, r.Effects, []Effect{{Kind: EffectDone, Result: QuestionnaireResult{Answers: []QuestionAnswer{}, Cancelled: true}}})
	})
	tw(t, f, "submit emits done with cancelled: false", func(t *testing.T) {
		r := Reduce(makeQuestionnaireState(), QuestionnaireAction{Kind: ActionSubmit}, makeApplyContext())
		eqv(t, r.Effects, []Effect{{Kind: EffectDone, Result: QuestionnaireResult{Answers: []QuestionAnswer{}, Cancelled: false}}})
	})

	// global note lift
	twoCtx := func() ApplyContext {
		return makeApplyContext(func(c *ApplyContext) {
			c.Questions = []QuestionData{makeQuestion(), makeQuestion()}
			c.ItemsByTab = twoItems()
		})
	}
	tw(t, f, "submit on the Submit tab lifts notesByTab[questions.length] into result.globalNote (strict toEqual)", func(t *testing.T) {
		state := makeQuestionnaireState(func(s *QuestionnaireState) { s.CurrentTab = 2; s.NotesByTab = map[int]string{2: "ship it Friday"} })
		r := Reduce(state, QuestionnaireAction{Kind: ActionSubmit}, twoCtx())
		eqv(t, r.Effects, []Effect{{Kind: EffectDone, Result: QuestionnaireResult{Answers: []QuestionAnswer{}, Cancelled: false, GlobalNote: "ship it Friday"}}})
	})
	tw(t, f, "cancel with a committed note still lifts it (attach-on-cancel — no cancelled guard)", func(t *testing.T) {
		state := makeQuestionnaireState(func(s *QuestionnaireState) {
			s.CurrentTab = 2
			s.NotesByTab = map[int]string{2: "note survives decline"}
		})
		r := Reduce(state, QuestionnaireAction{Kind: ActionCancel}, twoCtx())
		eqv(t, r.Effects, []Effect{{Kind: EffectDone, Result: QuestionnaireResult{Answers: []QuestionAnswer{}, Cancelled: true, GlobalNote: "note survives decline"}}})
	})
	tw(t, f, "note-free submit/cancel/confirm produce the key absent (never undefined-assigned)", func(t *testing.T) {
		submit := Reduce(makeQuestionnaireState(), QuestionnaireAction{Kind: ActionSubmit}, twoCtx())
		cancel := Reduce(makeQuestionnaireState(), QuestionnaireAction{Kind: ActionCancel}, twoCtx())
		confirm := Reduce(makeQuestionnaireState(), QuestionnaireAction{Kind: ActionConfirm, Answer: optAns(0, "Pick one", "A")}, twoCtx())
		for _, r := range []ApplyResult{submit, cancel, confirm} {
			done := r.Effects[0]
			eqv(t, done.Kind, EffectDone)
			eqv(t, done.Result.GlobalNote, "")
		}
	})
	tw(t, f, "single-question dialog: a per-question note rides answers[0].notes, never globalNote", func(t *testing.T) {
		state := makeQuestionnaireState(func(s *QuestionnaireState) { s.NotesByTab = map[int]string{0: "per-question note"} })
		r := Reduce(state, QuestionnaireAction{Kind: ActionConfirm, Answer: optAns(0, "Pick one", "A")}, makeApplyContext())
		done := r.Effects[0]
		eqv(t, done.Result.Answers[0].Notes, "per-question note")
		eqv(t, done.Result.GlobalNote, "")
	})

	// notes_enter / exit / forward
	tw(t, f, "notes_enter seeds state.notesDraft from existing answer.notes and emits set_notes_value", func(t *testing.T) {
		a := optAns(0, "q", "A")
		a.Notes = "old note"
		r := Reduce(makeQuestionnaireState(func(s *QuestionnaireState) { s.Answers = map[int]QuestionAnswer{0: a} }), QuestionnaireAction{Kind: ActionNotesEnter}, makeApplyContext())
		eqv(t, r.State.NotesVisible, true)
		eqv(t, r.State.NotesDraft, "old note")
		eqv(t, r.Effects, []Effect{{Kind: EffectSetNotesValue, Value: "old note"}, {Kind: EffectSetNotesFocused, Focused: true}})
	})
	tw(t, f, "notes_enter seeds notesDraft from notesByTab when the option is not yet confirmed (regression: reopening cleared the note)", func(t *testing.T) {
		state := makeQuestionnaireState(func(s *QuestionnaireState) { s.NotesByTab = map[int]string{0: "pending note"} })
		r := Reduce(state, QuestionnaireAction{Kind: ActionNotesEnter}, makeApplyContext())
		eqv(t, r.State.NotesVisible, true)
		eqv(t, r.State.NotesDraft, "pending note")
		eqv(t, r.Effects, []Effect{{Kind: EffectSetNotesValue, Value: "pending note"}, {Kind: EffectSetNotesFocused, Focused: true}})
	})
	tw(t, f, "notes_enter prefers notesByTab over a committed answer.notes", func(t *testing.T) {
		a := optAns(0, "q", "A")
		a.Notes = "committed"
		state := makeQuestionnaireState(func(s *QuestionnaireState) {
			s.Answers = map[int]QuestionAnswer{0: a}
			s.NotesByTab = map[int]string{0: "in-flight edit"}
		})
		r := Reduce(state, QuestionnaireAction{Kind: ActionNotesEnter}, makeApplyContext())
		eqv(t, r.State.NotesDraft, "in-flight edit")
	})
	tw(t, f, "notes_exit with empty notesDraft clears notesByTab + strips answer.notes", func(t *testing.T) {
		a := optAns(0, "q", "A")
		a.Notes = "old note"
		state := makeQuestionnaireState(func(s *QuestionnaireState) {
			s.Answers = map[int]QuestionAnswer{0: a}
			s.NotesByTab = map[int]string{0: "old note"}
			s.NotesVisible = true
			s.NotesDraft = ""
		})
		r := Reduce(state, QuestionnaireAction{Kind: ActionNotesExit}, makeApplyContext())
		eqv(t, r.State.NotesVisible, false)
		_, has := r.State.NotesByTab[0]
		eqv(t, has, false)
		eqv(t, r.State.Answers[0].Notes, "")
	})
	tw(t, f, "notes_exit trims state.notesDraft before persisting", func(t *testing.T) {
		state := makeQuestionnaireState(func(s *QuestionnaireState) {
			s.Answers = map[int]QuestionAnswer{0: optAns(0, "q", "A")}
			s.NotesVisible = true
			s.NotesDraft = "  fresh  "
		})
		r := Reduce(state, QuestionnaireAction{Kind: ActionNotesExit}, makeApplyContext())
		eqv(t, r.State.NotesByTab[0], "fresh")
		eqv(t, r.State.Answers[0].Notes, "fresh")
	})
	tw(t, f, "notes_exit with whitespace-only notesDraft clears notesByTab + strips answer.notes", func(t *testing.T) {
		a := optAns(0, "q", "A")
		a.Notes = "old note"
		state := makeQuestionnaireState(func(s *QuestionnaireState) {
			s.Answers = map[int]QuestionAnswer{0: a}
			s.NotesByTab = map[int]string{0: "old note"}
			s.NotesVisible = true
			s.NotesDraft = "   "
		})
		r := Reduce(state, QuestionnaireAction{Kind: ActionNotesExit}, makeApplyContext())
		_, has := r.State.NotesByTab[0]
		eqv(t, has, false)
		eqv(t, r.State.Answers[0].Notes, "")
	})
	tw(t, f, "notes_forward emits a single forward_notes_keystroke effect with no state change", func(t *testing.T) {
		s := makeQuestionnaireState(func(s *QuestionnaireState) { s.NotesVisible = true; s.NotesDraft = "hel" })
		r := Reduce(s, QuestionnaireAction{Kind: ActionNotesForward, Data: "l"}, makeApplyContext())
		eqv(t, r.State, s)
		eqv(t, r.Effects, []Effect{{Kind: EffectForwardNotesKeystroke, Data: "l"}})
	})

	// custom-input controls
	tw(t, f, "input_clear clears the headless input buffer and records an explicit empty draft", func(t *testing.T) {
		state := makeQuestionnaireState(func(s *QuestionnaireState) { s.InputMode = true; s.CustomDraftsByTab = map[int]string{0: "draft"} })
		r := Reduce(state, QuestionnaireAction{Kind: ActionInputClear}, makeApplyContext())
		eqv(t, r.State.CustomDraftsByTab[0], "")
		_, has := r.State.CustomDraftsByTab[0]
		eqv(t, has, true)
		eqv(t, r.Effects, []Effect{{Kind: EffectClearInputBuffer}})
	})
	tw(t, f, "input_edit opens the external editor with the current draft", func(t *testing.T) {
		r := Reduce(makeQuestionnaireState(func(s *QuestionnaireState) { s.InputMode = true }), QuestionnaireAction{Kind: ActionInputEdit, Value: "draft"}, makeApplyContext())
		eqv(t, r.Effects, []Effect{{Kind: EffectOpenInputEditor, Value: "draft"}})
	})
	tw(t, f, "input_replace stores and rehydrates the edited value", func(t *testing.T) {
		r := Reduce(makeQuestionnaireState(func(s *QuestionnaireState) { s.InputMode = true }), QuestionnaireAction{Kind: ActionInputReplace, Value: "edited"}, makeApplyContext())
		eqv(t, r.State.CustomDraftsByTab[0], "edited")
		eqv(t, r.Effects, []Effect{{Kind: EffectSetInputBuffer, Value: "edited"}})
	})

	tw(t, f, "submit_nav updates submitChoiceIndex with no effects", func(t *testing.T) {
		r := Reduce(makeQuestionnaireState(), QuestionnaireAction{Kind: ActionSubmitNav, NextIndex: 1}, makeApplyContext())
		eqv(t, r.State.SubmitChoiceIndex, 1)
		eqv(t, r.Effects, noEffects)
	})
	tw(t, f, "ignore is identity (state unchanged, no effects)", func(t *testing.T) {
		s := makeQuestionnaireState(func(s *QuestionnaireState) { s.OptionIndex = 2 })
		r := Reduce(s, QuestionnaireAction{Kind: ActionIgnore}, makeApplyContext())
		eqv(t, r.State, s)
		eqv(t, r.Effects, noEffects)
	})

	// confirmHandler mutual exclusivity
	mq := QuestionData{Question: "areas?", Header: "H", MultiSelect: true, Options: []OptionData{{Label: "FE", Description: "f"}, {Label: "BE", Description: "b"}}}
	tw(t, f, "custom confirm on a multi-select tab clears pre-existing checks", func(t *testing.T) {
		state := makeQuestionnaireState(func(s *QuestionnaireState) { s.CurrentTab = 0; s.MultiSelectChecked = map[int]bool{0: true, 1: true} })
		ctx := makeApplyContext(func(c *ApplyContext) { c.Questions = []QuestionData{mq} })
		result := Reduce(state, QuestionnaireAction{Kind: ActionConfirm, Answer: QuestionAnswer{QuestionIndex: 0, Question: "areas?", Kind: "custom", Answer: strp("custom-text")}}, ctx)
		eqv(t, len(setKeys(result.State.MultiSelectChecked)), 0)
		eqv(t, result.State.Answers[0].Kind, "custom")
	})
	tw(t, f, "option confirm on a single-select tab leaves multiSelectChecked untouched (no spurious clear)", func(t *testing.T) {
		singleQ := QuestionData{Question: "pick?", Header: "H", Options: []OptionData{{Label: "A", Description: "a"}}}
		state := makeQuestionnaireState(func(s *QuestionnaireState) { s.CurrentTab = 0; s.MultiSelectChecked = map[int]bool{0: true} })
		ctx := makeApplyContext(func(c *ApplyContext) { c.Questions = []QuestionData{singleQ} })
		result := Reduce(state, QuestionnaireAction{Kind: ActionConfirm, Answer: optAns(0, "pick?", "A")}, ctx)
		eqv(t, len(setKeys(result.State.MultiSelectChecked)), 1)
	})

	// toggle_collapsed
	tw(t, f, "flips false → true and emits set_overlay_hidden(true) so the runtime hides the overlay", func(t *testing.T) {
		r := Reduce(makeQuestionnaireState(), QuestionnaireAction{Kind: ActionToggleCollapsed}, makeApplyContext())
		eqv(t, r.State.Collapsed, true)
		eqv(t, r.Effects, []Effect{{Kind: EffectSetOverlayHidden, Hidden: true}})
	})
	tw(t, f, "flips true → false (expand round-trip) and emits set_overlay_hidden(false)", func(t *testing.T) {
		r := Reduce(makeQuestionnaireState(func(s *QuestionnaireState) { s.Collapsed = true }), QuestionnaireAction{Kind: ActionToggleCollapsed}, makeApplyContext())
		eqv(t, r.State.Collapsed, false)
		eqv(t, r.Effects, []Effect{{Kind: EffectSetOverlayHidden, Hidden: false}})
	})
	tw(t, f, "preserves orthogonal fields — collapse is a pure render-mode flip, never touches answers/optionIndex/notes", func(t *testing.T) {
		answers := map[int]QuestionAnswer{0: optAns(0, "Pick one", "A")}
		s := makeQuestionnaireState(func(s *QuestionnaireState) {
			s.OptionIndex = 1
			s.NotesVisible = true
			s.NotesDraft = "in-flight"
			s.Answers = answers
		})
		r := Reduce(s, QuestionnaireAction{Kind: ActionToggleCollapsed}, makeApplyContext())
		eqv(t, r.State.Collapsed, true)
		eqv(t, r.State.OptionIndex, 1)
		eqv(t, r.State.NotesVisible, true)
		eqv(t, r.State.NotesDraft, "in-flight")
		eqv(t, r.State.Answers, answers)
		eqv(t, r.Effects, []Effect{{Kind: EffectSetOverlayHidden, Hidden: true}})
	})

	// multi-select notes merge
	mctx := func() ApplyContext {
		return makeApplyContext(func(c *ApplyContext) { c.Questions = []QuestionData{makeQuestion(multiQ)} })
	}
	tw(t, f, "toggle attaches a pending note onto the multi answer (persistMultiSelectAnswer merge)", func(t *testing.T) {
		state := makeQuestionnaireState(func(s *QuestionnaireState) { s.NotesByTab = map[int]string{0: "side note"} })
		r := Reduce(state, QuestionnaireAction{Kind: ActionToggle, Index: 0}, mctx())
		a := r.State.Answers[0]
		eqv(t, a.Kind, "multi")
		eqv(t, a.Selected, []string{"A"})
		eqv(t, a.Notes, "side note")
	})
	tw(t, f, "toggle with empty notesByTab persists a multi answer with NO notes field (negative case)", func(t *testing.T) {
		r := Reduce(makeQuestionnaireState(), QuestionnaireAction{Kind: ActionToggle, Index: 0}, mctx())
		a := r.State.Answers[0]
		eqv(t, a.Kind, "multi")
		eqv(t, a.Selected, []string{"A"})
		eqv(t, a.Notes, "")
	})
	tw(t, f, "multi_confirm attaches a pending note onto the multi answer (multiConfirmHandler merge)", func(t *testing.T) {
		state := makeQuestionnaireState(func(s *QuestionnaireState) { s.NotesByTab = map[int]string{0: "confirm note"} })
		r := Reduce(state, QuestionnaireAction{Kind: ActionMultiConfirm, Selected: []string{"A", "B"}}, mctx())
		a := r.State.Answers[0]
		eqv(t, a.Kind, "multi")
		eqv(t, a.Selected, []string{"A", "B"})
		eqv(t, a.Notes, "confirm note")
	})
	tw(t, f, "strip round-trip: notes_exit with an empty draft strips notes from a multi answer; a subsequent toggle does not resurrect it", func(t *testing.T) {
		answers := map[int]QuestionAnswer{0: {QuestionIndex: 0, Question: "Pick one", Kind: "multi", Answer: nil, Selected: []string{"A"}, HasSelected: true, Notes: "stale"}}
		state := makeQuestionnaireState(func(s *QuestionnaireState) {
			s.Answers = answers
			s.NotesByTab = map[int]string{0: "stale"}
			s.NotesVisible = true
			s.NotesDraft = "   "
			s.MultiSelectChecked = map[int]bool{0: true}
		})
		state = Reduce(state, QuestionnaireAction{Kind: ActionNotesExit}, mctx()).State
		eqv(t, state.Answers[0].Notes, "")
		_, has := state.NotesByTab[0]
		eqv(t, has, false)
		after := Reduce(state, QuestionnaireAction{Kind: ActionToggle, Index: 1}, mctx()).State
		eqv(t, after.Answers[0].Selected, []string{"A", "B"})
		eqv(t, after.Answers[0].Notes, "")
	})

	// global note at the Submit-tab pseudo-index
	submitState := func(mods ...func(*QuestionnaireState)) QuestionnaireState {
		return makeQuestionnaireState(append([]func(*QuestionnaireState){func(s *QuestionnaireState) { s.CurrentTab = 2 }}, mods...)...)
	}
	tw(t, f, "notes_enter seeds notesDraft from notesByTab[questions.length]", func(t *testing.T) {
		r := Reduce(submitState(func(s *QuestionnaireState) { s.NotesByTab = map[int]string{2: "ship it Friday"} }), QuestionnaireAction{Kind: ActionNotesEnter}, twoCtx())
		eqv(t, r.State.NotesVisible, true)
		eqv(t, r.State.NotesDraft, "ship it Friday")
		eqv(t, r.Effects, []Effect{{Kind: EffectSetNotesValue, Value: "ship it Friday"}, {Kind: EffectSetNotesFocused, Focused: true}})
	})
	tw(t, f, "notes_exit writes the trimmed draft into notesByTab[questions.length], leaving answers untouched", func(t *testing.T) {
		answers := map[int]QuestionAnswer{0: optAns(0, "Pick one", "A")}
		r := Reduce(submitState(func(s *QuestionnaireState) { s.Answers = answers; s.NotesVisible = true; s.NotesDraft = "  global  " }), QuestionnaireAction{Kind: ActionNotesExit}, twoCtx())
		eqv(t, r.State.NotesVisible, false)
		eqv(t, r.State.NotesByTab[2], "global")
		_, has := r.State.Answers[2]
		eqv(t, has, false)
		eqv(t, r.State.Answers, answers)
	})
	tw(t, f, "notes_exit with an empty draft deletes the pseudo-index key", func(t *testing.T) {
		r := Reduce(submitState(func(s *QuestionnaireState) {
			s.NotesByTab = map[int]string{2: "stale"}
			s.NotesVisible = true
			s.NotesDraft = "   "
		}), QuestionnaireAction{Kind: ActionNotesExit}, twoCtx())
		_, has := r.State.NotesByTab[2]
		eqv(t, has, false)
	})
	tw(t, f, "tab away and back reseeds the shared editor via switchTabResult", func(t *testing.T) {
		s := submitState(func(s *QuestionnaireState) { s.NotesByTab = map[int]string{2: "keep me"} })
		s = Reduce(s, QuestionnaireAction{Kind: ActionTabSwitch, NextTab: 0}, twoCtx()).State
		eqv(t, s.CurrentTab, 0)
		eqv(t, s.NotesVisible, false)
		eqv(t, s.NotesDraft, "")
		s = Reduce(s, QuestionnaireAction{Kind: ActionTabSwitch, NextTab: 2}, twoCtx()).State
		eqv(t, s.CurrentTab, 2)
		eqv(t, s.NotesDraft, "keep me")
	})
}
