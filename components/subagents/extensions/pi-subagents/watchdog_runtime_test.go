package pi_subagents

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Twins of test/unit/watchdog-runtime.test.ts. A promise the original awaits is a goroutine here (wdAsync); `await tick()` is
// wdEventually.

func wdRtConfig(mod func(*watchdogConfig)) *watchdogConfig {
	c := defaultWatchdogConfig()
	c.Enabled = true
	c.Main.Enabled = true
	c.Lsp.Enabled = false
	if mod != nil {
		mod(c)
	}
	return c
}

func wdCfgResult(c *watchdogConfig) watchdogSettingsResult {
	return watchdogSettingsResult{OK: true, Config: c}
}

func wdRtWarning() wdWarning {
	return wdWarning{Severity: "concern", Importance: "high", Summary: "Runtime concern", Evidence: "The runtime test emitted a concern.",
		RecommendedAction: "Review the displayed warning before accepting the turn.", Source: "main"}
}

// wdConfigBox is a config the test swaps while the runtime reads it.
type wdConfigBox struct {
	p atomic.Pointer[watchdogConfig]
}

func newWdConfigBox(c *watchdogConfig) *wdConfigBox { b := &wdConfigBox{}; b.p.Store(c); return b }

func (b *wdConfigBox) resolve(string, *jsObject) watchdogSettingsResult {
	return wdCfgResult(b.p.Load())
}

func wdRepoForRuntime(t *testing.T) string {
	t.Helper()
	repo := tmp(t)
	wdGit(t, repo, "init")
	wdGit(t, repo, "config", "user.email", "watchdog@example.com")
	wdGit(t, repo, "config", "user.name", "Watchdog Tests")
	wdGit(t, repo, "config", "commit.gpgsign", "false")
	writeFile(t, filepath.Join(repo, "src", "file.ts"), "export const value = 1;\n")
	wdGit(t, repo, "add", "-A")
	wdGit(t, repo, "commit", "-m", "initial")
	return repo
}

func wdActivityTurn(t *testing.T, name, argsJSON, text string, resultCallID ...string) any {
	t.Helper()
	id := "activity-call"
	resultID := id
	if len(resultCallID) > 0 {
		resultID = resultCallID[0]
	}
	return wdObj(t, `{"type":"turn_end","message":{"role":"assistant","content":[{"type":"toolCall","id":"`+id+`","name":"`+name+`","arguments":`+argsJSON+`}]},
		"toolResults":[{"role":"toolResult","toolCallId":"`+resultID+`","toolName":"`+name+`","content":[{"type":"text","text":`+marshalJSON(text, "")+`}],"isError":false}]}`)
}

func wdPrompt(t *testing.T, prompt string) any {
	t.Helper()
	return wdObj(t, `{"prompt":`+marshalJSON(prompt, "")+`}`)
}

// wdRequests records the requests a review function saw.
type wdRequests struct {
	mu   sync.Mutex
	list []watchdogReviewRequest
}

func (r *wdRequests) add(req watchdogReviewRequest) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.list = append(r.list, req)
}
func (r *wdRequests) n() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.list) }
func (r *wdRequests) at(i int) watchdogReviewRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	if i < 0 {
		i += len(r.list)
	}
	return r.list[i]
}

// wdSink records what the runtime displayed.
type wdSink struct {
	mu       sync.Mutex
	warnings []wdWarning
	options  []*wdSendOptions
	entries  []wdWarning
	messages []string
}

func (s *wdSink) warning(w wdWarning, o *wdSendOptions) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.warnings, s.options = append(s.warnings, w), append(s.options, o)
}
func (s *wdSink) entry(w wdWarning) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, w)
}
func (s *wdSink) message(c string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, c)
}
func (s *wdSink) nWarnings() int   { s.mu.Lock(); defer s.mu.Unlock(); return len(s.warnings) }
func (s *wdSink) nEntries() int    { s.mu.Lock(); defer s.mu.Unlock(); return len(s.entries) }
func (s *wdSink) nMessages() int   { s.mu.Lock(); defer s.mu.Unlock(); return len(s.messages) }
func (s *wdSink) msg(i int) string { s.mu.Lock(); defer s.mu.Unlock(); return s.messages[i] }

func (s *wdSink) options0() watchdogRuntimeOptions {
	return watchdogRuntimeOptions{DisplayWarning: s.warning, DisplayUserWarning: s.entry}
}

func TestWatchdogRuntimeTwins(t *testing.T) {
	const f = "watchdog-runtime"
	ctx := wdRunCtx{Cwd: "/tmp/project"}
	withTempHome(t)

	tw(t, f, "invalidates an admitted LSP pre-pass on user input, model selection or configured model change", func(t *testing.T) {
		for _, edge := range []string{"input", "model", "config"} {
			box := newWdConfigBox(wdRtConfig(func(c *watchdogConfig) { c.Clarification, c.Lsp = true, defaultWatchdogConfig().Lsp }))
			var key atomic.Value
			key.Store("baseline")
			ready := make(chan struct{})
			finish := make(chan wdLspResult, 1)
			var signal atomic.Value
			var reviews atomic.Int32
			sink := &wdSink{}
			rt := newWatchdogRuntime(watchdogRuntimeOptions{Cwd: ctx.Cwd, ResolveConfig: box.resolve, ReviewChangesOnly: true,
				RepoChangeSignature: func(string) *watchdogRepoChangeSignature {
					return &watchdogRepoChangeSignature{Root: ctx.Cwd, Key: key.Load().(string), ChangedPaths: []string{"file.ts"}}
				},
				LspDiagnostics: func(req watchdogLspRequest) wdLspResult {
					signal.Store(req.Signal)
					close(ready)
					return <-finish
				},
				DisplayClarification: sink.message,
				Review: func(watchdogReviewRequest) (*watchdogReviewResult, error) {
					reviews.Add(1)
					return &watchdogReviewResult{Clarification: &watchdogClarification{Question: "Old scope?", Evidence: "Old boundary"}}, nil
				}})
			rt.handleBeforeAgentStart(wdPrompt(t, "Original task"), ctx)
			key.Store("edited")
			boundary := wdAsync(func() { rt.handleAgentEnd(nil, ctx) })
			wdAwait(t, "lsp start "+edge, ready)
			switch edge {
			case "input":
				rt.handleUserInput()
			case "model":
				rt.handleModelChange()
			default:
				c := *box.p.Load()
				c.Main.Model = "mock/new-model"
				box.p.Store(&c)
				rt.refreshConfig("")
			}
			wdEq(t, wdClosed(signal.Load().(<-chan struct{})), true, edge+": signal aborted")
			finish <- wdLspResult{Status: "ok", CheckedPaths: []string{"file.ts"}, SkippedPaths: []string{}, Diagnostics: []wdLspDiagnostic{}}
			wdAwait(t, "boundary "+edge, boundary)
			wdEq(t, int(reviews.Load()), 0, edge+": reviews")
			wdEq(t, sink.nMessages(), 0, edge+": notices")
			rt.dispose()
		}
	})

	tw(t, f, "reviews delivered orchestration outcomes without edits once per prompt and retains evidence through a side ask", func(t *testing.T) {
		requests := &wdRequests{}
		rt := newWatchdogRuntime(watchdogRuntimeOptions{Cwd: ctx.Cwd, ResolveConfig: newWdConfigBox(wdRtConfig(func(c *watchdogConfig) { c.Clarification = true })).resolve, ReviewChangesOnly: true,
			RepoChangeSignature: func(string) *watchdogRepoChangeSignature {
				return &watchdogRepoChangeSignature{Root: ctx.Cwd, Key: "unchanged", ChangedPaths: []string{}}
			},
			DisplayClarification: func(string) {},
			Review: func(r watchdogReviewRequest) (*watchdogReviewResult, error) {
				requests.add(r)
				if requests.n() == 2 {
					return &watchdogReviewResult{Warnings: []wdWarning{wdRtWarning()}}, nil
				}
				return &watchdogReviewResult{}, nil
			}})
		defer rt.dispose()
		rt.handleBeforeAgentStart(wdPrompt(t, "Finish the original implementation and CI gate"), ctx)
		rt.handleTurnEnd(wdActivityTurn(t, "subagent", `{"agent":"worker","task":"Implement original objective"}`, "Implementation remains active; explicitly held pending CI"), ctx, false)
		rt.handleAgentEnd(nil, ctx)
		wdEq(t, requests.n(), 1, "first review")
		wdMatch(t, requests.at(0).Delta, `explicitly held pending CI`)
		wdMatch(t, requests.at(0).Delta, `waits/holds are not evidence of neglect`)
		wdEq(t, rt.getSnapshot("").LastWarning == nil, true, "a pending/held outcome is evidence, not an automatic finding")
		rt.handleTurnEnd(wdActivityTurn(t, "bg_wait", `{"id":"ci-gate"}`, "CI gate completed successfully"), ctx, false)
		rt.handleAgentEnd(nil, ctx)
		wdEq(t, requests.n(), 1, "one additional activity review per prompt")
		rt.handleBeforeAgentStart(wdPrompt(t, "Side question: explain this acronym"), ctx)
		rt.handleAgentEnd(nil, ctx)
		wdEq(t, requests.n(), 2, "unreviewed delivered completion survives skipped boundary and new prompt")
		for _, text := range []string{"original implementation", "Side question", "CI gate completed successfully", "Implementation remains active", "Side questions are additive"} {
			wdTrue(t, strings.Contains(requests.at(1).Delta, text), "delta carries "+text)
		}
		rt.handleTurnEnd(wdActivityTurn(t, "subagent", `{"action":"status","id":"ci-gate"}`, "Warning continuation status"), ctx, false)
		rt.handleAgentEnd(nil, ctx)
		rt.handleBeforeAgentStart(wdPrompt(t, "Continue"), ctx)
		rt.handleAgentEnd(nil, ctx)
		wdEq(t, requests.n(), 2, "warning continuations and side prompts alone cannot create fresh activity")
		rt.reset(wdResetOptions{clearScope: true})
		rt.handleBeforeAgentStart(wdPrompt(t, "New scope"), ctx)
		rt.handleTurnEnd(wdActivityTurn(t, "subagent_supervisor", `{"action":"pending"}`, "No pending requests"), ctx, false)
		rt.handleAgentEnd(nil, ctx)
		wdNoMatch(t, requests.at(-1).Delta, `CI gate completed successfully`)
	})

	tw(t, f, "bounds activity reviews and excludes unmatched tools and watchdog status calls", func(t *testing.T) {
		requests := &wdRequests{}
		rt := newWatchdogRuntime(watchdogRuntimeOptions{Cwd: ctx.Cwd, ResolveConfig: newWdConfigBox(wdRtConfig(func(c *watchdogConfig) { c.Clarification = true })).resolve, ReviewChangesOnly: true,
			RepoChangeSignature:  func(string) *watchdogRepoChangeSignature { return nil },
			DisplayClarification: func(string) {},
			Review: func(r watchdogReviewRequest) (*watchdogReviewResult, error) {
				requests.add(r)
				if r.AllowClarification {
					return &watchdogReviewResult{Clarification: &watchdogClarification{Question: "Is the gate held?", Evidence: "Delivered gate status is unclear"}}, nil
				}
				return &watchdogReviewResult{}, nil
			}})
		defer rt.dispose()
		rt.handleBeforeAgentStart(wdPrompt(t, "Finish task"), ctx)
		rt.handleTurnEnd(wdActivityTurn(t, "subagent", `{"action":"status"}`, "unmatched", "other"), ctx, false)
		rt.handleTurnEnd(wdActivityTurn(t, "bash", `{"command":"echo subagent"}`, "not orchestration evidence"), ctx, false)
		rt.handleAgentEnd(nil, ctx)
		wdEq(t, requests.n(), 0, "no evidence")
		rt.handleTurnEnd(wdActivityTurn(t, "subagent", `{"action":"status","id":"gate"}`, "Gate state unclear"), ctx, false)
		rt.handleAgentEnd(nil, ctx)
		rt.handleTurnEnd(wdActivityTurn(t, "subagent", `{"action":"watchdog.status"}`, "status"), ctx, false)
		rt.handleAgentEnd(nil, ctx)
		rt.handleBeforeAgentStart(wdPrompt(t, "Next task"), ctx)
		rt.handleAgentEnd(nil, ctx)
		wdEq(t, requests.n(), 1, "question continuation and watchdog status did not create activity")
		rt.handleTurnEnd(wdActivityTurn(t, "bg_wait", `{}`, "New unclear outcome "+strings.Repeat("x", 40_000)), ctx, false)
		rt.handleAgentEnd(nil, ctx)
		wdTrue(t, wdLen(requests.at(-1).Delta) <= 24_000, "activity and delta share the input cap")
		rt.handleTurnEnd(wdActivityTurn(t, "bg_wait", `{}`, "Gate changed again"), ctx, false)
		rt.handleAgentEnd(nil, ctx)
		wdEq(t, requests.n(), 2, "one additional activity review per prompt")
	})

	tw(t, f, "allows a bounded question for observed non-Git edits without an exchange to verify", func(t *testing.T) {
		sink := &wdSink{}
		rt := newWatchdogRuntime(watchdogRuntimeOptions{Cwd: ctx.Cwd, ResolveConfig: newWdConfigBox(wdRtConfig(func(c *watchdogConfig) { c.Clarification = true })).resolve, ReviewChangesOnly: true,
			RepoChangeSignature:  func(string) *watchdogRepoChangeSignature { return nil },
			DisplayClarification: sink.message,
			Review: func(r watchdogReviewRequest) (*watchdogReviewResult, error) {
				if r.AllowClarification {
					return &watchdogReviewResult{Clarification: &watchdogClarification{Question: "Which scope?", Evidence: "Observed edit"}}, nil
				}
				return &watchdogReviewResult{}, nil
			}})
		defer rt.dispose()
		rt.handleBeforeAgentStart(wdPrompt(t, "Update the file"), ctx)
		rt.handleTurnEnd(wdActivityTurn(t, "edit", `{"path":"file.ts"}`, "Edited file"), ctx, false)
		rt.handleAgentEnd(nil, ctx)
		rt.handleAgentEnd(nil, ctx)
		wdEq(t, sink.nMessages(), 1, "ordinary fallback reviews cannot create a question loop")
		wdMatch(t, sink.msg(0), `Which scope\?[\s\S]*Observed edit`)
	})

	// clarificationFixture of the test.
	type fixture struct {
		rt       *watchdogRuntime
		config   *watchdogConfig
		requests *wdRequests
		sink     *wdSink
		key      *atomic.Value
	}
	newFixture := func(t *testing.T, clarification, main bool) *fixture {
		config := wdRtConfig(func(c *watchdogConfig) { c.Clarification, c.Cadence = clarification, wdCadence{EveryNTools: wdPtr(1)} })
		key := &atomic.Value{}
		key.Store("baseline")
		f := &fixture{config: config, requests: &wdRequests{}, sink: &wdSink{}, key: key}
		o := watchdogRuntimeOptions{Cwd: ctx.Cwd, ResolveConfig: func(string, *jsObject) watchdogSettingsResult { return wdCfgResult(config) }, ReviewChangesOnly: true,
			RepoChangeSignature: func(string) *watchdogRepoChangeSignature {
				return &watchdogRepoChangeSignature{Root: ctx.Cwd, Key: key.Load().(string), ChangedPaths: []string{"file.ts"}}
			},
			DisplayWarning: f.sink.warning,
			Review: func(r watchdogReviewRequest) (*watchdogReviewResult, error) {
				f.requests.add(r)
				if r.AllowClarification {
					return &watchdogReviewResult{Clarification: &watchdogClarification{Question: "Which scope?", Evidence: "Original evidence"}}, nil
				}
				return &watchdogReviewResult{Warnings: []wdWarning{wdRtWarning()}}, nil
			}}
		if main {
			o.DisplayClarification = f.sink.message
		}
		f.rt = newWatchdogRuntime(o)
		f.rt.handleBeforeAgentStart(wdPrompt(t, "Original task"), ctx)
		key.Store("edited")
		f.rt.enqueueDelta("Original bounded delta")
		return f
	}

	tw(t, f, "asks once, skips unchanged evidence, and leaves later edit and cadence reviews to normal triggers", func(t *testing.T) {
		f := newFixture(t, true, true)
		defer f.rt.dispose()
		zero := 0
		f.config.MaxWarnings = &zero
		f.rt.handleAgentEnd(nil, ctx)
		wdMatch(t, f.sink.msg(0), `Which scope\?[\s\S]*Original evidence`)
		wdEq(t, f.rt.getSnapshot("").Status, "idle", "status")
		f.rt.enqueueDelta("Orchestrator continues without answering")
		f.rt.handleAgentEnd(nil, ctx)
		wdEq(t, f.requests.n(), 1, "no mandatory follow-up review")
		f.key.Store("changed again")
		f.rt.enqueueDelta("New work, same user prompt")
		f.rt.handleAgentEnd(nil, ctx)
		wdEq(t, f.requests.at(-1).AllowClarification, false, "ordinary review does not ask")
		wdEq(t, f.requests.n(), 2, "new edits trigger ordinary review")
		f.rt.handleToolResult(ctx)
		wdEventually(t, "the cadence review", func() bool { return f.requests.n() == 3 })
		wdEq(t, f.sink.nMessages(), 1, "question does not block normal cadence")
		wdEq(t, f.sink.nWarnings(), 0, "question does not reset the warning budget")
		f.rt.handleBeforeAgentStart(wdPrompt(t, "New genuine prompt"), ctx)
		f.key.Store("next edit")
		f.rt.handleAgentEnd(nil, ctx)
		wdEq(t, f.sink.nMessages(), 2, "new prompt renews the one-question budget")
	})

	tw(t, f, "adds no clarification calls or activity collection when off or without main delivery capability", func(t *testing.T) {
		for _, c := range []struct{ enabled, main bool }{{false, true}, {true, false}} {
			f := newFixture(t, c.enabled, c.main)
			f.rt.handleTurnEnd(wdActivityTurn(t, "subagent", `{"action":"status"}`, "disabled"), ctx, false)
			wdEq(t, f.rt.getSnapshot("").FailedReviews, 0, "failedReviews")
			wdEq(t, f.rt.activityTail == "" && !f.rt.activityPending, true, "disabled activity must not be collected")
			f.rt.handleAgentEnd(nil, ctx)
			wdEq(t, f.requests.at(0).AllowClarification, false, "no clarification request")
			wdEq(t, f.sink.nMessages(), 0, "messages")
			epoch := f.rt.getSnapshot("").Epoch
			f.rt.handleModelChange()
			f.rt.handleUserInput()
			wdEq(t, f.rt.getSnapshot("").Epoch, epoch, "disabled clarification lifecycle handlers are inert")
			f.rt.dispose()
		}
	})

	tw(t, f, "aborts an active review on ${edge} and suppresses stale questions and warnings", func(t *testing.T) {
		for _, edge := range []string{"clarification disabled", "watchdog model changed", "new user input"} {
			t.Run(edge, func(t *testing.T) {
				box := newWdConfigBox(wdRtConfig(func(c *watchdogConfig) { c.Clarification = true }))
				started := make(chan watchdogReviewRequest, 1)
				finish := make(chan *watchdogReviewResult, 1)
				sink := &wdSink{}
				rt := newWatchdogRuntime(watchdogRuntimeOptions{Cwd: ctx.Cwd, ResolveConfig: box.resolve, DisplayClarification: sink.message, DisplayWarning: sink.warning,
					Review: func(r watchdogReviewRequest) (*watchdogReviewResult, error) {
						started <- r
						return <-finish, nil
					}})
				defer rt.dispose()
				rt.enqueueDelta("Original delta")
				boundary := wdAsync(func() { rt.handleAgentEnd(nil, ctx) })
				var req watchdogReviewRequest
				select {
				case req = <-started:
				case <-time.After(5 * time.Second):
					t.Fatal("review did not start")
				}
				c := *box.p.Load()
				switch edge {
				case "clarification disabled":
					c.Clarification = false
					box.p.Store(&c)
				case "watchdog model changed":
					c.Main.Model = "mock/new"
					box.p.Store(&c)
				default:
					rt.handleUserInput()
				}
				rt.refreshConfig("")
				wdEq(t, wdClosed(req.Signal), true, "signal aborted")
				epoch := rt.getSnapshot("").Epoch
				rt.refreshConfig("")
				wdEq(t, rt.getSnapshot("").Epoch, epoch, "terminal invalidation is not repeated on refresh")
				wdEq(t, req.EmitWarning(wdRtWarning()), false, "late warning callback is stale")
				finish <- &watchdogReviewResult{Clarification: &watchdogClarification{Question: "Old question?", Evidence: "Old context"}}
				wdAwait(t, "boundary", boundary)
				wdEq(t, sink.nWarnings(), 0, "delivered")
				wdEq(t, rt.getSnapshot("").ActiveReviewID, 0, "activeReviewId")
				wdEq(t, sink.nMessages(), 0, "messages")
			})
		}
	})

	tw(t, f, "does not inspect the repository until the watchdog is enabled", func(t *testing.T) {
		var enabled atomic.Bool
		var calls atomic.Int32
		rt := newWatchdogRuntime(watchdogRuntimeOptions{Cwd: "/tmp/project", ReviewChangesOnly: true,
			ResolveConfig: func(string, *jsObject) watchdogSettingsResult {
				if enabled.Load() {
					return wdCfgResult(wdRtConfig(nil))
				}
				return wdCfgResult(defaultWatchdogConfig())
			},
			RepoChangeSignature: func(string) *watchdogRepoChangeSignature {
				calls.Add(1)
				return &watchdogRepoChangeSignature{Root: "/tmp/project", Key: "signature", ChangedPaths: []string{}}
			}})
		wdEq(t, int(calls.Load()), 0, "calls at construction")
		rt.handleBeforeAgentStart(wdPrompt(t, "disabled"), ctx)
		wdEq(t, int(calls.Load()), 0, "calls while disabled")
		enabled.Store(true)
		rt.handleBeforeAgentStart(wdPrompt(t, "enabled"), ctx)
		wdEq(t, int(calls.Load()), 1, "calls once enabled")
	})

	tw(t, f, "does not use stale enabled config to inspect a newly bound default-off session", func(t *testing.T) {
		var calls atomic.Int32
		rt := newWatchdogRuntime(watchdogRuntimeOptions{Cwd: "/tmp/enabled", ReviewChangesOnly: true,
			ResolveConfig: func(cwd string, _ *jsObject) watchdogSettingsResult {
				if cwd == "/tmp/enabled" {
					return wdCfgResult(wdRtConfig(nil))
				}
				return wdCfgResult(defaultWatchdogConfig())
			},
			RepoChangeSignature: func(cwd string) *watchdogRepoChangeSignature {
				calls.Add(1)
				return &watchdogRepoChangeSignature{Root: cwd, Key: cwd, ChangedPaths: []string{}}
			}})
		wdEq(t, int(calls.Load()), 1, "calls at construction")
		rt.bindSession(wdRunCtx{Cwd: "/tmp/default-off"})
		wdEq(t, int(calls.Load()), 1, "calls after bindSession")
		wdEq(t, rt.getSnapshot("").Enabled, false, "enabled")
	})

	tw(t, f, "stays default-off and contains invalid config at the watchdog boundary", func(t *testing.T) {
		var reviews atomic.Int32
		rt := newWatchdogRuntime(watchdogRuntimeOptions{
			ResolveConfig: func(string, *jsObject) watchdogSettingsResult {
				return watchdogSettingsResult{OK: false, Config: defaultWatchdogConfig(),
					Errors: []wdSettingsError{{Scope: "user", Path: "/tmp/settings.json", Message: "bad watchdog config"}}, Sources: []wdSettingsSource{{Scope: "user", Path: "/tmp/settings.json", Exists: true}}}
			},
			Review: func(watchdogReviewRequest) (*watchdogReviewResult, error) { reviews.Add(1); return nil, nil }})
		rt.handleTurnEnd(wdObj(t, `{"type":"turn_end","message":{"role":"assistant","content":"Done."},"toolResults":[]}`), ctx, false)
		s := rt.getSnapshot("")
		wdEq(t, s.Enabled, false, "enabled")
		wdEq(t, s.ConfigOK, false, "configOk")
		wdEq(t, s.Status, "idle", "status")
		wdEq(t, int(reviews.Load()), 0, "reviews")
		wdEq(t, s.Errors[0].Message, "bad watchdog config", "error")
	})

	tw(t, f, "queues turn deltas, reviews once at agent_end, displays one warning, and returns idle", func(t *testing.T) {
		started := make(chan struct{})
		release := make(chan struct{})
		sink := &wdSink{}
		rt := newWatchdogRuntime(watchdogRuntimeOptions{ResolveConfig: newWdConfigBox(wdRtConfig(nil)).resolve, DisplayWarning: sink.warning,
			Review: func(r watchdogReviewRequest) (*watchdogReviewResult, error) {
				close(started)
				<-release
				if r.Delta != "Assistant:\nWorking" {
					t.Errorf("delta %q", r.Delta)
				}
				if !r.EmitWarning(wdRtWarning()) {
					t.Error("warning refused")
				}
				return &watchdogReviewResult{StopReason: "stop"}, nil
			}})
		rt.enqueueDelta("Assistant:\nWorking")
		wdEq(t, rt.getSnapshot("").Status, "queued", "queued")
		end := wdAsync(func() { rt.handleAgentEnd(wdObj(t, `{"type":"agent_end","messages":[]}`), ctx) })
		wdAwait(t, "review start", started)
		wdEq(t, rt.getSnapshot("").Status, "reviewing", "reviewing")
		close(release)
		wdAwait(t, "agent end", end)
		s := rt.getSnapshot("")
		wdEq(t, s.Status, "idle", "status")
		wdEq(t, s.LastWarning.State, "displayed", "lastWarning state")
		wdEq(t, sink.nWarnings(), 1, "displayed")
		wdEq(t, sink.warnings[0].State, "displayed", "displayed state")
	})

	tw(t, f, "routes low and medium findings only to persisted user entries, independent of severity", func(t *testing.T) {
		for _, severity := range []string{"concern", "blocker"} {
			for _, importance := range []string{"low", "medium", "high"} {
				sink := &wdSink{}
				o := sink.options0()
				o.ResolveConfig = newWdConfigBox(wdRtConfig(nil)).resolve
				o.Review = func(r watchdogReviewRequest) (*watchdogReviewResult, error) {
					w := wdRtWarning()
					w.Severity, w.Importance, w.Summary = severity, importance, severity+"-"+importance
					r.EmitWarning(w)
					return &watchdogReviewResult{StopReason: "stop"}, nil
				}
				rt := newWatchdogRuntime(o)
				rt.enqueueDelta("Assistant:\nWorking")
				rt.handleAgentEnd(nil, ctx)
				wantMessages, wantEntries := 0, 1
				if importance == "high" {
					wantMessages, wantEntries = 1, 0
				}
				wdEq(t, sink.nWarnings(), wantMessages, severity+" "+importance+" messages")
				wdEq(t, sink.nEntries(), wantEntries, severity+" "+importance+" entries")
			}
		}
	})

	tw(t, f, "drops stale async warning callbacks after reset", func(t *testing.T) {
		emit := make(chan func(wdWarning) bool, 1)
		release := make(chan struct{})
		rt := newWatchdogRuntime(watchdogRuntimeOptions{ResolveConfig: newWdConfigBox(wdRtConfig(nil)).resolve,
			Review: func(r watchdogReviewRequest) (*watchdogReviewResult, error) {
				emit <- r.EmitWarning
				<-release
				return nil, nil
			}})
		rt.enqueueDelta("Assistant:\nWorking")
		end := wdAsync(func() { rt.handleAgentEnd(wdObj(t, `{"type":"agent_end","messages":[]}`), ctx) })
		emitWarning := <-emit
		epoch := rt.getSnapshot("").Epoch
		rt.reset(wdResetOptions{})
		wdEq(t, emitWarning(wdRtWarning()), false, "stale callback")
		close(release)
		wdAwait(t, "agent end", end)
		s := rt.getSnapshot("")
		wdEq(t, s.Epoch, epoch+1, "epoch")
		wdEq(t, s.Status, "idle", "status")
		wdEq(t, s.LastWarning == nil, true, "lastWarning")
	})

	tw(t, f, "treats failed review stop reasons as failed", func(t *testing.T) {
		rt := newWatchdogRuntime(watchdogRuntimeOptions{ResolveConfig: newWdConfigBox(wdRtConfig(nil)).resolve,
			Review: func(watchdogReviewRequest) (*watchdogReviewResult, error) {
				return &watchdogReviewResult{StopReason: "length"}, nil
			}})
		rt.enqueueDelta("Assistant:\nWorking")
		rt.handleAgentEnd(wdObj(t, `{"type":"agent_end","messages":[]}`), ctx)
		s := rt.getSnapshot("")
		wdEq(t, s.Status, "failed", "status")
		wdMatch(t, s.LastError, `stop reason 'length'`)
		wdEq(t, s.FailedReviews, 1, "failedReviews")
	})

	tw(t, f, "surfaces the provider error text of a failed review in lastError", func(t *testing.T) {
		diagnostic := "provider-head-" + strings.Repeat("H", 400) + "-middle-" + strings.Repeat("M", 400) + "-provider-tail"
		rt := newWatchdogRuntime(watchdogRuntimeOptions{ResolveConfig: newWdConfigBox(wdRtConfig(nil)).resolve,
			Review: func(watchdogReviewRequest) (*watchdogReviewResult, error) {
				return &watchdogReviewResult{StopReason: "error", ErrorMessage: diagnostic}, nil
			}})
		rt.enqueueDelta("Assistant:\nWorking")
		rt.handleAgentEnd(wdObj(t, `{"type":"agent_end","messages":[]}`), ctx)
		s := rt.getSnapshot("")
		wdEq(t, s.Status, "failed", "status")
		wdTrue(t, strings.Contains(s.LastError, diagnostic[:150]), "head")
		wdTrue(t, strings.Contains(s.LastError, "[... about "+itoa(len(diagnostic)-600)+" characters omitted ...]"), "omitted marker")
		wdTrue(t, strings.HasSuffix(s.LastError, diagnostic[len(diagnostic)-100:]), "tail")
		wdTrue(t, !strings.Contains(s.LastError, diagnostic), "not the whole text")
	})

	tw(t, f, "marks unresolved agent-end review work stale on timeout", func(t *testing.T) {
		started := make(chan struct{})
		block := make(chan struct{})
		defer close(block)
		rt := newWatchdogRuntime(watchdogRuntimeOptions{ResolveConfig: newWdConfigBox(wdRtConfig(func(c *watchdogConfig) { c.AgentEndTimeoutMs = 5 })).resolve,
			Review: func(watchdogReviewRequest) (*watchdogReviewResult, error) { close(started); <-block; return nil, nil }})
		rt.enqueueDelta("Assistant:\nStill working")
		end := wdAsync(func() { rt.handleAgentEnd(wdObj(t, `{"type":"agent_end","messages":[]}`), ctx) })
		wdAwait(t, "review start", started)
		wdAwait(t, "agent end", end)
		s := rt.getSnapshot("")
		wdEq(t, s.Status, "stale", "status")
		wdEq(t, s.StaleReviews, 1, "staleReviews")
		rt.reset(wdResetOptions{})
	})

	tw(t, f, "aborts timed-out review work before another agent-end review can overlap", func(t *testing.T) {
		var active, maxActive, aborts atomic.Int32
		rt := newWatchdogRuntime(watchdogRuntimeOptions{ResolveConfig: newWdConfigBox(wdRtConfig(func(c *watchdogConfig) { c.AgentEndTimeoutMs = 5 })).resolve,
			Review: func(r watchdogReviewRequest) (*watchdogReviewResult, error) {
				n := active.Add(1)
				for {
					m := maxActive.Load()
					if n <= m || maxActive.CompareAndSwap(m, n) {
						break
					}
				}
				<-r.Signal
				aborts.Add(1)
				active.Add(-1)
				return &watchdogReviewResult{StopReason: "aborted"}, nil
			}})
		rt.enqueueDelta("Assistant:\nFirst review")
		rt.handleAgentEnd(wdObj(t, `{"type":"agent_end","messages":[]}`), ctx)
		wdEventually(t, "first abort", func() bool { return aborts.Load() == 1 })
		rt.enqueueDelta("Assistant:\nSecond review")
		rt.handleAgentEnd(wdObj(t, `{"type":"agent_end","messages":[]}`), ctx)
		wdEventually(t, "second abort", func() bool { return aborts.Load() == 2 && active.Load() == 0 })
		wdEq(t, int(maxActive.Load()), 1, "max active reviews")
		wdEq(t, rt.getSnapshot("").StaleReviews, 2, "staleReviews")
	})

	tw(t, f, "drops late warning callbacks after agent-end timeout marks a review stale", func(t *testing.T) {
		emit := make(chan func(wdWarning) bool, 1)
		release := make(chan struct{})
		rt := newWatchdogRuntime(watchdogRuntimeOptions{ResolveConfig: newWdConfigBox(wdRtConfig(func(c *watchdogConfig) { c.AgentEndTimeoutMs = 5 })).resolve,
			Review: func(r watchdogReviewRequest) (*watchdogReviewResult, error) {
				emit <- r.EmitWarning
				<-release
				return nil, nil
			}})
		rt.enqueueDelta("Assistant:\nStill working")
		rt.handleAgentEnd(wdObj(t, `{"type":"agent_end","messages":[]}`), ctx)
		emitWarning := <-emit
		wdEq(t, emitWarning(wdRtWarning()), false, "late callback")
		close(release)
		time.Sleep(20 * time.Millisecond)
		s := rt.getSnapshot("")
		wdEq(t, s.Status, "stale", "status")
		wdEq(t, s.LastWarning == nil, true, "lastWarning")
	})

	tw(t, f, "invalidates an in-flight review when refreshed config disables the watchdog", func(t *testing.T) {
		var enabled atomic.Bool
		enabled.Store(true)
		emit := make(chan func(wdWarning) bool, 1)
		release := make(chan struct{})
		rt := newWatchdogRuntime(watchdogRuntimeOptions{
			ResolveConfig: func(string, *jsObject) watchdogSettingsResult {
				if enabled.Load() {
					return wdCfgResult(wdRtConfig(nil))
				}
				return wdCfgResult(defaultWatchdogConfig())
			},
			Review: func(r watchdogReviewRequest) (*watchdogReviewResult, error) {
				emit <- r.EmitWarning
				<-release
				return nil, nil
			}})
		rt.enqueueDelta("Assistant:\nWorking")
		end := wdAsync(func() { rt.handleAgentEnd(wdObj(t, `{"type":"agent_end","messages":[]}`), ctx) })
		emitWarning := <-emit
		enabled.Store(false)
		rt.refreshConfig("/tmp/project")
		wdEq(t, rt.getSnapshot("").Enabled, false, "enabled")
		wdEq(t, rt.getSnapshot("").Status, "idle", "status")
		wdEq(t, emitWarning(wdRtWarning()), false, "stale callback")
		close(release)
		wdAwait(t, "agent end", end)
		wdEq(t, rt.getSnapshot("").LastWarning == nil, true, "lastWarning")
	})

	tw(t, f, "bounds review input before calling the reviewer", func(t *testing.T) {
		var reviewed string
		rt := newWatchdogRuntime(watchdogRuntimeOptions{ResolveConfig: newWdConfigBox(wdRtConfig(nil)).resolve,
			Review: func(r watchdogReviewRequest) (*watchdogReviewResult, error) {
				reviewed = r.Delta
				return &watchdogReviewResult{StopReason: "stop"}, nil
			}})
		rt.enqueueDelta("Assistant:\n" + strings.Repeat("x", 30_000))
		rt.handleAgentEnd(wdObj(t, `{"type":"agent_end","messages":[]}`), ctx)
		wdEq(t, wdLen(reviewed), 24_000, "head + marker + tail fill the cap exactly")
		wdMatch(t, reviewed, `^Assistant:\nx+\n\n\[\.\.\. about \d+ characters omitted \.\.\.\]\n\nx+$`)
		wdEq(t, strings.Index(reviewed, "\n\n[..."), 6_000, "the first 6,000 characters are kept as the head")
	})

	tw(t, f, "does not record duplicate signatures for stale invalidated reviews", func(t *testing.T) {
		var calls atomic.Int32
		started := make(chan struct{})
		release := make(chan struct{})
		rt := newWatchdogRuntime(watchdogRuntimeOptions{ResolveConfig: newWdConfigBox(wdRtConfig(nil)).resolve,
			Review: func(watchdogReviewRequest) (*watchdogReviewResult, error) {
				if calls.Add(1) == 1 {
					close(started)
					<-release
				}
				return &watchdogReviewResult{StopReason: "stop"}, nil
			}})
		rt.enqueueDelta("Assistant:\nSame work")
		first := wdAsync(func() { rt.handleAgentEnd(wdObj(t, `{"type":"agent_end","messages":[]}`), ctx) })
		wdAwait(t, "first review", started)
		rt.reset(wdResetOptions{})
		close(release)
		wdAwait(t, "first agent end", first)
		rt.enqueueDelta("Assistant:\nSame work")
		rt.handleAgentEnd(wdObj(t, `{"type":"agent_end","messages":[]}`), ctx)
		wdEq(t, int(calls.Load()), 2, "review calls")
		wdEq(t, rt.getSnapshot("").Status, "idle", "status")
	})

	tw(t, f, "skips duplicate bounded review input within the session", func(t *testing.T) {
		var calls atomic.Int32
		rt := newWatchdogRuntime(watchdogRuntimeOptions{ResolveConfig: newWdConfigBox(wdRtConfig(nil)).resolve,
			Review: func(watchdogReviewRequest) (*watchdogReviewResult, error) {
				calls.Add(1)
				return &watchdogReviewResult{StopReason: "stop"}, nil
			}})
		for i := 0; i < 2; i++ {
			rt.enqueueDelta("Assistant:\nSame work")
			rt.handleAgentEnd(wdObj(t, `{"type":"agent_end","messages":[]}`), ctx)
		}
		wdEq(t, int(calls.Load()), 1, "review calls")
		wdEq(t, rt.getSnapshot("").Status, "idle", "status")
	})

	// Edit-gated reviews against a real repository.
	repoRuntime := func(t *testing.T, repo string, mod func(*watchdogConfig), review watchdogReviewFunc, extra func(*watchdogRuntimeOptions)) *watchdogRuntime {
		o := watchdogRuntimeOptions{Cwd: repo, ReviewChangesOnly: true, ResolveConfig: newWdConfigBox(wdRtConfig(mod)).resolve, Review: review}
		if extra != nil {
			extra(&o)
		}
		return newWatchdogRuntime(o)
	}
	agentEnd := func(t *testing.T, rt *watchdogRuntime, repo string) {
		rt.handleAgentEnd(wdObj(t, `{"type":"agent_end"}`), wdRunCtx{Cwd: repo})
	}
	editFile := func(t *testing.T, repo, name, body string) { writeFile(t, filepath.Join(repo, "src", name), body) }

	tw(t, f, "skips edit-gated watchdog reviews when no repo changes occurred", func(t *testing.T) {
		repo := wdRepoForRuntime(t)
		var calls atomic.Int32
		rt := repoRuntime(t, repo, nil, func(watchdogReviewRequest) (*watchdogReviewResult, error) {
			calls.Add(1)
			return &watchdogReviewResult{StopReason: "stop"}, nil
		}, nil)
		rt.handleBeforeAgentStart(wdPrompt(t, "Explain the file."), wdRunCtx{Cwd: repo})
		rt.enqueueDelta("Assistant:\nI inspected the repo without editing.")
		agentEnd(t, rt, repo)
		wdEq(t, int(calls.Load()), 0, "review calls")
		wdEq(t, rt.getSnapshot(repo).ReviewTrigger, "repo-edits", "reviewTrigger")
		wdEq(t, rt.getSnapshot(repo).Status, "idle", "status")
	})

	tw(t, f, "coalesces multiple repo edits in one turn into one edit-gated review", func(t *testing.T) {
		repo := wdRepoForRuntime(t)
		var calls atomic.Int32
		var reviewed string
		rt := repoRuntime(t, repo, nil, func(r watchdogReviewRequest) (*watchdogReviewResult, error) {
			calls.Add(1)
			reviewed = r.Delta
			return &watchdogReviewResult{StopReason: "stop"}, nil
		}, nil)
		rt.handleBeforeAgentStart(wdPrompt(t, "Patch the feature."), wdRunCtx{Cwd: repo})
		editFile(t, repo, "file.ts", "export const value = 2;\n")
		editFile(t, repo, "other.ts", "export const other = true;\n")
		rt.enqueueDelta("Assistant:\nEdited two files.")
		agentEnd(t, rt, repo)
		wdEq(t, int(calls.Load()), 1, "review calls")
		wdMatch(t, reviewed, `Changed repo paths:`)
		wdMatch(t, reviewed, `src/file\.ts`)
		wdMatch(t, reviewed, `src/other\.ts`)
		rt.handleBeforeAgentStart(wdPrompt(t, "Say more."), wdRunCtx{Cwd: repo})
		rt.enqueueDelta("Assistant:\nNo further edits.")
		agentEnd(t, rt, repo)
		wdEq(t, int(calls.Load()), 1, "review calls after no further edits")
	})

	tw(t, f, "reviews same-path repo edits with identical turn text when content changes", func(t *testing.T) {
		repo := wdRepoForRuntime(t)
		var deltas []string
		rt := repoRuntime(t, repo, nil, func(r watchdogReviewRequest) (*watchdogReviewResult, error) {
			deltas = append(deltas, r.Delta)
			return &watchdogReviewResult{StopReason: "stop"}, nil
		}, nil)
		for _, value := range []int{2, 3} {
			rt.handleBeforeAgentStart(wdPrompt(t, "Patch the feature."), wdRunCtx{Cwd: repo})
			editFile(t, repo, "file.ts", "export const value = "+itoa(value)+";\n")
			rt.enqueueDelta("Assistant:\nEdited the file.")
			agentEnd(t, rt, repo)
		}
		wdEq(t, len(deltas), 2, "review calls")
		wdMatch(t, deltas[1], `Changed repo paths:`)
		wdMatch(t, deltas[1], `src/file\.ts`)
	})

	tw(t, f, "keeps changed repo paths when bounding large edit-gated review input", func(t *testing.T) {
		repo := wdRepoForRuntime(t)
		var reviewed string
		rt := repoRuntime(t, repo, nil, func(r watchdogReviewRequest) (*watchdogReviewResult, error) {
			reviewed = r.Delta
			return &watchdogReviewResult{StopReason: "stop"}, nil
		}, nil)
		rt.handleBeforeAgentStart(wdPrompt(t, "Patch the feature."), wdRunCtx{Cwd: repo})
		editFile(t, repo, "file.ts", "export const value = 4;\n")
		rt.enqueueDelta("Assistant:\n" + strings.Repeat("x", 30_000))
		agentEnd(t, rt, repo)
		wdEq(t, wdLen(reviewed), 24_000, "length")
		wdMatch(t, reviewed, `^Current scope:`)
		wdMatch(t, reviewed, `Changed repo paths:`)
		wdMatch(t, reviewed, `src/file\.ts`)
		wdMatch(t, reviewed, `x+$`)
	})

	lspConfig := func(c *watchdogConfig) { c.Lsp = defaultWatchdogConfig().Lsp }
	tsError := wdLspDiagnostic{Path: "src/file.ts", Line: 1, Column: 14, Severity: "error", Source: "typescript", Code: "TS2322", Message: "Type 'string' is not assignable to type 'number'."}

	tw(t, f, "emits changed-file LSP errors as watchdog blockers before model review", func(t *testing.T) {
		repo := wdRepoForRuntime(t)
		sink := &wdSink{}
		var reviewed string
		rt := repoRuntime(t, repo, lspConfig, func(r watchdogReviewRequest) (*watchdogReviewResult, error) {
			reviewed = r.Delta
			w := wdRtWarning()
			w.Summary, w.Evidence = "Model tried to add a second warning", "The same boundary already displayed an LSP warning."
			if r.EmitWarning(w) {
				t.Error("a second warning in the same boundary was accepted")
			}
			return &watchdogReviewResult{StopReason: "stop"}, nil
		}, func(o *watchdogRuntimeOptions) {
			o.DisplayWarning = sink.warning
			o.LspDiagnostics = func(watchdogLspRequest) wdLspResult { return wdLspOK(tsError) }
		})
		rt.handleBeforeAgentStart(wdPrompt(t, "Patch the feature."), wdRunCtx{Cwd: repo})
		editFile(t, repo, "file.ts", "export const value: number = 'bad';\n")
		rt.enqueueDelta("Assistant:\nEdited the file.")
		agentEnd(t, rt, repo)
		wdMatch(t, reviewed, `Changed repo paths:`)
		wdMatch(t, reviewed, `LSP diagnostics:`)
		wdMatch(t, reviewed, `TS2322`)
		wdEq(t, sink.nWarnings(), 1, "displayed")
		wdEq(t, sink.warnings[0].Source, "lsp", "source")
		wdEq(t, sink.warnings[0].Severity, "blocker", "severity")
		s := rt.getSnapshot(repo)
		wdEq(t, s.Lsp.Status, "ok", "lsp status")
		wdEq(t, s.Lsp.DiagnosticCount, 1, "diagnosticCount")
		wdEq(t, s.Lsp.FreshDiagnosticCount, 1, "freshDiagnosticCount")
	})

	tw(t, f, "records LSP info and hints without sending them to model review", func(t *testing.T) {
		repo := wdRepoForRuntime(t)
		sink := &wdSink{}
		var reviewed string
		rt := repoRuntime(t, repo, lspConfig, func(r watchdogReviewRequest) (*watchdogReviewResult, error) {
			reviewed = r.Delta
			return &watchdogReviewResult{StopReason: "stop"}, nil
		}, func(o *watchdogRuntimeOptions) {
			o.DisplayWarning = sink.warning
			o.LspDiagnostics = func(watchdogLspRequest) wdLspResult {
				return wdLspOK(wdLspDiagnostic{Path: "src/file.ts", Line: 1, Column: 14, Severity: "info", Source: "typescript", Message: "Helpful note."},
					wdLspDiagnostic{Path: "src/file.ts", Line: 2, Column: 4, Severity: "hint", Source: "typescript", Message: "Suggestion."})
			}
		})
		rt.handleBeforeAgentStart(wdPrompt(t, "Patch the feature."), wdRunCtx{Cwd: repo})
		editFile(t, repo, "file.ts", "export const value = 7;\n")
		rt.enqueueDelta("Assistant:\nEdited the file.")
		agentEnd(t, rt, repo)
		wdMatch(t, reviewed, `Changed repo paths:`)
		wdNoMatch(t, reviewed, `LSP diagnostics:`)
		wdEq(t, sink.nWarnings(), 0, "displayed")
		s := rt.getSnapshot(repo)
		wdEq(t, s.Lsp.DiagnosticCount, 2, "diagnosticCount")
		wdEq(t, s.Lsp.FreshDiagnosticCount, 2, "freshDiagnosticCount")
		var severities []string
		for _, d := range s.Lsp.Diagnostics {
			severities = append(severities, d.Severity)
		}
		wdEq(t, severities, []string{"info", "hint"}, "severities")
	})

	tw(t, f, "aborts and ignores stale LSP diagnostics after reset", func(t *testing.T) {
		repo := wdRepoForRuntime(t)
		sink := &wdSink{}
		started := make(chan struct{})
		var aborted atomic.Bool
		var reviews atomic.Int32
		rt := repoRuntime(t, repo, lspConfig, func(watchdogReviewRequest) (*watchdogReviewResult, error) {
			reviews.Add(1)
			return &watchdogReviewResult{StopReason: "stop"}, nil
		},
			func(o *watchdogRuntimeOptions) {
				o.DisplayWarning = sink.warning
				o.LspDiagnostics = func(r watchdogLspRequest) wdLspResult {
					close(started)
					<-r.Signal
					aborted.Store(true)
					return wdLspOK(tsError)
				}
			})
		rt.handleBeforeAgentStart(wdPrompt(t, "Patch the feature."), wdRunCtx{Cwd: repo})
		editFile(t, repo, "file.ts", "export const value: number = 'bad';\n")
		rt.enqueueDelta("Assistant:\nEdited the file.")
		end := wdAsync(func() { agentEnd(t, rt, repo) })
		wdAwait(t, "lsp start", started)
		rt.reset(wdResetOptions{})
		wdAwait(t, "agent end", end)
		wdEq(t, aborted.Load(), true, "signal aborted")
		wdEq(t, sink.nWarnings(), 0, "displayed")
		wdEq(t, int(reviews.Load()), 0, "review calls")
		wdEq(t, rt.getSnapshot(repo).Status, "idle", "status")
	})

	tw(t, f, "clears pending deltas when an edit-gated signature was already reviewed", func(t *testing.T) {
		repo := wdRepoForRuntime(t)
		var calls atomic.Int32
		rt := repoRuntime(t, repo, nil, func(watchdogReviewRequest) (*watchdogReviewResult, error) {
			calls.Add(1)
			return &watchdogReviewResult{StopReason: "stop"}, nil
		}, nil)
		rt.handleBeforeAgentStart(wdPrompt(t, "Patch the feature."), wdRunCtx{Cwd: repo})
		editFile(t, repo, "file.ts", "export const value = 2;\n")
		rt.enqueueDelta("Assistant:\nEdited the file.")
		agentEnd(t, rt, repo)
		rt.enqueueDelta("Assistant:\nDiscussed the already-reviewed edit.")
		agentEnd(t, rt, repo)
		s := rt.getSnapshot(repo)
		wdEq(t, int(calls.Load()), 1, "review calls")
		wdEq(t, s.Status, "idle", "status")
		wdEq(t, s.BufferedDeltas, 0, "bufferedDeltas")
	})

	tw(t, f, "dedupes repeated LSP diagnostic identities until diagnostics clear", func(t *testing.T) {
		repo := wdRepoForRuntime(t)
		sink := &wdSink{}
		var mu sync.Mutex
		diagnostics := []wdLspDiagnostic{{Path: "src/file.ts", Line: 1, Column: 14, Severity: "warning", Source: "typescript", Code: "TS6133", Message: "'value' is declared but its value is never read."}}
		rt := repoRuntime(t, repo, lspConfig, func(watchdogReviewRequest) (*watchdogReviewResult, error) {
			return &watchdogReviewResult{StopReason: "stop"}, nil
		},
			func(o *watchdogRuntimeOptions) {
				o.DisplayWarning = sink.warning
				o.LspDiagnostics = func(watchdogLspRequest) wdLspResult {
					mu.Lock()
					defer mu.Unlock()
					return wdLspOK(diagnostics...)
				}
			})
		edit := func(prompt string, value int) {
			rt.handleBeforeAgentStart(wdPrompt(t, prompt), wdRunCtx{Cwd: repo})
			editFile(t, repo, "file.ts", "export const value = "+itoa(value)+";\n")
			rt.enqueueDelta("Assistant:\nEdited the file.")
			agentEnd(t, rt, repo)
		}
		edit("Patch the feature.", 2)
		edit("Patch the feature.", 3)
		wdEq(t, sink.nWarnings(), 1, "displayed once")
		wdEq(t, rt.getSnapshot(repo).Lsp.FreshDiagnosticCount, 0, "freshDiagnosticCount")
		mu.Lock()
		diagnostics = nil
		mu.Unlock()
		edit("Fix diagnostics.", 4)
		mu.Lock()
		diagnostics = []wdLspDiagnostic{{Path: "src/file.ts", Line: 2, Column: 1, Severity: "warning", Source: "typescript", Code: "TS6133", Message: "'value' is declared but its value is never read."}}
		mu.Unlock()
		edit("Reintroduce diagnostics.", 5)
		wdEq(t, sink.nWarnings(), 2, "displayed again once the diagnostics cleared")
	})

	tw(t, f, "records slow LSP checks as timeout status without emitting a warning", func(t *testing.T) {
		repo := wdRepoForRuntime(t)
		sink := &wdSink{}
		rt := repoRuntime(t, repo, lspConfig, func(watchdogReviewRequest) (*watchdogReviewResult, error) {
			return &watchdogReviewResult{StopReason: "stop"}, nil
		},
			func(o *watchdogRuntimeOptions) {
				o.DisplayWarning = sink.warning
				o.LspDiagnostics = func(watchdogLspRequest) wdLspResult {
					return wdLspResult{Status: "timeout", Provider: "stub-lsp", CheckedPaths: []string{"src/file.ts"}, SkippedPaths: []string{}, Diagnostics: []wdLspDiagnostic{}, Message: "Timed out waiting for diagnostics."}
				}
			})
		rt.handleBeforeAgentStart(wdPrompt(t, "Patch the feature."), wdRunCtx{Cwd: repo})
		editFile(t, repo, "file.ts", "export const value = 6;\n")
		rt.enqueueDelta("Assistant:\nEdited the file.")
		agentEnd(t, rt, repo)
		s := rt.getSnapshot(repo)
		wdEq(t, sink.nWarnings(), 0, "displayed")
		wdEq(t, s.Lsp.Status, "timeout", "lsp status")
		wdMatch(t, s.Lsp.Message, `Timed out`)
	})

	tw(t, f, "does not review reverted or ignored tmp-only changes in edit-gated mode", func(t *testing.T) {
		repo := wdRepoForRuntime(t)
		var calls atomic.Int32
		rt := repoRuntime(t, repo, nil, func(watchdogReviewRequest) (*watchdogReviewResult, error) {
			calls.Add(1)
			return &watchdogReviewResult{StopReason: "stop"}, nil
		}, nil)
		rt.handleBeforeAgentStart(wdPrompt(t, "Try an edit."), wdRunCtx{Cwd: repo})
		editFile(t, repo, "file.ts", "export const value = 3;\n")
		editFile(t, repo, "file.ts", "export const value = 1;\n")
		rt.enqueueDelta("Assistant:\nEdited then reverted.")
		agentEnd(t, rt, repo)
		if err := os.MkdirAll(filepath.Join(repo, "tmp"), 0o755); err != nil {
			t.Fatal(err)
		}
		rt.handleBeforeAgentStart(wdPrompt(t, "Write tmp artifact."), wdRunCtx{Cwd: repo})
		writeFile(t, filepath.Join(repo, "tmp", "artifact.md"), "ignore me\n")
		rt.enqueueDelta("Assistant:\nWrote tmp artifact.")
		agentEnd(t, rt, repo)
		wdEq(t, int(calls.Load()), 0, "review calls")
	})

	tw(t, f, "hard-caps each successful review to one displayed warning", func(t *testing.T) {
		sink := &wdSink{}
		rt := newWatchdogRuntime(watchdogRuntimeOptions{ResolveConfig: newWdConfigBox(wdRtConfig(nil)).resolve, DisplayWarning: sink.warning,
			Review: func(r watchdogReviewRequest) (*watchdogReviewResult, error) {
				if !r.EmitWarning(wdRtWarning()) {
					t.Error("first warning refused")
				}
				second := wdRtWarning()
				second.Summary, second.Evidence = "Second runtime concern", "The same review tried to emit another concern."
				if r.EmitWarning(second) {
					t.Error("second warning accepted")
				}
				return &watchdogReviewResult{StopReason: "stop"}, nil
			}})
		rt.enqueueDelta("Assistant:\nWorking")
		rt.handleAgentEnd(wdObj(t, `{"type":"agent_end","messages":[]}`), ctx)
		wdEq(t, sink.nWarnings(), 1, "displayed")
		wdEq(t, rt.getSnapshot("").LastWarning.Summary, "Runtime concern", "summary")
		wdEq(t, rt.getSnapshot("").LastWarning.State, "displayed", "state")
	})

	tw(t, f, "prepends current scope to boundary reviews", func(t *testing.T) {
		var reviewed string
		rt := newWatchdogRuntime(watchdogRuntimeOptions{ResolveConfig: newWdConfigBox(wdRtConfig(nil)).resolve,
			Review: func(r watchdogReviewRequest) (*watchdogReviewResult, error) {
				reviewed = r.Delta
				if !r.HasScope {
					t.Error("hasScope false")
				}
				return &watchdogReviewResult{StopReason: "stop"}, nil
			}})
		rt.handleBeforeAgentStart(wdPrompt(t, "Only update docs."), ctx)
		rt.enqueueDelta("Assistant:\nEdited docs.")
		rt.handleAgentEnd(wdObj(t, `{"type":"agent_end"}`), ctx)
		wdMatch(t, reviewed, `^Current scope:`)
		wdMatch(t, reviewed, `Only update docs\.`)
		wdMatch(t, reviewed, `category 'scope-drift'`)
	})

	tw(t, f, "runs visible steer corrections at cadence multiples and skips overlapping mid-run reviews", func(t *testing.T) {
		started := make(chan struct{})
		release := make(chan struct{})
		var calls atomic.Int32
		sink := &wdSink{}
		rt := newWatchdogRuntime(watchdogRuntimeOptions{ResolveConfig: newWdConfigBox(wdRtConfig(func(c *watchdogConfig) { c.Cadence = wdCadence{EveryNTools: wdPtr(5)} })).resolve, DisplayWarning: sink.warning,
			Review: func(r watchdogReviewRequest) (*watchdogReviewResult, error) {
				n := calls.Add(1)
				wdMatch(t, r.Delta, `Current scope:`)
				w := wdRtWarning()
				w.Severity, w.Summary = "blocker", "Cadence blocker "+itoa(int(n))
				if !r.EmitWarning(w) {
					t.Error("cadence warning refused")
				}
				if n == 1 {
					close(started)
				}
				<-release
				return &watchdogReviewResult{StopReason: "stop"}, nil
			}})
		rt.handleBeforeAgentStart(wdPrompt(t, "Stay in scope."), ctx)
		rt.enqueueDelta("Assistant:\nUsing tools.")
		for i := 0; i < 10; i++ {
			rt.handleToolResult(ctx)
		}
		wdAwait(t, "first cadence review", started)
		wdEq(t, int(calls.Load()), 1, "overlapping mid-run reviews are skipped")
		close(release)
		wdEventually(t, "the correction", func() bool { return sink.nWarnings() == 1 })
		wdEq(t, sink.options[0], &wdSendOptions{DeliverAs: "steer"}, "delivery options")
	})

	tw(t, f, "cancels an in-flight cadence review so the agent-end boundary review still runs", func(t *testing.T) {
		release := make(chan struct{})
		started := make(chan struct{})
		var calls atomic.Int32
		rt := newWatchdogRuntime(watchdogRuntimeOptions{ResolveConfig: newWdConfigBox(wdRtConfig(func(c *watchdogConfig) { c.Cadence = wdCadence{EveryNTools: wdPtr(5)} })).resolve,
			Review: func(watchdogReviewRequest) (*watchdogReviewResult, error) {
				if calls.Add(1) == 1 {
					close(started)
					<-release
				}
				return &watchdogReviewResult{StopReason: "stop"}, nil
			}})
		rt.handleBeforeAgentStart(wdPrompt(t, "Stay in scope."), ctx)
		rt.enqueueDelta("Assistant:\nUsing tools.")
		for i := 0; i < 5; i++ {
			rt.handleToolResult(ctx)
		}
		wdAwait(t, "first review", started)
		rt.enqueueDelta("Assistant:\nFinal edits.")
		agentEnd := wdAsync(func() { rt.handleAgentEnd(wdObj(t, `{"type":"agent_end"}`), ctx) })
		// the original's agent_end handler runs up to its first await (the cancel of the cadence review) before the release below
		wdEventually(t, "the cadence review to be superseded", func() bool { return rt.getSnapshot("").StaleReviews == 1 })
		close(release)
		wdAwait(t, "agent end", agentEnd)
		s := rt.getSnapshot("")
		wdEq(t, int(calls.Load()), 2, "boundary review must run even when a cadence review was in flight")
		wdEq(t, s.StaleReviews, 1, "superseded cadence review is counted as stale")
		wdEq(t, s.Status, "idle", "status")
	})

	tw(t, f, "marks the third consecutive identical boundary warning as stalemate and delivers it held", func(t *testing.T) {
		sink := &wdSink{}
		rt := newWatchdogRuntime(watchdogRuntimeOptions{ResolveConfig: newWdConfigBox(wdRtConfig(func(c *watchdogConfig) { c.StalemateRepeats = 3 })).resolve, DisplayWarning: sink.warning,
			Review: func(r watchdogReviewRequest) (*watchdogReviewResult, error) {
				w := wdRtWarning()
				w.Severity, w.Summary = "blocker", "Same blocker"
				r.EmitWarning(w)
				return &watchdogReviewResult{StopReason: "stop"}, nil
			}})
		rt.handleBeforeAgentStart(wdPrompt(t, "Patch carefully."), ctx)
		for _, delta := range []string{"Assistant:\nBad patch.", "Assistant:\nStill bad.", "Assistant:\nStill bad again."} {
			rt.enqueueDelta(delta)
			rt.handleAgentEnd(wdObj(t, `{"type":"agent_end"}`), ctx)
		}
		wdEq(t, sink.nWarnings(), 3, "repeats keep nudging until the stalemate threshold")
		wdEq(t, sink.options[0] == nil && sink.options[1] == nil, true, "first two are ordinary")
		hold := false
		wdEq(t, sink.options[2], &wdSendOptions{TriggerTurn: &hold}, "third is held")
		wdEq(t, sink.warnings[2].State, "stalemate", "state")
		wdEq(t, *sink.warnings[2].StalemateRepeats, 3, "stalemateRepeats")
		s := rt.getSnapshot("")
		wdEq(t, s.Stalemate, true, "stalemate")
		wdEq(t, s.BoundaryRepeats, 3, "boundaryRepeats")
		wdEq(t, s.LastWarning.State, "stalemate", "lastWarning state")
		// After stalemate the same finding is a plain duplicate again: no further delivery.
		rt.enqueueDelta("Assistant:\nStill bad, fourth time.")
		rt.handleAgentEnd(wdObj(t, `{"type":"agent_end"}`), ctx)
		wdEq(t, sink.nWarnings(), 3, "no further delivery")
		// A real prompt resets the counter.
		rt.handleBeforeAgentStart(wdPrompt(t, "Human: try a different approach."), ctx)
		rt.enqueueDelta("Assistant:\nNew attempt.")
		rt.handleAgentEnd(wdObj(t, `{"type":"agent_end"}`), ctx)
		wdEq(t, sink.nWarnings(), 4, "delivered again")
		wdEq(t, sink.options[3] == nil, true, "ordinary again")
		s = rt.getSnapshot("")
		wdEq(t, s.BoundaryRepeats, 1, "boundaryRepeats")
		wdEq(t, s.Stalemate, false, "stalemate")
	})

	sessionResolver := func(mainEnabled bool) func(string, *jsObject) watchdogSettingsResult {
		return func(_ string, session *jsObject) watchdogSettingsResult {
			config := wdRtConfig(func(c *watchdogConfig) { c.Main.Enabled = mainEnabled })
			if session != nil {
				if b, ok := session.flag("enabled"); ok {
					config.Enabled = b
				}
				if m := session.obj("main"); m != nil {
					if b, ok := m.flag("enabled"); ok {
						config.Main.Enabled = b
					}
				}
			}
			return wdCfgResult(config)
		}
	}
	tw(t, f, "session on/off overrides explicit main enabled settings", func(t *testing.T) {
		rt := newWatchdogRuntime(watchdogRuntimeOptions{ResolveConfig: sessionResolver(false)})
		wdEq(t, rt.getSnapshot("").Enabled, false, "initial")
		wdEq(t, rt.setSessionEnabled(true, "/tmp/project").Enabled, true, "session on")
		wdEq(t, rt.setSessionEnabled(false, "/tmp/project").Enabled, false, "session off")
	})
	tw(t, f, "clears session overrides when a new session is bound", func(t *testing.T) {
		rt := newWatchdogRuntime(watchdogRuntimeOptions{ResolveConfig: sessionResolver(true)})
		wdEq(t, rt.setSessionEnabled(false, "/tmp/project").Enabled, false, "session off")
		rt.bindSession(wdRunCtx{Cwd: "/tmp/project"})
		s := rt.getSnapshot("")
		wdEq(t, s.Enabled, true, "enabled")
		wdEq(t, s.SessionOverride == nil, true, "sessionOverride")
	})
}
