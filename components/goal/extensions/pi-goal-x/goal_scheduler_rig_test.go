package pi_goal_x_test

// The goal-scheduler twins that drive the whole extension (handlers and tools) rather than the scheduler module: they run on the real
// SDK through goalRig, with the scheduler operated through the Probe and time moved by its mock clock (`t.mock.timers`).
// They need the GoalEvents (tool_call, context, before_agent_start, before_provider_request, session_shutdown) and GoalTools
// (update_goal, update_goal_task) slices.

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	goal "github.com/MichaelKinsy/pigpen/pi-goal-x"
)

const gschRigF = "goal-scheduler"

type rigSched struct {
	*goalRig
	clock *goal.FakeClock
}

// newRigSched is fixture() of tests/goal-scheduler.test.ts on the rig: strictExecutionContract unless lax, maxAutonomousRuns when limit >= 0.
func newRigSched(t *testing.T, limit int, lax bool) *rigSched {
	t.Helper()
	cwd := t.TempDir()
	settings := fmt.Sprintf(`{"strictExecutionContract": %v`, !lax)
	if limit >= 0 {
		settings += fmt.Sprintf(`, "maxAutonomousRuns": %d`, limit)
	}
	rel, text := seedGoal("scheduler-fixture", "Test explicit scheduling")
	r := newGoalRig(t, rigOptions{
		Cwd: cwd, Settings: settings + "}", SessionID: "owner", Seed: map[string]string{rel: text},
		Branch: []any{focusEntry("scheduler-fixture")}, StartSession: true,
		Env: map[string]string{"PI_GOAL_GLOBAL_SETTINGS_FILE": filepath.Join(cwd, "absent-global.json")},
	})
	s := &rigSched{goalRig: r}
	s.clock = r.Probe.FakeClock(0)
	t.Cleanup(s.clock.Restore)
	return s
}

func (s *rigSched) tick(ms int64) { s.clock.Tick(ms) }

func (s *rigSched) sch() map[string]any {
	g := s.Probe.Goal()
	sc, _ := g["scheduler"].(map[string]any)
	return sc
}

func (s *rigSched) dispatchKind() string {
	d, _ := s.sch()["dispatch"].(map[string]any)
	k, _ := d["kind"].(string)
	return k
}

func (s *rigSched) used() float64 { u, _ := s.sch()["used"].(float64); return u }
func (s *rigSched) status() string {
	st, _ := s.Probe.Goal()["status"].(string)
	return st
}

func (s *rigSched) admit() {
	s.t.Helper()
	s.Probe.SchedulerBegin()
	sent := s.Sent()
	if len(sent) == 0 {
		s.t.Fatal("nothing was sent to admit")
	}
	msg := map[string]any{"role": "custom"}
	for k, v := range sent[len(sent)-1].Message {
		msg[k] = v
	}
	s.Probe.SchedulerMessage(msg)
}

func (s *rigSched) toolCall(name string, input map[string]any) {
	s.t.Helper()
	s.Fire("tool_call", map[string]any{"toolName": name, "toolCallId": "call", "input": input})
}

func reMatch(t *testing.T, got, re, what string) {
	t.Helper()
	if !regexp.MustCompile(re).MatchString(got) {
		t.Errorf("%s: %q does not match /%s/", what, got, re)
	}
}

func same(t *testing.T, got, want any, what string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: got %#v, want %#v", what, got, want)
	}
}

func TestGoalSchedulerRigTwins(t *testing.T) {
	tw(t, gschRigF, "every tool category requires the same declaration; one repair then pause", func(t *testing.T) {
		h := newRigSched(t, 10, false)
		for _, name := range []string{"read", "write", "bash", "custom_tool", "update_goal_task"} {
			h.Probe.SchedulerResume()
			h.tick(1)
			h.admit()
			h.toolCall(name, map[string]any{})
			h.Probe.SchedulerSettled(true)
			h.tick(1)
			same(t, h.dispatchKind(), "repair", name)
			h.admit()
			h.Probe.SchedulerSettled(true)
			h.tick(1)
			same(t, h.status(), "paused", name)
			same(t, h.used(), 2.0, name)
		}
	})

	tw(t, gschRigF, "update_goal rejects mixed forms and scheduler cloning protects rollback", func(t *testing.T) {
		h := newRigSched(t, 5, false)
		h.Probe.SchedulerBegin()
		mixed := h.ToolResult("update_goal", map[string]any{"status": "complete", "continuation": map[string]any{"kind": "ready"}})
		same(t, mixed.Terminate, false, "mixed forms do not terminate")
		h.Probe.SchedulerDeclareReady()
		if problems := goal.SchedulerRollbackProtection(h.Probe.Goal()); len(problems) > 0 {
			t.Errorf("rollback protection: %v", problems)
		}
	})

	tw(t, gschRigF, "implicit executions continue without tools through task completion and wrap-up", func(t *testing.T) {
		h := newRigSched(t, -1, true)
		tasks := []any{
			map[string]any{"id": "one", "title": "One", "status": "pending"},
			map[string]any{"id": "two", "title": "Two", "status": "pending"},
		}
		if !h.Probe.PatchGoal(map[string]any{"taskList": map[string]any{"proposedAt": time.Now().UTC().Format(time.RFC3339Nano), "tasks": tasks, "blockCompletion": false}}) {
			t.Fatal("could not set the task list")
		}
		h.Probe.SchedulerBegin()
		for _, id := range []string{"", "one", "two", ""} {
			if id != "" {
				h.ToolResult("update_goal_task", map[string]any{"task_id": id, "status": "complete"})
			}
			h.Probe.SchedulerSettled(true)
			h.tick(1)
			same(t, h.dispatchKind(), "ready", "dispatch kind")
			same(t, h.status(), "active", "status")
			h.admit()
		}
		same(t, len(h.Sent()), 4, "sent")
		same(t, h.used(), 4.0, "used")
		list, _ := h.Probe.Goal()["taskList"].(map[string]any)
		for _, task := range list["tasks"].([]any) {
			same(t, task.(map[string]any)["status"], "complete", "task status")
		}
	})

	gschPromptCacheRig(t)
}

// ---- the prompt-cache twins: the context handler's request-only tails ----

func ctxMessages(t *testing.T, raw json.RawMessage) []map[string]any {
	t.Helper()
	var out struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("context result %s: %v", raw, err)
	}
	return out.Messages
}

func liveTails(msgs []map[string]any) int {
	n := 0
	for _, m := range msgs {
		if m["customType"] == "pi-goal-live-context" {
			n++
		}
	}
	return n
}

func content(m map[string]any) string { s, _ := m["content"].(string); return s }

func gschPromptCacheRig(t *testing.T) {
	asAny := func(ms []map[string]any) []any {
		out := make([]any, len(ms))
		for i, m := range ms {
			out[i] = m
		}
		return out
	}
	request := func(t *testing.T, h *rigSched, history []map[string]any) []map[string]any {
		return ctxMessages(t, h.Fire("context", map[string]any{"messages": asAny(history)}))
	}
	cloneMsgs := func(ms []map[string]any) []map[string]any {
		b, _ := json.Marshal(ms)
		var out []map[string]any
		_ = json.Unmarshal(b, &out)
		return out
	}
	wireText := func(text string, extra map[string]any) map[string]any {
		part := map[string]any{"type": "text", "text": text}
		for k, v := range extra {
			part[k] = v
		}
		return map[string]any{"role": "user", "content": []any{part}}
	}

	tw(t, gschRigF, "prompt cache: normal and custom runs preserve history while refreshing all live goal state", func(t *testing.T) {
		h := newRigSched(t, -1, false)
		history := []map[string]any{{"role": "user", "content": "Work on the goal", "timestamp": 1.0}}
		original := cloneMsgs(history)
		preflight := h.Fire("before_agent_start", map[string]any{"prompt": "continue", "systemPrompt": "host policy", "systemPromptOptions": map[string]any{}})
		// The SDK wire answer carries its own _pig* prompt bookkeeping; what matters is that no systemPrompt/message rewrite is returned.
		var pre map[string]json.RawMessage
		_ = json.Unmarshal(preflight, &pre)
		if _, has := pre["systemPrompt"]; has {
			t.Errorf("goal state never rewrites the system prefix: %s", preflight)
		}
		if _, has := pre["message"]; has {
			t.Errorf("goal state never adds a message in before_agent_start: %s", preflight)
		}
		first := request(t, h, history)
		same(t, first[:len(first)-2], original, "stable policy and volatile counters ride as two request-only tails")
		same(t, liveTails(first), 2, "live tails")
		reMatch(t, content(first[len(first)-2]), `PI GOAL ACTIVE`, "stable tail")
		reMatch(t, content(first[len(first)-1]), `Goal snapshot:`, "volatile tail")
		wire := map[string]any{"messages": []any{
			map[string]any{"role": "user", "content": "Work on the goal"},
			wireText(content(first[len(first)-2]), nil),
			wireText(content(first[len(first)-1]), map[string]any{"cache_control": map[string]any{"type": "ephemeral"}}),
		}}
		var payload struct {
			Messages []struct {
				Content []map[string]any `json:"content"`
			} `json:"messages"`
		}
		raw := h.Fire("before_provider_request", map[string]any{"payload": wire})
		if err := json.Unmarshal(raw, &payload); err != nil || len(payload.Messages) != 3 {
			t.Fatalf("provider payload %s: %v", raw, err)
		}
		cc := func(i int) any { return payload.Messages[i].Content[0]["cache_control"] }
		same(t, cc(0).(map[string]any)["type"], "ephemeral", "registered provider hook places the breakpoint on history")
		same(t, cc(1), nil, "stable tail is transient")
		same(t, cc(2), nil, "volatile tail is transient")
		h.Probe.SetGoalTokensUsed(12345)
		h.Probe.SetGoalField("scheduler", map[string]any{"version": 1.0, "owner": "owner", "generation": "g", "used": 7.0, "phase": "idle", "repairUsed": false})
		toolCall := map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "toolCall", "id": "call-1", "name": "read", "arguments": map[string]any{"path": "README.md"}}}, "timestamp": 2.0}
		toolResult := map[string]any{"role": "toolResult", "toolCallId": "call-1", "toolName": "read", "content": []any{map[string]any{"type": "text", "text": "file contents"}}, "isError": false, "timestamp": 3.0}
		history = append(history, toolCall, toolResult)
		second := request(t, h, history)
		same(t, second[:len(first)], first, "the advanced request literally extends the previous one")
		same(t, second[3:5], []map[string]any{cloneMsgs([]map[string]any{toolCall})[0], cloneMsgs([]map[string]any{toolResult})[0]}, "tool call and result stay adjacent")
		reMatch(t, content(second[1]), `PI GOAL ACTIVE`, "retained policy block")
		same(t, liveTails(second), 3, "changed counters append one small tail; the policy block is retained, not resent")
		reMatch(t, content(second[len(second)-1]), `12345 tokens`, "tokens")
		if regexp.MustCompile(`Autonomous runs`).MatchString(content(second[len(second)-1])) {
			t.Error("an unlimited allowance reports no runs line")
		}
		// An objective edit changes the stable policy block: retention resets once so no stale objective lingers mid-history, and
		// the fresh pair anchors the new prefix.
		h.Probe.SetGoalField("objective", "Changed objective")
		third := request(t, h, history)
		same(t, liveTails(third), 2, "tails after an objective edit")
		same(t, third[:3], cloneMsgs(history), "history")
		reMatch(t, content(third[len(third)-2]), `Changed objective`, "stable tail")
		reMatch(t, content(third[len(third)-1]), `Goal snapshot:`, "volatile tail")
		h.Probe.SchedulerBegin() // Custom-message run bypassing preflight.
		for i := range 4 {
			history = append(history, map[string]any{"role": "custom", "customType": "pi-goal-event", "content": "legacy full prompt",
				"details": map[string]any{"goalId": h.Probe.Goal()["id"], "kind": "checkpoint"}, "timestamp": float64(i + 4)})
			current := request(t, h, history)
			same(t, current[:len(third)-1], third[:len(third)-1], "history stays")
			same(t, liveTails(current), 2, "retained tails are re-sent in place, never duplicated per request")
			before := cloneMsgs(current[:len(current)-1])
			again := request(t, h, history)
			same(t, again[:len(again)-1], before, "a repeated request is stable")
		}
		same(t, history[0], original[0], "request transforms never mutate stored history")
		for _, status := range []string{"paused", "blocked", "budget_limited"} {
			h.Probe.SetGoalField("status", status)
			stopped := request(t, h, history)
			last := content(stopped[len(stopped)-1])
			reMatch(t, last, strings.ToUpper(strings.ReplaceAll(status, "_", " ")), status)
			if strings.Contains(last, "PI GOAL ACTIVE") {
				t.Errorf("%s: a stopped goal's tail says ACTIVE", status)
			}
		}
	})

	tw(t, gschRigF, "prompt cache: interleaved sessions keep per-session transient sets", func(t *testing.T) {
		h := newRigSched(t, -1, false)
		history := []map[string]any{{"role": "user", "content": "Work on the goal", "timestamp": 1.0}}
		first := request(t, h, history)
		// Session B advances the same goal so its transient set differs from A's.
		h.Probe.SetGoalTokensUsed(12345)
		h.Probe.SetGoalField("scheduler", map[string]any{"version": 1.0, "owner": "owner", "generation": "g", "used": 7.0, "phase": "idle", "repairUsed": false})
		h.SetSession("owner-B")
		request(t, h, history)
		h.SetSession("owner")
		wire := map[string]any{"messages": []any{
			map[string]any{"role": "user", "content": "Work on the goal"},
			wireText(content(first[len(first)-2]), nil),
			wireText(content(first[len(first)-1]), map[string]any{"cache_control": map[string]any{"type": "ephemeral"}}),
		}}
		var payload struct {
			Messages []struct {
				Content []map[string]any `json:"content"`
			} `json:"messages"`
		}
		raw := h.Fire("before_provider_request", map[string]any{"payload": wire})
		if err := json.Unmarshal(raw, &payload); err != nil || len(payload.Messages) != 3 {
			t.Fatalf("provider payload %s: %v", raw, err)
		}
		cc := payload.Messages[0].Content[0]["cache_control"]
		m, _ := cc.(map[string]any)
		same(t, m["type"], "ephemeral", "session A's request relocates with A's set even after B overwrote its own")
		same(t, payload.Messages[2].Content[0]["cache_control"], nil, "A's live tail stays transient")
	})

	tw(t, gschRigF, "prompt cache: cleared scheduling instructions vanish from retained history", func(t *testing.T) {
		h := newRigSched(t, -1, false)
		history := []map[string]any{{"role": "user", "content": "Work on the goal", "timestamp": 1.0}}
		// The standing instruction is a saved wait reason. A ready decision no longer carries text: next actions were removed.
		h.Probe.SetGoalField("scheduler", map[string]any{"version": 1.0, "owner": "owner", "generation": "g", "used": 0.0, "phase": "waiting", "repairUsed": false,
			"decision": map[string]any{"kind": "wait"},
			"wait":     map[string]any{"id": "w1", "token": "t1", "reason": "Publish release v1", "deadline": float64(time.Now().UnixMilli() + 60000)}})
		first := request(t, h, history)
		found := false
		for _, m := range first {
			found = found || strings.Contains(content(m), "Publish release v1")
		}
		same(t, found, true, "standing instruction rides the live state")
		// Cleared by omission: a plain scheduler with no decision must not re-issue the order.
		h.Probe.SetGoalField("scheduler", map[string]any{"version": 1.0, "owner": "owner", "generation": "g", "used": 1.0, "phase": "idle", "repairUsed": false})
		advanced := append(cloneMsgs(history), map[string]any{"role": "assistant", "content": "ack", "timestamp": 2.0})
		second := request(t, h, advanced)
		for _, m := range second {
			if strings.Contains(content(m), "Publish release v1") {
				t.Error("no retained tail re-issues the cancelled order")
			}
		}
	})

	tw(t, gschRigF, "prompt cache: model changes and missing identities cannot replay another request's counters", func(t *testing.T) {
		h := newRigSched(t, -1, false)
		history := []map[string]any{{"role": "user", "content": "inspect", "timestamp": 1.0}}
		h.SetModel("provider-a", "model-a")
		request(t, h, history)
		h.Probe.SetGoalTokensUsed(123456)
		for _, model := range [][2]string{{"provider-a", "model-b"}, {"provider-b", "model-b"}} {
			h.SetModel(model[0], model[1])
			out := request(t, h, history)
			same(t, liveTails(out), 2, "tails after a model change")
			reMatch(t, content(out[len(out)-1]), `123456 tokens`, "tokens")
		}
		h.SetSession("-")
		request(t, h, history)
		h.Probe.SetGoalTokensUsed(234567)
		next := request(t, h, history)
		same(t, liveTails(next), 2, "tails without an identity")
		if b, _ := json.Marshal(next); strings.Contains(string(b), "123456 tokens") {
			t.Error("another request's counters were replayed")
		}
		h.SetSession("owner")
		h.SetModel("provider-a", "model-a")
		h.Fire("session_shutdown", map[string]any{})
		restarted := request(t, h, history)
		same(t, liveTails(restarted), 2, "tails after a restart")
		reMatch(t, content(restarted[len(restarted)-1]), `234567 tokens`, "tokens")
	})
}
