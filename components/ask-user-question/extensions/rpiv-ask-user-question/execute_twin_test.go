package ask_user_question

import (
	"strings"
	"testing"
)

// The custom() dispatch of ask-user-question.execute.test.ts: ctxWithCustom(result) resolves custom() with a fixed
// result and never runs the component.
func TestExecuteCustomDispatch(t *testing.T) {
	base := askArgsOf(askQ("Which?", "Pick", false, labels("A", "B")...))
	withResult := func(t *testing.T, args map[string]any, result any) (string, map[string]any) {
		t.Helper()
		f := newFakeUI(t)
		f.customResult = result
		return f.run(args)
	}
	tw(t, "ask-user-question.execute", "User cancels (cancelled: true) → decline envelope", func(t *testing.T) {
		text, d := withResult(t, base, map[string]any{"answers": []any{}, "cancelled": true})
		matchObject(t, d, map[string]any{"cancelled": true}, "details")
		if !strings.Contains(text, "declined") {
			t.Fatalf("text %q", text)
		}
	})
	tw(t, "ask-user-question.execute", "Normal selection → CC envelope wrapper with quoted question and answer", func(t *testing.T) {
		text, _ := withResult(t, base, map[string]any{"cancelled": false, "answers": []any{
			map[string]any{"questionIndex": 0, "question": "Which?", "kind": "option", "answer": "A"}}})
		if !strings.Contains(text, `"Which?"="A"`) || !strings.HasPrefix(text, "User has answered your questions:") {
			t.Fatalf("text %q", text)
		}
	})
	tw(t, "ask-user-question.execute", "Custom typed answer sets kind:'custom'", func(t *testing.T) {
		text, _ := withResult(t, base, map[string]any{"cancelled": false, "answers": []any{
			map[string]any{"questionIndex": 0, "question": "Which?", "kind": "custom", "answer": "typed"}}})
		if !strings.Contains(text, `"Which?"="typed"`) {
			t.Fatalf("text %q", text)
		}
	})
	tw(t, "ask-user-question.execute", "multi-select free-text yields kind:'custom' (not 'multi')", func(t *testing.T) {
		// Focusing 'Type something.', typing, Enter on a multi-select question routes through the inputMode branch
		// (key-router twins). The execute path surfaces that answer verbatim.
		args := askArgsOf(askQ("Pick?", "H", true, labels("A", "B")...))
		text, _ := withResult(t, args, map[string]any{"cancelled": false, "answers": []any{
			map[string]any{"questionIndex": 0, "question": "Pick?", "kind": "custom", "answer": "typed"}}})
		if !strings.Contains(text, `"Pick?"="typed"`) {
			t.Fatalf("text %q", text)
		}
	})
}
