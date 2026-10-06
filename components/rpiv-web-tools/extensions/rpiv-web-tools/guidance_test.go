// SPDX-License-Identifier: MIT

package rpiv_web_tools

import "testing"

const fGuidance = "web-tools.guidance"

// toolGuidance is what a tool is registered with: the model-facing snippet and guidelines, each either the config's
// override or the built-in default. upstream: the promptSnippet/promptGuidelines fields of the registration.
type toolGuidance struct {
	PromptSnippet    string
	PromptGuidelines []string
}

// resolveToolGuidance is the per-tool override: an override field wins, an absent or empty one falls back to the
// default. The two tools are independent, so overriding one leaves the other's defaults in place. upstream:
// web-tools.ts registerWebSearchTool and registerWebFetchTool reading guidance off the config.
func resolveToolGuidance(override *guidanceFields, defaultSnippet string, defaultGuidelines []string) toolGuidance {
	out := toolGuidance{PromptSnippet: defaultSnippet, PromptGuidelines: defaultGuidelines}
	if override == nil {
		return out
	}
	if override.PromptSnippet != "" {
		out.PromptSnippet = override.PromptSnippet
	}
	if override.PromptGuidelines != nil {
		out.PromptGuidelines = override.PromptGuidelines
	}
	return out
}

// guidanceForTools resolves both tools from one config, which is what registration does. The two are independent by
// design: a config that overrides web_search must not disturb web_fetch. upstream: the two registration functions.
func guidanceForTools(cfg config) (search, fetch toolGuidance) {
	var searchOverride, fetchOverride *guidanceFields
	if cfg.Guidance != nil {
		searchOverride, fetchOverride = cfg.Guidance.WebSearch, cfg.Guidance.WebFetch
	}
	return resolveToolGuidance(searchOverride, defaultWebSearchSnippet, defaultWebSearchGuidelines),
		resolveToolGuidance(fetchOverride, defaultWebFetchSnippet, defaultWebFetchGuidelines)
}

func TestGuidanceResolution(t *testing.T) {
	tw(t, fGuidance, "uses built-in defaults when no config file exists", func(t *testing.T) {
		configHome(t)
		search, fetch := guidanceForTools(ReadConfig())
		eq(t, search.PromptSnippet, defaultWebSearchSnippet, "search snippet")
		eq(t, len(search.PromptGuidelines), 5, "search guidelines")
		eq(t, fetch.PromptSnippet, defaultWebFetchSnippet, "fetch snippet")
		eq(t, len(fetch.PromptGuidelines), 4, "fetch guidelines")
	})
	tw(t, fGuidance, "overrides web_search snippet only, web_fetch uses defaults", func(t *testing.T) {
		write, _ := configHome(t)
		write(`{"guidance":{"web_search":{"promptSnippet":"Custom search snippet"}}}`)
		search, fetch := guidanceForTools(ReadConfig())
		eq(t, search.PromptSnippet, "Custom search snippet", "search snippet")
		eq(t, len(search.PromptGuidelines), 5, "search guidelines stay default")
		eq(t, fetch.PromptSnippet, defaultWebFetchSnippet, "fetch snippet")
	})
	tw(t, fGuidance, "overrides web_fetch snippet only, web_search uses defaults", func(t *testing.T) {
		write, _ := configHome(t)
		write(`{"guidance":{"web_fetch":{"promptSnippet":"Custom fetch snippet"}}}`)
		search, fetch := guidanceForTools(ReadConfig())
		eq(t, search.PromptSnippet, defaultWebSearchSnippet, "search snippet")
		eq(t, fetch.PromptSnippet, "Custom fetch snippet", "fetch snippet")
	})
	tw(t, fGuidance, "overrides both tools independently", func(t *testing.T) {
		write, _ := configHome(t)
		write(`{"guidance":{"web_search":{"promptSnippet":"S","promptGuidelines":["only"]},"web_fetch":{"promptSnippet":"F","promptGuidelines":["a","b"]}}}`)
		search, fetch := guidanceForTools(ReadConfig())
		eq(t, search.PromptSnippet, "S", "search snippet")
		eq(t, search.PromptGuidelines, []string{"only"}, "search guidelines")
		eq(t, fetch.PromptSnippet, "F", "fetch snippet")
		eq(t, fetch.PromptGuidelines, []string{"a", "b"}, "fetch guidelines")
	})
	tw(t, fGuidance, "falls back to defaults on invalid guidance types", func(t *testing.T) {
		write, _ := configHome(t)
		// The schema salvage of slice 1 drops the wrong-typed leaf, so the tool keeps its default.
		write(`{"guidance":{"web_search":{"promptSnippet":123},"web_fetch":{"promptGuidelines":"not-a-list"}}}`)
		search, fetch := guidanceForTools(ReadConfig())
		eq(t, search.PromptSnippet, defaultWebSearchSnippet, "search snippet")
		eq(t, len(search.PromptGuidelines), 5, "search guidelines")
		eq(t, fetch.PromptSnippet, defaultWebFetchSnippet, "fetch snippet")
		eq(t, len(fetch.PromptGuidelines), 4, "fetch guidelines")
	})
	tw(t, fGuidance, "falls back to defaults on empty promptSnippet", func(t *testing.T) {
		write, _ := configHome(t)
		write(`{"guidance":{"web_search":{"promptSnippet":""},"web_fetch":{"promptSnippet":"  "}}}`)
		search, fetch := guidanceForTools(ReadConfig())
		eq(t, search.PromptSnippet, defaultWebSearchSnippet, "an empty snippet is no override")
		eq(t, fetch.PromptSnippet, "  ", "a whitespace snippet is still set")
	})
	tw(t, fGuidance, "preserves guidance when saving API key via /web-tools", func(t *testing.T) {
		write, _ := configHome(t)
		write(`{"guidance":{"web_search":{"promptSnippet":"Keep me"}},"otherField":"keep"}`)
		saved := saveProviderConfig(ReadConfig(), "brave", providerConfigChange{APIKey: "k", HasAPIKey: true})
		if !WriteConfig(saved) {
			t.Fatal("the save must succeed")
		}
		search, _ := guidanceForTools(ReadConfig())
		eq(t, search.PromptSnippet, "Keep me", "guidance survives the save")
		eq(t, ReadConfig().Provider, "brave", "the provider switch persisted")
		eq(t, string(ReadConfig().extra["otherField"]), `"keep"`, "an unknown key survives too")
	})
}
