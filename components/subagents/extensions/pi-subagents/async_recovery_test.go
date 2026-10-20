package pi_subagents

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// Twins of abort-recovery.test.ts and the unknown-action cases of subagent-action-recovery.test.ts.

type msgs = []map[string]any

func abortMessages(entries ...map[string]any) msgs { return entries }

func zeroUsage() map[string]any {
	return map[string]any{"input": 0.0, "output": 0.0, "cacheRead": 0.0, "cacheWrite": 0.0, "cost": map[string]any{"total": 0.0}}
}

func progressMsg() map[string]any {
	return map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "Implemented the helper and running validation."}},
		"usage": map[string]any{"input": 10.0, "output": 5.0, "cacheRead": 0.0, "cacheWrite": 0.0, "cost": map[string]any{"total": 0.0}}}
}

func abortedMsg() map[string]any {
	return map[string]any{"role": "assistant", "content": []any{}, "stopReason": "aborted", "usage": zeroUsage()}
}

func withStop(m map[string]any, reason string) map[string]any {
	c := map[string]any{}
	for k, v := range m {
		c[k] = v
	}
	c["stopReason"] = reason
	return c
}

// baseRecovery is base() of the test: progress then an abort, a session available, nothing resumed yet.
func baseRecovery() AbortRecoveryInput {
	return AbortRecoveryInput{Messages: abortMessages(progressMsg(), abortedMsg()), SessionAvailable: true}
}

func settled(reason string) AbortRecoveryPlan {
	return AbortRecoveryPlan{Action: "settle", Reason: reason}
}

func TestPlanAbortRecovery(t *testing.T) {
	const f = "abort-recovery"
	planEq := func(t *testing.T, got, want AbortRecoveryPlan) {
		t.Helper()
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %+v, want %+v", got, want)
		}
	}
	tw(t, f, "does not recover an abort without verified compaction settlement", func(t *testing.T) {
		planEq(t, planAbortRecovery(baseRecovery()), settled("compaction settlement not verified"))
	})
	tw(t, f, "plans the same retained-session continuation for a compaction-induced abort", func(t *testing.T) {
		in := baseRecovery()
		in.AfterCompactionSettlement, in.ProcessSignal = true, "SIGTERM"
		planEq(t, planAbortRecovery(in), AbortRecoveryPlan{Action: "resume", Prompt: abortRecoveryPrompt})
	})
	tw(t, f, "accepts a completed tool result as useful progress", func(t *testing.T) {
		in := baseRecovery()
		in.Messages = abortMessages(
			map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "toolCall", "id": "call-1", "name": "bash", "arguments": map[string]any{}}}, "usage": map[string]any{"output": 1.0}},
			map[string]any{"role": "toolResult", "toolCallId": "call-1", "isError": false, "content": []any{map[string]any{"type": "text", "text": "tests passed"}}},
			abortedMsg())
		in.AfterCompactionSettlement = true
		if got := planAbortRecovery(in).Action; got != "resume" {
			t.Errorf("action %s", got)
		}
	})
	tw(t, f, "fails closed without progress, a retained session, or a first-attempt permit", func(t *testing.T) {
		a := baseRecovery()
		a.Messages = abortMessages(abortedMsg())
		b := baseRecovery()
		b.SessionAvailable = false
		c := baseRecovery()
		c.AlreadyResumed = true
		for _, in := range []AbortRecoveryInput{a, b, c} {
			if got := planAbortRecovery(in).Action; got != "settle" {
				t.Errorf("action %s", got)
			}
		}
	})
	tw(t, f, "does not reinterpret an ordinary empty completion as an abort", func(t *testing.T) {
		in := baseRecovery()
		in.Messages = abortMessages(progressMsg(), withStop(abortedMsg(), "stop"))
		if got := planAbortRecovery(in).Action; got != "settle" {
			t.Errorf("action %s", got)
		}
	})
	tw(t, f, "does not treat a process signal alone as provider abort evidence", func(t *testing.T) {
		in := baseRecovery()
		in.Messages = abortMessages(progressMsg(), withStop(abortedMsg(), "stop"))
		in.ProcessSignal = "SIGKILL"
		if got := planAbortRecovery(in).Action; got != "settle" {
			t.Errorf("action %s", got)
		}
	})
	tw(t, f, "does not resume when a process signal accompanies abort evidence", func(t *testing.T) {
		in := baseRecovery()
		in.ProcessSignal = "SIGTERM"
		planEq(t, planAbortRecovery(in), settled("process terminated by signal"))
		in = baseRecovery()
		in.Messages, in.Error, in.ProcessSignal = abortMessages(progressMsg()), "Connection reset by provider transport", "SIGKILL"
		planEq(t, planAbortRecovery(in), settled("process terminated by signal"))
	})
	tw(t, f, "excludes operator control, timeouts, budgets, schema and acceptance failures", func(t *testing.T) {
		for _, mutate := range []func(*AbortRecoveryInput){
			func(i *AbortRecoveryInput) { i.Stopped = true },
			func(i *AbortRecoveryInput) { i.Interrupted = true },
			func(i *AbortRecoveryInput) { i.TimedOut = true },
			func(i *AbortRecoveryInput) { i.ToolBudgetExhausted = true },
			func(i *AbortRecoveryInput) { i.UsageBudgetExhausted = true },
			func(i *AbortRecoveryInput) { i.StructuredOutputFailed = true },
			func(i *AbortRecoveryInput) { i.AcceptanceFailed = true },
		} {
			in := baseRecovery()
			mutate(&in)
			if got := planAbortRecovery(in).Action; got != "settle" {
				t.Errorf("action %s", got)
			}
		}
	})
	tw(t, f, "fails closed while a tool call is unresolved", func(t *testing.T) {
		inFlight := abortMessages(
			map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "toolCall", "id": "call-1", "name": "bash", "arguments": map[string]any{}}}, "usage": map[string]any{"output": 1.0}},
			abortedMsg())
		in := baseRecovery()
		in.Messages, in.CurrentTool = inFlight, "bash"
		planEq(t, planAbortRecovery(in), settled("active tool 'bash' remains in flight"))
		in.CurrentTool = ""
		planEq(t, planAbortRecovery(in), settled("unresolved tool call remains in transcript"))
	})
	tw(t, f, "names the exact blocker when compaction abort recovery is unsafe", func(t *testing.T) {
		in := baseRecovery()
		in.SessionAvailable, in.AfterCompactionSettlement = false, true
		planEq(t, planAbortRecovery(in), AbortRecoveryPlan{Action: "settle", Reason: "retained session unavailable",
			Diagnostic: "Compaction-induced child abort could not be resumed safely: retained session unavailable."})
		in = baseRecovery()
		in.CurrentTool, in.AfterCompactionSettlement = "bash", true
		planEq(t, planAbortRecovery(in), AbortRecoveryPlan{Action: "settle", Reason: "active tool 'bash' remains in flight",
			Diagnostic: "Compaction-induced child abort could not be resumed safely: active tool 'bash' remains in flight."})
	})
	tw(t, f, "requires terminal assistant abort evidence for compaction recovery", func(t *testing.T) {
		in := baseRecovery()
		in.Messages, in.Error = abortMessages(progressMsg()), "Connection reset by provider transport"
		planEq(t, planAbortRecovery(in), settled("compaction settlement not verified"))
		in.AfterCompactionSettlement = true
		planEq(t, planAbortRecovery(in), settled("terminal assistant abort evidence not verified"))
		in = baseRecovery()
		errored := withStop(abortedMsg(), "error")
		errored["errorMessage"] = "This operation was aborted"
		in.Messages, in.Error, in.AfterCompactionSettlement = abortMessages(progressMsg(), errored), "This operation was aborted", true
		if got := planAbortRecovery(in).Action; got != "resume" {
			t.Errorf("action %s", got)
		}
	})
	tw(t, f, "does not recover a generic transport abort after a normally settled compaction", func(t *testing.T) {
		settledMsg := map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "Compaction completed and the child settled normally."}},
			"stopReason": "stop", "usage": map[string]any{"input": 10.0, "output": 5.0, "cacheRead": 0.0, "cacheWrite": 0.0, "cost": map[string]any{"total": 0.0}}}
		in := baseRecovery()
		in.Messages, in.Error, in.AfterCompactionSettlement = abortMessages(progressMsg(), settledMsg), "Connection reset by provider transport", true
		planEq(t, planAbortRecovery(in), settled("terminal assistant abort evidence not verified"))
	})
	tw(t, f, "does not reinterpret a network-flavored task tool failure as a provider abort", func(t *testing.T) {
		in := baseRecovery()
		in.Messages, in.Error = abortMessages(progressMsg()), "bash failed (exit 1): request connection error"
		if got := planAbortRecovery(in).Action; got != "settle" {
			t.Errorf("action %s", got)
		}
	})
}

func TestUnknownSubagentActionMessage(t *testing.T) {
	const f = "subagent-action-recovery"
	match := func(t *testing.T, msg, pattern string) {
		t.Helper()
		if !regexp.MustCompile(pattern).MatchString(msg) {
			t.Errorf("%q does not match %s", msg, pattern)
		}
	}
	tw(t, f, "suggests a benign typo with safe next steps", func(t *testing.T) {
		m := unknownSubagentActionMessage("statsu")
		match(t, m, `Unknown action: statsu\. Did you mean status\?`)
		match(t, m, `Use subagent\(\{ action: "status" \}\)`)
		match(t, m, `Valid: .*status`)
	})
	tw(t, f, "does not suggest a destructive near-miss", func(t *testing.T) {
		m := unknownSubagentActionMessage("del")
		match(t, m, `Unknown action: del\.`)
		if strings.Contains(m, "Did you mean delete?") {
			t.Errorf("suggested delete: %s", m)
		}
		match(t, m, `Valid: .*delete`)
	})
	tw(t, f, "lists and suggests mission decision resolution", func(t *testing.T) {
		m := unknownSubagentActionMessage("mission.resolve-decison")
		match(t, m, `Did you mean mission\.resolve-decision\?`)
		match(t, m, `Valid: .*mission\.resolve-decision`)
	})
}
