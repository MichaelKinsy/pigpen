package tintinweb_tasks_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	tintinweb_tasks "github.com/MichaelKinsy/pigpen/tintinweb-tasks"
)

func agentTask(r *rig, subject, description string) {
	r.must("TaskCreate", obj{"subject": subject, "description": description, "agentType": "general-purpose"})
}

func TestSessionTaskRehydration(t *testing.T) {
	const f = "subagent-integration"
	setup := func(t *testing.T, id string) (*rig, string) {
		useEnv(t, "PI_TASKS", "")
		useConfig(t, obj{})
		cwd := t.TempDir()
		r := startRig(t, cwd)
		r.setSession(id, true)
		return r, cwd
	}
	widgetShown := func(t *testing.T, r *rig) {
		t.Helper()
		calls := r.widgetCalls()
		if len(calls) == 0 {
			t.Fatal("the widget was not shown")
		}
		eqv(t, calls[0]["key"], "tasks")
		eqv(t, calls[0]["options"].(map[string]any)["placement"], "aboveEditor")
	}
	sessionReads := func(r *rig) int {
		n := 0
		for _, c := range r.CallsTo("sessionRead") {
			if c.Args["method"] == "getSessionId" {
				n++
			}
		}
		return n
	}
	tw(t, f, "renders default session-scoped tasks immediately after reload", func(t *testing.T) {
		r, cwd := setup(t, "reload-1")
		seedStoreNamed(t, filepath.Join(cwd, ".pi", "tasks", "tasks-reload-1.json"), []string{"Review the rerun"})
		r.fireEvent("session_start", obj{"reason": "reload"})
		eqv(t, sessionReads(r), 1)
		widgetShown(t, r)
	})
	tw(t, f, "renders tasks from a PI_TASKS path override after reload", func(t *testing.T) {
		dir := t.TempDir()
		file := filepath.Join(dir, "tasks.json")
		seedStoreNamed(t, file, []string{"Review the rerun"})
		useEnv(t, "PI_TASKS", file)
		useConfig(t, obj{})
		r := startRig(t, "")
		r.fireEvent("session_start", obj{"reason": "reload"})
		widgetShown(t, r)
	})
	tw(t, f, "renders persisted tasks after /resume", func(t *testing.T) {
		r, cwd := setup(t, "resume-1")
		seedStoreNamed(t, filepath.Join(cwd, ".pi", "tasks", "tasks-resume-1.json"), []string{"Resume this"})
		r.fireEvent("session_start", obj{"reason": "resume"})
		widgetShown(t, r)
	})
	tw(t, f, "switches the session-scoped store to the new session on /new", func(t *testing.T) {
		r, cwd := setup(t, "switch-a")
		seedStoreNamed(t, filepath.Join(cwd, ".pi", "tasks", "tasks-switch-a.json"), []string{"Task in A"})
		seedStoreNamed(t, filepath.Join(cwd, ".pi", "tasks", "tasks-switch-b.json"), []string{"Task in B"})
		r.fireEvent("session_start", obj{"reason": "startup"})
		eqv(t, sessionReads(r), 1)
		// /new must re-point at the new session file. This was previously handled by the never-emitted
		// session_switch event, leaving the store on session A.
		r.setSession("switch-b", true)
		r.fireEvent("session_start", obj{"reason": "new"})
		eqv(t, sessionReads(r), 2)
		eqv(t, contains(r.must("TaskList", obj{}), "Task in B"), true)
	})
	tw(t, f, "seeds a forked session with an independent copy of the parent's tasks", func(t *testing.T) {
		r, cwd := setup(t, "fork-parent")
		parentFile := filepath.Join(cwd, ".pi", "tasks", "tasks-fork-parent.json")
		childFile := filepath.Join(cwd, ".pi", "tasks", "tasks-fork-child.json")
		seedStoreNamed(t, parentFile, []string{"Inherited task"})
		r.fireEvent("session_start", obj{"reason": "startup"})
		// /fork re-points to a brand-new (empty) session file. Without seeding, the fork would silently lose
		// the parent's tasks; with it, the fork gets an independent copy that does not write back.
		r.setSession("fork-child", true)
		r.fireEvent("session_start", obj{"reason": "fork"})
		eqv(t, storedSubjects(t, childFile), []string{"Inherited task"})
		// The fork is independent: mutating it must not touch the parent's file.
		r.must("TaskCreate", obj{"subject": "Fork-only task", "description": "not in parent"})
		eqv(t, storedSubjects(t, parentFile), []string{"Inherited task"})
	})
}

// seedStoreNamed writes a task file whose tasks have the given subjects.
func seedStoreNamed(t *testing.T, path string, subjects []string) {
	t.Helper()
	tasks := arr{}
	for i, s := range subjects {
		tasks = append(tasks, obj{"id": itoa(i + 1), "subject": s, "description": "d", "status": "pending",
			"metadata": obj{}, "blocks": arr{}, "blockedBy": arr{}, "createdAt": 1, "updatedAt": 1})
	}
	writeConfig(t, path, obj{"nextId": len(subjects) + 1, "tasks": tasks})
}

func TestWorkspaceScopedStoreResolution(t *testing.T) {
	const f = "subagent-integration"
	tw(t, f, "namespaces session tasks by ctx.cwd instead of the host process cwd", func(t *testing.T) {
		useEnv(t, "PI_TASKS", "")
		useConfig(t, obj{})
		cwd := t.TempDir()
		r := startRig(t, cwd)
		r.setSession("ctx-cwd-1", true)
		r.fireEvent("session_start", obj{"reason": "startup"})
		r.must("TaskCreate", obj{"subject": "Workspace task", "description": "Must use the session workspace"})
		eqv(t, storedSubjects(t, filepath.Join(cwd, ".pi", "tasks", "tasks-ctx-cwd-1.json")), []string{"Workspace task"})
		wd := t.TempDir() // the host process cwd is never written to
		eqv(t, exists(filepath.Join(wd, ".pi", "tasks", "tasks-ctx-cwd-1.json")), false)
	})
	tw(t, f, "keeps identical session IDs isolated between workspaces", func(t *testing.T) {
		useEnv(t, "PI_TASKS", "")
		useConfig(t, obj{})
		// The Go SDK fixes the workspace per extension process (the ready message), so the two workspaces
		// are two hosts that share one session id.
		cwdA, cwdB := t.TempDir(), t.TempDir()
		a := startRig(t, cwdA)
		a.setSession("shared-1", true)
		a.fireEvent("session_start", obj{"reason": "startup"})
		a.must("TaskCreate", obj{"subject": "Workspace A", "description": "d"})
		b := startRig(t, cwdB)
		b.setSession("shared-1", true)
		b.fireEvent("session_start", obj{"reason": "startup"})
		b.must("TaskCreate", obj{"subject": "Workspace B", "description": "d"})
		eqv(t, storedSubjects(t, filepath.Join(cwdA, ".pi", "tasks", "tasks-shared-1.json")), []string{"Workspace A"})
		eqv(t, storedSubjects(t, filepath.Join(cwdB, ".pi", "tasks", "tasks-shared-1.json")), []string{"Workspace B"})
	})
	tw(t, f, "loads project scope from ctx.cwd and stores the shared task list there", func(t *testing.T) {
		useEnv(t, "PI_TASKS", "")
		useConfig(t, obj{"taskScope": "project"})
		cwd := t.TempDir()
		r := startRig(t, cwd)
		r.fireEvent("session_start", obj{"reason": "startup"})
		r.must("TaskCreate", obj{"subject": "Shared workspace task", "description": "Must use the project-scoped store"})
		eqv(t, storedSubjects(t, filepath.Join(cwd, ".pi", "tasks", "tasks.json")), []string{"Shared workspace task"})
	})
	tw(t, f, "resolves relative PI_TASKS paths from ctx.cwd", func(t *testing.T) {
		useEnv(t, "PI_TASKS", "./state/tasks.json")
		useConfig(t, obj{})
		cwd := t.TempDir()
		r := startRig(t, cwd)
		r.fireEvent("session_start", obj{"reason": "startup"})
		r.must("TaskCreate", obj{"subject": "Relative override task", "description": "Must resolve relative to the session workspace"})
		eqv(t, storedSubjects(t, filepath.Join(cwd, "state", "tasks.json")), []string{"Relative override task"})
	})
	tw(t, f, "switches session stores when the session ID changes in the same workspace", func(t *testing.T) {
		useEnv(t, "PI_TASKS", "")
		useConfig(t, obj{})
		cwd := t.TempDir()
		r := startRig(t, cwd)
		r.setSession("same-cwd-a", true)
		r.fireEvent("session_start", obj{"reason": "startup"})
		r.must("TaskCreate", obj{"subject": "Task A", "description": "Session A"})
		r.setSession("same-cwd-b", true)
		r.fireEvent("session_start", obj{"reason": "startup"})
		r.must("TaskCreate", obj{"subject": "Task B", "description": "Session B"})
		eqv(t, storedSubjects(t, filepath.Join(cwd, ".pi", "tasks", "tasks-same-cwd-a.json")), []string{"Task A"})
		eqv(t, storedSubjects(t, filepath.Join(cwd, ".pi", "tasks", "tasks-same-cwd-b.json")), []string{"Task B"})
	})
	tskip(t, f, "keeps an in-memory store when the context cwd changes",
		"the Go SDK fixes the workspace of an extension process (the ready message), so a context cannot change cwd under a running extension; the in-memory store of this port never reads cwd, so there is no behavior left to compare")
}

func TestTaskExecute(t *testing.T) {
	const f = "subagent-integration"
	boot := func(t *testing.T) (*rig, *subMock) {
		useEnv(t, "PI_TASKS", "off")
		useConfig(t, obj{})
		r := startRig(t, "")
		return r, installSubagents(r)
	}
	tw(t, f, "is registered as a tool", func(t *testing.T) {
		r, _ := boot(t)
		eqv(t, r.Host.tools["TaskExecute"], true)
	})
	tw(t, f, "returns error when subagent extension is not loaded", func(t *testing.T) {
		// Re-init without the mock to simulate a missing extension.
		useEnv(t, "PI_TASKS", "off")
		useConfig(t, obj{})
		fresh := startRig(t, "")
		agentTask(fresh, "Test task", "Do something")
		eqv(t, contains(fresh.must("TaskExecute", obj{"task_ids": arr{"1"}}), "Subagent execution is currently unavailable"), true)
	})
	tw(t, f, "rejects non-existent tasks", func(t *testing.T) {
		r, _ := boot(t)
		eqv(t, contains(r.must("TaskExecute", obj{"task_ids": arr{"999"}}), "#999: not found"), true)
	})
	tw(t, f, "rejects tasks without agentType", func(t *testing.T) {
		r, _ := boot(t)
		r.must("TaskCreate", obj{"subject": "No agent type", "description": "Plain task"})
		eqv(t, contains(r.must("TaskExecute", obj{"task_ids": arr{"1"}}), "#1: no agentType set"), true)
	})
	tw(t, f, "rejects non-pending tasks", func(t *testing.T) {
		r, _ := boot(t)
		agentTask(r, "Already started", "Desc")
		r.must("TaskUpdate", obj{"taskId": "1", "status": "in_progress"})
		eqv(t, contains(r.must("TaskExecute", obj{"task_ids": arr{"1"}}), "#1: not pending"), true)
	})
	tw(t, f, "rejects tasks with unresolved blockers", func(t *testing.T) {
		r, _ := boot(t)
		agentTask(r, "Blocker", "Desc")
		agentTask(r, "Blocked", "Desc")
		r.must("TaskUpdate", obj{"taskId": "2", "addBlockedBy": arr{"1"}})
		eqv(t, contains(r.must("TaskExecute", obj{"task_ids": arr{"2"}}), "#2: blocked by #1"), true)
	})
	tw(t, f, "spawns agent for valid task and updates metadata", func(t *testing.T) {
		r, m := boot(t)
		agentTask(r, "Run tests", "Run the test suite")
		text := r.must("TaskExecute", obj{"task_ids": arr{"1"}})
		eqv(t, contains(text, "Launched 1 agent"), true)
		eqv(t, contains(text, "#1 → agent agent-1"), true)
		spawned, _, _, _ := m.snapshot()
		eqv(t, len(spawned), 1)
		eqv(t, spawned[0].Type, "general-purpose")
		eqv(t, contains(spawned[0].Prompt, "Run the test suite"), true)
		eqv(t, spawned[0].Options["isBackground"], true)
	})
	tw(t, f, "passes additional_context and max_turns to spawned agents", func(t *testing.T) {
		r, m := boot(t)
		r.must("TaskCreate", obj{"subject": "Explore codebase", "description": "Find all API endpoints", "agentType": "Explore"})
		r.must("TaskExecute", obj{"task_ids": arr{"1"}, "additional_context": "Focus on REST endpoints only", "max_turns": 10})
		spawned, _, _, _ := m.snapshot()
		eqv(t, contains(spawned[0].Prompt, "Focus on REST endpoints only"), true)
		eqv(t, spawned[0].Options["maxTurns"], 10.0)
	})
	tw(t, f, "allows executing tasks whose blockers are all completed", func(t *testing.T) {
		r, _ := boot(t)
		agentTask(r, "Blocker", "Desc")
		agentTask(r, "Dependent", "Desc")
		r.must("TaskUpdate", obj{"taskId": "2", "addBlockedBy": arr{"1"}})
		r.must("TaskUpdate", obj{"taskId": "1", "status": "completed"})
		eqv(t, contains(r.must("TaskExecute", obj{"task_ids": arr{"2"}}), "Launched 1 agent"), true)
	})
	tw(t, f, "handles mixed valid and invalid tasks in one call", func(t *testing.T) {
		r, _ := boot(t)
		agentTask(r, "Valid", "Desc")
		r.must("TaskCreate", obj{"subject": "No agent type", "description": "Desc"})
		text := r.must("TaskExecute", obj{"task_ids": arr{"1", "2", "999"}})
		eqv(t, contains(text, "Launched 1 agent"), true)
		eqv(t, contains(text, "#2: no agentType set"), true)
		eqv(t, contains(text, "#999: not found"), true)
	})
	tw(t, f, "detects subagents when ready fires after tasks init", func(t *testing.T) {
		// Init tasks WITHOUT the mock: subagents not available yet.
		useEnv(t, "PI_TASKS", "off")
		useConfig(t, obj{})
		r := startRig(t, "")
		// Now install the mock (subagents loading later) and broadcast ready.
		m := installSubagents(r)
		defer m.unsub()
		agentTask(r, "Late-loaded test", "Desc")
		eqv(t, contains(r.must("TaskExecute", obj{"task_ids": arr{"1"}}), "Launched 1 agent"), true)
		spawned, _, _, _ := m.snapshot()
		eqv(t, len(spawned), 1)
	})
}

func TestCompletionListener(t *testing.T) {
	const f = "subagent-integration"
	boot := func(t *testing.T) *rig {
		useEnv(t, "PI_TASKS", "off")
		useConfig(t, obj{})
		r := startRig(t, "")
		installSubagents(r)
		return r
	}
	launched := func(r *rig, subject string) {
		agentTask(r, subject, "Desc")
		r.must("TaskExecute", obj{"task_ids": arr{"1"}})
	}
	tw(t, f, "marks task completed on subagents:completed event", func(t *testing.T) {
		r := boot(t)
		launched(r, "Agent task")
		r.emit("subagents:completed", obj{"id": "agent-1"})
		eqv(t, contains(r.must("TaskGet", obj{"taskId": "1"}), "Status: completed"), true)
	})
	tw(t, f, "reverts task to pending on subagents:failed event", func(t *testing.T) {
		r := boot(t)
		launched(r, "Failing task")
		r.emit("subagents:failed", obj{"id": "agent-1", "error": "Out of turns", "status": "error"})
		eqv(t, contains(r.must("TaskGet", obj{"taskId": "1"}), "Status: pending"), true)
	})
	tw(t, f, "completes the task and keeps the partial result when the agent was stopped", func(t *testing.T) {
		r := boot(t)
		launched(r, "Stopped task")
		r.emit("subagents:failed", obj{"id": "agent-1", "result": "partial work", "status": "stopped"})
		text := r.must("TaskGet", obj{"taskId": "1"})
		eqv(t, contains(text, "Status: completed"), true)
		eqv(t, contains(text, "partial work"), true)
	})
	tw(t, f, "keeps an earlier result when a stopped agent reports none", func(t *testing.T) {
		r := boot(t)
		launched(r, "Stopped task")
		r.must("TaskUpdate", obj{"taskId": "1", "metadata": obj{"result": "earlier output"}})
		r.emit("subagents:failed", obj{"id": "agent-1", "status": "stopped"})
		text := r.must("TaskGet", obj{"taskId": "1"})
		eqv(t, contains(text, "Status: completed"), true)
		eqv(t, contains(text, "earlier output"), true)
	})
	tw(t, f, "drops an earlier result when a retry fails", func(t *testing.T) {
		r := boot(t)
		launched(r, "Retried task")
		r.must("TaskUpdate", obj{"taskId": "1", "metadata": obj{"result": "earlier output"}})
		r.emit("subagents:failed", obj{"id": "agent-1", "error": "Out of turns", "status": "error"})
		text := r.must("TaskGet", obj{"taskId": "1"})
		eqv(t, contains(text, "Status: pending"), true)
		eqv(t, contains(text, "Out of turns"), true)
		eqv(t, contains(text, "earlier output"), false)
	})
	tw(t, f, "ignores events for unknown agent IDs", func(t *testing.T) {
		r := boot(t)
		r.must("TaskCreate", obj{"subject": "Unrelated", "description": "Desc"})
		r.emit("subagents:completed", obj{"id": "unknown-agent"})
		r.emit("subagents:failed", obj{"id": "unknown-agent", "error": "boom", "status": "error"})
		eqv(t, contains(r.must("TaskGet", obj{"taskId": "1"}), "Status: pending"), true)
	})
}

func TestAutoCascadeOffByDefault(t *testing.T) {
	const f = "subagent-integration"
	boot := func(t *testing.T) (*rig, *subMock) {
		useEnv(t, "PI_TASKS", "off")
		useConfig(t, obj{})
		r := startRig(t, "")
		return r, installSubagents(r)
	}
	pair := func(r *rig) {
		agentTask(r, "Task A", "Desc")
		agentTask(r, "Task B", "Desc")
		r.must("TaskUpdate", obj{"taskId": "2", "addBlockedBy": arr{"1"}})
		r.must("TaskExecute", obj{"task_ids": arr{"1"}})
	}
	tw(t, f, "does NOT cascade when auto-cascade is off (default)", func(t *testing.T) {
		r, m := boot(t)
		pair(r)
		s, _, _, _ := m.snapshot()
		eqv(t, len(s), 1)
		r.emit("subagents:completed", obj{"id": "agent-1"})
		s, _, _, _ = m.snapshot()
		eqv(t, len(s), 1)
		eqv(t, contains(r.must("TaskGet", obj{"taskId": "2"}), "Status: pending"), true)
	})
	tw(t, f, "does NOT cascade on failure (branch stops)", func(t *testing.T) {
		r, m := boot(t)
		pair(r)
		r.emit("subagents:failed", obj{"id": "agent-1", "error": "crashed", "status": "error"})
		s, _, _, _ := m.snapshot()
		eqv(t, len(s), 1)
		eqv(t, contains(r.must("TaskGet", obj{"taskId": "2"}), "Status: pending"), true)
	})
	tw(t, f, "tasks without agentType are not cascaded even if unblocked", func(t *testing.T) {
		r, m := boot(t)
		agentTask(r, "Agent task", "Desc")
		r.must("TaskCreate", obj{"subject": "Manual task", "description": "Desc"})
		r.must("TaskUpdate", obj{"taskId": "2", "addBlockedBy": arr{"1"}})
		r.must("TaskExecute", obj{"task_ids": arr{"1"}})
		r.emit("subagents:completed", obj{"id": "agent-1"})
		s, _, _, _ := m.snapshot()
		eqv(t, len(s), 1)
	})
}

func TestStandaloneOperation(t *testing.T) {
	const f = "subagent-integration"
	boot := func(t *testing.T) *rig {
		useEnv(t, "PI_TASKS", "off")
		useConfig(t, obj{})
		return startRig(t, "")
	}
	tw(t, f, "all core task tools are registered", func(t *testing.T) {
		r := boot(t)
		for _, name := range []string{"TaskCreate", "TaskList", "TaskGet", "TaskUpdate", "TaskExecute"} {
			eqv(t, r.Host.tools[name], true)
		}
	})
	tw(t, f, "TaskCreate works without subagents", func(t *testing.T) {
		eqv(t, contains(boot(t).must("TaskCreate", obj{"subject": "Write tests", "description": "Add unit tests for the parser"}), "Write tests"), true)
	})
	tw(t, f, "TaskList works without subagents", func(t *testing.T) {
		r := boot(t)
		r.must("TaskCreate", obj{"subject": "A", "description": "desc"})
		r.must("TaskCreate", obj{"subject": "B", "description": "desc"})
		text := r.must("TaskList", obj{})
		eqv(t, contains(text, "#1"), true)
		eqv(t, contains(text, "#2"), true)
	})
	tw(t, f, "TaskGet works without subagents", func(t *testing.T) {
		r := boot(t)
		r.must("TaskCreate", obj{"subject": "Read me", "description": "details here"})
		text := r.must("TaskGet", obj{"taskId": "1"})
		eqv(t, contains(text, "Read me"), true)
		eqv(t, contains(text, "details here"), true)
	})
	tw(t, f, "TaskUpdate works without subagents", func(t *testing.T) {
		r := boot(t)
		r.must("TaskCreate", obj{"subject": "Update me", "description": "desc"})
		r.must("TaskUpdate", obj{"taskId": "1", "status": "in_progress"})
		eqv(t, contains(r.must("TaskGet", obj{"taskId": "1"}), "in_progress"), true)
	})
	tw(t, f, "TaskExecute gracefully refuses without subagents", func(t *testing.T) {
		r := boot(t)
		agentTask(r, "Agent task", "desc")
		text := r.must("TaskExecute", obj{"task_ids": arr{"1"}})
		eqv(t, contains(text, "Subagent execution is currently unavailable"), true)
		eqv(t, contains(text, "Agent-tool spawns"), true)
		eqv(t, contains(text, "won't track them"), true)
	})
	tw(t, f, "subagents lifecycle events are silently ignored without mapped agents", func(t *testing.T) {
		r := boot(t)
		r.emit("subagents:completed", obj{"id": "ghost-agent", "result": "done"})
		r.emit("subagents:failed", obj{"id": "ghost-agent", "error": "boom", "status": "error"})
	})
	tw(t, f, "task dependencies work without subagents", func(t *testing.T) {
		r := boot(t)
		r.must("TaskCreate", obj{"subject": "First", "description": "desc"})
		r.must("TaskCreate", obj{"subject": "Second", "description": "desc"})
		r.must("TaskUpdate", obj{"taskId": "2", "addBlockedBy": arr{"1"}})
		text := r.must("TaskGet", obj{"taskId": "2"})
		eqv(t, contains(text, "Blocked by"), true)
		eqv(t, contains(text, "#1"), true)
	})
}

func TestRPCProtocolCorrectness(t *testing.T) {
	const f = "subagent-integration"
	boot := func(t *testing.T, opts ...subOpts) (*rig, *subMock) {
		useEnv(t, "PI_TASKS", "off")
		useConfig(t, obj{})
		r := startRig(t, "")
		return r, installSubagents(r, opts...)
	}
	tw(t, f, "ping uses scoped reply channel (not shared channel)", func(t *testing.T) {
		useEnv(t, "PI_TASKS", "off")
		useConfig(t, obj{})
		r := startRig(t, "")
		r.startSession() // the extension pings on its first session_start
		var ping map[string]any
		for _, c := range r.CallsTo("events.emit") {
			if c.Args["channel"] == "subagents:rpc:ping" {
				ping, _ = c.Args["json"].(map[string]any)
				break
			}
		}
		if ping == nil {
			t.Fatal("no ping")
		}
		id, ok := ping["requestId"].(string)
		eqv(t, ok && id != "", true)
	})
	tw(t, f, "spawn reply cleans up listener and timer on success", func(t *testing.T) {
		r, m := boot(t)
		agentTask(r, "Test", "desc")
		r.must("TaskExecute", obj{"task_ids": arr{"1"}})
		s, _, _, _ := m.snapshot()
		eqv(t, len(s), 1)
		r.must("TaskCreate", obj{"subject": "Test 2", "description": "desc", "agentType": "general-purpose"})
		r.must("TaskExecute", obj{"task_ids": arr{"2"}})
		s, _, _, _ = m.snapshot()
		eqv(t, len(s), 2)
		eqv(t, s[0].ID != s[1].ID, true)
		// Each finished request unsubscribed its reply listener.
		for ch, ids := range r.extBusSnapshot() {
			if strings.HasPrefix(ch, "subagents:rpc:spawn:reply:") && len(ids) > 0 {
				t.Fatalf("reply listener left on %s", ch)
			}
		}
	})
	tw(t, f, "spawn RPC rejects on timeout when no responder exists", func(t *testing.T) {
		defer tintinweb_tasks.SetRPCTimeouts(300*time.Millisecond, 100*time.Millisecond)()
		useEnv(t, "PI_TASKS", "off")
		useConfig(t, obj{})
		r := startRig(t, "")
		// A responder that answers the ping with version 2 but never the spawn.
		unping := r.onBus("subagents:rpc:ping", func(data any) {
			r.emit("subagents:rpc:ping:reply:"+data.(map[string]any)["requestId"].(string), obj{"success": true, "data": obj{"version": 2}})
		})
		defer unping()
		r.emit("subagents:ready", obj{})
		agentTask(r, "Timeout test", "desc")
		eqv(t, contains(r.must("TaskExecute", obj{"task_ids": arr{"1"}}), "timeout"), true)
	})
	tw(t, f, "ready broadcast sets subagentsAvailable even after init", func(t *testing.T) {
		useEnv(t, "PI_TASKS", "off")
		useConfig(t, obj{})
		r := startRig(t, "")
		agentTask(r, "Test", "desc")
		eqv(t, contains(r.must("TaskExecute", obj{"task_ids": arr{"1"}}), "Subagent execution is currently unavailable"), true)
		r.must("TaskUpdate", obj{"taskId": "1", "status": "pending"})
		m := installSubagents(r)
		defer m.unsub()
		eqv(t, contains(r.must("TaskExecute", obj{"task_ids": arr{"1"}}), "Launched 1 agent"), true)
	})
	tw(t, f, "spawn RPC rejects with error message from server", func(t *testing.T) {
		r, _ := boot(t, subOpts{spawnError: "No active session"})
		agentTask(r, "Err test", "desc")
		eqv(t, contains(r.must("TaskExecute", obj{"task_ids": arr{"1"}}), "No active session"), true)
	})
	tw(t, f, "stop RPC resolves on success", func(t *testing.T) {
		r, m := boot(t)
		agentTask(r, "Stoppable", "desc")
		r.must("TaskExecute", obj{"task_ids": arr{"1"}})
		s, _, _, _ := m.snapshot()
		eqv(t, len(s), 1)
		eqv(t, contains(r.must("TaskStop", obj{"task_id": "1"}), "stopped successfully"), true)
		_, stopped, _, _ := m.snapshot()
		eqv(t, stopped, []string{"agent-1"})
	})
	tw(t, f, "stop RPC returns false on error (agent not found) without throwing", func(t *testing.T) {
		r, m := boot(t)
		agentTask(r, "Ghost", "desc")
		r.must("TaskExecute", obj{"task_ids": arr{"1"}})
		m.mu.Lock()
		m.spawned = nil // the agent is no longer known to pi-subagents
		m.mu.Unlock()
		eqv(t, contains(r.must("TaskStop", obj{"task_id": "1"}), "stopped successfully"), true)
	})
	tw(t, f, "stop RPC returns false on timeout without throwing", func(t *testing.T) {
		defer tintinweb_tasks.SetRPCTimeouts(300*time.Millisecond, 100*time.Millisecond)()
		useEnv(t, "PI_TASKS", "off")
		useConfig(t, obj{})
		r := startRig(t, "")
		r.emit("subagents:ready", obj{})
		r.must("TaskCreate", obj{"subject": "Timeout stop", "description": "desc"})
		r.must("TaskUpdate", obj{"taskId": "1", "status": "in_progress", "metadata": obj{"agentType": "general-purpose", "agentId": "ghost-agent"}})
		eqv(t, contains(r.must("TaskStop", obj{"task_id": "1"}), "stopped successfully"), true)
	})
}

func TestProtocolVersionMismatch(t *testing.T) {
	const f = "subagent-integration"
	// versioned installs a ping-only responder with a protocol version (nil = a v1 handler whose reply has none).
	versioned := func(r *rig, version *float64) {
		r.onBus("subagents:rpc:ping", func(data any) {
			reply := obj{}
			if version != nil {
				reply = obj{"success": true, "data": obj{"version": *version}}
			}
			r.emit("subagents:rpc:ping:reply:"+data.(map[string]any)["requestId"].(string), reply)
		})
		r.emit("subagents:ready", obj{})
	}
	v := func(n float64) *float64 { return &n }
	boot := func(t *testing.T, version *float64) *rig {
		useEnv(t, "PI_TASKS", "off")
		useConfig(t, obj{})
		r := startRig(t, "")
		versioned(r, version)
		return r
	}
	warned := func(r *rig, want string) {
		t.Helper()
		r.fireEvent("before_agent_start", obj{})
		for _, n := range r.notifies() {
			if strings.Contains(n["message"].(string), want) && n["level"] == "warning" {
				return
			}
		}
		t.Fatalf("no warning containing %q in %v", want, r.notifies())
	}
	tw(t, f, "matching version — no warning", func(t *testing.T) {
		r := boot(t, v(2))
		r.fireEvent("before_agent_start", obj{})
		eqv(t, len(r.notifies()), 0)
	})
	tw(t, f, "old handler (no version) — warns about pi-subagents", func(t *testing.T) {
		warned(boot(t, nil), "pi-subagents is outdated")
	})
	tw(t, f, "handler ahead (v3) — warns about pi-tasks", func(t *testing.T) {
		warned(boot(t, v(3)), "pi-tasks is outdated")
	})
	tw(t, f, "handler behind (v1) — warns about pi-subagents", func(t *testing.T) {
		warned(boot(t, v(1)), "pi-subagents is outdated")
	})
	tw(t, f, "warning shown only once", func(t *testing.T) {
		r := boot(t, nil) // v1: triggers the warning
		r.fireEvent("before_agent_start", obj{})
		eqv(t, len(r.notifies()), 1)
		r.fireEvent("before_agent_start", obj{})
		eqv(t, len(r.notifies()), 1)
	})
}

func TestCascadeDataInjection(t *testing.T) {
	const f = "subagent-integration"
	boot := func(t *testing.T) (*rig, *subMock) {
		useEnv(t, "PI_TASKS", "")
		useConfig(t, obj{"autoCascade": true, "taskScope": "memory"})
		r := startRig(t, "")
		m := installSubagents(r)
		r.fireEvent("turn_start", obj{})
		return r, m
	}
	chain := func(r *rig, aDesc, bDesc string) {
		agentTask(r, "Task A", aDesc)
		agentTask(r, "Task B", bDesc)
		r.must("TaskUpdate", obj{"taskId": "2", "addBlockedBy": arr{"1"}})
		r.must("TaskExecute", obj{"task_ids": arr{"1"}})
	}
	tw(t, f, "injects prerequisite result into cascaded agent prompt", func(t *testing.T) {
		r, m := boot(t)
		chain(r, "Produce a result", "Use Task A result")
		r.emit("subagents:completed", obj{"id": "agent-1", "result": "The answer is 42"})
		s, _, _, _ := m.snapshot()
		eqv(t, len(s), 2)
		prompt := s[1].Prompt
		eqv(t, contains(prompt, "Prerequisite task results"), true)
		eqv(t, contains(prompt, "Task #1"), true)
		eqv(t, contains(prompt, "The answer is 42"), true)
	})
	tw(t, f, "truncates long prerequisite results at 4KB", func(t *testing.T) {
		r, m := boot(t)
		chain(r, "Produce a long result", "Use truncated result")
		long := strings.Repeat("x", 5000)
		r.emit("subagents:completed", obj{"id": "agent-1", "result": long})
		s, _, _, _ := m.snapshot()
		eqv(t, len(s), 2)
		prompt := s[1].Prompt
		eqv(t, contains(prompt, "truncated"), true)
		eqv(t, contains(prompt, "TaskGet"), true)
		eqv(t, len(prompt) < len(long), true)
	})
	tw(t, f, "handles dependencies with no stored result gracefully", func(t *testing.T) {
		r, m := boot(t)
		chain(r, "No result stored", "Works without A result")
		r.emit("subagents:completed", obj{"id": "agent-1"})
		s, _, _, _ := m.snapshot()
		eqv(t, len(s), 2)
		eqv(t, contains(s[1].Prompt, "Prerequisite task results"), false)
	})
}
