//go:build !dashtmp

package pi_goal_x

// Temporary stubs for symbols owned by other wave-1 slices; removed at integration.

// owned by GoalLedger (goal-activity.ts, goal-ledger.ts, goal-session-health.ts)
type goalActivityItem struct{ at, kind, text, marker string }

type deriveGoalActivityOptions struct {
	taskTitles map[string]string
	limit      *int
}

func deriveGoalActivity(events []*jsObject, goalID string, o deriveGoalActivityOptions) []goalActivityItem {
	panic("stub: owned by GoalLedger")
}

func latestAuditorResultForGoal(events []*jsObject, goalID string) *jsObject {
	panic("stub: owned by GoalLedger")
}

func latestEventsForGoal(events []*jsObject, goalID string, limit int) []*jsObject {
	panic("stub: owned by GoalLedger")
}

type checkpointHealth struct{ total, legacyFull, v2Minimal, totalContentChars, recoverableChars, largestCheckpointChars int }

func formatCheckpointHealthReport(h *checkpointHealth, sessionFile *string) string {
	panic("stub: owned by GoalLedger")
}

// owned by GoalService (goal-scheduler-state.ts)
func schedulerSummary(s *jsObject, limit *float64, showRuns bool) string {
	panic("stub: owned by GoalService")
}

// owned by GoalSettings (goal-settings.ts)
type goalDashboardKeybindings struct{ toggleExpand, scrollUp, scrollDown string }

type goalKeybindings struct{ dashboard goalDashboardKeybindings }

var defaultGoalKeybindings = goalKeybindings{dashboard: goalDashboardKeybindings{"ctrl+shift+t", "ctrl+shift+up", "ctrl+shift+down"}}

func formatGoalKeybinding(key string) string { panic("stub: owned by GoalSettings") }

// owned by GoalAuditor (goal-auditor.ts)
type auditorProgress struct {
	currentTool, currentToolArgs string
	currentToolStartedAt         *float64
	recentOutput                 []string
	phase                        string
	elapsedMs                    float64
	label                        string
	percentage                   *float64
}
