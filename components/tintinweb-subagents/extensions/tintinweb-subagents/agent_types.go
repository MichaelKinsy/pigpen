package tintinweb_subagents

import (
	"strings"
	"sync"
)

// The process-wide agent registry and the spawn-type policy. upstream: src/agent-types.ts.

const noFallback = "none" // `fallbackSubagent` value that disables the fallback (strict dispatch). upstream: NO_FALLBACK

var (
	typesMu         sync.Mutex
	registry        = newRegistry()
	defaultsOff     bool
	fallbackSubType *string
)

func isDefaultsDisabled() bool   { typesMu.Lock(); defer typesMu.Unlock(); return defaultsOff }
func setDefaultsDisabled(b bool) { typesMu.Lock(); defer typesMu.Unlock(); defaultsOff = b }
func setFallbackSubagent(v *string) {
	typesMu.Lock()
	defer typesMu.Unlock()
	fallbackSubType = v
}
func getFallbackSubagent() *string { typesMu.Lock(); defer typesMu.Unlock(); return fallbackSubType }

// buildAgentRegistry is the defaults (unless disabled) overlaid with the user's agents. upstream: buildAgentRegistry.
func buildAgentRegistry(user *agentRegistry) *agentRegistry {
	r := newRegistry()
	if !isDefaultsDisabled() {
		d := defaultAgents()
		for _, k := range d.keys {
			r.set(d.m[k])
		}
	}
	for _, k := range user.keys {
		r.set(user.m[k])
	}
	return r
}

// registerAgents replaces the process-wide registry. Disabled agents stay in it but are never spawned.
func registerAgents(user *agentRegistry) {
	r := buildAgentRegistry(user)
	typesMu.Lock()
	registry = r
	typesMu.Unlock()
}

func currentRegistry() *agentRegistry { typesMu.Lock(); defer typesMu.Unlock(); return registry }

func (r *agentRegistry) isValid(t string) bool {
	k, ok := r.resolveKey(t)
	return ok && r.m[k].Enabled
}

func isValidType(t string) bool { return currentRegistry().isValid(t) }

// resolveType is the canonical key of a type, case-insensitively, or "" (it ignores `enabled`).
func resolveType(name string) string { k, _ := currentRegistry().resolveKey(name); return k }

func getAgentConfig(name string) *agentConfig { return currentRegistry().get(name) }

func getAvailableTypes() []string { return currentRegistry().availableTypes() }

func getDefaultAgentNames() []string { return currentRegistry().names(true) }
func getUserAgentNames() []string    { return currentRegistry().names(false) }

func (r *agentRegistry) names(defaults bool) []string {
	var out []string
	for _, k := range r.keys {
		if r.m[k].IsDefault == defaults {
			out = append(out, k)
		}
	}
	return out
}

// getMemoryToolNames are the memory tools (read, write, edit) not already present. upstream: getMemoryToolNames.
func getMemoryToolNames(existing map[string]bool) []string {
	return without([]string{"read", "write", "edit"}, existing)
}
func getReadOnlyMemoryToolNames(existing map[string]bool) []string {
	return without([]string{"read"}, existing)
}

func without(names []string, existing map[string]bool) []string {
	out := []string{}
	for _, n := range names {
		if !existing[n] {
			out = append(out, n)
		}
	}
	return out
}

// getToolNamesForType: the built-in tools of a type; a definition that omits the field gets all of them, an
// explicit empty list gets none.
func getToolNamesForType(t string) []string {
	r := currentRegistry()
	if k, ok := r.resolveKey(t); ok && r.m[k].Enabled && r.m[k].BuiltinToolNames != nil {
		return r.m[k].BuiltinToolNames
	}
	return append([]string{}, builtinToolNames...)
}

// typeConfig is the view getConfig returns.
type typeConfig struct {
	DisplayName, Color, Description string
	BuiltinToolNames                []string
	Extensions, Skills              any
	ExcludeExtensions               []string
	PromptMode                      string
}

func viewOf(c *agentConfig) typeConfig {
	d := c.DisplayName
	if d == "" {
		d = c.Name
	}
	tools := c.BuiltinToolNames
	if tools == nil {
		tools = builtinToolNames
	}
	return typeConfig{DisplayName: d, Color: c.Color, Description: c.Description, BuiltinToolNames: tools,
		Extensions: c.Extensions, Skills: c.Skills, ExcludeExtensions: c.ExcludeExtensions, PromptMode: c.PromptMode}
}

// getConfig is the config of a type, case-insensitively; an unknown or disabled one gets general-purpose's.
func getConfig(t string) typeConfig {
	r := currentRegistry()
	if k, ok := r.resolveKey(t); ok && r.m[k].Enabled {
		return viewOf(r.m[k])
	}
	if gp := r.m["general-purpose"]; gp != nil && gp.Enabled {
		return viewOf(gp)
	}
	return typeConfig{DisplayName: "Agent", Description: "General-purpose agent for complex, multi-step tasks",
		BuiltinToolNames: builtinToolNames, Extensions: true, Skills: true, PromptMode: "append"}
}

// resolveEnabledTypeIn is the canonical key of the one enabled agent a caller-supplied name identifies, or "".
func resolveEnabledTypeIn(r *agentRegistry, requested any) string {
	raw, _ := requested.(string)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if k, ok := r.unambiguous(raw); ok && r.m[k].Enabled {
		return k
	}
	return ""
}

// spawnResolution is the outcome of resolving a caller-supplied type.
type spawnResolution struct {
	OK           bool
	Type         string
	FellBackFrom string
	Message      string
}

// resolveSpawnTypeIn applies the `fallbackSubagent` policy; the one decision point for every caller-supplied
// spawn. upstream: resolveSpawnTypeIn.
func resolveSpawnTypeIn(r *agentRegistry, requested any) spawnResolution {
	raw, _ := requested.(string)
	raw = strings.TrimSpace(raw)
	available := func() string {
		if a := r.availableTypes(); len(a) > 0 {
			return strings.Join(a, ", ")
		}
		return "(none)"
	}
	if k := resolveEnabledTypeIn(r, raw); k != "" {
		return spawnResolution{OK: true, Type: k}
	}
	reason := "No agent type given."
	if raw != "" {
		reason = `Unknown or disabled agent type: "` + raw + `".`
	}
	if cfg := getFallbackSubagent(); cfg != nil {
		configured := strings.TrimSpace(*cfg)
		if strings.ToLower(configured) == noFallback {
			return spawnResolution{Message: reason + " Available: " + available() + "."}
		}
		k, ok := r.unambiguous(configured)
		if !ok || !r.m[k].Enabled {
			return spawnResolution{Message: reason + ` The configured fallbackSubagent "` + configured + `" is itself unknown or disabled. Available: ` + available() + "."}
		}
		return spawnResolution{OK: true, Type: k, FellBackFrom: raw}
	}
	return spawnResolution{OK: true, Type: "general-purpose", FellBackFrom: raw}
}

func resolveSpawnType(requested any) spawnResolution {
	return resolveSpawnTypeIn(currentRegistry(), requested)
}
