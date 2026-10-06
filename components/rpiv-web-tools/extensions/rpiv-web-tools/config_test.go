// SPDX-License-Identifier: MIT

package rpiv_web_tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const fConfig = "providers/config"

// configHome points HOME at a temp dir, clears XDG_CONFIG_HOME, and returns a writer for the config file and its
// path. upstream: providers/config.test.ts beforeEach + writeRaw.
func configHome(t *testing.T) (write func(contents string), path string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	path = ConfigPath()
	write = func(contents string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return write, path
}

// eq is the deep-equality assertion the twins share.
func eq(t *testing.T, got, want any, what string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: got %#v, want %#v", what, got, want)
	}
}

// mustJSON decodes a JSON literal the way the twin fixtures do, so the assertion compares values, not text.
func mustJSON(t *testing.T, raw string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("fixture is not a JSON object: %v", err)
	}
	return out
}

func TestGetConfigPath(t *testing.T) {
	tw(t, fConfig, "returns the canonical ~/.config/rpiv-web-tools/config.json", func(t *testing.T) {
		_, path := configHome(t)
		eq(t, GetConfigPath(), path, "config path")
		eq(t, strings.HasSuffix(path, filepath.Join(".config", "rpiv-web-tools", "config.json")), true, "canonical suffix")
	})
}

func TestReadConfigFailSoft(t *testing.T) {
	tw(t, fConfig, "returns {} when the file does not exist", func(t *testing.T) {
		configHome(t)
		eq(t, ReadConfig(), config{}, "readConfig")
	})
	tw(t, fConfig, "returns {} on malformed JSON (matches loadJsonConfig tolerance)", func(t *testing.T) {
		write, _ := configHome(t)
		write("{ not valid json")
		eq(t, ReadConfig(), config{}, "readConfig")
	})
	tw(t, fConfig, "returns {} when the file is a directory (EISDIR)", func(t *testing.T) {
		_, path := configHome(t)
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		eq(t, ReadConfig(), config{}, "readConfig")
	})
	tw(t, fConfig, "drops a wrong-typed field and returns {} when nothing else is present", func(t *testing.T) {
		write, _ := configHome(t)
		write(`{"provider":123}`)
		eq(t, ReadConfig(), config{}, "readConfig")
	})
}

func TestReadConfigSalvage(t *testing.T) {
	tw(t, fConfig, "keeps the rest of the config when one top-level field is wrong-typed", func(t *testing.T) {
		write, _ := configHome(t)
		write(`{"provider":123,"apiKeys":{"brave":"k"},"otherField":"keep"}`)
		got := ReadConfig()
		eq(t, got.APIKeys, map[string]string{"brave": "k"}, "apiKeys")
		eq(t, got.Provider, "", "wrong-typed provider dropped")
		eq(t, string(got.extra["otherField"]), `"keep"`, "unknown key kept")
	})
	tw(t, fConfig, "drops only the offending guidance leaf (wrong-typed description), keeping its siblings", func(t *testing.T) {
		// The GuidanceFields.description enrollment regression: a wrong-typed nested leaf must cost that field
		// alone, never the whole config.
		write, _ := configHome(t)
		write(`{"provider":"brave","apiKeys":{"brave":"k"},"guidance":{"web_search":{"promptSnippet":"snip","description":123},"web_fetch":{"promptSnippet":"snip2"}}}`)
		got := ReadConfig()
		eq(t, got.Provider, "brave", "provider")
		eq(t, got.APIKeys, map[string]string{"brave": "k"}, "apiKeys")
		eq(t, got.Guidance.WebSearch.PromptSnippet, "snip", "web_search.promptSnippet")
		eq(t, got.Guidance.WebSearch.Description, "", "description dropped")
		eq(t, got.Guidance.WebFetch.PromptSnippet, "snip2", "web_fetch.promptSnippet")
	})
	tw(t, fConfig, "drops a whole array field on one bad element rather than leaving a sparse hole", func(t *testing.T) {
		write, _ := configHome(t)
		write(`{"provider":"brave","guidance":{"web_search":{"promptSnippet":"snip","promptGuidelines":["a",5]}}}`)
		got := ReadConfig()
		eq(t, got.Provider, "brave", "provider")
		eq(t, got.Guidance.WebSearch.PromptSnippet, "snip", "sibling kept")
		eq(t, got.Guidance.WebSearch.PromptGuidelines, []string(nil), "whole array field dropped")
	})
	tw(t, fConfig, "drops a wrong-typed interceptors union field, keeping the rest", func(t *testing.T) {
		write, _ := configHome(t)
		write(`{"provider":"brave","interceptors":"nope"}`)
		got := ReadConfig()
		eq(t, got.Provider, "brave", "provider")
		eq(t, got.Interceptors, (*interceptorsConfig)(nil), "interceptors dropped")
	})
	tw(t, fConfig, "drops a single wrong-typed apiKeys record entry, keeping the valid keys", func(t *testing.T) {
		write, _ := configHome(t)
		write(`{"provider":"brave","apiKeys":{"brave":"k","broken":7}}`)
		got := ReadConfig()
		eq(t, got.Provider, "brave", "provider")
		eq(t, got.APIKeys, map[string]string{"brave": "k"}, "valid keys kept")
	})
}

func TestReadConfigReleasedShape(t *testing.T) {
	tw(t, fConfig, "loads a minimal { provider, apiKeys } config unchanged", func(t *testing.T) {
		write, _ := configHome(t)
		write(`{"provider":"brave","apiKeys":{"brave":"k"}}`)
		got := ReadConfig()
		eq(t, got.Provider, "brave", "provider")
		eq(t, got.APIKeys, map[string]string{"brave": "k"}, "apiKeys")
	})
	tw(t, fConfig, "loads the legacy top-level apiKey field", func(t *testing.T) {
		write, _ := configHome(t)
		write(`{"apiKey":"legacy"}`)
		eq(t, ReadConfig().APIKey, "legacy", "legacy apiKey")
	})
	tw(t, fConfig, "preserves unknown top-level keys (otherField round-trip contract)", func(t *testing.T) {
		// The released /web-tools migrate-legacy-apiKey twin relies on this: unknown keys MUST NOT be stripped by
		// the schema reader.
		write, _ := configHome(t)
		write(`{"apiKey":"k","otherField":"keep"}`)
		got := ReadConfig()
		eq(t, string(got.extra["otherField"]), `"keep"`, "otherField")
	})
	tw(t, fConfig, "loads the guidance subtree with web_search + web_fetch", func(t *testing.T) {
		write, _ := configHome(t)
		write(`{"guidance":{"web_search":{"promptSnippet":"snip","promptGuidelines":["a","b"]},"web_fetch":{"promptSnippet":"snip2"}}}`)
		got := ReadConfig()
		eq(t, got.Guidance.WebSearch.PromptSnippet, "snip", "web_search.promptSnippet")
		eq(t, got.Guidance.WebSearch.PromptGuidelines, []string{"a", "b"}, "web_search.promptGuidelines")
		eq(t, got.Guidance.WebFetch.PromptSnippet, "snip2", "web_fetch.promptSnippet")
	})
}

func TestReadConfigGitHubUnion(t *testing.T) {
	tw(t, fConfig, "accepts the boolean true shorthand", func(t *testing.T) {
		write, _ := configHome(t)
		write(`{"interceptors":{"github":true}}`)
		eq(t, ReadConfig().Interceptors.GitHub.Disabled, false, "github enabled")
	})
	tw(t, fConfig, "accepts the boolean false shorthand", func(t *testing.T) {
		write, _ := configHome(t)
		write(`{"interceptors":{"github":false}}`)
		eq(t, ReadConfig().Interceptors.GitHub.Disabled, true, "github disabled")
	})
	tw(t, fConfig, "accepts the object override form", func(t *testing.T) {
		write, _ := configHome(t)
		write(`{"interceptors":{"github":{"maxRepoSizeMB":1000,"clonePath":"/x"}}}`)
		gh := ReadConfig().Interceptors.GitHub
		eq(t, gh.Object != nil, true, "object form")
		eq(t, *gh.Object.MaxRepoSizeMB, 1000.0, "maxRepoSizeMB")
		eq(t, *gh.Object.ClonePath, "/x", "clonePath")
	})
	tw(t, fConfig, "drops only the type-incompatible github entry (per-field salvage)", func(t *testing.T) {
		// A number is neither boolean nor a GitHubInterceptorOptions object: the offending field alone falls back,
		// not the whole config.
		write, _ := configHome(t)
		write(`{"provider":"brave","interceptors":{"github":42}}`)
		got := ReadConfig()
		eq(t, got.Provider, "brave", "provider")
		eq(t, got.Interceptors != nil, true, "interceptors kept")
		eq(t, got.Interceptors.GitHub, (*githubInterceptor)(nil), "github entry dropped")
	})
}

func TestWriteConfig(t *testing.T) {
	tw(t, fConfig, "round-trips a config through readConfig", func(t *testing.T) {
		configHome(t)
		eq(t, WriteConfig(config{Provider: "brave", APIKeys: map[string]string{"brave": "k"}}), true, "writeConfig")
		got := ReadConfig()
		eq(t, got.Provider, "brave", "provider")
		eq(t, got.APIKeys, map[string]string{"brave": "k"}, "apiKeys")
	})
	tw(t, fConfig, "preserves the interceptors.github stanza across save+load", func(t *testing.T) {
		configHome(t)
		size := 500.0
		eq(t, WriteConfig(config{Interceptors: &interceptorsConfig{GitHub: &githubInterceptor{Object: &githubInterceptorOptions{MaxRepoSizeMB: &size}}}}), true, "writeConfig")
		gh := ReadConfig().Interceptors.GitHub
		eq(t, gh.Object != nil, true, "object form")
		eq(t, *gh.Object.MaxRepoSizeMB, 500.0, "maxRepoSizeMB")
	})
}

// The schema-only sanity twin has no Go counterpart: the port has no TypeBox, so there is no schema object to ask
// for a type. The object shape the case guards is covered by the salvage and round-trip twins above.
func TestSchemaOnlySanity(t *testing.T) {
	tskip(t, fConfig, "exists and is a TypeBox object",
		"the port has no TypeBox: the schema is the config struct, and its object shape is covered by the salvage and round-trip twins")
}
