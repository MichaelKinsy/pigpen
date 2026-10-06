// SPDX-License-Identifier: MIT

package rpiv_web_tools

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

const fCommand = "index"

// pickerLabels runs the command up to the picker and returns the rows it was offered.
func pickerLabels(t *testing.T, cfg config, env func(string) string) []string {
	t.Helper()
	labels, _ := providerPickerLabels(cfg, env)
	return labels
}

func TestCommandPersistence(t *testing.T) {
	tw(t, fCommand, "preserves keys for all providers when switching", func(t *testing.T) {
		current := config{
			Provider: "brave",
			APIKeys: map[string]string{
				"brave":     "brave-key",
				"tavily":    "tavily-key",
				"jina":      "jina-key",
				"firecrawl": "firecrawl-key",
			},
		}
		saved := saveProviderConfig(current, "firecrawl", providerConfigChange{APIKey: "new-firecrawl-key", HasAPIKey: true})
		eq(t, saved.Provider, "firecrawl", "provider")
		eq(t, saved.APIKeys["brave"], "brave-key", "brave key kept")
		eq(t, saved.APIKeys["tavily"], "tavily-key", "tavily key kept")
		eq(t, saved.APIKeys["jina"], "jina-key", "jina key kept")
		eq(t, saved.APIKeys["firecrawl"], "new-firecrawl-key", "the written key")
	})
	tw(t, fCommand, "migrates legacy apiKey to apiKeys on save", func(t *testing.T) {
		current := config{APIKey: "legacy-key", extra: map[string]anyOther{"otherField": jsonString("keep")}}
		saved := saveProviderConfig(current, "brave", providerConfigChange{APIKey: "new-key", HasAPIKey: true})
		eq(t, saved.Provider, "brave", "provider")
		eq(t, saved.APIKeys, map[string]string{"brave": "new-key"}, "the legacy key is migrated into apiKeys")
		eq(t, saved.APIKey, "", "the legacy field is deleted")
		eq(t, string(saved.extra["otherField"]), `"keep"`, "an unknown key is carried over")
	})
}

func TestCommandShow(t *testing.T) {
	tw(t, fCommand, "--show displays all providers with masked keys", func(t *testing.T) {
		configHome(t)
		cfg := config{Provider: "brave", APIKeys: map[string]string{"brave": "sk-cfg-abcdefghijklmnop"}}
		env := envMap(map[string]string{braveAPIKeyEnvVar: "sk-live-abcdefghijklmnop"})
		msg := strings.Join(showConfigLines(cfg, env, interceptorsConfig{}, false, ""), "\n")
		if !strings.Contains(msg, "sk-l...mnop") {
			t.Fatalf("the env key is masked, got:\n%s", msg)
		}
		if !strings.Contains(msg, "sk-c...mnop") {
			t.Fatalf("the config key is masked, got:\n%s", msg)
		}
		if !strings.Contains(msg, "active provider: brave") {
			t.Fatalf("the active provider is named, got:\n%s", msg)
		}
		for _, name := range knownProviderNames() {
			if !strings.Contains(msg, "  "+name+": ") {
				t.Fatalf("--show omits %q, got:\n%s", name, msg)
			}
		}
	})
	tw(t, fCommand, "--show shows '(not set)' when nothing configured", func(t *testing.T) {
		configHome(t)
		msg := strings.Join(showConfigLines(config{}, noEnv, interceptorsConfig{}, false, ""), "\n")
		if !strings.Contains(msg, "(not set)") {
			t.Fatalf("--show marks the unset state, got:\n%s", msg)
		}
	})
}

func TestCommandPicker(t *testing.T) {
	tw(t, fCommand, "lists active provider first with a ✓ marker", func(t *testing.T) {
		cfg := config{Provider: "exa", APIKeys: map[string]string{"exa": "exa-key"}}
		labels := pickerLabels(t, cfg, noEnv)
		eq(t, labels[0], "Exa ✓ (configured)", "active provider first")
		eq(t, labels[1:], []string{"Brave", "Tavily", "Serper", "You.com", "Jina", "Firecrawl", "Perplexity", "SearXNG", "Ollama"}, "the rest in declaration order")
		checked := 0
		for _, label := range labels {
			if strings.Contains(label, "✓") {
				checked++
			}
		}
		eq(t, checked, 1, "exactly one active marker")
	})
	tw(t, fCommand, "marks every provider with a saved key as (configured)", func(t *testing.T) {
		cfg := config{
			Provider: "exa",
			APIKeys:  map[string]string{"exa": "exa-key", "brave": "brave-key", "tavily": "tavily-key"},
		}
		labels := pickerLabels(t, cfg, noEnv)
		eq(t, labels[0], "Exa ✓ (configured)", "active provider first")
		eq(t, contains(labels, "Brave (configured)"), true, "brave")
		eq(t, contains(labels, "Tavily (configured)"), true, "tavily")
		eq(t, contains(labels, "Serper"), true, "serper is listed unmarked")
		eq(t, contains(labels, "Jina"), true, "jina is listed unmarked")
		eq(t, contains(labels, "Firecrawl"), true, "firecrawl is listed unmarked")
	})
	tw(t, fCommand, "marks provider as (configured) when key is in env var", func(t *testing.T) {
		labels := pickerLabels(t, config{}, envMap(map[string]string{jinaAPIKeyEnvVar: "env-jina-key"}))
		eq(t, contains(labels, "Jina (configured)"), true, "jina is configured through its env var")
	})
	tw(t, fCommand, "defaults to brave-first when no provider is configured", func(t *testing.T) {
		labels := pickerLabels(t, config{}, noEnv)
		eq(t, labels[0], "Brave ✓", "brave is first and active")
	})
}

// The URL lines of --show resolve env over config over default, and the picker treats a self-hosted provider as
// configured only once a URL is really set: the bare default is a hint, not a configuration.
func TestSelfHostedResolution(t *testing.T) {
	searxngMeta, _ := providerMetaByName("searxng")
	t.Run("uses env URL (wins over config and default)", func(t *testing.T) {
		env := envMap(map[string]string{searxngURLEnvVar: "http://env:8080"})
		eq(t, resolveProviderBaseURL(searxngMeta, config{BaseURLs: map[string]string{"searxng": "http://config:8080"}}, env),
			"http://env:8080", "env url")
	})
	t.Run("falls back to config URL when env is unset", func(t *testing.T) {
		eq(t, resolveProviderBaseURL(searxngMeta, config{BaseURLs: map[string]string{"searxng": "http://config:8080"}}, noEnv),
			"http://config:8080", "config url")
	})
	t.Run("falls back to default URL (http://localhost:8080) when neither env nor config is set", func(t *testing.T) {
		eq(t, resolveProviderBaseURL(searxngMeta, config{}, noEnv), "http://localhost:8080", "default url")
	})
	t.Run("--show surfaces the resolved searxng URL and its source", func(t *testing.T) {
		configHome(t)
		for _, c := range []struct {
			cfg  config
			env  func(string) string
			want string
		}{
			{config{}, envMap(map[string]string{searxngURLEnvVar: "http://env:8080"}), "  searxng url: http://env:8080 (source: env)"},
			{config{BaseURLs: map[string]string{"searxng": "http://config:8080"}}, noEnv, "  searxng url: http://config:8080 (source: config)"},
			{config{}, noEnv, "  searxng url: http://localhost:8080 (source: default)"},
		} {
			msg := strings.Join(showConfigLines(c.cfg, c.env, interceptorsConfig{}, false, ""), "\n")
			if !strings.Contains(msg, c.want) {
				t.Fatalf("--show line %q missing, got:\n%s", c.want, msg)
			}
		}
	})
	t.Run("marks searxng (configured) when SEARXNG_URL env is set, but not when only the default applies", func(t *testing.T) {
		eq(t, contains(pickerLabels(t, config{}, envMap(map[string]string{searxngURLEnvVar: "http://env:8080"})), "SearXNG (configured)"),
			true, "env URL configures it")
		eq(t, contains(pickerLabels(t, config{}, noEnv), "SearXNG"), true, "the default alone does not configure it")
		eq(t, contains(pickerLabels(t, config{}, noEnv), "SearXNG (configured)"), false, "no (configured) marker from the default")
	})
}

func TestCommandPromptOutcomes(t *testing.T) {
	t.Run("empty input keeps existing key and persists provider switch", func(t *testing.T) {
		key, ok := keyPromptOutcome(inputOutcome{Value: ""}, "existing")
		if !ok || key != "existing" {
			t.Fatalf("an empty answer keeps the key, got %q ok=%v", key, ok)
		}
	})
	t.Run("select cancelled leaves config untouched", func(t *testing.T) {
		meta, ok := matchProviderByLabel("")
		eq(t, ok && meta.Name == "brave", false, "a cancelled selection resolves to nothing")
	})
	t.Run("input cancelled after select leaves config untouched", func(t *testing.T) {
		_, ok := keyPromptOutcome(inputOutcome{Cancelled: true}, "existing")
		eq(t, ok, false, "a cancelled prompt saves nothing")
	})
	t.Run("empty input after select leaves config untouched when no existing key", func(t *testing.T) {
		_, ok := keyPromptOutcome(inputOutcome{Value: "   "}, "")
		eq(t, ok, false, "an empty answer with no existing key saves nothing")
	})
	t.Run("resolves a picked label back to its provider, marker suffix and all", func(t *testing.T) {
		meta, ok := matchProviderByLabel("Exa ✓ (configured)")
		if !ok || meta.Name != "exa" {
			t.Fatalf("the label resolves to exa, got %q ok=%v", meta.Name, ok)
		}
		if _, ok := matchProviderByLabel("Nonexistent"); ok {
			t.Fatal("an unknown label resolves to nothing")
		}
	})
	t.Run("prompts with the masked preview when a key exists", func(t *testing.T) {
		eq(t, keyPromptPlaceholder(""), "...", "no key")
		eq(t, keyPromptPlaceholder("sk-live-abcdefghijklmnop"),
			"Press Enter to keep current (sk-l...mnop), or type new key", "masked preview")
	})
	t.Run("names the disk write, not the vendor, when a save fails", func(t *testing.T) {
		eq(t, keySaveFailedMessage("Brave", "/x/config.json"),
			"Failed to save Brave API key to /x/config.json — disk write failed", "failure text")
		eq(t, keySavedMessage("Brave", "/x/config.json", true),
			"Saved Brave API key to /x/config.json", "typed key")
		eq(t, keySavedMessage("Brave", "/x/config.json", false),
			"Active provider set to Brave; existing key kept", "empty answer")
	})
	t.Run("reports a save failure instead of claiming success", func(t *testing.T) {
		configHome(t)
		_, path := configHome(t)
		// A directory at the config path makes the write fail the way a full disk would.
		if err := mkdirAt(path); err != nil {
			t.Fatal(err)
		}
		saved := saveProviderConfig(config{}, "brave", providerConfigChange{APIKey: "k", HasAPIKey: true})
		eq(t, WriteConfig(saved), false, "the write fails")
	})
}

// contains is the label-list membership assertion the picker twins share.
func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// anyOther is the decoded form of an unknown value carried through the config.
type anyOther = json.RawMessage

// jsonString encodes a value the way the fixture literals do.
func jsonString(v string) anyOther { return anyOther(`"` + v + `"`) }

// mkdirAt creates a directory where the config file is expected, so a write fails.
func mkdirAt(path string) error { return os.MkdirAll(path, 0o755) }
