package tintinweb_tasks

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
)

// tasksConfig is the content of a tasks-config.json: a JSON object whose keys are each read leniently.
// <agent dir>/tasks-config.json provides global defaults, <cwd>/.pi/tasks-config.json project overrides.
// Configuration is data, never code: `.pi/` lives inside cloned repositories. upstream: tasks-config.ts.
//
//	taskScope           memory | session | session-global | project   (default session)
//	autoCascade         bool                                          (default false)
//	autoClearCompleted  never | on_list_complete | on_task_complete   (default on_list_complete)
//	collapseCompleted   bool; showAll bool; maxVisible number; sortOrder preset|spec; hiddenAt top|bottom
//	glyphs              object (see glyphs.go)
type tasksConfig map[string]any

func (c tasksConfig) str(key, fallback string) string {
	if s, ok := c[key].(string); ok {
		return s
	}
	return fallback
}

func (c tasksConfig) flag(key string) bool {
	b, _ := c[key].(bool)
	return b
}

// readTasksConfig reads one config file; a missing, unreadable, malformed or non-object file reads as empty.
// upstream: tasks-config.ts:32-39.
func readTasksConfig(path string) tasksConfig {
	data, err := os.ReadFile(path)
	if err != nil {
		return tasksConfig{}
	}
	var parsed any
	if json.Unmarshal(data, &parsed) != nil {
		return tasksConfig{}
	}
	if m, ok := parsed.(map[string]any); ok {
		return m
	}
	return tasksConfig{}
}

func loadGlobalTasksConfig(agentDir string) tasksConfig {
	return readTasksConfig(filepath.Join(agentDir, "tasks-config.json"))
}

// loadTasksConfig merges the project overrides over the global defaults, key by key; `glyphs` merges a level
// deeper, so a project file overriding one glyph keeps the global ones. upstream: tasks-config.ts:45-55.
func loadTasksConfig(cwd, agentDir string) tasksConfig {
	global := loadGlobalTasksConfig(agentDir)
	project := readTasksConfig(filepath.Join(cwd, ".pi", "tasks-config.json"))
	merged := tasksConfig{}
	for k, v := range global {
		merged[k] = v
	}
	for k, v := range project {
		merged[k] = v
	}
	if _, g := global["glyphs"]; g || project["glyphs"] != nil {
		glyphs := map[string]any{}
		for _, src := range []tasksConfig{global, project} {
			if m, ok := src["glyphs"].(map[string]any); ok {
				for k, v := range m {
					glyphs[k] = v
				}
			}
		}
		if global["glyphs"] != nil || project["glyphs"] != nil {
			merged["glyphs"] = glyphs
		}
	}
	return merged
}

// differs compares two settings as JSON, so object-valued settings are matched by value; a setting absent on
// one side differs from any value on the other. upstream: tasks-config.ts:30.
func differs(a any, aPresent bool, b any) bool {
	if !aPresent {
		return true
	}
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return !bytes.Equal(x, y)
}

// saveTasksConfig writes the settings that differ from the global defaults as the project overrides; the
// glyphs are diffed one by one, to match how they are merged. upstream: tasks-config.ts:57-80.
func saveTasksConfig(config tasksConfig, cwd, agentDir string) error {
	path := filepath.Join(cwd, ".pi", "tasks-config.json")
	global := loadGlobalTasksConfig(agentDir)
	overrides := map[string]any{}
	for key, value := range config {
		if key == "glyphs" {
			continue
		}
		g, present := global[key]
		if differs(g, present, value) {
			overrides[key] = value
		}
	}
	globalGlyphs, _ := global["glyphs"].(map[string]any)
	glyphOverrides := map[string]any{}
	if mine, ok := config["glyphs"].(map[string]any); ok {
		for name, glyph := range mine {
			g, present := globalGlyphs[name]
			if differs(g, present, glyph) {
				glyphOverrides[name] = glyph
			}
		}
	}
	if len(glyphOverrides) > 0 {
		overrides["glyphs"] = glyphOverrides
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(overrides); err != nil {
		return err
	}
	return os.WriteFile(path, bytes.TrimRight(buf.Bytes(), "\n"), 0o644)
}
