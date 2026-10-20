package pi_goal_x

// Temporary: the rest of goal-format.ts is being ported (GoalFormat slice); these are what the merged slices need now.

const (
	goalEventEntry = "pi-goal-event"
	goalAuditEntry = "pi-goal-audit-event"
)

func goalEventMessageId(message any) (string, bool) { panic("stub: owned by GoalFormat") }

// dialogInnerWidth, borderedLine, horizontalRule: owned by GoalWidgets (widgets/dialog-scaffold.ts), removed when it merges.
func dialogInnerWidth(termWidth, maxInner int) int { panic("stub: owned by GoalWidgets") }
func borderedLine(accent func(string) string, innerWidth int, content string) string {
	panic("stub: owned by GoalWidgets")
}
func horizontalRule(innerWidth int) string { panic("stub: owned by GoalWidgets") }
