package schedule_prompt

import (
	"context"
	"errors"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Cases of the port beyond the original's tests: what the runner does with the session's events, errors and abort signal.

func ame(delta, typ string) map[string]any {
	return map[string]any{"type": "message_update", "assistantMessageEvent": map[string]any{"type": typ, "delta": delta}}
}

func TestRunnerSessionOptionsAndCleanup(t *testing.T) {
	h := newSubHost()
	res := runSubagentOnce(h, "p", "gpt-4o", nil, subagentOptions{})
	if !res.OK {
		t.Fatalf("%+v", res)
	}
	o := h.options[0]
	if o.Cwd != "/tmp" || !o.InMemory || !o.Resources.NoPromptTemplates || !o.Resources.NoThemes || o.Model.(map[string]any)["id"] != "gpt-4o" {
		t.Fatalf("options = %+v", o)
	}
	if h.session.disposed != 1 || len(h.session.prompted) != 1 || h.session.prompted[0] != "p" {
		t.Fatalf("session = %+v", h.session)
	}
}

func TestRunnerUnknownModel(t *testing.T) {
	h := newSubHost()
	res := runSubagentOnce(h, "p", "nope", nil, subagentOptions{})
	want := "Unknown model 'nope'. Available: anthropic/claude-opus-4-6, anthropic/claude-sonnet-4-6, anthropic/claude-haiku-4-5-20251001, openai/gpt-4o, google/gemini-2.5-pro"
	if res.OK || res.Error != want || len(h.options) != 0 {
		t.Fatalf("%+v", res)
	}
}

func TestRunnerAnswerFromEvents(t *testing.T) {
	h := newSubHost()
	h.session.script = func(emit func(map[string]any)) {
		emit(map[string]any{"type": "message_start"})
		emit(ame("dropped", "text_delta"))
		emit(map[string]any{"type": "message_start"})
		emit(ame("  B", "text_delta"))
		emit(ame("secret", "thinking_delta"))
		emit(ame("C \n", "text_delta"))
	}
	// the buffer is "  BC \n" after the second message_start, trimmed
	if res := runSubagentOnce(h, "p", "gpt-4o", nil, subagentOptions{}); !res.OK || res.Text != "BC" {
		t.Fatalf("%+v", res)
	}
}

func TestRunnerFallsBackToTheMessages(t *testing.T) {
	h := newSubHost()
	h.session.messages = []map[string]any{{"role": "user", "content": "q"}, {"role": "assistant", "content": []any{map[string]any{"type": "text", "text": " from messages "}}}}
	if res := runSubagentOnce(h, "p", "gpt-4o", nil, subagentOptions{}); !res.OK || res.Text != "from messages" {
		t.Fatalf("%+v", res)
	}
	h2 := newSubHost()
	if res := runSubagentOnce(h2, "p", "gpt-4o", nil, subagentOptions{}); !res.OK || res.Text != "" {
		t.Fatalf("%+v", res)
	}
}

func TestRunnerErrors(t *testing.T) {
	h := newSubHost()
	h.createErr = errors.New("no credentials")
	if res := runSubagentOnce(h, "p", "gpt-4o", nil, subagentOptions{}); res.OK || res.Error != "no credentials" {
		t.Fatalf("%+v", res)
	}
	h = newSubHost()
	h.session.promptErr = errors.New("rejected")
	if res := runSubagentOnce(h, "p", "gpt-4o", nil, subagentOptions{}); res.OK || res.Error != "rejected" || h.session.disposed != 1 || h.session.listener != nil {
		t.Fatalf("%+v %+v", res, h.session)
	}
}

type ctxProbe struct {
	fakeChild
	seen context.Context
}

func (p *ctxProbe) Prompt(ctx context.Context, text string, o ...sdk.PromptOptions) error {
	p.seen = ctx
	return p.fakeChild.Prompt(ctx, text, o...)
}

type probeHost struct {
	*fakeSubHost
	probe *ctxProbe
}

func (h probeHost) CreateAgentSession(o sdk.CreateAgentSessionOptions) (childSession, error) {
	h.fakeSubHost.options = append(h.fakeSubHost.options, o)
	return h.probe, nil
}

func TestRunnerAbortSignal(t *testing.T) {
	// A live signal is the prompt's context: cancelling it aborts the run.
	live, cancel := context.WithCancel(context.Background())
	p := &ctxProbe{}
	runSubagentOnce(probeHost{newSubHost(), p}, "p", "gpt-4o", live, subagentOptions{})
	if p.seen != live || p.aborted != 0 {
		t.Fatalf("seen %v aborted %d", p.seen, p.aborted)
	}
	// An already aborted signal aborts the idle session (nothing) and the run still happens, as in the original.
	cancel()
	p = &ctxProbe{}
	res := runSubagentOnce(probeHost{newSubHost(), p}, "p", "gpt-4o", live, subagentOptions{})
	if !res.OK || p.aborted != 1 || p.seen == nil || p.seen.Err() != nil || len(p.prompted) != 1 {
		t.Fatalf("%+v seen %v aborted %d", res, p.seen, p.aborted)
	}
}

func TestEnabledAndNameLists(t *testing.T) {
	for v, want := range map[string]bool{"true": true, "false": false} {
		if got := isEnabled(v == "true"); got != want {
			t.Errorf("isEnabled(%s) = %v", v, got)
		}
	}
	if isEnabled([]any{}) || !isEnabled([]any{"a"}) || isEnabled(nil) || isEnabled("yes") {
		t.Error("isEnabled on lists")
	}
	if nameList([]any{}) != nil || nameList(true) != nil || len(nameList([]any{"a", float64(2)})) != 2 {
		t.Error("nameList")
	}
}
