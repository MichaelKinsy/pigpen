package ask_user_question

import (
	"strings"
	"testing"
)

func TestNormalizedOverlay(t *testing.T) {
	tw(t, "ask-user-question.normalize", "renders the TUI overlay with normalized question, header, labels, and descriptions", func(t *testing.T) {
		args := askArgsOf(askQ("Which\r option\r is best?", "Pick\r", false,
			map[string]any{"label": "Option\rA\r (Recommended)", "description": "choice with\r\r stray CR"},
			map[string]any{"label": "GEMBA\r_LOG\r_FILE", "description": "crlf\r\nline", "preview": "a\r\nb"}))
		f := newFakeUI(t)
		f.cols, f.rows = 80, 40
		rendered := ""
		f.script = func(c *scriptComp, done func(any)) {
			rendered = strings.Join(c.render(80), "\n")
			done(map[string]any{"answers": []any{}, "cancelled": false})
		}
		f.run(args)
		eq(t, f.customCalls, 1, "custom calls")
		for _, want := range []string{"Which option is best?", " Pick ", "1. OptionA (Recommended)", "choice with stray CR", "2. GEMBA_LOG_FILE"} {
			if !strings.Contains(rendered, want) {
				t.Fatalf("rendered text lacks %q:\n%s", want, rendered)
			}
		}
		if strings.Contains(rendered, "\r") {
			t.Fatalf("rendered text contains CR: %q", rendered)
		}
	})
}

// ctx.mode unset: the host's Mode() is "" and anything but "rpc" takes the custom() path.
func TestModeUnset(t *testing.T) {
	single := askArgsOf(askQ("Which?", "Pick", false,
		map[string]any{"label": "A", "description": "a"}, map[string]any{"label": "B", "description": "b"}))
	tw(t, "rpc-fallback", "falls back to the dialog walker when ctx.mode is unset and custom() resolves undefined", func(t *testing.T) {
		f := newFakeUI(t)
		f.mode = ""
		f.customNil = true
		f.selectPick = func(o []string) string { return o[1] }
		text, _ := f.run(single)
		eq(t, f.customCalls, 1, "custom calls")
		eq(t, len(f.dialogs), 1, "selects")
		if !strings.Contains(text, `"Which?"="B"`) {
			t.Fatalf("text %q", text)
		}
	})
	tw(t, "rpc-fallback", "stays on the custom() path when ctx.mode is unset and custom() renders (TUI)", func(t *testing.T) {
		f := newFakeUI(t)
		f.mode = ""
		f.customResult = map[string]any{"cancelled": false, "answers": []any{
			map[string]any{"questionIndex": 0, "question": "Which?", "kind": "option", "answer": "A"}}}
		f.selectPick = func([]string) string { return "should-not-be-called" }
		text, _ := f.run(single)
		eq(t, f.customCalls, 1, "custom calls")
		eq(t, len(f.dialogs), 0, "selects")
		if !strings.Contains(text, `"Which?"="A"`) {
			t.Fatalf("text %q", text)
		}
	})
}
