package pi_subagents

import (
	"os"
	"path/filepath"
	"testing"
)

// Twins of test/unit/watchdog-tool-actions.test.ts.

type wdToolEnv struct{ home, project string }

func newWdToolEnv(t *testing.T) wdToolEnv {
	t.Helper()
	home := withTempHome(t)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, ".pi", "agent"))
	return wdToolEnv{home: home, project: tmp(t)}
}

func (e wdToolEnv) userSettings() string { return filepath.Join(e.home, ".pi", "agent", "settings.json") }

func (e wdToolEnv) ctx(provider, id string) *wdFakeCtx {
	models := []map[string]any{{"provider": "openai-codex", "id": "gpt-5.5", "reasoning": true}, {"provider": "anthropic", "id": "claude-opus-4-8", "reasoning": true}}
	return &wdFakeCtx{cwd: e.project, model: map[string]any{"provider": provider, "id": id}, sessionID: "session-test",
		reg: &wdFakeRegistry{models: models, available: models}}
}

func (e wdToolEnv) runtime() *watchdogRuntime { return newWatchdogRuntime(watchdogRuntimeOptions{Cwd: e.project}) }

func wdExists(path string) bool { _, err := os.Stat(path); return err == nil }

func TestWatchdogToolActionsTwins(t *testing.T) {
	const f = "watchdog-tool-actions"
	tw(t, f, "configures the recommended model for the current session without writing settings", func(t *testing.T) {
		e := newWdToolEnv(t)
		rt := e.runtime()
		result := handleWatchdogToolAction("watchdog.configure", watchdogToolParams{Model: "recommended"}, e.ctx("openai-codex", "gpt-5.5"), rt)
		wdEq(t, result.IsError, false, "isError")
		wdMatch(t, result.Text, `session model configured: anthropic/claude-opus-4-8:high`)
		wdEq(t, wdExists(e.userSettings()), false, "settings file")
		main := rt.getSnapshot(e.project).Config.Main
		wdEq(t, main.Model, "anthropic/claude-opus-4-8", "model")
		wdEq(t, main.Thinking, wdThinkingLevel("high"), "thinking")
	})
	tw(t, f, "keeps session model overrides and their thinking in recommendations", func(t *testing.T) {
		e := newWdToolEnv(t)
		rt := e.runtime()
		ctx := e.ctx("openai-codex", "gpt-5.5")
		rt.setSessionModel(wdSessionModelPatch{Model: wdSetTo("openai-codex/gpt-5.5"), Thinking: wdSetTo(wdThinkingOff)}, e.project)
		recommendation := handleWatchdogToolAction("watchdog.recommend-model", watchdogToolParams{}, ctx, rt)
		wdEq(t, recommendation.IsError, false, "isError")
		wdMatch(t, recommendation.Text, `Keep configured watchdog: openai-codex/gpt-5\.5:off`)
		wdNoMatch(t, recommendation.Text, `strong independent watchdog`)
		applied := handleWatchdogToolAction("watchdog.configure", watchdogToolParams{Model: "recommended"}, ctx, rt)
		wdEq(t, applied.IsError, false, "applied isError")
		main := rt.getSnapshot(e.project).Config.Main
		wdEq(t, main.Model, "openai-codex/gpt-5.5", "model")
		wdEq(t, main.Thinking, wdThinkingLevel("off"), "thinking")
		rt.setSessionModel(wdSessionModelPatch{Model: wdSetTo("openai-codex/gpt-5.5:low"), Thinking: wdSetTo(wdThinkingOff)}, e.project)
		suffixed := handleWatchdogToolAction("watchdog.configure", watchdogToolParams{Model: "recommended"}, ctx, rt)
		wdEq(t, suffixed.IsError, false, "suffixed isError")
		wdEq(t, rt.getSnapshot(e.project).Config.Main.Thinking, wdThinkingLevel("low"), "suffixed thinking")
		wdEq(t, wdExists(e.userSettings()), false, "settings file")
	})
	tw(t, f, "does not replace an unavailable configured model with a strong recommendation", func(t *testing.T) {
		e := newWdToolEnv(t)
		rt := e.runtime()
		ctx := e.ctx("openai-codex", "gpt-5.5")
		rt.setSessionModel(wdSessionModelPatch{Model: wdSetTo("custom/unavailable"), Thinking: wdSetTo(wdThinkingLevel("low"))}, e.project)
		result := handleWatchdogToolAction("watchdog.configure", watchdogToolParams{Model: "recommended", Scope: "user"}, ctx, rt)
		wdEq(t, result.IsError, true, "isError")
		wdMatch(t, result.Text, `was not found`)
		wdEq(t, rt.getSnapshot(e.project).Config.Main.Model, "custom/unavailable", "model")
		wdEq(t, wdExists(e.userSettings()), false, "settings file")
	})
	tw(t, f, "preserves omitted session model fields when configuring thinking", func(t *testing.T) {
		e := newWdToolEnv(t)
		rt := e.runtime()
		ctx := e.ctx("openai-codex", "gpt-5.5")
		handleWatchdogToolAction("watchdog.configure", watchdogToolParams{Model: "recommended"}, ctx, rt)
		result := handleWatchdogToolAction("watchdog.configure", watchdogToolParams{Thinking: "low"}, ctx, rt)
		wdEq(t, result.IsError, false, "isError")
		main := rt.getSnapshot(e.project).Config.Main
		wdEq(t, main.Model, "anthropic/claude-opus-4-8", "model")
		wdEq(t, main.Thinking, wdThinkingLevel("low"), "thinking")
		off := handleWatchdogToolAction("watchdog.configure", watchdogToolParams{Thinking: false}, ctx, rt)
		wdEq(t, off.IsError, false, "off isError")
		wdEq(t, rt.getSnapshot(e.project).Config.Main.Thinking, wdThinkingOff, "thinking off")
	})
	tw(t, f, "rejects transport-permitted true thinking without changing session or settings", func(t *testing.T) {
		e := newWdToolEnv(t)
		rt := e.runtime()
		ctx := e.ctx("openai-codex", "gpt-5.5")
		before := rt.getSnapshot(e.project).Config.Main
		for _, scope := range []string{"session", "user"} {
			result := handleWatchdogToolAction("watchdog.configure", watchdogToolParams{Scope: scope, Thinking: true}, ctx, rt)
			wdEq(t, result.IsError, true, scope+" isError")
			wdMatch(t, result.Text, `Unsupported watchdog thinking 'true'`)
			wdEq(t, rt.getSnapshot(e.project).Config.Main, before, scope+" main config")
		}
		wdEq(t, wdExists(e.userSettings()), false, "user settings")
		wdEq(t, wdExists(filepath.Join(e.project, ".pi", "settings.json")), false, "project settings")
	})
	tw(t, f, "persists the recommended model when user scope is explicit", func(t *testing.T) {
		e := newWdToolEnv(t)
		rt := e.runtime()
		result := handleWatchdogToolAction("watchdog.configure", watchdogToolParams{Scope: "user", Model: "recommended"}, e.ctx("anthropic", "claude-opus-4-8"), rt)
		wdEq(t, result.IsError, false, "isError")
		b, err := os.ReadFile(e.userSettings())
		if err != nil {
			t.Fatal(err)
		}
		settings, _ := parseJSONObject(string(b))
		main := wdDig(settings, "subagents", "watchdog", "main")
		wdEq(t, main.vals["model"], "openai-codex/gpt-5.5", "model")
		wdEq(t, main.vals["thinking"], "high", "thinking")
	})
}
