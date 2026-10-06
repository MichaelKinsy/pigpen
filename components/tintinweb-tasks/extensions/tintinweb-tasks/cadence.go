package tintinweb_tasks

// Pure cadence logic for the system-reminder injection. upstream: reminder-cadence.ts.

type cadenceState struct {
	CurrentTurn               int
	LastTaskToolUseTurn       int
	ReminderInjectedThisCycle bool
	ReminderDue               bool
}

type cadenceConfig struct {
	// ReminderInterval is the number of turns without a task-tool call before a reminder is due.
	ReminderInterval int
	// TaskToolNames counts as task tool usage and resets the cadence.
	TaskToolNames map[string]bool
}

func createCadenceState() *cadenceState { return &cadenceState{} }

func resetCadenceState(s *cadenceState) { *s = cadenceState{} }

// onTurnStart increments the turn counter at `turn_start`.
func (s *cadenceState) onTurnStart() { s.CurrentTurn++ }

// evaluateToolResult decides what a tool_result implies and reports whether a reminder should be queued for
// the next LLM call. A task tool resets the cadence and clears a pending reminder. upstream: reminder-cadence.ts:54-77.
func evaluateToolResult(s *cadenceState, toolName string, hasTasks bool, c cadenceConfig) bool {
	if c.TaskToolNames[toolName] {
		s.LastTaskToolUseTurn = s.CurrentTurn
		s.ReminderInjectedThisCycle = false
		s.ReminderDue = false
		return false
	}
	if s.CurrentTurn-s.LastTaskToolUseTurn < c.ReminderInterval {
		return false
	}
	if s.ReminderInjectedThisCycle {
		return false
	}
	if !hasTasks {
		return false
	}
	s.ReminderDue = true
	return true
}

// drainReminderForContext is called when `context` fires: it reports whether the reminder is to be injected
// into the upcoming LLM call, once per cycle.
func drainReminderForContext(s *cadenceState) bool {
	if !s.ReminderDue {
		return false
	}
	s.ReminderDue = false
	s.ReminderInjectedThisCycle = true
	s.LastTaskToolUseTurn = s.CurrentTurn
	return true
}
