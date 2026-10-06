package tintinweb_tasks_test

import (
	"path/filepath"
	"testing"
)

func TestTasksCommand(t *testing.T) {
	const f = "tasks-command"
	// runTasks boots the extension (tasks in a file named by PI_TASKS), seeds it and runs /tasks against a
	// scripted UI: a string is the literal answer, an int an index into the offered choices, nil (or running
	// out of script) a cancelled dialog.
	runTasks := func(t *testing.T, cfg obj, script []any, seed func(r *rig)) (*rig, string) {
		taskFile := filepath.Join(t.TempDir(), "tasks.json")
		useEnv(t, "PI_TASKS", taskFile)
		useConfig(t, cfg)
		r := startRig(t, "")
		if seed != nil {
			seed(r)
		}
		r.setScript(script...)
		r.runTasks()
		return r, taskFile
	}
	create := func(subject string) func(r *rig) {
		return func(r *rig) { r.must("TaskCreate", obj{"subject": subject, "description": "d"}) }
	}
	doneAndOpen := func(r *rig) {
		create("Done")(r)
		create("Open")(r)
		r.must("TaskUpdate", obj{"taskId": "1", "status": "completed"})
	}
	tw(t, f, "offers only view and create when the list is empty", func(t *testing.T) {
		r, _ := runTasks(t, obj{}, []any{nil}, nil)
		eqv(t, r.selects[0].Choices, []string{"View all tasks (0)", "Create task", "Settings"})
	})
	tw(t, f, "offers the clear actions with their counts once tasks exist", func(t *testing.T) {
		r, _ := runTasks(t, obj{}, []any{nil}, doneAndOpen)
		eqv(t, r.selects[0].Choices, []string{"View all tasks (2)", "Create task", "Clear completed (1)", "Clear all (2)", "Settings"})
	})
	tskip(t, f, "opens the settings panel and returns to the main menu afterwards",
		"the original counts ui.custom() calls of a SettingsList component; the Go SDK cannot run that TUI component, so the settings panel is a chain of selects (tested in settings_test.go)")
	tw(t, f, "starts a pending task", func(t *testing.T) {
		r, _ := runTasks(t, obj{}, []any{0, 0, "▸ Start (in_progress)"}, create("Work"))
		eqv(t, contains(r.must("TaskGet", obj{"taskId": "1"}), "Status: in_progress"), true)
	})
	tw(t, f, "completes an in-progress task", func(t *testing.T) {
		r, _ := runTasks(t, obj{}, []any{0, 0, "▸ Start (in_progress)", 0, "✓ Complete"}, create("Work"))
		eqv(t, contains(r.must("TaskGet", obj{"taskId": "1"}), "Status: completed"), true)
	})
	t.Run("a task completed from the menu is auto-cleared like any other (on_task_complete)", func(t *testing.T) {
		r, _ := runTasks(t, obj{"autoClearCompleted": "on_task_complete"}, []any{0, 0, "▸ Start (in_progress)", 0, "✓ Complete"}, create("Work"))
		eqv(t, contains(r.must("TaskGet", obj{"taskId": "1"}), "Status: completed"), true)
		for i := 0; i < 6; i++ {
			r.fireEvent("turn_start", obj{})
		}
		eqv(t, r.must("TaskList", obj{}), "No tasks found")
	})
	tw(t, f, "deletes a task", func(t *testing.T) {
		r, _ := runTasks(t, obj{}, []any{0, 0, "✗ Delete"}, create("Work"))
		eqv(t, r.must("TaskList", obj{}), "No tasks found")
	})
	tw(t, f, "offers Complete only for in-progress tasks", func(t *testing.T) {
		r, _ := runTasks(t, obj{}, []any{0, 0, nil}, create("Work"))
		eqv(t, r.selects[2].Choices, []string{"▸ Start (in_progress)", "✗ Delete", "← Back"})
	})
	tw(t, f, "acts on the task whose row was picked, not on an ID inside its subject", func(t *testing.T) {
		// The row reads "◻ #1 [pending] Fix #42 in the parser": the ID must come from the row's own marker.
		r, _ := runTasks(t, obj{}, []any{0, 0, "✗ Delete"}, create("Fix #42 in the parser"))
		eqv(t, r.must("TaskList", obj{}), "No tasks found")
	})
	tw(t, f, "acts on the picked row even when the status glyph contains an ID", func(t *testing.T) {
		// Nothing stops a hand-written glyph from looking like a task marker.
		r, _ := runTasks(t, obj{"glyphs": obj{"pending": "#12"}}, []any{0, 0, "✗ Delete"}, create("Work"))
		eqv(t, r.must("TaskList", obj{}), "No tasks found")
	})
	tw(t, f, "lists tasks with the default status glyphs", func(t *testing.T) {
		r, _ := runTasks(t, obj{}, []any{0, nil}, create("Work"))
		eqv(t, r.selects[1].Choices, []string{"◻ #1 [pending] Work", "← Back"})
	})
	tw(t, f, "lists tasks with the configured status glyphs", func(t *testing.T) {
		r, _ := runTasks(t, obj{"glyphs": obj{"pending": "[ ]"}}, []any{0, nil}, create("Work"))
		eqv(t, r.selects[1].Choices, []string{"[ ] #1 [pending] Work", "← Back"})
	})
	tw(t, f, "shows a placeholder screen when there is nothing to view", func(t *testing.T) {
		r, _ := runTasks(t, obj{}, []any{0, nil}, nil)
		eqv(t, r.selects[1], selectCall{"No tasks", []string{"← Back"}})
	})
	tw(t, f, "clears only completed tasks", func(t *testing.T) {
		r, _ := runTasks(t, obj{}, []any{"Clear completed (1)", nil}, doneAndOpen)
		list := r.must("TaskList", obj{})
		eqv(t, contains(list, "Open"), true)
		eqv(t, contains(list, "Done"), false)
	})
	tw(t, f, "clears every task and removes the now-empty session file", func(t *testing.T) {
		r, file := runTasks(t, obj{}, []any{"Clear all (2)", nil}, func(r *rig) { create("One")(r); create("Two")(r) })
		eqv(t, r.must("TaskList", obj{}), "No tasks found")
		eqv(t, exists(file), false)
	})
	tw(t, f, "keeps the file when clearing completed leaves work behind", func(t *testing.T) {
		_, file := runTasks(t, obj{}, []any{"Clear completed (1)", nil}, doneAndOpen)
		eqv(t, exists(file), true)
		eqv(t, storedSubjects(t, file), []string{"Open"})
	})
	tw(t, f, "creates a task from the subject and description prompts", func(t *testing.T) {
		r, _ := runTasks(t, obj{}, []any{"Create task", "Ship it", "Get the release out", nil}, nil)
		eqv(t, r.inputs, []string{"Task subject", "Task description"})
		task := r.must("TaskGet", obj{"taskId": "1"})
		eqv(t, contains(task, "Ship it"), true)
		eqv(t, contains(task, "Get the release out"), true)
	})
	tw(t, f, "creates nothing when the subject prompt is cancelled", func(t *testing.T) {
		r, _ := runTasks(t, obj{}, []any{"Create task", nil}, nil)
		eqv(t, r.inputs, []string{"Task subject"})
		eqv(t, r.must("TaskList", obj{}), "No tasks found")
	})
	tw(t, f, "creates nothing when the description prompt is cancelled", func(t *testing.T) {
		r, _ := runTasks(t, obj{}, []any{"Create task", "Ship it", nil}, nil)
		eqv(t, r.must("TaskList", obj{}), "No tasks found")
	})
}
