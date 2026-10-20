package pi_goal_x

import "fmt"

// Stubs for symbols other slices own (goal-format.ts / prompts/goal-prompts.ts). They panic until integration.

const (
	goalEventEntry = "pi-goal-event"
	goalAuditEntry = "pi-goal-audit-event"
)

// goalEventMessageId: goal-format.ts, owned by another slice.
func goalEventMessageId(message any) (string, bool) { panic("stub: owned by goal-format") }

// checkpointTriggerPrompt: prompts/goal-prompts.ts, owned by GoalPrompts.
func checkpointTriggerPrompt(goalID string) string {
	panic(fmt.Sprint("stub: owned by GoalPrompts ", goalID))
}
