package pi_subagents

import (
	"fmt"
	"sort"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The port does not run everything the original runs. Whatever it does not run is refused with this one message, which
// names the original (pinned in provenance.json) as the working alternative: never a silent fallback, never a partial run.
const (
	portPackageName  = "@pi-in-go/pigpen-subagents"
	originalSpec     = "npm:pi-subagents@0.73.1"
	refusalFormatFmt = "%s (Go port) does not support %s yet. Install the original extension, %s, which runs unchanged in PiG."
)

// refusal is the text of a refused feature.
func refusal(feature string) string {
	return fmt.Sprintf(refusalFormatFmt, portPackageName, feature, originalSpec)
}

// Parameters of the subagent tool that the port honors, per mode.
var (
	runParams  = map[string]bool{"agent": true, "task": true, "agentScope": true, "cwd": true, "model": true, "async": true}
	listParams = map[string]bool{"action": true, "agentScope": true}
	getParams  = map[string]bool{"action": true, "agent": true, "agentScope": true}
)

// Inputs that start a workflow (the original's only orchestration: parallel and chained runs are workflow scripts).
var workflowParams = []string{"workflow", "workflowScript", "workflowScriptPath"}

// Legacy top-level orchestration inputs (the original itself refuses them in favor of workflowScript; the port has no workflow runtime).
var legacyOrchestrationParams = []string{"tasks", "chain", "parallel", "concurrency", "chainDir", "chainName"}

// present lists the parameters the model set (a JSON null counts as unset), sorted.
func present(params map[string]any) []string {
	var keys []string
	for k, v := range params {
		if v != nil {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

func has(params map[string]any, name string) bool { return params[name] != nil }

func quoteList(names []string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = "`" + n + "`"
	}
	return strings.Join(q, ", ")
}

func optionsFeature(names []string) string {
	if len(names) == 1 {
		return "the " + quoteList(names) + " option"
	}
	return "the " + quoteList(names) + " options"
}

// refuseUnsupported returns the refusal for the first unported input of a call to the subagent tool, or "" when the
// port runs every parameter that is set. action is the call's action ("" for an execution).
func refuseUnsupported(params map[string]any, action string) string {
	var set []string
	for _, n := range workflowParams {
		if has(params, n) {
			set = append(set, n)
		}
	}
	if len(set) > 0 {
		return refusal("workflow runs (" + quoteList(set) + ")")
	}
	for _, n := range legacyOrchestrationParams {
		if has(params, n) {
			set = append(set, n)
		}
	}
	if len(set) > 0 {
		return refusal("parallel and chain runs (" + quoteList(set) + ")")
	}
	allowed := runParams
	switch action {
	case "":
	case "list":
		allowed = listParams
	case "get":
		allowed = getParams
	default:
		return refusal("the `" + action + "` action")
	}
	if action == "" {
		if v, ok := params["async"]; ok && v != nil && v != false {
			return refusal("async (background) runs (`async: " + fmt.Sprint(v) + "`)")
		}
	}
	for _, k := range present(params) {
		if !allowed[k] {
			set = append(set, k)
		}
	}
	if len(set) > 0 {
		return refusal(optionsFeature(set))
	}
	return ""
}

// refuseTool is a tool the port declares so the model sees the original's tool list, and refuses to run.
func refuseTool(feature string) sdk.ToolFunc {
	return func(_ sdk.Context, _ map[string]any) (any, error) {
		return nil, sdk.NewToolError(refusal(feature))
	}
}

