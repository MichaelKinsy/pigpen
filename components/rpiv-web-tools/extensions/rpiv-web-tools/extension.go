// Package rpiv_web_tools is a Go port of @juicesharp/rpiv-web-tools 2.12.0 (rpiv-mono packages/rpiv-web-tools):
// the web_search and web_fetch tools over ten pluggable backends and the /web-tools command. It starts no
// process and makes no network call until a tool runs.
package rpiv_web_tools

import (
	"fmt"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func searchSchema() sdk.Schema {
	names := knownProviderNames()
	anyOf := make([]any, len(names))
	for i, n := range names {
		anyOf[i] = map[string]any{"const": n, "type": "string"}
	}
	return sdk.Schema{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string", "description": "The search query. Be specific and use natural language."},
			"max_results": map[string]any{"type": "number", "default": defaultSearchResults, "minimum": minSearchResults, "maximum": maxSearchResults,
				"description": fmt.Sprintf("Maximum number of results to return (%d-%d). Default: %d.", minSearchResults, maxSearchResults, defaultSearchResults)},
			"provider": map[string]any{"anyOf": anyOf, "description": "Search provider to use for this call only, overriding the active provider set via /web-tools. " +
				fmt.Sprintf("Valid values: %s. ", strings.Join(names, ", ")) +
				"Omit to use the configured active provider. The named provider must have its API key/URL configured (via env var or /web-tools) or the call throws — there is no silent fallback."},
		},
		"required": []any{"query"},
	}
}

func fetchSchema() sdk.Schema {
	return sdk.Schema{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{"type": "string", "description": "The URL to fetch. Must be http or https."},
			"raw": map[string]any{"type": "boolean", "default": false, "description": "If true, return the raw HTML instead of extracted text. Default: false."},
		},
		"required": []any{"url"},
	}
}

func partial(ctx sdk.Context) func(text string, details any) {
	return func(text string, details any) {
		_ = ctx.OnUpdate(map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}, "details": details})
	}
}

// Extension returns the web tools extension. The tool guidance is read from the config at load, as the original
// does at registration.
func Extension() *sdk.Extension {
	e := sdk.New("rpiv-web-tools")
	cfg := readConfig()
	snippet, guidelines := guidanceFor(cfg, "web_search", defaultWebSearchSnippet, defaultWebSearchGuidelines)
	e.RegisterTool(sdk.ToolDefinition{
		Name: "web_search", Label: "Web Search",
		Description:      "Search the web for information. Returns a list of results with titles, URLs, and snippets. Use when you need current information not in your training data.",
		PromptSnippet:    snippet,
		PromptGuidelines: guidelines,
		Parameters:       searchSchema(),
		Execute: func(ctx sdk.Context, p map[string]any) (any, error) {
			query, _ := p["query"].(string)
			var maxResults *float64
			if v, ok := p["max_results"].(float64); ok {
				maxResults = &v
			}
			var override *string
			if v, ok := p["provider"].(string); ok {
				override = &v
			}
			text, details, err := searchTool(toolContext(ctx), query, maxResults, override, partial(ctx))
			if err != nil {
				return nil, err
			}
			return sdk.ToolResult{Content: text, Details: details}, nil
		},
		RenderCall:   renderSearchCall,
		RenderResult: renderSearchResult,
	})
	fsnippet, fguidelines := guidanceFor(cfg, "web_fetch", defaultWebFetchSnippet, defaultWebFetchGuidelines)
	e.RegisterTool(sdk.ToolDefinition{
		Name: "web_fetch", Label: "Web Fetch",
		Description:      "Fetch the content of a specific URL. Returns text content for HTML pages (tags stripped), raw text for plain text or JSON. Supports http and https only. Content is truncated to avoid overwhelming the context window.",
		PromptSnippet:    fsnippet,
		PromptGuidelines: fguidelines,
		Parameters:       fetchSchema(),
		Execute: func(ctx sdk.Context, p map[string]any) (any, error) {
			u, _ := p["url"].(string)
			raw, _ := p["raw"].(bool)
			text, details, err := fetchTool(toolContext(ctx), u, raw, partial(ctx))
			if err != nil {
				return nil, err
			}
			return sdk.ToolResult{Content: text, Details: details}, nil
		},
		RenderCall:   renderFetchCall,
		RenderResult: renderFetchResult,
	})
	e.Command("web-tools", "Configure the search provider and API key used by web_search", webToolsCommand)
	return e
}

// guidanceFor is a tool's prompt snippet and guidelines: the config's override where it is valid, else the
// defaults. upstream: web-tools.ts registerWebSearchTool and registerWebFetchTool.
func guidanceFor(cfg webToolsConfig, tool, defSnippet string, defGuidelines []string) (string, []string) {
	g := validateGuidanceFields(nestedGuidance(cfg, tool))
	snippet, guidelines := defSnippet, defGuidelines
	if g.HasSnippet {
		snippet = g.PromptSnippet
	}
	if g.HasGuidelines {
		guidelines = g.PromptGuidelines
	}
	return snippet, guidelines
}

func nestedGuidance(cfg webToolsConfig, tool string) any {
	g, _ := cfg["guidance"].(map[string]any)
	return g[tool]
}
