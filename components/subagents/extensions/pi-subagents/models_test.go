package pi_subagents

import (
	"path/filepath"
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
		model: map[string]any{"scout": "p/cheap", "worker": false}, thinking: map[string]any{"scout": "off", "worker": false}},
		"the user's, trimmed; other override fields are not read")

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
	t.Setenv("PIG_EXT_HOST_INFO", `{"agentDir": "`+filepath.Join(home, ".pig", "agent")+`", "configDirName": ".pig"}`)

	writeFile(t, piUser, `{"subagents": {"defaultModel": "pi/default", "agentOverrides": {"scout": {"model": "pi/scout", "thinking": "low"}}}}`)
	writeFile(t, pigUser, `{"subagents": {"agentOverrides": {"scout": {"model": "pig/scout"}}}}`)
	d, err := subagentSettingsFor(cwd, ScopeBoth)
	eq(t, err, nil, "error")
	eq(t, []any{d.defaultModel, d.model["scout"], d.thinking["scout"]}, []any{"pi/default", "pig/scout", "low"},
		"the host's user file over Pi's, field by field")
}
