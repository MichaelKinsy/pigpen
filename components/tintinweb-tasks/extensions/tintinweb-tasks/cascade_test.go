package tintinweb_tasks_test

import (
	"testing"
)

func TestAutoCascade(t *testing.T) {
	const f = "auto-cascade"
	boot := func(t *testing.T) (*rig, *subMock) {
		useEnv(t, "PI_TASKS", "")
		useConfig(t, obj{"autoCascade": true, "taskScope": "memory"})
		r := startRig(t, "")
		m := installSubagents(r)
		// Cascade needs the latest context, which only a lifecycle hook sets. Without this the cascade
		// silently no-ops and every assertion below would pass vacuously.
		r.fireEvent("turn_start", obj{})
		return r, m
	}
	createAgentTask := func(r *rig, subject string) string {
		text := r.must("TaskCreate", obj{"subject": subject, "description": "do " + subject, "agentType": "general-purpose"})
		return taskIDRe.FindStringSubmatch(text)[1]
	}
	tw(t, f, "starts a dependent task and records its agent when the blocker completes", func(t *testing.T) {
		r, m := boot(t)
		createAgentTask(r, "Task A")
		createAgentTask(r, "Task B")
		r.must("TaskUpdate", obj{"taskId": "2", "addBlockedBy": arr{"1"}})
		r.must("TaskExecute", obj{"task_ids": arr{"1"}})
		spawned, _, _, _ := m.snapshot()
		eqv(t, len(spawned), 1)
		r.emit("subagents:completed", obj{"id": "agent-1", "result": "done"})
		spawned, _, _, _ = m.snapshot()
		eqv(t, len(spawned), 2)
		eqv(t, spawned[1].Type, "general-purpose")
		eqv(t, spawned[1].Options["isBackground"], true)
		b := r.must("TaskGet", obj{"taskId": "2"})
		eqv(t, contains(b, "Status: in_progress"), true)
		eqv(t, contains(b, "Owner: agent-2"), true)
		eqv(t, contains(b, `"agentId":"agent-2"`), true)
	})
	tw(t, f, "carries the launch model and turn limit into cascaded agents", func(t *testing.T) {
		r, m := boot(t)
		createAgentTask(r, "Task A")
		createAgentTask(r, "Task B")
		r.must("TaskUpdate", obj{"taskId": "2", "addBlockedBy": arr{"1"}})
		r.must("TaskExecute", obj{"task_ids": arr{"1"}, "model": "haiku", "max_turns": 7})
		r.emit("subagents:completed", obj{"id": "agent-1", "result": "done"})
		spawned, _, _, _ := m.snapshot()
		eqv(t, spawned[1].Options["model"], "haiku")
		eqv(t, spawned[1].Options["maxTurns"], 7.0)
	})
	tw(t, f, "waits for every blocker, not just the one that completed", func(t *testing.T) {
		r, m := boot(t)
		createAgentTask(r, "Task A")
		createAgentTask(r, "Task B")
		createAgentTask(r, "Task C")
		r.must("TaskUpdate", obj{"taskId": "3", "addBlockedBy": arr{"1", "2"}})
		r.must("TaskExecute", obj{"task_ids": arr{"1", "2"}})
		spawned, _, _, _ := m.snapshot()
		eqv(t, len(spawned), 2)
		// A done, B still running: C must stay put.
		r.emit("subagents:completed", obj{"id": "agent-1", "result": "a"})
		spawned, _, _, _ = m.snapshot()
		eqv(t, len(spawned), 2)
		eqv(t, contains(r.must("TaskGet", obj{"taskId": "3"}), "Status: pending"), true)
		// B done too: now C cascades.
		r.emit("subagents:completed", obj{"id": "agent-2", "result": "b"})
		spawned, _, _, _ = m.snapshot()
		eqv(t, len(spawned), 3)
	})
	tw(t, f, "does not cascade into tasks that do not depend on the completed one", func(t *testing.T) {
		r, m := boot(t)
		createAgentTask(r, "Task A")
		createAgentTask(r, "Unrelated")
		r.must("TaskExecute", obj{"task_ids": arr{"1"}})
		r.emit("subagents:completed", obj{"id": "agent-1", "result": "done"})
		spawned, _, _, _ := m.snapshot()
		eqv(t, len(spawned), 1)
		eqv(t, contains(r.must("TaskGet", obj{"taskId": "2"}), "Status: pending"), true)
	})
	tw(t, f, "reverts a dependent to pending and records the error when its spawn fails", func(t *testing.T) {
		r, m := boot(t)
		createAgentTask(r, "Task A")
		createAgentTask(r, "Task B")
		r.must("TaskUpdate", obj{"taskId": "2", "addBlockedBy": arr{"1"}})
		r.must("TaskExecute", obj{"task_ids": arr{"1"}})
		// Swap in a subagents extension that refuses to spawn, then complete A.
		m.unsub()
		failing := installSubagents(r, subOpts{spawnError: "no capacity"})
		defer failing.unsub()
		r.emit("subagents:completed", obj{"id": "agent-1", "result": "done"})
		spawned, _, _, _ := failing.snapshot()
		eqv(t, len(spawned), 0)
		b := r.must("TaskGet", obj{"taskId": "2"})
		eqv(t, contains(b, "Status: pending"), true)
		eqv(t, contains(b, "no capacity"), true)
	})
	tw(t, f, "chains through a three-task dependency line", func(t *testing.T) {
		r, m := boot(t)
		createAgentTask(r, "Task A")
		createAgentTask(r, "Task B")
		createAgentTask(r, "Task C")
		r.must("TaskUpdate", obj{"taskId": "2", "addBlockedBy": arr{"1"}})
		r.must("TaskUpdate", obj{"taskId": "3", "addBlockedBy": arr{"2"}})
		r.must("TaskExecute", obj{"task_ids": arr{"1"}})
		r.emit("subagents:completed", obj{"id": "agent-1", "result": "a"})
		r.emit("subagents:completed", obj{"id": "agent-2", "result": "b"})
		spawned, _, _, _ := m.snapshot()
		eqv(t, len(spawned), 3)
		eqv(t, contains(r.must("TaskGet", obj{"taskId": "3"}), "Status: in_progress"), true)
	})
	tw(t, f, "does not cascade when the blocker fails", func(t *testing.T) {
		r, m := boot(t)
		createAgentTask(r, "Task A")
		createAgentTask(r, "Task B")
		r.must("TaskUpdate", obj{"taskId": "2", "addBlockedBy": arr{"1"}})
		r.must("TaskExecute", obj{"task_ids": arr{"1"}})
		r.emit("subagents:failed", obj{"id": "agent-1", "error": "crashed", "status": "error"})
		spawned, _, _, _ := m.snapshot()
		eqv(t, len(spawned), 1)
	})
}
