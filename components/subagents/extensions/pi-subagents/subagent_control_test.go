package pi_subagents

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// Twins of subagent-control.test.ts.

func fl(v float64) *float64 { return &v }

func ctlConfig(override wfControlConfig) wfControlConfig {
	return wfResolveControlConfig(nil, &override)
}

func TestSubagentControlTwins(t *testing.T) {
	const f = "subagent-control"
	config := ctlConfig(wfControlConfig{NeedsAttentionAfterMs: fl(300)})
	i64 := func(v int64) *int64 { return &v }
	turns := func(v int) *int { return &v }
	match := func(t *testing.T, text, pattern string) {
		t.Helper()
		polMatches(t, text, pattern)
	}

	tw(t, f, "marks a run as needing attention only after the idle threshold", func(t *testing.T) {
		equal(t, deriveActivityState(activityInput{Config: config, StartedAt: 0, LastActivityAt: i64(0), Now: 50}), "", "no turns")
		equal(t, deriveActivityState(activityInput{Config: config, StartedAt: 0, LastActivityAt: i64(0), TurnCount: turns(1), Now: 400}), "needs_attention", "idle")
		equal(t, deriveActivityState(activityInput{Config: config, StartedAt: 0, LastActivityAt: i64(0), CurrentTool: "bash", Now: 400}), "", "open tool")
		equal(t, deriveActivityState(activityInput{Config: config, StartedAt: 0, TurnCount: turns(1), Now: 400}), "needs_attention", "from the start")
	})

	tw(t, f, "does not mark a zero-turn run as needing attention", func(t *testing.T) {
		equal(t, deriveActivityState(activityInput{Config: config, StartedAt: 0, LastActivityAt: i64(0), TurnCount: turns(0), Now: 400}), "", "zero turns")
		equal(t, deriveActivityState(activityInput{Config: config, StartedAt: 0, LastActivityAt: i64(0), Now: 400}), "", "no turn count")
	})

	tw(t, f, "builds compact needs-attention control events", func(t *testing.T) {
		got := buildControlEvent(controlEventInput{To: "needs_attention", RunID: "run-1", Agent: "worker", Index: turns(2), TS: 1000, LastActivityAt: i64(100)})
		equal(t, got, controlEvent{Type: "needs_attention", To: "needs_attention", TS: 1000, RunID: "run-1", Agent: "worker", Index: turns(2),
			Message: "worker needs attention (no observed activity for 0s)", Reason: "idle", ElapsedMs: i64(900)}, "event")
	})

	tw(t, f, "supports a specific attention message", func(t *testing.T) {
		got := buildControlEvent(controlEventInput{To: "needs_attention", RunID: "run-1", Agent: "worker", Message: "worker needs attention"})
		equal(t, got.Message, "worker needs attention", "message")
	})

	tw(t, f, "defaults notifications to active-long-running and needs attention", func(t *testing.T) {
		event := buildControlEvent(controlEventInput{To: "needs_attention", RunID: "run-1", Agent: "worker"})
		active := buildControlEvent(controlEventInput{Type: "active_long_running", To: "active_long_running", RunID: "run-1", Agent: "worker"})
		equal(t, shouldNotifyControlEvent(config, event), true, "needs attention")
		equal(t, shouldNotifyControlEvent(config, active), true, "active")
		equal(t, *config.NotifyOn, []string{"active_long_running", "needs_attention"}, "notifyOn")
		equal(t, *config.NotifyChannels, []string{"event", "async", "intercom"}, "notifyChannels")
	})

	tw(t, f, "defaults active-long-running notices to elapsed time only", func(t *testing.T) {
		defaults := wfResolveControlConfig(nil, nil)
		equal(t, *defaults.ActiveNoticeAfterMs, float64(240000), "ms")
		equal(t, defaults.ActiveNoticeAfterTurns == nil && defaults.ActiveNoticeAfterTokens == nil, true, "no turn or token thresholds")
		equal(t, nextLongRunningTrigger(defaults, longRunningMetrics{StartedAt: 0, Now: 77000, Turns: 50, Tokens: 800000}), "", "below the time threshold")
		equal(t, nextLongRunningTrigger(defaults, longRunningMetrics{StartedAt: 0, Now: 240000, Turns: 1, Tokens: 1}), "time_threshold", "at the time threshold")
	})

	tw(t, f, "scales the default idle threshold for higher thinking levels", func(t *testing.T) {
		defaults := wfResolveControlConfig(nil, nil)
		derive := func(thinking string, now int64) string {
			return deriveActivityState(activityInput{Config: defaults, StartedAt: 0, TurnCount: turns(1), Thinking: thinking, Now: now})
		}
		equal(t, derive("", 60001), "needs_attention", "default")
		equal(t, derive("low", 60001), "needs_attention", "low")
		equal(t, derive("minimal", 60001), "needs_attention", "minimal")
		equal(t, derive("high", 60001), "", "high")
		equal(t, derive("high", 300001), "needs_attention", "high, later")
	})

	tw(t, f, "keeps explicit idle threshold overrides higher priority than thinking scale", func(t *testing.T) {
		explicit := ctlConfig(wfControlConfig{NeedsAttentionAfterMs: fl(90000)})
		equal(t, *explicit.NeedsAttentionAfterMsIsExplicit, true, "explicit")
		equal(t, deriveActivityState(activityInput{Config: explicit, StartedAt: 0, TurnCount: turns(1), Thinking: "high", Now: 90001}), "needs_attention", "state")
	})

	tw(t, f, "preserves inherited default idle scaling when merging a resolved control config", func(t *testing.T) {
		inherited := wfResolveControlConfig(nil, nil)
		merged := wfResolveControlConfig(&inherited, &wfControlConfig{ActiveNoticeAfterMs: fl(123000)})
		equal(t, *merged.NeedsAttentionAfterMsIsExplicit, false, "explicit")
		equal(t, *merged.ActiveNoticeAfterMs, float64(123000), "active notice")
		equal(t, deriveActivityState(activityInput{Config: merged, StartedAt: 0, TurnCount: turns(1), Thinking: "high", Now: 60001}), "", "state")
	})

	tw(t, f, "treats recovered resolved configs without explicitness metadata as fixed thresholds", func(t *testing.T) {
		recovered := ctlConfig(wfControlConfig{NeedsAttentionAfterMs: fl(90000)})
		recovered.NeedsAttentionAfterMsIsExplicit = nil
		equal(t, deriveActivityState(activityInput{Config: recovered, StartedAt: 0, TurnCount: turns(1), Thinking: "high", Now: 90001}), "needs_attention", "state")
	})

	tw(t, f, "marks non-exempt open tools for attention at the active threshold", func(t *testing.T) {
		defaults := wfResolveControlConfig(nil, nil)
		equal(t, shouldEmitOpenToolAttention(defaults, "bash", i64(0), 239999), false, "before")
		equal(t, shouldEmitOpenToolAttention(defaults, "bash", i64(0), 240000), true, "at")
		equal(t, shouldEmitOpenToolAttention(defaults, "contact_supervisor", i64(0), 999999), false, "exempt tool")
		off := false
		disabled := defaults
		disabled.Enabled = &off
		equal(t, shouldEmitOpenToolAttention(disabled, "bash", i64(0), 999999), false, "disabled")
	})

	tw(t, f, "supports opt-in turn and token long-running thresholds", func(t *testing.T) {
		tokenBudget := ctlConfig(wfControlConfig{ActiveNoticeAfterMs: fl(999999), ActiveNoticeAfterTokens: fl(500000)})
		turnBudget := ctlConfig(wfControlConfig{ActiveNoticeAfterMs: fl(999999), ActiveNoticeAfterTurns: fl(5)})
		equal(t, nextLongRunningTrigger(tokenBudget, longRunningMetrics{StartedAt: 0, Now: 77000, Turns: 1, Tokens: 500000}), "token_threshold", "tokens")
		equal(t, nextLongRunningTrigger(turnBudget, longRunningMetrics{StartedAt: 0, Now: 77000, Turns: 5, Tokens: 1}), "turn_threshold", "turns")
	})

	tw(t, f, "resolves custom notification config", func(t *testing.T) {
		on := []string{"active_long_running", "needs_attention", "nope"}
		ch := []string{"event", "intercom", "bad"}
		custom := ctlConfig(wfControlConfig{NeedsAttentionAfterMs: fl(1234), ActiveNoticeAfterMs: fl(2345), ActiveNoticeAfterTurns: fl(7), ActiveNoticeAfterTokens: fl(8000),
			FailedToolAttemptsBeforeAttention: fl(4), NotifyOn: &on, NotifyChannels: &ch})
		equal(t, [5]float64{*custom.NeedsAttentionAfterMs, *custom.ActiveNoticeAfterMs, *custom.ActiveNoticeAfterTurns, *custom.ActiveNoticeAfterTokens, *custom.FailedToolAttemptsBeforeAttention},
			[5]float64{1234, 2345, 7, 8000, 4}, "numbers")
		equal(t, *custom.NotifyOn, []string{"active_long_running", "needs_attention"}, "notifyOn")
		equal(t, *custom.NotifyChannels, []string{"event", "intercom"}, "notifyChannels")
	})

	tw(t, f, "falls back to defaults for invalid non-empty notification arrays", func(t *testing.T) {
		bogus := []string{"bogus"}
		custom := ctlConfig(wfControlConfig{NotifyOn: &bogus, NotifyChannels: &bogus})
		equal(t, *custom.NotifyOn, []string{"active_long_running", "needs_attention"}, "notifyOn")
		equal(t, *custom.NotifyChannels, []string{"event", "async", "intercom"}, "notifyChannels")
	})

	tw(t, f, "allows empty notification arrays to disable notifications", func(t *testing.T) {
		empty := []string{}
		custom := ctlConfig(wfControlConfig{NotifyOn: &empty, NotifyChannels: &empty})
		event := buildControlEvent(controlEventInput{To: "needs_attention", RunID: "run-1", Agent: "worker"})
		equal(t, *custom.NotifyOn, []string{}, "notifyOn")
		equal(t, *custom.NotifyChannels, []string{}, "notifyChannels")
		equal(t, shouldNotifyControlEvent(custom, event), false, "notify")
	})

	tw(t, f, "formats control notices with a proactive hint and concrete commands", func(t *testing.T) {
		event := buildControlEvent(controlEventInput{To: "needs_attention", RunID: "78f659a3", Agent: "worker"})
		message := formatControlNoticeMessage(event, "subagent-worker-78f659a3")
		for _, p := range []string{
			`Subagent needs attention: worker`,
			`Hint: Inspect status first unless the run is clearly blocked`,
			`steer for a top-level live async child, routed resume for a live nested child`,
			`Top-level live async nudge: subagent\(\{ action: "steer", id: "78f659a3", message: "What are you blocked on\?`,
			`Routed live nested nudge: subagent\(\{ action: "resume", id: "78f659a3", message: "What are you blocked on\?`,
			`Direct intercom target: subagent-worker-78f659a3`,
			`Status: subagent\(\{ action: "status", id: "78f659a3" \}\)`,
			`Interrupt: subagent\(\{ action: "interrupt", id: "78f659a3" \}\)`,
		} {
			match(t, message, p)
		}
		polNotMatches(t, message, `Wait:`)
	})

	tw(t, f, "formats open-tool attention notices with tool facts", func(t *testing.T) {
		event := buildControlEvent(controlEventInput{To: "needs_attention", RunID: "78f659a3", Agent: "worker", Reason: "tool_open_threshold",
			Message: "worker has had tool 'bash' open for 240s", CurrentTool: "bash", CurrentToolDurationMs: i64(240000), CurrentPath: "scripts/run-tests.sh"})
		message := formatControlNoticeMessage(event, "subagent-worker-78f659a3")
		match(t, message, `worker has had tool 'bash' open for 240s`)
		match(t, message, `Facts: tool bash 240s \| path scripts/run-tests\.sh`)
		match(t, message, `message: "Check tool bash at path scripts/run-tests\.sh\. Report the smallest next step or ask for a decision\."`)
	})

	tw(t, f, "uses bounded task context in nudges and de-duplicates distinct contexts", func(t *testing.T) {
		first := buildControlEvent(controlEventInput{To: "needs_attention", RunID: "run-1", Agent: "reviewer", Label: "release gate " + strings.Repeat("x", 200)})
		second := buildControlEvent(controlEventInput{To: "needs_attention", RunID: "run-1", Agent: "reviewer", Label: "security gate"})
		message := formatControlNoticeMessage(first, "")
		m := regexp.MustCompile(`message: ("(?:[^"\\]|\\.)*")`).FindStringSubmatch(message)
		if m == nil {
			t.Fatal("no nudge in " + message)
		}
		var nudge string
		if err := json.Unmarshal([]byte(m[1]), &nudge); err != nil {
			t.Fatal(err)
		}
		if len([]rune(nudge)) > 160 {
			t.Errorf("nudge length %d", len(nudge))
		}
		if controlNotificationKey(first, "") == controlNotificationKey(second, "") {
			t.Error("distinct contexts share a key")
		}
	})

	tw(t, f, "formats supervisor-request notices with pending-channel guidance", func(t *testing.T) {
		event := buildControlEvent(controlEventInput{To: "needs_attention", RunID: "78f659a3", Agent: "worker", Reason: "supervisor_request", CurrentTool: "contact_supervisor"})
		message := formatControlNoticeMessage(event, "subagent-worker-78f659a3")
		match(t, message, `Supervisor request: reply to the pending request`)
		match(t, message, `subagent_supervisor pending`)
		match(t, message, `intercom pending`)
	})

	tw(t, f, "formats active-long-running notices as informational", func(t *testing.T) {
		n := func(v int64) *int64 { return &v }
		event := buildControlEvent(controlEventInput{Type: "active_long_running", To: "active_long_running", RunID: "78f659a3", Agent: "worker", Turns: n(15), Tokens: n(160000), ToolCount: n(42),
			CurrentTool: "edit", CurrentPath: "src/runs/background/async-status.ts", Reason: "turn_threshold"})
		message := formatControlNoticeMessage(event, "subagent-worker-78f659a3-1")
		for _, p := range []string{
			`Subagent active but long-running: worker`,
			`Inspect status`,
			`steer for a top-level live async child, routed resume for a live nested child`,
			`Top-level live async nudge: subagent\(\{ action: "steer", id: "78f659a3", message: "Check tool edit at path src/runs/background/async-status\.ts`,
			`Routed live nested nudge: subagent\(\{ action: "resume", id: "78f659a3", message: "Check tool edit at path src/runs/background/async-status\.ts`,
			`15 turns`, `160000 tokens`, `path src/runs/background/async-status\.ts`,
		} {
			match(t, message, p)
		}
		polNotMatches(t, message, `Subagent needs attention`)
	})

	tw(t, f, "formats intercom notifications with the same control commands", func(t *testing.T) {
		event := buildControlEvent(controlEventInput{To: "needs_attention", RunID: "78f659a3", Agent: "worker"})
		message := formatControlIntercomMessage(event, "subagent-worker-78f659a3")
		match(t, message, `worker needs attention in run 78f659a3`)
		match(t, message, `Top-level live async nudge: subagent\(\{ action: "steer", id: "78f659a3", message: "What are you blocked on\?`)
		match(t, message, `Routed live nested nudge: subagent\(\{ action: "resume", id: "78f659a3", message: "What are you blocked on\?`)
	})

	tw(t, f, "dedupes notifications once per child target and attention state", func(t *testing.T) {
		zero := 0
		event := buildControlEvent(controlEventInput{To: "needs_attention", RunID: "run-1", Agent: "worker", Index: &zero})
		seen := map[string]bool{}
		match(t, controlNotificationKey(event, "subagent-worker-run-1-1"), `^subagent-worker-run-1-1:needs_attention:idle:[a-f0-9]{8}$`)
		cfg := wfResolveControlConfig(nil, nil)
		equal(t, claimControlNotification(cfg, event, seen, "subagent-worker-run-1-1"), true, "first claim")
		equal(t, claimControlNotification(cfg, event, seen, "subagent-worker-run-1-1"), false, "second claim")
		terminal := buildControlEvent(controlEventInput{To: "needs_attention", RunID: "run-1", Agent: "worker", Index: &zero, Message: "worker needs attention", Reason: "tool_failures"})
		equal(t, claimControlNotification(cfg, terminal, seen, "subagent-worker-run-1-1"), true, "another reason")
	})
}
