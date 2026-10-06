package tintinweb_tasks_test

import (
	"path/filepath"
	"testing"
	"time"
)

func TestAgentReattach(t *testing.T) {
	const f = "agent-reattach"
	// sessionWithRunningAgent boots an extension, runs an agent-backed task and leaves the agent running.
	sessionWithRunningAgent := func(t *testing.T) *rig {
		dir := t.TempDir()
		useEnv(t, "PI_TASKS", filepath.Join(dir, "tasks.json"))
		useConfig(t, obj{})
		r := startRig(t, dir)
		m := installSubagents(r)
		r.must("TaskCreate", obj{"subject": "Long job", "description": "d", "agentType": "general-purpose"})
		r.must("TaskExecute", obj{"task_ids": arr{"1"}})
		m.unsub()
		return r
	}
	// reload boots a fresh extension over the same store and announces the reload.
	reload := func(t *testing.T) (*rig, *subMock) {
		r := startRig(t, "")
		m := installSubagents(r)
		r.fireEvent("session_start", obj{"reason": "reload"})
		return r, m
	}
	status := func(r *rig, id string) string { return r.must("TaskGet", obj{"taskId": id}) }
	tw(t, f, "completes the task when the agent finishes after a reload", func(t *testing.T) {
		sessionWithRunningAgent(t)
		r, _ := reload(t)
		r.emit("subagents:completed", obj{"id": "agent-1", "result": "the answer"})
		task := status(r, "1")
		eqv(t, contains(task, "Status: completed"), true)
		eqv(t, contains(task, "the answer"), true)
	})
	tw(t, f, "reverts the task to pending when the agent fails after a reload", func(t *testing.T) {
		sessionWithRunningAgent(t)
		r, _ := reload(t)
		r.emit("subagents:failed", obj{"id": "agent-1", "error": "out of turns", "status": "error"})
		task := status(r, "1")
		eqv(t, contains(task, "Status: pending"), true)
		eqv(t, contains(task, "out of turns"), true)
	})
	tw(t, f, "keeps the partial result when the agent was stopped before a reload", func(t *testing.T) {
		sessionWithRunningAgent(t)
		r, _ := reload(t)
		r.emit("subagents:failed", obj{"id": "agent-1", "result": "half done", "status": "stopped"})
		task := status(r, "1")
		eqv(t, contains(task, "Status: completed"), true)
		eqv(t, contains(task, "half done"), true)
	})
	tw(t, f, "lets a blocking TaskOutput resolve on the reattached agent's event", func(t *testing.T) {
		sessionWithRunningAgent(t)
		r, _ := reload(t)
		ch := make(chan string, 1)
		go func() {
			text, _ := r.tool("TaskOutput", obj{"task_id": "1", "block": true, "timeout": 5000})
			ch <- text
		}()
		time.Sleep(60 * time.Millisecond)
		r.emit("subagents:completed", obj{"id": "agent-1", "result": "done"})
		eqv(t, <-ch, "Task #1 [completed] — subagent agent-1\n\ndone")
	})
	tw(t, f, "resolves an agent ID to its task after a reload", func(t *testing.T) {
		sessionWithRunningAgent(t)
		r, _ := reload(t)
		eqv(t, r.must("TaskOutput", obj{"task_id": "agent-1", "block": false, "timeout": 30000}), "Task #1 [in_progress] — subagent agent-1")
	})
	tw(t, f, "does not reattach a task that is no longer in progress", func(t *testing.T) {
		// A failed agent leaves metadata.agentId behind on a task reverted to pending. Reattaching that
		// would let a late event resurrect work the user reset.
		first := sessionWithRunningAgent(t)
		first.emit("subagents:failed", obj{"id": "agent-1", "error": "boom", "status": "error"})
		eqv(t, contains(status(first, "1"), "Status: pending"), true)
		r, _ := reload(t)
		r.emit("subagents:completed", obj{"id": "agent-1", "result": "late"})
		task := status(r, "1")
		eqv(t, contains(task, "Status: pending"), true)
		eqv(t, contains(task, "late"), false)
	})
	tw(t, f, "ignores a duplicate event after the reattached agent already reported", func(t *testing.T) {
		sessionWithRunningAgent(t)
		r, _ := reload(t)
		r.emit("subagents:completed", obj{"id": "agent-1", "result": "first"})
		// A second session_start must not re-map the now-completed task.
		r.fireEvent("before_agent_start", obj{})
		r.emit("subagents:failed", obj{"id": "agent-1", "error": "late failure", "status": "error"})
		task := status(r, "1")
		eqv(t, contains(task, "Status: completed"), true)
		eqv(t, contains(task, "late failure"), false)
	})
	tw(t, f, "does not carry an agent mapping into the next session", func(t *testing.T) {
		// Task IDs restart at 1 in every session, so a mapping left over from the previous one points at an
		// unrelated task here. The agent's completion would then close a task it never ran.
		useEnv(t, "PI_TASKS", "")
		useConfig(t, obj{})
		r := startRig(t, "")
		m := installSubagents(r)
		defer m.unsub()
		r.setSession("session-a", true)
		r.fireEvent("session_start", obj{"reason": "startup"})
		r.must("TaskCreate", obj{"subject": "A's job", "description": "d", "agentType": "general-purpose"})
		r.must("TaskExecute", obj{"task_ids": arr{"1"}})
		r.setSession("session-b", true)
		r.fireEvent("session_start", obj{"reason": "new"})
		r.must("TaskCreate", obj{"subject": "B's unrelated task", "description": "d"})
		r.emit("subagents:completed", obj{"id": "agent-1", "result": "belongs to session A"})
		task := status(r, "1")
		eqv(t, contains(task, "B's unrelated task"), true)
		eqv(t, contains(task, "Status: pending"), true)
		eqv(t, contains(task, "belongs to session A"), false)
	})
	tw(t, f, "reattaches every running agent, not just the first", func(t *testing.T) {
		dir := t.TempDir()
		useEnv(t, "PI_TASKS", filepath.Join(dir, "tasks.json"))
		useConfig(t, obj{})
		first := startRig(t, dir)
		m := installSubagents(first)
		for _, subject := range []string{"A", "B"} {
			first.must("TaskCreate", obj{"subject": subject, "description": "d", "agentType": "general-purpose"})
		}
		first.must("TaskExecute", obj{"task_ids": arr{"1", "2"}})
		m.unsub()
		r, _ := reload(t)
		r.emit("subagents:completed", obj{"id": "agent-2", "result": "b done"})
		r.emit("subagents:completed", obj{"id": "agent-1", "result": "a done"})
		eqv(t, contains(status(r, "1"), "Status: completed"), true)
		eqv(t, contains(status(r, "2"), "Status: completed"), true)
	})
}

func TestTaskOutputResultConsumption(t *testing.T) {
	const f = "subagent-result-consumption"
	boot := func(t *testing.T) (*rig, *subMock) {
		useEnv(t, "PI_TASKS", "off")
		useConfig(t, obj{})
		r := startRig(t, "")
		m := installSubagents(r)
		r.must("TaskCreate", obj{"subject": "Agent task", "description": "d", "agentType": "general-purpose"})
		r.must("TaskExecute", obj{"task_ids": arr{"1"}})
		return r, m
	}
	wait := func(r *rig, params obj) chan string {
		ch := make(chan string, 1)
		go func() { text, _ := r.tool("TaskOutput", params); ch <- text }()
		time.Sleep(60 * time.Millisecond)
		return ch
	}
	tw(t, f, "returns the agent's result to the blocking caller", func(t *testing.T) {
		r, m := boot(t)
		pending := wait(r, obj{"task_id": "1", "block": true, "timeout": 5000})
		m.complete("agent-1", "TASK_EXECUTE_AGENT_OK")
		eqv(t, <-pending, "Task #1 [completed] — subagent agent-1\n\nTASK_EXECUTE_AGENT_OK")
	})
	tw(t, f, "consumes the result, so no completion notification follows the answer", func(t *testing.T) {
		r, m := boot(t)
		pending := wait(r, obj{"task_id": "1", "block": true, "timeout": 5000})
		m.complete("agent-1", "TASK_EXECUTE_AGENT_OK")
		<-pending
		m.afterNudgeHold()
		_, _, consumed, notified := m.snapshot()
		eqv(t, consumed, []string{"agent-1"})
		eqv(t, notified, []string{})
	})
	tw(t, f, "consumes a result read after the fact, not only one waited for", func(t *testing.T) {
		r, m := boot(t)
		m.complete("agent-1", "TASK_EXECUTE_AGENT_OK")
		text := r.must("TaskOutput", obj{"task_id": "1", "block": false, "timeout": 30000})
		eqv(t, contains(text, "TASK_EXECUTE_AGENT_OK"), true)
		_, _, consumed, _ := m.snapshot()
		eqv(t, consumed, []string{"agent-1"})
	})
	tw(t, f, "returns the failure and consumes it too", func(t *testing.T) {
		// Seeded with output from an earlier run, which the failure listener drops, so the error is what
		// `result ?? lastError` finds.
		r, m := boot(t)
		r.must("TaskUpdate", obj{"taskId": "1", "metadata": obj{"result": "earlier output"}})
		pending := wait(r, obj{"task_id": "1", "block": true, "timeout": 5000})
		m.fail("agent-1", "boom", "error")
		// The failure listener reverts the task to pending so it can be retried.
		eqv(t, <-pending, "Task #1 [pending] — subagent agent-1\n\nError: boom")
		m.afterNudgeHold()
		_, _, consumed, notified := m.snapshot()
		eqv(t, consumed, []string{"agent-1"})
		eqv(t, notified, []string{})
	})
	tw(t, f, "leaves the notification alone while the agent is still running", func(t *testing.T) {
		// Nothing has been read here: the notification is the only thing that will wake the parent.
		r, m := boot(t)
		eqv(t, r.must("TaskOutput", obj{"task_id": "1", "block": false, "timeout": 30000}), "Task #1 [in_progress] — subagent agent-1")
		_, _, consumed, _ := m.snapshot()
		eqv(t, len(consumed), 0)
		m.complete("agent-1", "TASK_EXECUTE_AGENT_OK")
		m.afterNudgeHold()
		_, _, _, notified := m.snapshot()
		eqv(t, notified, []string{"agent-1"})
	})
	tw(t, f, "still hands over the result when pi-subagents predates the consume channel", func(t *testing.T) {
		// The consume RPC is fire-and-forget and outside the version handshake, so a pi-subagents with no
		// handler for it must leave the read untouched: result inline, notification delivered as always.
		r, m := boot(t)
		m.unsub()
		legacy := installSubagents(r, subOpts{withoutConsume: true})
		defer legacy.unsub()
		r.must("TaskCreate", obj{"subject": "Second", "description": "d", "agentType": "general-purpose"})
		r.must("TaskExecute", obj{"task_ids": arr{"2"}})
		pending := wait(r, obj{"task_id": "2", "block": true, "timeout": 5000})
		legacy.complete("agent-1", "TASK_EXECUTE_AGENT_OK")
		eqv(t, <-pending, "Task #2 [completed] — subagent agent-1\n\nTASK_EXECUTE_AGENT_OK")
		legacy.afterNudgeHold()
		_, _, _, notified := legacy.snapshot()
		eqv(t, notified, []string{"agent-1"})
	})
	tw(t, f, "leaves the notification alone when the blocking wait times out", func(t *testing.T) {
		r, m := boot(t)
		eqv(t, contains(r.must("TaskOutput", obj{"task_id": "1", "block": true, "timeout": 30}), "[in_progress]"), true)
		_, _, consumed, _ := m.snapshot()
		eqv(t, len(consumed), 0)
		m.complete("agent-1", "late but still the first anyone hears of it")
		m.afterNudgeHold()
		_, _, _, notified := m.snapshot()
		eqv(t, notified, []string{"agent-1"})
	})
}
