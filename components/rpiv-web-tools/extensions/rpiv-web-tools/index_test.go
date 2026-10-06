// SPDX-License-Identifier: MIT

package rpiv_web_tools

import (
	"strings"
	"testing"
)

const fIndex = "index"

// envMap builds an env reader over a fixed map, so a twin states only the variables it cares about. upstream: the
// process.env writes the upstream twins do around each case.
func envMap(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

// noEnv is an environment with nothing set.
func noEnv(string) string { return "" }

// registeredTool is one tool the extension registers. upstream: web-tools.ts registerWebSearchTool /
// registerWebFetchTool.
type registeredTool struct {
	Name  string
	Label string
}

// registeredTools is the registration set the orchestrator installs. upstream: web-tools.ts index.ts.
func registeredTools() []registeredTool {
	return []registeredTool{
		{Name: "web_search", Label: "Web Search"},
		{Name: "web_fetch", Label: "Web Fetch"},
	}
}

// registeredCommand is the single slash command. upstream: web-tools.ts registerWebSearchConfigCommand.
func registeredCommand() string { return webToolsCommandName }

func TestRegistration(t *testing.T) {
	tw(t, fIndex, "registers web_search + web_fetch tools", func(t *testing.T) {
		tools := registeredTools()
		eq(t, len(tools), 2, "tool count")
		eq(t, tools[0].Name, "web_search", "first tool")
		eq(t, tools[1].Name, "web_fetch", "second tool")
	})
	tw(t, fIndex, "registers /web-tools command", func(t *testing.T) {
		eq(t, registeredCommand(), "web-tools", "command name")
	})
}

func TestSearchSchema(t *testing.T) {
	tw(t, fIndex, "web_search schema declares min:1, max:10, default:5", func(t *testing.T) {
		p := searchSchemaMaxResultsParameter()
		eq(t, p.Minimum, 1.0, "minimum")
		eq(t, p.Maximum, 10.0, "maximum")
		eq(t, p.Default, 5.0, "default")
		eq(t, p.Description, "Maximum number of results to return (1-10). Default: 5.", "description")
	})
}

// The credential and provider-resolution twins are parameterized upstream over the provider table, so the ledger keeps
// the template title and the twin sweeps every provider itself.
func TestCredentialResolution(t *testing.T) {
	tw(t, fIndex, "uses env key for ${provider}", func(t *testing.T) {
		for _, meta := range providers {
			t.Run(meta.Name, func(t *testing.T) {
				env := envMap(map[string]string{meta.EnvVar: "env-key"})
				eq(t, resolveProviderAPIKey(meta.Name, config{APIKeys: map[string]string{meta.Name: "config-key"}}, env), "env-key", meta.Name)
			})
		}
	})
	tw(t, fIndex, "falls back to config key for ${provider}", func(t *testing.T) {
		for _, meta := range providers {
			t.Run(meta.Name, func(t *testing.T) {
				eq(t, resolveProviderAPIKey(meta.Name, config{APIKeys: map[string]string{meta.Name: "config-key"}}, noEnv), "config-key", meta.Name)
			})
		}
	})
	tw(t, fIndex, "throws when no key configured for ${provider}", func(t *testing.T) {
		for _, meta := range providers {
			t.Run(meta.Name, func(t *testing.T) {
				key := resolveProviderAPIKey(meta.Name, config{}, noEnv)
				if key != "" {
					t.Fatalf("%s: got a key %q with nothing configured", meta.Name, key)
				}
			})
		}
	})
	tw(t, fIndex, "treats empty-string env key as unset", func(t *testing.T) {
		for _, meta := range providers {
			t.Run(meta.Name, func(t *testing.T) {
				env := envMap(map[string]string{meta.EnvVar: ""})
				eq(t, resolveProviderAPIKey(meta.Name, config{APIKeys: map[string]string{meta.Name: "config-key"}}, env), "config-key", meta.Name)
			})
		}
	})
	tw(t, fIndex, "defaults to brave when no provider configured", func(t *testing.T) {
		eq(t, resolveActiveProviderName(config{}, noEnv), activeProvider{Name: "brave", Source: sourceDefault}, "active provider")
	})
	tw(t, fIndex, "uses legacy apiKey fallback for brave", func(t *testing.T) {
		eq(t, resolveProviderAPIKey("brave", config{APIKey: "legacy"}, noEnv), "legacy", "legacy key")
		// The fallback is brave-only: another provider must not read the legacy field.
		eq(t, resolveProviderAPIKey("tavily", config{APIKey: "legacy"}, noEnv), "", "not a fallback for tavily")
	})
	tw(t, fIndex, "treats empty-string legacy brave apiKey as unset", func(t *testing.T) {
		eq(t, resolveProviderAPIKey("brave", config{APIKey: "  "}, noEnv), "", "whitespace legacy key")
	})
}

func TestSearchResultCount(t *testing.T) {
	tw(t, fIndex, "clamps max_results to [1,10]", func(t *testing.T) {
		for _, c := range []struct{ requested, want float64 }{
			{-3, 1}, {0, 1}, {1, 1}, {5, 5}, {10, 10}, {11, 10}, {999, 10},
		} {
			requested := c.requested
			eq(t, clampSearchResultCount(&requested), int(c.want), "clamped")
		}
		// Nothing requested is the default, not the minimum.
		eq(t, clampSearchResultCount(nil), defaultSearchResult, "default")
	})
}

func TestMalformedConfigFallback(t *testing.T) {
	tw(t, fIndex, "falls back to defaults when config file is malformed JSON", func(t *testing.T) {
		// The fail-soft read of slice 1 is what makes this case hold: malformed JSON is an empty config, so the
		// provider resolution falls through to the default tier.
		write, _ := configHome(t)
		write("{ not valid json")
		cfg := ReadConfig()
		eq(t, cfg.Provider, "", "no provider from a malformed file")
		eq(t, resolveActiveProviderName(cfg, noEnv), activeProvider{Name: "brave", Source: sourceDefault}, "active provider")
		eq(t, resolveProviderAPIKey("brave", cfg, noEnv), "", "no key from a malformed file")
	})
}

// Provider metadata has no upstream test file of its own; these are the port's own checks over the table the schema
// enum, the key resolver and /web-tools all read.
func TestProviderTable(t *testing.T) {
	t.Run("carries the ten providers in declaration order", func(t *testing.T) {
		eq(t, knownProviderNames(), []string{
			"brave", "tavily", "serper", "exa", "youcom", "jina", "firecrawl", "perplexity", "searxng", "ollama",
		}, "provider order")
	})
	t.Run("marks the six full providers with both roles", func(t *testing.T) {
		full := 0
		for _, meta := range providers {
			if hasRole(meta, roleSearch) && hasRole(meta, roleFetch) {
				full++
			}
		}
		eq(t, full, 6, "full providers")
	})
	t.Run("gives the self-hosted providers their URL env var and default", func(t *testing.T) {
		searxng, _ := providerMetaByName("searxng")
		eq(t, searxng.BaseURLEnvVar, "SEARXNG_URL", "searxng url env")
		eq(t, searxng.DefaultBaseURL, "http://localhost:8080", "searxng default")
		eq(t, searxng.HasConfigure, true, "searxng configures")
		ollama, _ := providerMetaByName("ollama")
		eq(t, ollama.BaseURLEnvVar, "OLLAMA_HOST", "ollama host env")
		eq(t, ollama.DefaultBaseURL, "http://localhost:11434", "ollama default")
		// A hosted provider has no URL knob, so its base URL resolves to the empty string.
		eq(t, resolveProviderBaseURL(ollama, config{}, noEnv) != "", true, "ollama default wins over the empty fallback")
		brave, _ := providerMetaByName("brave")
		eq(t, resolveProviderBaseURL(brave, config{}, noEnv), "", "hosted provider has no base URL")
	})
	t.Run("rejects an unknown provider with the factory's own message", func(t *testing.T) {
		_, err := newSearchProvider("nope", providerCredentials{}, &fakeHTTP{})
		if err == nil {
			t.Fatal("an unknown provider must fail")
		}
		eq(t, err.Error(), `Unknown search provider: "nope"`, "factory error")
		// The orchestrator's uniform error carries the valid set instead.
		orchestratorErr := assertKnownProvider("nope")
		eq(t, orchestratorErr.Error(),
			`Unknown web_search provider: "nope". Valid providers: `+strings.Join(knownProviderNames(), ", ")+`.`,
			"orchestrator error")
	})
	t.Run("names every provider in the override description", func(t *testing.T) {
		desc := providerOverrideDescription()
		for _, name := range knownProviderNames() {
			if !strings.Contains(desc, name) {
				t.Fatalf("override description omits %q", name)
			}
		}
	})
}
