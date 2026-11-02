package pi_subagents

import (
	"path/filepath"
	"strings"
	"testing"
)

// The original's `subagents.defaultModel`, `defaultThinking` and `agentOverrides.<name>.model` / `thinking` settings.

func TestModelSettings(t *testing.T) {
	home := withTempHome(t)
	cwd := tmp(t)
	userSettings := filepath.Join(home, ".pi", "agent", "settings.json")
	projectSettings := filepath.Join(cwd, ".pi", "settings.json")

	writeFile(t, userSettings, `{"subagents": {"defaultModel": " p/default ", "defaultThinking": "low", "agentOverrides": {
		"scout": {"model": "p/cheap", "thinking": "off"}, "worker": {"model": false, "thinking": false}, "oracle": {"tools": "read"}}}}`)
	d, err := subagentSettingsFor(cwd, ScopeBoth)
	eq(t, err, nil, "error")
	eq(t, d, subagentSettings{defaultModel: "p/default", defaultThinking: "low",
		model: map[string]any{"scout": "p/cheap", "worker": false}, thinking: map[string]any{"scout": "off", "worker": false},
		agentOverrides: map[string]map[string]any{"scout": {"model": "p/cheap", "thinking": "off"}, "worker": {"model": false, "thinking": false}, "oracle": {"tools": "read"}}},
		"the user's, trimmed; other override fields are kept but not applied")

	scout := d.apply(AgentConfig{Name: "scout", Model: "p/frontmatter", Thinking: "high"})
	eq(t, []any{scout.Model, scout.Thinking}, []any{"p/cheap", "off"}, "an override wins over the definition")
	worker := d.apply(AgentConfig{Name: "worker", Model: "p/frontmatter", Thinking: "high"})
	eq(t, []any{worker.Model, worker.Thinking}, []any{"p/default", "low"}, "false clears the definition's value, and the default fills it")
	own := d.apply(AgentConfig{Name: "other", Model: "p/own", Thinking: false})
	eq(t, []any{own.Model, own.Thinking}, []any{"p/own", false}, "the definition's model, and thinking off, win over the defaults")
	bare := d.apply(AgentConfig{Name: "other"})
	eq(t, []any{bare.Model, bare.Thinking}, []any{"p/default", "low"}, "the defaults fill what the definition leaves unset")
	eq(t, buildChildArgs(&scout, ""), []string{"--print", "--model", "p/cheap", "--thinking", "off"}, "the child gets them")

	eq(t, subagentSettings{}.apply(AgentConfig{Name: "a"}).Thinking, nil, "without a default, thinking stays unset")

	writeFile(t, projectSettings, `{"subagents": {"defaultModel": "p/project", "defaultThinking": "high", "agentOverrides": {"scout": {"thinking": "minimal"}}}}`)
	d, _ = subagentSettingsFor(cwd, ScopeBoth)
	eq(t, []any{d.defaultModel, d.defaultThinking, d.model["scout"], d.thinking["scout"], d.thinking["worker"]},
		[]any{"p/project", "high", "p/cheap", "minimal", false}, "the project's where it sets one, field by field")
	d, _ = subagentSettingsFor(cwd, ScopeUser)
	eq(t, []any{d.defaultModel, d.thinking["scout"]}, []any{"p/default", "off"}, "scope user reads no project settings")
	d, _ = subagentSettingsFor(cwd, ScopeProject)
	eq(t, []any{d.defaultModel, d.model["scout"]}, []any{"p/project", nil}, "scope project reads no user settings")
	writeFile(t, projectSettings, `{"subagents": {"agentOverrides": {"scout": {"model": "p/project-scout"}}}}`)
	d, _ = subagentSettingsFor(cwd, ScopeBoth)
	eq(t, d.model["scout"], any("p/project-scout"), "the project's model wins where both files set one")

	for body, msg := range map[string]string{
		`{"subagents": {"defaultModel": " "}}`:                             "Subagent settings in '" + projectSettings + "' have invalid 'defaultModel'; expected a non-empty string.",
		`{"subagents": {"defaultThinking": 1}}`:                            "have invalid 'defaultThinking'; expected a non-empty string.",
		`{"subagents": {"agentOverrides": {"scout": "p/x"}}}`:              "Builtin override 'scout' in '" + projectSettings + "' must be an object.",
		`{"subagents": {"agentOverrides": {"scout": {"model": true}}}}`:    "Builtin override 'scout' in '" + projectSettings + "' has invalid 'model'; expected a string or false.",
		`{"subagents": {"agentOverrides": {"scout": {"thinking": null}}}}`: "has invalid 'thinking'; expected a string or false.",
	} {
		writeFile(t, projectSettings, body)
		_, err = subagentSettingsFor(cwd, ScopeBoth)
		hasMatch(t, err, msg)
	}
	writeFile(t, projectSettings, `{"subagents": {"agentOverrides": ["scout"]}}`)
	_, err = subagentSettingsFor(cwd, ScopeBoth)
	eq(t, err, nil, "an agentOverrides that is not an object is ignored, as in the original")
}

func TestModelSettingsFromTheHost(t *testing.T) {
	home := withTempHome(t)
	cwd := tmp(t)
	pigUser := filepath.Join(home, ".pig", "agent", "settings.json")
	piUser := filepath.Join(home, ".pi", "agent", "settings.json")
	t.Setenv("PIG_EXT_HOST_INFO", hostInfoJSON(filepath.Join(home, ".pig", "agent"), ".pig"))

	writeFile(t, piUser, `{"subagents": {"defaultModel": "pi/default", "agentOverrides": {"scout": {"model": "pi/scout", "thinking": "low"}}}}`)
	writeFile(t, pigUser, `{"subagents": {"agentOverrides": {"scout": {"model": "pig/scout"}}}}`)
	d, err := subagentSettingsFor(cwd, ScopeBoth)
	eq(t, err, nil, "error")
	eq(t, []any{d.defaultModel, d.model["scout"], d.thinking["scout"]}, []any{"pi/default", "pig/scout", "low"},
		"the host's user file over Pi's, field by field")
}

// The models, capabilities and guide actions.

func TestModelsAction(t *testing.T) {
	cwd, _ := mgmtHost(t)
	writeFile(t, filepath.Join(cwd, ".pi", "agents", "custom.md"),
		"---\nname: custom\ndescription: Custom.\nmodel: p/known\naliases: c1, c2\nthinking: high\n---\nYou help.\n")
	writeFile(t, filepath.Join(cwd, ".pi", "agents", "inherit.md"), agentMD("inherit", "Inherits.", "Inherit."))

	registry := []availableModel{{Provider: "p", ID: "known", FullID: "p/known"}, {Provider: "q", ID: "solo", FullID: "q/solo"}}
	// No session model: an inheriting agent is unresolved and says so.
	r := modelsAction(modelsView{models: registry}, cwd, map[string]any{})
	if r.isError {
		t.Fatalf("models: %s", r.text)
	}
	for _, want := range []string{
		"Subagent models",
		"Current session model:",
		"  (unavailable)",
		"custom",
		"  model:",
		"    p/known",
		"  source: project agent config",
		"  thinking: high",
		"c1",
		"  model:",
		"inherit",
		"  source: inherit requested, but no current session model is available",
		"  thinking: default",
		"Available models in this session's registry (copy an exact provider/id when passing model):",
		"  p/known",
		"  q/solo",
		"Use an exact provider/id from this list when you pass model; bare ids resolve only when unique in the registry.",
	} {
		if !strings.Contains(r.text, want) {
			t.Errorf("models missing %q:\n%s", want, r.text)
		}
	}

	// With a session model, the inheriting agent resolves to it; a bare id
	// resolves through the registry; an unknown model stays as declared.
	r = modelsAction(modelsView{current: "p/current"}, cwd, map[string]any{"agent": "inherit"})
	if r.isError {
		t.Fatalf("models one agent: %s", r.text)
	}
	for _, want := range []string{
		"Subagent model",
		"Agent: inherit",
		"Effective model:",
		"  p/current",
		"Source: inherits current session model",
		"Thinking: default",
		"Current session model:",
		"  p/current",
	} {
		if !strings.Contains(r.text, want) {
			t.Errorf("one agent missing %q:\n%s", want, r.text)
		}
	}
	eq(t, resolveModelCandidate("solo", registry), "q/solo", "a bare unique id resolves")
	eq(t, resolveModelCandidate("missing", registry), "missing", "an unknown model stays")
	eq(t, resolveModelCandidate("known:high", registry), "p/known:high", "the thinking suffix survives")

	// Scope validation matches the original.
	r = modelsAction(modelsView{current: "p/current"}, cwd, map[string]any{"agentScope": "team"})
	eq(t, r.text, "agentScope must be 'user', 'project', or 'both' for models.", "scope error")
	// A disabled builtin is hidden from the effective set (a custom file
	// shadows its override, as in the original, so disable a builtin here).
	r = disableAction(cwd, map[string]any{"agent": "scout"})
	eq(t, r.isError, false, "disable")
	r = modelsAction(modelsView{current: "p/current"}, cwd, map[string]any{})
	if strings.Contains(r.text, "scout") {
		t.Errorf("disabled agent still listed:\n%s", r.text)
	}
	r = enableAction(cwd, map[string]any{"agent": "scout"})
	eq(t, r.isError, false, "enable")
}

func TestCapabilitiesList(t *testing.T) {
	cwd, _ := mgmtHost(t)
	writeFile(t, filepath.Join(cwd, ".pi", "agents", "worker-two.md"),
		"---\nname: worker-two\ndescription: Works.\ntools: read, grep, mcp:docs\nexcludeTools: edit\nmodel: p/m\nthinking: false\nacceptance: checked\nacceptanceRole: writer\ndefaultContext: fresh\n---\nWork.\n")
	r := capabilitiesText(cwd, map[string]any{})
	if r.isError {
		t.Fatalf("capabilities: %s", r.text)
	}
	if !strings.HasPrefix(r.text, "Executable agents (capabilities):") {
		t.Errorf("heading:\n%s", r.text)
	}
	line := "- worker-two (project, context: fresh): Description: Works.; Tools: read, grep, mcp:docs; excludes: edit; Model: p/m; Thinking: off; Acceptance: checked; Acceptance role: writer"
	if !strings.Contains(r.text, line) {
		t.Errorf("capabilities line missing:\n%s", r.text)
	}
	// An agent with no declared tools says default/ambient.
	writeFile(t, filepath.Join(cwd, ".pi", "agents", "plain.md"), agentMD("plain", "Plain.", "Plain."))
	if !strings.Contains(r.text, "Tools: default/ambient") {
		t.Errorf("default tools:\n%s", r.text)
	}
}

func TestGuideAction(t *testing.T) {
	r := guideAction(map[string]any{})
	if r.isError || !strings.HasPrefix(r.text, "# pi-subagents (Go port)") {
		t.Errorf("overview: %q", r.text)
	}
	for _, topic := range portGuideTopics() {
		r = guideAction(map[string]any{"topic": topic})
		if r.isError || r.text == "" {
			t.Errorf("topic %s: %q", topic, r.text)
		}
	}
	r = guideAction(map[string]any{"topic": "nope"})
	if !r.isError || !strings.HasPrefix(r.text, "Unknown subagents guide topic 'nope'. Valid topics: ") || !strings.HasSuffix(r.text, "No files were changed.") {
		t.Errorf("unknown topic: %q", r.text)
	}
	// A topic that documents an unported runtime says so.
	r = guideAction(map[string]any{"topic": "workflows"})
	if !strings.Contains(r.text, "npm:pi-subagents@0.73.1") {
		t.Errorf("unported topic: %q", r.text)
	}
}

func TestModelWarningOnCreateAndUpdate(t *testing.T) {
	cwd, _ := mgmtHost(t)
	registry := []availableModel{{Provider: "p", ID: "known", FullID: "p/known"}}
	r := createAction(cwd, map[string]any{"config": map[string]any{"name": "x", "description": "d", "scope": "project", "model": "p/known"}}, registry)
	if r.isError || strings.Contains(r.text, "Warning: model") {
		t.Errorf("known model warns: %q", r.text)
	}
	r = createAction(cwd, map[string]any{"config": map[string]any{"name": "y", "description": "d", "scope": "project", "model": "p/other"}}, registry)
	if !strings.Contains(r.text, "Warning: model 'p/other' is not in the current model registry.") {
		t.Errorf("unknown model warning: %q", r.text)
	}
	r = updateAction(cwd, map[string]any{"agent": "y", "config": map[string]any{"model": "still-missing"}}, registry)
	if !strings.Contains(r.text, "Warning: model 'still-missing' is not in the current model registry.") {
		t.Errorf("update warning: %q", r.text)
	}
}
