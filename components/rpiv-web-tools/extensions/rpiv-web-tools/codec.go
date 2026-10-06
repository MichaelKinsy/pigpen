// SPDX-License-Identifier: MIT

package rpiv_web_tools

import (
	"encoding/json"
	"sort"
	"strconv"
)

// configFromMap projects a decoded document onto the schema. Wrong-typed known fields are simply not set: ReadConfig
// only calls it on a document that passes validate, and salvageConfig calls it on one whose offending paths are gone.
// upstream: providers/config.ts WebToolsConfig.
func configFromMap(raw map[string]any) config {
	c := config{}
	for _, key := range sortedKeys(raw) {
		value := raw[key]
		if isKnownField(key) {
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			continue
		}
		if c.extra == nil {
			c.extra = map[string]json.RawMessage{}
		}
		c.extra[key] = encoded
		c.order = append(c.order, key)
	}
	if v, ok := raw["provider"].(string); ok {
		c.Provider = v
	}
	if v, ok := raw["apiKey"].(string); ok {
		c.APIKey = v
	}
	c.APIKeys = stringRecord(raw["apiKeys"])
	c.BaseURLs = stringRecord(raw["baseUrls"])
	if v, ok := raw["guidance"].(map[string]any); ok {
		c.Guidance = guidanceFromMap(v)
	}
	if v, ok := raw["interceptors"].(map[string]any); ok {
		c.Interceptors = interceptorsFromMap(v)
	}
	return c
}

func isKnownField(key string) bool {
	for _, f := range knownFields {
		if f == key {
			return true
		}
	}
	return false
}

// stringRecord is Type.Record(Type.String(), Type.String()): a missing field is absent, a present one keeps only its
// string entries. upstream: providers/config.ts apiKeys / baseUrls.
func stringRecord(value any) map[string]string {
	rec, ok := value.(map[string]any)
	if !ok || rec == nil {
		return nil
	}
	out := make(map[string]string, len(rec))
	for k, v := range rec {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

func guidanceFromMap(raw map[string]any) *guidance {
	g := &guidance{}
	if leaf, ok := raw["web_search"].(map[string]any); ok {
		g.WebSearch = guidanceFieldsFromMap(leaf)
	}
	if leaf, ok := raw["web_fetch"].(map[string]any); ok {
		g.WebFetch = guidanceFieldsFromMap(leaf)
	}
	return g
}

func guidanceFieldsFromMap(raw map[string]any) *guidanceFields {
	g := &guidanceFields{}
	for _, key := range sortedKeys(raw) {
		value := raw[key]
		switch key {
		case "description":
			if s, ok := value.(string); ok {
				g.Description = s
			}
			continue
		case "promptSnippet":
			if s, ok := value.(string); ok {
				g.PromptSnippet = s
			}
			continue
		case "promptGuidelines":
			if list, ok := value.([]any); ok {
				g.PromptGuidelines = []string{}
				for _, e := range list {
					if s, ok := e.(string); ok {
						g.PromptGuidelines = append(g.PromptGuidelines, s)
					}
				}
			}
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			continue
		}
		if g.extra == nil {
			g.extra = map[string]json.RawMessage{}
		}
		g.extra[key] = encoded
		g.order = append(g.order, key)
	}
	return g
}

func interceptorsFromMap(raw map[string]any) *interceptorsConfig {
	ic := &interceptorsConfig{}
	value, present := raw["github"]
	if !present {
		return ic
	}
	switch g := value.(type) {
	case bool:
		ic.GitHub = &githubInterceptor{Disabled: !g}
	case map[string]any:
		opts := &githubInterceptorOptions{}
		if v, ok := g["enabled"].(bool); ok {
			opts.Enabled = &v
		}
		if v, ok := g["clonePath"].(string); ok {
			opts.ClonePath = &v
		}
		if v, ok := numberValue(g["maxRepoSizeMB"]); ok {
			opts.MaxRepoSizeMB = &v
		}
		if v, ok := numberValue(g["cloneTimeoutSeconds"]); ok {
			opts.CloneTimeoutSeconds = &v
		}
		ic.GitHub = &githubInterceptor{Object: opts}
	}
	return ic
}

func numberValue(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

// sortedKeys gives the document-order-independent, deterministic key order Go maps cannot offer. The original walks
// its own validation errors in schema order; Go needs a stable order for the same result.
func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// itoa renders a small non-negative integer for a JSON-pointer index segment.
func itoa(n int) string { return strconv.Itoa(n) }

// MarshalJSON writes the known fields in schema order and then every additionalProperty, so a load and a save round
// trip keeps the unknown keys the schema never named.
func (c config) MarshalJSON() ([]byte, error) {
	out := []byte{'{'}
	first := true
	writeField := func(key string, value any) error {
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		if !first {
			out = append(out, ',')
		}
		first = false
		keyJSON, _ := json.Marshal(key)
		out = append(out, keyJSON...)
		out = append(out, ':')
		out = append(out, encoded...)
		return nil
	}
	if c.Provider != "" {
		if err := writeField("provider", c.Provider); err != nil {
			return nil, err
		}
	}
	if c.APIKeys != nil {
		if err := writeField("apiKeys", c.APIKeys); err != nil {
			return nil, err
		}
	}
	if c.BaseURLs != nil {
		if err := writeField("baseUrls", c.BaseURLs); err != nil {
			return nil, err
		}
	}
	if c.APIKey != "" {
		if err := writeField("apiKey", c.APIKey); err != nil {
			return nil, err
		}
	}
	if c.Guidance != nil {
		if err := writeField("guidance", c.Guidance); err != nil {
			return nil, err
		}
	}
	if c.Interceptors != nil {
		if err := writeField("interceptors", c.Interceptors); err != nil {
			return nil, err
		}
	}
	for _, key := range c.order {
		if !first {
			out = append(out, ',')
		}
		first = false
		keyJSON, _ := json.Marshal(key)
		out = append(out, keyJSON...)
		out = append(out, ':')
		out = append(out, c.extra[key]...)
	}
	return append(out, '}'), nil
}

// UnmarshalJSON keeps the unknown keys that a plain struct decode would drop. upstream: the additionalProperties
// contract of WebToolsConfigSchema.
func (c *config) UnmarshalJSON(data []byte) error {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*c = configFromMap(raw)
	return nil
}

// MarshalJSON keeps a guidance leaf's additionalProperties alongside its three known fields. upstream: the shared
// GuidanceFieldsSchema leaf.
func (g guidanceFields) MarshalJSON() ([]byte, error) {
	out := []byte{'{'}
	first := true
	write := func(key string, value any) error {
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		if !first {
			out = append(out, ',')
		}
		first = false
		keyJSON, _ := json.Marshal(key)
		out = append(out, keyJSON...)
		out = append(out, ':')
		out = append(out, encoded...)
		return nil
	}
	if g.PromptSnippet != "" {
		if err := write("promptSnippet", g.PromptSnippet); err != nil {
			return nil, err
		}
	}
	if g.PromptGuidelines != nil {
		if err := write("promptGuidelines", g.PromptGuidelines); err != nil {
			return nil, err
		}
	}
	if g.Description != "" {
		if err := write("description", g.Description); err != nil {
			return nil, err
		}
	}
	for _, key := range g.order {
		if !first {
			out = append(out, ',')
		}
		first = false
		keyJSON, _ := json.Marshal(key)
		out = append(out, keyJSON...)
		out = append(out, ':')
		out = append(out, g.extra[key]...)
	}
	return append(out, '}'), nil
}

// UnmarshalJSON reads a guidance leaf without losing its additionalProperties.
func (g *guidanceFields) UnmarshalJSON(data []byte) error {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*g = *guidanceFieldsFromMap(raw)
	return nil
}

// MarshalJSON writes the two optional tool leaves, each only when present. upstream: WebToolsGuidanceSchema.
func (g guidance) MarshalJSON() ([]byte, error) {
	out := []byte{'{'}
	first := true
	if g.WebSearch != nil {
		encoded, err := json.Marshal(g.WebSearch)
		if err != nil {
			return nil, err
		}
		out = append(out, `"web_search":`...)
		out = append(out, encoded...)
		first = false
	}
	if g.WebFetch != nil {
		if !first {
			out = append(out, ',')
		}
		encoded, err := json.Marshal(g.WebFetch)
		if err != nil {
			return nil, err
		}
		out = append(out, `"web_fetch":`...)
		out = append(out, encoded...)
	}
	return append(out, '}'), nil
}

// UnmarshalJSON reads both tool leaves.
func (g *guidance) UnmarshalJSON(data []byte) error {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*g = *guidanceFromMap(raw)
	return nil
}

// MarshalJSON writes the boolean shorthand or the object override form, whichever this stanza holds. upstream:
// the github union in InterceptorsConfigSchema.
func (i githubInterceptor) MarshalJSON() ([]byte, error) {
	if i.Object == nil {
		return json.Marshal(!i.Disabled)
	}
	return json.Marshal(i.Object)
}

// UnmarshalJSON reads both arms of the github union.
func (i *githubInterceptor) UnmarshalJSON(data []byte) error {
	var asBool bool
	if err := json.Unmarshal(data, &asBool); err == nil {
		*i = githubInterceptor{Disabled: !asBool}
		return nil
	}
	var asObject githubInterceptorOptions
	if err := json.Unmarshal(data, &asObject); err != nil {
		return err
	}
	*i = githubInterceptor{Object: &asObject}
	return nil
}

// MarshalJSON writes the object override form. upstream: GitHubInterceptorOptionsSchema.
func (o githubInterceptorOptions) MarshalJSON() ([]byte, error) {
	out := []byte{'{'}
	first := true
	write := func(key string, value any) error {
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		if !first {
			out = append(out, ',')
		}
		first = false
		keyJSON, _ := json.Marshal(key)
		out = append(out, keyJSON...)
		out = append(out, ':')
		out = append(out, encoded...)
		return nil
	}
	if o.Enabled != nil {
		if err := write("enabled", *o.Enabled); err != nil {
			return nil, err
		}
	}
	if o.MaxRepoSizeMB != nil {
		if err := write("maxRepoSizeMB", *o.MaxRepoSizeMB); err != nil {
			return nil, err
		}
	}
	if o.CloneTimeoutSeconds != nil {
		if err := write("cloneTimeoutSeconds", *o.CloneTimeoutSeconds); err != nil {
			return nil, err
		}
	}
	if o.ClonePath != nil {
		if err := write("clonePath", *o.ClonePath); err != nil {
			return nil, err
		}
	}
	return append(out, '}'), nil
}

// UnmarshalJSON reads the object override form.
func (o *githubInterceptorOptions) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*o = githubInterceptorOptions{}
	if v, ok := raw["enabled"]; ok {
		var b bool
		if err := json.Unmarshal(v, &b); err == nil {
			o.Enabled = &b
		}
	}
	if v, ok := raw["clonePath"]; ok {
		var s string
		if err := json.Unmarshal(v, &s); err == nil {
			o.ClonePath = &s
		}
	}
	if v, ok := raw["maxRepoSizeMB"]; ok {
		if f, ok := numberValue(any(v)); ok {
			o.MaxRepoSizeMB = &f
		}
	}
	if v, ok := raw["cloneTimeoutSeconds"]; ok {
		if f, ok := numberValue(any(v)); ok {
			o.CloneTimeoutSeconds = &f
		}
	}
	return nil
}

// MarshalJSON writes the interceptors object with the github stanza only when present. upstream:
// InterceptorsConfigSchema.
func (ic interceptorsConfig) MarshalJSON() ([]byte, error) {
	out := []byte{'{'}
	if ic.GitHub != nil {
		encoded, err := json.Marshal(ic.GitHub)
		if err != nil {
			return nil, err
		}
		out = append(out, `"github":`...)
		out = append(out, encoded...)
	}
	return append(out, '}'), nil
}

// UnmarshalJSON reads the github stanza.
func (ic *interceptorsConfig) UnmarshalJSON(data []byte) error {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*ic = *interceptorsFromMap(raw)
	return nil
}
