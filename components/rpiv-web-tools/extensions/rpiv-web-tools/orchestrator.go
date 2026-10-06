// SPDX-License-Identifier: MIT

package rpiv_web_tools

import "strings"

// The tool-namespace constants of the orchestrator. upstream: web-tools.ts.
const (
	minSearchResults    = 1
	maxSearchResults    = 10
	defaultSearchResult = 5

	webToolsCommandName = "web-tools"
	showFlag            = "--show"
	unsetLabel          = "(not set)"

	// defaultProviderName is the backend used when neither an override, the env var nor the config names one.
	// upstream: web-tools.ts DEFAULT_PROVIDER_NAME.
	defaultProviderName = "brave"

	// legacyTopLevelKeyProvider is the one provider whose key was historically stored at the config's top level
	// (config.apiKey) before the per-provider apiKeys map. The legacy field is auto-migrated to apiKeys.brave by
	// /web-tools, which deletes apiKey from the saved object. upstream: web-tools.ts
	// LEGACY_TOP_LEVEL_KEY_PROVIDER.
	legacyTopLevelKeyProvider = "brave"

	searchResultPreviewLimit = 5
	fetchPreviewLineLimit    = 15
	apiKeyMaskVisibleChars   = 4
	fetchTempDirPrefix       = "rpiv-fetch-"
	fetchTempFileName        = "content.txt"
)

// The executor guidance defaults the tools fall back to when the config carries no override. upstream: web-tools.ts
// DEFAULT_WEB_SEARCH_SNIPPET, DEFAULT_WEB_SEARCH_GUIDELINES, DEFAULT_WEB_FETCH_SNIPPET, DEFAULT_WEB_FETCH_GUIDELINES.
const (
	defaultWebSearchSnippet = "Search the web for up-to-date information"
	defaultWebFetchSnippet  = "Fetch and read content from a specific URL"
)

var defaultWebSearchGuidelines = []string{
	"Use web_search for information beyond your training data — recent events, current library versions, live API documentation.",
	`Use the current year from "Current date:" in your context when searching for recent information or documentation.`,
	`After answering using search results, include a "Sources:" section listing relevant URLs as markdown hyperlinks: [Title](URL). Never skip this.`,
	"Domain filtering is supported to include or block specific websites.",
	"If no API key is configured, ask the user to run /web-tools before proceeding.",
}

var defaultWebFetchGuidelines = []string{
	"Use web_fetch to read the full content of a specific URL — documentation pages, blog posts, API references found via web_search.",
	"web_fetch is complementary to web_search: search finds URLs, fetch reads them.",
	`After answering using fetched content, include a "Sources:" section with a markdown hyperlink to the fetched URL.`,
	"Large responses are truncated and spilled to a temp file — the temp path is reported in the result details.",
}

// clampSearchResultCount is the requested count clamped into [1,10], defaulting to 5 when nothing was asked for.
// upstream: web-tools.ts clampSearchResultCount.
func clampSearchResultCount(requested *float64) int {
	value := defaultSearchResult
	if requested != nil {
		value = int(*requested)
	}
	if value < minSearchResults {
		return minSearchResults
	}
	if value > maxSearchResults {
		return maxSearchResults
	}
	return value
}

// maskApiKey keeps the last four characters and hides the rest, or reports the unset label. upstream: web-tools.ts
// maskApiKey.
func maskApiKey(key string) string {
	if key == "" {
		return unsetLabel
	}
	if len(key) <= apiKeyMaskVisibleChars {
		return strings.Repeat("*", len(key))
	}
	return strings.Repeat("*", len(key)-apiKeyMaskVisibleChars) + key[len(key)-apiKeyMaskVisibleChars:]
}

// resolveProviderAPIKey is the three-tier credential lookup: the provider's env var, then apiKeys[provider] in the
// config, then the legacy top-level apiKey for brave alone. Every candidate is trimmed, so an empty string reads as
// unset. An unknown provider has no meta and therefore no key. upstream: web-tools.ts resolveProviderApiKey.
func resolveProviderAPIKey(providerName string, cfg config, env func(string) string) string {
	meta, ok := providerMetaByName(providerName)
	if !ok {
		return ""
	}
	if meta.EnvVar != "" {
		if envKey := strings.TrimSpace(env(meta.EnvVar)); envKey != "" {
			return envKey
		}
	}
	if configKey, ok := cfg.APIKeys[providerName]; ok {
		if trimmed := strings.TrimSpace(configKey); trimmed != "" {
			return trimmed
		}
	}
	if providerName == legacyTopLevelKeyProvider {
		return strings.TrimSpace(cfg.APIKey)
	}
	return ""
}

// resolveProviderBaseURL is the generic per-provider base-URL lookup: env, then baseUrls[name], then the meta's
// default. A provider without a base-URL env var short-circuits to the empty string. upstream: web-tools.ts
// resolveProviderBaseUrl.
func resolveProviderBaseURL(meta providerMeta, cfg config, env func(string) string) string {
	if meta.BaseURLEnvVar == "" {
		return ""
	}
	if envURL := strings.TrimSpace(env(meta.BaseURLEnvVar)); envURL != "" {
		return envURL
	}
	if configURL, ok := cfg.BaseURLs[meta.Name]; ok {
		if trimmed := strings.TrimSpace(configURL); trimmed != "" {
			return trimmed
		}
	}
	return meta.DefaultBaseURL
}

// providerSource names which tier resolved the active provider, so /web-tools can show where it came from.
// upstream: web-tools.ts resolveActiveProviderName's return type.
type providerSource string

const (
	sourceEnv     providerSource = "env"
	sourceConfig  providerSource = "config"
	sourceDefault providerSource = "default"
)

// activeProvider is the active backend plus the tier that named it. upstream: web-tools.ts
// resolveActiveProviderName.
type activeProvider struct {
	Name   string
	Source providerSource
}

// resolveActiveProviderName is env over config over default, and it does not validate: a bogus WEB_SEARCH_PROVIDER
// still renders in --show and only throws on the next call, through assertKnownProvider. upstream: web-tools.ts
// resolveActiveProviderName.
func resolveActiveProviderName(cfg config, env func(string) string) activeProvider {
	if envProvider := strings.TrimSpace(env("WEB_SEARCH_PROVIDER")); envProvider != "" {
		return activeProvider{Name: envProvider, Source: sourceEnv}
	}
	if cfg.Provider != "" {
		return activeProvider{Name: cfg.Provider, Source: sourceConfig}
	}
	return activeProvider{Name: defaultProviderName, Source: sourceDefault}
}

// assertKnownProvider is the uniform unknown-provider error for the per-call override and the env path, so a
// misconfiguration surfaces the same shape either way. upstream: web-tools.ts assertKnownProvider.
func assertKnownProvider(name string) error {
	if _, ok := providerMetaByName(name); ok {
		return nil
	}
	return &unknownProviderError{Name: name, Valid: knownProviderNames()}
}

// unknownProviderError is the "Unknown web_search provider" failure with its valid set. upstream: web-tools.ts
// assertKnownProvider.
type unknownProviderError struct {
	Name  string
	Valid []string
}

func (e *unknownProviderError) Error() string {
	return `Unknown web_search provider: "` + e.Name + `". Valid providers: ` + strings.Join(e.Valid, ", ") + `.`
}
