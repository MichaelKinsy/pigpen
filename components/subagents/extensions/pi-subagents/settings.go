package pi_subagents

// The subagent settings layer (readSubagentSettings in src/agents/agents.ts):
// the `subagents` object of the project's and the user's settings.json. This
// port applies disableBuiltins, defaultExtensions, defaultSubagentOnlyExtensions,
// defaultModel, defaultThinking, modelScope and the agentOverrides fields
// disabled/model/thinking/machine (enough for disable/enable/reset, model
// scopes, profiles and the child's extensions); every other subagents key the
// original defines is refused with the port's message, never silently
// ignored. An agentOverrides entry field the port does not apply is left
// unread, as in the original's per-field reads.

import (
	"errors"
)

// subagentSettings is the applied subset of one settings file's subagents
// object: the original's launch fields and this port's additions.
type subagentSettings struct {
	extensions, subagentOnly      []string
	defaultModel, defaultThinking string
	model, thinking               map[string]any
	disableBuiltins               *bool
	modelScope                    *ModelScopeConfig
	agentOverrides                map[string]map[string]any
}

// appliedSettingsFields are the subagents object keys this port applies.
var appliedSettingsFields = map[string]bool{
	"disableBuiltins": true, "defaultExtensions": true, "defaultSubagentOnlyExtensions": true,
	"defaultModel": true, "defaultThinking": true, "modelScope": true, "agentOverrides": true,
}

// portRefusal is the port's one refusal message (refuse.go).
func portRefusal(feature string) string {
	return "@pi-in-go/pigpen-subagents (Go port) does not support " + feature + " yet. " +
		"Install the original extension, npm:pi-subagents@0.73.1, which runs unchanged in PiG."
}

// parseSubagentSettingsFile loads and validates one settings file's subagents
// object (readSubagentSettings in the original). An absent file or absent
// subagents object yields empty settings; a malformed one yields an error text
// in the original's wording, or the port's refusal for a field it does not
// apply. The per-file shape lets discovery report a diagnostic per file while
// the run, chain and get paths refuse with the whole message.
func parseSubagentSettingsFile(filePath string) (*subagentSettings, string) {
	settings, err := readSubagentSettings(filePath)
	if err != nil {
		return nil, err.Error()
	}
	return &settings, ""
}

// validateOverrideEntry validates the applied override fields in the
// original's wording (parseBuiltinOverrideEntry). A field the port does not
// apply is ignored, as the original leaves it to its own reads.
func validateOverrideEntry(name, filePath string, entry map[string]any) string {
	if v, present := entry["disabled"]; present {
		if _, ok := v.(bool); !ok {
			return "Builtin override '" + name + "' in '" + filePath + "' has invalid 'disabled'; expected a boolean."
		}
	}
	if v, present := entry["model"]; present {
		if s, ok := v.(string); ok {
			if jsTrim(s) == "" {
				return "Builtin override '" + name + "' in '" + filePath + "' has invalid 'model'; expected a string or false."
			}
		} else if v != false {
			return "Builtin override '" + name + "' in '" + filePath + "' has invalid 'model'; expected a string or false."
		}
	}
	if v, present := entry["thinking"]; present {
		if s, ok := v.(string); ok {
			if jsTrim(s) == "" {
				return "Builtin override '" + name + "' in '" + filePath + "' has invalid 'thinking'; expected a string or false."
			}
		} else if v != false {
			return "Builtin override '" + name + "' in '" + filePath + "' has invalid 'thinking'; expected a string or false."
		}
	}
	if v, present := entry["machine"]; present {
		if s, ok := v.(string); ok {
			if jsTrim(s) == "" {
				return "Builtin override '" + name + "' in '" + filePath + "' has invalid 'machine'; expected a non-empty string or false."
			}
		} else if v != false {
			return "Builtin override '" + name + "' in '" + filePath + "' has invalid 'machine'; expected a non-empty string or false."
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
		} else if settings.modelScope != nil {
			return settings.modelScope
		}
	}
	if settings, errText := parseSubagentSettingsFile(userAgentSettingsPath()); errText != "" {
		if onErr != nil {
			onErr(errText)
		}
		return nil
	} else {
		return settings.modelScope
	}
}

// refuseSubagentSettingsField is the refusal for a subagents key the port does
// not apply, shaped as the read error its callers expect.
func refuseSubagentSettingsField(key string) error {
	return errors.New(portRefusal("the subagents settings field '" + key + "'"))
}
