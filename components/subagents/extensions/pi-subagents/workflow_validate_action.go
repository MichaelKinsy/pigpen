package pi_subagents

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The `validate` action (public-execution.ts, subagent-executor.ts:5266-5287): a workflowScript or workflowScriptPath checked
// statically, without launching a child. The answer is the JSON of the validation, `{ ok, errors, warnings?, preflight? }`, and
// an error result when it is not ok.

const defaultMaxSubagentSpawnsPerRun = 64 // DEFAULT_MAX_SUBAGENT_SPAWNS_PER_RUN

func init() {
	registerAction("validate", []string{"workflowScript", "workflowScriptPath", "args", "preflight", "maxSubagentSpawnsPerRun", "globalConcurrencyLimit", "cwd", "agentScope", "agent", "task", "step"}, validateAction)
}

func validationFailure(message string) (any, error) {
	return nil, sdk.NewToolError(message)
}

func positiveSafeInt(v any) (float64, bool) {
	f, ok := v.(float64)
	return f, ok && f == math.Trunc(f) && f > 0 && f <= 9007199254740991
}

// configMaxSpawns reads maxSubagentSpawnsPerRun of the extension config.
func configMaxSpawns() any {
	data, err := os.ReadFile(asyncConfigPath())
	if err != nil {
		return nil
	}
	v, err := parseJSON(data)
	if o, ok := v.(*jsObject); ok && err == nil {
		return o.vals["maxSubagentSpawnsPerRun"]
	}
	return nil
}

// loadWorkflowScriptPath is loadWorkflowScriptPath (subagent-executor.ts:547): the script file, read from the request's cwd.
func loadWorkflowScriptPath(runtimeCwd string, params map[string]any) (script string, errText string) {
	cwd := runtimeCwd
	if c, ok := params["cwd"].(string); ok && c != "" {
		cwd = c
		if !filepath.IsAbs(c) {
			cwd = filepath.Join(runtimeCwd, c)
		}
	}
	p, _ := params["workflowScriptPath"].(string)
	path := p
	if !filepath.IsAbs(p) {
		path = filepath.Join(cwd, p)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Sprintf("Failed to read workflowScriptPath '%s': %s", path, nodeFSErrorText(err, "open", path))
	}
	if jsTrim(string(data)) == "" {
		return "", fmt.Sprintf("workflowScriptPath file '%s' is empty.", path)
	}
	return string(data), ""
}

// workflowAgentNameError is the launch-time resolution of a literal agent name, as the validator asks for it.
func workflowAgentNameError(agents []AgentConfig) func(name string) string {
	return func(name string) string {
		if a, msg := ResolveAgentName(name, agents); a != nil || msg != "" {
			return msg
		}
		requested := strings.ToLower(jsTrim(name))
		seen := map[string]bool{}
		var candidates []string
		for _, a := range agents {
			for _, c := range append([]string{a.Name, a.LocalName}, a.Aliases...) {
				if c != "" && !seen[c] {
					seen[c] = true
					candidates = append(candidates, c)
				}
			}
		}
		type scored struct {
			name string
			dist int
		}
		var close []scored
		for _, c := range candidates {
			d := editDistance(requested, strings.ToLower(c))
			if d <= max(1, len(jsUTF16(c))/4) || hasSingleAdjacentTransposition(requested, strings.ToLower(c)) {
				close = append(close, scored{c, d})
			}
		}
		sort.SliceStable(close, func(i, j int) bool {
			if close[i].dist != close[j].dist {
				return close[i].dist < close[j].dist
			}
			return localeCompare(close[i].name, close[j].name) < 0
		})
		suggestion := ""
		if len(close) > 0 {
			suggestion = " Did you mean '" + close[0].name + "'?"
		}
		return fmt.Sprintf("Unknown agent '%s'.%s Use subagent({ action: \"list\" }) to inspect agents.", name, suggestion)
	}
}

func validationObject(v WorkflowScriptValidationResult, preflight *WorkflowPreflight, extraError string) *jsObject {
	errs := make([]any, 0, len(v.Errors)+1)
	for _, e := range v.Errors {
		o := newObject()
		if e.Kind != "" {
			o.set("kind", e.Kind)
		}
		o.set("message", e.Message)
		if e.Line != 0 {
			o.set("line", float64(e.Line))
		}
		if e.Column != 0 {
			o.set("column", float64(e.Column))
		}
		errs = append(errs, o)
	}
	ok := v.OK
	if extraError != "" {
		errs = append(errs, jo("message", extraError))
		ok = false
	}
	out := jo("ok", ok, "errors", errs)
	if len(v.Warnings) > 0 {
		ws := make([]any, len(v.Warnings))
		for i, w := range v.Warnings {
			ws[i] = jo("kind", w.Kind, "message", w.Message)
		}
		out.set("warnings", ws)
	}
	if preflight != nil {
		b, _ := jsonMarshalNoEscape(preflight)
		if pv, err := parseJSON(b); err == nil {
			out.set("preflight", pv)
		}
	}
	return out
}

func validateAction(ctx sdk.Context, params map[string]any) (any, error) {
	isSet := func(k string) bool { return params[k] != nil }
	if isSet("workflowScript") && isSet("workflowScriptPath") {
		return validationFailure("workflowScript and workflowScriptPath are mutually exclusive.")
	}
	if v := params["globalConcurrencyLimit"]; v != nil {
		if _, ok := positiveSafeInt(v); !ok {
			return validationFailure("globalConcurrencyLimit must be a positive safe integer.")
		}
	}
	if v := params["maxSubagentSpawnsPerRun"]; v != nil {
		if _, ok := positiveSafeInt(v); !ok {
			return validationFailure("maxSubagentSpawnsPerRun must be a positive safe integer.")
		}
	}
	if isSet("globalConcurrencyLimit") {
		return validationFailure("Workflow capacity overrides are only supported on top-level workflowScript or workflowScriptPath calls; validate accepts maxSubagentSpawnsPerRun for static budget checks.")
	}
	script, hasScript := params["workflowScript"].(string)
	path, hasPath := params["workflowScriptPath"].(string)
	if isSet("agent") || isSet("task") || isSet("step") {
		return validationFailure("validate requires workflowScript or workflowScriptPath and does not accept direct agent, task, or step execution fields.")
	}
	if !(hasScript && jsTrim(script) != "") && !(hasPath && jsTrim(path) != "") {
		return validationFailure("validate requires a non-empty workflowScript or workflowScriptPath.")
	}
	if isSet("args") {
		v, _ := parseJSON([]byte(marshalAny(params["args"])))
		if _, err := NormalizeWorkflowArgs(v); err != nil {
			return validationFailure(err.Error())
		}
	}
	if hasPath && !hasScript {
		loaded, errText := loadWorkflowScriptPath(ctx.Cwd(), params)
		if errText != "" {
			return validationFailure(errText)
		}
		script = loaded
	}
	cwd := ctx.Cwd()
	if c, ok := params["cwd"].(string); ok && c != "" {
		cwd = c
		if !filepath.IsAbs(c) {
			cwd = filepath.Join(ctx.Cwd(), c)
		}
	}
	scope := ResolveExecutionAgentScope(params["agentScope"])
	agents := DiscoverAgents(cwd, scope).Agents
	budget := resolveMaxSubagentSpawnsPerRun(configMaxSpawns(), osEnv)
	if f, ok := positiveSafeInt(params["maxSubagentSpawnsPerRun"]); ok {
		budget = int(f)
	}
	validation := ValidateWorkflowScript(script, WorkflowScriptValidationOptions{MaxSubagentSpawnsPerRun: &budget, AgentNameError: workflowAgentNameError(agents)})
	var preflight *WorkflowPreflight
	extra := ""
	if isSet("preflight") {
		v, _ := parseJSON([]byte(marshalAny(params["preflight"])))
		p, err := NormalizeWorkflowPreflight(v)
		if err != nil {
			extra = err.Error()
		}
		preflight = p
	}
	text := marshalJSON(validationObject(validation, preflight, extra), "")
	if !validation.OK || extra != "" {
		return nil, sdk.NewToolError(text)
	}
	return sdk.ToolResult{Content: text, Details: map[string]any{"mode": "management", "results": []any{}}}, nil
}

func marshalAny(v any) string {
	b, err := jsonMarshalNoEscape(v)
	if err != nil {
		return "null"
	}
	return string(b)
}
