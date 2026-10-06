// SPDX-License-Identifier: MIT

package rpiv_web_tools

// The per-tool prompt copy: each tool's snippet and guidelines, resolved from the config against the built-in
// defaults. upstream: the promptSnippet and promptGuidelines fields of the two tool registrations, read through
// validateGuidanceFields.

// toolGuidance is what a tool is registered with: the model-facing snippet and guidelines, each either the config's
// override or the built-in default.
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

// guidanceForTools resolves both tools from one config, which is what registration does. upstream: the two
// registration functions calling validateGuidanceFields(loadConfig().guidance?.<tool>).
func guidanceForTools(cfg config) (search, fetch toolGuidance) {
	var searchOverride, fetchOverride *guidanceFields
	if cfg.Guidance != nil {
		searchOverride, fetchOverride = cfg.Guidance.WebSearch, cfg.Guidance.WebFetch
	}
	return resolveToolGuidance(searchOverride, defaultWebSearchSnippet, defaultWebSearchGuidelines),
		resolveToolGuidance(fetchOverride, defaultWebFetchSnippet, defaultWebFetchGuidelines)
}
