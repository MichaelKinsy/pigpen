// Package tintinweb_tasks is a Go port of @tintinweb/pi-tasks 0.9.0: Claude Code-style task tracking for PiG (seven task
// tools, the /tasks command, a live widget, system reminders and the pi-subagents integration over the event bus).
package tintinweb_tasks

// task statuses. "deleted" is only ever an update request, never a stored status.
const (
	statusPending    = "pending"
	statusInProgress = "in_progress"
	statusCompleted  = "completed"
	statusDeleted    = "deleted"
)

// task is one stored task. upstream: types.ts:8-20.
type task struct {
	ID          string         `json:"id"`
	Subject     string         `json:"subject"`
	Description string         `json:"description"`
	Status      string         `json:"status"`
	ActiveForm  string         `json:"activeForm,omitempty"`
	Owner       string         `json:"owner,omitempty"`
	Metadata    map[string]any `json:"metadata"`
	Blocks      []string       `json:"blocks"`
	BlockedBy   []string       `json:"blockedBy"`
	CreatedAt   int64          `json:"createdAt"`
	UpdatedAt   int64          `json:"updatedAt"`
}

// storeData is the serialized store format on disk. upstream: types.ts:23-26.
type storeData struct {
	NextID int    `json:"nextId"`
	Tasks  []task `json:"tasks"`
}
