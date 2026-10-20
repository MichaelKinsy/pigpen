package tintinweb_subagents

// Deciding what to do when another extension already offers a workflow tool. Workflows are on by default, so this
// extension can be the second orchestrator in a session; two workflow tools in one spec is worse than either alone, and
// the one installed on purpose should be the one that survives. A pure function of the tool list: the caller owns
// GetAllTools, the notify and the SetActiveTools. upstream: src/workflow/collisions.ts.

// workflowToolName is the tool this extension registers. upstream: SUBAGENT_TOOL_NAMES.WORKFLOW.
const workflowToolName = "SubagentWorkflow"

// foreignWorkflowToolNames are the tool names that mean "another extension already orchestrates subagents": our own
// name (a duplicate registration is resolved silently, first wins) and Claude Code's bare `Workflow`. Exact names, not
// a substring: `github_workflow_run` has nothing to do with orchestration.
var foreignWorkflowToolNames = map[string]bool{workflowToolName: true, "Workflow": true}

// registeredToolInfo is the fields of a registered tool this decision reads.
type registeredToolInfo struct{ Name, Description, Source string }

// workflowCollision.Kind is "none", "report" or "standDown"; Withdraw is false when ours never reached the registry.
type workflowCollision struct {
	Kind     string
	Message  string
	Withdraw bool
}

// decideWorkflowCollision decides from the registered tools alone. ownDescription identifies our own registration
// (the one field that is certainly ours); pinned is an explicit workflowsEnabled: a default yields to evidence, a
// choice does not. upstream: decideWorkflowCollision.
func decideWorkflowCollision(tools []registeredToolInfo, ownDescription string, pinned bool) workflowCollision {
	var foreign *registeredToolInfo
	for i := range tools {
		if foreignWorkflowToolNames[tools[i].Name] && tools[i].Description != ownDescription {
			foreign = &tools[i]
			break
		}
	}
	if foreign == nil {
		return workflowCollision{Kind: "none"}
	}
	source := foreign.Source
	if source == "" {
		source = "unknown source"
	}
	tookOurName := foreign.Name == workflowToolName
	if pinned {
		if !tookOurName {
			return workflowCollision{Kind: "none"}
		}
		return workflowCollision{Kind: "report", Message: `Another extension (` + source + `) already registers a "` + workflowToolName + `" tool. ` +
			"Pi keeps the first registration, so this extension's workflow tool is not offered to the " +
			"model. Disable one of the two."}
	}
	return workflowCollision{Kind: "standDown", Message: `Another extension (` + source + `) already provides a "` + foreign.Name + `" tool, so this extension's ` +
		"workflows are disabled for this session to avoid offering the model two orchestrators. " +
		"Set `\"workflowsEnabled\": true` in .pi/subagents.json to keep both.",
		// Case 1: ours never reached the registry, so there is nothing to withdraw.
		Withdraw: !tookOurName}
}
