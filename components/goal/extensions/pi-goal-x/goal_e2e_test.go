package pi_goal_x_test

// Twins of tests/e2e/goal-scheduler-sdk.test.ts and tests/e2e/goal-session-safety.test.ts. The originals run a real Pi session
// against a fixture model server; here the run loop is rigAgent (goal_rig_agent_test.go) around the real SDK and goalRig, with the
// provider script of the original's workers. They need the GoalEvents and GoalTools slices.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	goal "github.com/MichaelKinsy/pigpen/pi-goal-x"
)

const (
	gesF = "e2e/goal-scheduler-sdk"
	gssF = "e2e/goal-session-safety"
)

func isoMs(ms int64) string { return time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000Z") }

type schedSDKResult struct {
	requests   int
	used       float64
	retries    int
	compaction int
}

// runSchedulerSDK is scheduler-sdk-worker.mjs: mode is "", "uncapped", "lifecycle" or "implicit".
func runSchedulerSDK(t *testing.T, mode string) schedSDKResult {
	t.Helper()
	implicit, uncapped, lifecycle := mode == "implicit", mode == "uncapped", mode == "lifecycle"
	limit := 4
	if uncapped {
		limit = -1
	}
	h := newRigSched(t, limit, implicit)
	agent := newRigAgent(t, h.goalRig, h.clock)
	agent.FailFirst = lifecycle
	agent.Model = func(n int) []agentCall {
		if implicit {
			return nil
		}
		switch n {
		case 1:
			return []agentCall{{"write", map[string]any{"path": "sample.txt", "content": "fixture"}}}
		case 2, 6:
			return []agentCall{{"update_goal", map[string]any{"continuation": map[string]any{"kind": "ready"}}}}
		case 3, 5:
			return []agentCall{{"read", map[string]any{"path": "sample.txt"}}}
		case 4:
			return []agentCall{{"update_goal", map[string]any{"continuation": map[string]any{"kind": "wait", "reason": "Await producer",
				"deadline": isoMs(h.clock.Now() + 15000), "polling": map[string]any{"interval_seconds": 10.0, "max_checks": 1.0}}}}}
		}
		return nil
	}
	agent.Prompt("Run the fixture.")
	agent.Drain()
	if implicit {
		same(t, h.status(), "paused", "status")
		same(t, agent.WorkRequests, 5, "host execution plus four declaration-free continuations")
		same(t, h.used(), 4.0, "used")
		reMatch(t, str(h.Probe.Goal()["pauseReason"]), `allowance exhausted`, "pause reason")
		return schedSDKResult{requests: agent.Requests(), used: h.used()}
	}
	same(t, h.sch()["phase"], "waiting", "phase")
	same(t, agent.WorkRequests, 4, "work requests")
	same(t, h.used(), 1.0, "used")
	h.tick(200)
	same(t, agent.WorkRequests, 4, "waiting must not spin after reads or writes")
	if lifecycle {
		agent.Compact()
		same(t, h.used(), 1.0, "host compaction does not spend an extension run")
		same(t, h.sch()["phase"], "waiting", "phase")
	}
	wait, _ := h.sch()["wait"].(map[string]any)
	id := str(h.Probe.Goal()["id"])
	h.Probe.SchedulerSignal(id, str(wait["token"]))
	h.Probe.SchedulerSignal(id, str(wait["token"]))
	agent.Drain()
	same(t, h.status(), "paused", "status")
	same(t, agent.WorkRequests, 8, "ready, wake, ready, one repair only")
	same(t, h.used(), 4.0, "used")
	if uncapped {
		reMatch(t, str(h.Probe.Goal()["pauseReason"]), `No execution disposition`, "uncapped mode stops after its only repair")
	}
	same(t, h.sch()["repairUsed"], true, "the repair dispatch was spent")
	h.tick(200)
	same(t, agent.WorkRequests, 8, "no more requests")
	idx := 2
	if lifecycle {
		idx = 3
	}
	live := string(agent.Contexts[idx])
	if !strings.Contains(live, "PI GOAL ACTIVE") {
		t.Error("custom-message run receives the goal prompt with current scheduling context")
	}
	if strings.Contains(live, "Inspect the fixture result") {
		t.Error("a declared next action is never stored or echoed")
	}
	var checkpoint map[string]any
	for _, s := range h.Sent() {
		if s.Message["customType"] == "pi-goal-event" {
			checkpoint = s.Message
		}
	}
	if checkpoint == nil {
		t.Fatal("no checkpoint was sent")
	}
	agent.Deliver(checkpoint)
	same(t, agent.WorkRequests, 8, "stale dispatch is rejected before another provider request")
	if lifecycle {
		same(t, agent.FailedAttempts, 1, "failed attempts")
		if agent.Retries == 0 {
			t.Error("the retry lifecycle was not exercised")
		}
	}
	return schedSDKResult{requests: agent.Requests(), used: h.used(), retries: agent.Retries, compaction: agent.Compactions}
}

func TestGoalE2ESchedulerTwins(t *testing.T) {
	tw(t, gesF, "real SDK: opt-in uncapped runs retain waits, one repair and stale-wake protection", func(t *testing.T) {
		res := runSchedulerSDK(t, "uncapped")
		same(t, res.requests, 8, "requests")
		same(t, res.used, 4.0, "used")
	})
	tw(t, gesF, "real SDK: explicit dispositions, custom wakes and one repair enforce allowance", func(t *testing.T) {
		runSchedulerSDK(t, "")
	})
	tw(t, gesF, "real SDK: native retry and compaction preserve consumed autonomous allowance", func(t *testing.T) {
		res := runSchedulerSDK(t, "lifecycle")
		same(t, res.requests, 10, "requests")
		same(t, res.used, 4.0, "used")
		same(t, res.compaction, 1, "compaction requests")
		if res.retries == 0 {
			t.Error("no retries")
		}
	})
	tw(t, gesF, "real SDK: default runs continue without tools or declarations until configured cap", func(t *testing.T) {
		res := runSchedulerSDK(t, "implicit")
		same(t, res.requests, 5, "requests")
		same(t, res.used, 4.0, "used")
	})
}

// ---- goal session safety ----

func auditSent(r *goalRig) []map[string]any {
	var out []map[string]any
	for _, s := range r.Sent() {
		if s.Message["customType"] == "pi-goal-audit-event" {
			out = append(out, s.Message)
		}
	}
	return out
}

func TestGoalE2ESessionSafetyTwins(t *testing.T) {
	tw(t, gssF, "real SDK: goal session safety (${mode})", func(t *testing.T) {
		for _, mode := range []string{"approve", "reject", "skip", "abort", "error"} {
			t.Run(mode, func(t *testing.T) { gssParentMode(t, mode) })
		}
		for _, mode := range []string{"history-completions", "history-responses"} {
			t.Run(mode, func(t *testing.T) { gssHistoryMode(t, mode) })
		}
		for _, mode := range []string{"child-fresh", "child-fork", "child-resume", "child-nested"} {
			t.Run(mode, func(t *testing.T) { gssChildMode(t, mode) })
		}
	})
}

func gssParentMode(t *testing.T, mode string) {
	cwd := t.TempDir()
	rel, text := seedGoal("session-safety-goal", "Finish the delegated assignment with tested evidence.")
	text = strings.Replace(text, `"autoContinue": true,`, `"autoContinue": false,`, 1)
	if mode == "skip" {
		text = strings.Replace(text, `"sisyphus": false,`, `"sisyphus": false, "skipAuditor": true,`, 1)
	}
	var r *goalRig
	r = newGoalRig(t, rigOptions{
		Cwd: cwd, Settings: "-", Seed: map[string]string{rel: text}, Branch: []any{focusEntry("session-safety-goal")},
		Deps: goal.RigDeps{Auditor: func(context.Context, map[string]any, string, string) goal.RigAuditResult {
			if n := len(auditSent(r)); n != 0 {
				t.Errorf("audit messages must wait for the completion tool result (%d sent)", n)
			}
			switch mode {
			case "approve":
				return goal.RigAuditResult{Approved: true, Output: "Evidence verified\n<approved/>"}
			case "abort":
				return goal.RigAuditResult{Disapproved: true, Output: "More evidence needed\n<disapproved/>", Err: "Auditor aborted."}
			case "error":
				return goal.RigAuditResult{Disapproved: true, Output: "More evidence needed\n<disapproved/>", Err: "Fixture auditor error"}
			}
			return goal.RigAuditResult{Disapproved: true, Output: "More evidence needed\n<disapproved/>"}
		}},
		StartSession: true,
	})
	clock := r.Probe.FakeClock(1_800_000_000_000)
	t.Cleanup(clock.Restore)
	agent := newRigAgent(t, r, clock)
	agent.Model = func(n int) []agentCall {
		if n == 1 {
			return []agentCall{{"get_goal", map[string]any{}}, {"update_goal", map[string]any{"status": "complete"}}}
		}
		return nil
	}
	agent.Prompt("Complete the goal.")
	before := agent.WorkRequests
	clock.Tick(100)
	same(t, agent.WorkRequests, before, "audit transcript must not trigger an extra model request")
	var phases []string
	for _, m := range auditSent(r) {
		if d, ok := m["details"].(map[string]any); ok {
			phases = append(phases, str(d["phase"]))
		}
	}
	want := []string{"started", "rejected"}
	switch mode {
	case "skip":
		want = []string{"skipped"}
	case "abort":
		want = []string{"started"}
	case "approve":
		want = []string{"started", "approved"}
	}
	same(t, phases, want, "audit phases")
	agent.Prompt("Report the resulting state.")
	same(t, agent.WorkRequests, before+1, "the next prompt is one more request")
}

func gssHistory() []any {
	return []any{
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "Inherited assignment context"}}},
		map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "toolCall", "id": "call_info", "name": "get_goal", "arguments": map[string]any{}}}},
		map[string]any{"role": "custom", "customType": "pi-goal-audit-event", "content": "AUDIT DISPLAY ONLY", "display": true, "details": map[string]any{"phase": "started"}},
		map[string]any{"role": "toolResult", "toolCallId": "call_info", "toolName": "get_goal", "content": []any{map[string]any{"type": "text", "text": "actual result call_info"}}},
		map[string]any{"role": "custom", "customType": "other-extension", "content": "UNRELATED DISPLAY PRESERVED", "display": true},
	}
}

func gssHistoryMode(t *testing.T, mode string) {
	r := newGoalRig(t, rigOptions{Settings: "-", StartSession: true})
	got := ctxMessages(t, r.Fire("context", map[string]any{"messages": gssHistory()}))
	b, _ := json.Marshal(got)
	if strings.Contains(string(b), "AUDIT DISPLAY ONLY") {
		t.Errorf("%s: the display-only audit reached the provider context", mode)
	}
	if !strings.Contains(string(b), "UNRELATED DISPLAY PRESERVED") {
		t.Errorf("%s: an unrelated custom message was dropped", mode)
	}
	if !strings.Contains(string(b), "actual result call_info") {
		t.Errorf("%s: the tool result was dropped", mode)
	}
}

func gssChildMode(t *testing.T, mode string) {
	if mode == "child-nested" {
		t.Setenv("PI_SUBAGENT_DEPTH", "3")
		t.Setenv("PI_SUBAGENT_CHILD", "")
	} else {
		t.Setenv("PI_SUBAGENT_CHILD", "1")
		t.Setenv("PI_SUBAGENT_DEPTH", "")
	}
	cwd := t.TempDir()
	rel, text := seedGoal("parent-goal", "Finish the delegated assignment with tested evidence.")
	r := newGoalRig(t, rigOptions{Cwd: cwd, Settings: `{"maxAutonomousRuns": 0}`, Seed: map[string]string{rel: text}})
	snapshot := func() map[string]string {
		out := map[string]string{}
		_ = filepath.Walk(filepath.Join(cwd, ".pi"), func(p string, fi os.FileInfo, err error) error {
			if err == nil && !fi.IsDir() {
				b, _ := os.ReadFile(p)
				out[p] = string(b)
			}
			return nil
		})
		return out
	}
	before := snapshot()
	msgs := append(gssHistory(), map[string]any{"role": "custom", "customType": "pi-goal-event", "content": `<pi_goal_continuation goal_id="parent-goal" kind="checkpoint" v="2"/>`,
		"display": false, "details": map[string]any{"version": 2.0, "kind": "checkpoint", "goalId": "parent-goal"}}, map[string]any{"role": "user", "content": "Complete only this delegated assignment."})
	got := r.Fire("context", map[string]any{"messages": msgs})
	b := string(got)
	for _, leak := range []string{"pi_goal_continuation", "AUDIT DISPLAY ONLY", "PI GOAL UNFOCUSED"} {
		if strings.Contains(b, leak) {
			t.Errorf("%s: %q reached the child's context", mode, leak)
		}
	}
	if !strings.Contains(b, "UNRELATED DISPLAY PRESERVED") || !strings.Contains(b, "Complete only this delegated assignment.") {
		t.Errorf("%s: the child's own conversation was dropped: %s", mode, b)
	}
	if len(r.Host.tools) != 0 || len(r.Host.cmds) != 0 || r.Host.Registered("session_start") {
		t.Errorf("%s: the child registered goal tools, commands or session handlers", mode)
	}
	same(t, snapshot(), before, "parent goal files/ledger/settings must be byte-identical")
	same(t, len(r.Entries()), 0, "child must not append focus/state entries")
}
