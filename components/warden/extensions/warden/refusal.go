package warden

import (
	"fmt"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// originalSpec is the unmodified TypeScript extension this port is held against (port/oracle/package.json).
const originalSpec = "npm:pi-warden@0.80.0"

// refusal is the one message for every upstream feature this port does not carry yet.
func refusal(feature string) string {
	return fmt.Sprintf("pi-warden (Go port) does not support %s yet. Install the original extension, %s, which runs unchanged in PiG.", feature, originalSpec)
}

// unportedAction maps the first words of /warden args to the upstream action that is not ported, or "".
// Upstream's list: status, rules, report, enable, disable, mode, config, test, trace, init, audit, index, prefs,
// loops, recommend, unmute, bench (extension.ts), plus the two-word rules check, audit, calibrate and tune.
func unportedAction(sub string, rest []string) string {
	switch sub {
	case "rules":
		if len(rest) > 0 {
			switch strings.ToLower(rest[0]) {
			case "check", "audit", "calibrate", "tune":
				return "/warden rules " + strings.ToLower(rest[0])
			}
		}
		return "/warden rules"
	case "report", "config", "init", "audit", "index", "prefs", "loops", "recommend", "unmute", "bench":
		return "/warden " + sub
	}
	return ""
}

func refuseTool(name string) sdk.ToolFunc {
	return func(sdk.Context, map[string]any) (any, error) {
		return nil, fmt.Errorf("%s", refusal("the "+name+" tool"))
	}
}

func registerRefusedTools(e *sdk.Extension) {
	obj := func(props map[string]any, required ...string) sdk.Schema {
		s := sdk.Schema{"type": "object", "properties": props, "additionalProperties": false}
		if required == nil {
			required = []string{}
		}
		s["required"] = required
		return s
	}
	e.Tool("warden_remember", "Record a standing lesson for this project, so later sessions keep it. Not available in this Go port: the call is refused with the original's install instructions.",
		obj(map[string]any{"lesson": map[string]any{"type": "string", "maxLength": 160, "description": "One standing instruction for this project, at most 160 characters, e.g. \"Never X\" or \"Always Y\"."}}, "lesson"),
		refuseTool("warden_remember"))
	e.Tool("warden_loops", "Keep open loops for this session. Not available in this Go port: the call is refused with the original's install instructions.",
		obj(map[string]any{
			"action": map[string]any{"type": "string", "enum": []string{"add", "done", "drop", "list"}, "description": "add, done, drop, or list."},
			"text":   map[string]any{"type": "string", "maxLength": 160, "description": "For add: what you promised to do."},
			"when":   map[string]any{"type": "string", "maxLength": 160, "description": "For add, optional: the condition that makes it due."},
			"id":     map[string]any{"type": "integer", "minimum": 1, "description": "For done and drop: the loop number."},
			"reason": map[string]any{"type": "string", "maxLength": 160, "description": "For drop: why it is no longer needed."},
		}, "action"),
		refuseTool("warden_loops"))
	e.Tool("warden_recall", "Recall what was already tried in this session. Not available in this Go port: the call is refused with the original's install instructions.",
		obj(map[string]any{}), refuseTool("warden_recall"))
}
