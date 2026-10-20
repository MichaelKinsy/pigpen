package pi_subagents

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// Twins of test/unit/watchdog-settings.test.ts.

type wdSettingsEnv struct{ home, project string }

func newWdSettingsEnv(t *testing.T) wdSettingsEnv {
	t.Helper()
	home := withTempHome(t)
	return wdSettingsEnv{home: home, project: tmp(t)}
}

func (e wdSettingsEnv) userPath() string {
	return filepath.Join(e.home, ".pi", "agent", "settings.json")
}
func (e wdSettingsEnv) projectPath() string {
	return filepath.Join(e.project, ".pi", "settings.json")
}

func (e wdSettingsEnv) writeUser(t *testing.T, raw string) {
	t.Helper()
	writeFile(t, e.userPath(), raw)
}
func (e wdSettingsEnv) writeProject(t *testing.T, raw string) {
	t.Helper()
	writeFile(t, e.projectPath(), raw)
}

func (e wdSettingsEnv) resolve(session string) watchdogSettingsResult {
	var s *jsObject
	if session != "" {
		s, _ = parseJSONObject(session)
	}
	return resolveWatchdogConfig(e.project, s)
}

func parseJSONObject(raw string) (*jsObject, error) {
	v, err := parseJSON([]byte(raw))
	if err != nil {
		return nil, err
	}
	o, _ := v.(*jsObject)
	return o, nil
}

func (e wdSettingsEnv) readUserSettings(t *testing.T) *jsObject {
	t.Helper()
	b, err := os.ReadFile(e.userPath())
	if err != nil {
		t.Fatal(err)
	}
	o, err := parseJSONObject(string(b))
	if err != nil || o == nil {
		t.Fatalf("settings: %v", err)
	}
	return o
}

func wdDig(o *jsObject, path ...string) *jsObject {
	for _, p := range path {
		if o == nil {
			return nil
		}
		o = o.obj(p)
	}
	return o
}

func TestWatchdogSettingsTwins(t *testing.T) {
	const f = "watchdog-settings"
	tw(t, f, "resolves a default-off config when no watchdog settings exist", func(t *testing.T) {
		e := newWdSettingsEnv(t)
		result := e.resolve("")
		wdEq(t, result.OK, true, "ok")
		c := result.Config
		wdEq(t, c.Enabled, false, "enabled")
		wdEq(t, c.Clarification, false, "clarification")
		wdEq(t, c.Main.Enabled, false, "main.enabled")
		wdEq(t, c.Children.Enabled, false, "children.enabled")
		wdEq(t, c.GuidanceWatchdogMd, true, "guidance.watchdogMd")
		wdEq(t, c.AgentEndTimeoutMs, 30000, "agentEndTimeoutMs")
		wdEq(t, c.Children.WatchdogTailTimeoutMs, 120000, "watchdogTailTimeoutMs")
		wdEq(t, c.StalemateRepeats, 3, "stalemateRepeats")
		wdEq(t, c.ScopeEnabled, true, "scope")
		wdEq(t, c.Cadence, wdCadence{}, "cadence")
		wdEq(t, c.Lsp, wdLsp{Enabled: true, TimeoutMs: 3000, MaxFiles: 20, MaxDiagnostics: 50}, "lsp")
	})
	tw(t, f, "accepts only a boolean clarification opt-in", func(t *testing.T) {
		e := newWdSettingsEnv(t)
		wdEq(t, e.resolve(`{"clarification":true}`).Config.Clarification, true, "boolean")
		wdEq(t, e.resolve(`{"clarification":"true"}`).OK, false, "string")
		wdEq(t, e.resolve(`{"children":{"clarification":true}}`).OK, false, "children")
	})
	tw(t, f, "rejects removed fallbackModels settings", func(t *testing.T) {
		e := newWdSettingsEnv(t)
		wdEq(t, e.resolve(`{"main":{"fallbackModels":["model/a"]}}`).OK, false, "fallbackModels")
	})
	tw(t, f, "lets root enabled opt the main watchdog in while children stay default-off", func(t *testing.T) {
		e := newWdSettingsEnv(t)
		e.writeUser(t, `{"subagents":{"watchdog":{"enabled":true}}}`)
		c := e.resolve("").Config
		wdEq(t, c.Enabled, true, "enabled")
		wdEq(t, c.Main.Enabled, true, "main.enabled")
		wdEq(t, c.Children.Enabled, false, "children.enabled")
	})
	tw(t, f, "merges user, project, and session overrides field by field", func(t *testing.T) {
		e := newWdSettingsEnv(t)
		e.writeUser(t, `{"subagents":{"watchdog":{"enabled":true,"stalemateRepeats":5,"children":{"cadence":{"everyNTools":10},"overrides":{"worker":{"enabled":true,"model":"anthropic/claude-test","cadence":{"everyNTools":5}}}},"guidance":{"watchdogMd":false},"scope":{"enabled":false},"cadence":{"everyNTools":10}}}}`)
		e.writeProject(t, `{"subagents":{"watchdog":{"stalemateRepeats":2,"lsp":{"enabled":false,"timeoutMs":1500,"maxFiles":4,"maxDiagnostics":7},"main":{"model":"openai/gpt-test"},"children":{"overrides":{"worker":{"thinking":"medium"},"reviewer":{"enabled":false}}}}}}`)
		result := e.resolve(`{"children":{"overrides":{"worker":{"enabled":false}}}}`)
		c := result.Config
		wdEq(t, result.OK, true, "ok")
		wdEq(t, c.Main.Enabled, true, "main.enabled")
		wdEq(t, c.Main.Model, "openai/gpt-test", "main.model")
		wdEq(t, c.StalemateRepeats, 2, "stalemateRepeats")
		wdEq(t, c.GuidanceWatchdogMd, false, "guidance")
		wdEq(t, c.ScopeEnabled, false, "scope")
		wdEq(t, c.Cadence, wdCadence{EveryNTools: wdPtr(10)}, "cadence")
		wdEq(t, c.Children.Cadence, &wdCadence{EveryNTools: wdPtr(10)}, "children.cadence")
		wdEq(t, c.Lsp, wdLsp{Enabled: false, TimeoutMs: 1500, MaxFiles: 4, MaxDiagnostics: 7}, "lsp")
		wdEq(t, *c.Children.override("worker"), wdChildOverride{Enabled: wdPtr(false), Model: "anthropic/claude-test", Thinking: wdThinkingLevel("medium"), Cadence: &wdCadence{EveryNTools: wdPtr(5)}}, "worker")
		wdEq(t, *c.Children.override("reviewer"), wdChildOverride{Enabled: wdPtr(false)}, "reviewer")
	})
	tw(t, f, "returns exact validation errors at the watchdog boundary", func(t *testing.T) {
		e := newWdSettingsEnv(t)
		e.writeUser(t, `{"subagents":{"watchdog":{"enabled":"yes"}}}`)
		result := e.resolve("")
		wdEq(t, result.OK, false, "ok")
		wdEq(t, result.Errors, []wdSettingsError{{Scope: "user", Path: e.userPath(),
			Message: "Watchdog settings in '" + e.userPath() + "' have invalid 'subagents.watchdog.enabled'; expected a boolean."}}, "errors")
		wdEq(t, result.Config.Enabled, false, "enabled")
		wdEq(t, result.Config.Main.Enabled, false, "main.enabled")
	})
	tw(t, f, "throws the same exact validation error in strict mode", func(t *testing.T) {
		e := newWdSettingsEnv(t)
		e.writeUser(t, `{"subagents":{"watchdog":{"children":{"overrides":{"worker":{"mode":"fast"}}}}}}`)
		_, err := resolveWatchdogConfigStrict(e.project, nil)
		if err == nil {
			t.Fatal("no error")
		}
		wdEq(t, err.Error(), "Watchdog settings in '"+e.userPath()+"' have unknown field 'subagents.watchdog.children.overrides.worker.mode'.", "message")
	})
	tw(t, f, "parses launch rules and rejects malformed rule fields", func(t *testing.T) {
		e := newWdSettingsEnv(t)
		e.writeUser(t, `{"subagents":{"watchdog":{"rules":{"action":"block","roleModels":{"scout":{"allow":["openai-codex/gpt-5.6-luna:max"]},"oracle":{"deny":["*"],"note":"ask first"}}}}}}`)
		result := e.resolve("")
		if !result.OK {
			t.Fatalf("errors: %+v", result.Errors)
		}
		wdEq(t, result.Config.Rules, &wdRules{Action: "block", RoleModels: []wdRoleModelEntry{
			{"scout", wdRoleModelRule{Allow: []string{"openai-codex/gpt-5.6-luna:max"}}},
			{"oracle", wdRoleModelRule{Deny: []string{"*"}, Note: "ask first"}},
		}}, "rules")
		wdEq(t, e.resolve(`{"enabled":true}`).Config.Rules.Action, "block", "rules survive a session override")

		first := func() string {
			r := e.resolve("")
			if len(r.Errors) == 0 {
				return ""
			}
			return r.Errors[0].Message
		}
		e.writeUser(t, `{"subagents":{"watchdog":{"rules":{"action":"shout"}}}}`)
		wdMatch(t, first(), `invalid 'subagents\.watchdog\.rules\.action'; expected 'warn' or 'block'`)
		e.writeUser(t, `{"subagents":{"watchdog":{"rules":{"roleModels":{"scout":{"allowed":["x"]}}}}}}`)
		wdMatch(t, first(), `unknown field 'subagents\.watchdog\.rules\.roleModels\.scout\.allowed'`)
		e.writeUser(t, `{"subagents":{"watchdog":{}}}`)
		if e.resolve("").Config.Rules != nil {
			t.Error("rules without a rules setting")
		}
	})
	tw(t, f, "rejects invalid scope and cadence config at settings load", func(t *testing.T) {
		e := newWdSettingsEnv(t)
		first := func() string {
			r := e.resolve("")
			wdEq(t, r.OK, false, "ok")
			if len(r.Errors) == 0 {
				return ""
			}
			return r.Errors[0].Message
		}
		e.writeUser(t, `{"subagents":{"watchdog":{"scope":{"mode":"hidden"}}}}`)
		wdMatch(t, first(), `unknown field 'subagents\.watchdog\.scope\.mode'`)
		e.writeUser(t, `{"subagents":{"watchdog":{"cadence":{"everyNTools":4}}}}`)
		wdMatch(t, first(), `invalid 'subagents\.watchdog\.cadence\.everyNTools'`)
		e.writeUser(t, `{"subagents":{"watchdog":{"children":{"cadence":{"everyNTools":4}}}}}`)
		wdMatch(t, first(), `invalid 'subagents\.watchdog\.children\.cadence\.everyNTools'`)
	})
	tw(t, f, "rejects invalid LSP config at settings load", func(t *testing.T) {
		e := newWdSettingsEnv(t)
		e.writeUser(t, `{"subagents":{"watchdog":{"lsp":{"timeoutMs":0}}}}`)
		result := e.resolve("")
		wdEq(t, result.OK, false, "ok")
		wdEq(t, result.Config.Main.Enabled, false, "main.enabled")
		wdMatch(t, result.Errors[0].Message, `invalid 'subagents\.watchdog\.lsp\.timeoutMs'`)
	})
	tw(t, f, "rejects unsupported watchdog thinking values at settings load", func(t *testing.T) {
		e := newWdSettingsEnv(t)
		e.writeUser(t, `{"subagents":{"watchdog":{"main":{"thinking":"maximum"}}}}`)
		result := e.resolve("")
		wdEq(t, result.OK, false, "ok")
		wdEq(t, result.Config.Main.Enabled, false, "main.enabled")
		wdMatch(t, result.Errors[0].Message, `invalid 'subagents\.watchdog\.main\.thinking'`)
		wdMatch(t, result.Errors[0].Message, `'high'`)
	})
	tw(t, f, "writes watchdog model settings without toggling enablement", func(t *testing.T) {
		e := newWdSettingsEnv(t)
		e.writeUser(t, `{"subagents":{"watchdog":{"enabled":false,"main":{"enabled":false}}}}`)
		_, err := writeWatchdogModelSettings(wdModelWrite{Scope: "user", Target: wdTarget{Kind: "main"}, Model: wdSetTo("anthropic/claude-opus-4-8"), Thinking: wdSetTo(wdThinkingLevel("high"))})
		if err != nil {
			t.Fatal(err)
		}
		wd := wdDig(e.readUserSettings(t), "subagents", "watchdog")
		wdEq(t, wd.vals["enabled"], false, "enabled")
		main := wd.obj("main")
		wdEq(t, main.vals["enabled"], false, "main.enabled")
		wdEq(t, main.vals["model"], "anthropic/claude-opus-4-8", "main.model")
		wdEq(t, main.vals["thinking"], "high", "main.thinking")
	})
	tw(t, f, "clears watchdog model and thinking overrides", func(t *testing.T) {
		e := newWdSettingsEnv(t)
		e.writeUser(t, `{"subagents":{"watchdog":{"main":{"model":"openai-codex/gpt-5.5","thinking":"high"}}}}`)
		if _, err := writeWatchdogModelSettings(wdModelWrite{Scope: "user", Target: wdTarget{Kind: "main"}, Model: wdClear[string](), Thinking: wdClear[wdThinking]()}); err != nil {
			t.Fatal(err)
		}
		main := wdDig(e.readUserSettings(t), "subagents", "watchdog", "main")
		_, hasModel := main.vals["model"]
		_, hasThinking := main.vals["thinking"]
		wdEq(t, hasModel, false, "model present")
		wdEq(t, hasThinking, false, "thinking present")
	})
	tw(t, f, "preserves JSON.parse errors in boundary results", func(t *testing.T) {
		e := newWdSettingsEnv(t)
		e.writeUser(t, "{ bad")
		result := e.resolve("")
		wdEq(t, result.OK, false, "ok")
		wdEq(t, len(result.Errors), 1, "errors")
		wdEq(t, result.Errors[0].Scope, "user", "scope")
		wdMatch(t, result.Errors[0].Message, "^Failed to parse settings file '"+regexp.QuoteMeta(e.userPath())+"': ")
		wdEq(t, result.Config.Main.Enabled, false, "main.enabled")
	})
	tw(t, f, "writes persistent on/off toggles to root and main enabled", func(t *testing.T) {
		e := newWdSettingsEnv(t)
		e.writeUser(t, `{"subagents":{"watchdog":{"enabled":false,"main":{"enabled":false,"model":"openai/watchdog"}}}}`)
		if _, err := writeUserWatchdogEnabled(true); err != nil {
			t.Fatal(err)
		}
		wd := wdDig(e.readUserSettings(t), "subagents", "watchdog")
		wdEq(t, wd.vals["enabled"], true, "enabled on")
		wdEq(t, wd.obj("main").vals["enabled"], true, "main on")
		wdEq(t, wd.obj("main").vals["model"], "openai/watchdog", "model kept")
		wdEq(t, e.resolve("").Config.Main.Enabled, true, "resolved on")
		if _, err := writeUserWatchdogEnabled(false); err != nil {
			t.Fatal(err)
		}
		wd = wdDig(e.readUserSettings(t), "subagents", "watchdog")
		wdEq(t, wd.vals["enabled"], false, "enabled off")
		wdEq(t, wd.obj("main").vals["enabled"], false, "main off")
		wdEq(t, wd.obj("main").vals["model"], "openai/watchdog", "model kept")
		wdEq(t, e.resolve("").Config.Main.Enabled, false, "resolved off")
	})
}
