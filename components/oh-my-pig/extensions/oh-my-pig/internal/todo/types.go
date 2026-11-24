// Package todo ports omp's structured todo tool, /todo slash command, HUD widget,
// and completion reminder onto PiG's Go extension SDK.
package todo

// TodoStatus is the lifecycle state of a todo item.
// upstream: packages/tui/src/tools/todo.ts:26
type TodoStatus string

const (
	StatusPending    TodoStatus = "pending"
	StatusInProgress TodoStatus = "in_progress"
	StatusCompleted  TodoStatus = "completed"
	StatusAbandoned  TodoStatus = "abandoned"
	StatusBlocked    TodoStatus = "blocked"
)

// IsValidStatus reports whether s is a recognized omp TodoStatus.
func IsValidStatus(s string) bool {
	switch TodoStatus(s) {
	case StatusPending, StatusInProgress, StatusCompleted, StatusAbandoned, StatusBlocked:
		return true
	default:
		return false
	}
}

// TodoOperation is an operation name accepted by the todo tool.
// upstream: packages/tui/src/tools/todo.ts:29
type TodoOperation string

const (
	OpInit    TodoOperation = "init"
	OpStart   TodoOperation = "start"
	OpDone    TodoOperation = "done"
	OpRm      TodoOperation = "rm"
	OpDrop    TodoOperation = "drop"
	OpBlock   TodoOperation = "block"
	OpUnblock TodoOperation = "unblock"
	OpAppend  TodoOperation = "append"
	OpView    TodoOperation = "view"
)

// IsValidOp reports whether op is a recognized omp TodoOperation.
func IsValidOp(op string) bool {
	switch TodoOperation(op) {
	case OpInit, OpStart, OpDone, OpRm, OpDrop, OpBlock, OpUnblock, OpAppend, OpView:
		return true
	default:
		return false
	}
}

// TodoItem is a single task within a phase.
// upstream: packages/tui/src/tools/todo.ts:32-39
type TodoItem struct {
	Content string     `json:"content"`
	Status  TodoStatus `json:"status"`
	Blocker string     `json:"blocker,omitempty"`
}

// TodoPhase is a named group of todo tasks.
// upstream: packages/tui/src/tools/todo.ts:42-45
type TodoPhase struct {
	Name  string     `json:"name"`
	Tasks []TodoItem `json:"tasks"`
}

// TodoCompletionTransition records a task that became completed in the latest update.
// upstream: packages/tui/src/tools/todo.ts:48-51
type TodoCompletionTransition struct {
	Phase   string `json:"phase"`
	Content string `json:"content"`
}

// TodoToolDetails is persisted on toolResult entries and used for branch replay.
// upstream: packages/tui/src/tools/todo.ts:54-60
type TodoToolDetails struct {
	Op             TodoOperation              `json:"op,omitempty"`
	Phases         []TodoPhase                `json:"phases"`
	Storage        string                     `json:"storage"`
	CompletedTasks []TodoCompletionTransition `json:"completedTasks,omitempty"`
}

// InitListEntry is one phase entry in an `init` op's `list` parameter.
// upstream: packages/coding-agent/src/tools/todo.ts:55-58
type InitListEntry struct {
	Phase string   `json:"phase"`
	Items []string `json:"items"`
}

// TodoParams is the decoded parameter object for a single todo operation.
// upstream: packages/coding-agent/src/tools/todo.ts:60-72
type TodoParams struct {
	Op     TodoOperation   `json:"op"`
	List   []InitListEntry `json:"list,omitempty"`
	Task   string          `json:"task,omitempty"`
	Phase  string          `json:"phase,omitempty"`
	Items  []string        `json:"items,omitempty"`
	Reason string          `json:"reason,omitempty"`
}

// UserTodoEditCustomType is the custom session entry type written by /todo mutations.
// upstream: packages/coding-agent/src/tools/todo.ts:157
const UserTodoEditCustomType = "user_todo_edit"

// CloneTask returns a copy of task.
func CloneTask(task TodoItem) TodoItem {
	return TodoItem{
		Content: task.Content,
		Status:  task.Status,
		Blocker: task.Blocker,
	}
}

// ClonePhases returns a deep copy of phases.
// upstream: packages/coding-agent/src/tools/todo.ts:99-101
func ClonePhases(phases []TodoPhase) []TodoPhase {
	out := make([]TodoPhase, len(phases))
	for i, phase := range phases {
		tasks := make([]TodoItem, len(phase.Tasks))
		for j, task := range phase.Tasks {
			tasks[j] = CloneTask(task)
		}
		out[i] = TodoPhase{Name: phase.Name, Tasks: tasks}
	}
	return out
}

// IsClosedTodo reports whether a task is settled (completed or abandoned).
// upstream: packages/tui/src/tools/todo.ts:122-124
func IsClosedTodo(task TodoItem) bool {
	return task.Status == StatusCompleted || task.Status == StatusAbandoned
}

// NextActionableTask returns the active todo task, preferring an in_progress item
// over the first pending item across phases.
// upstream: packages/coding-agent/src/tools/todo.ts:146-155
func NextActionableTask(phases []TodoPhase) *TodoItem {
	var firstPending *TodoItem
	for i := range phases {
		for j := range phases[i].Tasks {
			task := &phases[i].Tasks[j]
			if task.Status == StatusInProgress {
				c := CloneTask(*task)
				return &c
			}
			if firstPending == nil && task.Status == StatusPending {
				c := CloneTask(*task)
				firstPending = &c
			}
		}
	}
	return firstPending
}
