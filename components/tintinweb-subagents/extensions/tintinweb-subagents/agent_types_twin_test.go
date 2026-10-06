package tintinweb_subagents

import (
	"reflect"
	"slices"
	"testing"
)

func mk(name string, mod ...func(*agentConfig)) *agentConfig {
	c := &agentConfig{Name: name, Description: "Test agent", BuiltinToolNames: []string{"read", "grep"}, Extensions: false, Skills: false,
		SystemPrompt: "You are a test agent.", PromptMode: "replace", Enabled: true}
	f := false
	c.InheritContext, c.RunInBackground, c.Isolated = &f, &f, &f
	for _, m := range mod {
		m(c)
	}
	return c
}

func reg(cs ...*agentConfig) *agentRegistry {
	r := newRegistry()
	for _, c := range cs {
		r.set(c)
	}
	return r
}

func eq(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func resetTypes() {
	setFallbackSubagent(nil)
	setDefaultsDisabled(false)
	registerAgents(newRegistry())
}

func ptr(s string) *string { return &s }

func TestAgentTypes(t *testing.T) {
	const f = "agent-types"
	resetTypes()
	t.Cleanup(resetTypes)
	t.Run("default agents", func(t *testing.T) {
		resetTypes()
		tw(t, f, "recognizes all default agent types", func(t *testing.T) {
			for _, n := range []string{"general-purpose", "Explore", "Plan"} {
				eq(t, isValidType(n), true)
			}
		})
		tw(t, f, "does not include removed agents", func(t *testing.T) {
			eq(t, isValidType("statusline-setup"), false)
			eq(t, isValidType("claude-code-guide"), false)
		})
		tw(t, f, "rejects unknown types", func(t *testing.T) {
			eq(t, isValidType("nonexistent"), false)
			eq(t, isValidType(""), false)
		})
		tw(t, f, "case-insensitive lookup works for isValidType", func(t *testing.T) {
			for _, n := range []string{"explore", "EXPLORE", "General-Purpose", "plan"} {
				eq(t, isValidType(n), true)
			}
		})
		tw(t, f, "case-insensitive lookup works for getAgentConfig", func(t *testing.T) {
			c := getAgentConfig("explore")
			eq(t, c.Name, "Explore")
			eq(t, c.Model, "anthropic/claude-haiku-4-5")
		})
		tw(t, f, "resolveType returns canonical key or undefined", func(t *testing.T) {
			eq(t, resolveType("Explore"), "Explore")
			eq(t, resolveType("explore"), "Explore")
			eq(t, resolveType("GENERAL-PURPOSE"), "general-purpose")
			eq(t, resolveType("nonexistent"), "")
		})
		tw(t, f, "returns correct config for default types", func(t *testing.T) {
			c := getConfig("general-purpose")
			eq(t, c.DisplayName, "Agent")
			eq(t, c.BuiltinToolNames, builtinToolNames)
			eq(t, c.Extensions, true)
			eq(t, c.Skills, true)
		})
		tw(t, f, "Explore has read-only tools", func(t *testing.T) {
			c := getConfig("Explore")
			eq(t, c.BuiltinToolNames, []string{"read", "bash", "grep", "find", "ls"})
			eq(t, slices.Contains(c.BuiltinToolNames, "edit"), false)
			eq(t, slices.Contains(c.BuiltinToolNames, "write"), false)
		})
		tw(t, f, "Explore has haiku model in config", func(t *testing.T) {
			eq(t, getAgentConfig("Explore").Model, "anthropic/claude-haiku-4-5")
		})
		tw(t, f, "default agents are marked isDefault", func(t *testing.T) {
			eq(t, getAgentConfig("general-purpose").IsDefault, true)
		})
		tw(t, f, "default agents do not lock strategy fields (run_in_background / inherit_context / isolated)", func(t *testing.T) {
			for _, n := range []string{"general-purpose", "Explore", "Plan"} {
				c := getAgentConfig(n)
				if c.RunInBackground != nil || c.InheritContext != nil || c.Isolated != nil {
					t.Fatalf("%s locks a strategy field", n)
				}
			}
		})
		tw(t, f, "getDefaultAgentNames returns default agent names", func(t *testing.T) {
			names := getDefaultAgentNames()
			for _, n := range []string{"general-purpose", "Explore", "Plan"} {
				eq(t, slices.Contains(names, n), true)
			}
		})
		tw(t, f, "BUILTIN_TOOL_NAMES includes all built-in tools", func(t *testing.T) {
			for _, n := range []string{"read", "bash", "edit", "write", "grep", "find", "ls"} {
				eq(t, slices.Contains(builtinToolNames, n), true)
			}
			eq(t, len(builtinToolNames) >= 7, true)
		})
	})
	t.Run("disable defaults", func(t *testing.T) {
		resetTypes()
		t.Cleanup(resetTypes)
		tw(t, f, "defaults to enabled", func(t *testing.T) { eq(t, isDefaultsDisabled(), false) })
		tw(t, f, "registerAgents skips DEFAULT_AGENTS when disabled", func(t *testing.T) {
			setDefaultsDisabled(true)
			registerAgents(newRegistry())
			eq(t, getAvailableTypes(), []string(nil))
			for _, n := range []string{"general-purpose", "Explore", "Plan"} {
				eq(t, isValidType(n), false)
			}
		})
		tw(t, f, "user agents are unaffected when defaults are disabled", func(t *testing.T) {
			setDefaultsDisabled(true)
			registerAgents(reg(mk("auditor")))
			eq(t, getAvailableTypes(), []string{"auditor"})
			eq(t, isValidType("auditor"), true)
			eq(t, getDefaultAgentNames(), []string(nil))
		})
		tw(t, f, "re-enabling restores defaults on next registerAgents", func(t *testing.T) {
			setDefaultsDisabled(true)
			registerAgents(newRegistry())
			eq(t, isValidType("general-purpose"), false)
			setDefaultsDisabled(false)
			registerAgents(newRegistry())
			for _, n := range []string{"general-purpose", "Explore", "Plan"} {
				eq(t, isValidType(n), true)
			}
		})
		tw(t, f, "getConfig falls back to the hardcoded config when defaults are disabled and no user agents exist", func(t *testing.T) {
			setDefaultsDisabled(true)
			registerAgents(newRegistry())
			c := getConfig("general-purpose")
			eq(t, c.DisplayName, "Agent")
			eq(t, c.BuiltinToolNames, builtinToolNames)
			eq(t, c.PromptMode, "append")
		})
	})
	t.Run("user agents", func(t *testing.T) {
		resetTypes()
		t.Cleanup(resetTypes)
		tw(t, f, "registers and retrieves user agents", func(t *testing.T) {
			registerAgents(reg(mk("auditor", func(c *agentConfig) { c.Description = "Auditor" })))
			eq(t, isValidType("auditor"), true)
			eq(t, getAgentConfig("auditor").Description, "Auditor")
		})
		tw(t, f, "includes user agents in available types", func(t *testing.T) {
			registerAgents(reg(mk("auditor")))
			types := getAvailableTypes()
			for _, n := range []string{"general-purpose", "Explore", "auditor"} {
				eq(t, slices.Contains(types, n), true)
			}
		})
		tw(t, f, "lists user agent names separately", func(t *testing.T) {
			registerAgents(reg(mk("auditor"), mk("reviewer")))
			names := getUserAgentNames()
			eq(t, names, []string{"auditor", "reviewer"})
			eq(t, slices.Contains(names, "general-purpose"), false)
		})
		tw(t, f, "getConfig returns config for user agents", func(t *testing.T) {
			registerAgents(reg(mk("auditor", func(c *agentConfig) { c.Description = "Security auditor"; c.Skills = true })))
			c := getConfig("auditor")
			eq(t, c.DisplayName, "auditor")
			eq(t, c.Description, "Security auditor")
			eq(t, c.BuiltinToolNames, []string{"read", "grep"})
			eq(t, c.Extensions, false)
			eq(t, c.Skills, true)
		})
		tw(t, f, "getConfig returns extension allowlist for user agents", func(t *testing.T) {
			registerAgents(reg(mk("partial", func(c *agentConfig) { c.Extensions = []string{"web-search"}; c.Skills = []string{"planning"} })))
			c := getConfig("partial")
			eq(t, c.Extensions, []string{"web-search"})
			eq(t, c.Skills, []string{"planning"})
		})
		tw(t, f, "getToolNamesForType works for user agents", func(t *testing.T) {
			registerAgents(reg(mk("auditor", func(c *agentConfig) { c.BuiltinToolNames = []string{"read", "grep", "find"} })))
			eq(t, getToolNamesForType("auditor"), []string{"read", "grep", "find"})
		})
		tw(t, f, "getToolNamesForType honors an explicit empty builtinToolNames as zero built-ins", func(t *testing.T) {
			registerAgents(reg(mk("ext-only", func(c *agentConfig) { c.BuiltinToolNames = []string{} })))
			eq(t, getToolNamesForType("ext-only"), []string{})
		})
		tw(t, f, "getConfig falls back to general-purpose for unknown types", func(t *testing.T) {
			c := getConfig("nonexistent")
			eq(t, c.DisplayName, "Agent")
			eq(t, c.Description, defaultAgents().m["general-purpose"].Description)
		})
		tw(t, f, "clearing user agents works (defaults remain)", func(t *testing.T) {
			registerAgents(reg(mk("auditor")))
			eq(t, isValidType("auditor"), true)
			registerAgents(newRegistry())
			eq(t, isValidType("auditor"), false)
			eq(t, isValidType("general-purpose"), true)
		})
		tw(t, f, "user agent overrides default with same name", func(t *testing.T) {
			registerAgents(reg(mk("Explore", func(c *agentConfig) { c.Description = "Custom Explore"; c.BuiltinToolNames = builtinToolNames })))
			c := getConfig("Explore")
			eq(t, c.Description, "Custom Explore")
			eq(t, c.BuiltinToolNames, builtinToolNames)
		})
		tw(t, f, "disabled agent is excluded from available types", func(t *testing.T) {
			registerAgents(reg(mk("Plan", func(c *agentConfig) { c.Enabled = false })))
			eq(t, isValidType("Plan"), false)
			eq(t, slices.Contains(getAvailableTypes(), "Plan"), false)
		})
		tw(t, f, "general-purpose can be disabled but fallback still works", func(t *testing.T) {
			registerAgents(reg(mk("general-purpose", func(c *agentConfig) { c.Enabled = false })))
			eq(t, isValidType("general-purpose"), false)
			eq(t, getConfig("general-purpose").DisplayName, "Agent")
		})
	})
	t.Run("getMemoryToolNames", func(t *testing.T) {
		tw(t, f, "returns read, write, edit when none exist", func(t *testing.T) {
			eq(t, getMemoryToolNames(map[string]bool{}), []string{"read", "write", "edit"})
		})
		tw(t, f, "skips tools that already exist", func(t *testing.T) {
			eq(t, getMemoryToolNames(map[string]bool{"read": true, "edit": true}), []string{"write"})
		})
		tw(t, f, "returns empty when all memory tools already exist", func(t *testing.T) {
			eq(t, len(getMemoryToolNames(map[string]bool{"read": true, "write": true, "edit": true})), 0)
		})
	})
	t.Run("getReadOnlyMemoryToolNames", func(t *testing.T) {
		tw(t, f, "returns only read when missing", func(t *testing.T) {
			eq(t, getReadOnlyMemoryToolNames(map[string]bool{}), []string{"read"})
		})
		tw(t, f, "returns empty when read already exists", func(t *testing.T) {
			eq(t, len(getReadOnlyMemoryToolNames(map[string]bool{"read": true})), 0)
		})
	})
	t.Run("BUILTIN_TOOL_NAMES", func(t *testing.T) {
		tw(t, f, "contains at least the 7 known built-ins", func(t *testing.T) {
			for _, n := range []string{"read", "bash", "edit", "write", "grep", "find", "ls"} {
				eq(t, slices.Contains(builtinToolNames, n), true)
			}
		})
		tw(t, f, "has no duplicate entries", func(t *testing.T) {
			seen := map[string]bool{}
			for _, n := range builtinToolNames {
				eq(t, seen[n], false)
				seen[n] = true
			}
		})
	})
	t.Run("resolveSpawnType — fail-closed dispatch (#183)", func(t *testing.T) {
		roster := func() *agentRegistry {
			return reg(mk("scout"), mk("retired", func(c *agentConfig) { c.Enabled = false }), mk("router"))
		}
		gp := func(from string) spawnResolution {
			return spawnResolution{OK: true, Type: "general-purpose", FellBackFrom: from}
		}
		tw(t, f, "resolves an enabled type case-insensitively", func(t *testing.T) {
			resetTypes()
			registerAgents(roster())
			eq(t, resolveSpawnType("SCOUT"), spawnResolution{OK: true, Type: "scout"})
		})
		tw(t, f, "falls back to general-purpose when unset, reporting what was asked for", func(t *testing.T) {
			resetTypes()
			registerAgents(roster())
			eq(t, resolveSpawnType("typoo"), gp("typoo"))
		})
		tw(t, f, "rejects unknown types under `none` and names what is available", func(t *testing.T) {
			resetTypes()
			registerAgents(roster())
			setFallbackSubagent(ptr(noFallback))
			r := resolveSpawnType("typoo")
			eq(t, r.OK, false)
			eq(t, contains(r.Message, `Unknown or disabled agent type: "typoo"`), true)
			eq(t, contains(r.Message, "scout"), true)
			eq(t, contains(r.Message, "retired"), false)
		})
		tw(t, f, "treats a disabled type as unresolvable, not as a valid name", func(t *testing.T) {
			resetTypes()
			registerAgents(roster())
			eq(t, resolveSpawnType("retired"), gp("retired"))
			setFallbackSubagent(ptr(noFallback))
			eq(t, resolveSpawnType("retired").OK, false)
		})
		tw(t, f, "refuses to guess between two types differing only by case", func(t *testing.T) {
			resetTypes()
			registerAgents(reg(mk("Scout"), mk("scout")))
			eq(t, resolveSpawnType("scout"), spawnResolution{OK: true, Type: "scout"})
			eq(t, resolveSpawnType("SCOUT").OK, true)
			eq(t, resolveSpawnType("SCOUT"), gp("SCOUT"))
		})
		tw(t, f, "routes unresolvable types to a named fallback agent", func(t *testing.T) {
			resetTypes()
			registerAgents(roster())
			setFallbackSubagent(ptr("router"))
			eq(t, resolveSpawnType("typoo"), spawnResolution{OK: true, Type: "router", FellBackFrom: "typoo"})
		})
		tw(t, f, "fails loudly when the configured fallback is itself unusable", func(t *testing.T) {
			resetTypes()
			registerAgents(roster())
			setFallbackSubagent(ptr("retired"))
			r := resolveSpawnType("typoo")
			eq(t, r.OK, false)
			eq(t, contains(r.Message, "fallbackSubagent"), true)
		})
		tw(t, f, "treats a missing type like any other unresolvable one", func(t *testing.T) {
			resetTypes()
			registerAgents(roster())
			for _, empty := range []any{"", "   ", nil} {
				r := resolveSpawnType(empty)
				eq(t, r.OK, true)
				eq(t, r.Type, "general-purpose")
			}
			setFallbackSubagent(ptr(noFallback))
			for _, empty := range []any{"", "   ", nil} {
				r := resolveSpawnType(empty)
				eq(t, r.OK, false)
				eq(t, contains(r.Message, "No agent type given"), true)
			}
		})
		tw(t, f, "resolves strictly regardless of the setting, for nested delegation", func(t *testing.T) {
			resetTypes()
			r := roster()
			setFallbackSubagent(ptr("router"))
			eq(t, resolveEnabledTypeIn(r, "typoo"), "")
			eq(t, resolveEnabledTypeIn(r, "retired"), "")
			eq(t, resolveEnabledTypeIn(r, " SCOUT "), "scout")
			eq(t, resolveSpawnTypeIn(r, "typoo"), spawnResolution{OK: true, Type: "router", FellBackFrom: "typoo"})
		})
	})
}
