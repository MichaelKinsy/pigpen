package pi_goal_x_test

// Twins of tests/goal-network-recovery.test.ts (lifecycle part), goal-stale-continuation-golden.test.ts, goal-budget.test.ts and
// the two compaction cases: the original's hand-made harness calls the extension's handlers with a mock ctx; here the same calls
// reach the extension through the real SDK and the fake host (goal_rig_test.go).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	goal "github.com/MichaelKinsy/pigpen/pi-goal-x"
)

const (
	netFile    = "goal-network-recovery"
	staleFile  = "goal-stale-continuation-golden"
	budgetFile = "goal-budget"

	reported503 = `503: {"type":"server_error","message":"Error from provider (Console): Upstream request failed: Endpoint is unavailable."}`
)

// evRig is the original tests' harness: ctx is busy by default, idleCtx(ctx) answers idle and no pending messages. The host answers
// for one session, so the state of the last event's context stays until the next event (a timer that fires between sees it).
type evRig struct {
	*goalRig
	idle atomic.Bool
}

var evFixtureGoal = func() string {
	b, err := os.ReadFile("../../port/oracle/tests/fixtures/goals/active_goal_fixture.md")
	if err != nil {
		panic(err)
	}
	return string(b)
}()

// newEvRig starts the extension over the golden fixture goal (fixtureCwd + sessionEntriesFor). settings "" is no settings file.
func newEvRig(t *testing.T, settings string, env map[string]string) *evRig {
	t.Helper()
	e := &evRig{}
	noUI := false
	if settings == "" {
		settings = "-"
	}
	branch := []any{
		map[string]any{"type": "custom", "customType": "pi-goal-focus", "data": map[string]any{"version": 1, "focusedGoalId": "golden_fixture_goal", "reason": "created"}},
		map[string]any{"type": "custom", "customType": "pi-goal-state", "data": map[string]any{"version": 3, "goal": map[string]any{
			"id": "golden_fixture_goal", "objective": "Golden fixture goal objective", "status": "active", "autoContinue": true, "sisyphus": false,
			"usage": map[string]any{"tokensUsed": 0, "activeSeconds": 0}, "createdAt": "2026-08-03T09:00:00.000Z", "updatedAt": "2026-08-03T09:00:00.000Z",
			"activePath": ".pi/goals/active_goal_fixture.md"}}},
	}
	e.goalRig = newGoalRig(t, rigOptions{
		HasUI: &noUI, SessionID: "test-session", Settings: settings, Env: env, Branch: branch,
		Seed:    map[string]string{".pi/goals/active_goal_fixture.md": evFixtureGoal, ".pi/goals/archived/.keep": ""},
		Idle:    func() bool { return e.idle.Load() },
		Pending: func() bool { return !e.idle.Load() },
	})
	t.Cleanup(func() {
		if e.Probe != nil {
			e.Probe.Teardown()
		}
	})
	return e
}

// fire delivers an event with the busy ctx; fireIdle with idleCtx.
func (e *evRig) fire(event string, payload map[string]any) json.RawMessage {
	e.idle.Store(false)
	return e.Fire(event, payload)
}

func (e *evRig) fireIdle(event string, payload map[string]any) json.RawMessage {
	e.idle.Store(true)
	return e.Fire(event, payload)
}

// runSignal sends the host's run_signal frame: an active run, aborted or not (ctx.signal of the original's mock ctx).
func (e *evRig) runSignal(active, aborted bool) {
	e.Host.write(map[string]any{"type": "notify", "notify": map[string]any{"method": "run_signal", "args": map[string]any{"run": 1, "active": active, "aborted": aborted}}})
}

func (e *evRig) startSession() { e.fire("session_start", map[string]any{"reason": "start"}) }

func (e *evRig) beforeAgentStart(prompt string) {
	e.fire("before_agent_start", map[string]any{"systemPrompt": "base", "prompt": prompt, "systemPromptOptions": map[string]any{}})
}

// messages are the ui.notify messages, in order.
func (e *evRig) messages() []string {
	var out []string
	for _, c := range e.Host.CallsTo("ui.notify") {
		out = append(out, str(c.Args["message"]))
	}
	return out
}

func (e *evRig) lastMessage() string {
	m := e.messages()
	if len(m) == 0 {
		return ""
	}
	return m[len(m)-1]
}

func (e *evRig) aborts() int { return len(e.Host.CallsTo("abort")) }

// checkpoints is countCheckpoints: wait 20ms, then count the checkpoint messages sent.
func (e *evRig) checkpoints() int {
	time.Sleep(20 * time.Millisecond)
	n := 0
	for _, s := range e.Sent() {
		if s.Message["customType"] == "pi-goal-event" {
			if d, _ := s.Message["details"].(map[string]any); d["kind"] == "checkpoint" {
				n++
			}
		}
	}
	return n
}

// markGoalWork is markGoalWork/declareNextRun: a work turn, then a ready declaration.
func (e *evRig) markGoalWork(t *testing.T) {
	t.Helper()
	e.fire("turn_start", map[string]any{})
	e.fire("tool_call", map[string]any{"toolName": "bash", "args": map[string]any{"command": "ls"}})
	e.fire("tool_execution_end", map[string]any{})
	if !e.Probe.SchedulerDeclareReadyTerminate() {
		t.Error("declare ready must terminate")
	}
}

func evMatch(t *testing.T, got, re, what string) {
	t.Helper()
	if !regexp.MustCompile(re).MatchString(got) {
		t.Errorf("%s: %q does not match /%s/", what, got, re)
	}
}

// contextTail is the content of the last message the context handler returns for an empty conversation.
func (e *evRig) contextTail(t *testing.T) string {
	t.Helper()
	raw := e.fire("context", map[string]any{"messages": []any{}})
	var out struct {
		Messages []map[string]any `json:"messages"`
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	if len(out.Messages) == 0 {
		return ""
	}
	s, _ := out.Messages[len(out.Messages)-1]["content"].(string)
	return s
}

func errMessage(m string) map[string]any {
	return map[string]any{"role": "assistant", "stopReason": "error", "errorMessage": m}
}

func TestGoalNetworkRecoveryLifecycle(t *testing.T) {
	const maxDelay = "PI_GOAL_NETWORK_RECOVERY_MAX_DELAY_MS"
	const settings = `{"maxAutonomousRuns": 100}`

	tw(t, netFile, "regression: reported 503 payload schedules goal-level recovery after settle", func(t *testing.T) {
		e := newEvRig(t, settings, nil)
		e.startSession()
		e.beforeAgentStart("user typed: continue")
		e.fireIdle("agent_end", map[string]any{"messages": []any{errMessage("Retry failed after 3 attempts: " + reported503)}})
		if n := e.checkpoints(); n != 0 {
			t.Errorf("agent_end must not race Pi's built-in retries: %d checkpoints", n)
		}
		e.fireIdle("agent_settled", map[string]any{})
		evMatch(t, e.lastMessage(), `Retrying the goal in 5s`, "the reported 503 outage must engage the bounded goal-level recovery")
		if n := e.checkpoints(); n != 0 {
			t.Errorf("the first recovery is delayed by the backoff policy: %d", n)
		}
	})

	tw(t, netFile, "regression: generic Provider finish_reason: error schedules goal-level recovery after settle", func(t *testing.T) {
		e := newEvRig(t, settings, nil)
		e.startSession()
		e.beforeAgentStart("user typed: continue")
		e.fireIdle("agent_end", map[string]any{"messages": []any{errMessage("Provider finish_reason: error")}})
		if n := e.checkpoints(); n != 0 {
			t.Errorf("agent_end must not race Pi's built-in retries: %d checkpoints", n)
		}
		e.fireIdle("agent_settled", map[string]any{})
		evMatch(t, e.lastMessage(), `Retrying the goal in 5s`, "the generic finish_reason: error must engage the bounded goal-level recovery")
		if n := e.checkpoints(); n != 0 {
			t.Errorf("the first recovery is delayed by the backoff policy: %d", n)
		}
	})

	tw(t, netFile, "lifecycle: provider-initiated abort routes into recovery instead of pausing", func(t *testing.T) {
		e := newEvRig(t, settings, map[string]string{maxDelay: "25"})
		e.startSession()
		e.beforeAgentStart("user typed: continue")
		e.fireIdle("agent_end", map[string]any{"messages": []any{map[string]any{"role": "assistant", "stopReason": "aborted", "errorMessage": "Aborted after 3 retry attempts"}}})
		e.fireIdle("agent_settled", map[string]any{})
		evMatch(t, e.lastMessage(), `Retrying the goal in \d+s \(recovery 1, unbounded\)`, "a provider-initiated abort without a user abort signal must engage recovery")
	})

	tw(t, netFile, "lifecycle: full real event ordering (message_end → turn_end → agent_end → settled) routes provider-side aborts into recovery", func(t *testing.T) {
		e := newEvRig(t, settings, map[string]string{maxDelay: "25"})
		e.startSession()
		e.beforeAgentStart("user typed: continue")
		aborted := map[string]any{"role": "assistant", "stopReason": "aborted", "usage": map[string]any{"input": 0, "output": 0}}
		paused := func() bool {
			for _, m := range e.messages() {
				if strings.Contains(m, "Goal paused") {
					return true
				}
			}
			return false
		}
		e.fireIdle("message_end", map[string]any{"message": aborted})
		if paused() {
			t.Error("message_end must not pause without a user abort signal")
		}
		e.fireIdle("turn_end", map[string]any{"message": aborted})
		if paused() {
			t.Error("turn_end must not pause without a user abort signal")
		}
		e.fireIdle("agent_end", map[string]any{"messages": []any{aborted}})
		e.fireIdle("agent_settled", map[string]any{})
		evMatch(t, e.lastMessage(), `Retrying the goal in \d+s \(recovery 1, unbounded\)`, "recovery must engage through the full real event ordering")
	})

	tw(t, netFile, "lifecycle: genuine user abort still pauses the goal", func(t *testing.T) {
		e := newEvRig(t, settings, nil)
		e.startSession()
		e.beforeAgentStart("user typed: continue")
		e.runSignal(true, true)
		e.fireIdle("agent_end", map[string]any{"messages": []any{map[string]any{"role": "assistant", "stopReason": "aborted", "errorMessage": "Aborted after 2 retry attempts"}}})
		e.runSignal(false, false)
		evMatch(t, e.lastMessage(), `Goal paused\.`, "a user abort signal must keep pausing the goal")
		for _, m := range e.messages() {
			if strings.Contains(m, "Retrying the goal") {
				t.Errorf("user-initiated pauses never schedule recovery: %q", m)
			}
		}
	})

	tw(t, netFile, "lifecycle: unbounded recovery keeps retrying past the old 5-attempt cap", func(t *testing.T) {
		e := newEvRig(t, settings, map[string]string{maxDelay: "25"})
		e.startSession()
		e.beforeAgentStart("user typed: continue")
		for attempt := 1; attempt <= 7; attempt++ {
			e.fireIdle("agent_end", map[string]any{"messages": []any{errMessage(reported503)}})
			e.fireIdle("agent_settled", map[string]any{})
			evMatch(t, e.lastMessage(), `Retrying the goal in 0s \(recovery `+string(rune('0'+attempt))+`, unbounded\)`, "cycle must keep scheduling recovery")
			// Let the (25ms) recovery timer fire and clear before the next cycle.
			time.Sleep(80 * time.Millisecond)
		}
	})

	tw(t, netFile, "lifecycle: configured bounded cap exhausts with a resume hint instead of retrying", func(t *testing.T) {
		e := newEvRig(t, settings, map[string]string{"PI_GOAL_NETWORK_RECOVERY_MAX_ATTEMPTS": "2", maxDelay: "25"})
		e.startSession()
		e.beforeAgentStart("user typed: continue")
		for attempt := 1; attempt <= 2; attempt++ {
			e.fireIdle("agent_end", map[string]any{"messages": []any{errMessage(reported503)}})
			e.fireIdle("agent_settled", map[string]any{})
			evMatch(t, e.lastMessage(), `recovery `+string(rune('0'+attempt))+`/2`, "bounded cycle")
			time.Sleep(80 * time.Millisecond)
		}
		before := e.checkpoints()
		e.fireIdle("agent_end", map[string]any{"messages": []any{errMessage(reported503)}})
		e.fireIdle("agent_settled", map[string]any{})
		evMatch(t, e.lastMessage(), `persisted after all recovery attempts\. The goal remains active`, "exhaustion must stop the loop with a resume hint")
		if n := e.checkpoints(); n != before {
			t.Errorf("exhausted recovery must not deliver a continuation: %d -> %d", before, n)
		}
	})

	tw(t, netFile, "lifecycle: a successful turn resets the recovery counter and clears pending backoff", func(t *testing.T) {
		e := newEvRig(t, settings, map[string]string{maxDelay: "25"})
		e.startSession()
		e.beforeAgentStart("user typed: continue")
		// Two failed cycles advance the counter.
		for range 2 {
			e.fireIdle("agent_end", map[string]any{"messages": []any{errMessage(reported503)}})
			e.fireIdle("agent_settled", map[string]any{})
			time.Sleep(80 * time.Millisecond)
		}
		// A successful turn uses the normal continuation path.
		fromTimers := e.checkpoints()
		notificationsBefore := len(e.messages())
		e.markGoalWork(t)
		e.fireIdle("agent_end", map[string]any{"messages": []any{map[string]any{"role": "assistant", "stopReason": "end_turn"}}})
		e.fireIdle("agent_settled", map[string]any{})
		if n := e.checkpoints(); n != fromTimers+1 {
			t.Errorf("success queues the normal continuation: %d, want %d", n, fromTimers+1)
		}
		if n := len(e.messages()); n != notificationsBefore {
			t.Errorf("success announces no recovery: %v", e.messages()[notificationsBefore:])
		}
		// The next failure starts over at recovery 1.
		e.fireIdle("agent_end", map[string]any{"messages": []any{errMessage(reported503)}})
		e.fireIdle("agent_settled", map[string]any{})
		evMatch(t, e.lastMessage(), `\(recovery 1, unbounded\)`, "counter restarted")
	})

	tw(t, netFile, "session_compact_failed warns the user instead of failing silently (pi 1.0.0)", func(t *testing.T) {
		e := newEvRig(t, "", nil)
		e.startSession()
		e.beforeAgentStart("user typed: go")
		before := len(e.messages())
		e.fire("session_compact_failed", map[string]any{
			"reason": "threshold", "errorMessage": "provider rejected the summarization request", "aborted": false, "willRetry": false, "fromExtension": false,
		})
		if n := len(e.messages()); n != before+1 {
			t.Fatalf("a failed compaction is announced: %d -> %d", before, n)
		}
		m := e.lastMessage()
		evMatch(t, m, `(?i)compaction failed`, "the warning names the failure")
		evMatch(t, m, `threshold`, "the warning names the trigger reason")
		evMatch(t, m, `provider rejected the summarization request`, "the warning carries the error text")
		evMatch(t, m, `(?i)stays active`, "the goal is not silently stopped")
	})

	tw(t, netFile, "session_compact_failed stays silent when the user aborted or no goal is focused", func(t *testing.T) {
		e := newEvRig(t, "", nil)
		e.startSession()
		e.beforeAgentStart("go")
		before := len(e.messages())
		e.fire("session_compact_failed", map[string]any{"reason": "manual", "aborted": true, "willRetry": false, "fromExtension": false})
		if n := len(e.messages()); n != before {
			t.Errorf("an aborted compaction produces no warning: %v", e.messages()[before:])
		}
	})
}

func TestGoalStaleContinuationGolden(t *testing.T) {
	tw(t, staleFile, "golden: stale checkpoint for a non-focused goal aborts the turn and injects GOAL STALE", func(t *testing.T) {
		e := newEvRig(t, "", nil)
		e.startSession()
		// A checkpoint claims a goal that is not focused/active in this session.
		e.beforeAgentStart(`<pi_goal_continuation goal_id="ghost-goal" kind="checkpoint">continue`)
		if n := e.aborts(); n != 1 {
			t.Errorf("stale checkpoint must abort the turn: %d aborts", n)
		}
		prompt := e.contextTail(t)
		evMatch(t, prompt, `\[GOAL STALE goalId=ghost-goal\]`, "stale prompt")
		evMatch(t, prompt, `Do not perform task work for this stale checkpoint`, "stale prompt")
	})

	tw(t, staleFile, "golden: matching checkpoint proceeds without stale handling", func(t *testing.T) {
		e := newEvRig(t, "", nil)
		e.startSession()
		e.beforeAgentStart(`<pi_goal_continuation goal_id="golden_fixture_goal" kind="checkpoint">continue`)
		if n := e.aborts(); n != 0 {
			t.Errorf("matching checkpoint must not abort: %d", n)
		}
		if prompt := e.contextTail(t); strings.Contains(prompt, "GOAL STALE") {
			t.Errorf("stale handling: %q", prompt)
		}
	})

	tw(t, staleFile, "golden: user-driven turn cancels a pending continuation", func(t *testing.T) {
		e := newEvRig(t, "", nil)
		e.startSession()
		// A work turn that queues a continuation: turn_start, a bash tool call, then a non-tool-use assistant message at turn_end.
		e.fire("turn_start", map[string]any{})
		e.fire("tool_call", map[string]any{"toolName": "bash", "args": map[string]any{"command": "ls"}})
		e.fire("tool_execution_end", map[string]any{})
		e.fire("turn_end", map[string]any{"message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "done"}}}})
		// A user-driven turn must cancel the pending continuation before it fires.
		e.beforeAgentStart("user typed a new instruction")
		// Wait beyond the idle retry delay; the queued continuation must not fire.
		time.Sleep(120 * time.Millisecond)
		if n := e.checkpoints(); n != 0 {
			t.Errorf("user turn must cancel the pending continuation: %d checkpoints", n)
		}
	})

	tw(t, staleFile, "provider-error guard: turn_end with stopReason=error never queues a continuation", func(t *testing.T) {
		e := newEvRig(t, "", nil)
		e.startSession()
		// Cancel the continuation session_start armed so the turn itself owns the queue decision.
		e.beforeAgentStart("user typed: continue")
		e.fire("turn_start", map[string]any{})
		e.fire("tool_call", map[string]any{"toolName": "bash", "args": map[string]any{"command": "ls"}})
		e.fire("tool_execution_end", map[string]any{})
		e.fireIdle("turn_end", map[string]any{"message": map[string]any{"role": "assistant", "stopReason": "error"}})
		if n := e.checkpoints(); n != 0 {
			t.Errorf("error turn must not queue a continuation: %d", n)
		}
	})

	tw(t, staleFile, "ordinary work without a disposition does not queue a continuation", func(t *testing.T) {
		e := newEvRig(t, "", nil)
		e.startSession()
		e.beforeAgentStart("user typed: continue")
		e.fire("turn_start", map[string]any{})
		e.fire("tool_call", map[string]any{"toolName": "bash", "args": map[string]any{"command": "ls"}})
		e.fire("tool_execution_end", map[string]any{})
		e.fireIdle("turn_end", map[string]any{"message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "done"}}}})
		if n := e.checkpoints(); n != 0 {
			t.Errorf("work tools alone must not authorize continuation: %d", n)
		}
	})

	tw(t, staleFile, "provider-error guard: agent_end with an error message never queues a continuation", func(t *testing.T) {
		e := newEvRig(t, "", nil)
		e.startSession()
		e.fireIdle("agent_end", map[string]any{"messages": []any{map[string]any{"role": "assistant", "stopReason": "error"}}})
		e.fireIdle("agent_settled", map[string]any{})
		if n := e.checkpoints(); n != 0 {
			t.Errorf("agent_end with an error message must not queue a continuation: %d", n)
		}
	})

	tw(t, staleFile, "network-error recovery waits for agent_settled before scheduling its backoff", func(t *testing.T) {
		e := newEvRig(t, "", nil)
		e.startSession()
		e.beforeAgentStart("user typed: continue")
		e.fireIdle("agent_end", map[string]any{"messages": []any{errMessage("Provider finish_reason: network_error")}})
		if n := e.checkpoints(); n != 0 {
			t.Errorf("agent_end must not race Pi's built-in retries: %d", n)
		}
		e.fireIdle("agent_settled", map[string]any{})
		if n := e.checkpoints(); n != 0 {
			t.Errorf("the first recovery is delayed by the backoff policy: %d", n)
		}
		evMatch(t, e.lastMessage(), `Retrying the goal in 5s \(recovery 1, unbounded\)`, "recovery notice")
	})

	tw(t, staleFile, "a successful Pi retry clears the pending network-error recovery", func(t *testing.T) {
		e := newEvRig(t, "", nil)
		e.startSession()
		e.beforeAgentStart("user typed: continue")
		e.fireIdle("agent_end", map[string]any{"messages": []any{errMessage("Provider finish_reason: network_error")}})
		e.declareNextRun(t)
		e.fireIdle("agent_end", map[string]any{"messages": []any{map[string]any{"role": "assistant", "stopReason": "end_turn"}}})
		e.fireIdle("agent_settled", map[string]any{})
		if n := e.checkpoints(); n != 1 {
			t.Errorf("the successful Pi retry uses the normal continuation path: %d", n)
		}
		if n := len(e.messages()); n != 0 {
			t.Errorf("no goal-level recovery is announced after Pi retries successfully: %v", e.messages())
		}
	})

	tw(t, staleFile, "successful agent_end waits for agent_settled before queuing a continuation", func(t *testing.T) {
		e := newEvRig(t, "", nil)
		e.startSession()
		e.declareNextRun(t)
		e.fireIdle("agent_end", map[string]any{"messages": []any{map[string]any{"role": "assistant", "stopReason": "end_turn"}}})
		if n := e.checkpoints(); n != 0 {
			t.Errorf("agent_end runs before pi is truly idle: %d", n)
		}
		e.fireIdle("agent_settled", map[string]any{})
		if n := e.checkpoints(); n != 1 {
			t.Errorf("agent_settled queues the continuation without idle polling: %d", n)
		}
	})

	tw(t, staleFile, "empty no-tool runs continue implicitly after agent_settled", func(t *testing.T) {
		e := newEvRig(t, "", nil)
		e.startSession()
		e.beforeAgentStart("Continue the fixture goal.")
		e.fireIdle("agent_start", map[string]any{})
		text := func(s string) map[string]any {
			return map[string]any{"role": "assistant", "stopReason": "end_turn", "content": []any{map[string]any{"type": "text", "text": s}}}
		}
		e.fireIdle("agent_end", map[string]any{"messages": []any{text("Paused. No action.")}})
		e.fireIdle("agent_settled", map[string]any{})
		if n := e.checkpoints(); n != 1 {
			t.Fatalf("a successful no-tool execution continues: %d", n)
		}
		dispatch := func() any {
			sch, _ := e.Probe.Goal()["scheduler"].(map[string]any)
			d, _ := sch["dispatch"].(map[string]any)
			return d["kind"]
		}
		if dispatch() != "ready" {
			t.Errorf("dispatch kind %v", dispatch())
		}
		e.fireIdle("agent_start", map[string]any{})
		sent := e.Sent()
		message := map[string]any{}
		for k, v := range sent[len(sent)-1].Message {
			message[k] = v
		}
		message["role"] = "custom"
		e.fireIdle("message_start", map[string]any{"message": message})
		e.fireIdle("agent_end", map[string]any{"messages": []any{text("Still no action.")}})
		e.fireIdle("agent_settled", map[string]any{})
		if n := e.checkpoints(); n != 2 {
			t.Errorf("missing declarations never require repair in default mode: %d", n)
		}
		if st := e.Probe.Goal()["status"]; st != "active" {
			t.Errorf("status %v", st)
		}
	})
}

// declareNextRun is declareNextRun of goal-stale-continuation-golden: markGoalWork with maxAutonomousRuns 5.
func (e *evRig) declareNextRun(t *testing.T) {
	t.Helper()
	e.fire("turn_start", map[string]any{})
	e.fire("tool_call", map[string]any{"toolName": "bash", "args": map[string]any{"command": "ls"}})
	e.fire("tool_execution_end", map[string]any{})
	goal.SaveGoalSettingsForRig(e.Cwd, `{"maxAutonomousRuns": 5}`)
	if !e.Probe.SchedulerDeclareReadyTerminate() {
		t.Error("declare ready must terminate")
	}
}

// budgetRig is goal-budget's createHarness: a UI-less idle session over one goal with token budget 100 and 80 used.
func budgetRig(t *testing.T, objective string, tokenBudget, tokensUsed float64) (*goalRig, map[string]any) {
	t.Helper()
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".pi", "goals", "archived"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	g := goal.WriteGoalForRig(cwd, objective, time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC).UnixMilli(), tokenBudget, tokensUsed)
	noUI := false
	r := newGoalRig(t, rigOptions{
		Cwd: cwd, HasUI: &noUI, SessionID: "budget-test-session", Settings: "-",
		Branch: []any{map[string]any{"type": "custom", "customType": "pi-goal-focus", "data": map[string]any{"version": 1, "focusedGoalId": g["id"], "reason": "created"}}},
	})
	t.Cleanup(func() { r.Probe.Teardown() })
	return r, g
}

func ledgerOf(t *testing.T, cwd, kind string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(cwd, ".pi", "goals", "goal_events.jsonl"))
	if err != nil {
		return nil
	}
	var out []map[string]any
	for _, line := range strings.Split(string(raw), "\n") {
		if line == "" {
			continue
		}
		var e map[string]any
		if json.Unmarshal([]byte(line), &e) == nil && e["type"] == kind {
			out = append(out, e)
		}
	}
	return out
}

func activeGoalFiles(t *testing.T, cwd string) []string {
	t.Helper()
	entries, _ := os.ReadDir(filepath.Join(cwd, ".pi", "goals"))
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "active_goal_") {
			out = append(out, e.Name())
		}
	}
	return out
}

func diskStatus(t *testing.T, cwd, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(cwd, ".pi", "goals", name))
	if err != nil {
		t.Fatal(err)
	}
	head, _, _ := strings.Cut(string(raw), "\n\n")
	var g map[string]any
	if err := json.Unmarshal([]byte(head), &g); err != nil {
		t.Fatalf("goal file must parse: %v", err)
	}
	s, _ := g["status"].(string)
	return s
}

func turnEndMessage(tokens float64) map[string]any {
	return map[string]any{"role": "assistant", "stopReason": "stop", "usage": map[string]any{"input": tokens, "output": 0}}
}

func TestGoalBudget(t *testing.T) {
	tw(t, budgetFile, "budget reached marks the goal budget_limited exactly once with ledger + one-time steering", func(t *testing.T) {
		r, goal := budgetRig(t, "=== Goal ===\nObjective: Budgeted goal", 100, 80)
		r.Fire("session_start", map[string]any{"reason": "start"})
		// First turn_end charges 30 tokens: 80 + 30 = 110 >= budget 100 → transition.
		r.Fire("turn_end", map[string]any{"message": turnEndMessage(30)})

		active := activeGoalFiles(t, r.Cwd)
		if len(active) != 1 {
			t.Fatalf("goal stays in the active dir (not archived): %v", active)
		}
		if s := diskStatus(t, r.Cwd, active[0]); s != "budget_limited" {
			t.Errorf("disk status %q", s)
		}
		events := ledgerOf(t, r.Cwd, "goal_budget_limited")
		if len(events) != 1 {
			t.Fatalf("exactly one goal_budget_limited event: %d", len(events))
		}
		if events[0]["goalId"] != goal["id"] || events[0]["budget"] != float64(100) {
			t.Errorf("event %v", events[0])
		}
		if used, _ := events[0]["tokensUsed"].(float64); used < 100 {
			t.Errorf("tokensUsed %v", events[0]["tokensUsed"])
		}

		tail := func() string {
			r.Fire("before_agent_start", map[string]any{"systemPrompt": "base", "prompt": "", "systemPromptOptions": map[string]any{}})
			raw := r.Fire("context", map[string]any{"messages": []any{}})
			var out struct {
				Messages []map[string]any `json:"messages"`
			}
			_ = json.Unmarshal(raw, &out)
			if len(out.Messages) == 0 {
				return ""
			}
			s, _ := out.Messages[len(out.Messages)-1]["content"].(string)
			return s
		}
		first := tail()
		if !strings.Contains(first, "BUDGET LIMITED") {
			t.Errorf("budget-limited block injected: %q", first)
		}
		if !strings.Contains(first, "TOKEN BUDGET REACHED") {
			t.Errorf("one-time wrap-up steering injected: %q", first)
		}
		if second := tail(); strings.Contains(second, "TOKEN BUDGET REACHED") {
			t.Error("steering fires exactly once")
		}
		r.Fire("turn_end", map[string]any{"message": turnEndMessage(50)})
		if n := len(ledgerOf(t, r.Cwd, "goal_budget_limited")); n != 1 {
			t.Errorf("transition fires exactly once: %d", n)
		}
	})

	tw(t, budgetFile, "goal without a budget never transitions", func(t *testing.T) {
		r, _ := budgetRig(t, "No budget", 0, 0)
		r.Fire("session_start", map[string]any{"reason": "start"})
		r.Fire("turn_end", map[string]any{"message": turnEndMessage(5000)})
		active := activeGoalFiles(t, r.Cwd)
		if len(active) != 1 {
			t.Fatalf("active files %v", active)
		}
		if s := diskStatus(t, r.Cwd, active[0]); s != "active" {
			t.Errorf("no budget → no transition: %q", s)
		}
		if n := len(ledgerOf(t, r.Cwd, "goal_budget_limited")); n != 0 {
			t.Errorf("budget events %d", n)
		}
	})

	tw(t, budgetFile, "one accounting update coalesces crossed thresholds and changed budgets warn afresh", func(t *testing.T) {
		r, _ := budgetRig(t, "=== Goal ===\nObjective: Budgeted goal", 100, 80)
		r.Fire("session_start", map[string]any{"reason": "start"})
		warnings := func() int { return len(ledgerOf(t, r.Cwd, "goal_budget_warning")) }
		tokenNotices := func() int {
			n := 0
			for _, c := range r.Host.CallsTo("ui.notify") {
				if strings.HasPrefix(str(c.Args["message"]), "Token budget") {
					n++
				}
			}
			return n
		}
		r.Probe.AccountProgress(15) // 80 -> 95 crosses all three
		if n := warnings(); n != 1 {
			t.Errorf("warnings after crossing: %d", n)
		}
		if n := tokenNotices(); n != 1 {
			t.Errorf("token budget notices: %d", n)
		}
		r.Probe.AccountProgress(1)
		if n := warnings(); n != 1 {
			t.Errorf("warnings after one more token: %d", n)
		}
		r.Probe.SetTokenBudget(120)
		r.Probe.AccountProgress(1)
		if n := warnings(); n != 2 {
			t.Errorf("a changed budget warns afresh: %d", n)
		}
		r.Probe.AccountProgress(12)
		if n := warnings(); n != 3 {
			t.Errorf("warnings after crossing again: %d", n)
		}
	})
}
