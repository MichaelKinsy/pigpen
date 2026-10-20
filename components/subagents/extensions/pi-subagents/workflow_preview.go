package pi_subagents

import (
	"regexp"
)

// Display helpers of the workflow runtime (scripted-workflow.ts:1474-1517).

// FormatWorkflowJSONPreview is formatWorkflowJsonPreview: the JSON text of a value cut to maxLength UTF-16 units (a cut ends
// in an ellipsis), or false when the value is not JSON or has no JSON text (undefined).
func FormatWorkflowJSONPreview(value any, maxLength int) (string, bool) { panic("not implemented") }

// WorkflowRunPreview is the agent and task a simple `return runs.run(...)` script names, when they are string literals.
type WorkflowRunPreview struct {
	Agent *string
	Task  *string
}

var workflowSimpleRunRe = regexp.MustCompile(`^\s*return\s+(?:await\s+)?runs\.run\s*\(\s*(?:"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|` + "`[^`$\\\\]*`" + `)\s*,\s*\{([\s\S]*)\}\s*\)\s*;?\s*$`)

func workflowReadSimpleProperty(body, name string) *string { panic("not implemented") }

// PreviewSimpleWorkflowRun is previewSimpleWorkflowRun: a display-only preview for the exact simple `return runs.run(key, {...})` form.
func PreviewSimpleWorkflowRun(script string) (WorkflowRunPreview, bool) { panic("not implemented") }
