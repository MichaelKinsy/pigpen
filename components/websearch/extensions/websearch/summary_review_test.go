package websearch

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Twins of summary-provider-routing, summary-opencode-headers and summary-cold-deadline.

func TestUpstream_summary_provider_routing(t *testing.T) {
	const F = "summary-provider-routing"
	tw(t, F, "summary generation preserves registered provider behavior", func(t *testing.T) {
		model := map[string]any{"provider": "custom-gateway", "id": "upstream/test-model", "api": "custom-gateway-api", "baseUrl": "https://gateway.example.test"}
		reg := &fakeSideRegistry{models: []map[string]any{model}, findFn: func(string, string) map[string]any { return model }}
		reg.completeFn = func(_ context.Context, _, _ map[string]any, o CompleteOptions) (map[string]any, error) {
			if o.APIKey != "" || o.Headers != nil {
				t.Errorf("the registry path must not send an api key or headers: %+v", o)
			}
			return stopText("Summary from the registered gateway provider."), nil
		}
		draft, err := GenerateSummaryDraft(bg(), []QueryResultData{{
			Query: "test query", Answer: "Provider search answer.",
			Results: []SearchResult{{Title: "Source", URL: "https://example.test/source"}}, Provider: "search-provider",
		}}, &fakeSide{reg: reg}, SummaryOptions{ModelOverride: "custom-gateway/upstream/test-model", DeadlineMs: 1000})
		noErr(t, err)
		if draft.Summary != "Summary from the registered gateway provider." {
			t.Fatalf("%+v", draft)
		}
	})
}

func openCodeSummarySide(models ...map[string]any) (*fakeSide, *fakeSideRegistry) {
	reg := &fakeSideRegistry{models: models}
	reg.authFn = func(map[string]any) (map[string]any, error) {
		return map[string]any{"ok": true, "apiKey": "test-key", "headers": map[string]any{"x-existing": "kept"}}, nil
	}
	return &fakeSide{reg: reg, session: "summary-session-385"}, reg
}

func openCodeModel() map[string]any {
	return map[string]any{"provider": "opencode-go", "id": "deepseek-v4-flash", "api": "openai-completions", "baseUrl": "https://opencode.ai/zen/go/v1"}
}

func TestUpstream_summary_opencode_headers(t *testing.T) {
	const F = "summary-opencode-headers"
	attribution := map[string]any{"x-existing": "kept", "x-opencode-session": "summary-session-385", "x-opencode-client": "pi"}

	tw(t, F, "registry summary attributes the configured OpenCode candidate instead of falling through", func(t *testing.T) {
		configured := openCodeModel()
		fallback := map[string]any{"provider": "anthropic", "id": "claude-haiku-4-5", "api": "anthropic-messages"}
		side, reg := openCodeSummarySide(configured, fallback)
		var calls []map[string]any
		reg.completeFn = func(_ context.Context, model, _ map[string]any, o CompleteOptions) (map[string]any, error) {
			calls = append(calls, model)
			if !sameModel(model, configured) {
				t.Error("configured candidate silently fell through")
			}
			if o.TransformHeaders == nil {
				t.Error("no header transform")
				return stopText("OpenCode registry summary"), nil
			}
			want := map[string]any{"accept": "application/json", "x-opencode-session": "summary-session-385", "x-opencode-client": "pi"}
			if got := o.TransformHeaders(map[string]any{"accept": "application/json"}); !reflect.DeepEqual(got, want) {
				t.Errorf("headers %v", got)
			}
			return stopText("OpenCode registry summary"), nil
		}
		draft, err := GenerateSummaryDraft(bg(), summaryResults, side, SummaryOptions{ModelOverride: "opencode-go/deepseek-v4-flash"})
		noErr(t, err)
		if draft.Summary != "OpenCode registry summary" || *draft.Meta.Model != "opencode-go/deepseek-v4-flash" || draft.Meta.FallbackUsed || len(calls) != 1 {
			t.Fatalf("%+v %d", draft, len(calls))
		}
	})

	tw(t, F, "direct summary completion merges OpenCode attribution with provider headers", func(t *testing.T) {
		side, _ := openCodeSummarySide(openCodeModel())
		calls := 0
		draft, err := GenerateSummaryDraft(bg(), summaryResults, side, SummaryOptions{
			ModelOverride: "opencode-go/deepseek-v4-flash",
			Complete: func(_ context.Context, _, _ map[string]any, o CompleteOptions) (map[string]any, error) {
				calls++
				if !reflect.DeepEqual(o.Headers, attribution) || o.APIKey != "test-key" {
					t.Errorf("%+v", o)
				}
				return stopText("OpenCode direct summary"), nil
			},
		})
		noErr(t, err)
		if *draft.Meta.Model != "opencode-go/deepseek-v4-flash" || calls != 1 {
			t.Fatalf("%+v %d", draft.Meta, calls)
		}
	})

	tw(t, F, "non-OpenCode registry summaries do not install a header transform", func(t *testing.T) {
		model := map[string]any{"provider": "test", "id": "summary-model", "api": "custom"}
		side, reg := openCodeSummarySide(model)
		reg.completeFn = func(_ context.Context, _, _ map[string]any, o CompleteOptions) (map[string]any, error) {
			if o.TransformHeaders != nil {
				t.Error("a header transform was installed")
			}
			return stopText("Unchanged summary"), nil
		}
		draft, err := GenerateSummaryDraft(bg(), summaryResults, side, SummaryOptions{ModelOverride: "test/summary-model"})
		noErr(t, err)
		if *draft.Meta.Model != "test/summary-model" {
			t.Fatalf("%+v", draft.Meta)
		}
	})

	tw(t, F, "non-OpenCode direct summaries preserve provider headers", func(t *testing.T) {
		model := map[string]any{"provider": "test", "id": "summary-model", "api": "custom"}
		side, _ := openCodeSummarySide(model)
		providerHeaders := map[string]any{"x-existing": "kept"}
		side.reg.authFn = func(map[string]any) (map[string]any, error) {
			return map[string]any{"ok": true, "apiKey": "test-key", "headers": providerHeaders}, nil
		}
		_, err := GenerateSummaryDraft(bg(), summaryResults, side, SummaryOptions{
			ModelOverride: "test/summary-model",
			Complete: func(_ context.Context, _, _ map[string]any, o CompleteOptions) (map[string]any, error) {
				if reflect.ValueOf(o.Headers).Pointer() != reflect.ValueOf(providerHeaders).Pointer() {
					t.Errorf("the provider headers were copied or changed: %v", o.Headers)
				}
				return stopText("Unchanged direct summary"), nil
			},
		})
		noErr(t, err)
	})

	// Adapted: the upstream case runs pi-ai's completeSimple with the resolved api key and headers.
	// The port has one completion path, the registry's: a thinking request installs the same
	// attribution transform and carries the level.
	tw(t, F, "thinking summary completion sends OpenCode attribution headers", func(t *testing.T) {
		model := map[string]any{"provider": "opencode-go", "id": "deepseek-v4-flash", "api": "openai-completions", "reasoning": true}
		side, reg := openCodeSummarySide(model)
		side.session = "thinking-session-385"
		calls := 0
		reg.completeFn = func(_ context.Context, _, _ map[string]any, o CompleteOptions) (map[string]any, error) {
			calls++
			want := map[string]any{"x-existing": "kept", "x-opencode-session": "thinking-session-385", "x-opencode-client": "pi"}
			if o.TransformHeaders == nil || !reflect.DeepEqual(o.TransformHeaders(map[string]any{"x-existing": "kept"}), want) || o.Reasoning != "high" || o.ReasoningEffort != "high" {
				t.Errorf("%+v", o)
			}
			return stopText("Thinking summary"), nil
		}
		draft, err := GenerateSummaryDraft(bg(), summaryResults, side, SummaryOptions{ModelOverride: "opencode-go/deepseek-v4-flash:high"})
		noErr(t, err)
		if draft.Summary != "Thinking summary" || *draft.Meta.Model != "opencode-go/deepseek-v4-flash" || calls != 1 {
			t.Fatalf("%+v %d", draft, calls)
		}
	})
}

// fakeClock is Date under `mock.timers`: set moves it forward without waiting.
type fakeClock struct {
	base   time.Time
	offset atomic.Int64
}

func (c *fakeClock) now() time.Time          { return c.base.Add(time.Duration(c.offset.Load())) }
func (c *fakeClock) advance(d time.Duration) { c.offset.Add(int64(d)) }

func TestUpstream_summary_cold_deadline(t *testing.T) {
	const F = "summary-cold-deadline"

	tw(t, F, "pre-aborted summary rejects without creating an unhandled abort contender", func(t *testing.T) {
		reg := &fakeSideRegistry{}
		ctx, cancel := context.WithCancel(bg())
		cancel()
		_, err := GenerateSummaryDraft(ctx, nil, &fakeSide{reg: reg}, SummaryOptions{})
		wantErr(t, err, "Aborted")
		if reg.callCount() != 0 {
			t.Fatal("the registry was used")
		}
		// A context without a registry is reported before the cancellation, as upstream.
		_, err = GenerateSummaryDraft(ctx, nil, nil, SummaryOptions{})
		wantErr(t, err, "Summary generation context unavailable")
	})

	// Upstream gates the lazy import of pi-ai on a cold first use and expires the deadline or aborts
	// while it is pending. The Go port loads nothing lazily; the first-use host call that precedes
	// every completion (the model and credential lookup) cannot be cancelled either, so it is gated
	// the same way. The clock is the deadline's `Date.now`.
	tw(t, F, "summary ${mode}: ${scenario} respects cancellation on cold first use", func(t *testing.T) {
		for _, mode := range []string{"compat", "thinking"} {
			for _, scenario := range []string{"pending-import", "expired-import", "expired-completion", "expired-throw", "expired-rejection", "caller-abort"} {
				t.Run(mode+" "+scenario, func(t *testing.T) {
					clock := &fakeClock{base: time.Now()}
					previous := summaryClock
					summaryClock = clock.now
					t.Cleanup(func() { summaryClock = previous })

					model := map[string]any{"provider": "test", "id": "model", "reasoning": true}
					gate := make(chan struct{})
					var releaseOnce sync.Once
					release := func() { releaseOnce.Do(func() { close(gate) }) }
					defer release()
					entered := make(chan struct{})
					reg := &fakeSideRegistry{models: []map[string]any{model}}
					reg.authFn = func(map[string]any) (map[string]any, error) {
						close(entered)
						<-gate
						return map[string]any{"ok": true, "apiKey": "test-key"}, nil
					}
					var calls atomic.Int32
					reg.completeFn = func(ctx context.Context, _, _ map[string]any, _ CompleteOptions) (map[string]any, error) {
						calls.Add(1)
						if ctx.Err() != nil {
							t.Error("an expired provider was invoked")
						}
						fail := func() (map[string]any, error) {
							clock.advance(101 * time.Millisecond)
							return nil, errors.New("late provider failure")
						}
						switch scenario {
						case "expired-throw":
							return fail()
						case "expired-rejection":
							time.Sleep(time.Millisecond)
							return fail()
						case "expired-completion":
							clock.advance(101 * time.Millisecond)
						}
						return stopText("Late answer"), nil
					}
					override := "test/model"
					if mode == "thinking" {
						override += ":high"
					}
					ctx, cancel := context.WithCancel(bg())
					defer cancel()
					type outcome struct {
						draft SummaryDraft
						err   error
					}
					done := make(chan outcome, 1)
					go func() {
						draft, err := GenerateSummaryDraft(ctx, summaryResults[:0], &fakeSide{reg: reg}, SummaryOptions{ModelOverride: override, DeadlineMs: 100})
						done <- outcome{draft, err}
					}()
					await(t, entered, "the credential lookup")
					switch scenario {
					case "caller-abort":
						cancel()
					case "pending-import":
						// The lookup stays gated: the deadline must settle the call without waiting for it.
					case "expired-import":
						clock.advance(101 * time.Millisecond)
						release()
					default:
						release()
					}
					var got outcome
					select {
					case got = <-done:
					case <-time.After(5 * time.Second):
						t.Fatal("the generation did not settle")
					}
					if scenario == "caller-abort" {
						wantErr(t, got.err, "Aborted")
					} else {
						noErr(t, got.err)
						meta := got.draft.Meta
						if !meta.FallbackUsed || meta.FallbackReason != "summary-generation-timeout" || got.draft.Summary == "Late answer" {
							t.Fatalf("%+v", got.draft)
						}
						if scenario != "pending-import" && meta.DurationMs < 100 {
							t.Fatalf("duration %d", meta.DurationMs)
						}
					}
					release()
					time.Sleep(20 * time.Millisecond)
					want := int32(0)
					if scenario == "expired-completion" || scenario == "expired-throw" || scenario == "expired-rejection" {
						want = 1
					}
					if got := calls.Load(); got != want {
						t.Fatalf("%d completions, want %d (a cancelled or expired call must not start one)", got, want)
					}
				})
			}
		}
	})
}
