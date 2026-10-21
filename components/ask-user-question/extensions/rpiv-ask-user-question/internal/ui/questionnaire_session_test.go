package ui

import (
	"strings"
	"testing"
)

const (
	sDOWN       = "\x1b[B"
	sUP         = "\x1b[A"
	sENTER      = "<ENTER>"
	sESC        = "\x1b"
	sCTRL_G     = "\x07"
	sCTRL_U     = "\x15"
	sSHIFTENTER = "\x1b\r"
	sTAB        = "\t"
)

func sessionKeybindings(data, name string) bool {
	switch name {
	case "tui.select.up":
		return data == sUP
	case "tui.select.down":
		return data == sDOWN
	case "tui.select.confirm":
		return data == sENTER
	case "tui.input.newLine":
		return data == sSHIFTENTER
	case "tui.editor.cursorUp":
		return data == sUP
	case "tui.editor.cursorDown":
		return data == sDOWN
	case "tui.select.cancel":
		return data == sESC
	case "tui.editor.deleteToLineStart":
		return data == sCTRL_U
	case "app.editor.external":
		return data == sCTRL_G
	}
	return false
}

var sessionParams = []QuestionData{{
	Question: "Which?", Header: "Pick",
	Options: []OptionData{{Label: "A", Description: "a"}, {Label: "B", Description: "b"}},
}}

func sessionItemsFor(questions []QuestionData) [][]WrappingSelectItem {
	out := [][]WrappingSelectItem{}
	for _, q := range questions {
		items := []WrappingSelectItem{}
		for _, o := range q.Options {
			items = append(items, WrappingSelectItem{Kind: "option", Label: o.Label, Description: o.Description})
		}
		items = append(items, WrappingSelectItem{Kind: "other", Label: "Type something."})
		out = append(out, items)
	}
	return out
}

type sessionOpts struct {
	params    []QuestionData
	editInput func(value string, finish func(string, bool))
	kb        func(data, name string) bool
}

type sessionHarness struct {
	session *QuestionnaireSession
	results []QuestionnaireResult
}

func makeSession(o sessionOpts) *sessionHarness {
	params := o.params
	if params == nil {
		params = sessionParams
	}
	kb := o.kb
	if kb == nil {
		kb = sessionKeybindings
	}
	edit := o.editInput
	if edit == nil {
		edit = func(string, func(string, bool)) {}
	}
	h := &sessionHarness{}
	h.session = NewQuestionnaireSession(QuestionnaireSessionConfig{
		TUI: &fakeTUI{columns: 120, rows: 40}, Theme: identityTheme{}, Questions: params,
		ItemsByTab:  sessionItemsFor(params),
		Done:        func(r QuestionnaireResult) { h.results = append(h.results, r) },
		Keybindings: sentinelKBFn(kb), EditInput: edit, CollapseKey: "off", CanReopenWhileHidden: false,
	})
	return h
}

func (h *sessionHarness) dispatch(keys ...string) {
	for _, k := range keys {
		h.session.Dispatch(k)
	}
}
func (h *sessionHarness) view() string       { return strings.Join(h.session.Component().Render(120), "\n") }
func (h *sessionHarness) focusCustomAnswer() { h.dispatch(sDOWN, sDOWN) }

// calledWith is `toHaveBeenCalledWith` on the done spy: some call equals want.
func (h *sessionHarness) calledWith(t *testing.T, want QuestionnaireResult) {
	t.Helper()
	for _, r := range h.results {
		if eqResult(r, want) {
			return
		}
	}
	t.Fatalf("done not called with %+v; calls %+v", want, h.results)
}

func eqResult(a, b QuestionnaireResult) bool {
	if a.Cancelled != b.Cancelled || a.GlobalNote != b.GlobalNote || len(a.Answers) != len(b.Answers) {
		return false
	}
	for i := range a.Answers {
		if !eqAnswer(a.Answers[i], b.Answers[i]) {
			return false
		}
	}
	return true
}

func eqAnswer(a, b QuestionAnswer) bool {
	if a.QuestionIndex != b.QuestionIndex || a.Question != b.Question || a.Kind != b.Kind || a.Notes != b.Notes {
		return false
	}
	if (a.Answer == nil) != (b.Answer == nil) || (a.Answer != nil && *a.Answer != *b.Answer) {
		return false
	}
	return true
}

func lastResult(t *testing.T, h *sessionHarness) QuestionnaireResult {
	t.Helper()
	if len(h.results) == 0 {
		t.Fatal("done never called")
	}
	return h.results[len(h.results)-1]
}

func TestQuestionnaireSession(t *testing.T) {
	const f = "state/questionnaire-session"
	custom := func(answer string) QuestionAnswer {
		return QuestionAnswer{QuestionIndex: 0, Question: "Which?", Kind: "custom", Answer: strp(answer)}
	}
	// objectContaining({kind, answer}) matching: a result with exactly one answer of this kind/answer.
	oneCustom := func(t *testing.T, h *sessionHarness, answer *string) {
		t.Helper()
		r := lastResult(t, h)
		if r.Cancelled || len(r.Answers) != 1 || r.Answers[0].Kind != "custom" {
			t.Fatalf("%+v", r)
		}
		a := r.Answers[0].Answer
		if (a == nil) != (answer == nil) || (a != nil && *a != *answer) {
			t.Fatalf("answer %v want %v", a, answer)
		}
	}

	tw(t, f, "preserves a draft while browsing options and restores it on return", func(t *testing.T) {
		h := makeSession(sessionOpts{})
		h.focusCustomAnswer()
		h.dispatch("draft answer", sUP)
		browsing := h.view()
		if !strings.Contains(browsing, "draft answer") || strings.Contains(browsing, "Type something.") {
			t.Fatalf("view:\n%s", browsing)
		}
		h.dispatch(sDOWN, sENTER)
		h.calledWith(t, QuestionnaireResult{Answers: []QuestionAnswer{custom("draft answer")}, Cancelled: false})
	})
	tw(t, f, "submits a multiline custom answer composed with Shift+Enter", func(t *testing.T) {
		h := makeSession(sessionOpts{})
		h.focusCustomAnswer()
		h.dispatch("first line", sSHIFTENTER, "second line")
		view := h.view()
		if !strings.Contains(view, "first line") || !strings.Contains(view, "second line") {
			t.Fatalf("view:\n%s", view)
		}
		h.dispatch(sENTER)
		oneCustom(t, h, strp("first line\nsecond line"))
	})
	tw(t, f, "uses vertical arrows within the draft and returns to row navigation at the boundary", func(t *testing.T) {
		h := makeSession(sessionOpts{})
		h.focusCustomAnswer()
		h.dispatch("first", sSHIFTENTER, "second", sUP, "!", sUP, sDOWN, sENTER)
		oneCustom(t, h, strp("first!\nsecond"))
	})
	tw(t, f, "clears the whole draft with Pi's Ctrl+U line-kill binding", func(t *testing.T) {
		h := makeSession(sessionOpts{})
		h.focusCustomAnswer()
		h.dispatch("discard me", sCTRL_U, sENTER)
		oneCustom(t, h, nil)
	})
	tw(t, f, "replaces the inline draft with the external editor result", func(t *testing.T) {
		var calls []string
		h := makeSession(sessionOpts{editInput: func(value string, finish func(string, bool)) {
			calls = append(calls, value)
			finish(value+" + edited", true)
		}})
		h.focusCustomAnswer()
		h.dispatch("draft", sCTRL_G)
		eqv(t, calls, []string{"draft"})
		h.dispatch(sENTER)
		oneCustom(t, h, strp("draft + edited"))
	})
	tw(t, f, "keeps input exclusive while the external editor is open", func(t *testing.T) {
		var resolveEditor func(string, bool)
		h := makeSession(sessionOpts{editInput: func(_ string, finish func(string, bool)) { resolveEditor = finish }})
		h.focusCustomAnswer()
		h.dispatch("draft", sCTRL_G)
		h.dispatch(sUP, "late input")
		resolveEditor("edited", true)
		h.dispatch(sENTER)
		oneCustom(t, h, strp("edited"))
	})
	tw(t, f, "attaches multiline notes composed with Shift+Enter", func(t *testing.T) {
		h := makeSession(sessionOpts{})
		h.dispatch("n", "first note", sSHIFTENTER, "second note", sENTER, sENTER)
		r := lastResult(t, h)
		if r.Cancelled || len(r.Answers) != 1 || r.Answers[0].Kind != "option" || r.Answers[0].Notes != "first note\nsecond note" {
			t.Fatalf("%+v", r)
		}
	})
	tw(t, f, "commits the typed draft with a remapped tui.input.submit key (#156)", func(t *testing.T) {
		const ctrlEnter = "<CTRL_ENTER>"
		remapped := func(data, name string) bool {
			if name == "tui.input.submit" {
				return data == ctrlEnter
			}
			if name == "tui.input.newLine" {
				return data == sENTER || data == sSHIFTENTER
			}
			return sessionKeybindings(data, name)
		}
		h := makeSession(sessionOpts{kb: remapped})
		h.focusCustomAnswer()
		h.dispatch("first line", sSHIFTENTER, "second line", ctrlEnter)
		oneCustom(t, h, strp("first line\nsecond line"))
	})
	tw(t, f, "a raw Enter byte the router does not match cannot wipe the draft via the editor's own submit (#156)", func(t *testing.T) {
		h := makeSession(sessionOpts{})
		h.focusCustomAnswer()
		h.dispatch("precious draft", "\r")
		if !strings.Contains(h.view(), "precious draft") {
			t.Fatalf("draft wiped:\n%s", h.view())
		}
		h.dispatch(sENTER)
		oneCustom(t, h, strp("precious draft"))
	})
	tw(t, f, "keeps each question's latest draft isolated through real navigation and tab switches", func(t *testing.T) {
		q0, q1 := sessionParams[0], sessionParams[0]
		q0.Question, q0.Header = "First?", "First"
		q1.Question, q1.Header = "Second?", "Second"
		h := makeSession(sessionOpts{params: []QuestionData{q0, q1}})
		h.focusCustomAnswer()
		h.dispatch("first", sUP, sDOWN, "-latest", sENTER)
		h.focusCustomAnswer()
		h.dispatch("second", sUP, sTAB, sTAB)
		if !strings.Contains(h.view(), "first-latest") {
			t.Fatalf("view:\n%s", h.view())
		}
		h.dispatch(sTAB)
		if !strings.Contains(h.view(), "second") {
			t.Fatalf("view:\n%s", h.view())
		}
	})
	tw(t, f, "renders the cancel-only line, never a literal 'Off to expand' (#176)", func(t *testing.T) {
		h := makeSession(sessionOpts{})
		h.session.ToggleCollapsedExternal()
		collapsed := h.session.Component().Render(120)
		eqv(t, len(collapsed), 1)
		if !strings.Contains(collapsed[0], "Esc to cancel") || strings.Contains(collapsed[0], "to expand") || strings.Contains(collapsed[0], "Off") {
			t.Fatalf("%q", collapsed[0])
		}
	})
}
