package tintinweb_tasks_test

import (
	"path/filepath"
	"testing"
	"time"
)

func TestTaskOutputAndStop(t *testing.T) {
	const f = "task-output-stop"
	boot := func(t *testing.T, env ...string) (*rig, *subMock) {
		useEnv(t, "PI_TASKS", "off")
		useEnv(t, env...)
		useConfig(t, obj{})
		r := startRig(t, "")
		return r, installSubagents(r)
	}
	// launchAgentTask creates an agent-backed task and launches it.
	launch := func(r *rig) {
		r.must("TaskCreate", obj{"subject": "Agent task", "description": "d", "agentType": "general-purpose"})
		r.must("TaskExecute", obj{"task_ids": arr{"1"}})
	}
	// async runs a tool call in the background, so the test can emit an event while it waits.
	type outcome struct{ text, failure string }
	async := func(r *rig, name string, params obj) chan outcome {
		ch := make(chan outcome, 1)
		go func() { text, failure := r.tool(name, params); ch <- outcome{text, failure} }()
		time.Sleep(60 * time.Millisecond) // let the call start waiting (the original awaits flush())
		return ch
	}
	tw(t, f, "returns the current status without waiting when block is false", func(t *testing.T) {
		r, _ := boot(t)
		launch(r)
		eqv(t, r.must("TaskOutput", obj{"task_id": "1", "block": false, "timeout": 30000}), "Task #1 [in_progress] — subagent agent-1")
	})
	tw(t, f, "resolves a blocking wait when the agent completes", func(t *testing.T) {
		r, _ := boot(t)
		launch(r)
		pending := async(r, "TaskOutput", obj{"task_id": "1", "block": true, "timeout": 5000})
		r.emit("subagents:completed", obj{"id": "agent-1", "result": "done"})
		eqv(t, contains((<-pending).text, "[completed]"), true)
	})
	tw(t, f, "resolves a blocking wait when the agent fails", func(t *testing.T) {
		r, _ := boot(t)
		launch(r)
		pending := async(r, "TaskOutput", obj{"task_id": "1", "block": true, "timeout": 5000})
		r.emit("subagents:failed", obj{"id": "agent-1", "error": "boom", "status": "error"})
		// The failure listener reverts the task to pending so it can be retried.
		eqv(t, contains((<-pending).text, "[pending]"), true)
	})
	tw(t, f, "gives up after the timeout when the agent never reports back", func(t *testing.T) {
		r, _ := boot(t)
		launch(r)
		started := time.Now()
		text := r.must("TaskOutput", obj{"task_id": "1", "block": true, "timeout": 60})
		eqv(t, time.Since(started) >= 50*time.Millisecond, true)
		eqv(t, contains(text, "[in_progress]"), true)
	})
	tskip(t, f, "stops waiting when the tool call is aborted",
		"the fake host has no frame that cancels one request; the port's wait ends on the request's Done channel, which the SDK closes when the host cancels (see the extension's code)")
	tw(t, f, "does not wait for a task that is no longer in_progress", func(t *testing.T) {
		r, _ := boot(t)
		launch(r)
		r.must("TaskUpdate", obj{"taskId": "1", "status": "completed"})
		// block=true with a long timeout: this must return immediately, not hang.
		started := time.Now()
		eqv(t, contains(r.must("TaskOutput", obj{"task_id": "1", "block": true, "timeout": 30000}), "[completed]"), true)
		eqv(t, time.Since(started) < 5*time.Second, true)
	})
	tw(t, f, "throws for an unknown ID", func(t *testing.T) {
		r, _ := boot(t)
		_, failure := r.tool("TaskOutput", obj{"task_id": "99", "block": false, "timeout": 30000})
		eqv(t, contains(failure, "No task found with ID 99"), true)
	})
	tw(t, f, "rejects an empty ID instead of matching an arbitrary agent", func(t *testing.T) {
		// Every agent ID starts with "", so an empty id would prefix-match whichever entry came first.
		r, _ := boot(t)
		launch(r)
		_, failure := r.tool("TaskOutput", obj{"task_id": "", "block": false, "timeout": 30000})
		eqv(t, contains(failure, "task_id is required"), true)
	})
	tw(t, f, "throws for a task with neither a process nor an agent", func(t *testing.T) {
		r, _ := boot(t)
		r.must("TaskCreate", obj{"subject": "Manual", "description": "d"})
		_, failure := r.tool("TaskOutput", obj{"task_id": "1", "block": false, "timeout": 30000})
		eqv(t, contains(failure, "No background process for task 1"), true)
	})
	tw(t, f, "reports the resolved task, not a stale pre-wait snapshot", func(t *testing.T) {
		// Regression: resolvedId was computed from agentTaskMap and then discarded, so the status was read
		// with the caller's agent ID. Only reproducible file-backed.
		r, _ := boot(t, "PI_TASKS", filepath.Join(t.TempDir(), "tasks.json"))
		launch(r)
		pending := async(r, "TaskOutput", obj{"task_id": "agent-1", "block": true, "timeout": 5000})
		r.emit("subagents:completed", obj{"id": "agent-1", "result": "done"})
		eqv(t, (<-pending).text, "Task #1 [completed] — subagent agent-1\n\ndone")
	})
	tw(t, f, "resolves an agent ID to its task when not blocking", func(t *testing.T) {
		r, _ := boot(t)
		launch(r)
		eqv(t, r.must("TaskOutput", obj{"task_id": "agent-1", "block": false, "timeout": 30000}), "Task #1 [in_progress] — subagent agent-1")
	})
	tw(t, f, "resolves a unique agent ID prefix", func(t *testing.T) {
		// Partial prefixes are documented as accepted, and take the startsWith branch.
		r, _ := boot(t)
		launch(r)
		eqv(t, r.must("TaskOutput", obj{"task_id": "agent-", "block": false, "timeout": 30000}), "Task #1 [in_progress] — subagent agent-1")
	})
	tw(t, f, "stops the agent and completes the task", func(t *testing.T) {
		r, m := boot(t)
		launch(r)
		eqv(t, r.must("TaskStop", obj{"task_id": "1"}), "Task #1 stopped successfully")
		_, stopped, _, _ := m.snapshot()
		eqv(t, stopped, []string{"agent-1"})
		eqv(t, contains(r.must("TaskGet", obj{"taskId": "1"}), "Status: completed"), true)
	})
	tw(t, f, "completes the task when stopped by agent ID", func(t *testing.T) {
		// Regression: the agent was stopped and success reported, but the store update used the caller's
		// agent ID instead of the resolved task ID, so the task stayed in_progress forever.
		r, m := boot(t)
		launch(r)
		eqv(t, r.must("TaskStop", obj{"task_id": "agent-1"}), "Task #1 stopped successfully")
		_, stopped, _, _ := m.snapshot()
		eqv(t, stopped, []string{"agent-1"})
		eqv(t, contains(r.must("TaskGet", obj{"taskId": "1"}), "Status: completed"), true)
	})
	tw(t, f, "completes the task when stopped by a unique agent ID prefix", func(t *testing.T) {
		r, m := boot(t)
		launch(r)
		eqv(t, r.must("TaskStop", obj{"task_id": "agent-"}), "Task #1 stopped successfully")
		_, stopped, _, _ := m.snapshot()
		eqv(t, stopped, []string{"agent-1"})
		eqv(t, contains(r.must("TaskGet", obj{"taskId": "1"}), "Status: completed"), true)
	})
	tw(t, f, "accepts the deprecated shell_id parameter", func(t *testing.T) {
		r, m := boot(t)
		launch(r)
		eqv(t, r.must("TaskStop", obj{"shell_id": "1"}), "Task #1 stopped successfully")
		_, stopped, _, _ := m.snapshot()
		eqv(t, stopped, []string{"agent-1"})
	})
	tw(t, f, "throws when neither task_id nor shell_id is given", func(t *testing.T) {
		r, _ := boot(t)
		_, failure := r.tool("TaskStop", obj{})
		eqv(t, contains(failure, "task_id is required"), true)
	})
	tw(t, f, "throws for a task with no running agent", func(t *testing.T) {
		r, _ := boot(t)
		r.must("TaskCreate", obj{"subject": "Manual", "description": "d"})
		_, failure := r.tool("TaskStop", obj{"task_id": "1"})
		eqv(t, contains(failure, "No running background process for task 1"), true)
	})
	tw(t, f, "throws for an unknown ID", func(t *testing.T) {
		// This title is shared by the TaskOutput and TaskStop groups of the original; this is TaskStop's.
		r, _ := boot(t)
		_, failure := r.tool("TaskStop", obj{"task_id": "99"})
		eqv(t, contains(failure, "No running background process for task 99"), true)
	})
	tw(t, f, "does not re-stop an already completed agent task", func(t *testing.T) {
		r, m := boot(t)
		launch(r)
		r.emit("subagents:completed", obj{"id": "agent-1", "result": "done"})
		_, failure := r.tool("TaskStop", obj{"task_id": "1"})
		eqv(t, contains(failure, "No running background process for task 1"), true)
		_, stopped, _, _ := m.snapshot()
		eqv(t, len(stopped), 0)
	})
}
