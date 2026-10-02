package acp

import (
	"encoding/json"
	"os"
	"path/filepath"
)

func readJSONObject(path string) map[string]any {
	raw, err := os.ReadFile(path)
	if err != nil {
		return map[string]any{}
	}
	var data any
	if json.Unmarshal(raw, &data) != nil {
		return map[string]any{}
	}
	if m, ok := data.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func deepMerge(a, b map[string]any) map[string]any {
	out := make(map[string]any, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		av, aok := out[k].(map[string]any)
		bv, bok := v.(map[string]any)
		if aok && bok {
			out[k] = deepMerge(av, bv)
		} else {
			out[k] = v
		}
	}
	return out
}

// mergedSettings mirrors pig's settings: the global file, overridden by the project file.
func mergedSettings(cwd string) map[string]any {
	global := readJSONObject(filepath.Join(AgentDir(), "settings.json"))
	project := readJSONObject(filepath.Join(absPathFrom(cwd), ProjectDirName(), "settings.json"))
	return deepMerge(global, project)
}

func absPathFrom(cwd string) string {
	if filepath.IsAbs(cwd) {
		return filepath.Clean(cwd)
	}
	return absPath(cwd)
}

// GetEnableSkillCommands reads enableSkillCommands from the merged PiG settings.
func GetEnableSkillCommands(cwd string) bool {
	m := mergedSettings(cwd)
	if b, ok := m["enableSkillCommands"].(bool); ok {
		return b
	}
	if skills, ok := m["skills"].(map[string]any); ok {
		if b, ok := skills["enableSkillCommands"].(bool); ok {
			return b
		}
	}
	return true
}

// GetQuietStartup reads quietStartup from the merged PiG settings.
func GetQuietStartup(cwd string) bool {
	m := mergedSettings(cwd)
	if b, ok := m["quietStartup"].(bool); ok {
		return b
	}
	if b, ok := m["quietStart"].(bool); ok {
		return b
	}
	return false
}
