package pi_goal_x

import (
	"reflect"
	"testing"
)

const gssF = "goal-session-safety"

type gssSession struct {
	idle *bool
	id   *string
}

func (s gssSession) sessionKey() any { return *s.id }
func (s gssSession) isIdle() bool    { return *s.idle }

type gssSent struct {
	message any
	trigger bool
}

type gssPi struct{ sent *[]gssSent }

func (p gssPi) sendMessage(message any, triggerTurn bool) {
	*p.sent = append(*p.sent, gssSent{message, triggerTurn})
}

func gssMsg(kv ...any) map[string]any {
	m := map[string]any{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return m
}

func gssWith(m map[string]any, kv ...any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		out[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		out[kv[i].(string)] = kv[i+1]
	}
	return out
}

func TestGoalSessionSafetyTwins(t *testing.T) {
	audit := gssMsg("role", "custom", "customType", "pi-goal-audit-event", "content", "Audit starting", "display", true)
	checkpoint := gssMsg("role", "custom", "customType", "pi-goal-event", "content", `<pi_goal_continuation goal_id="goal" kind="checkpoint" v="2"/>`, "display", false,
		"details", gssMsg("kind", "checkpoint", "goalId", "goal"))

	tw(t, gssF, "audit messages wait for idle settlement, preserve order and never trigger turns", func(t *testing.T) {
		idle, id := false, "session-a"
		ctx := gssSession{&idle, &id}
		var sent []gssSent
		pi := gssPi{&sent}
		queue := &goalAuditMessages{}
		approved := gssWith(audit, "content", "Audit approved")
		queue.enqueue(ctx, audit)
		queue.enqueue(ctx, approved)
		queue.flush(ctx, pi)
		eqDeep(t, len(sent), 0, "nothing is sent while busy")
		idle = true
		queue.flush(ctx, pi)
		queue.flush(ctx, pi)
		eqDeep(t, sent, []gssSent{{audit, false}, {approved, false}}, "in order, never triggering a turn, once")
		queue.enqueue(ctx, audit)
		id = "session-b"
		queue.flush(ctx, pi)
		eqDeep(t, len(sent), 2, "events cannot leak into another session")
		queue.enqueue(ctx, audit)
		queue.clear()
		queue.flush(ctx, pi)
		eqDeep(t, len(sent), 2, "session/tree/shutdown boundaries discard pending events")
	})

	tw(t, gssF, "provider filtering preserves actual conversation and unrelated custom messages", func(t *testing.T) {
		assistant := gssMsg("role", "assistant", "content", []any{gssMsg("type", "toolCall", "id", "a")})
		tool := gssMsg("role", "toolResult", "toolCallId", "a", "content", "result")
		other := gssMsg("role", "custom", "customType", "other-extension", "content", "keep")
		keep := gssMsg("role", "user", "content", "keep", "customType", "pi-goal-audit-event")
		ordinary := []any{assistant, tool, other, keep}
		source := []any{assistant, audit, tool, checkpoint, other, keep}
		eqDeep(t, filterGoalSessionContext(source, false), []any{assistant, tool, checkpoint, other, keep}, "parent: only display-only audits go")
		eqDeep(t, filterGoalSessionContext(source, true), ordinary, "child: goal events go too")
		eqDeep(t, filterGoalSessionContext(ordinary, false) == nil, true, "nothing to remove is null")
		if !reflect.DeepEqual(source[1], audit) || len(source) != 6 {
			t.Error("history is mutated")
		}
	})

	tw(t, gssF, "child detection uses launch markers, not unrelated subagent settings", func(t *testing.T) {
		for _, env := range []map[string]string{{"PI_SUBAGENT_CHILD": "1"}, {"PI_SUBAGENT_DEPTH": "1"}, {"PI_SUBAGENT_DEPTH": "4"}} {
			eq(t, isDelegatedGoalSession(env), true, "delegated")
		}
		for _, value := range []*string{nil, new(""), new("0"), new("-1"), new("NaN"), new("Infinity")} {
			env := map[string]string{"PI_SUBAGENT_MAX_DEPTH": "4"}
			if value != nil {
				env["PI_SUBAGENT_DEPTH"] = *value
			}
			eq(t, isDelegatedGoalSession(env), false, "not delegated")
		}
	})

	tw(t, gssF, "child installer registers only context filtering, without constructing goal state", func(t *testing.T) {
		t.Setenv("PI_SUBAGENT_CHILD", "1")
		ext, c := buildGoalExtension(goalDeps{})
		eq(t, c == nil, true, "no goal state is constructed")
		r := registrationOf(t, ext)
		eq(t, r.Events, []string{"context"}, "events")
		eq(t, len(r.Tools)+len(r.Commands)+len(r.Renderers), 0, "no tools, commands or renderers")
		got, err := goalDelegatedContext(map[string]any{"messages": []any{audit, checkpoint, gssMsg("role", "user", "content", "assignment")}})
		if err != nil {
			t.Fatal(err)
		}
		eqDeep(t, got, map[string]any{"messages": []any{gssMsg("role", "user", "content", "assignment")}}, "context result")
	})
}
