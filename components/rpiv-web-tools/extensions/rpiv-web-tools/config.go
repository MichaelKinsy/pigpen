// SPDX-License-Identifier: MIT

// Package rpiv_web_tools is a Go port of @juicesharp/rpiv-web-tools 2.12.0: web search and fetch for the model
// with pluggable providers. See CREDITS.md for the upstream, its pinned commit and the license.
package rpiv_web_tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// The reader and writer for ~/.config/rpiv-web-tools/config.json.
//
// Every schema field is optional and unknown keys pass through (additionalProperties: true), so a config carrying
// legacy or unrelated fields keeps working: that is the contract the released `/web-tools` legacy-apiKey migration and
// the `otherField` preservation twin depend on.
//
// Validation is fail-soft, matching loadJsonConfig and validateConfig in rpiv-config: malformed JSON and EISDIR
// degrade to an empty config, and a schema violation degrades per field, so only the offending paths are dropped. The
// orchestrator never has to handle "config blew up at startup."

// configDirName is the directory rpiv-config's configPath builds for this package. upstream: providers/config.ts
// CONFIG_PATH.
const configDirName = "rpiv-web-tools"

// knownFields is the schema's own field list, in the order the writer emits them. Anything else in the document is an
// additionalProperty and is preserved verbatim. upstream: providers/config.ts WebToolsConfigSchema.
var knownFields = []string{"provider", "apiKeys", "baseUrls", "apiKey", "guidance", "interceptors"}

// config is the canonical config shape. upstream: providers/config.ts WebToolsConfigSchema.
type config struct {
	Provider string
	APIKeys  map[string]string
	BaseURLs map[string]string
	// APIKey is the legacy top-level Brave key, auto-migrated to apiKeys.brave by the /web-tools save path. It stays
	// in the shape for the load-and-rewrite round trip.
	APIKey       string
	Guidance     *guidance
	Interceptors *interceptorsConfig

	// extra holds every additionalProperty in document order, so an unknown key survives load and save untouched.
	extra map[string]json.RawMessage
	order []string
}

// guidanceFields is one tool's guidance leaf. upstream: @juicesharp/rpiv-config GuidanceFieldsSchema, composed into
// web-tools' own tool-namespaced shell by WebToolsGuidanceSchema.
type guidanceFields struct {
	PromptSnippet    string
	PromptGuidelines []string
	Description      string
	extra            map[string]json.RawMessage
	order            []string
}

// guidance is the tool-namespaced shell: one leaf per tool, both optional. upstream: providers/config.ts
// WebToolsGuidanceSchema.
type guidance struct {
	WebSearch *guidanceFields
	WebFetch  *guidanceFields
}

// githubInterceptorOptions is the object form of the github stanza. upstream: providers/config.ts
// GitHubInterceptorOptionsSchema.
type githubInterceptorOptions struct {
	Enabled             *bool
	MaxRepoSizeMB       *float64
	CloneTimeoutSeconds *float64
	ClonePath           *string
}

// githubInterceptor is the github stanza: the boolean shorthand or the object override form. `enabled: false` inside
// the object form is allowed but redundant; use the top-level false. upstream: providers/config.ts
// InterceptorsConfigSchema.
type githubInterceptor struct {
	Disabled bool
	Object   *githubInterceptorOptions
}

// interceptorsConfig is the per-family interceptor settings. upstream: providers/config.ts InterceptorsConfigSchema.
type interceptorsConfig struct {
	GitHub *githubInterceptor
}

// ConfigPath is the canonical config file path: XDG_CONFIG_HOME when it is absolute, ~/.config otherwise. A relative
// XDG_CONFIG_HOME is ignored, matching rpiv-config's configPath. upstream: @juicesharp/rpiv-config configPath,
// reached through providers/config.ts CONFIG_PATH.
func ConfigPath() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(xdg) {
		return filepath.Join(xdg, configDirName, "config.json")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = os.Getenv("USERPROFILE")
	}
	if home == "" {
		return filepath.Join(".config", configDirName, "config.json")
	}
	return filepath.Join(home, ".config", configDirName, "config.json")
}

// GetConfigPath is the canonical config file path. upstream: providers/config.ts getConfigPath.
func GetConfigPath() string { return ConfigPath() }

// pointerSegments splits a Value.Errors instancePath into its segments (~1 is /, ~0 is ~). upstream:
// providers/config.ts pointerSegments.
func pointerSegments(instancePath string) []string {
	if instancePath == "" {
		return nil
	}
	parts := strings.Split(instancePath, "/")[1:]
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.ReplaceAll(strings.ReplaceAll(p, "~1", "/"), "~0", "~"))
	}
	return out
}

// escapePointer escapes one JSON-pointer segment. upstream: providers/config.ts pointerSegments.
func escapePointer(segment string) string {
	return strings.ReplaceAll(strings.ReplaceAll(segment, "~", "~0"), "/", "~1")
}

// deleteAtPointer deletes the value at segs, widening to the nearest containing FIELD when the path descends into an
// array: deleting one element would leave a sparse hole that still fails validation, so the whole array field falls
// back instead. It reports false when the path is already gone, which happens once a prior deletion took an ancestor.
// upstream: providers/config.ts deleteAtPointer.
func deleteAtPointer(root map[string]any, segs []string) bool {
	parent := root
	for i := 0; i < len(segs)-1; i++ {
		next, ok := parent[segs[i]]
		if !ok {
			return false
		}
		switch next.(type) {
		case map[string]any:
			parent = next.(map[string]any)
		case []any:
			delete(parent, segs[i])
			return true
		default:
			return false
		}
	}
	leaf := segs[len(segs)-1]
	if _, ok := parent[leaf]; !ok {
		return false
	}
	delete(parent, leaf)
	return true
}

// validate returns every schema violation as a JSON-pointer path, in document order. A root-level violation (the
// empty path) means the document is not an object at all. upstream: providers/config.ts salvageConfig over
// Value.Errors.
func validate(raw map[string]any) []string {
	var errs []string
	for _, field := range knownFields {
		value, present := raw[field]
		if !present {
			continue
		}
		switch field {
		case "provider", "apiKey":
			if _, ok := value.(string); !ok {
				errs = append(errs, "/"+field)
			}
		case "apiKeys", "baseUrls":
			rec, ok := value.(map[string]any)
			if !ok {
				errs = append(errs, "/"+field)
				continue
			}
			for _, key := range sortedKeys(rec) {
				if _, ok := rec[key].(string); !ok {
					errs = append(errs, "/"+field+"/"+escapePointer(key))
				}
			}
		case "guidance":
			g, ok := value.(map[string]any)
			if !ok {
				errs = append(errs, "/"+field)
				continue
			}
			for _, tool := range []string{"web_search", "web_fetch"} {
				leaf, present := g[tool]
				if !present {
					continue
				}
				fields, ok := leaf.(map[string]any)
				if !ok {
					errs = append(errs, "/guidance/"+tool)
					continue
				}
				errs = append(errs, validateGuidanceLeaf(fields, "/guidance/"+tool)...)
			}
		case "interceptors":
			errs = append(errs, validateInterceptors(value)...)
		}
	}
	return errs
}

// validateGuidanceLeaf reports the typed fields of one guidance leaf. An array element that is not a string reports
// its own index, and deleteAtPointer widens that to the whole array field, because dropping one element would leave a
// sparse hole that still fails validation. upstream: providers/config.ts deleteAtPointer's array widening, applied to
// the shared GuidanceFieldsSchema leaf.
func validateGuidanceLeaf(fields map[string]any, base string) []string {
	var errs []string
	for _, key := range sortedKeys(fields) {
		switch key {
		case "promptSnippet", "description":
			if _, ok := fields[key].(string); !ok {
				errs = append(errs, base+"/"+key)
			}
		case "promptGuidelines":
			list, ok := fields[key].([]any)
			if !ok {
				errs = append(errs, base+"/"+key)
				continue
			}
			for i, e := range list {
				if _, ok := e.(string); !ok {
					errs = append(errs, base+"/"+key+"/"+itoa(i))
				}
			}
		}
	}
	return errs
}

// validateInterceptors checks the github union: a boolean, or an object whose four known fields are well typed. A
// value that is neither fails the union as a whole. upstream: providers/config.ts validateInterceptors over the
// InterceptorsConfigSchema union.
func validateInterceptors(value any) []string {
	ic, ok := value.(map[string]any)
	if !ok {
		return []string{"/interceptors"}
	}
	gh, present := ic["github"]
	if !present {
		return nil
	}
	switch g := gh.(type) {
	case bool:
		return nil
	case map[string]any:
		var errs []string
		for _, field := range sortedKeys(g) {
			want, known := map[string]string{
				"enabled": "bool", "maxRepoSizeMB": "number", "cloneTimeoutSeconds": "number", "clonePath": "string",
			}[field]
			if !known {
				continue // an additionalProperty is never an error
			}
			if !typeMatches(want, g[field]) {
				errs = append(errs, "/interceptors/github/"+escapePointer(field))
			}
		}
		return errs
	default:
		// The union rejects the leaf as a whole. Typebox reports the union path and its nested cause; the salvage
		// pass is bounded, so the cascade cannot loop.
		return []string{"/interceptors/github"}
	}
}

func typeMatches(want string, v any) bool {
	switch want {
	case "bool":
		_, ok := v.(bool)
		return ok
	case "string":
		_, ok := v.(string)
		return ok
	default: // number
		switch n := v.(type) {
		case float64:
			return true
		case json.Number:
			_, err := n.Float64()
			return err == nil
		}
		return false
	}
}

// salvageConfig drops exactly the offending paths and keeps everything else, so one wrong-typed leaf (say
// `guidance.web_search.description: 123`) costs that field alone rather than provider, apiKeys, baseUrls,
// interceptors and both guidance subtrees for the session. Unknown keys are untouched, which is what preserves the
// pass-through contract. The pass loop is bounded at five: a round that deletes nothing, or a config that still fails
// afterwards, degrades to the empty config exactly as before. upstream: providers/config.ts salvageConfig.
func salvageConfig(raw map[string]any) (map[string]any, bool) {
	cfg := deepCopyMap(raw)
	for pass := 0; pass < 5; pass++ {
		if len(validate(cfg)) == 0 {
			return cfg, true
		}
		deleted := false
		for _, err := range validate(cfg) {
			segs := pointerSegments(err)
			if len(segs) == 0 {
				return nil, false // the root itself is invalid: unsalvageable
			}
			if deleteAtPointer(cfg, segs) {
				deleted = true
			}
		}
		if !deleted {
			return nil, false
		}
	}
	return cfg, len(validate(cfg)) == 0
}

// ReadConfig is the tolerant read. A missing file, malformed JSON, a directory or a literal null all degrade to an
// empty config; a schema violation degrades per field, and the whole file degrades only when nothing salvageable is
// left. upstream: providers/config.ts readConfig.
func ReadConfig() config {
	raw := loadJSONConfigTolerant(ConfigPath())
	if raw == nil {
		return config{}
	}
	if len(validate(raw)) == 0 {
		return configFromMap(raw)
	}
	if salvaged, ok := salvageConfig(raw); ok {
		return configFromMap(salvaged)
	}
	return config{}
}

// WriteConfig persists the config with the host's private permissions. upstream: providers/config.ts writeConfig via
// @juicesharp/rpiv-config saveJsonConfig.
func WriteConfig(c config) bool {
	path := ConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return false
	}
	return os.WriteFile(path, append(data, '\n'), 0o600) == nil
}

// InvalidateConfigCache is deliberately a no-op: there is no in-memory config cache, because a direct-write test
// pattern would turn per-test invalidation into a rewrite-the-suite job for marginal perf. It stays exported so callers
// keep a stable invalidation hook. upstream: providers/config.ts invalidateConfigCache.
func InvalidateConfigCache() {}

// loadJSONConfigTolerant reads the file the way rpiv-config's loadJsonConfig does: a missing file, malformed JSON and
// EISDIR all become an empty object rather than an error, and nil marks "there was nothing to load". upstream:
// @juicesharp/rpiv-config loadJsonConfig, reached through providers/config.ts readConfig.
func loadJSONConfigTolerant(path string) map[string]any {
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]any{} // missing file, EISDIR, or an unreadable path
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return map[string]any{}
	}
	if raw == nil {
		return map[string]any{} // a literal null is an empty config
	}
	return raw
}

// deepCopyMap copies a decoded JSON object so the salvage pass never mutates the document it was handed.
func deepCopyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = deepCopyValue(v)
	}
	return out
}

func deepCopyValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return deepCopyMap(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = deepCopyValue(e)
		}
		return out
	default:
		return v
	}
}
