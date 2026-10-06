package tintinweb_tasks

import "testing"

var cadenceTaskTools = map[string]bool{"TaskCreate": true, "TaskList": true, "TaskGet": true, "TaskUpdate": true,
	"TaskOutput": true, "TaskStop": true, "TaskExecute": true}

func TestReminderCadence(t *testing.T) {
	const f = "reminder-cadence"
	cfg := cadenceConfig{ReminderInterval: 4, TaskToolNames: cadenceTaskTools}
	setup := func() (*cadenceState, func(n int)) {
		s := createCadenceState()
		return s, func(n int) {
			for i := 0; i < n; i++ {
				s.onTurnStart()
			}
		}
	}
	tw(t, f, "starts with reminder not due", func(t *testing.T) {
		s, _ := setup()
		eq(t, s.ReminderDue, false)
		eq(t, drainReminderForContext(s), false)
	})
	tw(t, f, "marks reminder due after REMINDER_INTERVAL non-task turns when tasks exist", func(t *testing.T) {
		s, advance := setup()
		advance(5) // currentTurn = 5, lastTaskToolUseTurn = 0
		eq(t, evaluateToolResult(s, "read", true, cfg), true)
		eq(t, s.ReminderDue, true)
	})
	tw(t, f, "does NOT mark reminder due when no tasks exist", func(t *testing.T) {
		s, advance := setup()
		advance(10)
		eq(t, evaluateToolResult(s, "read", false, cfg), false)
		eq(t, s.ReminderDue, false)
	})
	tw(t, f, "does NOT mark reminder due before the interval elapses", func(t *testing.T) {
		s, advance := setup()
		advance(2)
		eq(t, evaluateToolResult(s, "read", true, cfg), false)
		eq(t, s.ReminderDue, false)
	})
	tw(t, f, "task tool usage resets cadence and clears any pending reminder", func(t *testing.T) {
		s, advance := setup()
		advance(5)
		evaluateToolResult(s, "read", true, cfg) // queues reminder
		eq(t, s.ReminderDue, true)
		eq(t, evaluateToolResult(s, "TaskCreate", true, cfg), false)
		eq(t, s.ReminderDue, false)
		eq(t, s.ReminderInjectedThisCycle, false)
		eq(t, s.LastTaskToolUseTurn, s.CurrentTurn)
	})
	tw(t, f, "does not re-fire within the same injection cycle", func(t *testing.T) {
		s, advance := setup()
		advance(5)
		evaluateToolResult(s, "read", true, cfg)
		eq(t, drainReminderForContext(s), true)
		// Reminder injected this cycle: further non-task tool results do not re-queue it
		// until a task tool usage resets cadence.
		advance(10)
		eq(t, evaluateToolResult(s, "bash", true, cfg), false)
		eq(t, s.ReminderDue, false)
	})
	tw(t, f, "re-arms after a task tool usage resets the cycle", func(t *testing.T) {
		s, advance := setup()
		advance(5)
		evaluateToolResult(s, "read", true, cfg)
		drainReminderForContext(s)
		evaluateToolResult(s, "TaskUpdate", true, cfg) // use a task tool to reset
		eq(t, s.ReminderInjectedThisCycle, false)
		advance(5) // wait the interval again, expect a fresh reminder
		eq(t, evaluateToolResult(s, "grep", true, cfg), true)
	})
	tw(t, f, "drainReminderForContext is a one-shot (only fires once per cycle)", func(t *testing.T) {
		s, advance := setup()
		advance(5)
		evaluateToolResult(s, "read", true, cfg)
		eq(t, drainReminderForContext(s), true)
		eq(t, drainReminderForContext(s), false)
	})
	tw(t, f, "resetCadenceState wipes everything", func(t *testing.T) {
		s, advance := setup()
		advance(20)
		evaluateToolResult(s, "read", true, cfg)
		drainReminderForContext(s)
		resetCadenceState(s)
		eq(t, *s, cadenceState{})
	})
}
