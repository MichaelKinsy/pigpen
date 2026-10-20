package pi_subagents

import "testing"

// Twins of test/unit/watchdog-model-selection.test.ts.

func wdSelectionModels() []map[string]any {
	return []map[string]any{
		{"provider": "openai-codex", "id": "gpt-5.5", "reasoning": true},
		{"provider": "anthropic", "id": "claude-opus-4-8", "reasoning": true},
	}
}

// wdSelectionCtx is createCtx of the test: getAvailable lists every model, hasConfiguredAuth only the authenticated ones.
func wdSelectionCtx(current map[string]any, authenticated []string, models []map[string]any) *wdFakeCtx {
	if authenticated == nil {
		authenticated = []string{"openai-codex/gpt-5.5", "anthropic/claude-opus-4-8"}
	}
	if models == nil {
		models = wdSelectionModels()
	}
	reg := &wdFakeRegistry{models: models, available: models, authed: func(m map[string]any) bool { return wdContains(authenticated, wdFullModelID(m)) }}
	return &wdFakeCtx{cwd: "/tmp/watchdog-model-selection", model: current, reg: reg}
}

func TestWatchdogModelSelectionTwins(t *testing.T) {
	const f = "watchdog-model-selection"
	noStrong := `No authenticated strong complementary watchdog model`
	tw(t, f, "recommends Opus 4.8 high when the main session is GPT 5.5", func(t *testing.T) {
		rec, err := recommendStrongWatchdogModel(wdSelectionCtx(map[string]any{"provider": "openai-codex", "id": "gpt-5.5"}, nil, nil))
		if err != nil {
			t.Fatal(err)
		}
		wdEq(t, rec.Model, "anthropic/claude-opus-4-8", "model")
		wdEq(t, rec.Thinking, "high", "thinking")
	})
	tw(t, f, "recommends GPT 5.5 high when the main session is Opus 4.8", func(t *testing.T) {
		rec, err := recommendStrongWatchdogModel(wdSelectionCtx(map[string]any{"provider": "anthropic", "id": "claude-opus-4-8"}, nil, nil))
		if err != nil {
			t.Fatal(err)
		}
		wdEq(t, rec.Model, "openai-codex/gpt-5.5", "model")
		wdEq(t, rec.Thinking, "high", "thinking")
	})
	tw(t, f, "requires the complementary strong model to be authenticated", func(t *testing.T) {
		_, err := recommendStrongWatchdogModel(wdSelectionCtx(map[string]any{"provider": "openai-codex", "id": "gpt-5.5"}, []string{"openai-codex/gpt-5.5"}, nil))
		hasMatch(t, err, noStrong)
	})
	tw(t, f, "does not treat GPT 5.5 mini variants as the strong GPT 5.5 watchdog", func(t *testing.T) {
		models := []map[string]any{{"provider": "openai-codex", "id": "gpt-5.5-mini", "reasoning": true}, {"provider": "openai-codex", "id": "gpt-5.5-20250101-mini", "reasoning": true}}
		ctx := wdSelectionCtx(map[string]any{"provider": "anthropic", "id": "claude-opus-4-8"}, []string{"openai-codex/gpt-5.5-mini", "openai-codex/gpt-5.5-20250101-mini"}, models)
		_, err := recommendStrongWatchdogModel(ctx)
		hasMatch(t, err, noStrong)
	})
	tw(t, f, "does not treat dated Opus mini variants as the strong Opus 4.8 watchdog", func(t *testing.T) {
		models := []map[string]any{{"provider": "anthropic", "id": "claude-opus-4-8-20250101-mini", "reasoning": true}}
		ctx := wdSelectionCtx(map[string]any{"provider": "openai-codex", "id": "gpt-5.5"}, []string{"anthropic/claude-opus-4-8-20250101-mini"}, models)
		_, err := recommendStrongWatchdogModel(ctx)
		hasMatch(t, err, noStrong)
	})
	tw(t, f, "does not treat cheap-prefixed variants as strong watchdog families", func(t *testing.T) {
		models := []map[string]any{{"provider": "openai-codex", "id": "cheap-gpt-5.5", "reasoning": true}, {"provider": "anthropic", "id": "cheap-claude-opus-4-8", "reasoning": true}}
		ctx := wdSelectionCtx(map[string]any{"provider": "anthropic", "id": "claude-opus-4-8"}, []string{"openai-codex/cheap-gpt-5.5", "anthropic/cheap-claude-opus-4-8"}, models)
		_, err := recommendStrongWatchdogModel(ctx)
		hasMatch(t, err, noStrong)
	})
	tw(t, f, "canonicalizes explicit models and preserves thinking suffixes", func(t *testing.T) {
		resolved, err := resolveWatchdogModelInput(wdSelectionCtx(map[string]any{"provider": "anthropic", "id": "claude-opus-4-8"}, nil, nil), "openai-codex/gpt-5-5:high")
		if err != nil {
			t.Fatal(err)
		}
		wdEq(t, resolved.Model, "openai-codex/gpt-5.5", "model")
		wdEq(t, resolved.Thinking, "high", "thinking")
	})
	tw(t, f, "accepts a max suffix for an authenticated watchdog model", func(t *testing.T) {
		models := []map[string]any{{"provider": "openai", "id": "gpt-5", "reasoning": true, "thinkingLevelMap": map[string]any{"max": "max"}}}
		ctx := wdSelectionCtx(map[string]any{"provider": "openai", "id": "gpt-5"}, []string{"openai/gpt-5"}, models)
		resolved, err := resolveWatchdogModelInput(ctx, "openai/gpt-5:max")
		if err != nil {
			t.Fatal(err)
		}
		wdEq(t, resolved.Thinking, "max", "thinking")
	})
}
