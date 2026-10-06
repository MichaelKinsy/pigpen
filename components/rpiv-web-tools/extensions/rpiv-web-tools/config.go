package rpiv_web_tools

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// The config file: <config dir>/rpiv-web-tools/config.json, with the config dir resolved as rpiv-config does
// ($XDG_CONFIG_HOME when it is set and absolute, else ~/.config) and the ~/.config copy read as a legacy fallback.
// upstream: providers/config.ts and @juicesharp/rpiv-config 2.12.0 config.ts.

const configName = "rpiv-web-tools"

func homeDir() string { h, _ := os.UserHomeDir(); return h }

func expandTilde(p string) string {
	switch {
	case p == "~":
		return homeDir()
	case strings.HasPrefix(p, "~/"):
		return filepath.Join(homeDir(), p[2:])
	}
	return p
}

func defaultConfigDir() string { return filepath.Join(homeDir(), ".config") }

// resolveConfigDir: $XDG_CONFIG_HOME when set (a leading ~ is expanded) and absolute, else ~/.config.
func resolveConfigDir() string {
	xdg := jsTrim(os.Getenv("XDG_CONFIG_HOME"))
	if xdg == "" {
		return defaultConfigDir()
	}
	expanded := expandTilde(xdg)
	if filepath.IsAbs(expanded) {
		return expanded
	}
	return defaultConfigDir()
}

// configPath is the file this extension writes (resolved per call; the original resolves it at load).
func configPath() string { return filepath.Join(resolveConfigDir(), configName, "config.json") }

// loadJSONConfig reads a JSON object; a missing, malformed or non-object file reads as empty.
func loadJSONConfig(path string) map[string]any {
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]any{}
	}
	var parsed any
	if json.Unmarshal(data, &parsed) != nil {
		return map[string]any{}
	}
	if m, ok := parsed.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

// webToolsConfig is the decoded file: every field optional, unknown keys kept.
type webToolsConfig map[string]any

func (c webToolsConfig) str(key string) (string, bool) { s, ok := c[key].(string); return s, ok }

func (c webToolsConfig) apiKey(provider string) (string, bool) {
	if m, ok := c["apiKeys"].(map[string]any); ok {
		s, ok := m[provider].(string)
		return s, ok
	}
	return "", false
}

func (c webToolsConfig) baseURL(provider string) (string, bool) {
	if m, ok := c["baseUrls"].(map[string]any); ok {
		s, ok := m[provider].(string)
		return s, ok
	}
	return "", false
}

// errorPaths lists the JSON-pointer segments (as the original's schema reports them) of every value that
// breaks the schema. upstream: providers/config.ts WebToolsConfigSchema.
func errorPaths(cfg map[string]any) [][]string {
	var out [][]string
	bad := func(segs ...string) { out = append(out, segs) }
	if v, ok := cfg["provider"]; ok {
		if _, ok := v.(string); !ok {
			bad("provider")
		}
	}
	if v, ok := cfg["apiKey"]; ok {
		if _, ok := v.(string); !ok {
			bad("apiKey")
		}
	}
	for _, field := range []string{"apiKeys", "baseUrls"} {
		v, ok := cfg[field]
		if !ok {
			continue
		}
		m, isMap := v.(map[string]any)
		if !isMap {
			bad(field)
			continue
		}
		keys := sortedKeys(m)
		for _, k := range keys {
			if _, ok := m[k].(string); !ok {
				bad(field, k)
			}
		}
	}
	if g, ok := cfg["guidance"]; ok {
		gm, isMap := g.(map[string]any)
		if !isMap {
			bad("guidance")
		} else {
			for _, tool := range []string{"web_search", "web_fetch"} {
				f, ok := gm[tool]
				if !ok {
					continue
				}
				fm, isMap := f.(map[string]any)
				if !isMap {
					bad("guidance", tool)
					continue
				}
				for _, k := range []string{"promptSnippet", "description"} {
					if v, ok := fm[k]; ok {
						if _, ok := v.(string); !ok {
							bad("guidance", tool, k)
						}
					}
				}
				if v, ok := fm["promptGuidelines"]; ok {
					l, isList := v.([]any)
					if !isList {
						bad("guidance", tool, "promptGuidelines")
					} else {
						for i, e := range l {
							if _, ok := e.(string); !ok {
								bad("guidance", tool, "promptGuidelines", itoa(i))
							}
						}
					}
				}
			}
		}
	}
	if iv, ok := cfg["interceptors"]; ok {
		im, isMap := iv.(map[string]any)
		if !isMap {
			bad("interceptors")
		} else if gh, ok := im["github"]; ok {
			valid := false
			switch g := gh.(type) {
			case bool:
				valid = true
			case map[string]any:
				valid = true
				for _, k := range []string{"enabled"} {
					if v, ok := g[k]; ok {
						if _, ok := v.(bool); !ok {
							valid = false
						}
					}
				}
				for _, k := range []string{"maxRepoSizeMB", "cloneTimeoutSeconds"} {
					if v, ok := g[k]; ok {
						if _, ok := v.(float64); !ok {
							valid = false
						}
					}
				}
				if v, ok := g["clonePath"]; ok {
					if _, ok := v.(string); !ok {
						valid = false
					}
				}
			}
			if !valid {
				bad("interceptors", "github") // a union reports the field itself
			}
		}
	}
	return out
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func itoa(i int) string { b, _ := marshalJS(i); return string(b) }

// deleteAtPointer deletes the value at segs, widening to the containing field when the path descends into an
// array (deleting one element would leave a hole that still fails). It reports false when the path is gone.
func deleteAtPointer(root map[string]any, segs []string) bool {
	parent := root
	for i := 0; i < len(segs)-1; i++ {
		next, ok := parent[segs[i]]
		if !ok || next == nil {
			return false
		}
		switch n := next.(type) {
		case []any:
			delete(parent, segs[i])
			return true
		case map[string]any:
			parent = n
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

// salvageConfig drops exactly the offending values and keeps the rest, so one wrong-typed leaf costs that
// field alone; unknown keys are untouched. upstream: providers/config.ts salvageConfig.
func salvageConfig(raw map[string]any) map[string]any {
	data, _ := json.Marshal(raw)
	var cfg map[string]any
	json.Unmarshal(data, &cfg) // a deep copy
	for pass := 0; pass < 5; pass++ {
		errs := errorPaths(cfg)
		if len(errs) == 0 {
			return cfg
		}
		deleted := false
		for _, segs := range errs {
			if deleteAtPointer(cfg, segs) {
				deleted = true
			}
		}
		if !deleted {
			return nil
		}
	}
	if len(errorPaths(cfg)) == 0 {
		return cfg
	}
	return nil
}

// readConfig reads the config fail-soft: malformed JSON and a non-object read as empty, a schema violation
// drops the offending fields only.
func readConfig() webToolsConfig {
	path := configPath()
	var raw map[string]any
	if _, err := os.Stat(path); err == nil {
		raw = loadJSONConfig(path)
	} else {
		raw = loadJSONConfig(filepath.Join(defaultConfigDir(), configName, "config.json"))
	}
	if len(errorPaths(raw)) == 0 {
		return raw
	}
	if s := salvageConfig(raw); s != nil {
		return s
	}
	return webToolsConfig{}
}

// writeConfig saves the config (2-space JSON, mode 0600 where the filesystem allows) and reports success.
func writeConfig(c webToolsConfig) bool {
	path := configPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(map[string]any(c)); err != nil {
		return false
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		return false
	}
	_ = os.Chmod(path, 0o600)
	return true
}

// guidanceFields keeps the guidance fields that have the right type and are not empty. upstream: rpiv-config
// validateGuidanceFields.
type guidanceFields struct {
	PromptSnippet    string
	HasSnippet       bool
	PromptGuidelines []string
	HasGuidelines    bool
}

func validateGuidanceFields(fields any) guidanceFields {
	g, ok := fields.(map[string]any)
	if !ok {
		return guidanceFields{}
	}
	var out guidanceFields
	if s, ok := g["promptSnippet"].(string); ok && s != "" {
		out.PromptSnippet, out.HasSnippet = s, true
	}
	if l, ok := g["promptGuidelines"].([]any); ok && len(l) > 0 {
		lines := make([]string, 0, len(l))
		valid := true
		for _, e := range l {
			s, ok := e.(string)
			if !ok || s == "" {
				valid = false
				break
			}
			lines = append(lines, s)
		}
		if valid {
			out.PromptGuidelines, out.HasGuidelines = lines, true
		}
	}
	return out
}
