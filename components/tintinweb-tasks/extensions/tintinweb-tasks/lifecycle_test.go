package tintinweb_tasks_test

import (
	"path/filepath"
	"regexp"
	"testing"
)

var taskIDRe = regexp.MustCompile(`#(\d+)`)

func TestAutoClearLifecycle(t *testing.T) {
	const f = "auto-clear-lifecycle"
	boot := func(t *testing.T, cfg obj) (*rig, string) {
		useConfig(t, cfg)
		useEnv(t, "PI_TASKS", "")
		cwd := t.TempDir()
		r := startRig(t, cwd)
		r.fireEvent("session_start", obj{"reason": "startup"})
		return r, filepath.Join(cwd, ".pi", "tasks", "tasks-s1.json")
	}
	// runAndFinish is one run: a turn, `count` tasks created and completed, then the agent stops.
	runAndFinish := func(r *rig, count int) {
		r.fireEvent("turn_start", obj{})
		var ids []string
		for i := 0; i < count; i++ {
			text := r.must("TaskCreate", obj{"subject": "Task " + itoa(i), "description": "d"})
			ids = append(ids, taskIDRe.FindStringSubmatch(text)[1])
		}
		for _, id := range ids {
			r.must("TaskUpdate", obj{"taskId": id, "status": "completed"})
		}
		r.fireEvent("agent_settled", obj{})
	}
	tw(t, f, "keeps the finished list visible after the run that produced it", func(t *testing.T) {
		r, _ := boot(t, obj{})
		runAndFinish(r, 2)
		// The agent has stopped and the user is reading what was just completed.
		eqv(t, contains(r.must("TaskList", obj{}), "Task 0"), true)
	})
	tw(t, f, "keeps it through a follow-up that creates no work", func(t *testing.T) {
		r, _ := boot(t, obj{})
		runAndFinish(r, 2)
		r.fireEvent("turn_start", obj{}) // "what did you just do?"
		eqv(t, contains(r.must("TaskList", obj{}), "Task 0"), true)
	})
	tw(t, f, "starts the next batch clean instead of appending to the finished one", func(t *testing.T) {
		r, _ := boot(t, obj{})
		runAndFinish(r, 2)
		r.fireEvent("turn_start", obj{})
		created := r.must("TaskCreate", obj{"subject": "Fresh work", "description": "d"})
		list := r.must("TaskList", obj{})
		eqv(t, contains(list, "Fresh work"), true)
		eqv(t, contains(list, "Task 0"), false)
		eqv(t, contains(list, "Task 1"), false)
		// IDs stay monotonic: a shared list must never reuse one.
		eqv(t, contains(created, "#3"), true)
	})
	tw(t, f, "does the same after the session is resumed", func(t *testing.T) {
		r, _ := boot(t, obj{})
		runAndFinish(r, 1)
		// Resume shows the completed list for review...
		r.fireEvent("session_start", obj{"reason": "resume"})
		eqv(t, contains(r.must("TaskList", obj{}), "Task 0"), true)
		// ...and it still does not collect the next batch.
		r.fireEvent("turn_start", obj{})
		r.must("TaskCreate", obj{"subject": "Fresh work", "description": "d"})
		list := r.must("TaskList", obj{})
		eqv(t, contains(list, "Fresh work"), true)
		eqv(t, contains(list, "Task 0"), false)
	})
	tw(t, f, "keeps every step of a list the agent builds one task at a time", func(t *testing.T) {
		// An agent that creates a task, finishes it, then thinks of the next one is building a single list.
		r, _ := boot(t, obj{})
		for i, subject := range []string{"Step one", "Step two", "Step three"} {
			r.fireEvent("turn_start", obj{})
			r.must("TaskCreate", obj{"subject": subject, "description": "d"})
			if i < 2 {
				r.must("TaskUpdate", obj{"taskId": itoa(i + 1), "status": "completed"})
			}
		}
		list := r.must("TaskList", obj{})
		for _, s := range []string{"Step one", "Step two", "Step three"} {
			eqv(t, contains(list, s), true)
		}
	})
	tw(t, f, "leaves unfinished work in place across the run boundary", func(t *testing.T) {
		r, _ := boot(t, obj{})
		r.fireEvent("turn_start", obj{})
		r.must("TaskCreate", obj{"subject": "Done", "description": "d"})
		r.must("TaskCreate", obj{"subject": "Unfinished", "description": "d"})
		r.must("TaskUpdate", obj{"taskId": "1", "status": "completed"})
		// The run ends with work still open, so the boundary is armed, but a list that is not finished is
		// not a batch to retire.
		r.fireEvent("agent_settled", obj{})
		r.fireEvent("turn_start", obj{})
		r.must("TaskCreate", obj{"subject": "More", "description": "d"})
		list := r.must("TaskList", obj{})
		eqv(t, contains(list, "Unfinished"), true)
		eqv(t, contains(list, "Done"), true)
	})
	tw(t, f, "keeps the list when auto-clear is off", func(t *testing.T) {
		r, _ := boot(t, obj{"autoClearCompleted": "never"})
		runAndFinish(r, 1)
		r.fireEvent("turn_start", obj{})
		r.must("TaskCreate", obj{"subject": "Fresh work", "description": "d"})
		eqv(t, contains(r.must("TaskList", obj{}), "Task 0"), true)
	})
	tw(t, f, "removes the emptied session file when the countdown clears the list", func(t *testing.T) {
		r, file := boot(t, obj{})
		runAndFinish(r, 1)
		eqv(t, exists(file), true)
		// The conversation continues without new tasks, so the turn countdown expires.
		for i := 0; i < 5; i++ {
			r.fireEvent("turn_start", obj{})
		}
		eqv(t, exists(file), false)
		// Only the file goes. `.pi/tasks/` is left standing, as every release so far has left it.
		eqv(t, exists(filepath.Dir(file)), true)
	})
}
