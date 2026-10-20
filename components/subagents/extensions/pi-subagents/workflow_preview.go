package pi_subagents

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Display helpers of the workflow runtime (scripted-workflow.ts:1474-1517).

// FormatWorkflowJSONPreview is formatWorkflowJsonPreview: the JSON text of a value cut to maxLength UTF-16 units (a cut ends
// in an ellipsis), or false when the value is not JSON or has no JSON text (undefined).
func FormatWorkflowJSONPreview(value any, maxLength int) (string, bool) {
	if err := assertWorkflowJSONValue(value, "value", map[any]bool{}); err != nil {
		return "", false
	}
	if _, u := value.(undef); u {
		return "", false
	}
	serialized := marshalJSON(value, "")
	if jsLen(serialized) > maxLength {
		return string(utf16Slice(serialized, maxLength)) + "\u2026", true
	}
	return serialized, true
}

// WorkflowRunPreview is the agent and task a simple `return runs.run(...)` script names, when they are string literals.
type WorkflowRunPreview struct {
	Agent *string
	Task  *string
}

var workflowSimpleRunRe = regexp.MustCompile(`^\s*return\s+(?:await\s+)?runs\.run\s*\(\s*(?:"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|` + "`[^`$\\\\]*`" + `)\s*,\s*\{([\s\S]*)\}\s*\)\s*;?\s*$`)

func workflowReadSimpleProperty(body, name string) *string {
	re := regexp.MustCompile(`(?:^|,)\s*(?:` + name + `|["']` + name + `["'])\s*:\s*("(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|` + "`[^`$\\\\]*`" + `)`)
	m := re.FindStringSubmatch(body)
	if m == nil || m[1] == "" {
		return nil
	}
	literal := m[1]
	if strings.HasPrefix(literal, `"`) {
		var s string
		if err := json.Unmarshal([]byte(literal), &s); err != nil {
			return nil
		}
		return &s
	}
	inner := literal[1 : len(literal)-1]
	if strings.Contains(inner, `\`) {
		return nil
	}
	return &inner
}

// PreviewSimpleWorkflowRun is previewSimpleWorkflowRun: a display-only preview for the exact simple `return runs.run(key, {...})` form.
func PreviewSimpleWorkflowRun(script string) (WorkflowRunPreview, bool) {
	m := workflowSimpleRunRe.FindStringSubmatch(script)
	if m == nil {
		return WorkflowRunPreview{}, false
	}
	return WorkflowRunPreview{Agent: workflowReadSimpleProperty(m[1], "agent"), Task: workflowReadSimpleProperty(m[1], "task")}, true
}
