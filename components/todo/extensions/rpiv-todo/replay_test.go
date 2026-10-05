package rpiv_todo

import "testing"

const fReplay = "state/replay"

// branch builds session entries the way the host serialises them: a user message followed by one
// toolResult message per snapshot (upstream test-utils: buildSessionEntries, makeTodoToolResult).
func branch(snapshots ...map[string]any) []map[string]any {
	out := []map[string]any{{"type": "message", "message": map[string]any{"role": "user", "content": "hi"}}}
	for _, s := range snapshots {
		out = append(out, map[string]any{"type": "message", "message": map[string]any{"role": "toolResult", "toolName": "todo", "details": s}})
	}
	return out
}

func snap(nextID float64, tasks ...map[string]any) map[string]any {
	ts := []any{}
	for _, t := range tasks {
		ts = append(ts, t)
	}
	return map[string]any{"action": "create", "params": map[string]any{}, "tasks": ts, "nextId": nextID}
}

func jt(id float64, subject string) map[string]any {
	return map[string]any{"id": id, "subject": subject, "status": "pending"}
}

func TestIsTaskDetails(t *testing.T) {
	tw(t, fReplay, "rejects null and undefined", func(t *testing.T) {
		eq(t, isTaskDetails(nil), false, "nil")
	})
	tw(t, fReplay, "rejects primitives (string, number, boolean)", func(t *testing.T) {
		eq(t, isTaskDetails("oops"), false, "string")
		eq(t, isTaskDetails(42.0), false, "number")
		eq(t, isTaskDetails(true), false, "bool")
	})
	tw(t, fReplay, "rejects objects missing tasks[] or nextId", func(t *testing.T) {
		eq(t, isTaskDetails(map[string]any{}), false, "{}")
		eq(t, isTaskDetails(map[string]any{"tasks": "x", "nextId": 1.0}), false, "tasks string")
		eq(t, isTaskDetails(map[string]any{"tasks": []any{}, "nextId": "1"}), false, "nextId string")
	})
	tw(t, fReplay, "accepts well-formed snapshot envelopes", func(t *testing.T) {
		eq(t, isTaskDetails(map[string]any{"tasks": []any{}, "nextId": 1.0}), true, "minimal")
		eq(t, isTaskDetails(map[string]any{"action": "create", "params": map[string]any{}, "tasks": []any{}, "nextId": 1.0}), true, "full")
	})
}

func TestReplayFromBranch(t *testing.T) {
	tw(t, fReplay, "returns empty TaskState when branch has no todo toolResults", func(t *testing.T) {
		s := replayFromBranch(branch())
		eq(t, len(s.Tasks), 0, "tasks")
		eq(t, s.NextID, 1, "nextId")
	})
	tw(t, fReplay, "replays the last snapshot (last-write-wins)", func(t *testing.T) {
		s := replayFromBranch(branch(snap(2, jt(1, "old")), snap(3, jt(1, "old"), jt(2, "new"))))
		eq(t, len(s.Tasks), 2, "tasks")
		eq(t, s.NextID, 3, "nextId")
	})
	tw(t, fReplay, "clones tasks so mutating the fixture does not mutate replayed state", func(t *testing.T) {
		fixture := jt(1, "original")
		s := replayFromBranch(branch(snap(2, fixture)))
		fixture["subject"] = "mutated"
		eq(t, at(s.Tasks, 0).Subject, "original", "subject")
	})
	tw(t, fReplay, "skips non-message entries in the branch (defensive type guard)", func(t *testing.T) {
		b := append([]map[string]any{{"type": "tool_call", "call": map[string]any{"id": "x"}}}, branch(snap(2, jt(1, "kept")))...)
		s := replayFromBranch(b)
		eq(t, len(s.Tasks), 1, "tasks")
		eq(t, at(s.Tasks, 0).Subject, "kept", "subject")
		eq(t, s.NextID, 2, "nextId")
	})
	tw(t, fReplay, "skips toolResult entries whose details fail isTaskDetails (corrupt-snapshot guard)", func(t *testing.T) {
		corrupt := map[string]any{"type": "message", "message": map[string]any{"role": "toolResult", "toolName": "todo", "details": map[string]any{"tasks": "not-an-array"}}}
		s := replayFromBranch(append(branch(snap(2, jt(1, "good"))), corrupt))
		eq(t, len(s.Tasks), 1, "tasks")
		eq(t, at(s.Tasks, 0).Subject, "good", "subject")
	})
	tw(t, fReplay, "returns a fresh empty TaskState when called with an empty branch", func(t *testing.T) {
		eq(t, replayFromBranch(branch(snap(2, jt(1, "x")))).NextID, 2, "first")
		fresh := replayFromBranch(branch())
		eq(t, len(fresh.Tasks), 0, "tasks")
		eq(t, fresh.NextID, 1, "nextId")
	})
}

func TestReplayDecodesEveryTaskField(t *testing.T) {
	full := map[string]any{"id": 4.0, "subject": "s", "description": "d", "activeForm": "a", "status": "in_progress", "blockedBy": []any{1.0, 2.0}, "owner": "o", "metadata": map[string]any{"k": "v"}}
	g := at(replayFromBranch(branch(snap(5, full))).Tasks, 0)
	if g.ID != 4 || *g.Description != "d" || *g.ActiveForm != "a" || g.Status != "in_progress" || len(g.BlockedBy) != 2 || *g.Owner != "o" || g.Metadata["k"] != "v" {
		t.Fatalf("task = %#v", g)
	}
	t.Run("a toolResult of another tool is ignored", func(t *testing.T) {
		other := map[string]any{"type": "message", "message": map[string]any{"role": "toolResult", "toolName": "bash", "details": snap(9, jt(1, "no"))}}
		eq(t, len(replayFromBranch([]map[string]any{other}).Tasks), 0, "tasks")
	})
}
