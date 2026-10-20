package pi_goal_x

// THROWAWAY: stand-ins for what the GoalCommands (drafting, widget glue) and GoalTools (completion) slices define with these exact
// names. The lead deletes this file when merging those branches.

type goalAuditMessage struct {
	customType, content string
	display             bool
	details             *jsObject
}

func (c *core) syncTerminalInputPause() {}
func (c *core) rehydrateDraft()         {}
func (c *core) hasActiveDraft() bool    { return false }
