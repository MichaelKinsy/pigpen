package pi_subagents

import (
	"encoding/json"
	"strings"
	"testing"
)

// Twins of test/unit/watchdog-child-status.test.ts.

func wdSummary(severity, importance, summary, evidence, action string, addressed, stalemate bool) *childWatchdogWarningSummary {
	return &childWatchdogWarningSummary{Severity: severity, Importance: importance, Category: "correctness", Summary: summary, Evidence: evidence, RecommendedAction: action, Addressed: addressed, Stalemate: stalemate}
}

func wdBaseChildConfig() *watchdogConfig {
	c := defaultWatchdogConfig()
	c.Enabled = true
	c.Children.Enabled = true
	return c
}

func TestWatchdogChildStatusTwins(t *testing.T) {
	const f = "watchdog-child-status"
	tw(t, f, "marks status-event findings addressed on assistant turns", func(t *testing.T) {
		snapshot := acceptChildWatchdogEvent(acceptChildWatchdogInput{Event: childWatchdogStatusEvent{Type: childWatchdogStatusEventType, Seq: 1, Phase: "idle", Ts: 5,
			Warning: wdSummary("blocker", "high", "Unverified tests", "No test command.", "Run tests.", false, true)}})
		if applyChildWatchdogMessage(nil, wdObj(t, `{"role":"assistant","content":[]}`)) != nil {
			t.Error("a message without progress changed it")
		}
		if applyChildWatchdogMessage(nil, wdObj(t, `{"role":"custom","customType":"subagent-notify","details":{}}`)) != nil {
			t.Error("a custom message without progress changed it")
		}
		addressed := applyChildWatchdogMessage(snapshot, wdObj(t, `{"role":"assistant"}`))
		if addressed == nil {
			t.Fatal("assistant turn did not address the warning")
		}
		for _, w := range addressed.Warnings {
			wdEq(t, w.Addressed, true, "addressed")
		}
		wdEq(t, len(childWatchdogBlockers(addressed)), 1, "stalemate remains unresolved")
		if applyChildWatchdogMessage(addressed, wdObj(t, `{"role":"assistant"}`)) != nil {
			t.Error("nothing left to address")
		}
	})
	tw(t, f, "retains bounded findings in audit status but projects only high importance to the parent model", func(t *testing.T) {
		var snapshot *childWatchdogProgress
		for i := 0; i < childWatchdogWarningLimit+2; i++ {
			importance := []string{"high", "medium", "low"}[i%3]
			severity := "concern"
			if i == 4 {
				severity = "blocker"
			}
			snapshot = acceptChildWatchdogEvent(acceptChildWatchdogInput{Current: snapshot, Event: childWatchdogStatusEvent{Type: childWatchdogStatusEventType, Seq: i + 1, Phase: "idle", Ts: float64(i),
				Warning: wdSummary(severity, importance, importance+"-summary-"+itoa(i), importance+"-evidence-"+itoa(i), importance+"-action-"+itoa(i), false, false)}})
		}
		wdEq(t, len(snapshot.Warnings), childWatchdogWarningLimit, "retained warnings")
		wdEq(t, len(childWatchdogBlockers(snapshot)), 1, "low/medium blockers still gate acceptance")
		b, _ := json.Marshal(childWatchdogProgressForModel(snapshot))
		serialized := string(b)
		wdNoMatch(t, serialized, `low-(summary|evidence|action)|medium-(summary|evidence|action)`)
		wdMatch(t, serialized, `high-summary`)
	})
	tw(t, f, "resolves child cadence from override, then children, then the top-level cadence", func(t *testing.T) {
		cadence := func(c *watchdogConfig, agent string) wdCadence {
			r := resolveChildWatchdogConfig(resolveChildWatchdogInput{Config: c, Agent: agent})
			if r == nil {
				t.Fatal("no child config")
			}
			return r.Cadence
		}
		base := wdBaseChildConfig()
		wdEq(t, cadence(base, "worker"), wdCadence{}, "default")
		root := wdBaseChildConfig()
		root.Cadence = wdCadence{EveryNTools: wdPtr(20)}
		wdEq(t, cadence(root, "worker"), wdCadence{EveryNTools: wdPtr(20)}, "root")
		children := wdBaseChildConfig()
		children.Cadence = wdCadence{EveryNTools: wdPtr(20)}
		children.Children.Cadence = &wdCadence{EveryNTools: wdPtr(10)}
		wdEq(t, cadence(children, "worker"), wdCadence{EveryNTools: wdPtr(10)}, "children")
		override := wdBaseChildConfig()
		override.Cadence = children.Cadence
		override.Children.Cadence = children.Children.Cadence
		override.Children.Overrides = []wdOverrideEntry{{"worker", wdChildOverride{Cadence: &wdCadence{EveryNTools: wdPtr(5)}}}}
		wdEq(t, cadence(override, "worker"), wdCadence{EveryNTools: wdPtr(5)}, "override")
		wdEq(t, cadence(override, "reviewer"), wdCadence{EveryNTools: wdPtr(10)}, "another agent")
	})
	tw(t, f, "preserves child model and explicit override thinking when resolving config", func(t *testing.T) {
		c := defaultWatchdogConfig()
		c.Enabled = true
		c.Children.Enabled = true
		c.Children.Model = "openai/gpt-test-child"
		c.Children.Thinking = wdThinkingLevel("low")
		c.Children.Overrides = []wdOverrideEntry{{"worker", wdChildOverride{Model: "anthropic/claude-test-worker", Thinking: wdThinkingOff}}}
		config := resolveChildWatchdogConfig(resolveChildWatchdogInput{Config: c, Agent: "worker", RunID: "run-1", ChildIndex: wdPtr(0)})
		if config == nil {
			t.Fatal("no child config")
		}
		wdEq(t, config.Model, "anthropic/claude-test-worker", "model")
		wdEq(t, config.Thinking, wdThinkingOff, "thinking")
		wdEq(t, config.Lsp, defaultWatchdogConfig().Lsp, "lsp")
	})
	tw(t, f, "decodes child watchdog config and rejects malformed enabled payloads", func(t *testing.T) {
		payload := `{"runId":"run-1","agent":"worker","childIndex":1,"watchdogTailTimeoutMs":100,"agentEndTimeoutMs":200,"maxWarnings":null,"lsp":{"enabled":false,"timeoutMs":50,"maxFiles":2,"maxDiagnostics":3},"stalemateRepeats":3,"cadence":{"everyNTools":10}}`
		config, err := decodeChildWatchdogConfig(payload)
		if err != nil {
			t.Fatal(err)
		}
		wdEq(t, config, &childWatchdogConfig{RunID: "run-1", Agent: "worker", ChildIndex: wdPtr(1), WatchdogTailTimeoutMs: 100, AgentEndTimeoutMs: 200,
			Lsp: wdLsp{Enabled: false, TimeoutMs: 50, MaxFiles: 2, MaxDiagnostics: 3}, StalemateRepeats: 3, Cadence: wdCadence{EveryNTools: wdPtr(10)}}, "config")
		off, err := decodeChildWatchdogConfig(`{"enabled":false}`)
		wdEq(t, off == nil && err == nil, true, "enabled false")
		for _, c := range []struct{ from, to, want string }{
			{`"timeoutMs":50`, `"timeoutMs":0`, `lsp\.timeoutMs`},
			{`"stalemateRepeats":3`, `"stalemateRepeats":0`, `stalemateRepeats`},
			{`"everyNTools":10`, `"everyNTools":3`, `cadence\.everyNTools`},
		} {
			_, err := decodeChildWatchdogConfig(strings.Replace(payload, c.from, c.to, 1))
			if err == nil {
				t.Errorf("%s accepted", c.to)
				continue
			}
			wdMatch(t, err.Error(), c.want)
		}
	})
	tw(t, f, "rejects removed fallbackModels in child watchdog config", func(t *testing.T) {
		_, err := decodeChildWatchdogConfig(`{"enabled":false,"fallbackModels":["model/backup"]}`)
		if err == nil {
			t.Fatal("accepted")
		}
		wdMatch(t, err.Error(), `fallbackModels was removed; configure one model instead`)
	})
	tw(t, f, "accepts latest matching status events and drops stale or foreign events", func(t *testing.T) {
		first := childWatchdogStatusEvent{Type: childWatchdogStatusEventType, RunID: "run-1", Agent: "worker", ChildIndex: wdPtr(0), Seq: 1, Phase: "reviewing", Ts: 10}
		wdEq(t, isChildWatchdogStatusEvent(wdObj(t, `{"type":"subagent.watchdog.status","runId":"run-1","agent":"worker","childIndex":0,"seq":1,"phase":"reviewing","ts":10}`)), true, "valid event")
		in := func(cur *childWatchdogProgress, e childWatchdogStatusEvent) acceptChildWatchdogInput {
			return acceptChildWatchdogInput{Event: e, Current: cur, RunID: "run-1", Agent: "worker", ChildIndex: wdPtr(0)}
		}
		accepted := acceptChildWatchdogEvent(in(nil, first))
		wdEq(t, accepted, &childWatchdogProgress{Phase: "reviewing", Seq: 1, LastUpdate: 10}, "first")
		wdEq(t, childWatchdogIsActive(accepted), true, "active")
		with := func(mod func(*childWatchdogStatusEvent)) childWatchdogStatusEvent { e := first; mod(&e); return e }
		wdEq(t, acceptChildWatchdogEvent(in(accepted, first)) == nil, true, "repeated seq")
		wdEq(t, acceptChildWatchdogEvent(in(accepted, with(func(e *childWatchdogStatusEvent) { e.Seq, e.RunID = 2, "" }))) == nil, true, "missing runId")
		wdEq(t, acceptChildWatchdogEvent(in(accepted, with(func(e *childWatchdogStatusEvent) { e.Seq, e.Agent = 2, "" }))) == nil, true, "missing agent")
		wdEq(t, acceptChildWatchdogEvent(in(accepted, with(func(e *childWatchdogStatusEvent) { e.Seq, e.ChildIndex = 2, nil }))) == nil, true, "missing childIndex")
		wdEq(t, acceptChildWatchdogEvent(in(accepted, with(func(e *childWatchdogStatusEvent) { e.Seq, e.Agent = 2, "other" }))) == nil, true, "other agent")
		settled := acceptChildWatchdogEvent(in(accepted, with(func(e *childWatchdogStatusEvent) { e.Seq, e.Phase, e.Ts = 2, "idle", 20 })))
		wdEq(t, settled.Phase, "idle", "settled phase")
		wdEq(t, childWatchdogIsActive(settled), false, "settled")
	})
	tw(t, f, "rejects malformed status warning values without throwing", func(t *testing.T) {
		event := func(extra string) any {
			return wdObj(t, `{"type":"subagent.watchdog.status","seq":1,"phase":"idle","ts":1`+extra+`}`)
		}
		for _, w := range []string{`null`, `[]`, `"warning"`, `1`, `true`} {
			wdEq(t, isChildWatchdogStatusEvent(event(`,"warning":`+w)), false, "warning "+w)
		}
		wdEq(t, isChildWatchdogStatusEvent(nil), false, "null")
		wdEq(t, isChildWatchdogStatusEvent(wdObj(t, `[]`)), false, "array")
		warning := `{"severity":"concern","importance":"low","category":"other","summary":"summary","evidence":"evidence","recommendedAction":"act","addressed":false,"stalemate":false`
		wdEq(t, isChildWatchdogStatusEvent(event(`,"warning":`+warning+`,"displayedAt":1}`)), false, "numeric displayedAt")
		wdEq(t, isChildWatchdogStatusEvent(event(`,"warning":`+warning+`,"displayedAt":"2026-09-13T00:00:00.000Z"}`)), true, "string displayedAt")
	})
}
