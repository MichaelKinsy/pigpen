package rpiv_web_tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfig(t *testing.T) {
	const f = "providers/config"
	tw(t, f, "returns the canonical ~/.config/rpiv-web-tools/config.json", func(t *testing.T) {
		clearEnv(t)
		eq(t, configPath(), filepath.Join(os.Getenv("HOME"), ".config", "rpiv-web-tools", "config.json"))
	})
	tw(t, f, "returns {} when the file does not exist", func(t *testing.T) {
		clearEnv(t)
		eq(t, readConfig(), webToolsConfig{})
	})
	tw(t, f, "returns {} on malformed JSON (matches loadJsonConfig tolerance)", func(t *testing.T) {
		clearEnv(t)
		writeRaw(t, "{ not valid json")
		eq(t, readConfig(), webToolsConfig{})
	})
	tw(t, f, "returns {} when the file is a directory (EISDIR)", func(t *testing.T) {
		clearEnv(t)
		os.MkdirAll(configPath(), 0o755)
		eq(t, readConfig(), webToolsConfig{})
	})
	tw(t, f, "drops a wrong-typed field and returns {} when nothing else is present", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, obj{"provider": 123})
		eq(t, readConfig(), webToolsConfig{})
	})
	tw(t, f, "keeps the rest of the config when one top-level field is wrong-typed", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, obj{"provider": 123, "apiKeys": obj{"brave": "k"}, "otherField": "keep"})
		eq(t, readConfig(), webToolsConfig{"apiKeys": obj{"brave": "k"}, "otherField": "keep"})
	})
	tw(t, f, "drops only the offending guidance leaf (wrong-typed description), keeping its siblings", func(t *testing.T) {
		// The GuidanceFields.description enrollment regression: a wrong-typed nested leaf must cost that field
		// alone, never the whole config.
		clearEnv(t)
		writeConfigFile(t, obj{"provider": "brave", "apiKeys": obj{"brave": "k"},
			"guidance": obj{"web_search": obj{"promptSnippet": "keep me", "description": float64(123)}, "web_fetch": obj{"promptSnippet": "also kept"}}})
		eq(t, readConfig(), webToolsConfig{"provider": "brave", "apiKeys": obj{"brave": "k"},
			"guidance": obj{"web_search": obj{"promptSnippet": "keep me"}, "web_fetch": obj{"promptSnippet": "also kept"}}})
	})
	tw(t, f, "drops a whole array field on one bad element rather than leaving a sparse hole", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, obj{"provider": "brave", "guidance": obj{"web_search": obj{"promptSnippet": "kept", "promptGuidelines": arr{"ok", float64(2)}}}})
		eq(t, readConfig(), webToolsConfig{"provider": "brave", "guidance": obj{"web_search": obj{"promptSnippet": "kept"}}})
	})
	tw(t, f, "drops a wrong-typed interceptors union field, keeping the rest", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, obj{"provider": "brave", "interceptors": obj{"github": "yes"}})
		eq(t, readConfig(), webToolsConfig{"provider": "brave", "interceptors": obj{}})
	})
	tw(t, f, "drops a single wrong-typed apiKeys record entry, keeping the valid keys", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, obj{"apiKeys": obj{"brave": "good", "tavily": float64(5)}})
		eq(t, readConfig(), webToolsConfig{"apiKeys": obj{"brave": "good"}})
	})
	tw(t, f, "loads a minimal { provider, apiKeys } config unchanged", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, obj{"provider": "tavily", "apiKeys": obj{"tavily": "k"}})
		eq(t, readConfig(), webToolsConfig{"provider": "tavily", "apiKeys": obj{"tavily": "k"}})
	})
	tw(t, f, "loads the legacy top-level apiKey field", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, obj{"apiKey": "legacy"})
		eq(t, readConfig(), webToolsConfig{"apiKey": "legacy"})
	})
	tw(t, f, "preserves unknown top-level keys (otherField round-trip contract)", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, obj{"provider": "brave", "otherField": obj{"nested": true}})
		eq(t, readConfig(), webToolsConfig{"provider": "brave", "otherField": obj{"nested": true}})
	})
	tw(t, f, "loads the guidance subtree with web_search + web_fetch", func(t *testing.T) {
		clearEnv(t)
		g := obj{"web_search": obj{"promptSnippet": "s", "promptGuidelines": arr{"a"}}, "web_fetch": obj{"description": "d"}}
		writeConfigFile(t, obj{"guidance": g})
		eq(t, readConfig(), webToolsConfig{"guidance": g})
	})
	tw(t, f, "accepts the boolean true shorthand", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, obj{"interceptors": obj{"github": true}})
		eq(t, readConfig(), webToolsConfig{"interceptors": obj{"github": true}})
	})
	tw(t, f, "accepts the boolean false shorthand", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, obj{"interceptors": obj{"github": false}})
		eq(t, readConfig(), webToolsConfig{"interceptors": obj{"github": false}})
	})
	tw(t, f, "accepts the object override form", func(t *testing.T) {
		clearEnv(t)
		gh := obj{"enabled": true, "maxRepoSizeMB": float64(50), "cloneTimeoutSeconds": float64(30), "clonePath": "/tmp/clones"}
		writeConfigFile(t, obj{"interceptors": obj{"github": gh}})
		eq(t, readConfig(), webToolsConfig{"interceptors": obj{"github": gh}})
	})
	tw(t, f, "drops only the type-incompatible github entry (per-field salvage)", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, obj{"provider": "exa", "interceptors": obj{"github": obj{"enabled": "yes"}}})
		eq(t, readConfig(), webToolsConfig{"provider": "exa", "interceptors": obj{}})
	})
	tw(t, f, "round-trips a config through readConfig", func(t *testing.T) {
		clearEnv(t)
		c := webToolsConfig{"provider": "serper", "apiKeys": obj{"serper": "k"}, "baseUrls": obj{"searxng": "http://x"}}
		eq(t, writeConfig(c), true)
		eq(t, readConfig(), c)
		info, _ := os.Stat(configPath())
		eq(t, info.Mode().Perm(), os.FileMode(0o600))
	})
	tw(t, f, "preserves the interceptors.github stanza across save+load", func(t *testing.T) {
		clearEnv(t)
		c := webToolsConfig{"interceptors": obj{"github": obj{"enabled": true, "clonePath": "/p"}}}
		writeConfig(c)
		eq(t, readConfig(), c)
	})
	tskip(t, f, "exists and is a TypeBox object", "the original asserts that its exported WebToolsConfigSchema is a TypeBox object; the schema here is the errorPaths function, exercised by every other case of this file")
	tskip(t, "ship-manifest", "`package.json` `files` array covers every production .ts module across the tree", "the original asserts the npm `files` manifest of its own package.json; this Package's manifest is checked by npm run check (npm-packages) instead")
}
