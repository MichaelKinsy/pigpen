package posthorse

// Layer-1 tests for the cases of the original's real-Pi suite (test/official-host.ts) that no RPC scenario can
// reach: they need an in-process model router, an injected policy reader, a mocked session manager, or a loader that
// tolerates a duplicate tool name. Each test names its upstream title and line; the rest of that suite runs as
// recorded scenarios (port/scenarios/off-*.json, see port/coverage-ScenarioOfficial.md).

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// handoffLimit asks new_context for a 20,000-char handoff and returns the limit its refusal names.
func handoffLimit(t *testing.T, f *fixture) int {
	t.Helper()
	_, failure := f.RunErr("new_context", rec{"handoff": strings.Repeat("x", 20_000)})
	m := regexp.MustCompile(`\(20,000 characters; limit ([\d,]+)\)`).FindStringSubmatch(failure)
	if m == nil {
		t.Fatalf("want a 'too large' refusal naming its limit, got %q", failure)
	}
	n, err := strconv.Atoi(strings.ReplaceAll(m[1], ",", ""))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// official-host.ts:409 'routed guidance and handoff admission use native context capacity': under a routed (virtual)
// model the host's context usage names the physical limit (272,000) while the selected declaration says 100,000;
// guidance, the remaining-context tool and the handoff admission follow the native capacity, and a smaller physical
// route refuses an oversized handoff.
func TestOfficialHostRoutedGuidanceAndAdmissionUseNativeCapacity(t *testing.T) {
	f := newFixture(t)
	routed := f.With(withUsage(272_000, 272_000), withModelWindow(100_000), withReserve(16_384), withEnabled(true))
	guidance := routed.GuidanceFor()
	coreMatch(t, guidance, `Configured context capacity: 272,000 tokens`)
	coreMatch(t, guidance, `rollover line is 255,617 tokens used`)
	coreNoMatch(t, guidance, `100,000|83,617`)
	coreMatch(t, routed.Run("get_context_remaining", rec{}).Text(), `272,000 used`)

	small := f.With(withUsage(8_192, 100), withModelWindow(100_000), withEnabled(false))
	coreFails(t, small, "new_context", rec{"handoff": strings.Repeat("x", 20_000)}, `Handoff is too large for the active model`)
}

// official-host.ts:484 'explicit handoff admission counts the real prompt and active tool schemas': the system prompt
// the request will carry and the schemas of the ACTIVE tools reduce the handoff limit one for one (a token of
// either shrinks the handoff: four prompt characters are a token and the handoff gets half of what is left).
func TestOfficialHostHandoffAdmissionCountsPromptAndActiveToolSchemas(t *testing.T) {
	f := newFixture(t)
	base := []fixOpt{withUsage(32_768, 100), withModelWindow(32_768), withEnabled(false)}
	small := handoffLimit(t, f.With(append(base, withSystemPrompt(strings.Repeat("s", 90_000)))...))
	large := handoffLimit(t, f.With(append(base, withSystemPrompt(strings.Repeat("s", 98_000)))...))
	// 8,000 more prompt characters are 2,000 tokens; the handoff gets half of what is left, four characters a token.
	if delta := small - large; delta < 3_990 || delta > 4_010 {
		t.Fatalf("limits %d and %d: 8,000 prompt characters must cost about 4,000 handoff characters", small, large)
	}
	prompt := withSystemPrompt(strings.Repeat("s", 90_000))
	all := handoffLimit(t, f.With(append(base, prompt)...))
	only := handoffLimit(t, f.With(append(base, prompt, withActiveTools("new_context"))...))
	if only <= all {
		t.Fatalf("one active tool (%d) must leave more room than all of them (%d)", only, all)
	}
	// The limit is the one the boundary enforces: a handoff at the limit is accepted, one over it is refused.
	view := f.With(append(base, prompt)...)
	_, failure := view.RunErr("new_context", rec{"handoff": strings.Repeat("y", all)})
	if failure != "" {
		t.Fatalf("a handoff at its limit must be accepted: %s", failure)
	}
}

// official-host.ts:605 'a colliding foreign new_context cannot authorize Posthorse rollover (before|after)': a
// result named new_context that Posthorse's own tool did not request (a foreign extension's tool of the same name)
// never commits a reset, before or after Posthorse's own call settles.
func TestOfficialHostForeignNewContextCannotAuthorizeRollover(t *testing.T) {
	foreign := rec{"toolName": "new_context", "toolCallId": "survey-call", "details": rec{"foreign": true}, "content": []any{fxText("FOREIGN_RESULT")}}
	t.Run("no Posthorse request exists", func(t *testing.T) {
		f := newFixture(t)
		if got := f.TurnEnd(foreign); got != nil {
			t.Fatalf("a foreign new_context result must not reset, got %v", got)
		}
	})
	t.Run("a foreign result next to Posthorse's own one is ignored", func(t *testing.T) {
		f := newFixture(t)
		f.Run("new_context", rec{"handoff": "COLLIDING_ARGUMENT"})
		got := f.TurnEnd(foreign, roToolResult())
		entries, _ := got["entries"].([]any)
		if len(entries) != 1 || roRec(entries[0])["summary"] != "COLLIDING_ARGUMENT" {
			t.Fatalf("only Posthorse's own result commits its handoff, got %v", got)
		}
	})
}

// official-host.ts:2322 'automatic recovery uses event settings even when proactive settings disagree': the injected
// reader (disabled, reserve 99,999) drives guidance and the remaining-context tool, while the automatic hook uses the
// settings Pi puts on the event.
func TestOfficialHostRecoveryUsesEventSettingsWhenProactiveSettingsDisagree(t *testing.T) {
	disagree := func(sdk.Context) (compactionPolicy, error) {
		return compactionPolicy{enabled: false, reserveTokens: 99_999}, nil
	}
	branch := []rec{fxMessageEntry("u1", fxUser("keep working on the fix"))}
	f := newFixture(t, withPolicyReader(disagree), withProjection(branch...))
	coreMatch(t, f.GuidanceFor(), `disabled in the available settings`)
	coreMatch(t, f.Run("get_context_remaining", rec{}).Text(), `disabled in the available settings`)
	got := f.Fire("session_before_compact", roCompactEvent("threshold", 20_000, "u1", branch...))
	roSameJSON(t, roRec(roRec(got["compaction"])["details"]), rec{"posthorse": float64(1), "reason": "threshold"}, "the event's settings (enabled) rule the recovery")
}

// official-host.ts:2347 'unexpected recovery-builder failure cancels instead of falling through to a summarizer': a
// failure while the projection is read cancels the compaction (no summarizer runs) and is reported.
func TestOfficialHostRecoveryBuilderFailureCancelsAndReports(t *testing.T) {
	finish := roStderr(t)
	branch := []rec{fxMessageEntry("u1", fxUser("keep working on the fix"))}
	f := newFixture(t, withHostMode("print"), withHostError("sessionRead.buildSessionProjection", "INJECTED_PROJECTION_FAILURE"))
	got := f.Fire("session_before_compact", roCompactEvent("overflow", 1, "u1", branch...))
	roEqual(t, got["cancel"], true, "the hook cancels instead of letting Pi summarize")
	if text := finish(); !strings.Contains(text, "rollover cancelled: INJECTED_PROJECTION_FAILURE") {
		t.Fatalf("the failure must be reported, got %q", text)
	}
}

// official-host.ts:1172 'automatic retention drops an incomplete projected tool group (omitted call|result)': a
// receipt whose call the projection omitted is never retained (and is named as an orphan in the record); a receipt
// that is itself omitted never appears.
func TestOfficialHostIncompleteProjectedToolGroup(t *testing.T) {
	owner := fxMessageEntry("o1", fxUser(strings.Repeat("o", 400_000)))
	later := fxMessageEntry("o2", fxUser("LATER_OWNER_INPUT"))
	call := fxMessageEntry("c1", fxAssistant([]any{fxToolCall("omitted-call", "read", rec{})}))
	result := fxMessageEntry("r1", fxToolResult("omitted-call", "read", []any{fxText("LEGACY_ORPHAN_RECEIPT")}, false))
	branch := []rec{owner, later, call, result}
	t.Run("omitted call", func(t *testing.T) {
		f := newFixture(t)
		got := f.AutomaticResult(branch, owner, later, result)
		text, _ := roRec(got["compaction"])["summary"].(string)
		coreMatch(t, text, `LEGACY_ORPHAN_RECEIPT`)
		coreMatch(t, text, `No matching projected call`)
	})
	t.Run("omitted result", func(t *testing.T) {
		f := newFixture(t)
		got := f.AutomaticResult(branch, owner, later, call)
		text, _ := roRec(got["compaction"])["summary"].(string)
		coreNoMatch(t, text, `LEGACY_ORPHAN_RECEIPT`)
	})
}
