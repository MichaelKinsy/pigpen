package pi_goal_x

import (
	"encoding/json"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	pitui "github.com/MichaelKinsy/pigpen/components/pitui"
)

// The tools (goal-tools.ts): the core tools (goal_core_tools.go), the two task tools (goal_task_tools.go) and the shared completion
// flow (goal_completion.go). The drafting tools are registered by the commands slice (goal_drafting.go).

// registerGoalTools is registerGoalTools(core) of goal-tools.ts: the three core tools and the two task tools.
func registerGoalTools(c *core) {
	registerCoreTools(c)
	registerTaskTools(c)
	registerDraftingTools(c)
}

// goalToolExec is the body of one tool: it runs under the core's lock with the call's context bound as the core's host, and a
// returned error is the original's thrown Error.
type goalToolExec func(c *core, params map[string]any) (goalAgentResult, error)

// toolFunc binds an extension tool handler to the core: the handler runs under the core's lock with the call's context.
func (p *goalPi) toolFunc(fn func(c *core, params map[string]any) (any, error)) sdk.ToolFunc {
	return func(ctx sdk.Context, params map[string]any) (any, error) {
		var out any
		var err error
		p.c.withCtx(ctx, func() { out, err = fn(p.c, params) })
		return out, err
	}
}

// execute wraps a goalToolExec as the tool's Execute.
func (p *goalPi) execute(fn goalToolExec) sdk.ToolFunc {
	return p.toolFunc(func(c *core, params map[string]any) (any, error) {
		if params == nil {
			params = map[string]any{}
		}
		r, err := fn(c, params)
		if err != nil {
			return nil, err
		}
		return toolResult(r), nil
	})
}

// toolResult is the AgentToolResult of a goalAgentResult: one text block, the details and the terminate flag.
func toolResult(r goalAgentResult) sdk.ToolResult {
	out := sdk.ToolResult{Content: r.text, Terminate: r.terminate}
	if r.details != nil {
		out.Details = r.details
	}
	return out
}

// textResult is `{ content: [{ type: "text", text }], details: goalDetails(goal) }`.
func (c *core) textResult(text string) goalAgentResult {
	return goalAgentResult{text: text, details: goalDetails(c.state(), "")}
}

// ---- parameter decoding ----

func toolParamString(params map[string]any, key string) (string, bool) {
	s, ok := params[key].(string)
	return s, ok
}

// toolParamPresent is `params[key] !== undefined`.
func toolParamPresent(params map[string]any, key string) bool {
	_, ok := params[key]
	return ok
}

func toolParamBool(params map[string]any, key string) bool {
	b, _ := params[key].(bool)
	return b
}

// toolItemField reads a field of a decoded-JSON object or an ordered object.
func toolItemField(item any, key string) (any, bool) {
	switch o := item.(type) {
	case map[string]any:
		v, ok := o[key]
		return v, ok
	case *jsObject:
		v, ok := o.get(key)
		if _, u := v.(undef); u {
			return nil, false
		}
		return v, ok
	}
	return nil, false
}

func toolItemString(item any, key string) (string, bool) {
	v, _ := toolItemField(item, key)
	s, ok := v.(string)
	return s, ok
}

// optionalString is `value` for a nonempty string and undefined otherwise (`value || undefined`).
func optionalString(s string) any {
	if s == "" {
		return undef{}
	}
	return s
}

// ---- schemas ----

type schemaMap = map[string]any

func schemaObject(props schemaMap, required []string) sdk.Schema {
	s := sdk.Schema{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func schemaString(description string) schemaMap {
	s := schemaMap{"type": "string"}
	if description != "" {
		s["description"] = description
	}
	return s
}

func schemaBool(description string) schemaMap {
	s := schemaMap{"type": "boolean"}
	if description != "" {
		s["description"] = description
	}
	return s
}

func schemaEnum(values ...string) schemaMap {
	return schemaMap{"type": "string", "enum": values}
}

// ---- renderers ----

// toolRenderCall is `new Text(theme.fg("toolTitle", title) + theme.fg("muted", detail), 0, 0)` as lines.
func toolRenderCall(title, detail string) sdk.ToolRenderCallFunc {
	return func(ctx sdk.Context, args map[string]any, _ sdk.ToolRenderContext, width int) ([]string, error) {
		return toolRenderCallLines(sdkTheme{ctx.UITheme()}, title, detail, args, width), nil
	}
}

func toolRenderCallLines(theme goalTheme, title, detail string, args map[string]any, width int) []string {
	return pitui.NewText(theme.fg("toolTitle", title)+theme.fg("muted", detail), 0, 0, nil).Render(width)
}

// toolRenderResult is renderGoalResult as lines.
func toolRenderResult() sdk.ToolRenderResultFunc {
	return func(ctx sdk.Context, result sdk.ToolRenderResult, options sdk.ToolRenderResultOptions, _ sdk.ToolRenderContext, width int) ([]string, error) {
		return toolRenderResultLines(sdkTheme{ctx.UITheme()}, result, options.Expanded, width), nil
	}
}

func toolRenderResultLines(theme goalTheme, result sdk.ToolRenderResult, expanded bool, width int) []string {
	r := goalToolResult{}
	for _, block := range result.Content {
		item := goalToolContent{}
		item.typ, _ = block["type"].(string)
		item.text, item.hasText = block["text"].(string)
		r.content = append(r.content, item)
	}
	if result.Details != nil {
		raw, err := json.Marshal(result.Details)
		if err == nil {
			if v, err := parseJSON(raw); err == nil {
				r.details, _ = v.(*jsObject)
			}
		}
	}
	return renderGoalResult(r, expanded, theme).Render(width)
}
