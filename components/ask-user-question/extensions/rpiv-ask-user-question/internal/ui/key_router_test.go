package ui

import (
	"reflect"
	"testing"
)

const (
	krUP         = "tui.select.up"
	krDOWN       = "tui.select.down"
	krCONFIRM    = "tui.select.confirm"
	krSUBMIT     = "tui.input.submit"
	krCANCEL     = "tui.select.cancel"
	krNEWLINE    = "tui.input.newLine"
	krEDITORUP   = "tui.editor.cursorUp"
	krEDITORDOWN = "tui.editor.cursorDown"
	krCLEAR      = "tui.editor.deleteToLineStart"
	krEXTERNAL   = "app.editor.external"

	byteTab      = "\t"
	byteShiftTab = "\x1b[Z"
	byteRight    = "\x1b[C"
	byteLeft     = "\x1b[D"
	byteSpace    = " "
)

func sentinel(name string) string { return "<KEY:" + name + ">" }

type sentinelKB struct{}

func (sentinelKB) Matches(data, name string) bool { return data == sentinel(name) }

type krQuestionMod = func(*QuestionData)

func krQuestion(mods ...krQuestionMod) QuestionData {
	q := QuestionData{
		Question: "Pick one", Header: "H",
		Options: []OptionData{{Label: "A", Description: "a"}, {Label: "B", Description: "b"}, {Label: "C", Description: "c"}},
	}
	for _, m := range mods {
		m(&q)
	}
	return q
}

func krMultiQ(labels ...string) QuestionData {
	opts := []OptionData{}
	for _, l := range labels {
		opts = append(opts, OptionData{Label: l, Description: l})
	}
	return krQuestion(func(q *QuestionData) { q.MultiSelect = true; q.Options = opts })
}

func krAnswer(idx int) QuestionAnswer {
	return QuestionAnswer{QuestionIndex: idx, Question: "q", Kind: "option", Answer: strp("A")}
}

func krState(mods ...func(*QuestionnaireState)) QuestionnaireState {
	return makeQuestionnaireState(mods...)
}

type rtOpts struct {
	questions  []QuestionData
	items      []WrappingSelectItem
	current    *WrappingSelectItem
	currentSet bool
	isMulti    *bool
	kb         func(data, name string) bool
	inputBuf   string
	up, down   bool
	collapse   *string
}

func krRuntime(mods ...func(*rtOpts)) QuestionnaireRuntime {
	o := rtOpts{}
	for _, m := range mods {
		m(&o)
	}
	questions := o.questions
	if questions == nil {
		questions = []QuestionData{krQuestion(), krQuestion()}
	}
	items := o.items
	if items == nil {
		for _, op := range questions[0].Options {
			items = append(items, WrappingSelectItem{Kind: "option", Label: op.Label})
		}
	}
	current := o.current
	if !o.currentSet && len(items) > 0 {
		current = &items[0]
	}
	isMulti := len(questions) > 1
	if o.isMulti != nil {
		isMulti = *o.isMulti
	}
	var kb sentinelKBFn = sentinelKBFn(func(data, name string) bool { return data == sentinel(name) })
	if o.kb != nil {
		kb = o.kb
	}
	collapse := "ctrl+]"
	if o.collapse != nil {
		collapse = *o.collapse
	}
	return QuestionnaireRuntime{
		Keybindings: kb, InputBuffer: o.inputBuf, CanMoveInputUp: o.up, CanMoveInputDown: o.down,
		Questions: questions, IsMulti: isMulti, CurrentItem: current, Items: items, CollapseKey: collapse,
	}
}

type sentinelKBFn func(data, name string) bool

func (f sentinelKBFn) Matches(data, name string) bool { return f(data, name) }

func withQuestions(q ...QuestionData) func(*rtOpts) { return func(o *rtOpts) { o.questions = q } }
func withItems(items ...WrappingSelectItem) func(*rtOpts) {
	return func(o *rtOpts) { o.items = items }
}
func withCurrent(it WrappingSelectItem) func(*rtOpts) {
	return func(o *rtOpts) { o.current = &it; o.currentSet = true }
}
func notMulti(o *rtOpts)                                  { f := false; o.isMulti = &f }
func withMulti(v bool) func(*rtOpts)                      { return func(o *rtOpts) { o.isMulti = &v } }
func withInputBuffer(s string) func(*rtOpts)              { return func(o *rtOpts) { o.inputBuf = s } }
func withCollapse(s string) func(*rtOpts)                 { return func(o *rtOpts) { o.collapse = &s } }
func withKB(f func(data, name string) bool) func(*rtOpts) { return func(o *rtOpts) { o.kb = f } }
func withCanMove(up, down bool) func(*rtOpts)             { return func(o *rtOpts) { o.up = up; o.down = down } }

func opt(label string) WrappingSelectItem { return WrappingSelectItem{Kind: "option", Label: label} }
func nextItem() WrappingSelectItem        { return WrappingSelectItem{Kind: "next", Label: "Next"} }
func otherItem() WrappingSelectItem {
	return WrappingSelectItem{Kind: "other", Label: "Type something."}
}

func inTab(n int) func(*QuestionnaireState) { return func(s *QuestionnaireState) { s.CurrentTab = n } }
func atOption(n int) func(*QuestionnaireState) {
	return func(s *QuestionnaireState) { s.OptionIndex = n }
}
func inputModeOn(s *QuestionnaireState) { s.InputMode = true }
func notesOn(s *QuestionnaireState)     { s.NotesVisible = true }
func collapsedOn(s *QuestionnaireState) { s.Collapsed = true }
func choice(n int) func(*QuestionnaireState) {
	return func(s *QuestionnaireState) { s.SubmitChoiceIndex = n }
}
func checked(ix ...int) func(*QuestionnaireState) {
	return func(s *QuestionnaireState) {
		s.MultiSelectChecked = map[int]bool{}
		for _, i := range ix {
			s.MultiSelectChecked[i] = true
		}
	}
}
func answered(idx ...int) func(*QuestionnaireState) {
	return func(s *QuestionnaireState) {
		s.Answers = map[int]QuestionAnswer{}
		for _, i := range idx {
			s.Answers[i] = krAnswer(i)
		}
	}
}

func eqAction(t *testing.T, got, want QuestionnaireAction) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func act(kind string) QuestionnaireAction { return QuestionnaireAction{Kind: kind} }
func intp0(n int) *int                    { return &n }

func TestKeyRouter(t *testing.T) {
	const f = "state/key-router"
	ignore := act(ActionIgnore)

	tw(t, f, "wraps negative + over-max into [0, total)", func(t *testing.T) {
		eqv(t, WrapTab(-1, 3), 2)
		eqv(t, WrapTab(3, 3), 0)
		eqv(t, WrapTab(0, 0), 0)
	})
	tw(t, f, "allAnswered is false when any question lacks an answer", func(t *testing.T) {
		eqv(t, AllAnswered(krState(answered(0)), krRuntime()), false)
	})
	tw(t, f, "allAnswered is true when every question has an answer", func(t *testing.T) {
		eqv(t, AllAnswered(krState(answered(0, 1)), krRuntime()), true)
	})

	// nav
	tw(t, f, "UP from a non-zero index decrements by 1", func(t *testing.T) {
		eqAction(t, RouteKey(sentinel(krUP), krState(atOption(2)), krRuntime()), QuestionnaireAction{Kind: ActionNav, NextIndex: 1})
	})
	tw(t, f, "DOWN advances by 1", func(t *testing.T) {
		eqAction(t, RouteKey(sentinel(krDOWN), krState(), krRuntime()), QuestionnaireAction{Kind: ActionNav, NextIndex: 1})
	})
	tw(t, f, "DOWN at the last item wraps to 0 (no chat row, wrapTab clamp)", func(t *testing.T) {
		eqAction(t, RouteKey(sentinel(krDOWN), krState(atOption(2)), krRuntime()), QuestionnaireAction{Kind: ActionNav, NextIndex: 0})
	})
	tw(t, f, "UP at the first item wraps to the last (no chat row, wrapTab clamp)", func(t *testing.T) {
		rt := krRuntime()
		eqAction(t, RouteKey(sentinel(krUP), krState(atOption(0)), rt), QuestionnaireAction{Kind: ActionNav, NextIndex: len(rt.Items) - 1})
	})

	// tab_switch
	tw(t, f, "Tab cycles forward through total tabs (questions + Submit)", func(t *testing.T) {
		eqAction(t, RouteKey(byteTab, krState(), krRuntime()), QuestionnaireAction{Kind: ActionTabSwitch, NextTab: 1})
	})
	tw(t, f, "Right is an alias for Tab", func(t *testing.T) {
		eqAction(t, RouteKey(byteRight, krState(), krRuntime()), QuestionnaireAction{Kind: ActionTabSwitch, NextTab: 1})
	})
	tw(t, f, "Shift+Tab wraps backward from tab 0 to the Submit tab", func(t *testing.T) {
		eqAction(t, RouteKey(byteShiftTab, krState(inTab(0)), krRuntime()), QuestionnaireAction{Kind: ActionTabSwitch, NextTab: 2})
	})
	tw(t, f, "Left is an alias for Shift+Tab", func(t *testing.T) {
		eqAction(t, RouteKey(byteLeft, krState(inTab(1)), krRuntime()), QuestionnaireAction{Kind: ActionTabSwitch, NextTab: 0})
	})
	tw(t, f, "Tab is a no-op (returns ignore) in single-question mode", func(t *testing.T) {
		eqAction(t, RouteKey(byteTab, krState(), krRuntime(notMulti, withQuestions(krQuestion()))), ignore)
	})

	// confirm
	tw(t, f, "emits confirm with autoAdvanceTab pointing to the next tab", func(t *testing.T) {
		a := RouteKey(sentinel(krCONFIRM), krState(inTab(0)), krRuntime())
		eqv(t, a.Kind, ActionConfirm)
		eqv(t, a.Answer.QuestionIndex, 0)
		eqv(t, *a.Answer.Answer, "A")
		eqv(t, a.Answer.Kind, "option")
		eqv(t, *a.AutoAdvanceTab, 1)
	})
	tw(t, f, "last question -> autoAdvanceTab points at the Submit tab (questions.length)", func(t *testing.T) {
		a := RouteKey(sentinel(krCONFIRM), krState(inTab(1)), krRuntime())
		eqv(t, a.Kind, ActionConfirm)
		eqv(t, *a.AutoAdvanceTab, 2)
	})
	tw(t, f, "single-question (!isMulti) -> autoAdvanceTab is undefined (dialog submits)", func(t *testing.T) {
		a := RouteKey(sentinel(krCONFIRM), krState(), krRuntime(notMulti, withQuestions(krQuestion()), withItems(opt("A"), opt("B"), opt("C"))))
		eqv(t, a.Kind, ActionConfirm)
		if a.AutoAdvanceTab != nil {
			t.Fatal("autoAdvanceTab set")
		}
	})
	tw(t, f, "a key matching only tui.input.submit confirms (second confirm source)", func(t *testing.T) {
		a := RouteKey(sentinel(krSUBMIT), krState(inTab(0)), krRuntime())
		eqv(t, a.Kind, ActionConfirm)
		eqv(t, *a.Answer.Answer, "A")
		eqv(t, a.Answer.Kind, "option")
		eqv(t, *a.AutoAdvanceTab, 1)
	})
	tw(t, f, "inline-input mode: Enter confirms with the buffered text + kind:'custom'", func(t *testing.T) {
		a := RouteKey(sentinel(krCONFIRM), krState(inputModeOn), krRuntime(withCurrent(otherItem()), withInputBuffer("my custom answer")))
		eqv(t, a.Kind, ActionConfirm)
		eqv(t, *a.Answer.Answer, "my custom answer")
		eqv(t, a.Answer.Kind, "custom")
	})

	// multiSelect
	multiQ := krMultiQ("FE", "BE", "Tests")
	items4 := []WrappingSelectItem{opt("FE"), opt("BE"), opt("Tests"), nextItem()}
	tw(t, f, "Space emits toggle for the current optionIndex", func(t *testing.T) {
		eqAction(t, RouteKey(byteSpace, krState(atOption(1)), krRuntime(withQuestions(multiQ), notMulti, withItems(opt("FE"), opt("BE"), opt("Tests")), withCurrent(opt("BE")))),
			QuestionnaireAction{Kind: ActionToggle, Index: 1})
	})
	tw(t, f, "Enter on a regular row emits toggle for the current optionIndex", func(t *testing.T) {
		eqAction(t, RouteKey(sentinel(krCONFIRM), krState(atOption(1)), krRuntime(withQuestions(multiQ), notMulti, withItems(items4...), withCurrent(opt("BE")))),
			QuestionnaireAction{Kind: ActionToggle, Index: 1})
	})
	tw(t, f, "Space on Next sentinel is ignored", func(t *testing.T) {
		eqAction(t, RouteKey(byteSpace, krState(atOption(3)), krRuntime(withQuestions(multiQ), notMulti, withItems(items4...), withCurrent(nextItem()))), ignore)
	})
	tw(t, f, "Enter on Next emits multi_confirm with selected labels in option order", func(t *testing.T) {
		eqAction(t, RouteKey(sentinel(krCONFIRM), krState(atOption(3), checked(2, 0)), krRuntime(withQuestions(multiQ), notMulti, withItems(items4...), withCurrent(nextItem()))),
			QuestionnaireAction{Kind: ActionMultiConfirm, Selected: []string{"FE", "Tests"}})
	})
	tw(t, f, "single-question multi-select: Enter on Next carries autoAdvanceTab=undefined (host → submit)", func(t *testing.T) {
		a := RouteKey(sentinel(krCONFIRM), krState(atOption(3), checked(0)), krRuntime(withQuestions(multiQ), notMulti, withItems(items4...), withCurrent(nextItem())))
		eqv(t, a.Kind, ActionMultiConfirm)
		if a.AutoAdvanceTab != nil {
			t.Fatal("set")
		}
	})
	tw(t, f, "multi-question multi-select on tab 0: Enter on Next carries autoAdvanceTab=1", func(t *testing.T) {
		a := RouteKey(sentinel(krCONFIRM), krState(inTab(0), atOption(3), checked(0)), krRuntime(withQuestions(multiQ, krQuestion()), withMulti(true), withItems(items4...), withCurrent(nextItem())))
		eqv(t, a.Kind, ActionMultiConfirm)
		eqv(t, *a.AutoAdvanceTab, 1)
	})
	tw(t, f, "multi-question multi-select on last tab: Enter on Next carries autoAdvanceTab=questions.length (Submit)", func(t *testing.T) {
		a := RouteKey(sentinel(krCONFIRM), krState(inTab(1), atOption(3), checked(0)), krRuntime(withQuestions(krQuestion(), multiQ), withMulti(true), withItems(items4...), withCurrent(nextItem())))
		eqv(t, a.Kind, ActionMultiConfirm)
		eqv(t, *a.AutoAdvanceTab, 2)
	})
	tw(t, f, "Space does NOT emit toggle on a single-select question", func(t *testing.T) {
		eqAction(t, RouteKey(byteSpace, krState(), krRuntime()), ignore)
	})

	// multiSelect free text
	items5 := []WrappingSelectItem{opt("FE"), opt("BE"), opt("Tests"), otherItem(), nextItem()}
	mrt := func(cur int, extra ...func(*rtOpts)) QuestionnaireRuntime {
		return krRuntime(append([]func(*rtOpts){withQuestions(multiQ), notMulti, withItems(items5...), withCurrent(items5[cur])}, extra...)...)
	}
	tw(t, f, "Enter on the 'Type something.' row (inputMode) → confirm kind:'custom' with the buffer", func(t *testing.T) {
		a := RouteKey(sentinel(krCONFIRM), krState(atOption(3), inputModeOn), mrt(3, withInputBuffer("typed answer")))
		eqv(t, a.Kind, ActionConfirm)
		eqv(t, a.Answer.Kind, "custom")
		eqv(t, *a.Answer.Answer, "typed answer")
	})
	tw(t, f, "Space on the 'Type something.' row (defensive, !inputMode) → ignore (no phantom toggle)", func(t *testing.T) {
		eqAction(t, RouteKey(byteSpace, krState(atOption(3)), mrt(3)), ignore)
	})
	tw(t, f, "Enter on the 'Type something.' row (defensive, !inputMode) → ignore (no toggle/multi_confirm)", func(t *testing.T) {
		eqAction(t, RouteKey(sentinel(krCONFIRM), krState(atOption(3)), mrt(3)), ignore)
	})
	tw(t, f, "DOWN from last option (index 2) → nav to the other row (index 3)", func(t *testing.T) {
		eqAction(t, RouteKey(sentinel(krDOWN), krState(atOption(2)), mrt(2)), QuestionnaireAction{Kind: ActionNav, NextIndex: 3})
	})
	tw(t, f, "DOWN from the other row (index 3) → nav to Next (index 4)", func(t *testing.T) {
		eqAction(t, RouteKey(sentinel(krDOWN), krState(atOption(3)), mrt(3)), QuestionnaireAction{Kind: ActionNav, NextIndex: 4})
	})
	tw(t, f, "UP from Next (index 4) → nav back to the other row (index 3)", func(t *testing.T) {
		eqAction(t, RouteKey(sentinel(krUP), krState(atOption(4)), mrt(4)), QuestionnaireAction{Kind: ActionNav, NextIndex: 3})
	})

	// cancel + submit
	tw(t, f, "Esc cancels the entire questionnaire from any tab", func(t *testing.T) {
		eqAction(t, RouteKey(sentinel(krCANCEL), krState(), krRuntime()), act(ActionCancel))
		eqAction(t, RouteKey(sentinel(krCANCEL), krState(inTab(2)), krRuntime()), act(ActionCancel))
	})
	tw(t, f, "Submit tab + Enter on Submit row + allAnswered -> submit", func(t *testing.T) {
		eqAction(t, RouteKey(sentinel(krCONFIRM), krState(inTab(2), choice(0), answered(0, 1)), krRuntime()), act(ActionSubmit))
	})
	tw(t, f, "Submit tab + Enter on Submit row when not allAnswered -> submit (partial)", func(t *testing.T) {
		eqAction(t, RouteKey(sentinel(krCONFIRM), krState(inTab(2), choice(0), answered(0)), krRuntime()), act(ActionSubmit))
	})
	tw(t, f, "Submit tab + DOWN -> submit_nav nextIndex=1", func(t *testing.T) {
		eqAction(t, RouteKey(sentinel(krDOWN), krState(inTab(2), choice(0)), krRuntime()), QuestionnaireAction{Kind: ActionSubmitNav, NextIndex: 1})
	})
	tw(t, f, "Submit tab + UP wraps from 0 to 1", func(t *testing.T) {
		eqAction(t, RouteKey(sentinel(krUP), krState(inTab(2), choice(0)), krRuntime()), QuestionnaireAction{Kind: ActionSubmitNav, NextIndex: 1})
	})
	tw(t, f, "Submit tab + DOWN from index 1 wraps to 0", func(t *testing.T) {
		eqAction(t, RouteKey(sentinel(krDOWN), krState(inTab(2), choice(1)), krRuntime()), QuestionnaireAction{Kind: ActionSubmitNav, NextIndex: 0})
	})
	tw(t, f, "Submit tab + Enter on Cancel row (index 1) when complete -> cancel", func(t *testing.T) {
		eqAction(t, RouteKey(sentinel(krCONFIRM), krState(inTab(2), choice(1), answered(0, 1)), krRuntime()), act(ActionCancel))
	})
	tw(t, f, "Submit tab + Enter on Cancel row (index 1) when incomplete -> cancel", func(t *testing.T) {
		eqAction(t, RouteKey(sentinel(krCONFIRM), krState(inTab(2), choice(1)), krRuntime()), act(ActionCancel))
	})

	// notes
	tw(t, f, "'n' emits notes_enter regardless of preview (universal gate)", func(t *testing.T) {
		eqAction(t, RouteKey("n", krState(), krRuntime()), act(ActionNotesEnter))
	})
	tw(t, f, "'n' emits notes_enter on multiSelect questions even with preview (universal gate)", func(t *testing.T) {
		eqAction(t, RouteKey("n", krState(), krRuntime(withQuestions(krQuestion(func(q *QuestionData) { q.MultiSelect = true }), krQuestion()))), act(ActionNotesEnter))
	})
	tw(t, f, "'n' emits notes_enter when the multi-select Next sentinel row is focused", func(t *testing.T) {
		its := []WrappingSelectItem{opt("FE"), opt("BE"), opt("Tests"), nextItem()}
		eqAction(t, RouteKey("n", krState(atOption(3)), krRuntime(withQuestions(multiQ), notMulti, withItems(its...), withCurrent(its[3]))), act(ActionNotesEnter))
	})
	tw(t, f, "notesMode: Esc -> notes_exit", func(t *testing.T) {
		eqAction(t, RouteKey(sentinel(krCANCEL), krState(notesOn), krRuntime()), act(ActionNotesExit))
	})
	tw(t, f, "notesMode: Enter -> notes_exit (save + return to options)", func(t *testing.T) {
		eqAction(t, RouteKey(sentinel(krCONFIRM), krState(notesOn), krRuntime()), act(ActionNotesExit))
	})
	tw(t, f, "notesMode: Tab byte emits notes_forward (any non-Esc/Enter key forwards to the Input)", func(t *testing.T) {
		eqAction(t, RouteKey(byteTab, krState(notesOn), krRuntime()), QuestionnaireAction{Kind: ActionNotesForward, Data: byteTab})
	})
	tw(t, f, "notesMode: arbitrary printable byte emits notes_forward (single dispatch path)", func(t *testing.T) {
		eqAction(t, RouteKey("a", krState(notesOn), krRuntime()), QuestionnaireAction{Kind: ActionNotesForward, Data: "a"})
	})
	tw(t, f, "notesMode: configured newline is forwarded instead of closing the editor", func(t *testing.T) {
		data := sentinel(krNEWLINE)
		eqAction(t, RouteKey(data, krState(notesOn), krRuntime()), QuestionnaireAction{Kind: ActionNotesForward, Data: data})
	})

	// global note on the Submit tab
	submitTab := func(mods ...func(*QuestionnaireState)) QuestionnaireState {
		return krState(append([]func(*QuestionnaireState){inTab(2)}, mods...)...)
	}
	tw(t, f, "'n' with the editor closed emits notes_enter at the pseudo-index", func(t *testing.T) {
		eqAction(t, RouteKey("n", submitTab(), krRuntime()), act(ActionNotesEnter))
	})
	tw(t, f, "while open, the Tab byte forwards to the notes editor — NOT tab_switch", func(t *testing.T) {
		eqAction(t, RouteKey(byteTab, submitTab(notesOn), krRuntime()), QuestionnaireAction{Kind: ActionNotesForward, Data: byteTab})
	})
	tw(t, f, "while open, a printable byte forwards into the editor (typing captured)", func(t *testing.T) {
		eqAction(t, RouteKey("z", submitTab(notesOn), krRuntime()), QuestionnaireAction{Kind: ActionNotesForward, Data: "z"})
	})
	tw(t, f, "while open, a second 'n' forwards as a literal character instead of re-entering notes", func(t *testing.T) {
		eqAction(t, RouteKey("n", submitTab(notesOn), krRuntime()), QuestionnaireAction{Kind: ActionNotesForward, Data: "n"})
	})
	tw(t, f, "while open, Esc commits the note (notes_exit)", func(t *testing.T) {
		eqAction(t, RouteKey(sentinel(krCANCEL), submitTab(notesOn), krRuntime()), act(ActionNotesExit))
	})
	tw(t, f, "while open, Enter commits the note (notes_exit) — it does NOT submit", func(t *testing.T) {
		eqAction(t, RouteKey(sentinel(krCONFIRM), submitTab(notesOn), krRuntime()), act(ActionNotesExit))
	})
	tw(t, f, "with the editor closed, Enter still submits/cancels per submitChoiceIndex (the n branch shadows nothing)", func(t *testing.T) {
		eqAction(t, RouteKey(sentinel(krCONFIRM), submitTab(choice(0)), krRuntime()), act(ActionSubmit))
		eqAction(t, RouteKey(sentinel(krCONFIRM), submitTab(choice(1)), krRuntime()), act(ActionCancel))
	})

	// inputMode
	tw(t, f, "Tab byte is ignored under inputMode", func(t *testing.T) {
		eqAction(t, RouteKey(byteTab, krState(inputModeOn), krRuntime(withCurrent(otherItem()))), ignore)
	})
	tw(t, f, "printable bytes return ignore (dialog forwards to inlineInput.handleInput)", func(t *testing.T) {
		eqAction(t, RouteKey("x", krState(inputModeOn), krRuntime(withCurrent(otherItem()))), ignore)
	})
	tw(t, f, "configured newline is forwarded to the multiline editor", func(t *testing.T) {
		eqAction(t, RouteKey(sentinel(krNEWLINE), krState(inputModeOn), krRuntime(withCurrent(otherItem()))), ignore)
	})
	tw(t, f, "forwards vertical editor movement while the cursor is between logical lines", func(t *testing.T) {
		editing := krState(inputModeOn, atOption(1))
		rt := krRuntime(withCurrent(otherItem()), withCanMove(true, true))
		eqAction(t, RouteKey(sentinel(krEDITORUP), editing, rt), ignore)
		eqAction(t, RouteKey(sentinel(krEDITORDOWN), editing, rt), ignore)
	})
	tw(t, f, "'n' under inputMode returns ignore (types a literal 'n'), NOT notes_enter", func(t *testing.T) {
		eqAction(t, RouteKey("n", krState(inputModeOn), krRuntime(withCurrent(otherItem()))), ignore)
	})
	tw(t, f, "Pi's Ctrl+U line-kill binding clears the whole custom answer", func(t *testing.T) {
		eqAction(t, RouteKey(sentinel(krCLEAR), krState(inputModeOn), krRuntime(withCurrent(otherItem()))), act(ActionInputClear))
	})
	tw(t, f, "the app.editor.external binding opens the editor with the current draft", func(t *testing.T) {
		eqAction(t, RouteKey(sentinel(krEXTERNAL), krState(inputModeOn), krRuntime(withCurrent(otherItem()), withInputBuffer("draft"))),
			QuestionnaireAction{Kind: ActionInputEdit, Value: "draft"})
	})
	tw(t, f, "Esc cancels the questionnaire even in inputMode", func(t *testing.T) {
		eqAction(t, RouteKey(sentinel(krCANCEL), krState(inputModeOn), krRuntime(withCurrent(otherItem()))), act(ActionCancel))
	})

	// remapped submit key (#156)
	const remappedEnter, remappedCtrlEnter = "<REMAPPED:ENTER>", "<REMAPPED:CTRL_ENTER>"
	remapped := withKB(func(data, name string) bool {
		switch name {
		case krSUBMIT:
			return data == remappedCtrlEnter
		case krNEWLINE:
			return data == remappedEnter
		case krCONFIRM:
			return data == remappedEnter
		}
		return false
	})
	tw(t, f, "inputMode: Enter is consumed as a newline (collision resolves to newline)", func(t *testing.T) {
		eqAction(t, RouteKey(remappedEnter, krState(inputModeOn), krRuntime(remapped, withCurrent(otherItem()))), ignore)
	})
	tw(t, f, "inputMode: the submit key confirms with the buffered text instead of falling through to the editor", func(t *testing.T) {
		a := RouteKey(remappedCtrlEnter, krState(inputModeOn), krRuntime(remapped, withCurrent(otherItem()), withInputBuffer("typed answer")))
		eqv(t, a.Kind, ActionConfirm)
		eqv(t, a.Answer.Kind, "custom")
		eqv(t, *a.Answer.Answer, "typed answer")
	})
	tw(t, f, "notes: Enter forwards as a newline; the submit key exits the notes editor", func(t *testing.T) {
		eqAction(t, RouteKey(remappedEnter, krState(notesOn), krRuntime(remapped)), QuestionnaireAction{Kind: ActionNotesForward, Data: remappedEnter})
		eqAction(t, RouteKey(remappedCtrlEnter, krState(notesOn), krRuntime(remapped)), act(ActionNotesExit))
	})
	tw(t, f, "single-select option row: the submit key confirms", func(t *testing.T) {
		a := RouteKey(remappedCtrlEnter, krState(inTab(0)), krRuntime(remapped))
		eqv(t, a.Kind, ActionConfirm)
		eqv(t, *a.Answer.Answer, "A")
		eqv(t, a.Answer.Kind, "option")
	})
	tw(t, f, "Submit tab: the submit key submits the questionnaire", func(t *testing.T) {
		eqAction(t, RouteKey(remappedCtrlEnter, krState(inTab(2), choice(0)), krRuntime(remapped)), act(ActionSubmit))
	})
	tw(t, f, "multi-select: the submit key toggles a regular row and commits on the Next sentinel (matches Enter)", func(t *testing.T) {
		mq := krMultiQ("FE", "BE")
		its := []WrappingSelectItem{opt("FE"), opt("BE"), nextItem()}
		eqAction(t, RouteKey(remappedCtrlEnter, krState(atOption(1)), krRuntime(remapped, withQuestions(mq), notMulti, withItems(its...), withCurrent(its[1]))),
			QuestionnaireAction{Kind: ActionToggle, Index: 1})
		eqAction(t, RouteKey(remappedCtrlEnter, krState(atOption(2), checked(0)), krRuntime(remapped, withQuestions(mq), notMulti, withItems(its...), withCurrent(its[2]))),
			QuestionnaireAction{Kind: ActionMultiConfirm, Selected: []string{"FE"}})
	})

	// collapse/expand
	const byteCtrlRBracket = "\x1d"
	toggleCollapsed := act(ActionToggleCollapsed)
	tw(t, f, "Ctrl+] emits toggle_collapsed from the default question state", func(t *testing.T) {
		eqAction(t, RouteKey(byteCtrlRBracket, krState(), krRuntime()), toggleCollapsed)
	})
	tw(t, f, "Ctrl+] emits toggle_collapsed even while notesVisible (the notes branch never sees the key)", func(t *testing.T) {
		eqAction(t, RouteKey(byteCtrlRBracket, krState(notesOn), krRuntime()), toggleCollapsed)
	})
	tw(t, f, "Ctrl+] emits toggle_collapsed even when already collapsed (expand round-trip)", func(t *testing.T) {
		eqAction(t, RouteKey(byteCtrlRBracket, krState(collapsedOn), krRuntime()), toggleCollapsed)
	})
	tw(t, f, "while collapsed, Esc maps to cancel (the documented escape hatch in the one-line footer)", func(t *testing.T) {
		eqAction(t, RouteKey(sentinel(krCANCEL), krState(collapsedOn), krRuntime()), act(ActionCancel))
	})
	tw(t, f, "while collapsed, navigation keys are swallowed as ignore (no state mutation behind the one-line footer)", func(t *testing.T) {
		eqAction(t, RouteKey(sentinel(krUP), krState(collapsedOn, atOption(2)), krRuntime()), ignore)
		eqAction(t, RouteKey(sentinel(krDOWN), krState(collapsedOn), krRuntime()), ignore)
		eqAction(t, RouteKey(sentinel(krCONFIRM), krState(collapsedOn), krRuntime()), ignore)
		eqAction(t, RouteKey(byteTab, krState(collapsedOn), krRuntime()), ignore)
	})
	tw(t, f, "while collapsed, the notes-forward and toggle branches are unreachable (lockout precedes them)", func(t *testing.T) {
		eqAction(t, RouteKey("x", krState(collapsedOn, notesOn), krRuntime()), ignore)
		eqAction(t, RouteKey(byteSpace, krState(collapsedOn), krRuntime(withQuestions(krQuestion(func(q *QuestionData) { q.MultiSelect = true })))), ignore)
	})
	tw(t, f, "uses the runtime's collapseKey for the toggle (default `ctrl+]`, configurable for non-US layouts)", func(t *testing.T) {
		const byteAltO = "\x1bo"
		eqAction(t, RouteKey(byteAltO, krState(), krRuntime(withCollapse("alt+o"))), toggleCollapsed)
		eqAction(t, RouteKey("\x1d", krState(), krRuntime(withCollapse("alt+o"))), ignore)
	})
	tw(t, f, "`collapseKey: 'off'` disables the toggle entirely (the key is never matched)", func(t *testing.T) {
		got := RouteKey(byteCtrlRBracket, krState(), krRuntime(withCollapse("off")))
		if reflect.DeepEqual(got, toggleCollapsed) {
			t.Fatal("toggled")
		}
	})
	tw(t, f, "treats a missing collapseKey as disabled instead of throwing after an in-process package update", func(t *testing.T) {
		rt := krRuntime(withCollapse(""))
		eqAction(t, RouteKey(byteCtrlRBracket, krState(), rt), ignore)
	})
}
