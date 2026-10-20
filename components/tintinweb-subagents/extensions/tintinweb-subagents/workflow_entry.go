package tintinweb_subagents

// What a finished workflow leaves behind in the session transcript. A workflow started from --subagents-workflow-file
// has no tool call to hang its result card on, so it appends a custom session entry instead; a plain-JSON snapshot
// rather than the live task, which holds a cancel func, the script source and the run's control handle.
// upstream: src/workflow/entry.ts.

// workflowEntryType is the customType of the session entry a flag-launched workflow renders through. upstream: WORKFLOW_ENTRY_TYPE.
const workflowEntryType = "subagents:workflow"

// workflowEntryData is the persisted snapshot of a settled run. upstream: WorkflowEntryData.
type workflowEntryData struct {
	Name        string          `json:"name"`
	Status      string          `json:"status"`
	StartTime   int64           `json:"startTime"`
	EndTime     *int64          `json:"endTime,omitempty"`
	Progress    []workflowEntry `json:"progress"`
	AgentCount  int             `json:"agentCount"`
	TotalTokens int             `json:"totalTokens"`
	Meta        *workflowMeta   `json:"meta,omitempty"`
}

// snapshotWorkflowEntry snapshots a settled task for workflowEntryType. upstream: workflowEntryData().
func snapshotWorkflowEntry(task *workflowTask) workflowEntryData {
	task.mu.Lock()
	defer task.mu.Unlock()
	name := task.WorkflowName
	if name == "" {
		name = task.ID
	}
	return workflowEntryData{Name: name, Status: task.Status, StartTime: task.StartTime, EndTime: task.EndTime,
		Progress: append([]workflowEntry{}, task.WorkflowProgress...), AgentCount: task.AgentCount, TotalTokens: task.TotalTokens, Meta: task.Meta}
}
