package ask_user_question

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	tui "github.com/MichaelKinsy/pigpen/components/pitui"
)

const fFactory = "factory"

var (
	threeOptionParams = askArgsOf(askQ("Pick one", "Choice", false, labels("Alpha", "Beta", "Gamma")...))
	mixedParams       = askArgsOf(
		askQ("Q1", "H1", false, labels("A", "B")...),
		askQ("Q2", "H2", true, labels("FE", "BE", "DB", "QA", "Ops")...))
	freeTextParams = askArgsOf(askQ("Name?", "Name", false,
		map[string]any{"label": "Default", "description": "Default option"},
		map[string]any{"label": "Second", "description": "Second option"}))
	twoParams = askArgsOf(
		askQ("Q1?", "First", false, labels("A", "B")...),
		askQ("Q2?", "Second", false, labels("X", "Y")...))
)

// drive runs the tool against the real questionnaire component (driveCustom) and returns the envelope.
func drive(t *testing.T, args map[string]any, script func(c *scriptComp, done func(any))) (*fakeUIHost, string, map[string]any) {
	t.Helper()
	f := newFakeUI(t)
	f.script = script
	text, details := f.run(args)
	return f, text, details
}

func cancelledNow(done func(any)) { done(map[string]any{"answers": []any{}, "cancelled": true}) }

func TestFactory(t *testing.T) {
	tw(t, fFactory, "renders a non-empty view at width 80", func(t *testing.T) {
		drive(t, threeOptionParams, func(c *scriptComp, done func(any)) {
			if len(c.render(80)) == 0 {
				t.Fatal("empty render")
			}
			cancelledNow(done)
		})
	})
	tw(t, fFactory, "Esc cancels → returns decline envelope with cancelled=true", func(t *testing.T) {
		_, _, d := drive(t, threeOptionParams, func(c *scriptComp, _ func(any)) { c.in(keyEsc) })
		matchObject(t, d, map[string]any{"cancelled": true}, "details")
	})
	tw(t, fFactory, "Enter on first item → single-question auto-submits", func(t *testing.T) {
		_, _, d := drive(t, threeOptionParams, func(c *scriptComp, _ func(any)) { c.in(keyEnter) })
		matchObject(t, d, map[string]any{"cancelled": false}, "details")
		matchObject(t, answerAt(t, d, 0), map[string]any{"answer": "Alpha", "kind": "option"}, "answer")
	})
	tw(t, fFactory, "DOWN navigates without completing; Esc cancels", func(t *testing.T) {
		f, _, _ := drive(t, threeOptionParams, func(c *scriptComp, _ func(any)) {
			c.in(keyDown)
			c.in(keyEsc)
		})
		if f.renders == 0 {
			t.Fatal("requestRender was not called")
		}
	})
	tw(t, fFactory, "invalidate() is callable without throwing", func(t *testing.T) {
		// The SDK's RemoteComponent has no invalidate(): the host drives the SetInvalidate hook (the session's
		// requestRender), replaced on attach and cleared with nil on detach.
		drive(t, threeOptionParams, func(c *scriptComp, done func(any)) {
			inv, ok := c.inner.(interface{ SetInvalidate(func()) })
			if !ok {
				t.Fatal("component has no SetInvalidate")
			}
			inv.SetInvalidate(func() {})
			inv.SetInvalidate(nil)
			c.in(keyDown)
			c.render(80)
			cancelledNow(done)
		})
	})
	tw(t, fFactory, "Ctrl+] collapses the questionnaire to a one-line footer; Ctrl+] again expands to the full render", func(t *testing.T) {
		var expanded, reExpanded int
		var collapsed []string
		drive(t, threeOptionParams, func(c *scriptComp, done func(any)) {
			expanded = len(c.render(120))
			c.in(ctrlRBr)
			collapsed = c.render(120)
			c.in(ctrlRBr)
			reExpanded = len(c.render(120))
			cancelledNow(done)
		})
		eq(t, len(collapsed), 1, "collapsed rows")
		if !strings.Contains(collapsed[0], "Ctrl+] to expand") || !strings.Contains(collapsed[0], "Esc to cancel") {
			t.Fatalf("collapsed row %q", collapsed[0])
		}
		if expanded <= 1 {
			t.Fatalf("expanded rows %d", expanded)
		}
		eq(t, reExpanded, expanded, "re-expanded rows")
	})
	tw(t, fFactory, "Esc while collapsed cancels (the only key honoured behind the one-line footer)", func(t *testing.T) {
		_, _, d := drive(t, threeOptionParams, func(c *scriptComp, _ func(any)) {
			c.in(ctrlRBr)
			c.in(keyEsc)
		})
		matchObject(t, d, map[string]any{"cancelled": true}, "details")
	})

	// single-question navigation
	tw(t, fFactory, "DOWN to Beta, Enter → selects Beta (kind:'option')", func(t *testing.T) {
		_, _, d := drive(t, threeOptionParams, func(c *scriptComp, _ func(any)) {
			c.in(keyDown)
			c.in(keyEnter)
		})
		eq(t, d["cancelled"], false, "cancelled")
		matchObject(t, answerAt(t, d, 0), map[string]any{"questionIndex": float64(0), "answer": "Beta", "kind": "option"}, "answer")
	})
	tw(t, fFactory, "DOWN×2 to Gamma, UP×1 back to Beta, Enter → selects Beta", func(t *testing.T) {
		_, _, d := drive(t, threeOptionParams, func(c *scriptComp, _ func(any)) {
			c.in(keyDown)
			c.in(keyDown)
			c.in(keyUp)
			c.in(keyEnter)
		})
		eq(t, answerAt(t, d, 0)["answer"], "Beta", "answer")
	})
	tw(t, fFactory, "UP from Alpha wraps to Type-something, then UP to Gamma", func(t *testing.T) {
		_, _, d := drive(t, threeOptionParams, func(c *scriptComp, _ func(any)) {
			c.in(keyUp)
			c.in(keyUp)
			c.in(keyEnter)
		})
		eq(t, d["cancelled"], false, "cancelled")
		eq(t, answerAt(t, d, 0)["answer"], "Gamma", "answer")
	})

	// 'Type something.' free-text flow
	tw(t, fFactory, "navigate to Other sentinel, type text, Enter → kind:'custom' with typed text", func(t *testing.T) {
		_, _, d := drive(t, freeTextParams, func(c *scriptComp, _ func(any)) {
			c.in(keyDown)
			c.in(keyDown)
			typeText(c, "hello")
			c.in(keyEnter)
		})
		eq(t, d["cancelled"], false, "cancelled")
		eq(t, answerAt(t, d, 0)["kind"], "custom", "kind")
		eq(t, answerAt(t, d, 0)["answer"], "hello", "answer")
	})
	tw(t, fFactory, "Type something with no input → kind:'custom', answer=null", func(t *testing.T) {
		_, text, d := drive(t, freeTextParams, func(c *scriptComp, _ func(any)) {
			c.in(keyDown)
			c.in(keyDown)
			c.in(keyEnter)
		})
		eq(t, answerAt(t, d, 0)["kind"], "custom", "kind")
		eq(t, answerAt(t, d, 0)["answer"], nil, "answer")
		if !strings.Contains(text, "(no input)") {
			t.Fatalf("text %q", text)
		}
	})
	tw(t, fFactory, "Type text, backspace removes last char, then Enter", func(t *testing.T) {
		_, _, d := drive(t, freeTextParams, func(c *scriptComp, _ func(any)) {
			c.in(keyDown)
			c.in(keyDown)
			typeText(c, "abc")
			c.in("\x7f")
			c.in(keyEnter)
		})
		eq(t, answerAt(t, d, 0)["answer"], "ab", "answer")
	})

	tw(t, fFactory, "dialog total line count is identical across tab switches (mixed single+multi fixture)", func(t *testing.T) {
		var tab0, tab1 int
		drive(t, mixedParams, func(c *scriptComp, done func(any)) {
			tab0 = len(c.render(120))
			c.in(keyTab)
			tab1 = len(c.render(120))
			cancelledNow(done)
		})
		eq(t, tab1, tab0, "line count")
	})

	// multi-select flow (single question)
	multiParams := askArgsOf(askQ("Pick areas", "Areas", true, labels("Frontend", "Backend", "DevOps")...))
	tw(t, fFactory, "Space toggles items, then DOWN to Next + Enter confirms selected labels", func(t *testing.T) {
		_, _, d := drive(t, multiParams, func(c *scriptComp, _ func(any)) {
			c.in(keySpace)
			c.in(keyDown)
			c.in(keySpace)
			c.in(keyDown)
			c.in(keyDown)
			c.in(keyDown)
			c.in(keyEnter)
		})
		eq(t, d["cancelled"], false, "cancelled")
		eq(t, answerAt(t, d, 0)["answer"], nil, "answer")
		eq(t, answerAt(t, d, 0)["selected"], []any{"Frontend", "Backend"}, "selected")
	})
	tw(t, fFactory, "Enter on a regular row toggles (no submit); commit happens on Next", func(t *testing.T) {
		_, _, d := drive(t, multiParams, func(c *scriptComp, _ func(any)) {
			c.in(keyEnter)
			c.in(keyDown)
			c.in(keyEnter)
			c.in(keyEnter)
			c.in(keyDown)
			c.in(keyDown)
			c.in(keyDown)
			c.in(keyEnter)
		})
		eq(t, d["cancelled"], false, "cancelled")
		eq(t, answerAt(t, d, 0)["selected"], []any{"Frontend"}, "selected")
	})
	tw(t, fFactory, "Space toggles on then off → item excluded", func(t *testing.T) {
		_, _, d := drive(t, multiParams, func(c *scriptComp, _ func(any)) {
			c.in(keySpace)
			c.in(keySpace)
			c.in(keyDown)
			c.in(keySpace)
			c.in(keyDown)
			c.in(keyDown)
			c.in(keyDown)
			c.in(keyEnter)
		})
		eq(t, answerAt(t, d, 0)["selected"], []any{"Backend"}, "selected")
	})
	tw(t, fFactory, "Enter on Next with nothing toggled → selected=[]", func(t *testing.T) {
		_, _, d := drive(t, multiParams, func(c *scriptComp, _ func(any)) {
			c.in(keyDown)
			c.in(keyDown)
			c.in(keyDown)
			c.in(keyDown)
			c.in(keyEnter)
		})
		eq(t, answerAt(t, d, 0)["selected"], []any{}, "selected")
	})

	// multi-select toggle persistence (regression)
	persistParams := askArgsOf(
		askQ("Q1", "H1", true, labels("FE", "BE", "DB")...),
		askQ("Q2", "H2", false, labels("A", "B")...))
	findQ := func(t *testing.T, d map[string]any, idx float64) map[string]any {
		t.Helper()
		for _, a := range answersOf(t, d) {
			if m, _ := a.(map[string]any); m["questionIndex"] == idx {
				return m
			}
		}
		t.Fatalf("no answer for question %v in %#v", idx, d)
		return nil
	}
	tw(t, fFactory, "Tab away from a multi-select tab preserves toggles in the final answers", func(t *testing.T) {
		_, _, d := drive(t, persistParams, func(c *scriptComp, _ func(any)) {
			c.in(keySpace)
			c.in(keyDown)
			c.in(keySpace)
			c.in(keyTab)
			c.in(keyEnter)
			c.in(keyEnter)
		})
		eq(t, d["cancelled"], false, "cancelled")
		eq(t, findQ(t, d, 0)["selected"], []any{"FE", "BE"}, "selected")
	})
	tw(t, fFactory, "Tab back to a multi-select tab restores the previous toggle state", func(t *testing.T) {
		_, _, d := drive(t, persistParams, func(c *scriptComp, _ func(any)) {
			c.in(keySpace)
			c.in(keyTab)
			c.in(keyShiftTab)
			c.in(keyDown)
			c.in(keySpace)
			c.in(keyDown)
			c.in(keyDown)
			c.in(keyDown)
			c.in(keyEnter)
			c.in(keyEnter)
			c.in(keyEnter)
		})
		eq(t, d["cancelled"], false, "cancelled")
		eq(t, findQ(t, d, 0)["selected"], []any{"FE", "BE"}, "selected")
	})

	// multi-question tab cycling flow
	tw(t, fFactory, "answer Q1 → auto-advance to Q2 → answer Q2 → auto-advance to Submit", func(t *testing.T) {
		_, _, d := drive(t, twoParams, func(c *scriptComp, _ func(any)) {
			c.in(keyEnter)
			c.in(keyEnter)
			c.in(keyEnter)
		})
		eq(t, d["cancelled"], false, "cancelled")
		eq(t, len(answersOf(t, d)), 2, "answers")
		eq(t, answerAt(t, d, 0)["answer"], "A", "a0")
		eq(t, answerAt(t, d, 1)["answer"], "X", "a1")
	})
	tw(t, fFactory, "Tab cycles: Q1 → Q2 → Submit → Shift+Tab back to Q2", func(t *testing.T) {
		var q1, q2, sub int
		drive(t, twoParams, func(c *scriptComp, done func(any)) {
			q1 = len(c.render(120))
			c.in(keyTab)
			q2 = len(c.render(120))
			c.in(keyTab)
			sub = len(c.render(120))
			c.in(keyShiftTab)
			cancelledNow(done)
		})
		eq(t, q2, q1, "Q2 vs Q1")
		eq(t, sub, q2, "Submit vs Q2")
	})
	tw(t, fFactory, "cancel mid-flow preserves partial answers in details", func(t *testing.T) {
		_, text, d := drive(t, twoParams, func(c *scriptComp, _ func(any)) {
			c.in(keyEnter)
			c.in(keyEsc)
		})
		eq(t, d["cancelled"], true, "cancelled")
		if !strings.Contains(text, "declined") {
			t.Fatalf("text %q", text)
		}
		eq(t, len(answersOf(t, d)), 1, "answers")
		eq(t, answerAt(t, d, 0)["answer"], "A", "answer")
	})
	tw(t, fFactory, "Submit tab with Enter on Submit row submits with partial answers (D1 revised)", func(t *testing.T) {
		_, _, d := drive(t, twoParams, func(c *scriptComp, _ func(any)) {
			c.in(keyEnter)
			c.in(keyTab)
			c.in(keyEnter)
		})
		eq(t, d["cancelled"], false, "cancelled")
		eq(t, len(answersOf(t, d)), 1, "answers")
		eq(t, answerAt(t, d, 0)["question"], "Q1?", "question")
	})
	tw(t, fFactory, "TAB → Submit, n + note + Enter commits, Enter submits → details.globalNote set + 'global note:' in envelope", func(t *testing.T) {
		_, text, d := drive(t, twoParams, func(c *scriptComp, _ func(any)) {
			c.in(keyTab)
			c.in(keyTab)
			c.in("n")
			typeText(c, "ship")
			c.in(keyEnter)
			c.in(keyEnter)
		})
		eq(t, d["cancelled"], false, "cancelled")
		eq(t, d["globalNote"], "ship", "globalNote")
		if !strings.Contains(text, "global note:") {
			t.Fatalf("text %q", text)
		}
	})
	tw(t, fFactory, "answer all → Submit tab → DOWN → Enter on Cancel returns cancelled=true with all answers preserved", func(t *testing.T) {
		_, _, d := drive(t, twoParams, func(c *scriptComp, _ func(any)) {
			c.in(keyEnter)
			c.in(keyEnter)
			c.in(keyDown)
			c.in(keyEnter)
		})
		eq(t, d["cancelled"], true, "cancelled")
		eq(t, len(answersOf(t, d)), 2, "answers")
		eq(t, answerAt(t, d, 0)["answer"], "A", "a0")
		eq(t, answerAt(t, d, 1)["answer"], "X", "a1")
	})
	tw(t, fFactory, "Tab back to a single-select tab marks the prior option with ` ✔`", func(t *testing.T) {
		var lines []string
		drive(t, twoParams, func(c *scriptComp, done func(any)) {
			c.in(keyDown)
			c.in(keyEnter)
			c.in(keyShiftTab)
			lines = c.render(120)
			cancelledNow(done)
		})
		if !anyLine(lines, lineHas("B ✔")) {
			t.Fatalf("no `B ✔` row in %q", lines)
		}
		if anyLine(lines, lineHas("A ✔")) {
			t.Fatalf("unexpected `A ✔` row in %q", lines)
		}
		if !anyLine(lines, lineHas("❯ 1. A")) {
			t.Fatalf("no `❯ 1. A` row in %q", lines)
		}
	})
	tw(t, fFactory, "Tab back after `Type something.` → row reads `<text> ✔` and buffer is restored", func(t *testing.T) {
		var back, onOther []string
		drive(t, twoParams, func(c *scriptComp, done func(any)) {
			c.in(keyDown)
			c.in(keyDown)
			typeText(c, "Hello")
			c.in(keyEnter)
			c.in(keyShiftTab)
			back = c.render(120)
			c.in(keyDown)
			c.in(keyDown)
			onOther = c.render(120)
			cancelledNow(done)
		})
		if !anyLine(back, lineHas("Hello ✔")) {
			t.Fatalf("no `Hello ✔` row in %q", back)
		}
		if anyLine(back, lineHas("Type something.")) {
			t.Fatalf("`Type something.` still shown in %q", back)
		}
		if !anyLine(back, lineHas("❯ 1. A")) {
			t.Fatalf("no `❯ 1. A` row in %q", back)
		}
		if !anyLine(onOther, func(l string) bool { return strings.Contains(l, "Hello") && strings.Contains(l, tui.CursorMarker) }) {
			t.Fatalf("no row with Hello and the cursor marker in %q", onOther)
		}
	})
	tw(t, fFactory, "Tab back to a multi-select tab keeps `[✔]` and does NOT add a trailing ` ✔`", func(t *testing.T) {
		args := askArgsOf(
			askQ("Q1?", "H1", true, labels("FE", "BE", "DB")...),
			askQ("Q2?", "H2", false, labels("A", "B")...))
		var lines []string
		drive(t, args, func(c *scriptComp, done func(any)) {
			c.in(keySpace)
			c.in(keyTab)
			c.in(keyShiftTab)
			lines = c.render(120)
			cancelledNow(done)
		})
		if !anyLine(lines, func(l string) bool { return strings.Contains(l, "[✔]") && strings.Contains(l, "FE") }) {
			t.Fatalf("no `[✔] FE` row in %q", lines)
		}
		// `FE\s+✔(?!\])`: Go has no lookahead; a ✔ that is not followed by `]`.
		re := regexp.MustCompile(`FE\s+✔($|[^\]])`)
		if anyLine(lines, re.MatchString) {
			t.Fatalf("multi-select row gained a trailing ✔ in %q", lines)
		}
	})

	// MAX_QUESTIONS (4 questions) complete flow
	d2 := func(label string) []any {
		return []any{
			map[string]any{"label": label, "description": label + " option"},
			map[string]any{"label": label + "2", "description": label + "2 option"},
		}
	}
	fourParams := askArgsOf(
		askQ("Q1?", "H1", false, d2("A")...), askQ("Q2?", "H2", false, d2("B")...),
		askQ("Q3?", "H3", false, d2("C")...), askQ("Q4?", "H4", false, d2("D")...))
	tw(t, fFactory, "answer all 4 questions with auto-advance, then submit", func(t *testing.T) {
		_, text, d := drive(t, fourParams, func(c *scriptComp, _ func(any)) {
			for range 5 {
				c.in(keyEnter)
			}
		})
		eq(t, d["cancelled"], false, "cancelled")
		eq(t, len(answersOf(t, d)), 4, "answers")
		var got []any
		for _, a := range answersOf(t, d) {
			got = append(got, a.(map[string]any)["answer"])
		}
		eq(t, got, []any{"A", "B", "C", "D"}, "labels")
		if !strings.Contains(text, `"Q1?"="A".`) || !strings.Contains(text, `"Q4?"="D".`) {
			t.Fatalf("text %q", text)
		}
		if !regexp.MustCompile(`^User has answered your questions:`).MatchString(text) {
			t.Fatalf("prefix: %q", text)
		}
		if !regexp.MustCompile(`You can now continue with the user's answers in mind\.$`).MatchString(text) {
			t.Fatalf("suffix: %q", text)
		}
	})
	tw(t, fFactory, "cancel after answering 2 of 4 → partial answers preserved", func(t *testing.T) {
		_, _, d := drive(t, fourParams, func(c *scriptComp, _ func(any)) {
			c.in(keyEnter)
			c.in(keyEnter)
			c.in(keyEsc)
		})
		eq(t, d["cancelled"], true, "cancelled")
		eq(t, len(answersOf(t, d)), 2, "answers")
	})

	tw(t, fFactory, "answer single-select Q1, toggle multi-select Q2, submit", func(t *testing.T) {
		_, _, d := drive(t, mixedParams, func(c *scriptComp, _ func(any)) {
			c.in(keyEnter)
			c.in(keySpace)
			c.in(keyDown)
			c.in(keyDown)
			c.in(keySpace)
			for range 4 {
				c.in(keyDown)
			}
			c.in(keyEnter)
			c.in(keyEnter)
		})
		eq(t, d["cancelled"], false, "cancelled")
		eq(t, len(answersOf(t, d)), 2, "answers")
		matchObject(t, answerAt(t, d, 0), map[string]any{"answer": "A", "kind": "option"}, "a0")
		matchObject(t, answerAt(t, d, 1), map[string]any{"answer": nil, "selected": []any{"FE", "DB"}}, "a1")
	})

	// notes pre-answer (Slice 5 notes UX)
	previewParams := askArgsOf(askQ("Pick layout", "Layout", false,
		map[string]any{"label": "Centered", "description": "Centered logo", "preview": "## Centered\n\nbody"},
		map[string]any{"label": "Left", "description": "Left logo"}))
	tw(t, fFactory, "user can press 'n' to add notes BEFORE pressing Enter on a preview-bearing option", func(t *testing.T) {
		_, _, d := drive(t, previewParams, func(c *scriptComp, _ func(any)) {
			c.in("n")
			typeText(c, "hello")
			c.in(keyEsc)
			c.in(keyEnter)
		})
		eq(t, d["cancelled"], false, "cancelled")
		matchObject(t, answerAt(t, d, 0), map[string]any{"answer": "Centered", "kind": "option"}, "answer")
		eq(t, answerAt(t, d, 0)["notes"], "hello", "notes")
	})
	tw(t, fFactory, "'n' opens the notes editor on a no-preview option and lands the note on the answer (universal `n` gate)", func(t *testing.T) {
		_, _, d := drive(t, previewParams, func(c *scriptComp, _ func(any)) {
			c.in(keyDown)
			c.in("n")
			typeText(c, "hello")
			c.in(keyEsc)
			c.in(keyEnter)
		})
		eq(t, d["cancelled"], false, "cancelled")
		eq(t, answerAt(t, d, 0)["answer"], "Left", "answer")
		eq(t, answerAt(t, d, 0)["notes"], "hello", "notes")
	})

	// bracketed paste + Kitty CSI-u (dictation parity)
	pasteInto := func(t *testing.T, chunks ...string) map[string]any {
		t.Helper()
		_, _, d := drive(t, freeTextParams, func(c *scriptComp, _ func(any)) {
			c.in(keyDown)
			c.in(keyDown)
			for _, ch := range chunks {
				c.in(ch)
			}
			c.in(keyEnter)
		})
		return d
	}
	tw(t, fFactory, "bracketed paste on the inline-Other row commits the pasted text as kind:'custom'", func(t *testing.T) {
		d := pasteInto(t, "\x1b[200~Hello world\x1b[201~")
		eq(t, d["cancelled"], false, "cancelled")
		eq(t, answerAt(t, d, 0)["kind"], "custom", "kind")
		eq(t, answerAt(t, d, 0)["answer"], "Hello world", "answer")
	})
	tw(t, fFactory, "bracketed paste split across two handleInput calls is reassembled correctly", func(t *testing.T) {
		d := pasteInto(t, "\x1b[200~Hel", "lo\x1b[201~")
		eq(t, answerAt(t, d, 0)["answer"], "Hello", "answer")
	})
	tw(t, fFactory, "bracketed paste preserves line breaks and expands tabs", func(t *testing.T) {
		d := pasteInto(t, "\x1b[200~Hello\nworld\twith\rmix\x1b[201~")
		eq(t, answerAt(t, d, 0)["answer"], "Hello\nworld    with\nmix", "answer")
	})
	tw(t, fFactory, "Kitty CSI-u printables (\\x1b[97u …) are decoded and committed", func(t *testing.T) {
		d := pasteInto(t, "\x1b[97u", "\x1b[98u", "\x1b[99u")
		eq(t, answerAt(t, d, 0)["kind"], "custom", "kind")
		eq(t, answerAt(t, d, 0)["answer"], "abc", "answer")
	})
	tw(t, fFactory, "bracketed paste followed by typed chars + backspace produces the expected merged value", func(t *testing.T) {
		d := pasteInto(t, "\x1b[200~Hi\x1b[201~", "!", "\x7f")
		eq(t, answerAt(t, d, 0)["answer"], "Hi", "answer")
	})
	tw(t, fFactory, "raw multi-character chunk (no bracketed paste markers) appends as-is", func(t *testing.T) {
		d := pasteInto(t, "Hello world")
		eq(t, answerAt(t, d, 0)["kind"], "custom", "kind")
		eq(t, answerAt(t, d, 0)["answer"], "Hello world", "answer")
	})
	lines := func(prefix string) string {
		b := make([]string, 12)
		for i := range b {
			b[i] = fmt.Sprintf("%s %d", prefix, i+1)
		}
		return strings.Join(b, "\n")
	}
	tw(t, fFactory, "large bracketed paste (>10 lines) commits the expanded content, not the compact marker", func(t *testing.T) {
		bigPaste := lines("line")
		d := pasteInto(t, "\x1b[200~"+bigPaste+"\x1b[201~")
		eq(t, d["cancelled"], false, "cancelled")
		eq(t, answerAt(t, d, 0)["kind"], "custom", "kind")
		eq(t, answerAt(t, d, 0)["answer"], bigPaste, "answer")
		if strings.Contains(answerAt(t, d, 0)["answer"].(string), "[paste #") {
			t.Fatal("the compact paste marker leaked into the answer")
		}
	})
	tw(t, fFactory, "large bracketed paste into notes lands the expanded text in answer.notes", func(t *testing.T) {
		bigNotes := lines("note")
		_, _, d := drive(t, freeTextParams, func(c *scriptComp, _ func(any)) {
			c.in("n")
			c.in("\x1b[200~" + bigNotes + "\x1b[201~")
			c.in(keyEsc)
			c.in(keyEnter)
		})
		eq(t, answerAt(t, d, 0)["answer"], "Default", "answer")
		eq(t, answerAt(t, d, 0)["notes"], bigNotes, "notes")
	})
	tw(t, fFactory, "large paste survives a tab-switch draft round-trip (restores go through setText)", func(t *testing.T) {
		bigPaste := lines("line")
		_, _, d := drive(t, twoParams, func(c *scriptComp, _ func(any)) {
			c.in(keyDown)
			c.in(keyDown)
			c.in("\x1b[200~" + bigPaste + "\x1b[201~")
			c.in(keyDown)
			c.in(keyTab)
			c.in(keyShiftTab)
			c.in(keyDown)
			c.in(keyDown)
			c.in(keyEnter)
			c.in(keyTab)
			c.in(keyEnter)
		})
		eq(t, d["cancelled"], false, "cancelled")
		var q1 map[string]any
		for _, a := range answersOf(t, d) {
			if m, _ := a.(map[string]any); m["questionIndex"] == float64(0) {
				q1 = m
			}
		}
		if q1 == nil {
			t.Fatalf("no answer for question 0 in %#v", d)
		}
		eq(t, q1["kind"], "custom", "kind")
		eq(t, q1["answer"], bigPaste, "answer")
		if strings.Contains(q1["answer"].(string), "[paste #") {
			t.Fatal("the compact paste marker leaked into the answer")
		}
	})

	// multi-select notes end-to-end
	tw(t, fFactory, "n → type → Esc → Space → Next+Enter lands a multi answer carrying the typed note", func(t *testing.T) {
		_, _, d := drive(t, askArgsOf(askQ("Pick areas", "Areas", true, labels("FE", "BE")...)), func(c *scriptComp, _ func(any)) {
			c.in("n")
			typeText(c, "hi")
			c.in(keyEsc)
			c.in(keySpace)
			c.in(keyDown)
			c.in(keyDown)
			c.in(keyDown)
			c.in(keyEnter)
		})
		eq(t, d["cancelled"], false, "cancelled")
		matchObject(t, answerAt(t, d, 0), map[string]any{"kind": "multi"}, "answer")
		sel, _ := answerAt(t, d, 0)["selected"].([]any)
		found := false
		for _, s := range sel {
			found = found || s == "FE"
		}
		if !found {
			t.Fatalf("selected %#v does not contain FE", sel)
		}
		eq(t, answerAt(t, d, 0)["notes"], "hi", "notes")
	})
}
