package tintinweb_subagents

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
)

// The subagents.json settings store: all 24 keys of the original, parsed exactly like its `sanitize` (a value of the
// wrong shape is dropped, silently), merged global then project key by key, written back to the project file, and
// applied to the running state through setter hooks. The global file (<agent dir>/subagents.json) is read first, the
// project file (<cwd>/.pi/subagents.json) overrides it. A nil field is "unset": the original's default applies.
// upstream: src/settings.ts (SubagentsSettings, sanitize, loadSettings, saveSettings, applySettings, persistToastFor,
// applyAndEmitLoaded, saveAndEmitChanged).
//
// The struct tags give the order in which the original's `snapshotSettings()` writes the file (index.ts), which is
// the order saveSettings writes.
type settings struct {
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

// Valid values of the enum settings. upstream: settings.ts VALID_*.
var (
	validJoinModes            = []string{"async", "group", "smart"}
	validToolDescriptionModes = []string{"full", "compact", "custom"}
	validWidgetModes          = []string{"all", "background", "off"}
	validViewerMarkdownModes  = []string{"off", "assistant", "all"}
	validAgentMentionModes    = []string{"model", "direct", "off"}
)

// Sanity ceilings: they keep a hand-edited file from asking for values that make no operational sense.
// upstream: settings.ts MAX_CONCURRENT_CEILING and the others.
const (
	maxConcurrentCeiling    = 1024
	maxTurnsCeiling         = 10_000
	graceTurnsCeiling       = 1_000
	subagentDepthCeiling    = 16
	defaultJoinModeSetting  = "smart"
	defaultMaxSubagentDepth = 2
)

func integer(v any) (int, bool) {
	f, ok := v.(float64)
	if !ok || f != math.Trunc(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return int(f), true
}

func inList(list []string, s string) bool {
	for _, e := range list {
		if e == s {
			return true
		}
	}
	return false
}

func intIn(v any, lo, hi int) *int {
	if n, ok := integer(v); ok && n >= lo && n <= hi {
		return &n
	}
	return nil
}

func boolOf(v any) *bool {
	if b, ok := v.(bool); ok {
		return &b
	}
	return nil
}

func enumOf(v any, valid []string) *string {
	if s, ok := v.(string); ok && inList(valid, s) {
		return &s
	}
	return nil
}

// sanitizeSettings drops the fields that do not match the expected shape. upstream: settings.ts sanitize.
func sanitizeSettings(raw any) settings {
	r, ok := raw.(map[string]any)
	if !ok {
		return settings{}
	}
	out := settings{
		MaxConcurrent:           intIn(r["maxConcurrent"], 1, maxConcurrentCeiling),
		MaxConcurrentForeground: intIn(r["maxConcurrentForeground"], 0, maxConcurrentCeiling), // 0 is "unlimited", the default
		DefaultMaxTurns:         intIn(r["defaultMaxTurns"], 0, maxTurnsCeiling),              // 0 is "unlimited"
		GraceTurns:              intIn(r["graceTurns"], 1, graceTurnsCeiling),
		MaxSubagentDepth:        intIn(r["maxSubagentDepth"], 0, subagentDepthCeiling),
		DefaultJoinMode:         enumOf(r["defaultJoinMode"], validJoinModes),
		BackgroundByDefault:     boolOf(r["backgroundByDefault"]),
		SchedulingEnabled:       boolOf(r["schedulingEnabled"]),
		ScopeModels:             boolOf(r["scopeModels"]),
		StrictAgentFiles:        boolOf(r["strictAgentFiles"]),
		DisableDefaultAgents:    boolOf(r["disableDefaultAgents"]),
		ToolDescriptionMode:     enumOf(r["toolDescriptionMode"], validToolDescriptionModes),
		FleetView:               boolOf(r["fleetView"]),
		RememberAgents:          boolOf(r["rememberAgents"]),
		WidgetMode:              enumOf(r["widgetMode"], validWidgetModes),
		OutputTranscript:        boolOf(r["outputTranscript"]),
		WorktreeIsolation:       boolOf(r["worktreeIsolation"]),
		ReportUsage:             boolOf(r["reportUsage"]),
		ShowCost:                boolOf(r["showCost"]),
		ShowModel:               boolOf(r["showModel"]),
		ViewerMarkdown:          enumOf(r["viewerMarkdown"], validViewerMarkdownModes),
		WorkflowsEnabled:        boolOf(r["workflowsEnabled"]),
	}
	// The setting was a boolean before the `model` mode existed: true reads as "model", false as "off".
	if b, ok := r["agentMentions"].(bool); ok {
		mode := "off"
		if b {
			mode = "model"
		}
		out.AgentMentions = &mode
	} else {
		out.AgentMentions = enumOf(r["agentMentions"], validAgentMentionModes)
	}
	if b, ok := r["rememberAgents"].(bool); ok {
		out.RememberAgents = &b
	}
	if b, ok := r["outputTranscript"].(bool); ok {
		out.OutputTranscript = &b
	}
	if b, ok := r["reportUsage"].(bool); ok {
		out.ReportUsage = &b
	}
	if b, ok := r["showCost"].(bool); ok {
		out.ShowCost = &b
	}
	if b, ok := r["worktreeIsolation"].(bool); ok {
		out.WorktreeIsolation = &b
	}
	switch v := r["fallbackSubagent"].(type) {
	case bool:
		if !v { // `false` is a spelling of "none": dropped, it would silently leave the permissive default
			n := noFallback
			out.FallbackSubagent = &n
		}
	case string:
		if s := strings.TrimSpace(v); s != "" {
			out.FallbackSubagent = &s
		}
	}
	return out
}

// settingsWarn reports a malformed settings file (the original's console.warn). A test replaces it.
var settingsWarn = func(msg string) { fmt.Fprintln(os.Stderr, msg) }

func globalSettingsPath() string { return filepath.Join(agentDir(), "subagents.json") }

func projectSettingsPath(cwd string) string { return filepath.Join(cwd, ".pi", "subagents.json") }

// readSettingsFile reads one file. A missing file is silent; one that exists and cannot be parsed warns and reads as
// empty, so startup proceeds. upstream: settings.ts readSettingsFile.
func readSettingsFile(path string) settings {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return settings{}
		}
		settingsWarn(fmt.Sprintf("[pi-subagents] Ignoring malformed settings at %s: %v", path, err))
		return settings{}
	}
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		settingsWarn(fmt.Sprintf("[pi-subagents] Ignoring malformed settings at %s: %v", path, err))
		return settings{}
	}
	return sanitizeSettings(raw)
}

// mergeSettings lays over on top of base key by key (a spread of two objects in the original).
func mergeSettings(base, over settings) settings {
	out := base
	ov, ob := reflect.ValueOf(over), reflect.ValueOf(&out).Elem()
	for i := range ov.NumField() {
		if !ov.Field(i).IsNil() {
			ob.Field(i).Set(ov.Field(i))
		}
	}
	return out
}

// loadSettings merges the global and the project file, the project one winning field by field.
func loadSettings(cwd string) settings {
	return mergeSettings(readSettingsFile(globalSettingsPath()), readSettingsFile(projectSettingsPath(cwd)))
}

// saveSettings writes the project-local file (the global one is never touched) and reports whether it could.
// upstream: settings.ts saveSettings (JSON.stringify(s, null, 2)).
func saveSettings(s settings, cwd string) bool {
	path := projectSettingsPath(cwd)
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return false
	}
	if os.MkdirAll(filepath.Dir(path), 0o755) != nil {
		return false
	}
	return os.WriteFile(path, data, 0o644) == nil
}

// settingsAppliers are the setters applySettings wires persisted values into the running state with. A nil setter is
// skipped (a key whose slice has not landed keeps its value in the store only). upstream: settings.ts SettingsAppliers.
type settingsAppliers struct {
	SetMaxConcurrent           func(n int)
	SetMaxConcurrentForeground func(n int)
	SetDefaultMaxTurns         func(n int)
	SetGraceTurns              func(n int)
	SetDefaultJoinMode         func(mode string)
	SetBackgroundByDefault     func(b bool)
	SetSchedulingEnabled       func(b bool)
	SetScopeModels             func(b bool)
	SetStrictAgentFiles        func(b bool)
	SetDisableDefaultAgents    func(b bool)
	SetToolDescriptionMode     func(mode string)
	SetFleetView               func(b bool)
	SetAgentMentions           func(mode string)
	SetRememberAgents          func(b bool)
	SetWidgetMode              func(mode string)
	SetOutputTranscript        func(b bool)
	SetWorktreeIsolation       func(b bool)
	SetWorkflowsEnabled        func(b bool)
	SetMaxSubagentDepth        func(n int)
	SetFallbackSubagent        func(v string)
	SetReportUsage             func(b bool)
	SetShowCost                func(b bool)
	SetShowModel               func(b bool)
	SetViewerMarkdown          func(mode string)
}

// applySettings applies the persisted settings that are present; absence leaves the running value alone.
// upstream: settings.ts applySettings.
func applySettings(s settings, ap settingsAppliers) {
	applyInt(s.MaxConcurrent, ap.SetMaxConcurrent)
	applyInt(s.MaxConcurrentForeground, ap.SetMaxConcurrentForeground)
	applyInt(s.DefaultMaxTurns, ap.SetDefaultMaxTurns)
	applyInt(s.GraceTurns, ap.SetGraceTurns)
	applyInt(s.MaxSubagentDepth, ap.SetMaxSubagentDepth)
	applyStr(s.FallbackSubagent, ap.SetFallbackSubagent)
	applyStr(s.DefaultJoinMode, ap.SetDefaultJoinMode)
	applyBool(s.BackgroundByDefault, ap.SetBackgroundByDefault)
	applyBool(s.SchedulingEnabled, ap.SetSchedulingEnabled)
	applyBool(s.ScopeModels, ap.SetScopeModels)
	applyBool(s.StrictAgentFiles, ap.SetStrictAgentFiles)
	applyBool(s.DisableDefaultAgents, ap.SetDisableDefaultAgents)
	applyStr(s.ToolDescriptionMode, ap.SetToolDescriptionMode)
	applyBool(s.FleetView, ap.SetFleetView)
	applyStr(s.AgentMentions, ap.SetAgentMentions)
	applyBool(s.RememberAgents, ap.SetRememberAgents)
	applyStr(s.WidgetMode, ap.SetWidgetMode)
	applyBool(s.OutputTranscript, ap.SetOutputTranscript)
	applyBool(s.WorktreeIsolation, ap.SetWorktreeIsolation)
	applyBool(s.ReportUsage, ap.SetReportUsage)
	applyBool(s.ShowCost, ap.SetShowCost)
	applyBool(s.ShowModel, ap.SetShowModel)
	applyStr(s.ViewerMarkdown, ap.SetViewerMarkdown)
	applyBool(s.WorkflowsEnabled, ap.SetWorkflowsEnabled)
}

func applyInt(v *int, set func(int)) {
	if v != nil && set != nil {
		set(*v)
	}
}

func applyBool(v *bool, set func(bool)) {
	if v != nil && set != nil {
		set(*v)
	}
}

func applyStr(v *string, set func(string)) {
	if v != nil && set != nil {
		set(*v)
	}
}

// persistToastFor is the toast of a settings mutation. upstream: settings.ts persistToastFor.
func persistToastFor(successMsg string, persisted bool) (message, level string) {
	if persisted {
		return successMsg, "info"
	}
	return successMsg + " (session only; failed to persist)", "warning"
}

// settingsEmit is a subset of the event bus's Emit.
type settingsEmit func(event string, payload any)

// applyAndEmitLoaded loads the merged settings, applies them and emits `subagents:settings_loaded`.
// upstream: settings.ts applyAndEmitLoaded.
func applyAndEmitLoaded(ap settingsAppliers, emit settingsEmit, cwd string) settings {
	s := loadSettings(cwd)
	applySettings(s, ap)
	emit("subagents:settings_loaded", map[string]any{"settings": s.payload()})
	return s
}

// saveAndEmitChanged persists a snapshot, emits `subagents:settings_changed` whatever the outcome, and returns the
// toast to show. upstream: settings.ts saveAndEmitChanged.
func saveAndEmitChanged(snapshot settings, successMsg string, emit settingsEmit, cwd string) (message, level string) {
	persisted := saveSettings(snapshot, cwd)
	emit("subagents:settings_changed", map[string]any{"settings": snapshot.payload(), "persisted": persisted})
	return persistToastFor(successMsg, persisted)
}

// payload is the settings as the JSON object the original's event payload is.
func (s settings) payload() map[string]any {
	data, _ := json.Marshal(s)
	out := map[string]any{}
	_ = json.Unmarshal(data, &out)
	return out
}

// The store: the merged persisted values, kept current by every load and by the apply hooks, so the settings of
// another slice's feature (parsed and stored here) are read through settingsView with the original's defaults.
var (
	storeMu sync.Mutex
	store   settings
)

// settingsView is the current merged settings. Read the effective value of a key through the methods below, which
// carry the original's default for an unset one.
func settingsView() settings {
	storeMu.Lock()
	defer storeMu.Unlock()
	return store
}

func setSettingsView(s settings) {
	storeMu.Lock()
	store = s
	storeMu.Unlock()
}

func resetSettingsView() { setSettingsView(settings{}) }

func orBool(v *bool, d bool) bool {
	if v == nil {
		return d
	}
	return *v
}

func orStr(v *string, d string) string {
	if v == nil {
		return d
	}
	return *v
}

func orInt(v *int, d int) int {
	if v == nil {
		return d
	}
	return *v
}

// The effective values, with the original's defaults. upstream: index.ts (the `let` of each setting).
func (s settings) schedulingEnabled() bool     { return orBool(s.SchedulingEnabled, true) }
func (s settings) worktreeIsolation() bool     { return orBool(s.WorktreeIsolation, true) }
func (s settings) workflowsEnabled() bool      { return orBool(s.WorkflowsEnabled, true) }
func (s settings) fleetView() bool             { return orBool(s.FleetView, true) }
func (s settings) agentMentions() string       { return orStr(s.AgentMentions, "model") }
func (s settings) rememberAgents() bool        { return orBool(s.RememberAgents, true) }
func (s settings) widgetMode() string          { return orStr(s.WidgetMode, "background") }
func (s settings) outputTranscript() bool      { return orBool(s.OutputTranscript, true) }
func (s settings) maxSubagentDepth() int       { return orInt(s.MaxSubagentDepth, defaultMaxSubagentDepth) }
func (s settings) reportUsage() bool           { return orBool(s.ReportUsage, false) }
func (s settings) showCost() bool              { return orBool(s.ShowCost, false) }
func (s settings) showModel() bool             { return orBool(s.ShowModel, false) }
func (s settings) viewerMarkdown() string      { return orStr(s.ViewerMarkdown, "assistant") }
func (s settings) toolDescriptionMode() string { return orStr(s.ToolDescriptionMode, "full") }
func (s settings) defaultJoinMode() string     { return orStr(s.DefaultJoinMode, defaultJoinModeSetting) }
func (s settings) backgroundByDefault() bool   { return orBool(s.BackgroundByDefault, true) }
func (s settings) scopeModels() bool           { return orBool(s.ScopeModels, false) }
func (s settings) strictAgentFiles() bool      { return orBool(s.StrictAgentFiles, false) }
