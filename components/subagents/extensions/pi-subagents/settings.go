package pi_subagents

// The subagent settings layer (readSubagentSettings in src/agents/agents.ts):
// the `subagents` object of the project's and the user's settings.json. This
// port applies disableBuiltins, defaultModel, defaultThinking, modelScope and
// the agentOverrides fields disabled/model/thinking/machine (enough for
// disable/enable/reset, model scopes and profiles); every other key the
// original defines is refused with the port's message, never silently ignored.

import (
	"fmt"
	"sort"
)

// subagentSettings is the applied subset of one settings file's subagents
// object.
type subagentSettings struct {
	DisableBuiltins *bool
	DefaultModel    string
	DefaultThinking string
	ModelScope      *ModelScopeConfig
	AgentOverrides  map[string]map[string]any
}

// appliedOverrideFields are the agentOverrides entry fields this port applies.
var appliedOverrideFields = map[string]bool{
	"disabled": true, "model": true, "thinking": true, "machine": true,
}

// appliedSettingsFields are the subagents object keys this port applies.
var appliedSettingsFields = map[string]bool{
	"disableBuiltins": true, "defaultModel": true, "defaultThinking": true,
	"modelScope": true, "agentOverrides": true,
}

// portRefusal is the port's one refusal message (refuse.go).
func portRefusal(feature string) string {
	return "@pi-in-go/pigpen-subagents (Go port) does not support " + feature + " yet. " +
		"Install the original extension, npm:pi-subagents@0.73.1, which runs unchanged in PiG."
}

// parseSubagentSettingsFile loads and validates one settings file's subagents
// object (readSubagentSettings in the original). An absent file or absent
// subagents object yields empty settings; a malformed one yields an error text
// in the original's wording, or the port's refusal for a field it cannot run.
func parseSubagentSettingsFile(filePath string) (*subagentSettings, string) {
	settings, errText := readSettingsStrict(filePath)
	if errText != "" {
		return nil, errText
	}
	subagents, ok := settings["subagents"].(map[string]any)
	if !ok || subagents == nil {
		return &subagentSettings{}, ""
	}
	out := &subagentSettings{}
	// The original reads every known key in definition order; the port
	// validates each and refuses the ones it does not apply.
	keys := make([]string, 0, len(subagents))
	for k := range subagents {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := subagents[key]
		switch key {
		case "disableBuiltins":
			b, ok := value.(bool)
			if !ok {
				return nil, fmt.Sprintf("Subagent settings in '%s' have invalid 'disableBuiltins'; expected a boolean.", filePath)
			}
			out.DisableBuiltins = &b
		case "defaultModel":
			s, ok := value.(string)
			if !ok || jsTrim(s) == "" {
				return nil, fmt.Sprintf("Subagent settings in '%s' have invalid 'defaultModel'; expected a non-empty string.", filePath)
			}
			out.DefaultModel = jsTrim(s)
		case "defaultThinking":
			s, ok := value.(string)
			if !ok || jsTrim(s) == "" {
				return nil, fmt.Sprintf("Subagent settings in '%s' have invalid 'defaultThinking'; expected a non-empty string.", filePath)
			}
			out.DefaultThinking = jsTrim(s)
		case "modelScope":
			scope, errText := parseModelScopeConfig(value, filePath)
			if errText != "" {
				return nil, errText
			}
			out.ModelScope = scope
		case "agentOverrides":
			overrides, ok := value.(map[string]any)
			if !ok || overrides == nil {
				// The original does not validate agentOverrides' shape here beyond
				// iteration; a non-object is ignored by for..of over entries of an
				// object, and the settings layer treats it as absent.
				continue
			}
			out.AgentOverrides = map[string]map[string]any{}
			names := make([]string, 0, len(overrides))
			for name := range overrides {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				entry, ok := overrides[name].(map[string]any)
				if !ok || entry == nil {
					return nil, fmt.Sprintf("Builtin override '%s' in '%s' has invalid entry; expected an object.", name, filePath)
				}
				fields := make([]string, 0, len(entry))
				for f := range entry {
					fields = append(fields, f)
				}
				sort.Strings(fields)
				for _, f := range fields {
					if !appliedOverrideFields[f] {
						return nil, portRefusal("the settings override field '" + f + "'")
					}
				}
				if errText := validateOverrideEntry(name, filePath, entry); errText != "" {
					return nil, errText
				}
				out.AgentOverrides[name] = entry
			}
		default:
			return nil, portRefusal("the subagents settings field '" + key + "'")
		}
	}
	return out, ""
}

// validateOverrideEntry validates the applied override fields in the
// original's wording (parseBuiltinOverrideEntry).
func validateOverrideEntry(name, filePath string, entry map[string]any) string {
	if v, present := entry["disabled"]; present {
		if _, ok := v.(bool); !ok {
			return fmt.Sprintf("Builtin override '%s' in '%s' has invalid 'disabled'; expected a boolean.", name, filePath)
		}
	}
	if v, present := entry["model"]; present {
		if s, ok := v.(string); !ok || jsTrim(s) == "" {
			return fmt.Sprintf("Builtin override '%s' in '%s' has invalid 'model'; expected a non-empty string.", name, filePath)
		}
	}
	if v, present := entry["thinking"]; present {
		if s, ok := v.(string); ok {
			if jsTrim(s) == "" {
				return fmt.Sprintf("Builtin override '%s' in '%s' has invalid 'thinking'; expected a non-empty string.", name, filePath)
			}
		} else if v != false {
			return fmt.Sprintf("Builtin override '%s' in '%s' has invalid 'thinking'; expected a string or false.", name, filePath)
		}
	}
	if v, present := entry["machine"]; present {
		if s, ok := v.(string); ok {
			if jsTrim(s) == "" {
				return fmt.Sprintf("Builtin override '%s' in '%s' has invalid 'machine'; expected a non-empty string.", name, filePath)
			}
		} else if v != false {
			return fmt.Sprintf("Builtin override '%s' in '%s' has invalid 'machine'; expected a non-empty string or false.", name, filePath)
		}
	}
	return ""
}

// winningModelScope is the launch-time modelScope: the project's settings win
// over the user's (projectSettings.modelScope ?? userSettings.modelScope). A
// settings error is reported through onErr; enforcement treats a broken file
// as no scope, as a launch that cannot read its settings has no policy.
func winningModelScope(cwd string, onErr func(string)) *ModelScopeConfig {
	if path := projectAgentSettingsPath(cwd); path != "" {
		if settings, errText := parseSubagentSettingsFile(path); errText != "" {
			if onErr != nil {
				onErr(errText)
			}
		} else if settings.ModelScope != nil {
			return settings.ModelScope
		}
	}
	if settings, errText := parseSubagentSettingsFile(userAgentSettingsPath()); errText != "" {
		if onErr != nil {
			onErr(errText)
		}
		return nil
	} else {
		return settings.ModelScope
	}
	return nil
}
