package tintinweb_tasks

import "testing"

func fixedMode(m string) func() string { return func() string { return m } }

func completeAll(s *taskStore, m *autoClearManager, turn int) {
	for _, tk := range s.list(nil) {
		s.update(tk.ID, updateFields{Status: sp("completed")})
		m.trackCompletion(tk.ID, turn)
	}
}

func TestAutoClear(t *testing.T) {
	const f = "auto-clear"
	has := func(s *taskStore, id string) bool { return s.get(id) != nil }
	taskComplete := func() (*taskStore, *autoClearManager) {
		s := newTaskStore("")
		return s, newAutoClearManager(func() *taskStore { return s }, fixedMode("on_task_complete"), 4)
	}
	listComplete := func() (*taskStore, *autoClearManager) {
		s := newTaskStore("")
		return s, newAutoClearManager(func() *taskStore { return s }, fixedMode("on_list_complete"), 4)
	}
	tw(t, f, "does not clear completed task before REMINDER_INTERVAL turns", func(t *testing.T) {
		s, m := taskComplete()
		s.create("Task", "Desc", "", nil)
		s.update("1", updateFields{Status: sp("completed")})
		m.trackCompletion("1", 1)
		for turn := 2; turn <= 4; turn++ { // turns 2, 3, 4: not enough
			m.onTurnStart(turn)
		}
		eq(t, has(s, "1"), true)
		eq(t, s.get("1").Status, "completed")
	})
	tw(t, f, "clears completed task after REMINDER_INTERVAL turns", func(t *testing.T) {
		s, m := taskComplete()
		s.create("Task", "Desc", "", nil)
		s.update("1", updateFields{Status: sp("completed")})
		m.trackCompletion("1", 1)
		m.onTurnStart(5) // turn 5 = turn 1 + 4 (REMINDER_INTERVAL)
		eq(t, has(s, "1"), false)
		eq(t, len(s.list(nil)), 0)
	})
	tw(t, f, "clears each task independently based on its own completion turn", func(t *testing.T) {
		s, m := taskComplete()
		s.create("Task A", "Desc", "", nil)
		s.create("Task B", "Desc", "", nil)
		s.update("1", updateFields{Status: sp("completed")})
		m.trackCompletion("1", 1)
		s.update("2", updateFields{Status: sp("completed")})
		m.trackCompletion("2", 3)
		m.onTurnStart(5) // task A expires (1+4), task B still lingers (3+4=7)
		eq(t, has(s, "1"), false)
		eq(t, has(s, "2"), true)
		m.onTurnStart(7) // task B expires
		eq(t, has(s, "2"), false)
	})
	tw(t, f, "does not clear pending or in_progress tasks", func(t *testing.T) {
		s, m := taskComplete()
		s.create("Pending", "Desc", "", nil)
		s.create("In Progress", "Desc", "", nil)
		s.create("Completed", "Desc", "", nil)
		s.update("2", updateFields{Status: sp("in_progress")})
		s.update("3", updateFields{Status: sp("completed")})
		m.trackCompletion("3", 1)
		m.onTurnStart(5)
		eq(t, has(s, "1"), true)
		eq(t, has(s, "2"), true)
		eq(t, has(s, "3"), false)
	})
	tw(t, f, "cleans up dependency edges when auto-clearing", func(t *testing.T) {
		s, m := taskComplete()
		s.create("Blocker", "Desc", "", nil)
		s.create("Blocked", "Desc", "", nil)
		s.update("1", updateFields{AddBlocks: []string{"2"}})
		s.update("1", updateFields{Status: sp("completed")})
		m.trackCompletion("1", 1)
		m.onTurnStart(5)
		eq(t, has(s, "1"), false)
		eq(t, s.get("2").BlockedBy, []string{})
	})
	// The title is shared by the on_task_complete and on_list_complete describes of the original: one twin each.
	returnsTrue := func(t *testing.T, mk func() (*taskStore, *autoClearManager)) {
		s, m := mk()
		s.create("Task", "Desc", "", nil)
		s.update("1", updateFields{Status: sp("completed")})
		m.trackCompletion("1", 1)
		eq(t, m.onTurnStart(4), false)
		eq(t, m.onTurnStart(5), true)
	}
	tw(t, f, "returns true when tasks are cleared", func(t *testing.T) { returnsTrue(t, taskComplete) })
	tw(t, f, "does not clear when some tasks are still pending", func(t *testing.T) {
		s, m := listComplete()
		s.create("Done", "Desc", "", nil)
		s.create("Pending", "Desc", "", nil)
		s.update("1", updateFields{Status: sp("completed")})
		m.trackCompletion("1", 1)
		for turn := 2; turn <= 10; turn++ {
			m.onTurnStart(turn)
		}
		eq(t, has(s, "1"), true)
		eq(t, len(s.list(nil)), 2)
	})
	tw(t, f, "does not clear immediately when all tasks complete", func(t *testing.T) {
		s, m := listComplete()
		s.create("A", "Desc", "", nil)
		s.create("B", "Desc", "", nil)
		s.update("1", updateFields{Status: sp("completed")})
		s.update("2", updateFields{Status: sp("completed")})
		m.trackCompletion("2", 1)
		for turn := 2; turn <= 4; turn++ {
			m.onTurnStart(turn)
		}
		eq(t, len(s.list(nil)), 2)
	})
	tw(t, f, "clears all completed tasks after REMINDER_INTERVAL turns when all are completed", func(t *testing.T) {
		s, m := listComplete()
		s.create("A", "Desc", "", nil)
		s.create("B", "Desc", "", nil)
		s.update("1", updateFields{Status: sp("completed")})
		s.update("2", updateFields{Status: sp("completed")})
		m.trackCompletion("2", 1)
		m.onTurnStart(5)
		eq(t, len(s.list(nil)), 0)
	})
	tw(t, f, "returns true when tasks are cleared", func(t *testing.T) { returnsTrue(t, listComplete) })
	tw(t, f, "resets countdown when a new task is created before REMINDER_INTERVAL", func(t *testing.T) {
		s, m := listComplete()
		s.create("A", "Desc", "", nil)
		s.update("1", updateFields{Status: sp("completed")})
		m.trackCompletion("1", 1)
		m.onTurnStart(3) // a new task is created: reset the countdown
		m.resetBatchCountdown()
		s.create("B", "Desc", "", nil)
		m.onTurnStart(5) // turn 5 would have cleared, but the countdown was reset at turn 3
		eq(t, has(s, "1"), true)
	})
	tw(t, f, "resets countdown when a task goes back to in_progress", func(t *testing.T) {
		s, m := listComplete()
		s.create("A", "Desc", "", nil)
		s.create("B", "Desc", "", nil)
		s.update("1", updateFields{Status: sp("completed")})
		s.update("2", updateFields{Status: sp("completed")})
		m.trackCompletion("2", 1)
		m.onTurnStart(3)
		s.update("2", updateFields{Status: sp("in_progress")})
		m.resetBatchCountdown()
		m.onTurnStart(5)
		eq(t, len(s.list(nil)), 2)
	})
	tw(t, f, "never clears completed tasks regardless of turns", func(t *testing.T) {
		s := newTaskStore("")
		m := newAutoClearManager(func() *taskStore { return s }, fixedMode("never"), 4)
		s.create("A", "Desc", "", nil)
		s.create("B", "Desc", "", nil)
		s.update("1", updateFields{Status: sp("completed")})
		s.update("2", updateFields{Status: sp("completed")})
		m.trackCompletion("1", 1)
		m.trackCompletion("2", 1)
		for turn := 2; turn <= 20; turn++ {
			m.onTurnStart(turn)
		}
		eq(t, len(s.list(nil)), 2)
	})
	tw(t, f, "trackCompletion is a no-op", func(t *testing.T) {
		s := newTaskStore("")
		m := newAutoClearManager(func() *taskStore { return s }, fixedMode("never"), 4)
		s.create("Task", "Desc", "", nil)
		s.update("1", updateFields{Status: sp("completed")})
		m.trackCompletion("1", 1)
		m.onTurnStart(100)
		eq(t, has(s, "1"), true)
	})
	tw(t, f, "respects mode changes via getMode callback", func(t *testing.T) {
		s := newTaskStore("")
		mode := "never"
		m := newAutoClearManager(func() *taskStore { return s }, func() string { return mode }, 4)
		s.create("Task", "Desc", "", nil)
		s.update("1", updateFields{Status: sp("completed")})
		m.trackCompletion("1", 1) // in never mode: a no-op
		m.onTurnStart(5)
		eq(t, has(s, "1"), true)
		mode = "on_task_complete" // switch and re-track
		m.trackCompletion("1", 5)
		m.onTurnStart(9)
		eq(t, has(s, "1"), false)
	})
	tw(t, f, "operates on the current store after swap", func(t *testing.T) {
		s := newTaskStore("")
		m := newAutoClearManager(func() *taskStore { return s }, fixedMode("on_task_complete"), 4)
		s.create("Old task", "Desc", "", nil)
		s.update("1", updateFields{Status: sp("completed")})
		m.trackCompletion("1", 1)
		s = newTaskStore("") // simulate a session switch: swap the store
		s.create("New task", "Desc", "", nil)
		m.reset()
		m.onTurnStart(5) // old task tracking was reset, the new store has no completed tasks
		eq(t, len(s.list(nil)), 1)
		eq(t, s.get("1").Subject, "New task")
	})
	tw(t, f, "clears from new store, not old store", func(t *testing.T) {
		s := newTaskStore("")
		m := newAutoClearManager(func() *taskStore { return s }, fixedMode("on_task_complete"), 4)
		s = newTaskStore("") // swap to a new store with a completed task
		s.create("Task in new store", "Desc", "", nil)
		s.update("1", updateFields{Status: sp("completed")})
		m.trackCompletion("1", 1)
		m.onTurnStart(5)
		eq(t, has(s, "1"), false)
	})
	tw(t, f, "reset clears per-task tracking so old completions don't fire", func(t *testing.T) {
		s, m := taskComplete()
		s.create("Task", "Desc", "", nil)
		s.update("1", updateFields{Status: sp("completed")})
		m.trackCompletion("1", 1)
		m.reset() // simulate /new before the delay expires
		m.onTurnStart(5)
		eq(t, has(s, "1"), true)
	})
	tw(t, f, "reset clears batch countdown so old all-completed state doesn't fire", func(t *testing.T) {
		s, m := listComplete()
		s.create("Task", "Desc", "", nil)
		s.update("1", updateFields{Status: sp("completed")})
		m.trackCompletion("1", 1)
		m.reset()
		m.onTurnStart(5)
		eq(t, has(s, "1"), true)
	})
	tw(t, f, "tracking works normally after reset", func(t *testing.T) {
		s, m := taskComplete()
		s.create("Task", "Desc", "", nil)
		s.update("1", updateFields{Status: sp("completed")})
		m.trackCompletion("1", 1)
		m.reset()
		m.trackCompletion("1", 10) // re-track after reset with a new turn baseline
		m.onTurnStart(14)
		eq(t, has(s, "1"), false)
	})
	tw(t, f, "retires a finished list before the new tasks land (${mode})", func(t *testing.T) {
		for _, mode := range []string{"on_list_complete", "on_task_complete"} {
			t.Run(mode, func(t *testing.T) {
				s := newTaskStore("")
				m := newAutoClearManager(func() *taskStore { return s }, fixedMode(mode), 4)
				s.create("A", "Desc", "", nil)
				s.create("B", "Desc", "", nil)
				completeAll(s, m, 1)
				// The run ends here: nowhere near the delay either mode would need.
				m.onRunEnded()
				m.startNewBatch()
				eq(t, len(s.list(nil)), 0)
				eq(t, s.create("C", "Desc", "", nil).ID, "3") // IDs are not reused: the new task is #3
			})
		}
	})
	tw(t, f, "keeps a list the agent is still building in the same run", func(t *testing.T) {
		s, m := listComplete()
		m.startNewBatch()
		s.create("Step one", "Desc", "", nil)
		completeAll(s, m, 1)
		m.startNewBatch()
		s.create("Step two", "Desc", "", nil)
		eq(t, subjects(s.list(nil)), []string{"Step one", "Step two"})
	})
	tw(t, f, "keeps the list in never mode", func(t *testing.T) {
		s := newTaskStore("")
		m := newAutoClearManager(func() *taskStore { return s }, fixedMode("never"), 4)
		s.create("A", "Desc", "", nil)
		completeAll(s, m, 1)
		m.onRunEnded()
		m.startNewBatch()
		eq(t, len(s.list(nil)), 1)
	})
	tw(t, f, "leaves a list with unfinished work alone", func(t *testing.T) {
		s, m := listComplete()
		s.create("Done", "Desc", "", nil)
		s.create("Still going", "Desc", "", nil)
		s.update("1", updateFields{Status: sp("completed")})
		m.trackCompletion("1", 1)
		s.update("2", updateFields{Status: sp("in_progress")})
		m.onRunEnded()
		m.startNewBatch()
		eq(t, len(s.list(nil)), 2)
	})
	tw(t, f, "does nothing on an empty store", func(t *testing.T) {
		s, m := listComplete()
		m.onRunEnded()
		m.startNewBatch()
		eq(t, len(s.list(nil)), 0)
	})
	tw(t, f, "clears a list a subagent finished after its run ended", func(t *testing.T) {
		s, m := listComplete()
		s.create("Cascaded", "Desc", "", nil)
		s.update("1", updateFields{Status: sp("in_progress")})
		m.onRunEnded()
		// The completion lands late, outside any turn: nothing ticks the countdown after it.
		s.update("1", updateFields{Status: sp("completed")})
		m.trackCompletion("1", 1)
		m.startNewBatch()
		eq(t, len(s.list(nil)), 0)
	})
	tw(t, f, "arms once per run, so the batch it starts is not swept mid-build", func(t *testing.T) {
		s, m := listComplete()
		s.create("Old", "Desc", "", nil)
		completeAll(s, m, 1)
		m.onRunEnded()
		m.startNewBatch()
		s.create("First of the new batch", "Desc", "", nil)
		completeAll(s, m, 3)
		// Still the same run: the next task joins that batch rather than replacing it.
		m.startNewBatch()
		s.create("Second of the new batch", "Desc", "", nil)
		eq(t, len(s.list(nil)), 2)
	})
	tw(t, f, "does not cut the new batch's own countdown short", func(t *testing.T) {
		s, m := listComplete()
		s.create("Old", "Desc", "", nil)
		completeAll(s, m, 1)
		m.onRunEnded()
		m.startNewBatch()
		s.create("New", "Desc", "", nil)
		completeAll(s, m, 3)
		eq(t, m.onTurnStart(4), false) // 3 + 4 not reached
		eq(t, len(s.list(nil)), 1)
		eq(t, m.onTurnStart(7), true)
	})
	tw(t, f, "reset drops the armed boundary", func(t *testing.T) {
		s, m := listComplete()
		s.create("A", "Desc", "", nil)
		completeAll(s, m, 1)
		m.onRunEnded()
		m.reset() // /new: nothing may carry over into the next session
		m.startNewBatch()
		eq(t, len(s.list(nil)), 1)
	})
}
