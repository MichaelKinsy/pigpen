package tintinweb_tasks

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTasksConfig(t *testing.T) {
	const f = "tasks-config"
	type env struct{ cwd, agent, globalPath, projectPath string }
	setup := func(t *testing.T) env {
		root := scratch(t)
		e := env{cwd: filepath.Join(root, "project"), agent: filepath.Join(root, "agent")}
		e.globalPath = filepath.Join(e.agent, "tasks-config.json")
		e.projectPath = filepath.Join(e.cwd, ".pi", "tasks-config.json")
		os.MkdirAll(e.cwd, 0o755)
		os.MkdirAll(e.agent, 0o755)
		return e
	}
	tw(t, f, "returns an empty config when no files exist", func(t *testing.T) {
		e := setup(t)
		eq(t, loadTasksConfig(e.cwd, e.agent), tasksConfig{})
	})
	tw(t, f, "loads global defaults from the agent directory", func(t *testing.T) {
		e := setup(t)
		writeJSON(t, e.globalPath, obj{"autoCascade": true, "maxVisible": 20})
		want := tasksConfig{"autoCascade": true, "maxVisible": float64(20)}
		eq(t, loadGlobalTasksConfig(e.agent), want)
		eq(t, loadTasksConfig(e.cwd, e.agent), want)
	})
	tw(t, f, "merges project overrides over global defaults", func(t *testing.T) {
		e := setup(t)
		writeJSON(t, e.globalPath, obj{"autoCascade": true, "maxVisible": 20, "taskScope": "session"})
		writeJSON(t, e.projectPath, obj{"autoCascade": false, "maxVisible": 10})
		eq(t, loadTasksConfig(e.cwd, e.agent), tasksConfig{"autoCascade": false, "maxVisible": float64(10), "taskScope": "session"})
	})
	tw(t, f, "ignores a malformed global config", func(t *testing.T) {
		e := setup(t)
		os.WriteFile(e.globalPath, []byte("{"), 0o644)
		writeJSON(t, e.projectPath, obj{"autoCascade": false})
		eq(t, loadTasksConfig(e.cwd, e.agent), tasksConfig{"autoCascade": false})
	})
	tw(t, f, "falls back to global defaults when the project config is malformed", func(t *testing.T) {
		e := setup(t)
		writeJSON(t, e.globalPath, obj{"autoCascade": true})
		os.MkdirAll(filepath.Dir(e.projectPath), 0o755)
		os.WriteFile(e.projectPath, []byte("{"), 0o644)
		eq(t, loadTasksConfig(e.cwd, e.agent), tasksConfig{"autoCascade": true})
	})
	tw(t, f, "ignores non-object config values", func(t *testing.T) {
		e := setup(t)
		writeJSON(t, e.globalPath, arr{"not", "a", "config"})
		writeJSON(t, e.projectPath, nil)
		eq(t, loadTasksConfig(e.cwd, e.agent), tasksConfig{})
	})
	tw(t, f, "saves project settings when no global defaults exist", func(t *testing.T) {
		e := setup(t)
		saveTasksConfig(tasksConfig{"autoCascade": true, "maxVisible": 15}, e.cwd, e.agent)
		eq(t, readJSON(t, e.projectPath), any(obj{"autoCascade": true, "maxVisible": float64(15)}))
	})
	tw(t, f, "saves only values that differ from global defaults", func(t *testing.T) {
		e := setup(t)
		writeJSON(t, e.globalPath, obj{"autoCascade": true, "maxVisible": 20})
		saveTasksConfig(tasksConfig{"autoCascade": true, "maxVisible": 30, "showAll": false}, e.cwd, e.agent)
		eq(t, readJSON(t, e.projectPath), any(obj{"maxVisible": float64(30), "showAll": false}))
		eq(t, readJSON(t, e.globalPath), any(obj{"autoCascade": true, "maxVisible": float64(20)}))
	})
	tw(t, f, "preserves a project override across save and reload cycles", func(t *testing.T) {
		e := setup(t)
		writeJSON(t, e.globalPath, obj{"autoCascade": true, "maxVisible": 20})
		c := loadTasksConfig(e.cwd, e.agent)
		c["autoCascade"] = false
		saveTasksConfig(c, e.cwd, e.agent)
		reloaded := loadTasksConfig(e.cwd, e.agent)
		eq(t, reloaded, tasksConfig{"autoCascade": false, "maxVisible": float64(20)})
		reloaded["maxVisible"] = 30
		saveTasksConfig(reloaded, e.cwd, e.agent)
		eq(t, loadTasksConfig(e.cwd, e.agent), tasksConfig{"autoCascade": false, "maxVisible": float64(30)})
		eq(t, readJSON(t, e.projectPath), any(obj{"autoCascade": false, "maxVisible": float64(30)}))
	})
	tw(t, f, "round-trips a custom sortOrder spec", func(t *testing.T) {
		e := setup(t)
		spec := arr{obj{"field": "status", "rank": arr{"in_progress", "pending", "completed"}}, obj{"field": "id"}}
		writeJSON(t, e.projectPath, obj{"sortOrder": spec})
		eq(t, loadTasksConfig(e.cwd, e.agent), tasksConfig{"sortOrder": spec})
	})
	tw(t, f, "does not copy a global sortOrder spec into the project override", func(t *testing.T) {
		e := setup(t)
		writeJSON(t, e.globalPath, obj{"sortOrder": arr{obj{"field": "updatedAt", "direction": "desc"}}})
		saveTasksConfig(loadTasksConfig(e.cwd, e.agent), e.cwd, e.agent)
		eq(t, readJSON(t, e.projectPath), any(obj{}))
	})
	tw(t, f, "writes a sortOrder spec that differs from the global default", func(t *testing.T) {
		e := setup(t)
		writeJSON(t, e.globalPath, obj{"sortOrder": "status"})
		spec := arr{obj{"field": "id", "direction": "desc"}}
		saveTasksConfig(tasksConfig{"sortOrder": spec}, e.cwd, e.agent)
		eq(t, readJSON(t, e.projectPath), any(obj{"sortOrder": spec}))
	})
	tw(t, f, "writes an empty project override object when effective settings match global defaults", func(t *testing.T) {
		e := setup(t)
		writeJSON(t, e.globalPath, obj{"autoCascade": true})
		saveTasksConfig(tasksConfig{"autoCascade": true}, e.cwd, e.agent)
		eq(t, fileExists(e.projectPath), true)
		eq(t, readJSON(t, e.projectPath), any(obj{}))
	})
	tw(t, f, "merges glyphs one by one rather than replacing the whole set", func(t *testing.T) {
		e := setup(t)
		writeJSON(t, e.globalPath, obj{"glyphs": obj{"pending": "[ ]", "spinner": arr{"|", "/"}}})
		writeJSON(t, e.projectPath, obj{"glyphs": obj{"completed": "[x]", "spinner": arr{"-", "\\"}}})
		eq(t, loadTasksConfig(e.cwd, e.agent), tasksConfig{"glyphs": obj{"pending": "[ ]", "completed": "[x]", "spinner": arr{"-", "\\"}}})
	})
	tw(t, f, "leaves glyphs absent when neither config sets any", func(t *testing.T) {
		e := setup(t)
		writeJSON(t, e.globalPath, obj{"autoCascade": true})
		eq(t, loadTasksConfig(e.cwd, e.agent), tasksConfig{"autoCascade": true})
	})
	tw(t, f, "does not copy global glyphs into the project override", func(t *testing.T) {
		e := setup(t)
		writeJSON(t, e.globalPath, obj{"glyphs": obj{"pending": "[ ]", "completed": "[x]"}})
		c := loadTasksConfig(e.cwd, e.agent)
		c["maxVisible"] = 5
		saveTasksConfig(c, e.cwd, e.agent)
		eq(t, readJSON(t, e.projectPath), any(obj{"maxVisible": float64(5)}))
	})
	tw(t, f, "writes only the glyphs that differ from the global ones", func(t *testing.T) {
		e := setup(t)
		writeJSON(t, e.globalPath, obj{"glyphs": obj{"pending": "[ ]", "completed": "[x]"}})
		saveTasksConfig(tasksConfig{"glyphs": obj{"pending": "[ ]", "completed": "done", "inProgress": "[>]"}}, e.cwd, e.agent)
		eq(t, readJSON(t, e.projectPath), any(obj{"glyphs": obj{"completed": "done", "inProgress": "[>]"}}))
	})
	tw(t, f, "preserves a project glyph override across save and reload cycles", func(t *testing.T) {
		e := setup(t)
		writeJSON(t, e.globalPath, obj{"glyphs": obj{"pending": "[ ]"}})
		writeJSON(t, e.projectPath, obj{"glyphs": obj{"completed": "[x]"}})
		saveTasksConfig(loadTasksConfig(e.cwd, e.agent), e.cwd, e.agent)
		eq(t, readJSON(t, e.projectPath), any(obj{"glyphs": obj{"completed": "[x]"}}))
		eq(t, loadTasksConfig(e.cwd, e.agent), tasksConfig{"glyphs": obj{"pending": "[ ]", "completed": "[x]"}})
	})
}
