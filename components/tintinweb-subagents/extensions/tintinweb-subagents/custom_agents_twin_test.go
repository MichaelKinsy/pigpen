package tintinweb_subagents

import (
	"os"
	"path/filepath"
	"testing"
)

// The twins of test/custom-agents.test.ts (describe "loadCustomAgents").

type caRig struct {
	t   *testing.T
	tmp string
}

func newCARig(t *testing.T) *caRig {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	for _, k := range []string{"PIG_CODING_AGENT_DIR", "PIG_HOME", "XDG_CONFIG_HOME", "PIG_USE_PI_DIRS", "PI_CODING_AGENT_DIR"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	return &caRig{t: t, tmp: tmp}
}

func (r *caRig) writeIn(dir, name, content string) {
	r.t.Helper()
	d := filepath.Join(r.tmp, dir, "agents")
	if err := os.MkdirAll(d, 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, name+".md"), []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}
func (r *caRig) writeAgent(name, content string) { r.t.Helper(); r.writeIn(".pi", name, content) }
func (r *caRig) writeWorkspace(name, content string) {
	r.t.Helper()
	r.writeIn(".agents", name, content)
}
func (r *caRig) load() *agentRegistry {
	reg, _ := loadCustomAgents(r.tmp)
	return reg
}
func (r *caRig) agent(name string) *agentConfig {
	r.t.Helper()
	c := r.load().m[name]
	if c == nil {
		r.t.Fatalf("agent %q not loaded", name)
	}
	return c
}

func sp(v string) *string { return &v }

func TestLoadCustomAgents(t *testing.T) {
	const f = "custom-agents"
	tw(t, f, "returns empty map when custom agent dirs do not exist", func(t *testing.T) {
		eq(t, len(newCARig(t).load().keys), 0)
	})
	tw(t, f, "loads a workspace project agent from .agents/agents", func(t *testing.T) {
		r := newCARig(t)
		r.writeWorkspace("reviewer", "---\ndescription: Workspace Reviewer\n---\nWorkspace prompt.")
		eq(t, len(r.load().keys), 1)
		a := r.agent("reviewer")
		eq(t, a.Description, "Workspace Reviewer")
		eq(t, a.SystemPrompt, "Workspace prompt.")
		eq(t, a.Source, "project")
	})
	tw(t, f, ".pi/agents overrides .agents/agents on a name clash", func(t *testing.T) {
		r := newCARig(t)
		r.writeWorkspace("dupe", "---\ndescription: Workspace Project\n---\nWorkspace prompt.")
		r.writeAgent("dupe", "---\ndescription: Pi Project\n---\nPi prompt.")
		eq(t, len(r.load().keys), 1)
		eq(t, r.agent("dupe").Description, "Pi Project")
		eq(t, r.agent("dupe").SystemPrompt, "Pi prompt.")
	})
	tw(t, f, "workspace project agents override global agents", func(t *testing.T) {
		r := newCARig(t)
		global := filepath.Join(r.tmp, "global-agent-dir")
		t.Setenv("PIG_CODING_AGENT_DIR", global)
		os.MkdirAll(filepath.Join(global, "agents"), 0o755)
		os.WriteFile(filepath.Join(global, "agents", "dupe.md"), []byte("---\ndescription: Global\n---\nGlobal prompt."), 0o644)
		r.writeWorkspace("dupe", "---\ndescription: Workspace Project\n---\nWorkspace prompt.")
		eq(t, len(r.load().keys), 1)
		eq(t, r.agent("dupe").Description, "Workspace Project")
		eq(t, r.agent("dupe").SystemPrompt, "Workspace prompt.")
	})
	tw(t, f, "loads a basic agent with all frontmatter fields", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("auditor", `---
description: Security Auditor
tools: read, grep, find
model: anthropic/claude-opus-4-6
thinking: high
max_turns: 30
persist_session: true
output_transcript: false
session_dir: .seams/pi-sessions/seam-plan-reviewer
allowed_subagents: scout, reviewer
prompt_mode: replace
inherit_context: true
run_in_background: true
isolated: true
---
You are a security auditor.`)
		eq(t, len(r.load().keys), 1)
		a := r.agent("auditor")
		eq(t, a.Name, "auditor")
		eq(t, a.Description, "Security Auditor")
		eq(t, a.BuiltinToolNames, []string{"read", "grep", "find"})
		eq(t, a.Model, "anthropic/claude-opus-4-6")
		eq(t, a.Thinking, "high")
		eq(t, *a.MaxTurns, 30)
		eq(t, *a.PersistSession, true)
		eq(t, *a.OutputTranscript, false)
		eq(t, a.SessionDir, ".seams/pi-sessions/seam-plan-reviewer")
		eq(t, a.AllowedSubagents, []string{"scout", "reviewer"})
		eq(t, a.PromptMode, "replace")
		eq(t, *a.InheritContext, true)
		eq(t, *a.RunInBackground, true)
		eq(t, *a.Isolated, true)
		eq(t, a.SystemPrompt, "You are a security auditor.")
	})
	tw(t, f, "uses sensible defaults when frontmatter is empty", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("minimal", "---\n---\nJust a prompt.")
		a := r.agent("minimal")
		eq(t, a.Name, "minimal")
		eq(t, a.DisplayName, "")
		eq(t, a.Color, "")
		eq(t, a.Description, "minimal")
		eq(t, a.BuiltinToolNames, builtinToolNames)
		eq(t, a.Extensions, true)
		eq(t, a.Skills, true)
		eq(t, a.Model, "")
		eq(t, a.Thinking, "")
		if a.MaxTurns != nil || a.PersistSession != nil || a.OutputTranscript != nil || a.InheritContext != nil || a.RunInBackground != nil || a.Isolated != nil {
			t.Fatal("an omitted field was set")
		}
		eq(t, a.SessionDir, "")
		eq(t, a.AllowedSubagents, nil)
		eq(t, a.PromptMode, "replace")
		eq(t, a.SystemPrompt, "Just a prompt.")
	})
	tw(t, f, "uses sensible defaults when no frontmatter at all", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("bare", "Just a system prompt, no frontmatter.")
		a := r.agent("bare")
		eq(t, a.Name, "bare")
		eq(t, a.Description, "bare")
		eq(t, a.BuiltinToolNames, builtinToolNames)
		eq(t, a.SystemPrompt, "Just a system prompt, no frontmatter.")
	})
	tw(t, f, "parses allowed_subagents: off by default, `all` wildcard, csv restriction", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("omitted", "---\n---\nOff.")
		r.writeAgent("unrestricted", "---\nallowed_subagents: all\n---\nUnrestricted.")
		r.writeAgent("wildcard", "---\nallowed_subagents: \"*\"\n---\nUnrestricted.")
		r.writeAgent("mixed-case", "---\nallowed_subagents: scout, ALL\n---\nUnrestricted.")
		r.writeAgent("none", "---\nallowed_subagents: none\n---\nOff.")
		r.writeAgent("blank", "---\nallowed_subagents:\n---\nOff.")
		r.writeAgent("restricted", "---\nallowed_subagents: scout, reviewer\n---\nRestricted.")
		eq(t, r.agent("omitted").AllowedSubagents, nil)
		eq(t, r.agent("unrestricted").AllowedSubagents, "all")
		eq(t, r.agent("wildcard").AllowedSubagents, "all")
		eq(t, r.agent("mixed-case").AllowedSubagents, "all")
		eq(t, r.agent("none").AllowedSubagents, nil)
		eq(t, r.agent("blank").AllowedSubagents, nil)
		eq(t, r.agent("restricted").AllowedSubagents, []string{"scout", "reviewer"})
	})
	tw(t, f, `accepts booleans like extensions:/skills: do, instead of a type named "true"`, func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("bool-on", "---\nallowed_subagents: true\n---\nOn.")
		r.writeAgent("bool-off", "---\nallowed_subagents: false\n---\nOff.")
		eq(t, r.agent("bool-on").AllowedSubagents, "all")
		eq(t, r.agent("bool-off").AllowedSubagents, nil)
	})
	tw(t, f, "handles tools: none → empty array", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("notool", "---\ntools: none\n---\nNo tools.")
		eq(t, r.agent("notool").BuiltinToolNames, []string{})
	})
	tw(t, f, "handles extensions: false → no extensions", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("noext", "---\nextensions: false\nskills: false\n---\nNo extensions.")
		eq(t, r.agent("noext").Extensions, false)
		eq(t, r.agent("noext").Skills, false)
	})
	tw(t, f, "handles extension allowlist", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("partial", "---\nextensions: web-search, mcp-server\nskills: planning, review\n---\nPartial access.")
		eq(t, r.agent("partial").Extensions, []string{"web-search", "mcp-server"})
		eq(t, r.agent("partial").Skills, []string{"planning", "review"})
	})
	tw(t, f, "parses exclude_extensions CSV", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("no-notify", "---\nextensions: true\nexclude_extensions: pi-notify, telemetry\n---\nNo notifications.")
		eq(t, r.agent("no-notify").Extensions, true)
		eq(t, r.agent("no-notify").ExcludeExtensions, []string{"pi-notify", "telemetry"})
	})
	tw(t, f, "parses exclude_extensions YAML list", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("no-notify-yaml", "---\nexclude_extensions:\n  - pi-notify\n---\nNo notifications.")
		eq(t, r.agent("no-notify-yaml").ExcludeExtensions, []string{"pi-notify"})
	})
	tw(t, f, "exclude_extensions omitted or none → undefined", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("plain", "---\ndescription: plain\n---\nPlain.")
		r.writeAgent("explicit-none", "---\nexclude_extensions: none\n---\nNone.")
		eq(t, r.agent("plain").ExcludeExtensions, []string(nil))
		eq(t, r.agent("explicit-none").ExcludeExtensions, []string(nil))
	})
	tw(t, f, "passes through unknown tool names (not filtered)", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("custom-tools", "---\ntools: read, my_custom_tool, grep\n---\nCustom tools.")
		eq(t, r.agent("custom-tools").BuiltinToolNames, []string{"read", "my_custom_tool", "grep"})
	})
	tw(t, f, "partitions tools: ext: entries out of builtinToolNames into extSelectors", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("ext-agent", "---\ntools: read, ext:foo, ext:bar/x\n---\nExtension selectors.")
		eq(t, r.agent("ext-agent").BuiltinToolNames, []string{"read"})
		eq(t, r.agent("ext-agent").ExtSelectors, []string{"ext:foo", "ext:bar/x"})
	})
	tw(t, f, "tools: with only ext: entries yields zero built-ins", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("ext-only", "---\ntools: ext:foo/bar\n---\nExt only.")
		eq(t, r.agent("ext-only").BuiltinToolNames, []string{})
		eq(t, r.agent("ext-only").ExtSelectors, []string{"ext:foo/bar"})
	})
	tw(t, f, "tools: '*' expands to all built-ins and composes with ext: selectors", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("wild", "---\ntools: \"*, ext:foo\"\n---\nWildcard plus ext.")
		eq(t, r.agent("wild").BuiltinToolNames, builtinToolNames)
		eq(t, r.agent("wild").ExtSelectors, []string{"ext:foo"})
	})
	tw(t, f, "tools: 'all' is a case-insensitive alias for '*' (closes #75)", func(t *testing.T) {
		r := newCARig(t)
		for _, p := range [][2]string{{"all-lower", "all"}, {"all-upper", "ALL"}, {"all-mixed", "All"}} {
			r.writeAgent(p[0], "---\ntools: "+p[1]+"\n---\n\nAlias.")
			eq(t, r.agent(p[0]).BuiltinToolNames, builtinToolNames)
			eq(t, r.agent(p[0]).ExtSelectors, []string(nil))
		}
	})
	tw(t, f, "tools: 'all' composes with ext: selectors like '*'", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("all-plus-ext", "---\ntools: \"all, ext:foo\"\n---\nAll plus ext.")
		eq(t, r.agent("all-plus-ext").BuiltinToolNames, builtinToolNames)
		eq(t, r.agent("all-plus-ext").ExtSelectors, []string{"ext:foo"})
	})
}

// A byte-order mark in front of the frontmatter does not hide it (the original's agent-file-bom tests).
func TestAgentFileBOM(t *testing.T) {
	r := newCARig(t)
	r.writeAgent("bom", "\ufeff---\ndescription: With BOM\ntools: read\n---\nBody.")
	a := r.agent("bom")
	eq(t, a.Description, "With BOM")
	eq(t, a.BuiltinToolNames, []string{"read"})
	eq(t, a.SystemPrompt, "Body.")
}
