package pi_goal_x

import (
	"math"
	"strings"
)

// ported from widgets/goal-dashboard-model.ts: the shared dashboard view model, pure data derivation (no TUI).
// Optional TS fields are "" / nil here. The TS taskIndex memo cache is not ported (it is not observable); the tree is
// walked directly.

type dashboardStatusCode = string

const (
	dashRunning       dashboardStatusCode = "running"
	dashIdle          dashboardStatusCode = "idle"
	dashPaused        dashboardStatusCode = "paused"
	dashBlocked       dashboardStatusCode = "blocked"
	dashBudgetLimited dashboardStatusCode = "budget_limited"
	dashComplete      dashboardStatusCode = "complete"
)

type dashboardGoalStatus struct {
	code        dashboardStatusCode
	label       string
	footerLabel string
	reason      string
	// suggestedAction is "" when absent.
	suggestedAction string
}

type dashboardTaskNode struct {
	id                   string
	title                string
	status               string
	depth                int
	isCurrent            bool
	verificationContract string
	evidence             string
	completedAt          string
	totalSubtasks        int
	completedSubtasks    int
}

type taskProgress struct{ completed, total, percentage int }

type currentTaskSubtaskProgress struct {
	completedSubtasks int
	totalSubtasks     int
	subtaskPercentage int
}

type dashboardCurrentTask struct {
	currentTaskSubtaskProgress
	id                   string
	title                string
	depth                int
	verificationContract string
	evidence             string
	inferred             bool
}

type dashboardBudget struct {
	used, total float64
	percentage  int
	remaining   float64
}

type dashboardUsage struct {
	activeSeconds float64
	tokens        float64
	footerBits    string
}

type goalDashboardModel struct {
	scheduling               []string
	goalId                   string
	title                    string
	status                   dashboardGoalStatus
	focused                  bool
	auditorEnabled           bool
	filePath                 string
	usage                    dashboardUsage
	budget                   *dashboardBudget
	taskProgress             *taskProgress
	taskTree                 []dashboardTaskNode
	currentTask              *dashboardCurrentTask
	goalVerificationContract string
	otherOpenGoals           int
	recentActivity           []goalActivityItem
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

// deriveGoalStatus maps the persisted lifecycle status to a display code.
func deriveGoalStatus(goal *jsObject) dashboardGoalStatus {
	switch gstr(goal, "status") {
	case "active":
		if gflag(goal, "autoContinue") {
			return dashboardGoalStatus{code: dashRunning, label: "In progress", footerLabel: statusLabel(goal)}
		}
		return dashboardGoalStatus{code: dashIdle, label: "Idle", footerLabel: statusLabel(goal)}
	case "paused":
		who := ""
		switch gstr(goal, "stopReason") {
		case "agent":
			who = " (agent)"
		case "user":
			who = " (user)"
		}
		st := dashboardGoalStatus{code: dashPaused, label: "Paused" + who, footerLabel: statusLabel(goal)}
		st.reason = gstr(goal, "pauseReason")
		st.suggestedAction = gstr(goal, "pauseSuggestedAction")
		return st
	case "blocked":
		st := dashboardGoalStatus{code: dashBlocked, label: "Blocked", footerLabel: statusLabel(goal)}
		st.reason = gstr(goal, "pauseReason")
		st.suggestedAction = gstr(goal, "pauseSuggestedAction")
		return st
	case "budget_limited":
		return dashboardGoalStatus{code: dashBudgetLimited, label: "Budget limited", footerLabel: statusLabel(goal)}
	case "complete":
		return dashboardGoalStatus{code: dashComplete, label: "Complete", footerLabel: statusLabel(goal)}
	}
	return dashboardGoalStatus{}
}

func dashTaskList(goal *jsObject) []any {
	tl := goal.obj("taskList")
	if tl == nil {
		return nil
	}
	tasks, _ := tl.vals["tasks"].([]any)
	return tasks
}

func dashSubtasks(t *jsObject) []any {
	subs, _ := t.vals["subtasks"].([]any)
	return subs
}

func dashIsDone(t any) bool {
	o, _ := t.(*jsObject)
	if o == nil {
		return false
	}
	s := gstr(o, "status")
	return s == "complete" || s == "skipped"
}

func dashCountDone(tasks []any) int {
	n := 0
	for _, t := range tasks {
		if dashIsDone(t) {
			n++
		}
	}
	return n
}

// deriveTopLevelTaskProgress: overall progress across top-level tasks only (skipped counts as done).
func deriveTopLevelTaskProgress(goal *jsObject) *taskProgress {
	tasks := dashTaskList(goal)
	if len(tasks) == 0 {
		return nil
	}
	completed := dashCountDone(tasks)
	return &taskProgress{completed, len(tasks), percentageOf(completed, len(tasks))}
}

func percentageOf(done, total int) int {
	return percentageOfF(float64(done), float64(total))
}

func percentageOfF(done, total float64) int {
	if total <= 0 {
		return 0
	}
	return int(math.Min(100, math.Max(0, jsRound(float64(done/total*100)))))
}

// flattenTaskTree flattens the recursive task tree into display rows.
func flattenTaskTree(tasks []any, currentTaskID string) []dashboardTaskNode {
	nodes := []dashboardTaskNode{}
	var walk func(list []any, depth int)
	walk = func(list []any, depth int) {
		for _, raw := range list {
			t, _ := raw.(*jsObject)
			if t == nil {
				continue
			}
			subs := dashSubtasks(t)
			id := gstr(t, "id")
			nodes = append(nodes, dashboardTaskNode{
				id: id, title: gstr(t, "title"), status: gstr(t, "status"), depth: depth, isCurrent: id == currentTaskID,
				verificationContract: gstr(t, "verificationContract"), evidence: gstr(t, "evidence"), completedAt: gstr(t, "completedAt"),
				totalSubtasks: len(subs), completedSubtasks: dashCountDone(subs),
			})
			walk(subs, depth+1)
		}
	}
	walk(tasks, 0)
	return nodes
}

func dashFindTask(tasks []any, id string) *jsObject {
	for _, raw := range tasks {
		t, _ := raw.(*jsObject)
		if t == nil {
			continue
		}
		if gstr(t, "id") == id {
			return t
		}
		if subs := dashSubtasks(t); len(subs) > 0 {
			if f := dashFindTask(subs, id); f != nil {
				return f
			}
		}
	}
	return nil
}

// deriveCurrentTaskSubtaskProgress: direct-child completion for a parent task; all-zero for a leaf.
func deriveCurrentTaskSubtaskProgress(currentID string, tasks []any) currentTaskSubtaskProgress {
	parent := dashFindTask(tasks, currentID)
	var children []any
	if parent != nil {
		children = dashSubtasks(parent)
	}
	if len(children) == 0 {
		return currentTaskSubtaskProgress{}
	}
	completed := dashCountDone(children)
	return currentTaskSubtaskProgress{completed, len(children), percentageOf(completed, len(children))}
}

// deriveCurrentTask resolves the persisted currentTaskId when it names a pending node, else the first pending node (inferred).
func deriveCurrentTask(goal *jsObject, nodes []dashboardTaskNode) *dashboardCurrentTask {
	tasks := dashTaskList(goal)
	if len(tasks) == 0 || len(nodes) == 0 {
		return nil
	}
	var node *dashboardTaskNode
	if cur := gstr(goal, "currentTaskId"); cur != "" {
		for i := range nodes {
			if nodes[i].id == cur && nodes[i].status == "pending" {
				node = &nodes[i]
				break
			}
		}
	}
	inferred := node == nil
	if inferred {
		for i := range nodes {
			if nodes[i].status == "pending" {
				node = &nodes[i]
				break
			}
		}
	}
	if node == nil {
		return nil
	}
	return &dashboardCurrentTask{
		currentTaskSubtaskProgress: deriveCurrentTaskSubtaskProgress(node.id, tasks),
		id:                         node.id, title: node.title, depth: node.depth,
		verificationContract: node.verificationContract, evidence: node.evidence, inferred: inferred,
	}
}

// latestCompletedNodeIndex: the node with status complete and the greatest completedAt (ties: last), or -1.
func latestCompletedNodeIndex(nodes []dashboardTaskNode) int {
	bestIndex, bestAt := -1, ""
	for i, n := range nodes {
		if n.status != "complete" || n.completedAt == "" {
			continue
		}
		if bestIndex == -1 || utf16Compare(n.completedAt, bestAt) >= 0 {
			bestIndex, bestAt = i, n.completedAt
		}
	}
	return bestIndex
}

// utf16Compare orders strings by UTF-16 code units, like JS `<`/`>=`.
func utf16Compare(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	ua, ub := dashUnits(ra), dashUnits(rb)
	for i := 0; i < len(ua) && i < len(ub); i++ {
		if ua[i] != ub[i] {
			if ua[i] < ub[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(ua) < len(ub):
		return -1
	case len(ua) > len(ub):
		return 1
	}
	return 0
}

func dashUnits(rs []rune) []uint16 {
	out := make([]uint16, 0, len(rs))
	for _, r := range rs {
		if r >= 0x10000 {
			r -= 0x10000
			out = append(out, uint16(0xD800+(r>>10)), uint16(0xDC00+(r&0x3FF)))
		} else {
			out = append(out, uint16(r))
		}
	}
	return out
}

func maxScrollOffset(totalRows, rows int) int {
	if totalRows-rows > 0 {
		return totalRows - rows
	}
	return 0
}

// clampScrollOffset clamps an offset into [0, maxScrollOffset].
func clampScrollOffset(offset float64, totalRows, rows int) int {
	o := math.Max(0, math.Floor(offset))
	return int(math.Min(o, float64(maxScrollOffset(totalRows, rows))))
}

// anchoredScrollOffset bottom-anchors the viewport to the most recently completed task.
func anchoredScrollOffset(nodes []dashboardTaskNode, rows int) int {
	anchor := latestCompletedNodeIndex(nodes)
	if anchor < 0 || rows <= 0 {
		return 0
	}
	return clampScrollOffset(float64(anchor-(rows-1)), len(nodes), rows)
}

// compactTaskViewportRows: task-row budget of the compact viewport by terminal width.
func compactTaskViewportRows(width int) int {
	switch {
	case width >= 100:
		return 5
	case width >= 70:
		return 4
	case width >= 50:
		return 3
	}
	return 2
}

// expandedTaskViewportRows: task-row budget of the expanded dashboard's task window by terminal width.
func expandedTaskViewportRows(width int) int {
	switch {
	case width >= 100:
		return 20
	case width >= 70:
		return 16
	case width >= 50:
		return 12
	}
	return 8
}

func taskViewportPageSize(rows float64) int {
	return int(math.Max(1, math.Floor(rows)))
}

type taskListViewport struct {
	totalRows, rows, offset, maxOffset, hiddenAbove, hiddenBelow int
}

// deriveTaskListViewport derives the window for totalRows given an offset and row budget.
func deriveTaskListViewport(totalRows, rows int, offset float64) taskListViewport {
	safeRows := rows
	if safeRows < 0 {
		safeRows = 0
	}
	clamped := clampScrollOffset(offset, totalRows, safeRows)
	maxOffset := maxScrollOffset(totalRows, safeRows)
	visible := totalRows - clamped
	if visible < 0 {
		visible = 0
	}
	if safeRows < visible {
		visible = safeRows
	}
	below := totalRows - (clamped + visible)
	if below < 0 {
		below = 0
	}
	return taskListViewport{totalRows, safeRows, clamped, maxOffset, clamped, below}
}

func formatDashboardDuration(seconds float64) string { return formatDuration(seconds) }

func formatAuditElapsed(elapsedMs float64) string {
	return formatDuration(math.Floor(math.Max(0, elapsedMs) / 1000))
}

func trimZero(value string) string { return strings.TrimSuffix(value, ".0") }

func formatCompactTokens(value float64) string {
	safe := math.Max(0, math.Floor(value))
	if safe >= 1_000_000 {
		return trimZero(jsToFixed(safe/1_000_000, 1)) + "M"
	}
	if safe >= 1_000 {
		return trimZero(jsToFixed(safe/1_000, 1)) + "K"
	}
	return jsNumber(safe)
}

// formatBudget: `18.2K / 50K · 36%`.
func formatBudget(used, total float64) string {
	pct := 0
	if total > 0 {
		pct = int(math.Min(100, math.Max(0, jsRound(float64(used/total*100)))))
	}
	return formatCompactTokens(used) + " / " + formatCompactTokens(total) + " · " + jsNumber(float64(pct)) + "%"
}

func dashUsageNum(goal *jsObject, k string) float64 {
	u := goal.obj("usage")
	if u == nil {
		return 0
	}
	return gnum(u, k)
}

// deriveGoalDashboardModel derives the unified dashboard model for one goal; nil when there is no goal.
func deriveGoalDashboardModel(goal *jsObject, options goalDashboardModelOptions) *goalDashboardModel {
	if goal == nil {
		return nil
	}
	status := deriveGoalStatus(goal)
	if sched := goal.obj("scheduler"); gstr(goal, "status") == "active" && sched != nil && gstr(sched, "phase") == "waiting" {
		status.code, status.label, status.footerLabel = dashIdle, "Waiting", "waiting"
	}
	// With tasks disabled, task sections are omitted entirely.
	var progress *taskProgress
	var tree []dashboardTaskNode
	var current *dashboardCurrentTask
	if !options.tasksDisabled {
		progress = deriveTopLevelTaskProgress(goal)
		tree = flattenTaskTree(dashTaskList(goal), "")
		current = deriveCurrentTask(goal, tree)
		if current != nil {
			for i := range tree {
				if tree[i].id == current.id {
					tree[i].isCurrent = true
				}
			}
		}
	}
	if tree == nil {
		tree = []dashboardTaskNode{}
	}
	taskTitles := make(map[string]string, len(tree))
	for _, n := range tree {
		taskTitles[n.id] = n.title
	}

	tokensUsed := dashUsageNum(goal, "tokensUsed")
	activeSeconds := dashUsageNum(goal, "activeSeconds")
	var budget *dashboardBudget
	if total, ok := goal.num("tokenBudget"); ok {
		budget = &dashboardBudget{used: tokensUsed, total: total, percentage: percentageOfF(tokensUsed, total), remaining: math.Max(0, total-tokensUsed)}
	}

	recent := deriveGoalActivity(options.ledgerEvents, gstr(goal, "id"), deriveGoalActivityOptions{taskTitles: taskTitles, limit: options.activityLimit})

	var bits []string
	if activeSeconds > 0 {
		bits = append(bits, formatDuration(activeSeconds))
	}
	if tokensUsed > 0 {
		bits = append(bits, formatCompactTokens(tokensUsed))
	}

	filePath := gstr(goal, "archivedPath")
	if p, ok := goal.str("activePath"); ok {
		filePath = p
	}
	var sched *jsObject = goal.obj("scheduler")
	scheduling := []string{}
	for _, row := range strings.Split(schedulerSummary(sched, options.maxAutonomousRuns, options.showAutonomousRuns == nil || *options.showAutonomousRuns), "\n") {
		if row != "" {
			scheduling = append(scheduling, row)
		}
	}
	return &goalDashboardModel{
		scheduling:               scheduling,
		goalId:                   gstr(goal, "id"),
		title:                    displayObjectiveTitle(gstr(goal, "objective")),
		status:                   status,
		focused:                  options.focused,
		auditorEnabled:           !(gflag(goal, "skipAuditor")),
		filePath:                 filePath,
		usage:                    dashboardUsage{activeSeconds: activeSeconds, tokens: tokensUsed, footerBits: strings.Join(bits, " ")},
		budget:                   budget,
		taskProgress:             progress,
		taskTree:                 tree,
		currentTask:              current,
		goalVerificationContract: gstr(goal, "verificationContract"),
		otherOpenGoals:           options.otherOpenGoals,
		recentActivity:           recent,
	}
}

// Note: every `x*y` handed to jsRound is wrapped in float64(...) so the compiler cannot fuse it into jsRound's
// `x - floor(x)` (arm64 FMA would turn 97.5 into 97.4999...); JavaScript always rounds the product first.
