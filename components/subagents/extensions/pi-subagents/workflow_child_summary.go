package pi_subagents

import ()

// Port of src/workflows/workflow-child-summary.ts: the bounded per-child summary a workflow publishes beside its status.

const workflowMaxRequiredIDBytes = 4096

var (
	workflowTerminalChildStates = map[string]bool{"completed": true, "failed": true, "paused": true, "stopped": true, "rejected": true, "detached": true}
	// workflowActivityCounters are the numeric activity fields, in the original's order (ACTIVITY_COUNTERS).
	workflowActivityCounters = []string{"currentToolStartedAt", "lastActivityAt", "durationMs", "toolCount", "turnCount", "tokens", "inputTokens", "outputTokens"}
)

// WorkflowChildActivity is the live activity of a synchronous foreground child (types.ts:181). A nil counter is absent.
type WorkflowChildActivity struct {
	CurrentTool          string   `json:"currentTool,omitempty"`
	CurrentToolStartedAt *float64 `json:"currentToolStartedAt,omitempty"`
	LastActivityAt       *float64 `json:"lastActivityAt,omitempty"`
	DurationMs           *float64 `json:"durationMs,omitempty"`
	ToolCount            *float64 `json:"toolCount,omitempty"`
	TurnCount            *float64 `json:"turnCount,omitempty"`
	Tokens               *float64 `json:"tokens,omitempty"`
	InputTokens          *float64 `json:"inputTokens,omitempty"`
	OutputTokens         *float64 `json:"outputTokens,omitempty"`
}

// counter returns the address of the counter with the given original field name.
func (a *WorkflowChildActivity) counter(name string) **float64 { panic("not implemented") }

// WorkflowAgentProgress is the part of the original's AgentProgress a workflow summary reads. Task, CurrentToolArgs and
// RecentOutput are carried only to show that they never reach a summary.
type WorkflowAgentProgress struct {
	WorkflowChildActivity
	Agent           string
	SessionName     string
	Model           string
	Thinking        string
	Task            string
	CurrentToolArgs string
	RecentOutput    []string
}

// WorkflowChildLiveProgress is the bounded snapshot published for a running child (workflowChildProgress).
type WorkflowChildLiveProgress struct {
	WorkflowChildActivity
	Agent       string
	SessionName string
	Model       string
	Thinking    string
}

// boundedText is bounded(): a non-blank string of at most maxBytes UTF-8 bytes, or "" when it is neither.
func boundedText(value string, maxBytes int) string { panic("not implemented") }

// WorkflowChildProgress is workflowChildProgress.
func WorkflowChildProgress(p WorkflowAgentProgress) WorkflowChildLiveProgress {
	panic("not implemented")
}

// WorkflowChildActivityOf is workflowChildActivity: a fixed field count and a 256-byte tool name keep the JSON of an activity
// below 2 KiB, escaping included.
func WorkflowChildActivityOf(p WorkflowChildActivity) WorkflowChildActivity { panic("not implemented") }

func parseWorkflowActivity(value any) (*WorkflowChildActivity, error) { panic("not implemented") }

func workflowRequiredID(value, label string) (string, error) { panic("not implemented") }

// WorkflowChildSummaryRow is one child of a summary.
type WorkflowChildSummaryRow struct {
	ChildID     string                 `json:"childId"`
	RunID       string                 `json:"runId,omitempty"`
	Agent       string                 `json:"agent,omitempty"`
	SessionName string                 `json:"sessionName,omitempty"`
	Model       string                 `json:"model,omitempty"`
	Thinking    string                 `json:"thinking,omitempty"`
	Activity    *WorkflowChildActivity `json:"activity,omitempty"`
	State       string                 `json:"state"` // pending, running, completed, failed, paused, stopped, rejected, detached
}

// WorkflowChildSummary is the published summary (types.ts:193).
type WorkflowChildSummary struct {
	Version           int                       `json:"version"`
	ParentToolCallID  string                    `json:"parentToolCallId"`
	WorkflowRunID     string                    `json:"workflowRunId"`
	InventoryComplete bool                      `json:"inventoryComplete"`
	WorkflowState     string                    `json:"workflowState"` // queued, running, completed, failed, paused, stopped
	Children          []WorkflowChildSummaryRow `json:"children"`
}

// WorkflowStepStatus is the part of an async status step that carries a workflow key (AsyncStatus["steps"]).
type WorkflowStepStatus struct {
	WorkflowKey string
	Async       *bool
	RunID       string
	Status      string
	SessionFile string
	Model       string
	Agent       string
	SessionName string
	Thinking    string
}

// WorkflowChildSummaryInput mirrors the argument of workflowChildSummary.
type WorkflowChildSummaryInput struct {
	ParentToolCallID  string
	WorkflowRunID     string
	WorkflowState     string
	InventoryComplete bool
	Trace             []WorkflowScriptTraceEntry
	Children          []*jsObject
	Steps             []WorkflowStepStatus
	// Progress holds in-memory snapshots from synchronous child callbacks, by workflow key.
	Progress map[string]WorkflowChildLiveProgress
}

type workflowRowSet struct {
	order []string
	rows  map[string]WorkflowChildSummaryRow
}

func (r *workflowRowSet) set(key string, row WorkflowChildSummaryRow) { panic("not implemented") }

// WorkflowChildSummaryOf is workflowChildSummary.
func WorkflowChildSummaryOf(in WorkflowChildSummaryInput) (WorkflowChildSummary, error) {
	panic("not implemented")
}

// ParseWorkflowChildSummary is parseWorkflowChildSummary: a published summary read back strictly. The value is parsed JSON
// (*jsObject); undef{} gives (nil, nil), as undefined does.
func ParseWorkflowChildSummary(value any) (*WorkflowChildSummary, error) { panic("not implemented") }

func isBool(v any) bool { panic("not implemented") }
