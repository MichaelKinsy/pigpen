package tintinweb_tasks_test

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestExtensionExtra(t *testing.T) {
	boot := func(t *testing.T, cfg obj) (*rig, string) {
		useEnv(t, "PI_TASKS", "")
		useConfig(t, cfg)
		cwd := t.TempDir()
		return startRig(t, cwd), cwd
	}
	t.Run("a pending-only list is reminded after four turns, not three", func(t *testing.T) {
		r, _ := boot(t, obj{"taskScope": "memory"})
		r.fireEvent("session_start", obj{"reason": "startup"})
		r.must("TaskCreate", obj{"subject": "Waiting", "description": "d"})
		for i := 0; i < 3; i++ {
			r.fireEvent("turn_start", obj{})
		}
		r.fireEvent("tool_result", obj{"toolName": "read"})
		eqv(t, len(r.contextMessages()), 0)
		r.fireEvent("turn_start", obj{})
		r.fireEvent("tool_result", obj{"toolName": "read"})
		eqv(t, contains(r.reminderText(), "Waiting"), true)
	})
	t.Run("the config is read again when a session starts", func(t *testing.T) {
		r, cwd := boot(t, obj{})
		r.fireEvent("session_start", obj{"reason": "startup"})
		r.setScript(nil)
		r.runTasks()
		eqv(t, r.selects[0].Choices[len(r.selects[0].Choices)-1], "Settings")
		// Settings shows the effective value of each row.
		r.setScript("Settings", nil, nil)
		r.runTasks()
		eqv(t, contains(r.selects[2].Choices[0], "Task storage: session"), true)
		writeConfig(t, filepath.Join(cwd, ".pi", "tasks-config.json"), obj{"taskScope": "project"})
		r.fireEvent("session_start", obj{"reason": "reload"})
		r.setScript("Settings", nil, nil)
		r.runTasks()
		last := r.selects[len(r.selects)-2].Choices[0]
		eqv(t, contains(last, "Task storage: project"), true)
	})
	t.Run("TaskUpdate reports every warning, joined by semicolons", func(t *testing.T) {
		r, _ := boot(t, obj{"taskScope": "memory"})
		r.must("TaskCreate", obj{"subject": "A", "description": "d"})
		text := r.must("TaskUpdate", obj{"taskId": "1", "addBlocks": arr{"1", "9999"}})
		eqv(t, text, "Updated task #1 blocks (warning: #1 blocks itself; #9999 does not exist)")
	})
	t.Run("TaskList shows only the blockers that are still open", func(t *testing.T) {
		r, _ := boot(t, obj{"taskScope": "memory"})
		r.must("TaskCreate", obj{"subject": "First", "description": "d"})
		r.must("TaskCreate", obj{"subject": "Second", "description": "d"})
		r.must("TaskCreate", obj{"subject": "Third", "description": "d"})
		r.must("TaskUpdate", obj{"taskId": "3", "addBlockedBy": arr{"1", "2"}})
		r.must("TaskUpdate", obj{"taskId": "1", "status": "completed"})
		eqv(t, contains(r.must("TaskList", obj{}), "#3 [pending] Third [blocked by #2]"), true)
	})
	t.Run("a task set back to pending ends the completed batch's countdown", func(t *testing.T) {
		r, _ := boot(t, obj{"taskScope": "memory"})
		r.fireEvent("session_start", obj{"reason": "startup"})
		r.fireEvent("turn_start", obj{})
		r.must("TaskCreate", obj{"subject": "A", "description": "d"})
		r.must("TaskCreate", obj{"subject": "B", "description": "d"})
		r.must("TaskUpdate", obj{"taskId": "1", "status": "completed"})
		r.must("TaskUpdate", obj{"taskId": "2", "status": "completed"}) // the countdown starts
		r.must("TaskUpdate", obj{"taskId": "1", "status": "pending"})   // work is open again: it ends
		for i := 0; i < 6; i++ {
			r.fireEvent("turn_start", obj{})
		}
		eqv(t, contains(r.must("TaskList", obj{}), "#2 [completed] B"), true)
	})
	t.Run("TaskExecute finds a pi-subagents that never announced itself", func(t *testing.T) {
		useEnv(t, "PI_TASKS", "off")
		useConfig(t, obj{})
		r := startRig(t, "")
		r.onBus("subagents:rpc:ping", func(data any) { // answers a ping, but never broadcasts ready
			r.emit("subagents:rpc:ping:reply:"+data.(map[string]any)["requestId"].(string), obj{"success": true, "data": obj{"version": 2.0}})
		})
		r.onBus("subagents:rpc:spawn", func(data any) {
			r.emit("subagents:rpc:spawn:reply:"+data.(map[string]any)["requestId"].(string), obj{"success": true, "data": obj{"id": "agent-9"}})
		})
		r.must("TaskCreate", obj{"subject": "Job", "description": "d", "agentType": "general-purpose"})
		eqv(t, contains(r.must("TaskExecute", obj{"task_ids": arr{"1"}}), "agent agent-9"), true)
	})
	t.Run("TaskExecute records the agent as the task's owner", func(t *testing.T) {
		useEnv(t, "PI_TASKS", "off")
		useConfig(t, obj{})
		r := startRig(t, "")
		installSubagents(r)
		r.must("TaskCreate", obj{"subject": "Job", "description": "d", "agentType": "general-purpose"})
		r.must("TaskExecute", obj{"task_ids": arr{"1"}})
		text := r.must("TaskGet", obj{"taskId": "1"})
		eqv(t, contains(text, "Owner: agent-1"), true)
		eqv(t, contains(r.must("TaskList", obj{}), "(agent-1)"), true)
	})
}

// A ping sent while the extension is still loading deadlocks the host when pi-subagents is loaded in the same host
// (its reply is dispatched to the loading extension): found with both extensions built into one Binary. The ping
// therefore waits for the first session_start, and is sent once.
func TestPingWaitsForTheFirstSession(t *testing.T) {
	useEnv(t, "PI_TASKS", "off")
	useConfig(t, obj{})
	r := startRig(t, "")
	time.Sleep(150 * time.Millisecond)
	eqv(t, len(r.emittedOn("subagents:rpc:ping")), 0)
	r.startSession()
	r.fireEvent("session_start", obj{"reason": "resume"})
	time.Sleep(100 * time.Millisecond)
	eqv(t, len(r.emittedOn("subagents:rpc:ping")), 1)
}

// Three mutants of the first full run were "killed" only by a scenario difference that had nothing to do with the
// mutated code (a timing flake under load), so they never had a test of their own; these are those tests.
func TestLaunchResultAndCadenceEdges(t *testing.T) {
	boot := func(t *testing.T) *rig {
		useEnv(t, "PI_TASKS", "off")
		useConfig(t, obj{})
		r := startRig(t, "")
		installSubagents(r)
		return r
	}
	t.Run("the launch result tells the model not to spawn more agents, word for word", func(t *testing.T) {
		r := boot(t)
		agentTask(r, "Run tests", "Run the test suite")
		eqv(t, r.must("TaskExecute", obj{"task_ids": arr{"1"}}),
			"Launched 1 agent(s):\n#1 → agent agent-1\nUse TaskOutput to check progress. Do not spawn additional agents for these tasks.")
	})
	t.Run("a completed agent that reports no result leaves the task without one", func(t *testing.T) {
		r := boot(t)
		agentTask(r, "Retried", "Desc")
		r.must("TaskExecute", obj{"task_ids": arr{"1"}})
		r.must("TaskUpdate", obj{"taskId": "1", "metadata": obj{"result": "earlier output"}})
		r.emit("subagents:completed", obj{"id": "agent-1"})
		text := r.must("TaskGet", obj{"taskId": "1"})
		eqv(t, contains(text, "Status: completed"), true)
		eqv(t, contains(text, "earlier output"), false)
	})
	t.Run("a task tool used right away still counts as the latest task tool use", func(t *testing.T) {
		r := boot(t)
		r.must("TaskCreate", obj{"subject": "Keep going", "description": "Desc"})
		r.must("TaskUpdate", obj{"taskId": "1", "status": "in_progress"})
		r.fireEvent("turn_start", obj{})
		r.must("TaskList", obj{}) // one turn after the last use: inside the gap that skips the store read
		r.fireEvent("turn_start", obj{})
		r.fireEvent("tool_result", obj{"toolName": "bash"})
		eqv(t, len(r.contextMessages()), 0)
	})
}

// Tool calls of one turn run concurrently in PiG and finish in no fixed order, so a scenario cannot compare ten creates
// of one turn (see PORT.md); what must hold is that none is lost or numbered twice.
func TestConcurrentCreatesAreAllKept(t *testing.T) {
	useEnv(t, "PI_TASKS", "off")
	useConfig(t, obj{})
	r := startRig(t, "")
	var wg sync.WaitGroup
	texts := make([]string, 10)
	for i := range texts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			raw, _ := r.Host.Tool("TaskCreate", obj{"subject": "Task " + itoa(i+1), "description": "d"})
			texts[i] = string(raw)
		}()
	}
	wg.Wait()
	list := r.must("TaskList", obj{})
	for i := 1; i <= 10; i++ {
		eqv(t, contains(list, "#"+itoa(i)+" "), true)
	}
	for i := 1; i <= 10; i++ {
		eqv(t, contains(list, "Task "+itoa(i)), true)
	}
}

func TestTaskExecuteRefusesAMissingBlocker(t *testing.T) {
	useEnv(t, "PI_TASKS", "off")
	useConfig(t, obj{})
	r := startRig(t, "")
	installSubagents(r)
	agentTask(r, "Needs the missing one", "Desc")
	r.must("TaskUpdate", obj{"taskId": "1", "addBlockedBy": arr{"99"}})
	eqv(t, contains(r.must("TaskExecute", obj{"task_ids": arr{"1"}}), "#1: blocked by #99"), true)
}
