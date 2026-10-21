package tintinweb_subagents

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// The subagents.json store the `/agents → Settings` menu writes: all 24 keys of the original, read exactly as its
// `sanitize` does (a value of the wrong shape is dropped, silently), merged global then project key by key, and written
// back whole to the project file, in the order of the original's `snapshotSettings()`. A nil field is unset. The global
// file is manual defaults, never written here. upstream: src/settings.ts (SubagentsSettings, sanitize, loadSettings,
// saveSettings, persistToastFor, saveAndEmitChanged) and src/index.ts snapshotSettings.
type subagentsSettings struct {
	MaxConcurrent           *int    `json:"maxConcurrent,omitempty"`
	MaxConcurrentForeground *int    `json:"maxConcurrentForeground,omitempty"`
	DefaultMaxTurns         *int    `json:"defaultMaxTurns,omitempty"`
	GraceTurns              *int    `json:"graceTurns,omitempty"`
	DefaultJoinMode         *string `json:"defaultJoinMode,omitempty"`
	BackgroundByDefault     *bool   `json:"backgroundByDefault,omitempty"`
	SchedulingEnabled       *bool   `json:"schedulingEnabled,omitempty"`
	ScopeModels             *bool   `json:"scopeModels,omitempty"`
	StrictAgentFiles        *bool   `json:"strictAgentFiles,omitempty"`
	DisableDefaultAgents    *bool   `json:"disableDefaultAgents,omitempty"`
	ToolDescriptionMode     *string `json:"toolDescriptionMode,omitempty"`
	FleetView               *bool   `json:"fleetView,omitempty"`
	AgentMentions           *string `json:"agentMentions,omitempty"`
	RememberAgents          *bool   `json:"rememberAgents,omitempty"`
	WidgetMode              *string `json:"widgetMode,omitempty"`
	OutputTranscript        *bool   `json:"outputTranscript,omitempty"`
	WorktreeIsolation       *bool   `json:"worktreeIsolation,omitempty"`
	WorkflowsEnabled        *bool   `json:"workflowsEnabled,omitempty"`
	MaxSubagentDepth        *int    `json:"maxSubagentDepth,omitempty"`
	FallbackSubagent        *string `json:"fallbackSubagent,omitempty"`
	ReportUsage             *bool   `json:"reportUsage,omitempty"`
	ShowCost                *bool   `json:"showCost,omitempty"`
	ShowModel               *bool   `json:"showModel,omitempty"`
	ViewerMarkdown          *string `json:"viewerMarkdown,omitempty"`
}

var (
	validJoinModes            = []string{"async", "group", "smart"}
	validToolDescriptionModes = []string{"full", "compact", "custom"}
	validWidgetModes          = []string{"all", "background", "off"}
	validViewerMarkdownModes  = []string{"off", "assistant", "all"}
	validAgentMentionModes    = []string{"model", "direct", "off"}
)

// subagentDepthCeiling is the sanity ceiling of maxSubagentDepth. upstream: SUBAGENT_DEPTH_CEILING.
const subagentDepthCeiling = 16

func settingInt(v any, lo, hi int) *int {
	if n, ok := integer(v); ok && n >= lo && n <= hi {
		return &n
	}
	return nil
}

func settingBool(v any) *bool {
	if b, ok := v.(bool); ok {
		return &b
	}
	return nil
}

func settingEnum(v any, valid []string) *string {
	if s, ok := v.(string); ok && inList(valid, s) {
		return &s
	}
	return nil
}

// sanitizeSubagentsSettings drops the fields that do not match the expected shape. upstream: settings.ts sanitize.
func sanitizeSubagentsSettings(raw any) subagentsSettings {
	r, ok := raw.(map[string]any)
	if !ok {
		return subagentsSettings{}
	}
	out := subagentsSettings{
		MaxConcurrent:           settingInt(r["maxConcurrent"], 1, maxConcurrentCeiling),
		MaxConcurrentForeground: settingInt(r["maxConcurrentForeground"], 0, maxConcurrentCeiling), // 0 is "unlimited", the default
		DefaultMaxTurns:         settingInt(r["defaultMaxTurns"], 0, maxTurnsCeiling),              // 0 is "unlimited"
		GraceTurns:              settingInt(r["graceTurns"], 1, graceTurnsCeiling),
		MaxSubagentDepth:        settingInt(r["maxSubagentDepth"], 0, subagentDepthCeiling),
		DefaultJoinMode:         settingEnum(r["defaultJoinMode"], validJoinModes),
		BackgroundByDefault:     settingBool(r["backgroundByDefault"]),
		SchedulingEnabled:       settingBool(r["schedulingEnabled"]),
		ScopeModels:             settingBool(r["scopeModels"]),
		StrictAgentFiles:        settingBool(r["strictAgentFiles"]),
		DisableDefaultAgents:    settingBool(r["disableDefaultAgents"]),
		ToolDescriptionMode:     settingEnum(r["toolDescriptionMode"], validToolDescriptionModes),
		FleetView:               settingBool(r["fleetView"]),
		RememberAgents:          settingBool(r["rememberAgents"]),
		WidgetMode:              settingEnum(r["widgetMode"], validWidgetModes),
		OutputTranscript:        settingBool(r["outputTranscript"]),
		WorktreeIsolation:       settingBool(r["worktreeIsolation"]),
		ReportUsage:             settingBool(r["reportUsage"]),
		ShowCost:                settingBool(r["showCost"]),
		ShowModel:               settingBool(r["showModel"]),
		ViewerMarkdown:          settingEnum(r["viewerMarkdown"], validViewerMarkdownModes),
		WorkflowsEnabled:        settingBool(r["workflowsEnabled"]),
	}
	// A boolean before the `model` mode existed: true reads as "model", false as "off".
	if b, ok := r["agentMentions"].(bool); ok {
		mode := "off"
		if b {
			mode = "model"
		}
		out.AgentMentions = &mode
	} else {
		out.AgentMentions = settingEnum(r["agentMentions"], validAgentMentionModes)
	}
	switch v := r["fallbackSubagent"].(type) {
	case bool:
		// The only non-string spelling worth accepting: a boolean would otherwise be dropped, silently leaving the
		// permissive default in place.
		if !v {
			none := noFallback
			out.FallbackSubagent = &none
		}
	case string:
		if s := strings.TrimSpace(v); s != "" {
			out.FallbackSubagent = &s
		}
	}
	return out
}

func readSubagentsSettingsFile(path string) subagentsSettings {
	data, err := os.ReadFile(path)
	if err != nil {
		return subagentsSettings{}
	}
	var raw any
	if json.Unmarshal(data, &raw) != nil {
		return subagentsSettings{}
	}
	return sanitizeSubagentsSettings(raw)
}

// mergeOver returns s with every field o sets (the original spreads the project file over the global one).
func (s subagentsSettings) mergeOver(o subagentsSettings) subagentsSettings {
	if o.MaxConcurrent != nil {
		s.MaxConcurrent = o.MaxConcurrent
	}
	if o.MaxConcurrentForeground != nil {
		s.MaxConcurrentForeground = o.MaxConcurrentForeground
	}
	if o.DefaultMaxTurns != nil {
		s.DefaultMaxTurns = o.DefaultMaxTurns
	}
	if o.GraceTurns != nil {
		s.GraceTurns = o.GraceTurns
	}
	if o.DefaultJoinMode != nil {
		s.DefaultJoinMode = o.DefaultJoinMode
	}
	if o.BackgroundByDefault != nil {
		s.BackgroundByDefault = o.BackgroundByDefault
	}
	if o.SchedulingEnabled != nil {
		s.SchedulingEnabled = o.SchedulingEnabled
	}
	if o.ScopeModels != nil {
		s.ScopeModels = o.ScopeModels
	}
	if o.StrictAgentFiles != nil {
		s.StrictAgentFiles = o.StrictAgentFiles
	}
	if o.DisableDefaultAgents != nil {
		s.DisableDefaultAgents = o.DisableDefaultAgents
	}
	if o.ToolDescriptionMode != nil {
		s.ToolDescriptionMode = o.ToolDescriptionMode
	}
	if o.FleetView != nil {
		s.FleetView = o.FleetView
	}
	if o.AgentMentions != nil {
		s.AgentMentions = o.AgentMentions
	}
	if o.RememberAgents != nil {
		s.RememberAgents = o.RememberAgents
	}
	if o.WidgetMode != nil {
		s.WidgetMode = o.WidgetMode
	}
	if o.OutputTranscript != nil {
		s.OutputTranscript = o.OutputTranscript
	}
	if o.WorktreeIsolation != nil {
		s.WorktreeIsolation = o.WorktreeIsolation
	}
	if o.WorkflowsEnabled != nil {
		s.WorkflowsEnabled = o.WorkflowsEnabled
	}
	if o.MaxSubagentDepth != nil {
		s.MaxSubagentDepth = o.MaxSubagentDepth
	}
	if o.FallbackSubagent != nil {
		s.FallbackSubagent = o.FallbackSubagent
	}
	if o.ReportUsage != nil {
		s.ReportUsage = o.ReportUsage
	}
	if o.ShowCost != nil {
		s.ShowCost = o.ShowCost
	}
	if o.ShowModel != nil {
		s.ShowModel = o.ShowModel
	}
	if o.ViewerMarkdown != nil {
		s.ViewerMarkdown = o.ViewerMarkdown
	}
	return s
}

// loadSubagentsSettings is the global file with the project file over it, key by key. upstream: settings.ts loadSettings.
func loadSubagentsSettings(cwd string) subagentsSettings {
	return readSubagentsSettingsFile(filepath.Join(agentDir(), "subagents.json")).
		mergeOver(readSubagentsSettingsFile(filepath.Join(cwd, ".pi", "subagents.json")))
}

// subagentsSettingsPath is the file the menu writes: the project's, which overrides the global one on load.
func subagentsSettingsPath(cwd string) string { return filepath.Join(cwd, ".pi", "subagents.json") }

// settingsWriter is the write, a seam so a test can make it fail.
var settingsWriter = saveSubagentsSettings

// saveSubagentsSettings writes the whole snapshot as JSON.stringify(s, null, 2) would (no trailing newline), and reports
// whether it was written. upstream: settings.ts saveSettings.
func saveSubagentsSettings(s subagentsSettings, cwd string) bool {
	path := subagentsSettingsPath(cwd)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if enc.Encode(s) != nil {
		return false
	}
	if os.MkdirAll(filepath.Dir(path), 0o777) != nil {
		return false
	}
	return os.WriteFile(path, bytes.TrimSuffix(buf.Bytes(), []byte("\n")), 0o666) == nil
}

// persistToastFor is the toast of a change: info when it was written, a warning when it is session only.
// upstream: settings.ts persistToastFor.
func persistToastFor(successMsg string, persisted bool) (message, level string) {
	if persisted {
		return successMsg, "info"
	}
	return successMsg + " (session only; failed to persist)", "warning"
}

// heldSettings is the state of the settings whose feature this port does not have. A value is held so the file
// round-trips and the menu shows it, and starts at the value this port actually behaves as (the one `settingRules` in
// refuse.go accepts without a refusal), so writing the snapshot back never announces a refusal for a default the user
// never chose. The effect belongs to the slice that ports the feature.
type heldSettings struct {
	mu                      sync.Mutex
	maxConcurrentForeground int
	defaultJoinMode         string
	backgroundByDefault     bool
	scopeModels             bool
	strictAgentFiles        bool
	toolDescriptionMode     string
	agentMentions           string
	rememberAgents          bool
	outputTranscript        bool
	worktreeIsolation       bool
	maxSubagentDepth        int
	reportUsage             bool
}

func defaultHeldSettings() *heldSettings {
	return &heldSettings{defaultJoinMode: "async", backgroundByDefault: true, toolDescriptionMode: "full", agentMentions: "off", maxSubagentDepth: 1}
}

// load reads the held keys of a workspace over the defaults.
func (h *heldSettings) load(s subagentsSettings) {
	h.mu.Lock()
	defer h.mu.Unlock()
	d := defaultHeldSettings()
	h.maxConcurrentForeground, h.defaultJoinMode, h.backgroundByDefault = d.maxConcurrentForeground, d.defaultJoinMode, d.backgroundByDefault
	h.scopeModels, h.strictAgentFiles, h.toolDescriptionMode = d.scopeModels, d.strictAgentFiles, d.toolDescriptionMode
	h.agentMentions, h.rememberAgents, h.outputTranscript = d.agentMentions, d.rememberAgents, d.outputTranscript
	h.worktreeIsolation, h.maxSubagentDepth, h.reportUsage = d.worktreeIsolation, d.maxSubagentDepth, d.reportUsage
	if s.MaxConcurrentForeground != nil {
		h.maxConcurrentForeground = *s.MaxConcurrentForeground
	}
	if s.DefaultJoinMode != nil {
		h.defaultJoinMode = *s.DefaultJoinMode
	}
	if s.BackgroundByDefault != nil {
		h.backgroundByDefault = *s.BackgroundByDefault
	}
	if s.ScopeModels != nil {
		h.scopeModels = *s.ScopeModels
	}
	if s.StrictAgentFiles != nil {
		h.strictAgentFiles = *s.StrictAgentFiles
	}
	if s.ToolDescriptionMode != nil {
		h.toolDescriptionMode = *s.ToolDescriptionMode
	}
	if s.AgentMentions != nil {
		h.agentMentions = *s.AgentMentions
	}
	if s.RememberAgents != nil {
		h.rememberAgents = *s.RememberAgents
	}
	if s.OutputTranscript != nil {
		h.outputTranscript = *s.OutputTranscript
	}
	if s.WorktreeIsolation != nil {
		h.worktreeIsolation = *s.WorktreeIsolation
	}
	if s.MaxSubagentDepth != nil {
		h.maxSubagentDepth = *s.MaxSubagentDepth
	}
	if s.ReportUsage != nil {
		h.reportUsage = *s.ReportUsage
	}
}
