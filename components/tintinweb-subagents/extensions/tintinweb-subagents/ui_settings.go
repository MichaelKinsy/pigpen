package tintinweb_subagents

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// The display settings of subagents.json: widgetMode, fleetView, showCost, showModel and viewerMarkdown. The global
// file (<agent dir>/subagents.json) is read first, the project file (<cwd>/.pi/subagents.json) overrides it; a value
// of the wrong shape is dropped, as in the original. upstream: src/settings.ts (sanitize) and the defaults of
// src/index.ts (widgetMode "background", fleetView on, showCost/showModel off, viewerMarkdown "assistant").
type uiSettings struct {
	widgetMode     widgetMode
	fleetView      bool
	showCost       bool
	showModel      bool
	viewerMarkdown viewerMarkdownMode
}

func defaultUISettings() uiSettings {
	return uiSettings{widgetMode: widgetModeBackground, fleetView: true, viewerMarkdown: viewerMarkdownAssistant}
}

func (s *uiSettings) apply(raw map[string]any) {
	if v, ok := raw["widgetMode"].(string); ok {
		switch m := widgetMode(v); m {
		case widgetModeAll, widgetModeBackground, widgetModeOff:
			s.widgetMode = m
		}
	}
	if v, ok := raw["fleetView"].(bool); ok {
		s.fleetView = v
	}
	if v, ok := raw["showCost"].(bool); ok {
		s.showCost = v
	}
	if v, ok := raw["showModel"].(bool); ok {
		s.showModel = v
	}
	if v, ok := raw["viewerMarkdown"].(string); ok {
		switch m := viewerMarkdownMode(v); m {
		case viewerMarkdownOff, viewerMarkdownAssistant, viewerMarkdownAll:
			s.viewerMarkdown = m
		}
	}
}

// loadUISettings merges the global and the project file over the defaults, key by key.
func loadUISettings(cwd string) uiSettings {
	s := defaultUISettings()
	for _, path := range []string{filepath.Join(agentDir(), "subagents.json"), filepath.Join(cwd, ".pi", "subagents.json")} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var raw map[string]any
		if json.Unmarshal(data, &raw) == nil {
			s.apply(raw)
		}
	}
	return s
}

// persistViewerMarkdown writes a mode chosen with the viewer's `m` key into the project file, so the key and the
// settings mean one thing. It reports whether the write succeeded. upstream: index.ts chooseViewerMarkdown ->
// persistSettings, which writes the whole snapshot; this writes the one key (a settings writer that owns the snapshot
// replaces this var).
var persistViewerMarkdown = func(cwd string, mode viewerMarkdownMode) bool {
	path := filepath.Join(cwd, ".pi", "subagents.json")
	raw := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &raw)
	}
	raw["viewerMarkdown"] = string(mode)
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return false
	}
	if os.MkdirAll(filepath.Dir(path), 0o755) != nil {
		return false
	}
	return os.WriteFile(path, out, 0o644) == nil
}
