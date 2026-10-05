package websearch

import (
	"fmt"
	"math"
	"regexp"
	"strings"
)

// The registration-time configuration of the tools (index.ts: resolveToolNames, isToolEnabled,
// resolveFetchModeConfig, toolActivation, maxInlineContentChars).

// ToolNames are the public names of the four tools.
type ToolNames struct{ WebSearch, SourceCheck, FetchContent, GetSearchContent string }

// DefaultToolNames are the names pi-web-access registers.
var DefaultToolNames = ToolNames{"web_search", "source_check", "fetch_content", "get_search_content"}

var toolNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)

// FetchModes are the modes fetch_content can offer.
var FetchModes = []string{"readable", "raw", "answer"}

var fetchModeDescriptions = map[string]string{
	"readable": "extract readable content as markdown",
	"raw":      "return the exact textual body using direct HTTP only",
	"answer":   "answer a prompt using only fetched content",
}

const (
	defaultMaxInlineContentChars = 30_000
	minInlineContentChars        = 1_000
	maxInlineContentChars        = 200_000
)

// ToolConfig is the configuration read once when the extension registers.
type ToolConfig struct {
	Names            ToolNames
	WebSearch        bool
	SourceCheck      bool
	FetchContent     bool
	GetSearchContent bool
	DefaultMode      string
	AllowedModes     []string
	ToolActivation   string
	AllowedProviders []string
	Commands         map[string]bool
	// Warning is set when web-search.json could not be read at registration; the tools then
	// register with defaults, as the original does.
	Warning string
	Root    map[string]any
}

func subObject(m map[string]any, key string) map[string]any {
	o, _ := m[key].(map[string]any)
	return o
}

func isToolEnabledIn(root map[string]any, key string) bool {
	if o := subObject(subObject(root, "tools"), key); o != nil {
		if e, ok := o["enabled"].(bool); ok {
			return e
		}
	}
	if key != "webSearch" && key != "sourceCheck" {
		return true
	}
	if e, ok := subObject(root, "webSearch")["enabled"].(bool); ok && !e {
		return false
	}
	return true
}

// LoadToolConfig reads and validates the registration configuration. An unreadable config is
// not an error (defaults plus a Warning); an invalid one is.
func LoadToolConfig() (*ToolConfig, error) {
	root, err := ReadConfigRoot()
	cfg := &ToolConfig{}
	if err != nil {
		cfg.Warning = err.Error()
		root = nil
	}
	if root == nil {
		root = map[string]any{}
	}
	cfg.Root = root
	path := ConfigPath()

	// fetch modes
	fetch := subObject(root, "fetch")
	modes := any(nil)
	if fetch != nil {
		modes = fetch["allowedModes"]
	}
	cfg.AllowedModes = FetchModes
	if modes != nil {
		list, ok := modes.([]any)
		if !ok || len(list) == 0 {
			return nil, fmt.Errorf(`fetch.allowedModes in %s must be a non-empty array containing only "readable", "raw", or "answer"`, path)
		}
		cfg.AllowedModes = nil
		for _, m := range list {
			s, _ := m.(string)
			if !sliceHas(FetchModes, s) {
				return nil, fmt.Errorf(`fetch.allowedModes in %s must be a non-empty array containing only "readable", "raw", or "answer"`, path)
			}
			cfg.AllowedModes = append(cfg.AllowedModes, s)
		}
		for i, m := range cfg.AllowedModes {
			for _, prev := range cfg.AllowedModes[:i] {
				if prev == m {
					return nil, fmt.Errorf(`fetch.allowedModes in %s must not contain duplicates: "%s"`, path, m)
				}
			}
		}
	}
	cfg.DefaultMode = "readable"
	if fetch != nil {
		if d, present := fetch["defaultMode"]; present && d != nil {
			cfg.DefaultMode, _ = d.(string)
		}
	}
	if !sliceHas(cfg.AllowedModes, cfg.DefaultMode) {
		return nil, fmt.Errorf("fetch.defaultMode in %s must be one of fetch.allowedModes", path)
	}

	cfg.ToolActivation = "dynamic"
	if v, present := root["toolActivation"]; present && v != nil {
		s, _ := v.(string)
		if s != "dynamic" && s != "eager" {
			return nil, fmt.Errorf(`toolActivation in %s must be "dynamic" or "eager"`, path)
		}
		cfg.ToolActivation = s
	}
	if _, present := root["webSearch"]; present {
		if _, has := subObject(root, "webSearch")["allowedProviders"]; has {
			allowed, err := AllowedSearchProviders()
			if err != nil {
				return nil, err
			}
			cfg.AllowedProviders = allowed
		}
	}
	if cfg.AllowedProviders == nil {
		cfg.AllowedProviders = ResolvedProviders
	}

	cfg.WebSearch = isToolEnabledIn(root, "webSearch")
	cfg.SourceCheck = isToolEnabledIn(root, "sourceCheck")
	cfg.FetchContent = isToolEnabledIn(root, "fetchContent")
	cfg.GetSearchContent = isToolEnabledIn(root, "getSearchContent")

	names, err := resolveToolNames(root, cfg, path)
	if err != nil {
		return nil, err
	}
	cfg.Names = names

	cfg.Commands = map[string]bool{}
	for _, c := range []string{"websearch", "curator", "search", "google-account"} {
		enabled := true
		if e, ok := subObject(subObject(root, "commands"), c)["enabled"].(bool); ok {
			enabled = e
		}
		cfg.Commands[c] = enabled
	}
	return cfg, nil
}

func resolveToolNames(root map[string]any, cfg *ToolConfig, path string) (ToolNames, error) {
	names := DefaultToolNames
	if raw, present := root["toolNames"]; present {
		obj, ok := raw.(map[string]any)
		if !ok {
			return names, fmt.Errorf("toolNames in %s must be an object", path)
		}
		slots := []struct {
			key string
			dst *string
		}{{"webSearch", &names.WebSearch}, {"sourceCheck", &names.SourceCheck}, {"fetchContent", &names.FetchContent}, {"getSearchContent", &names.GetSearchContent}}
		for _, s := range slots {
			v, present := obj[s.key]
			if !present {
				continue
			}
			str, ok := v.(string)
			if !ok {
				return names, fmt.Errorf("toolNames.%s in %s must be a string", s.key, path)
			}
			trimmed := jsTrim(str)
			if !toolNamePattern.MatchString(trimmed) {
				return names, fmt.Errorf("toolNames.%s in %s must start with a letter and contain only letters, numbers, underscores, or hyphens", s.key, path)
			}
			*s.dst = trimmed
		}
	}
	type reg struct {
		key     string
		enabled bool
		name    string
	}
	seen := map[string]string{}
	for _, r := range []reg{{"webSearch", cfg.WebSearch, names.WebSearch}, {"sourceCheck", cfg.SourceCheck, names.SourceCheck}, {"fetchContent", cfg.FetchContent, names.FetchContent}, {"getSearchContent", cfg.GetSearchContent, names.GetSearchContent}} {
		if !r.enabled {
			continue
		}
		if r.name == "web_enable" {
			return names, fmt.Errorf("toolNames.%s in %s uses reserved loader name web_enable", r.key, path)
		}
		if prev, dup := seen[r.name]; dup {
			return names, fmt.Errorf("toolNames.%s duplicates toolNames.%s in %s", r.key, prev, path)
		}
		seen[r.name] = r.key
	}
	return names, nil
}

// MaxInlineContentChars is maxInlineContentChars: an integer >= 1000, capped at 200000.
func MaxInlineContentChars(root map[string]any) int {
	f, ok := root["maxInlineContentChars"].(float64)
	if !ok || math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) || f < minInlineContentChars {
		return defaultMaxInlineContentChars
	}
	return int(math.Min(f, maxInlineContentChars))
}

func joinToolNames(names []string) string {
	switch len(names) {
	case 0:
		return "stored content"
	case 1:
		return names[0]
	case 2:
		return names[0] + " or " + names[1]
	}
	return strings.Join(names[:len(names)-1], ", ") + ", or " + names[len(names)-1]
}
