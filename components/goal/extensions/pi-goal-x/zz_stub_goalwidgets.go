//go:build !goalwidgets_local

package pi_goal_x

// Temporary stubs for symbols owned by other wave-1 slices; removed at integration. Bodies panic.

// owned by GoalSettings (goal-settings.ts)
type goalDashboardKeybindings struct{ toggleExpand, scrollUp, scrollDown string }
type goalKeybindings struct{ dashboard goalDashboardKeybindings }

var defaultGoalKeybindings = goalKeybindings{dashboard: goalDashboardKeybindings{"ctrl+shift+t", "ctrl+shift+up", "ctrl+shift+down"}}

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

// owned by GoalDashModel (goal-dashboard-model.ts, goal-dashboard-renderer.ts, auditor-dashboard-model.ts, text-cache.ts)
type goalTheme interface {
	fg(color, text string) string
	bold(text string) string
}

type dashboardTaskNode struct {
	depth       int
	completedAt string
}
type taskProgress struct{ completed, total, percentage int }
type dashboardCurrentTask struct{ id, title string }
type goalDashboardModel struct {
	goalId       string
	taskProgress *taskProgress
	taskTree     []dashboardTaskNode
	currentTask  *dashboardCurrentTask
}
type goalDashboardModelOptions struct {
	maxAutonomousRuns  *float64
	showAutonomousRuns *bool
	focused            bool
	otherOpenGoals     int
	ledgerEvents       []*jsObject
	activityLimit      *int
	tasksDisabled      bool
}
type goalCompactOptions struct {
	footerHint   *string
	scrollOffset *int
	keybindings  *goalDashboardKeybindings
}
type goalExpandedOptions struct {
	scrollOffset *int
	rows         *int
	keybindings  *goalDashboardKeybindings
}
type auditCheck struct {
	id     string
	label  string
	state  string // pending | running | passed | failed
	detail string
}

type auditorDashboardModel struct {
	auditorLabel    string
	elapsedMs       float64
	percentage      *int
	checks          []auditCheck
	currentTool     string
	currentToolArgs string
	recentOutput    []string
	verdict         string
	active          bool
}

type auditResultCard struct {
	verdict string
	label   string
	lines   []string
}
type deriveAuditorDashboardOptions struct {
	auditorLabel string
	verdict      string
}

func deriveGoalDashboardModel(goal *jsObject, options goalDashboardModelOptions) *goalDashboardModel {
	panic("stub: owned by GoalDashModel")
}
func renderCompactDashboard(model *goalDashboardModel, theme goalTheme, width int, opts goalCompactOptions) []string {
	panic("stub: owned by GoalDashModel")
}
func renderExpandedDashboard(model *goalDashboardModel, theme goalTheme, width int, opts goalExpandedOptions) []string {
	panic("stub: owned by GoalDashModel")
}
func renderUnfocusedDashboard(openGoalCount int, theme goalTheme, width int) []string {
	panic("stub: owned by GoalDashModel")
}
func renderAuditorDashboard(model *auditorDashboardModel, theme goalTheme, width int, showToolDetails bool) []string {
	panic("stub: owned by GoalDashModel")
}
func renderAuditResultCard(card auditResultCard, theme goalTheme, width int) []string {
	panic("stub: owned by GoalDashModel")
}
func clampLinesToWidth(lines []string, width int) []string { panic("stub: owned by GoalDashModel") }
func deriveAuditorDashboardModel(progress auditorProgress, options deriveAuditorDashboardOptions) *auditorDashboardModel {
	panic("stub: owned by GoalDashModel")
}
func deriveAuditResultCard(verdict, report string) auditResultCard {
	panic("stub: owned by GoalDashModel")
}
func expandedTaskViewportRows(width int) int { panic("stub: owned by GoalDashModel") }
func compactTaskViewportRows(width int) int  { panic("stub: owned by GoalDashModel") }
func taskViewportPageSize(rows float64) int  { panic("stub: owned by GoalDashModel") }
func anchoredScrollOffset(nodes []dashboardTaskNode, rows int) int {
	panic("stub: owned by GoalDashModel")
}
func clampScrollOffset(offset float64, totalRows, rows int) int {
	panic("stub: owned by GoalDashModel")
}
func maxScrollOffset(totalRows, rows int) int                { panic("stub: owned by GoalDashModel") }
func latestCompletedNodeIndex(nodes []dashboardTaskNode) int { panic("stub: owned by GoalDashModel") }
func truncateToWidth(text string, width int, ellipsis string, pad bool) string {
	panic("stub: owned by GoalDashModel")
}
func wrapTextWithAnsi(text string, width int) []string { panic("stub: owned by GoalDashModel") }
func visibleWidth(s string) int                        { panic("stub: owned by GoalDashModel") }

// owned by GoalTasksPolicy (goal-task-count.ts; countTaskSubtree already exists in format.go)
func countAllTasks(tasks []any) int { panic("stub: owned by GoalTasksPolicy") }

// owned by GoalLedger (goal-activity.ts, goal-ledger.ts)
func latestAuditorResultForGoal(events []*jsObject, goalID string) *jsObject {
	panic("stub: owned by GoalLedger")
}
func latestEventsForGoal(events []*jsObject, goalID string, limit int) []*jsObject {
	panic("stub: owned by GoalLedger")
}

// owned by GoalTasksPolicy (goal-policy.ts)
func countOrderedSteps(objective string) int { panic("stub: owned by GoalTasksPolicy") }
func sisyphusStepProgress(goal *jsObject) (current, total int, ok bool) {
	panic("stub: owned by GoalTasksPolicy")
}
