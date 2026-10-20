package pi_subagents

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

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
func (a *WorkflowChildActivity) counter(name string) **float64 {
	switch name {
	case "currentToolStartedAt":
		return &a.CurrentToolStartedAt
	case "lastActivityAt":
		return &a.LastActivityAt
	case "durationMs":
		return &a.DurationMs
	case "toolCount":
		return &a.ToolCount
	case "turnCount":
		return &a.TurnCount
	case "tokens":
		return &a.Tokens
	case "inputTokens":
		return &a.InputTokens
	case "outputTokens":
		return &a.OutputTokens
	}
	return nil
}

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
func boundedText(value string, maxBytes int) string {
	if jsTrim(value) == "" || len(value) > maxBytes {
		return ""
	}
	return value
}

// WorkflowChildProgress is workflowChildProgress.
func WorkflowChildProgress(p WorkflowAgentProgress) WorkflowChildLiveProgress {
	out := WorkflowChildLiveProgress{WorkflowChildActivity: WorkflowChildActivityOf(p.WorkflowChildActivity), Agent: boundedText(p.Agent, 256)}
	if boundedText(p.SessionName, 256) != "" {
		out.SessionName = p.SessionName
	}
	if boundedText(p.Model, 256) != "" {
		out.Model = p.Model
	}
	if boundedText(p.Thinking, 32) != "" {
		out.Thinking = p.Thinking
	}
	return out
}

// WorkflowChildActivityOf is workflowChildActivity: a fixed field count and a 256-byte tool name keep the JSON of an activity
// below 2 KiB, escaping included.
func WorkflowChildActivityOf(p WorkflowChildActivity) WorkflowChildActivity {
	var out WorkflowChildActivity
	if boundedText(p.CurrentTool, 256) != "" {
		out.CurrentTool = p.CurrentTool
	}
	for _, name := range workflowActivityCounters {
		if v := *p.counter(name); v != nil && !math.IsNaN(*v) && !math.IsInf(*v, 0) && *v >= 0 {
			*out.counter(name) = new(*v)
		}
	}
	return out
}

func parseWorkflowActivity(value any) (*WorkflowChildActivity, error) {
	if u, ok := value.(undef); ok || value == nil {
		_ = u
		if ok {
			return nil, nil
		}
		return nil, errors.New("workflowChildren child activity is invalid.")
	}
	in, ok := value.(*jsObject)
	if !ok {
		return nil, errors.New("workflowChildren child activity is invalid.")
	}
	var out WorkflowChildActivity
	for _, k := range in.order() {
		v := in.vals[k]
		if k == "currentTool" {
			s, isStr := v.(string)
			if !isStr || boundedText(s, 256) == "" {
				return nil, errors.New("workflowChildren child activity is invalid.")
			}
			out.CurrentTool = s
			continue
		}
		known := false
		for _, c := range workflowActivityCounters {
			known = known || c == k
		}
		f, isNum := goFloat(v)
		if !known || !isNum || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
			return nil, errors.New("workflowChildren child activity is invalid.")
		}
		*out.counter(k) = new(f)
	}
	return &out, nil
}

func workflowRequiredID(value, label string) (string, error) {
	id := boundedText(value, workflowMaxRequiredIDBytes)
	if id == "" {
		return "", fmt.Errorf("%s must be a non-empty identifier of at most %d UTF-8 bytes.", label, workflowMaxRequiredIDBytes)
	}
	return id, nil
}

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

func (r *workflowRowSet) set(key string, row WorkflowChildSummaryRow) {
	if _, ok := r.rows[key]; !ok {
		r.order = append(r.order, key)
	}
	r.rows[key] = row
}

// WorkflowChildSummaryOf is workflowChildSummary.
func WorkflowChildSummaryOf(in WorkflowChildSummaryInput) (WorkflowChildSummary, error) {
	rows := &workflowRowSet{rows: map[string]WorkflowChildSummaryRow{}}
	runningAsync := map[string]bool{}
	for _, c := range in.Children {
		if objStr(c, "state") == "running" {
			runningAsync[objStr(c, "key")] = true
		}
	}
	for _, e := range in.Trace {
		if e.Operation != "run" || !workflowKeyPattern.MatchString(e.Key) {
			continue
		}
		prev, hadPrev := rows.rows[e.Key]
		state := "running"
		switch e.State {
		case "completed", "failed", "stopped", "detached":
			state = e.State
		default:
			if hadPrev {
				state = prev.State
			}
		}
		row := WorkflowChildSummaryRow{ChildID: e.Key, State: state, RunID: boundedText(e.RunID, 256)}
		if hadPrev {
			row.Agent, row.SessionName, row.Model, row.Thinking = prev.Agent, prev.SessionName, prev.Model, prev.Thinking
		}
		rows.set(e.Key, row)
	}
	for _, step := range in.Steps {
		key := step.WorkflowKey
		if key == "" || !workflowKeyPattern.MatchString(key) {
			continue
		}
		if step.Async != nil && *step.Async && step.RunID != "" && step.Status == "running" {
			runningAsync[key] = true
		}
		state := "running"
		switch step.Status {
		case "complete", "completed":
			state = "completed"
		case "failed", "paused", "stopped", "rejected", "pending":
			state = step.Status
		}
		launchResolved := step.Async != nil || step.SessionFile != "" || step.RunID != "" || step.Model != ""
		row := WorkflowChildSummaryRow{ChildID: key, State: state, RunID: boundedText(step.RunID, 256), SessionName: boundedText(step.SessionName, 256), Model: boundedText(step.Model, 256), Thinking: boundedText(step.Thinking, 32)}
		if launchResolved {
			row.Agent = boundedText(step.Agent, 256)
		}
		rows.set(key, row)
	}
	for _, child := range in.Children {
		key := objStr(child, "key")
		if !workflowKeyPattern.MatchString(key) {
			continue
		}
		var result *jsObject
		if rs, ok := child.vals["results"].([]any); ok {
			for _, r := range rs {
				if ro, ok := r.(*jsObject); ok {
					result = ro
					break
				}
			}
		}
		state := "failed"
		switch {
		case objStr(child, "state") == "running":
			state = "running"
		case objTruthy(child, "detached"):
			state = "detached"
		case objTruthy(child, "stopped"):
			state = "stopped"
		case objTruthy(child, "interrupted"):
			state = "paused"
		case objTruthy(child, "ok"):
			state = "completed"
		default:
			if result != nil {
				if acc, ok := result.vals["acceptance"].(*jsObject); ok && objStr(acc, "status") == "rejected" {
					state = "rejected"
				}
			}
		}
		row := WorkflowChildSummaryRow{ChildID: key, State: state, RunID: boundedText(objStr(child, "runId"), 256), Agent: boundedText(objStr(child, "agent"), 256)}
		if result != nil {
			row.SessionName = boundedText(objStr(result, "sessionName"), 256)
			row.Model = boundedText(objStr(result, "model"), 256)
			row.Thinking = boundedText(objStr(result, "thinking"), 32)
		}
		rows.set(key, row)
	}
	if in.InventoryComplete {
		for _, key := range rows.order {
			row := rows.rows[key]
			if !workflowTerminalChildStates[row.State] && !runningAsync[key] {
				if in.WorkflowState == "stopped" {
					row.State = "stopped"
				} else {
					row.State = "failed"
				}
				rows.rows[key] = row
			}
		}
	}
	progressKeys := make([]string, 0, len(in.Progress))
	for k := range in.Progress {
		progressKeys = append(progressKeys, k)
	}
	sort.Strings(progressKeys)
	for _, key := range progressKeys {
		p := in.Progress[key]
		row, ok := rows.rows[key]
		if !ok || row.State != "running" {
			continue
		}
		if boundedText(p.Agent, 256) != "" {
			row.Agent = p.Agent
		}
		if boundedText(p.SessionName, 256) != "" {
			row.SessionName = p.SessionName
		}
		if boundedText(p.Model, 256) != "" {
			row.Model = p.Model
		}
		if boundedText(p.Thinking, 32) != "" {
			row.Thinking = p.Thinking
		}
		act := WorkflowChildActivityOf(p.WorkflowChildActivity)
		row.Activity = &act
		rows.rows[key] = row
	}
	parent, err := workflowRequiredID(in.ParentToolCallID, "parentToolCallId")
	if err != nil {
		return WorkflowChildSummary{}, err
	}
	run, err := workflowRequiredID(in.WorkflowRunID, "workflowRunId")
	if err != nil {
		return WorkflowChildSummary{}, err
	}
	out := WorkflowChildSummary{Version: 1, ParentToolCallID: parent, WorkflowRunID: run, InventoryComplete: in.InventoryComplete, WorkflowState: in.WorkflowState, Children: []WorkflowChildSummaryRow{}}
	for _, k := range rows.order {
		out.Children = append(out.Children, rows.rows[k])
	}
	return out, nil
}

// ParseWorkflowChildSummary is parseWorkflowChildSummary: a published summary read back strictly. The value is parsed JSON
// (*jsObject); undef{} gives (nil, nil), as undefined does.
func ParseWorkflowChildSummary(value any) (*WorkflowChildSummary, error) {
	if _, u := value.(undef); u {
		return nil, nil
	}
	in, ok := value.(*jsObject)
	if !ok {
		return nil, errors.New("workflowChildren must be an object.")
	}
	allowed := map[string]bool{"version": true, "parentToolCallId": true, "workflowRunId": true, "inventoryComplete": true, "workflowState": true, "children": true}
	for _, k := range in.order() {
		if !allowed[k] {
			return nil, errors.New("workflowChildren has unsupported fields.")
		}
	}
	rawChildren, isList := in.vals["children"].([]any)
	if v, _ := goFloat(in.vals["version"]); v != 1 || !isBool(in.vals["inventoryComplete"]) || !isList {
		return nil, errors.New("workflowChildren is invalid.")
	}
	workflowState, _ := in.vals["workflowState"].(string)
	switch workflowState {
	case "queued", "running", "completed", "failed", "paused", "stopped":
	default:
		return nil, errors.New("workflowChildren.workflowState is invalid.")
	}
	var children []WorkflowChildSummaryRow
	seen := map[string]bool{}
	for _, raw := range rawChildren {
		child, ok := raw.(*jsObject)
		if !ok {
			return nil, errors.New("workflowChildren child row is invalid.")
		}
		for _, k := range child.order() {
			switch k {
			case "childId", "runId", "agent", "sessionName", "model", "thinking", "state", "activity":
			default:
				return nil, errors.New("workflowChildren child row has unsupported fields.")
			}
		}
		id, isStr := child.vals["childId"].(string)
		if !isStr || !workflowKeyPattern.MatchString(id) {
			return nil, errors.New("workflowChildren childId is invalid.")
		}
		state, _ := child.vals["state"].(string)
		switch state {
		case "pending", "running", "completed", "failed", "paused", "stopped", "rejected", "detached":
		default:
			return nil, errors.New("workflowChildren child state is invalid.")
		}
		row := WorkflowChildSummaryRow{ChildID: id, State: state}
		for _, f := range []struct {
			name     string
			maxBytes int
			dst      *string
		}{{"runId", 256, &row.RunID}, {"agent", 256, &row.Agent}, {"sessionName", 256, &row.SessionName}, {"model", 256, &row.Model}, {"thinking", 32, &row.Thinking}} {
			v, present := child.vals[f.name]
			if _, u := v.(undef); !present || u {
				continue
			}
			s, isStr := v.(string)
			if !isStr || boundedText(s, f.maxBytes) == "" {
				return nil, fmt.Errorf("workflowChildren child %s is invalid.", f.name)
			}
			*f.dst = s
		}
		activityValue, has := child.vals["activity"]
		if !has {
			activityValue = undef{}
		}
		activity, err := parseWorkflowActivity(activityValue)
		if err != nil {
			return nil, err
		}
		if activity != nil && state != "running" {
			return nil, errors.New("workflowChildren child activity requires running state.")
		}
		row.Activity = activity
		children = append(children, row)
	}
	for _, c := range children {
		if seen[c.ChildID] {
			return nil, errors.New("workflowChildren has duplicate childId values.")
		}
		seen[c.ChildID] = true
	}
	parent, isStr := in.vals["parentToolCallId"].(string)
	if !isStr {
		return nil, errors.New("workflowChildren.parentToolCallId is invalid.")
	}
	run, isStr := in.vals["workflowRunId"].(string)
	if !isStr {
		return nil, errors.New("workflowChildren.workflowRunId is invalid.")
	}
	parent, err := workflowRequiredID(parent, "workflowChildren.parentToolCallId")
	if err != nil {
		return nil, err
	}
	run, err = workflowRequiredID(run, "workflowChildren.workflowRunId")
	if err != nil {
		return nil, err
	}
	complete, _ := in.vals["inventoryComplete"].(bool)
	return &WorkflowChildSummary{Version: 1, ParentToolCallID: parent, WorkflowRunID: run, InventoryComplete: complete, WorkflowState: workflowState, Children: children}, nil
}

func isBool(v any) bool { _, ok := v.(bool); return ok }
