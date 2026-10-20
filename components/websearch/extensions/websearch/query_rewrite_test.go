package websearch

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

// Twins of query-rewrite.test.mjs and query-cold-abort.test.mjs.

type ctxKey struct{}

func TestUpstream_query_rewrite(t *testing.T) {
	const F = "query-rewrite"
	tw(t, F, "rewriteSearchQuery uses the registered provider runtime", func(t *testing.T) {
		model := scModel("anthropic", "claude-haiku-4-5")
		reg := &fakeSideRegistry{models: []map[string]any{model}}
		reg.completeFn = func(context.Context, map[string]any, map[string]any, CompleteOptions) (map[string]any, error) {
			return stopText("HTTP status code semantics RFC 9110"), nil
		}
		// options.signal is the caller's signal: the model call runs under the caller's own context.
		ctx, cancel := context.WithCancel(context.WithValue(bg(), ctxKey{}, "caller"))
		defer cancel()
		result, err := RewriteSearchQuery(ctx, "http status codes", &fakeSide{reg: reg})
		noErr(t, err)
		call := reg.lastCall()
		if result != "HTTP status code semantics RFC 9110" || call.model["id"] != "claude-haiku-4-5" || call.ctx != ctx {
			t.Fatalf("%q %v %v", result, call.model, call.ctx == ctx)
		}
		if !regexpMust(`Query: http status codes`).MatchString(requestPrompt(call.request)) {
			t.Fatal(requestPrompt(call.request))
		}
	})

	tw(t, F, "rewriteSearchQuery skips models outside the host's resolved scope", func(t *testing.T) {
		haiku := scModel("anthropic", "claude-haiku-4-5")
		flash := scModel("google", "gemini-3.6-flash")
		var used []string
		reg := &fakeSideRegistry{models: []map[string]any{haiku, flash}}
		reg.completeFn = func(_ context.Context, model, _ map[string]any, _ CompleteOptions) (map[string]any, error) {
			used = append(used, modelLabel(model))
			return stopText("rewritten"), nil
		}
		result, err := RewriteSearchQuery(bg(), "http status codes", &fakeSide{reg: reg, scoped: []map[string]any{flash}})
		noErr(t, err)
		if result != "rewritten" || !reflect.DeepEqual(used, []string{"google/gemini-3.6-flash"}) {
			t.Fatalf("%q %v", result, used)
		}
	})
}

// Behaviour past the upstream titles (query-rewrite.ts:6-53): the candidate order, a model
// without credentials, and the failures of the completion.
func TestRewriteSearchQueryFallbacks(t *testing.T) {
	haiku := scModel("anthropic", "claude-haiku-4-5")
	mini := scModel("openai", "gpt-5-mini")
	reg := &fakeSideRegistry{models: []map[string]any{haiku, mini}}
	reg.authFn = func(model map[string]any) (map[string]any, error) {
		if model["provider"] == "anthropic" {
			return map[string]any{"ok": false}, nil
		}
		return map[string]any{"ok": true, "apiKey": "k"}, nil
	}
	reg.completeFn = func(_ context.Context, model, _ map[string]any, _ CompleteOptions) (map[string]any, error) {
		return stopText("  a better query \n"), nil
	}
	result, err := RewriteSearchQuery(bg(), "q", &fakeSide{reg: reg})
	noErr(t, err)
	if result != "a better query" || modelLabel(reg.lastCall().model) != "openai/gpt-5-mini" {
		t.Fatalf("%q %v", result, reg.lastCall().model)
	}

	// Text parts are joined without a separator.
	reg.completeFn = func(context.Context, map[string]any, map[string]any, CompleteOptions) (map[string]any, error) {
		return map[string]any{"stopReason": "stop", "content": []any{map[string]any{"type": "text", "text": "ab"}, map[string]any{"type": "text", "text": "cd"}}}, nil
	}
	result, err = RewriteSearchQuery(bg(), "q", &fakeSide{reg: reg})
	noErr(t, err)
	if result != "abcd" {
		t.Fatal(result)
	}

	_, err = RewriteSearchQuery(bg(), "q", &fakeSide{reg: &fakeSideRegistry{}})
	wantErr(t, err, `^No enabled model available: anthropic/claude-haiku-4-5, google/gemini-3\.6-flash, openai/gpt-5-mini$`)

	for _, c := range []struct {
		name    string
		message map[string]any
		want    string
	}{
		{"aborted", map[string]any{"stopReason": "aborted"}, "^Aborted$"},
		{"empty", map[string]any{"stopReason": "stop", "content": []any{map[string]any{"type": "thinking", "text": "hm"}}}, "^Rewrite returned empty response$"},
	} {
		reg.completeFn = func(context.Context, map[string]any, map[string]any, CompleteOptions) (map[string]any, error) {
			return c.message, nil
		}
		_, err := RewriteSearchQuery(bg(), "q", &fakeSide{reg: reg})
		t.Run(c.name, func(t *testing.T) { wantErr(t, err, c.want) })
	}
}

// The upstream child process gates the lazy import of pi-ai's compat module and cancels while it
// is pending. The Go port has no module to load, but the host call that precedes the completion
// (the credential lookup) cannot be cancelled either: the same invariant holds there. The public
// operation reports "Aborted" while that call is still pending, and a call that settles after the
// cancellation, successfully or not, never starts a completion.
func TestUpstream_query_cold_abort(t *testing.T) {
	const F = "query-cold-abort"
	tw(t, F, "${kind} cancels before cold compat import ${outcome}s without late completion", func(t *testing.T) {
		for _, kind := range []string{"page", "rewrite"} {
			for _, outcome := range []string{"resolve", "reject"} {
				t.Run(kind+" "+outcome, func(t *testing.T) {
					pageQuerySetup(t)
					model := scModel("anthropic", "claude-haiku-4-5")
					gate := make(chan struct{})
					entered := make(chan struct{})
					settled := make(chan struct{})
					reg := &fakeSideRegistry{models: []map[string]any{model}}
					reg.authFn = func(map[string]any) (map[string]any, error) {
						close(entered)
						<-gate
						defer close(settled)
						if outcome == "reject" {
							return nil, errors.New("late import failure")
						}
						return map[string]any{"ok": true, "apiKey": "test"}, nil
					}
					side := &fakeSide{model: model, reg: reg}
					ctx, cancel := context.WithCancel(bg())
					defer cancel()
					done := make(chan error, 1)
					go func() {
						var err error
						if kind == "page" {
							_, err = AnswerFromPage(ctx, PageAnswerInput{Question: "Q", PageText: "P", SourceURL: "https://example.com"}, side)
						} else {
							_, err = RewriteSearchQuery(ctx, "Q", side)
						}
						done <- err
					}()
					await(t, entered, "the credential lookup")
					cancel()
					// The credential lookup stays gated until the public operation acknowledges the cancellation.
					select {
					case err := <-done:
						wantErr(t, err, "^Aborted$")
					case <-time.After(5 * time.Second):
						t.Fatal("the operation did not report the cancellation while the lookup was pending")
					}
					close(gate)
					await(t, settled, "the credential lookup to settle")
					time.Sleep(20 * time.Millisecond)
					if n := reg.callCount(); n != 0 {
						t.Fatalf("%d completions started after the cancellation", n)
					}
				})
			}
		}
	})
}
