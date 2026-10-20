package tintinweb_subagents

import (
	_ "embed"
	"strings"
)

// The model-facing description of the SubagentWorkflow tool: a deliberate port of Claude Code's Workflow tool
// description, extracted from the original's template literal (src/workflow/tool-description.ts
// fullWorkflowToolDescription) by port/gen-workflow-text.mjs. {{typeList}} is the live agent roster, substituted by
// renderWorkflowToolDescription.
//
//go:embed workflow_tool_description.txt
var fullWorkflowToolDescription string

// renderWorkflowToolDescription is the description with the live roster in it. upstream: index.ts
// renderToolDescriptionTemplate (the only placeholder this text has is typeList).
func renderWorkflowToolDescription(reg *agentRegistry) string {
	return strings.ReplaceAll(fullWorkflowToolDescription, "{{typeList}}", buildTypeListText(reg))
}
