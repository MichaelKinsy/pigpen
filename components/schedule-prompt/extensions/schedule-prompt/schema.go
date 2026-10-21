package schedule_prompt

import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

// The tool name, description and parameters of the original, byte for byte.
// upstream: src/tool.ts:22-27 (name, label, description), src/types.ts:75-141 (the TypeBox parameters).
const (
	toolName  = "schedule_prompt"
	toolLabel = "Schedule Prompt"
	toolDesc  = "IMPORTANT: For action='add', you MUST provide both 'schedule' parameter AND 'prompt' parameter. Schedule prompts at times/intervals. Schedule formats: 6-field cron (with seconds: '0 * * * * *' = every minute), ISO timestamp, relative time (+10s, +5m, +1h), or interval (5m, 1h). Type defaults to 'cron', use 'once' for relative/ISO times. Actions: add (needs schedule+prompt), list, remove/enable/disable/update (need jobId), cleanup."
)

func stringEnum(desc string, values ...string) map[string]any {
	vals := make([]any, len(values))
	for i, v := range values {
		vals[i] = v
	}
	return map[string]any{"type": "string", "enum": vals, "description": desc}
}

func stringProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

// toolParameters is CronToolParams: `action` is required, every other member optional.
func toolParameters() sdk.Schema {
	return sdk.Schema{
		"type": "object",
		"properties": map[string]any{
			"action":      stringEnum("Action to perform", "add", "remove", "list", "enable", "disable", "update", "cleanup"),
			"name":        stringProp("Job name, auto-generated if omitted"),
			"schedule":    stringProp("Required for add. Cron expression, ISO timestamp, relative time (+10s, +5m), or interval string"),
			"prompt":      stringProp("Required for add. The prompt text to execute"),
			"jobId":       stringProp("Job ID for remove, enable, disable, or update actions"),
			"type":        stringEnum("Job type. Use 'once' for relative times like '+10s'. Default is cron", "cron", "once", "interval"),
			"description": stringProp("Optional job description"),
			"model": map[string]any{
				"type":      "string",
				"minLength": 1,
				"description": "Optional. If set, runs the prompt in a separate in-process agent session using this model (e.g. 'haiku', 'sonnet', or 'provider/model-id'). " +
					"If omitted, the prompt is injected into the current chat. Must be a non-empty string — to switch a job from subagent back to inline mode, remove the job and re-add it without a model.",
			},
			"notify": map[string]any{
				"type": "boolean",
				"description": "Subagent jobs only. If true, the parent agent is nudged to react to the subagent's result. Default false: the result is shown in chat but the parent is not interrupted. " +
					"Ignored for inline (no-model) jobs, where the prompt itself already wakes the parent. Recommended only for low-frequency jobs.",
			},
			"extensions": map[string]any{"anyOf": []any{
				map[string]any{"type": "boolean", "description": "If true, loads all registered extensions."},
				map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "List of extension package names to load."},
			}},
			"skills": map[string]any{"anyOf": []any{
				map[string]any{"type": "boolean", "description": "If true, loads all skills."},
				map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "List of skill names to load."},
			}},
		},
		"required": []any{"action"},
	}
}
