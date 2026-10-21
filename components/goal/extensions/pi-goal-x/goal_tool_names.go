package pi_goal_x

// ported from goal-tool-names.ts

const (
	createGoalToolName     = "create_goal"
	getGoalToolName        = "get_goal"
	updateGoalToolName     = "update_goal"
	setGoalTasksToolName   = "set_goal_tasks"
	updateGoalTaskToolName = "update_goal_task"
	questionToolName       = "goal_question"
	questionnaireToolName  = "goal_questionnaire"
	proposeDraftToolName   = "propose_goal_draft"
)

// coreGoalToolNames is the stable core model surface (CORE_GOAL_TOOL_NAMES and CORE_GOAL_TOOLS).
var coreGoalToolNames = []string{createGoalToolName, getGoalToolName, updateGoalToolName}

// taskToolNames are the two consolidated task tools (TASK_TOOL_NAMES).
var taskToolNames = []string{setGoalTasksToolName, updateGoalTaskToolName}

// fiveGoalTools is the fixed task-enabled profile (FIVE_GOAL_TOOLS).
var fiveGoalTools = []string{createGoalToolName, getGoalToolName, updateGoalToolName, setGoalTasksToolName, updateGoalTaskToolName}

// coreGoalTools is the fixed task-disabled profile (CORE_GOAL_TOOLS).
var coreGoalTools = coreGoalToolNames

// draftingGoalTools is the transient drafting profile (DRAFTING_GOAL_TOOLS).
var draftingGoalTools = []string{questionToolName, questionnaireToolName, proposeDraftToolName}

// allRegisteredGoalTools is every goal tool the extension registers (ALL_REGISTERED_GOAL_TOOLS).
var allRegisteredGoalTools = append(append([]string{}, fiveGoalTools...), draftingGoalTools...)

// goalWorkToolNames count as real work toward the active goal (GOAL_WORK_TOOL_NAMES).
var goalWorkToolNames = []string{
	updateGoalToolName, setGoalTasksToolName, updateGoalTaskToolName, createGoalToolName, getGoalToolName,
	"write", "edit", "bash", "read", "grep", "find", "ls",
}

// goalProgressToolNames is the subset of work tools that indicates progress (GOAL_PROGRESS_TOOL_NAMES).
var goalProgressToolNames = []string{
	updateGoalToolName, updateGoalTaskToolName,
	"write", "edit", "bash", "read", "grep", "find", "ls",
}

// postStopAllowedTools may still be called on a stopped turn (POST_STOP_ALLOWED_TOOLS).
var postStopAllowedTools = []string{"get_goal"}

// applicableGoalTools advertises valid operations; goal nil = no goal.
func applicableGoalTools(goal *jsObject, tasksEnabled bool) []string {
	names := []string{createGoalToolName, getGoalToolName}
	if goal == nil {
		return names
	}
	status := gstr(goal, "status")
	if inList(status, "active", "paused", "budget_limited") {
		names = append(names, updateGoalToolName)
	}
	if tasksEnabled && inList(status, "active", "paused") {
		names = append(names, setGoalTasksToolName)
	}
	if tasksEnabled && status == "active" {
		if tl := goal.obj("taskList"); tl != nil {
			if ts, _ := tl.vals["tasks"].([]any); len(ts) > 0 {
				names = append(names, updateGoalTaskToolName)
			}
		}
	}
	return names
}
