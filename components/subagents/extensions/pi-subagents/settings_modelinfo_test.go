package pi_subagents

import (
	"math"
	"testing"
)

// Twins of test/unit/model-info.test.ts.

func new(float64(f float64)) *float64 { return &f }

func lvl(kv ...any) map[string]*string {
	out := map[string]*string{}
	for i := 0; i < len(kv); i += 2 {
		if s, ok := kv[i+1].(string); ok {
			out[kv[i].(string)] = &s
		} else {
			out[kv[i].(string)] = nil
		}
	}
	return out
}

func TestModelInfo(t *testing.T) {
	const f = "model-info"
	yes := new(true)
	ambiguous := []ModelInfo{
		{Provider: "openai", ID: "gpt-5-mini", FullID: "openai/gpt-5-mini", Reasoning: yes, ThinkingLevelMap: lvl("high", "high")},
		{Provider: "github-copilot", ID: "gpt-5-mini", FullID: "github-copilot/gpt-5-mini", Reasoning: yes, ThinkingLevelMap: lvl("off", nil, "high", "high", "xhigh", "xhigh")},
	}
	legacy := []string{"off", "minimal", "low", "medium", "high", "xhigh"}
	tw(t, f, "does not choose arbitrary metadata for ambiguous bare model ids", func(t *testing.T) {
		eq(t, findModelInfo("gpt-5-mini", ambiguous, "") == nil, true, "ambiguous")
	})
	tw(t, f, "uses the preferred provider for ambiguous bare model metadata", func(t *testing.T) {
		eq(t, findModelInfo("gpt-5-mini", ambiguous, "github-copilot").FullID, "github-copilot/gpt-5-mini", "preferred")
	})
	tw(t, f, "matches provider-qualified model metadata before bare ids", func(t *testing.T) {
		eq(t, findModelInfo("openai/gpt-5-mini:high", ambiguous, "github-copilot").FullID, "openai/gpt-5-mini", "qualified")
	})
	tw(t, f, "matches owner/name registry ids without treating the owner as a provider", func(t *testing.T) {
		hf := []ModelInfo{{Provider: "huggingface", ID: "thinkingmachines/Inkling", FullID: "huggingface/thinkingmachines/Inkling", Reasoning: yes}}
		eq(t, findModelInfo("thinkingmachines/Inkling", hf, "").FullID, "huggingface/thinkingmachines/Inkling", "bare")
		eq(t, findModelInfo("huggingface/thinkingmachines/Inkling", hf, "").FullID, "huggingface/thinkingmachines/Inkling", "full")
	})
	tw(t, f, "preserves registry API metadata", func(t *testing.T) {
		eq(t, toModelInfo(RegistryModel{Provider: "gateway", ID: "model", API: "anthropic-messages"}).API, "anthropic-messages", "api")
	})
	tw(t, f, "keeps the legacy thinking list for models without per-level metadata", func(t *testing.T) {
		eq(t, getSupportedThinkingLevels(&ModelInfo{Provider: "openai", ID: "gpt-5", FullID: "openai/gpt-5", Reasoning: yes}), legacy, "model")
		eq(t, getSupportedThinkingLevels(nil), legacy, "nil")
	})
	tw(t, f, "keeps the legacy thinking list when older model metadata omits reasoning", func(t *testing.T) {
		eq(t, getSupportedThinkingLevels(&ModelInfo{Provider: "openai", ID: "gpt-5", FullID: "openai/gpt-5"}), legacy, "no reasoning")
	})
	tw(t, f, "filters levels only when per-level metadata is present", func(t *testing.T) {
		eq(t, getSupportedThinkingLevels(&ModelInfo{Provider: "deepseek", ID: "deepseek-v4-pro", FullID: "deepseek/deepseek-v4-pro", Reasoning: yes,
			ThinkingLevelMap: lvl("minimal", nil, "low", nil, "medium", nil, "high", "high", "xhigh", "max")}), []string{"off", "high", "xhigh"}, "levels")
	})
	tw(t, f, "honors metadata that marks off unsupported", func(t *testing.T) {
		eq(t, getSupportedThinkingLevels(&ModelInfo{Provider: "always-thinking", ID: "model", FullID: "always-thinking/model", Reasoning: yes,
			ThinkingLevelMap: lvl("off", nil, "minimal", nil, "low", nil, "medium", nil, "high", "high")}), []string{"high"}, "levels")
	})
	tw(t, f, "honors an explicit max mapping and recognizes max suffixes", func(t *testing.T) {
		eq(t, getSupportedThinkingLevels(&ModelInfo{Provider: "openai", ID: "gpt-5", FullID: "openai/gpt-5", Reasoning: yes,
			ThinkingLevelMap: lvl("off", nil, "minimal", nil, "low", nil, "medium", nil, "high", nil, "xhigh", nil, "max", "max")}), []string{"max"}, "levels")
		b, s := splitKnownThinkingSuffix("openai/gpt-5:max")
		eq(t, []string{b, s}, []string{"openai/gpt-5", ":max"}, "split")
	})
	tw(t, f, "carries contextWindow, maxTokens, input, and cost from the registry", func(t *testing.T) {
		info := toModelInfo(RegistryModel{Provider: "openai", ID: "gpt-5-mini", API: "openai-responses", Reasoning: yes, ContextWindow: new(float64(272000)), MaxTokens: new(float64(128000)),
			Input: []string{"text", "image"}, Cost: &ModelCost{Input: 0.25, Output: 2, CacheRead: 0.025, CacheWrite: 0.25}})
		eq(t, info.ContextWindow, 272000.0, "context")
		eq(t, info.MaxTokens, 128000.0, "max")
		eq(t, info.Input, []string{"text", "image"}, "input")
		eq(t, *info.Cost, ModelCost{Input: 0.25, Output: 2, CacheRead: 0.025, CacheWrite: 0.25}, "cost")
	})
	tw(t, f, "omits fields the registry does not report", func(t *testing.T) {
		info := toModelInfo(RegistryModel{Provider: "local", ID: "custom-model"})
		eq(t, info.FullID, "local/custom-model", "fullId")
		eq(t, []any{info.ContextWindow, info.MaxTokens, info.Input == nil, info.Cost == nil}, []any{0.0, 0.0, true, true}, "omitted")
	})
	tw(t, f, "ignores non-positive or malformed registry values", func(t *testing.T) {
		info := toModelInfo(RegistryModel{Provider: "local", ID: "m", ContextWindow: new(float64(0)), MaxTokens: new(float64(-1)), Input: []string{}, Cost: &ModelCost{Input: math.NaN(), Output: 1}})
		eq(t, []any{info.ContextWindow, info.MaxTokens, info.Input == nil, info.Cost == nil}, []any{0.0, 0.0, true, true}, "ignored")
	})
	tw(t, f, "copies the input array instead of aliasing the registry entry", func(t *testing.T) {
		in := []string{"text"}
		info := toModelInfo(RegistryModel{Provider: "local", ID: "m", Input: in})
		in[0] = "changed"
		eq(t, info.Input, []string{"text"}, "copy")
	})
	tw(t, f, "preserves and copies tiered registry cost", func(t *testing.T) {
		tiers := []CostTier{{InputTokensAbove: 100000, Input: 1, Output: 2, CacheRead: 0.1, CacheWrite: 0.2}}
		info := toModelInfo(RegistryModel{Provider: "openai", ID: "tiered", Cost: &ModelCost{Input: 0.5, Output: 1, CacheRead: 0.05, CacheWrite: 0.1, Tiers: tiers}})
		eq(t, info.Cost.Tiers, []CostTier{{InputTokensAbove: 100000, Input: 1, Output: 2, CacheRead: 0.1, CacheWrite: 0.2}}, "tiers")
		tiers[0].Input = 99
		eq(t, info.Cost.Tiers[0].Input, 1.0, "copied")
	})
}
