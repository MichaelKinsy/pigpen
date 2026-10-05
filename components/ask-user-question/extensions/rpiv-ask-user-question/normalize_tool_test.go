package ask_user_question_test

import (
	"strings"
	"testing"
)

const fNormalizeTool = "ask-user-question.normalize"

// crParams is the original's CR_PARAMS: bare CRs inside every user-facing string field (#192).
func crParams() map[string]any {
	return askArgs(questionArg("Which\r option\r is best?", "Pick\r", false,
		optArg("Option\rA\r (Recommended)", "choice with\r\r stray CR"),
		map[string]any{"label": "GEMBA\r_LOG\r_FILE", "description": "crlf\r\nline", "preview": "a\r\nb"}))
}

func TestNormalizedTextReachesEveryConsumer(t *testing.T) {
	tskip(t, fNormalizeTool, "renders the TUI overlay with normalized question, header, labels, and descriptions",
		"slice B: renders the interactive questionnaire component, which this revision does not port; the dialog walker's normalized titles are the RPC case below")
	tw(t, fNormalizeTool, "emits the prompt event with normalized text", func(t *testing.T) {
		// The original drives the custom() path; slice A emits the same event ahead of the dialog walker.
		r := startRig(t, rpcOpts(), pick("1. OptionA (Recommended) — choice with stray CR"))
		r.ask(crParams())
		eq(t, at(r.emittedEvents(), 0).Channel, "rpiv:ask-user:prompt", "channel")
		eq(t, at(r.emittedEvents(), 0).Payload, map[string]any{"questions": []any{
			map[string]any{"question": "Which option is best?", "header": "Pick", "multiSelect": false, "options": []any{
				map[string]any{"label": "OptionA (Recommended)", "description": "choice with stray CR", "hasPreview": false},
				map[string]any{"label": "GEMBA_LOG_FILE", "description": "crlf\nline", "hasPreview": true}}},
		}}, "payload")
	})
	tw(t, fNormalizeTool, "shows the RPC host normalized titles and option lines", func(t *testing.T) {
		r := startRig(t, rpcOpts(), pick("1. OptionA (Recommended) — choice with stray CR"))
		text, _ := r.ask(crParams())
		d := r.dialogAt(0)
		eq(t, d.Method, "ui.select", "method")
		if !strings.Contains(d.Title, "[Pick] Which option is best?") {
			t.Fatalf("title %q", d.Title)
		}
		if !strings.Contains(d.Title, "--- 2. GEMBA_LOG_FILE preview ---\na\nb") {
			t.Fatalf("title %q lacks the normalized preview", d.Title)
		}
		eq(t, d.Options, []string{"1. OptionA (Recommended) — choice with stray CR", "2. GEMBA_LOG_FILE — crlf\nline", "3. Type something."}, "options")
		if strings.Contains(d.Title, "\r") || strings.Contains(strings.Join(d.Options, ""), "\r") {
			t.Fatalf("a CR reached the dialog: %q %q", d.Title, d.Options)
		}
		if !strings.Contains(text, `"Which option is best?"="OptionA (Recommended)"`) {
			t.Fatalf("text %q", text)
		}
	})
}
