package pi_subagents

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Settings overrides for builtin and package agents
// (`settings.subagents.agentOverrides.<name>`, src/agents/agents.ts): a
// project's .pi/settings.json wins over the user's <agent dir>/settings.json.
// This port applies the `disabled` field (enough for disable/enable/reset and
// for hiding a definition from list and runtime discovery); the original's
// other override fields are part of the settings-override slice.

// agentSettingsOverride is one agentOverrides entry.
type agentSettingsOverride struct {
	Disabled    *bool
	Scope       string // "user" or "project"
	Path        string
	HasDisabled bool
}

// userAgentSettingsPath is the original's getUserAgentSettingsPath.
func userAgentSettingsPath() string { return filepath.Join(agentDir(), "settings.json") }

// projectAgentSettingsPath is the original's getProjectAgentSettingsPath ("" without a project root).
func projectAgentSettingsPath(cwd string) string {
	root := FindConfiguredProjectRoot(cwd)
	if root == "" {
		return ""
	}
	return filepath.Join(projectConfigDir(root), "settings.json")
}

// readSettingsStrict loads a settings file as the original's readSettingsFileStrict: absent is {}, unreadable is an error.
func readSettingsStrict(filePath string) (map[string]any, string) {
	b, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, ""
		}
		return nil, err.Error()
	}
	var settings map[string]any
	if err := json.Unmarshal(b, &settings); err != nil {
		return nil, fmt.Sprintf("Failed to parse settings file '%s': %s", filePath, err.Error())
	}
	if settings == nil {
		settings = map[string]any{}
	}
	return settings, ""
}

// writeSettingsFile writes the settings file the original's way (two-space JSON plus newline).
func writeSettingsFile(filePath string, settings map[string]any) string {
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err.Error()
	}
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return err.Error()
	}
	if err := os.WriteFile(filePath, append(data, '\n'), 0o644); err != nil {
		return err.Error()
	}
	return ""
}

// agentOverridesOf reads settings.subagents.agentOverrides from one settings file.
func agentOverridesOf(settings map[string]any) map[string]any {
	subagents, _ := settings["subagents"].(map[string]any)
	overrides, _ := subagents["agentOverrides"].(map[string]any)
	return overrides
}

// settingsOverrideFor finds the agent's override in one scope, or nil.
func settingsOverrideFor(filePath, name string) map[string]any {
	if filePath == "" {
		return nil
	}
	settings, errText := readSettingsStrict(filePath)
	if errText != "" || settings == nil {
		return nil
	}
	overrides := agentOverridesOf(settings)
	entry, _ := overrides[name].(map[string]any)
	return entry
}

// mergeBuiltinAgentOverride merges fields into the agent's override (the original's mergeBuiltinAgentOverride).
func mergeBuiltinAgentOverride(cwd, name, scope string, fields map[string]any) (string, string) {
	filePath := userAgentSettingsPath()
	if scope == "project" {
		filePath = projectAgentSettingsPath(cwd)
		if filePath == "" {
			return "", "Project override is not available here. No project config root was found."
		}
	}
	settings, errText := readSettingsStrict(filePath)
	if errText != "" {
		return "", errText
	}
	subagents, _ := settings["subagents"].(map[string]any)
	if subagents == nil {
		subagents = map[string]any{}
	}
	overrides, _ := subagents["agentOverrides"].(map[string]any)
	if overrides == nil {
		overrides = map[string]any{}
	}
	base, _ := overrides[name].(map[string]any)
	if base == nil {
		base = map[string]any{}
	}
	for k, v := range fields {
		base[k] = v
	}
	overrides[name] = base
	subagents["agentOverrides"] = overrides
	settings["subagents"] = subagents
	if msg := writeSettingsFile(filePath, settings); msg != "" {
		return "", msg
	}
	return filePath, ""
}

// removeBuiltinAgentOverrideFields removes fields from the agent's override (the original's).
func removeBuiltinAgentOverrideFields(cwd, name, scope string, fields []string) (path string, removed bool, errText string) {
	filePath := userAgentSettingsPath()
	if scope == "project" {
		filePath = projectAgentSettingsPath(cwd)
		if filePath == "" {
			return "", false, "Project override is not available here. No project config root was found."
		}
	}
	if _, statErr := os.Stat(filePath); statErr != nil {
		return filePath, false, ""
	}
	settings, errText := readSettingsStrict(filePath)
	if errText != "" {
		return filePath, false, errText
	}
	subagents, _ := settings["subagents"].(map[string]any)
	overrides := agentOverridesOf(settings)
	entry, _ := overrides[name].(map[string]any)
	if subagents == nil || entry == nil {
		return filePath, false, ""
	}
	didRemove := false
	for _, f := range fields {
		if _, present := entry[f]; present {
			delete(entry, f)
			didRemove = true
		}
	}
	if !didRemove {
		return filePath, false, ""
	}
	if len(entry) > 0 {
		overrides[name] = entry
	} else {
		delete(overrides, name)
	}
	if len(overrides) > 0 {
		subagents["agentOverrides"] = overrides
	} else {
		delete(subagents, "agentOverrides")
	}
	if len(subagents) > 0 {
		settings["subagents"] = subagents
	} else {
		delete(settings, "subagents")
	}
	if msg := writeSettingsFile(filePath, settings); msg != "" {
		return filePath, false, msg
	}
	return filePath, true, ""
}

// removeBuiltinAgentOverride removes the whole override entry, keeping its
// machine field when preserveMachine is set (the original's).
func removeBuiltinAgentOverride(cwd, name, scope string, preserveMachine bool) (path string, removed bool, machinePreserved bool, errText string) {
	filePath := userAgentSettingsPath()
	if scope == "project" {
		filePath = projectAgentSettingsPath(cwd)
		if filePath == "" {
			return "", false, false, "Project override is not available here. No project config root was found."
		}
	}
	if _, statErr := os.Stat(filePath); statErr != nil {
		return filePath, false, false, ""
	}
	settings, errText := readSettingsStrict(filePath)
	if errText != "" {
		return filePath, false, false, errText
	}
	subagents, _ := settings["subagents"].(map[string]any)
	overrides := agentOverridesOf(settings)
	entry, _ := overrides[name].(map[string]any)
	if subagents == nil || entry == nil {
		return filePath, false, false, ""
	}
	machine, hasMachine := entry["machine"].(string)
	if hasMachine && jsTrim(machine) == "" {
		hasMachine = false
	}
	if preserveMachine && hasMachine {
		overrides[name] = map[string]any{"machine": machine}
		machinePreserved = true
	} else {
		delete(overrides, name)
	}
	if len(overrides) > 0 {
		subagents["agentOverrides"] = overrides
	} else {
		delete(subagents, "agentOverrides")
	}
	if len(subagents) > 0 {
		settings["subagents"] = subagents
	} else {
		delete(settings, "subagents")
	}
	if msg := writeSettingsFile(filePath, settings); msg != "" {
		return filePath, false, false, msg
	}
	return filePath, true, machinePreserved, ""
}

// applySettingsOverrides applies the settings layer (settings.go) to the
// discovered agents: the project's settings win over the user's per agent and
// per field. A settings error becomes a diagnostic, as a broken settings file
// must not silently apply nothing where the original would have thrown.
func applySettingsOverrides(d *DiscoveryAll, cwd string) {
	userPath := userAgentSettingsPath()
	projectPath := projectAgentSettingsPath(cwd)
	var userSettings, projectSettings *subagentSettings
	if settings, errText := parseSubagentSettingsFile(userPath); errText != "" {
		d.Diagnostics = append(d.Diagnostics, Diagnostic{Error: errText})
	} else {
		userSettings = settings
	}
	if projectPath != "" {
		if settings, errText := parseSubagentSettingsFile(projectPath); errText != "" {
			d.Diagnostics = append(d.Diagnostics, Diagnostic{Error: errText})
		} else {
			projectSettings = settings
		}
	}
	if projectSettings != nil && projectSettings.ModelScope != nil {
		d.ModelScope = projectSettings.ModelScope
	} else if userSettings != nil {
		d.ModelScope = userSettings.ModelScope
	}
	disableBuiltins := (userSettings != nil && userSettings.DisableBuiltins != nil && *userSettings.DisableBuiltins) ||
		(projectSettings != nil && projectSettings.DisableBuiltins != nil && *projectSettings.DisableBuiltins)
	if disableBuiltins {
		for i := range d.Builtin {
			d.Builtin[i].Disabled = true
			d.Builtin[i].OverrideScope, d.Builtin[i].OverridePath = "user", userPath
		}
	}
	for _, list := range [][]AgentConfig{d.Builtin, d.Package, d.User, d.Project} {
		for i := range list {
			applyDefaultsToAgent(&list[i], userSettings, projectSettings)
			applyOverrideToAgent(&list[i], userSettings, projectSettings)
		}
	}
}

// applyDefaultsToAgent applies defaultModel and defaultThinking when the agent
// pins neither (applySubagentDefaults in the original: defaults first, then
// overrides).
func applyDefaultsToAgent(a *AgentConfig, userSettings, projectSettings *subagentSettings) {
	for _, settings := range []*subagentSettings{userSettings, projectSettings} {
		if settings == nil {
			continue
		}
		if a.Model == "" && settings.DefaultModel != "" {
			a.Model = settings.DefaultModel
		}
		if a.Thinking == nil && settings.DefaultThinking != "" {
			a.Thinking = settings.DefaultThinking
		}
	}
}

func applyOverrideToAgent(a *AgentConfig, userSettings, projectSettings *subagentSettings) {
	for _, scope := range []struct {
		scope    string
		settings *subagentSettings
	}{{"project", projectSettings}, {"user", userSettings}} {
		if scope.settings == nil || scope.settings.AgentOverrides == nil {
			continue
		}
		entry, ok := scope.settings.AgentOverrides[a.Name]
		if !ok {
			continue
		}
		if disabled, ok := entry["disabled"].(bool); ok {
			a.Disabled = disabled
			a.OverrideScope, a.OverridePath = scope.scope, ""
		}
		if model, ok := entry["model"].(string); ok {
			a.Model = model
			a.OverrideScope, a.OverridePath = scope.scope, ""
		}
		if thinking, present := entry["thinking"]; present {
			if s, ok := thinking.(string); ok {
				a.Thinking = s
			} else {
				a.Thinking = false
			}
			a.OverrideScope, a.OverridePath = scope.scope, ""
		}
		if machine, present := entry["machine"]; present {
			if s, ok := machine.(string); ok {
				a.Machine = s
			} else {
				a.Machine = ""
			}
			a.OverrideScope, a.OverridePath = scope.scope, ""
		}
		return
	}
}
