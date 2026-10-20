package pi_goal_x

// Temporary: the rest of goal-format.ts is being ported (GoalFormat slice); these are what the merged slices need now.

const (
	goalEventEntry = "pi-goal-event"
	goalAuditEntry = "pi-goal-audit-event"
)

func goalEventMessageId(message any) (string, bool) { panic("stub: owned by GoalFormat") }
