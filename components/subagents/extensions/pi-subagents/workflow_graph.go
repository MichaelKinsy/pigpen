package pi_subagents

import (
	"math"
	"regexp"
)

// Port of src/runs/shared/workflow-graph.ts. Chain steps are ordered JSON objects (*jsObject): the original reads them as plain
// literals, and the shape (sequential, parallel, dynamic parallel) is decided by which keys they carry (settings.ts:127,131).

// WorkflowGraphNode is a displayable workflow stage (types.ts WorkflowGraphNode).
type WorkflowGraphNode struct {
	ID               string
	Kind             string // step, parallel-group, dynamic-parallel-group, agent, host-step
	Agent            string
	Phase            string
	Label            string
	Status           string
	FlatIndex        *int
	StepIndex        *int
	Children         []*WorkflowGraphNode // nil when the node has no children list (a dynamic group with none has an empty one)
	Dynamic          *WorkflowGraphDynamic
	ItemKey          string
	OutputName       string
	Structured       bool
	AcceptanceStatus string
	Error            string
	HostStep         *WorkflowHostStep
}

// WorkflowGraphDynamic describes the expansion of a dynamic parallel group.
type WorkflowGraphDynamic struct {
	SourceOutput string
	SourcePath   string
	ItemName     string
	MaxItems     *float64
	CollectAs    string
}

// WorkflowGraphPhase groups node IDs under a phase title.
type WorkflowGraphPhase struct {
	Title   string
	NodeIDs []string
}

// WorkflowGraphSnapshot is the node tree of one run.
type WorkflowGraphSnapshot struct {
	RunID         string
	Mode          string
	Phases        []WorkflowGraphPhase
	Nodes         []*WorkflowGraphNode
	CurrentNodeID string
}

// WorkflowGraphResult is the part of a child result the graph reads (SingleResult).
type WorkflowGraphResult struct {
	ExitCode         int
	Detached         bool
	Interrupted      bool
	Error            string
	AcceptanceStatus string
}

// WorkflowGraphStepStatus is an externally supplied step status.
type WorkflowGraphStepStatus struct {
	Status string
	Error  string
}

// WorkflowGraphDynamicChild is one materialized child of a dynamic group.
type WorkflowGraphDynamicChild struct {
	Agent      string
	Label      string
	FlatIndex  int
	ItemKey    string
	OutputName string
	Structured bool
	Error      string
}

// WorkflowGraphGroupStatus overrides the status of a dynamic group.
type WorkflowGraphGroupStatus struct {
	Status           string
	Error            string
	AcceptanceStatus string
}

// WorkflowGraphBuildInput is the input of BuildWorkflowGraphSnapshot. A nil Results/StepStatuses entry means none.
type WorkflowGraphBuildInput struct {
	RunID                string
	Mode                 string
	Steps                []*jsObject
	Results              []*WorkflowGraphResult
	CurrentFlatIndex     *int
	CurrentStepIndex     *int
	StepStatuses         []*WorkflowGraphStepStatus
	DynamicChildren      map[int][]WorkflowGraphDynamicChild
	DynamicGroupStatuses map[int]WorkflowGraphGroupStatus
}

// WorkflowGraphStageNodes returns displayable workflow stages while hiding structural parallel groups and host monitors
// (workflow-graph.ts:17).
func WorkflowGraphStageNodes(graph *WorkflowGraphSnapshot) []*WorkflowGraphNode {
	if graph == nil || len(graph.Nodes) == 0 {
		return nil
	}
	var stages []*WorkflowGraphNode
	var visit func(n *WorkflowGraphNode)
	visit = func(n *WorkflowGraphNode) {
		if n.Kind == "parallel-group" || n.Kind == "dynamic-parallel-group" {
			for _, c := range n.Children {
				visit(c)
			}
			return
		}
		if n.Kind != "host-step" {
			stages = append(stages, n)
		}
	}
	for _, n := range graph.Nodes {
		visit(n)
	}
	return stages
}

func normalizeGraphStatus(status string) string {
	switch status {
	case "complete", "completed":
		return "completed"
	case "running", "failed", "paused", "detached", "rejected", "pending":
		return status
	}
	return ""
}

func graphResultStatus(r *WorkflowGraphResult) string {
	switch {
	case r == nil:
		return ""
	case r.Detached:
		return "detached"
	case r.Interrupted:
		return "paused"
	case r.ExitCode == 0:
		return "completed"
	}
	return "failed"
}

func graphResultAt(in *WorkflowGraphBuildInput, i int) *WorkflowGraphResult {
	if i >= 0 && i < len(in.Results) {
		return in.Results[i]
	}
	return nil
}

func graphStatusAt(in *WorkflowGraphBuildInput, i int) *WorkflowGraphStepStatus {
	if i >= 0 && i < len(in.StepStatuses) {
		return in.StepStatuses[i]
	}
	return nil
}

func graphNodeStatus(in *WorkflowGraphBuildInput, flatIndex int) string {
	if s := graphStatusAt(in, flatIndex); s != nil {
		if n := normalizeGraphStatus(s.Status); n != "" {
			return n
		}
	}
	if s := graphResultStatus(graphResultAt(in, flatIndex)); s != "" {
		return s
	}
	if in.CurrentFlatIndex != nil && *in.CurrentFlatIndex == flatIndex {
		return "running"
	}
	return "pending"
}

// graphError is `stepStatuses[i]?.error ?? results[i]?.error ?? fallback`.
func graphError(in *WorkflowGraphBuildInput, i int, fallback string) string {
	if s := graphStatusAt(in, i); s != nil && s.Error != "" {
		return s.Error
	}
	if r := graphResultAt(in, i); r != nil && r.Error != "" {
		return r.Error
	}
	return fallback
}

func graphAcceptance(in *WorkflowGraphBuildInput, i int) string {
	if r := graphResultAt(in, i); r != nil {
		return r.AcceptanceStatus
	}
	return ""
}

func pushGraphPhase(phases *[]WorkflowGraphPhase, phase, nodeID string) {
	if phase == "" {
		return
	}
	for i := range *phases {
		if (*phases)[i].Title == phase {
			(*phases)[i].NodeIDs = append((*phases)[i].NodeIDs, nodeID)
			return
		}
	}
	*phases = append(*phases, WorkflowGraphPhase{Title: phase, NodeIDs: []string{nodeID}})
}

func summarizeParallelStatuses(statuses []string) string {
	has := func(s string) bool {
		for _, x := range statuses {
			if x == s {
				return true
			}
		}
		return false
	}
	for _, s := range []string{"running", "rejected", "failed", "paused", "detached"} {
		if has(s) {
			return s
		}
	}
	all := len(statuses) > 0
	for _, s := range statuses {
		if s != "completed" {
			all = false
		}
	}
	if all {
		return "completed"
	}
	if has("completed") {
		return "running"
	}
	return "pending"
}

var graphItemKeyRe = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

func graphStr(o *jsObject, k string) string {
	s, _ := o.vals[k].(string)
	return s
}

// jsTruthy is the JavaScript truthiness of a decoded JSON value.
func jsTruthy(v any) bool {
	switch x := v.(type) {
	case nil, undef:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case float64:
		return x != 0 && !math.IsNaN(x)
	}
	return true
}

func isParallelGraphStep(step *jsObject) ([]any, bool) {
	list, ok := step.vals["parallel"].([]any)
	return list, ok
}

func isDynamicGraphStep(step *jsObject) bool {
	_, hasExpand := step.vals["expand"]
	_, hasCollect := step.vals["collect"]
	p, hasParallel := step.vals["parallel"]
	_, isArray := p.([]any)
	return hasExpand && hasCollect && hasParallel && !isArray
}

func graphSeqLabel(step *jsObject, stepIndex int) string {
	if l := jsTrim(graphStr(step, "label")); l != "" {
		return l
	}
	if a := graphStr(step, "agent"); a != "" {
		return a
	}
	return "Step " + itoa(stepIndex+1)
}

// BuildWorkflowGraphSnapshot is buildWorkflowGraphSnapshot (workflow-graph.ts:92).
func BuildWorkflowGraphSnapshot(in WorkflowGraphBuildInput) *WorkflowGraphSnapshot {
	var nodes []*WorkflowGraphNode
	var phases []WorkflowGraphPhase
	flatIndex := 0
	currentNodeID := ""
	curStep := func(i int) bool { return in.CurrentStepIndex != nil && *in.CurrentStepIndex == i }
	curFlat := func(i int) bool { return in.CurrentFlatIndex != nil && *in.CurrentFlatIndex == i }

	for stepIndex, step := range in.Steps {
		si := stepIndex
		if list, ok := isParallelGraphStep(step); ok {
			groupID := "step-" + itoa(stepIndex)
			children := []*WorkflowGraphNode{}
			var childStatuses []string
			for taskIndex, raw := range list {
				task, _ := raw.(*jsObject)
				if task == nil {
					task = newObject()
				}
				status := graphNodeStatus(&in, flatIndex)
				childStatuses = append(childStatuses, status)
				childID := "step-" + itoa(stepIndex) + "-agent-" + itoa(taskIndex)
				label := jsTrim(graphStr(task, "label"))
				if label == "" {
					label = graphStr(task, "agent")
				}
				if label == "" {
					label = "Agent " + itoa(taskIndex+1)
				}
				fi := flatIndex
				child := &WorkflowGraphNode{ID: childID, Kind: "agent", Agent: graphStr(task, "agent"), Phase: graphStr(task, "phase"), Label: label,
					Status: status, FlatIndex: &fi, StepIndex: &si, OutputName: graphStr(task, "as"), Structured: jsTruthy(task.vals["outputSchema"]),
					AcceptanceStatus: graphAcceptance(&in, flatIndex), Error: graphError(&in, flatIndex, "")}
				children = append(children, child)
				pushGraphPhase(&phases, child.Phase, childID)
				if status == "running" || curFlat(flatIndex) {
					currentNodeID = childID
				}
				flatIndex++
			}
			groupStatus := summarizeParallelStatuses(childStatuses)
			if curStep(stepIndex) && currentNodeID == "" {
				currentNodeID = groupID
			}
			label := "Parallel group (" + itoa(len(list)) + ")"
			if len(list) == 1 {
				label = "Parallel task"
			}
			nodes = append(nodes, &WorkflowGraphNode{ID: groupID, Kind: "parallel-group", Label: label, Status: groupStatus, StepIndex: &si, Children: children})
			continue
		}

		if isDynamicGraphStep(step) {
			groupID := "step-" + itoa(stepIndex)
			materialized := in.DynamicChildren[stepIndex]
			override, hasOverride := in.DynamicGroupStatuses[stepIndex]
			tmpl, _ := step.vals["parallel"].(*jsObject)
			if tmpl == nil {
				tmpl = newObject()
			}
			collect, _ := step.vals["collect"].(*jsObject)
			if collect == nil {
				collect = newObject()
			}
			expand, _ := step.vals["expand"].(*jsObject)
			if expand == nil {
				expand = newObject()
			}
			from, _ := expand.vals["from"].(*jsObject)
			if from == nil {
				from = newObject()
			}
			phase := graphStr(tmpl, "phase")
			if phase == "" {
				phase = graphStr(step, "phase")
			}
			children := []*WorkflowGraphNode{}
			var childStatuses []string
			for _, task := range materialized {
				status := graphNodeStatus(&in, task.FlatIndex)
				childStatuses = append(childStatuses, status)
				childID := "step-" + itoa(stepIndex) + "-item-" + graphItemKeyRe.ReplaceAllString(task.ItemKey, "-")
				label := jsTrim(task.Label)
				if label == "" {
					label = jsTrim(graphStr(tmpl, "label"))
				}
				if label == "" {
					label = task.Agent + " " + task.ItemKey
				}
				fi := task.FlatIndex
				child := &WorkflowGraphNode{ID: childID, Kind: "agent", Agent: task.Agent, Phase: phase, Label: label, Status: status, FlatIndex: &fi,
					StepIndex: &si, ItemKey: task.ItemKey, OutputName: task.OutputName, Structured: task.Structured,
					AcceptanceStatus: graphAcceptance(&in, task.FlatIndex), Error: graphError(&in, task.FlatIndex, task.Error)}
				children = append(children, child)
				pushGraphPhase(&phases, child.Phase, childID)
				if status == "running" || curFlat(task.FlatIndex) {
					currentNodeID = childID
				}
			}
			groupStatus := "pending"
			switch {
			case hasOverride && override.Status != "":
				groupStatus = override.Status
			case len(children) > 0:
				groupStatus = summarizeParallelStatuses(childStatuses)
			case curStep(stepIndex):
				groupStatus = "running"
			}
			if curStep(stepIndex) && currentNodeID == "" {
				currentNodeID = groupID
			}
			label := jsTrim(graphStr(step, "label"))
			if label == "" {
				label = jsTrim(graphStr(tmpl, "label"))
			}
			if label == "" {
				label = "Dynamic fanout (" + graphStr(collect, "as") + ")"
			}
			itemName := graphStr(expand, "item")
			if itemName == "" {
				itemName = "item"
			}
			dyn := &WorkflowGraphDynamic{SourceOutput: graphStr(from, "output"), SourcePath: graphStr(from, "path"), ItemName: itemName, CollectAs: graphStr(collect, "as")}
			if m, ok := expand.vals["maxItems"].(float64); ok {
				dyn.MaxItems = &m
			}
			node := &WorkflowGraphNode{ID: groupID, Kind: "dynamic-parallel-group", Label: label, Status: groupStatus, StepIndex: &si,
				OutputName: graphStr(collect, "as"), Structured: jsTruthy(collect.vals["outputSchema"]), Dynamic: dyn, Children: children}
			if hasOverride {
				node.AcceptanceStatus, node.Error = override.AcceptanceStatus, override.Error
			}
			nodes = append(nodes, node)
			for _, c := range materialized {
				if c.FlatIndex+1 > flatIndex {
					flatIndex = c.FlatIndex + 1
				}
			}
			continue
		}

		status := graphNodeStatus(&in, flatIndex)
		id := "step-" + itoa(stepIndex)
		fi := flatIndex
		nodes = append(nodes, &WorkflowGraphNode{ID: id, Kind: "step", Agent: graphStr(step, "agent"), Phase: graphStr(step, "phase"), Label: graphSeqLabel(step, stepIndex),
			Status: status, FlatIndex: &fi, StepIndex: &si, OutputName: graphStr(step, "as"), Structured: jsTruthy(step.vals["outputSchema"]),
			AcceptanceStatus: graphAcceptance(&in, flatIndex), Error: graphError(&in, flatIndex, "")})
		pushGraphPhase(&phases, graphStr(step, "phase"), id)
		if status == "running" || curFlat(flatIndex) || curStep(stepIndex) {
			currentNodeID = id
		}
		flatIndex++
	}
	mode := in.Mode
	if mode == "" {
		mode = "chain"
	}
	return &WorkflowGraphSnapshot{RunID: in.RunID, Mode: mode, Phases: phases, Nodes: nodes, CurrentNodeID: currentNodeID}
}
