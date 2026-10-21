package tintinweb_subagents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Twins of test/settings.test.ts. A settings object is compared as JSON (the original's toEqual on a plain object).

type settingsEnv struct {
	t       *testing.T
	global  string
	project string
}

func newSettingsEnv(t *testing.T) *settingsEnv {
	t.Helper()
	e := &settingsEnv{t: t, global: t.TempDir(), project: t.TempDir()}
	t.Setenv("PIG_CODING_AGENT_DIR", e.global)
	t.Setenv("PIG_USE_PI_DIRS", "")
	t.Cleanup(func() { settingsWarn = func(string) {} })
	return e
}

func (e *settingsEnv) globalFile() string  { return filepath.Join(e.global, "subagents.json") }
func (e *settingsEnv) projectFile() string { return filepath.Join(e.project, ".pi", "subagents.json") }

func (e *settingsEnv) writeGlobal(v any) { e.t.Helper(); writeJSONFile(e.t, e.globalFile(), v) }
func (e *settingsEnv) writeProject(v any) {
	e.t.Helper()
	writeJSONFile(e.t, e.projectFile(), v)
}

func writeJSONFile(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// loaded is the merged settings as the JSON object they read as.
func (e *settingsEnv) loaded() map[string]any { return loadSettings(e.project).payload() }

func (e *settingsEnv) expect(want string) {
	e.t.Helper()
	var w map[string]any
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		e.t.Fatal(err)
	}
	eq(e.t, e.loaded(), w)
}

func parseSettings(t *testing.T, js string) settings {
	t.Helper()
	var raw any
	if err := json.Unmarshal([]byte(js), &raw); err != nil {
		t.Fatal(err)
	}
	return sanitizeSettings(raw)
}

func (e *settingsEnv) save(js string) bool { return saveSettings(parseSettings(e.t, js), e.project) }

// calls records which appliers ran, with their arguments.
type calls map[string][]any

func recordingAppliers() (settingsAppliers, calls) {
	c := calls{}
	rec := func(name string) func(any) {
		return func(v any) { c[name] = append(c[name], v) }
	}
	i := func(name string) func(int) { f := rec(name); return func(n int) { f(n) } }
	b := func(name string) func(bool) { f := rec(name); return func(v bool) { f(v) } }
	s := func(name string) func(string) { f := rec(name); return func(v string) { f(v) } }
	return settingsAppliers{
		SetMaxConcurrent: i("maxConcurrent"), SetMaxConcurrentForeground: i("maxConcurrentForeground"), SetDefaultMaxTurns: i("defaultMaxTurns"),
		SetGraceTurns: i("graceTurns"), SetDefaultJoinMode: s("defaultJoinMode"), SetBackgroundByDefault: b("backgroundByDefault"),
		SetSchedulingEnabled: b("schedulingEnabled"), SetScopeModels: b("scopeModels"), SetStrictAgentFiles: b("strictAgentFiles"),
		SetDisableDefaultAgents: b("disableDefaultAgents"), SetToolDescriptionMode: s("toolDescriptionMode"), SetFleetView: b("fleetView"),
		SetAgentMentions: s("agentMentions"), SetRememberAgents: b("rememberAgents"), SetWidgetMode: s("widgetMode"), SetOutputTranscript: b("outputTranscript"),
		SetWorktreeIsolation: b("worktreeIsolation"), SetWorkflowsEnabled: b("workflowsEnabled"), SetMaxSubagentDepth: i("maxSubagentDepth"),
		SetFallbackSubagent: s("fallbackSubagent"), SetReportUsage: b("reportUsage"), SetShowCost: b("showCost"), SetShowModel: b("showModel"),
		SetViewerMarkdown: s("viewerMarkdown"),
	}, c
}

func TestSettingsTwins(t *testing.T) {
	const f = "settings"
	tw(t, f, "returns {} when both files are missing", func(t *testing.T) {
		newSettingsEnv(t).expect(`{}`)
	})
	tw(t, f, "returns {} when both files are malformed JSON", func(t *testing.T) {
		e := newSettingsEnv(t)
		settingsWarn = func(string) {}
		_ = os.WriteFile(e.globalFile(), []byte("not json {{"), 0o644)
		_ = os.MkdirAll(filepath.Join(e.project, ".pi"), 0o755)
		_ = os.WriteFile(e.projectFile(), []byte("also not json"), 0o644)
		e.expect(`{}`)
	})
	tw(t, f, "loads from global when no project file", func(t *testing.T) {
		e := newSettingsEnv(t)
		e.writeGlobal(obj{"maxConcurrent": 16, "graceTurns": 10})
		e.expect(`{"maxConcurrent":16,"graceTurns":10}`)
	})
	tw(t, f, "loads from project when no global file", func(t *testing.T) {
		e := newSettingsEnv(t)
		e.writeProject(obj{"maxConcurrent": 8, "defaultJoinMode": "group"})
		e.expect(`{"maxConcurrent":8,"defaultJoinMode":"group"}`)
	})
	tw(t, f, "merges global + project with project winning on conflicts", func(t *testing.T) {
		e := newSettingsEnv(t)
		e.writeGlobal(obj{"maxConcurrent": 16, "graceTurns": 10, "defaultJoinMode": "async"})
		e.writeProject(obj{"maxConcurrent": 4, "defaultMaxTurns": 50})
		e.expect(`{"maxConcurrent":4,"graceTurns":10,"defaultJoinMode":"async","defaultMaxTurns":50}`)
	})
	tw(t, f, "round-trips values: saveSettings then loadSettings", func(t *testing.T) {
		e := newSettingsEnv(t)
		js := `{"maxConcurrent":7,"defaultMaxTurns":30,"graceTurns":3,"defaultJoinMode":"smart","schedulingEnabled":false,"toolDescriptionMode":"compact"}`
		e.save(js)
		e.expect(js)
	})
	tw(t, f, "round-trips schedulingEnabled (true and false), and absence stays absent", func(t *testing.T) {
		e := newSettingsEnv(t)
		e.save(`{"schedulingEnabled":false}`)
		e.expect(`{"schedulingEnabled":false}`)
		e.save(`{"schedulingEnabled":true}`)
		e.expect(`{"schedulingEnabled":true}`)
		e.save(`{}`) // absence, the caller's "use default" signal, must not become a stored false
		e.expect(`{}`)
	})
	tw(t, f, "round-trips fleetView (true and false); keeps boolean, drops non-boolean", func(t *testing.T) {
		e := newSettingsEnv(t)
		e.save(`{"fleetView":false}`)
		e.expect(`{"fleetView":false}`)
		e.save(`{"fleetView":true}`)
		e.expect(`{"fleetView":true}`)
		e.writeProject(obj{"fleetView": "on"})
		e.expect(`{}`)
	})
	tw(t, f, "round-trips agentMentions modes; drops an unknown one", func(t *testing.T) {
		e := newSettingsEnv(t)
		for _, mode := range []string{"model", "direct", "off"} {
			e.save(`{"agentMentions":"` + mode + `"}`)
			e.expect(`{"agentMentions":"` + mode + `"}`)
		}
		e.writeProject(obj{"agentMentions": "on"})
		e.expect(`{}`)
	})
	tw(t, f, "reads the pre-mode agentMentions booleans as their modes", func(t *testing.T) {
		e := newSettingsEnv(t)
		e.writeProject(obj{"agentMentions": true})
		e.expect(`{"agentMentions":"model"}`)
		e.writeProject(obj{"agentMentions": false})
		e.expect(`{"agentMentions":"off"}`)
	})
	tw(t, f, "round-trips rememberAgents (true and false); keeps boolean, drops non-boolean", func(t *testing.T) {
		e := newSettingsEnv(t)
		e.save(`{"rememberAgents":false}`)
		e.expect(`{"rememberAgents":false}`)
		e.save(`{"rememberAgents":true}`)
		e.expect(`{"rememberAgents":true}`)
		e.writeProject(obj{"rememberAgents": "on"})
		e.expect(`{}`)
	})
	tw(t, f, "round-trips widgetMode; keeps valid values, drops invalid", func(t *testing.T) {
		e := newSettingsEnv(t)
		e.save(`{"widgetMode":"off"}`)
		e.expect(`{"widgetMode":"off"}`)
		e.save(`{"widgetMode":"background"}`)
		e.expect(`{"widgetMode":"background"}`)
		e.writeProject(obj{"widgetMode": "sideways"})
		e.expect(`{}`)
	})
	tw(t, f, "round-trips viewerMarkdown; keeps valid values, drops invalid", func(t *testing.T) {
		e := newSettingsEnv(t)
		e.save(`{"viewerMarkdown":"off"}`)
		e.expect(`{"viewerMarkdown":"off"}`)
		e.save(`{"viewerMarkdown":"all"}`)
		e.expect(`{"viewerMarkdown":"all"}`)
		e.writeProject(obj{"viewerMarkdown": "markdown"})
		e.expect(`{}`)
	})
	tw(t, f, "round-trips outputTranscript; drops non-boolean", func(t *testing.T) {
		e := newSettingsEnv(t)
		e.save(`{"outputTranscript":false}`)
		e.expect(`{"outputTranscript":false}`)
		e.save(`{"outputTranscript":true}`)
		e.expect(`{"outputTranscript":true}`)
		e.writeProject(obj{"outputTranscript": "no"})
		e.expect(`{}`)
	})
	tw(t, f, "round-trips backgroundByDefault (true and false), and absence stays absent", func(t *testing.T) {
		e := newSettingsEnv(t)
		e.save(`{"backgroundByDefault":false}`) // false is how a user restores the foreground default: it must survive a save
		e.expect(`{"backgroundByDefault":false}`)
		e.save(`{"backgroundByDefault":true}`)
		e.expect(`{"backgroundByDefault":true}`)
		e.save(`{}`)
		e.expect(`{}`)
	})
	tw(t, f, "sanitize drops non-boolean backgroundByDefault silently", func(t *testing.T) {
		e := newSettingsEnv(t)
		e.writeProject(obj{"backgroundByDefault": "yes"})
		e.expect(`{}`)
		e.writeProject(obj{"backgroundByDefault": 0})
		e.expect(`{}`)
	})
	tw(t, f, "round-trips worktreeIsolation; drops non-boolean", func(t *testing.T) {
		e := newSettingsEnv(t)
		e.save(`{"worktreeIsolation":false}`)
		e.expect(`{"worktreeIsolation":false}`)
		e.save(`{"worktreeIsolation":true}`)
		e.expect(`{"worktreeIsolation":true}`)
		e.writeProject(obj{"worktreeIsolation": "off"})
		e.expect(`{}`)
	})
	tw(t, f, "round-trips reportUsage and showCost; drops non-boolean", func(t *testing.T) {
		e := newSettingsEnv(t)
		e.save(`{"reportUsage":true,"showCost":true}`)
		e.expect(`{"reportUsage":true,"showCost":true}`)
		e.save(`{"reportUsage":false,"showCost":false}`)
		e.expect(`{"reportUsage":false,"showCost":false}`)
		e.writeProject(obj{"reportUsage": "on", "showCost": 1})
		e.expect(`{}`)
	})
	tw(t, f, "round-trips showModel; drops non-boolean", func(t *testing.T) {
		e := newSettingsEnv(t)
		e.save(`{"showModel":true}`)
		e.expect(`{"showModel":true}`)
		e.save(`{"showModel":false}`)
		e.expect(`{"showModel":false}`)
		e.writeProject(obj{"showModel": "on"})
		e.expect(`{}`)
	})
	tw(t, f, "round-trips workflowsEnabled; drops non-boolean", func(t *testing.T) {
		e := newSettingsEnv(t)
		e.save(`{"workflowsEnabled":true}`)
		e.expect(`{"workflowsEnabled":true}`)
		e.save(`{"workflowsEnabled":false}`)
		e.expect(`{"workflowsEnabled":false}`)
		e.writeProject(obj{"workflowsEnabled": "on"}) // dropped, not coerced: a truthy string must not switch the feature on
		e.expect(`{}`)
	})
	tw(t, f, "sanitize drops non-boolean schedulingEnabled silently", func(t *testing.T) {
		e := newSettingsEnv(t)
		e.writeProject(obj{"schedulingEnabled": "yes"})
		e.expect(`{}`)
		e.writeProject(obj{"schedulingEnabled": 1})
		e.expect(`{}`)
	})
	tw(t, f, "saveSettings writes only to the project file; global is untouched", func(t *testing.T) {
		e := newSettingsEnv(t)
		e.writeGlobal(obj{"maxConcurrent": 16})
		e.save(`{"maxConcurrent":2}`)
		var p, g map[string]any
		data, _ := os.ReadFile(e.projectFile())
		_ = json.Unmarshal(data, &p)
		data, _ = os.ReadFile(e.globalFile())
		_ = json.Unmarshal(data, &g)
		eq(t, p, obj{"maxConcurrent": 2.0})
		eq(t, g, obj{"maxConcurrent": 16.0})
	})
	tw(t, f, "saveSettings creates <cwd>/.pi/ when missing", func(t *testing.T) {
		e := newSettingsEnv(t)
		_, err := os.Stat(filepath.Join(e.project, ".pi"))
		eq(t, os.IsNotExist(err), true)
		e.save(`{"maxConcurrent":4}`)
		_, err = os.Stat(e.projectFile())
		eq(t, err, nil)
	})
	tw(t, f, "round-trips defaultMaxTurns: 0 (unlimited marker)", func(t *testing.T) {
		e := newSettingsEnv(t)
		e.save(`{"defaultMaxTurns":0}`)
		e.expect(`{"defaultMaxTurns":0}`)
	})
	tw(t, f, "ignores unknown extra fields on load (forward-compat)", func(t *testing.T) {
		e := newSettingsEnv(t)
		e.writeProject(obj{"maxConcurrent": 2, "futureField": "ignored"})
		e.expect(`{"maxConcurrent":2}`) // the sanitizer strips what it does not know
	})
	tw(t, f, "composes partial global + partial project correctly", func(t *testing.T) {
		e := newSettingsEnv(t)
		e.writeGlobal(obj{"graceTurns": 10})
		e.writeProject(obj{"maxConcurrent": 2})
		e.expect(`{"graceTurns":10,"maxConcurrent":2}`)
	})
	tw(t, f, "drops maxConcurrent < 1", func(t *testing.T) {
		e := newSettingsEnv(t)
		e.writeProject(obj{"maxConcurrent": 0, "graceTurns": 5})
		e.expect(`{"graceTurns":5}`)
	})
	tw(t, f, "drops negative maxConcurrent", func(t *testing.T) {
		e := newSettingsEnv(t)
		e.writeProject(obj{"maxConcurrent": -3})
		e.expect(`{}`)
	})
	tw(t, f, "drops non-integer maxConcurrent (floats, NaN, strings)", func(t *testing.T) {
		e := newSettingsEnv(t)
		for _, bad := range []any{3.5, "four", nil} {
			e.writeProject(obj{"maxConcurrent": bad})
			eq(t, loadSettings(e.project).MaxConcurrent == nil, true)
		}
	})
	tw(t, f, "keeps maxConcurrentForeground: 0 (explicit unlimited)", func(t *testing.T) {
		e := newSettingsEnv(t)
		e.writeProject(obj{"maxConcurrentForeground": 0})
		e.expect(`{"maxConcurrentForeground":0}`)
	})
	tw(t, f, "drops out-of-range or non-integer maxConcurrentForeground", func(t *testing.T) {
		e := newSettingsEnv(t)
		for _, bad := range []any{-1, 1025, 1.5, "four", nil} {
			e.writeProject(obj{"maxConcurrentForeground": bad})
			eq(t, loadSettings(e.project).MaxConcurrentForeground == nil, true)
		}
	})
	tw(t, f, "accepts defaultMaxTurns: 0 (explicit unlimited)", func(t *testing.T) {
		e := newSettingsEnv(t)
		e.writeProject(obj{"defaultMaxTurns": 0})
		e.expect(`{"defaultMaxTurns":0}`)
	})
	tw(t, f, "drops negative defaultMaxTurns", func(t *testing.T) {
		e := newSettingsEnv(t)
		e.writeProject(obj{"defaultMaxTurns": -1})
		e.expect(`{}`)
	})
	tw(t, f, "drops graceTurns < 1", func(t *testing.T) {
		e := newSettingsEnv(t)
		e.writeProject(obj{"graceTurns": 0})
		e.expect(`{}`)
	})
	tw(t, f, "keeps maxSubagentDepth 0 (nesting off) but drops negative, fractional, and over-ceiling values", func(t *testing.T) {
		e := newSettingsEnv(t)
		e.writeProject(obj{"maxSubagentDepth": 0})
		e.expect(`{"maxSubagentDepth":0}`)
		e.writeProject(obj{"maxSubagentDepth": -1})
		e.expect(`{}`)
		e.writeProject(obj{"maxSubagentDepth": 1.5})
		e.expect(`{}`)
		e.writeProject(obj{"maxSubagentDepth": 17})
		e.expect(`{}`)
	})
	tw(t, f, "accepts `none` and `false` as the disabled fallback, nothing else", func(t *testing.T) {
		e := newSettingsEnv(t)
		for _, spelling := range []any{"none", "NONE", " none ", false} {
			e.writeProject(obj{"fallbackSubagent": spelling})
			eq(t, strings.ToLower(*loadSettings(e.project).FallbackSubagent), "none")
		}
		e.writeProject(obj{"fallbackSubagent": "off"}) // every string is an agent name: a mistaken "off" fails loudly at dispatch
		e.expect(`{"fallbackSubagent":"off"}`)
	})
	tw(t, f, "drops values that aren't a string or `false`, without coercing them", func(t *testing.T) {
		e := newSettingsEnv(t)
		for _, junk := range []any{[]any{"none"}, nil, 42, true, obj{}} {
			e.writeProject(obj{"fallbackSubagent": junk})
			e.expect(`{}`)
		}
	})
	tw(t, f, "keeps a named fallback agent and drops non-strings", func(t *testing.T) {
		e := newSettingsEnv(t)
		e.writeProject(obj{"fallbackSubagent": "  my-router  "})
		e.expect(`{"fallbackSubagent":"my-router"}`)
		e.writeProject(obj{"fallbackSubagent": 42})
		e.expect(`{}`)
		e.writeProject(obj{"fallbackSubagent": "   "})
		e.expect(`{}`)
	})
	tw(t, f, "drops invalid defaultJoinMode values", func(t *testing.T) {
		e := newSettingsEnv(t)
		for _, bad := range []any{"invalid", 42, ""} {
			e.writeProject(obj{"defaultJoinMode": bad})
			e.expect(`{}`)
		}
	})
	tw(t, f, "accepts all three valid join modes", func(t *testing.T) {
		e := newSettingsEnv(t)
		for _, mode := range []string{"async", "group", "smart"} {
			e.writeProject(obj{"defaultJoinMode": mode})
			e.expect(`{"defaultJoinMode":"` + mode + `"}`)
		}
	})
	tw(t, f, "accepts scopeModels boolean (true and false)", func(t *testing.T) {
		e := newSettingsEnv(t)
		e.writeProject(obj{"scopeModels": true})
		e.expect(`{"scopeModels":true}`)
		e.writeProject(obj{"scopeModels": false})
		e.expect(`{"scopeModels":false}`)
	})
	tw(t, f, "accepts strictAgentFiles boolean (true and false)", func(t *testing.T) {
		e := newSettingsEnv(t)
		e.writeProject(obj{"strictAgentFiles": true})
		e.expect(`{"strictAgentFiles":true}`)
		e.writeProject(obj{"strictAgentFiles": false})
		e.expect(`{"strictAgentFiles":false}`)
	})
	tw(t, f, "drops non-boolean strictAgentFiles", func(t *testing.T) {
		e := newSettingsEnv(t)
		for _, bad := range []any{"yes", 1} {
			e.writeProject(obj{"strictAgentFiles": bad})
			eq(t, loadSettings(e.project).StrictAgentFiles == nil, true)
		}
	})
	tw(t, f, "drops non-boolean scopeModels", func(t *testing.T) {
		e := newSettingsEnv(t)
		for _, bad := range []any{"yes", 1, nil} {
			e.writeProject(obj{"scopeModels": bad})
			eq(t, loadSettings(e.project).ScopeModels == nil, true)
		}
	})
	tw(t, f, "accepts disableDefaultAgents boolean (true and false)", func(t *testing.T) {
		e := newSettingsEnv(t)
		e.writeProject(obj{"disableDefaultAgents": true})
		e.expect(`{"disableDefaultAgents":true}`)
		e.writeProject(obj{"disableDefaultAgents": false})
		e.expect(`{"disableDefaultAgents":false}`)
	})
	tw(t, f, "drops non-boolean disableDefaultAgents", func(t *testing.T) {
		e := newSettingsEnv(t)
		for _, bad := range []any{"yes", 1, nil} {
			e.writeProject(obj{"disableDefaultAgents": bad})
			eq(t, loadSettings(e.project).DisableDefaultAgents == nil, true)
		}
	})
	tw(t, f, "accepts all valid toolDescriptionMode values", func(t *testing.T) {
		e := newSettingsEnv(t)
		for _, mode := range []string{"full", "compact", "custom"} {
			e.writeProject(obj{"toolDescriptionMode": mode})
			e.expect(`{"toolDescriptionMode":"` + mode + `"}`)
		}
	})
	tw(t, f, "drops invalid toolDescriptionMode", func(t *testing.T) {
		e := newSettingsEnv(t)
		for _, bad := range []any{"tiny", true, nil} {
			e.writeProject(obj{"toolDescriptionMode": bad})
			eq(t, loadSettings(e.project).ToolDescriptionMode == nil, true)
		}
	})
	tw(t, f, "returns {} when the JSON root is not an object (array, string, null)", func(t *testing.T) {
		e := newSettingsEnv(t)
		_ = os.MkdirAll(filepath.Join(e.project, ".pi"), 0o755)
		for _, body := range []string{`["not", "an", "object"]`, `"just a string"`, "null"} {
			_ = os.WriteFile(e.projectFile(), []byte(body), 0o644)
			e.expect(`{}`)
		}
	})
	tw(t, f, "keeps valid fields while dropping invalid siblings", func(t *testing.T) {
		e := newSettingsEnv(t)
		e.writeProject(obj{"maxConcurrent": 4, "defaultMaxTurns": -5, "graceTurns": 3, "defaultJoinMode": "nope"})
		e.expect(`{"maxConcurrent":4,"graceTurns":3}`)
	})
	tw(t, f, "accepts values at the ceiling (maxConcurrent=1024, defaultMaxTurns=10000, graceTurns=1000)", func(t *testing.T) {
		e := newSettingsEnv(t)
		e.writeProject(obj{"maxConcurrent": 1024, "defaultMaxTurns": 10000, "graceTurns": 1000})
		e.expect(`{"maxConcurrent":1024,"defaultMaxTurns":10000,"graceTurns":1000}`)
	})
	tw(t, f, "drops values above the ceiling", func(t *testing.T) {
		e := newSettingsEnv(t)
		e.writeProject(obj{"maxConcurrent": 1025})
		eq(t, loadSettings(e.project).MaxConcurrent == nil, true)
		e.writeProject(obj{"defaultMaxTurns": 10001})
		eq(t, loadSettings(e.project).DefaultMaxTurns == nil, true)
		e.writeProject(obj{"graceTurns": 1001})
		eq(t, loadSettings(e.project).GraceTurns == nil, true)
	})
	tw(t, f, "drops absurdly large values (e.g. 1e6)", func(t *testing.T) {
		e := newSettingsEnv(t)
		e.writeProject(obj{"maxConcurrent": 1000000, "defaultMaxTurns": 1000000, "graceTurns": 1000000})
		e.expect(`{}`)
	})
	tw(t, f, "saveSettings returns true on success", func(t *testing.T) {
		e := newSettingsEnv(t)
		eq(t, e.save(`{"maxConcurrent":2}`), true)
		var p map[string]any
		data, _ := os.ReadFile(e.projectFile())
		_ = json.Unmarshal(data, &p)
		eq(t, p, obj{"maxConcurrent": 2.0})
	})
	tw(t, f, "saveSettings returns false when the target dir cannot be created", func(t *testing.T) {
		e := newSettingsEnv(t)
		notDir := filepath.Join(t.TempDir(), "file-posing-as-cwd") // a regular file where the parent of the settings file would go
		_ = os.WriteFile(notDir, nil, 0o644)
		eq(t, saveSettings(parseSettings(t, `{"maxConcurrent":1}`), notDir), false)
		_ = e
	})
	tw(t, f, "warns to console.warn when an existing file is malformed", func(t *testing.T) {
		e := newSettingsEnv(t)
		var warned []string
		settingsWarn = func(m string) { warned = append(warned, m) }
		_ = os.MkdirAll(filepath.Join(e.project, ".pi"), 0o755)
		_ = os.WriteFile(e.projectFile(), []byte("not valid json {{{"), 0o644)
		e.expect(`{}`)
		eq(t, len(warned), 1)
		eq(t, strings.Contains(warned[0], "Ignoring malformed settings"), true)
	})
	tw(t, f, "does NOT warn when a file is simply missing", func(t *testing.T) {
		e := newSettingsEnv(t)
		var warned []string
		settingsWarn = func(m string) { warned = append(warned, m) }
		e.expect(`{}`)
		eq(t, len(warned), 0)
	})

	apply := func(t *testing.T, js string) calls {
		ap, c := recordingAppliers()
		applySettings(parseSettings(t, js), ap)
		return c
	}
	tw(t, f, "applies maxConcurrentForeground, including an explicit 0", func(t *testing.T) {
		eq(t, apply(t, `{"maxConcurrentForeground":3}`)["maxConcurrentForeground"], []any{3})
		eq(t, apply(t, `{"maxConcurrentForeground":0}`)["maxConcurrentForeground"], []any{0}) // truthiness would skip it
		eq(t, len(apply(t, `{}`)["maxConcurrentForeground"]), 0)
	})
	tw(t, f, "applies reportUsage and showCost", func(t *testing.T) {
		c := apply(t, `{"reportUsage":true,"showCost":true}`)
		eq(t, c["reportUsage"], []any{true})
		eq(t, c["showCost"], []any{true})
		c = apply(t, `{"reportUsage":false,"showCost":false}`)
		eq(t, c["reportUsage"], []any{false})
		eq(t, c["showCost"], []any{false})
	})
	tw(t, f, "applies showModel", func(t *testing.T) {
		eq(t, apply(t, `{"showModel":true}`)["showModel"], []any{true})
		eq(t, apply(t, `{"showModel":false}`)["showModel"], []any{false})
	})
	tw(t, f, "is a no-op on an empty settings object", func(t *testing.T) {
		eq(t, len(apply(t, `{}`)), 0)
	})
	tw(t, f, "applies fallbackSubagent through to the registry", func(t *testing.T) {
		eq(t, apply(t, `{"fallbackSubagent":"none"}`)["fallbackSubagent"], []any{"none"})
	})
	tw(t, f, "applies only the fields that are present", func(t *testing.T) {
		c := apply(t, `{"maxConcurrent":4,"graceTurns":3,"maxSubagentDepth":1}`)
		eq(t, c["maxConcurrent"], []any{4})
		eq(t, c["graceTurns"], []any{3})
		eq(t, c["maxSubagentDepth"], []any{1})
		eq(t, len(c), 3)
	})
	tw(t, f, "applies all fields when all are present", func(t *testing.T) {
		c := apply(t, `{"maxConcurrent":8,"defaultMaxTurns":50,"graceTurns":7,"defaultJoinMode":"group","schedulingEnabled":false,"scopeModels":true,
			"disableDefaultAgents":true,"toolDescriptionMode":"compact","fleetView":false,"widgetMode":"off"}`)
		eq(t, c["maxConcurrent"], []any{8})
		eq(t, c["defaultMaxTurns"], []any{50})
		eq(t, c["graceTurns"], []any{7})
		eq(t, c["defaultJoinMode"], []any{"group"})
		eq(t, c["schedulingEnabled"], []any{false})
		eq(t, c["scopeModels"], []any{true})
		eq(t, len(c["strictAgentFiles"]), 0) // absent from this snapshot
		eq(t, c["disableDefaultAgents"], []any{true})
		eq(t, c["toolDescriptionMode"], []any{"compact"})
		eq(t, c["fleetView"], []any{false})
		eq(t, c["widgetMode"], []any{"off"})
	})
	tw(t, f, "applies strictAgentFiles; skips it when absent", func(t *testing.T) {
		eq(t, apply(t, `{"strictAgentFiles":true}`)["strictAgentFiles"], []any{true})
		eq(t, len(apply(t, `{}`)["strictAgentFiles"]), 0)
	})
	tw(t, f, "applies widgetMode; skips it when absent", func(t *testing.T) {
		eq(t, apply(t, `{"widgetMode":"off"}`)["widgetMode"], []any{"off"})
		eq(t, len(apply(t, `{}`)["widgetMode"]), 0)
	})
	tw(t, f, "applies viewerMarkdown; skips it when absent", func(t *testing.T) {
		eq(t, apply(t, `{"viewerMarkdown":"all"}`)["viewerMarkdown"], []any{"all"})
		eq(t, len(apply(t, `{}`)["viewerMarkdown"]), 0)
	})
	tw(t, f, "applies fleetView (true and false); skips it when absent", func(t *testing.T) {
		eq(t, apply(t, `{"fleetView":true}`)["fleetView"], []any{true})
		eq(t, apply(t, `{"fleetView":false}`)["fleetView"], []any{false})
		eq(t, len(apply(t, `{}`)["fleetView"]), 0)
	})
	tw(t, f, "applies agentMentions; skips it when absent", func(t *testing.T) {
		eq(t, apply(t, `{"agentMentions":"direct"}`)["agentMentions"], []any{"direct"})
		eq(t, len(apply(t, `{}`)["agentMentions"]), 0)
	})
	tw(t, f, "applies rememberAgents; skips it when absent", func(t *testing.T) {
		eq(t, apply(t, `{"rememberAgents":false}`)["rememberAgents"], []any{false})
		eq(t, len(apply(t, `{}`)["rememberAgents"]), 0)
	})
	tw(t, f, "applies scopeModels: false", func(t *testing.T) {
		eq(t, apply(t, `{"scopeModels":false}`)["scopeModels"], []any{false})
	})
	tw(t, f, "applies disableDefaultAgents: false", func(t *testing.T) {
		eq(t, apply(t, `{"disableDefaultAgents":false}`)["disableDefaultAgents"], []any{false})
	})
	tw(t, f, "applies toolDescriptionMode", func(t *testing.T) {
		eq(t, apply(t, `{"toolDescriptionMode":"full"}`)["toolDescriptionMode"], []any{"full"})
	})
	tw(t, f, "applies outputTranscript (both true and false)", func(t *testing.T) {
		eq(t, apply(t, `{"outputTranscript":false}`)["outputTranscript"], []any{false})
		eq(t, apply(t, `{"outputTranscript":true}`)["outputTranscript"], []any{true})
	})
	tw(t, f, "applies worktreeIsolation (both true and false)", func(t *testing.T) {
		eq(t, apply(t, `{"worktreeIsolation":false}`)["worktreeIsolation"], []any{false})
		eq(t, apply(t, `{"worktreeIsolation":true}`)["worktreeIsolation"], []any{true})
	})
	tw(t, f, "applies defaultMaxTurns: 0 as the explicit unlimited marker", func(t *testing.T) {
		eq(t, apply(t, `{"defaultMaxTurns":0}`)["defaultMaxTurns"], []any{0})
	})
	tw(t, f, "calls setBackgroundByDefault with either boolean", func(t *testing.T) {
		eq(t, apply(t, `{"backgroundByDefault":false}`)["backgroundByDefault"], []any{false})
		eq(t, apply(t, `{"backgroundByDefault":true}`)["backgroundByDefault"], []any{true})
	})
	tw(t, f, "does not call setBackgroundByDefault when the field is absent", func(t *testing.T) {
		eq(t, len(apply(t, `{"maxConcurrent":4}`)["backgroundByDefault"]), 0)
	})
	tw(t, f, "calls setSchedulingEnabled(true) when schedulingEnabled is true", func(t *testing.T) {
		eq(t, apply(t, `{"schedulingEnabled":true}`)["schedulingEnabled"], []any{true})
	})
	tw(t, f, "calls setSchedulingEnabled(false) when schedulingEnabled is false", func(t *testing.T) {
		eq(t, apply(t, `{"schedulingEnabled":false}`)["schedulingEnabled"], []any{false})
	})
	tw(t, f, "does not call setSchedulingEnabled when the field is absent", func(t *testing.T) {
		eq(t, len(apply(t, `{"maxConcurrent":4}`)["schedulingEnabled"]), 0)
	})
	tw(t, f, "returns info-level toast with the plain message on success", func(t *testing.T) {
		msg, level := persistToastFor("Max concurrency set to 7", true)
		eq(t, [2]string{msg, level}, [2]string{"Max concurrency set to 7", "info"})
	})
	tw(t, f, "returns warning-level toast with session-only suffix on failure", func(t *testing.T) {
		msg, level := persistToastFor("Max concurrency set to 7", false)
		eq(t, [2]string{msg, level}, [2]string{"Max concurrency set to 7 (session only; failed to persist)", "warning"})
	})

	type emitted struct {
		event   string
		payload any
	}
	tw(t, f, "loads, applies, and emits subagents:settings_loaded with merged settings", func(t *testing.T) {
		e := newSettingsEnv(t)
		e.writeGlobal(obj{"maxConcurrent": 16})
		e.writeProject(obj{"graceTurns": 7})
		ap, c := recordingAppliers()
		var got []emitted
		result := applyAndEmitLoaded(ap, func(ev string, p any) { got = append(got, emitted{ev, p}) }, e.project)
		eq(t, c["maxConcurrent"], []any{16})
		eq(t, c["graceTurns"], []any{7})
		eq(t, len(c["defaultMaxTurns"]), 0)
		eq(t, len(c["defaultJoinMode"]), 0)
		eq(t, got, []emitted{{"subagents:settings_loaded", obj{"settings": obj{"maxConcurrent": 16.0, "graceTurns": 7.0}}}})
		eq(t, result.payload(), obj{"maxConcurrent": 16.0, "graceTurns": 7.0})
	})
	tw(t, f, "still emits the event when both files are missing (payload carries {})", func(t *testing.T) {
		e := newSettingsEnv(t)
		ap, c := recordingAppliers()
		var got []emitted
		result := applyAndEmitLoaded(ap, func(ev string, p any) { got = append(got, emitted{ev, p}) }, e.project)
		eq(t, got, []emitted{{"subagents:settings_loaded", obj{"settings": obj{}}}})
		eq(t, result.payload(), obj{})
		eq(t, len(c), 0) // no setters fired: defaults preserved
	})
	tw(t, f, "persists, emits with persisted=true, and returns info toast on success", func(t *testing.T) {
		e := newSettingsEnv(t)
		var got []emitted
		snapshot := parseSettings(t, `{"maxConcurrent":5,"graceTurns":2}`)
		msg, level := saveAndEmitChanged(snapshot, "Max concurrency set to 5", func(ev string, p any) { got = append(got, emitted{ev, p}) }, e.project)
		eq(t, got, []emitted{{"subagents:settings_changed", obj{"settings": obj{"maxConcurrent": 5.0, "graceTurns": 2.0}, "persisted": true}}})
		eq(t, [2]string{msg, level}, [2]string{"Max concurrency set to 5", "info"})
		var p map[string]any
		data, _ := os.ReadFile(e.projectFile())
		_ = json.Unmarshal(data, &p)
		eq(t, p, obj{"maxConcurrent": 5.0, "graceTurns": 2.0}) // the file is actually written
	})
	tw(t, f, "emits with persisted=false and returns warning toast on save failure", func(t *testing.T) {
		notDir := filepath.Join(t.TempDir(), "file-posing-as-cwd")
		_ = os.WriteFile(notDir, nil, 0o644)
		var got []emitted
		msg, level := saveAndEmitChanged(parseSettings(t, `{"maxConcurrent":5}`), "Max concurrency set to 5", func(ev string, p any) { got = append(got, emitted{ev, p}) }, notDir)
		eq(t, got, []emitted{{"subagents:settings_changed", obj{"settings": obj{"maxConcurrent": 5.0}, "persisted": false}}})
		eq(t, [2]string{msg, level}, [2]string{"Max concurrency set to 5 (session only; failed to persist)", "warning"})
	})
}
