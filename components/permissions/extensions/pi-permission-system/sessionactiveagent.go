package pi_permission_system

import (
	"regexp"
)

// Port of src/session/active-agent.ts.

// ActiveAgentContext is the narrow context GetActiveAgentName reads: the session entries. An error is the original's throw.
type ActiveAgentContext interface {
	GetEntries() ([]SessionEntryView, error)
}

// ActiveAgentTagRegex matches the `<active_agent name="...">` tag pi-agent-router injects into the system prompt (ACTIVE_AGENT_TAG_REGEX).
// JavaScript's \s is spelled out: RE2's is ASCII only.
var ActiveAgentTagRegex = regexp.MustCompile(`(?i)<active_agent[\t\n\v\f\r \x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]+name=["']([^"']+)["'][^>]*>`)

// NormalizeAgentName is normalizeAgentName: the trimmed name, ok false for a non-string or an empty or blank one.
func NormalizeAgentName(value any) (string, bool) {
	s, ok := value.(string)
	if !ok {
		return "", false
	}
	trimmed := jsTrim(s)
	if trimmed == "" {
		return "", false
	}
	return trimmed, true
}

// entryField reads a key of a decoded JSON object (a map or the ordered object).
func entryField(v any, key string) (any, bool) {
	switch o := v.(type) {
	case map[string]any:
		x, ok := o[key]
		return x, ok
	case *jsObject:
		if o == nil {
			return nil, false
		}
		x, ok := o.vals[key]
		if _, u := x.(undef); u {
			return nil, false
		}
		return x, ok
	}
	return nil, false
}

// GetActiveAgentName is getActiveAgentName: the name of the last `active_agent` custom entry with a usable name; an entry whose
// name is null clears it (nil), a blank or non-string name is skipped. nil is the original's null. An error is the session
// manager's throw: callers decide what an unknown agent means (the original lets it propagate).
func GetActiveAgentName(ctx ActiveAgentContext) (*string, error) {
	entries, err := ctx.GetEntries()
	if err != nil {
		return nil, err
	}
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		if t, _ := entry["type"].(string); t != "custom" {
			continue
		}
		if ct, _ := entry["customType"].(string); ct != "active_agent" {
			continue
		}
		name, present := entryField(entry["data"], "name")
		if n, ok := NormalizeAgentName(name); ok {
			return &n, nil
		}
		if present && name == nil {
			return nil, nil
		}
	}
	return nil, nil
}

// GetActiveAgentNameFromSystemPrompt is getActiveAgentNameFromSystemPrompt; nil is null.
func GetActiveAgentNameFromSystemPrompt(systemPrompt string) *string {
	if systemPrompt == "" {
		return nil
	}
	m := ActiveAgentTagRegex.FindStringSubmatch(systemPrompt)
	if m == nil || m[1] == "" {
		return nil
	}
	if n, ok := NormalizeAgentName(m[1]); ok {
		return &n
	}
	return nil
}
