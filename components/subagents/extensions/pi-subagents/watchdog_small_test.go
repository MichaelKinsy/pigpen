package pi_subagents

import (
	"strings"
	"testing"
)

// Twins of test/unit/watchdog-rules, watchdog-scope, watchdog-render, watchdog-emission-guard and
// watchdog-stable-host-layout.

func TestWatchdogRulesTwins(t *testing.T) {
	const f = "watchdog-rules"
	tw(t, f, "matches globs anchored and case-sensitively", func(t *testing.T) {
		for _, c := range []struct {
			pattern, value string
			want           bool
			why            string
		}{
			{"openai-codex/gpt-5.6-*:max", "openai-codex/gpt-5.6-luna:max", true, ""},
			{"openai-codex/gpt-5.6-*", "openai-codex/gpt-5.6-luna:max", true, ""},
			{"gpt-5.6-*", "openai-codex/gpt-5.6-luna", false, "patterns are anchored at the start"},
			{"*", "anything/at-all", true, ""},
			{"anthropic/claude-opus-4.?", "anthropic/claude-opus-4.8", true, ""},
			{"anthropic/claude-opus-4.8", "anthropic/claude-opus-4x8", false, ""},
			{"Anthropic/*", "anthropic/claude", false, ""},
		} {
			if got := watchdogGlobMatch(c.pattern, c.value); got != c.want {
				t.Errorf("watchdogGlobMatch(%q, %q) = %v, want %v %s", c.pattern, c.value, got, c.want, c.why)
			}
		}
	})
	tw(t, f, "applies deny before allow, matches the base model when the suffix differs, and shapes a concern", func(t *testing.T) {
		config := &wdRules{Action: "warn", RoleModels: []wdRoleModelEntry{{"scout", wdRoleModelRule{Allow: []string{"openai-codex/gpt-5.6-luna"}, Deny: []string{"openai-codex/gpt-5.6-sol*"}, Note: "scout is cheap"}}}}
		if v := evaluateLaunchRule(config, "scout", "openai-codex/gpt-5.6-luna:max"); v != nil {
			t.Errorf("the allowed base model was judged: %+v", v)
		}
		denied := evaluateLaunchRule(config, "scout", "openai-codex/gpt-5.6-sol:high")
		if denied == nil {
			t.Fatal("no violation for the denied model")
		}
		wdMatch(t, denied.Summary, `denied model 'openai-codex/gpt-5\.6-sol:high'`)
		wdMatch(t, denied.Evidence, `scout is cheap`)
		outside := evaluateLaunchRule(config, "scout", "anthropic/claude-opus-4-8")
		if outside == nil {
			t.Fatal("no violation for the model outside the allowed list")
		}
		wdMatch(t, outside.Summary, `not in its allowed list`)
		wdMatch(t, outside.RecommendedAction, `openai-codex/gpt-5\.6-luna`)
		if evaluateLaunchRule(config, "worker", "anthropic/claude-opus-4-8") != nil {
			t.Error("other roles are unaffected")
		}
		if evaluateLaunchRule(config, "scout", "") != nil {
			t.Error("an unknown model cannot be judged")
		}
		if evaluateLaunchRule(nil, "scout", "x/y") != nil {
			t.Error("no rules, no violation")
		}
		warning := ruleViolationWarning(*denied)
		wdEq(t, warning.Severity, "concern", "severity")
		wdEq(t, warning.Category, "missed-constraint", "category")
		wdEq(t, warning.Importance, "high", "importance")
		wdEq(t, warning.Source, "main", "source")
		wdEq(t, warning.Agent, "scout", "agent")
	})
}

func TestWatchdogScopeTwins(t *testing.T) {
	const f = "watchdog-scope"
	tw(t, f, "keeps bounded prompts in newest-last order", func(t *testing.T) {
		scope := &watchdogScope{}
		for i := 0; i < 10; i++ {
			scope.addPrompt("prompt-"+string(rune('0'+i)), "t"+string(rune('0'+i)))
		}
		entries := scope.snapshot()
		wdEq(t, len(entries), 8, "entries")
		var prompts []string
		for _, e := range entries {
			prompts = append(prompts, e.Prompt)
		}
		wdEq(t, prompts, []string{"prompt-2", "prompt-3", "prompt-4", "prompt-5", "prompt-6", "prompt-7", "prompt-8", "prompt-9"}, "prompts")
		for _, re := range []string{`newest last`, `Side questions are additive`, `evidence-backed reminders`, `not dependencies still pending or explicit holds`, `prompt-9`} {
			wdMatch(t, scope.render(), re)
		}
	})
	tw(t, f, "bounds each prompt and resets", func(t *testing.T) {
		scope := &watchdogScope{}
		scope.addPrompt(strings.Repeat("x", 3000), "")
		wdEq(t, len(scope.snapshot()[0].Prompt), 2000, "prompt length")
		scope.reset()
		wdEq(t, len(scope.snapshot()), 0, "entries after reset")
		wdEq(t, scope.render(), "", "render after reset")
	})
}

func TestWatchdogRenderTwins(t *testing.T) {
	const f = "watchdog-render"
	tw(t, f, "puts all LLM-needed warning fields in custom message content", func(t *testing.T) {
		content := formatWatchdogWarningContent(wdWarning{
			Severity: "blocker", Importance: "high", Category: "correctness", Source: "main", Summary: "Fix <bug>",
			Evidence: "The failing assertion says A & B differ.", RecommendedAction: "Update the parser before finalizing.",
			Agent: "main", RunID: "run-1", Stale: wdPtr(true),
		})
		wdMatch(t, content, `^<subagent_watchdog severity="blocker" importance="high" category="correctness" source="main" guidance="weigh, don't blindly obey">`)
		wdMatch(t, content, `<summary>Fix &lt;bug&gt;</summary>`)
		wdMatch(t, content, `<evidence>The failing assertion says A &amp; B differ\.</evidence>`)
		wdMatch(t, content, `<recommended_action>Update the parser before finalizing\.</recommended_action>`)
		wdNoMatch(t, content, `confidence`)
		wdMatch(t, content, `<agent>main</agent>`)
		wdMatch(t, content, `<run_id>run-1</run_id>`)
		wdMatch(t, content, `<stale>true</stale>`)
		wdNoMatch(t, content, `auto_follow`)
		wdMatch(t, content, `<blocker_guidance>`)
	})
	tw(t, f, "keeps structured details alongside the LLM-visible content", func(t *testing.T) {
		message := createWatchdogWarningMessage(wdWarning{Severity: "concern", Importance: "medium", Summary: "Missing focused test",
			Evidence: "The parser changed without a unit test.", RecommendedAction: "Add a parser regression test."}, wdMessageOptions{})
		wdEq(t, message.CustomType, subagentWatchdogWarningType, "customType")
		wdEq(t, message.Display, true, "display")
		wdEq(t, message.Details.Category, "other", "category")
		wdEq(t, message.Details.Source, "main", "source")
		wdMatch(t, message.Content, `<summary>Missing focused test</summary>`)
		wdMatch(t, message.Content, `<recommended_action>Add a parser regression test\.</recommended_action>`)
	})
	tw(t, f, "renders concern, blocker, stale, failed, and stalemate states in text", func(t *testing.T) {
		base := wdWarning{Severity: "blocker", Importance: "low", Category: "loop-risk", Source: "main", Summary: "Repeated blocker",
			Evidence: "The same issue survived another continuation.", RecommendedAction: "Ask for user review.", State: "stalemate", StalemateRepeats: wdPtr(3)}
		with := func(mod func(*wdWarning)) string { w := base; mod(&w); return formatWatchdogWarningRenderText(w) }
		stalemate := formatWatchdogWarningRenderText(base)
		stale := with(func(w *wdWarning) { w.Severity, w.State, w.Stale = "concern", "stale", wdPtr(true) })
		failed := with(func(w *wdWarning) { w.State, w.Error = "failed", "provider aborted" })
		displayed := with(func(w *wdWarning) { w.State = "displayed" })
		wdMatch(t, stalemate, `Subagent watchdog Blocker \(stalemate\): Repeated blocker`)
		wdMatch(t, stalemate, `Importance: Low`)
		wdMatch(t, stalemate, `Same warning 3 times in a row; the watchdog stopped continuing the run\.`)
		wdMatch(t, stale, `Subagent watchdog Concern \(stale\): Repeated blocker`)
		wdMatch(t, stale, `arrived after the watchdog catch-up timeout\.`)
		wdMatch(t, failed, `failed review`)
		wdMatch(t, failed, `Failure: provider aborted`)
		wdMatch(t, displayed, `Subagent watchdog Blocker \(displayed\): Repeated blocker`)
	})
}

func wdGuardWarning(mod func(*wdWarning)) wdWarning {
	w := wdWarning{Severity: "concern", Summary: "Unvalidated file write", Evidence: "The implementation writes settings without a test.",
		RecommendedAction: "Add a focused settings write regression test."}
	if mod != nil {
		mod(&w)
	}
	return w
}

func TestWatchdogEmissionGuardTwins(t *testing.T) {
	const f = "watchdog-emission-guard"
	tw(t, f, "normalizes text with NFKC, punctuation folding, lowercase, and whitespace collapse", func(t *testing.T) {
		wdEq(t, normalizeWatchdogEmissionText("  Ｆｉｘ—THE\tBug!!!  "), "fix the bug", "normalized")
	})
	tw(t, f, "suppresses content-free and empty emissions", func(t *testing.T) {
		want := watchdogEmissionDecision{Reason: "content-free"}
		wdEq(t, newWatchdogEmissionGuard(nil).evaluate(wdGuardWarning(func(w *wdWarning) { w.Summary = "No concerns." }), ""), want, "summary")
		wdEq(t, newWatchdogEmissionGuard(nil).evaluate(wdGuardWarning(func(w *wdWarning) { w.Evidence = "LGTM" }), ""), want, "evidence")
		wdEq(t, newWatchdogEmissionGuard(nil).evaluate(wdGuardWarning(func(w *wdWarning) { w.RecommendedAction = "" }), ""), want, "action")
	})
	tw(t, f, "dedupes normalized repeated warnings across model updates", func(t *testing.T) {
		guard := newWatchdogEmissionGuard(nil)
		wdTrue(t, guard.evaluate(wdGuardWarning(func(w *wdWarning) { w.Summary, w.Evidence = "Fix Ｆｏｏ!!!", "Line 1: bad whitespace." }), "").Accepted, "first accepted")
		guard.startModelUpdate()
		dup := guard.evaluate(wdGuardWarning(func(w *wdWarning) { w.Summary, w.Evidence = "fix foo", "line 1 bad whitespace" }), "")
		wdEq(t, dup.Accepted, false, "accepted")
		wdEq(t, dup.Reason, "duplicate", "reason")
	})
	tw(t, f, "allows concern to blocker escalation for the same issue despite per-update budget", func(t *testing.T) {
		guard := newWatchdogEmissionGuard(nil)
		first := guard.evaluate(wdGuardWarning(func(w *wdWarning) { w.Severity, w.Summary, w.Evidence = "concern", "Tests missing", "No unit covers the parser." }), "")
		escalation := guard.evaluate(wdGuardWarning(func(w *wdWarning) { w.Severity, w.Summary, w.Evidence = "blocker", "Tests missing", "No unit covers the parser." }), "")
		extra := guard.evaluate(wdGuardWarning(func(w *wdWarning) { w.Severity, w.Summary, w.Evidence = "blocker", "Docs missing", "No README section exists." }), "")
		wdEq(t, first.Accepted, true, "first")
		wdEq(t, escalation.Accepted, true, "escalation accepted")
		wdEq(t, escalation.Escalation, true, "escalation")
		wdEq(t, extra.Accepted, false, "extra")
		wdEq(t, extra.Reason, "update-budget", "extra reason")
	})
	tw(t, f, "enforces max warnings while still allowing escalation of an accepted concern", func(t *testing.T) {
		guard := newWatchdogEmissionGuard(wdPtr(1))
		wdEq(t, guard.evaluate(wdGuardWarning(func(w *wdWarning) { w.Severity, w.Summary, w.Evidence = "concern", "Parser gap", "Unknown fields are ignored." }), "").Accepted, true, "first")
		guard.startModelUpdate()
		capped := guard.evaluate(wdGuardWarning(func(w *wdWarning) { w.Severity, w.Summary, w.Evidence = "concern", "Renderer gap", "Stale state is hidden." }), "")
		escalation := guard.evaluate(wdGuardWarning(func(w *wdWarning) { w.Severity, w.Summary, w.Evidence = "blocker", "Parser gap", "Unknown fields are ignored." }), "")
		wdEq(t, capped.Accepted, false, "capped")
		wdEq(t, capped.Reason, "max-warnings", "capped reason")
		wdEq(t, escalation.Accepted, true, "escalation accepted")
		wdEq(t, escalation.Escalation, true, "escalation")
	})
}

func TestWatchdogStableHostLayoutTwins(t *testing.T) {
	const f = "watchdog-stable-host-layout"
	tskip(t, f, "watchdog modules link on the stable host surface without newer pi-ai exports",
		"Node module-loader test: it links review.ts and permission-arbiter.ts through a --loader that hides pi-ai exports newer than the stable host. A Go package has no module graph that links at run time; the surface the port uses (ctx.ModelRegistry() Find, GetAvailable, HasConfiguredAuth, GetApiKeyAndHeaders, StreamSimple) is the one every build compiles against")
}
