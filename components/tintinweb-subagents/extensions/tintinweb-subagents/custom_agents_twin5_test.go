package tintinweb_subagents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadCustomAgentsWarnings(t *testing.T) {
	const f = "custom-agents"
	tw(t, f, "warns when a skipped file was overriding an agent that stays resolvable", func(t *testing.T) {
		r := newCARig(t)
		r.writeWorkspace("dup", "---\ndescription: Earlier definition\n---\n\nEarlier body.")
		r.writeAgent("dup", "---\nname: dup\ndescription: Use this: that\n---\n\nBroken body.")
		reg, message := r.loadWarn()
		eq(t, reg.m["dup"].Description, "Earlier definition")
		eq(t, strings.Contains(message, `Agent "dup" now loads from `+filepath.Join(r.tmp, ".agents", "agents", "dup.md")+" instead"), true)
	})
	tw(t, f, "does not claim a fallback when the shadowed definition is disabled", func(t *testing.T) {
		r := newCARig(t)
		r.writeWorkspace("dup", "---\ndescription: Earlier definition\nenabled: false\n---\n\nEarlier body.")
		r.writeAgent("dup", "---\nname: dup\ndescription: Use this: that\n---\n\nBroken body.")
		_, message := r.loadWarn()
		eq(t, strings.Contains(message, "Skipping agent file"), true)
		eq(t, strings.Contains(message, "now loads from"), false)
	})
	tw(t, f, "does not claim a fallback when the skipped file overrode nothing", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("lonely", "---\nname: lonely\ndescription: Use this: that\n---\n\nBroken body.")
		_, message := r.loadWarn()
		eq(t, strings.Contains(message, "Skipping agent file"), true)
		eq(t, strings.Contains(message, "now loads from"), false)
	})
	tw(t, f, "throws naming the file when strict, and skips it when not", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("broken", "---\nname: broken\ndescription: Use this: that\n---\n\nBroken.")
		r.writeAgent("healthy", "---\ndescription: Fine\n---\n\nFine.")
		broken := filepath.Join(r.tmp, ".pi", "agents", "broken.md")
		_, _, err := loadCustomAgentsStrict(r.tmp, true)
		eq(t, err != nil && strings.Contains(err.Error(), broken), true)
		eq(t, strings.Contains(err.Error(), "Nested mappings are not allowed"), true)
		reg := r.load()
		eq(t, reg.m["broken"] == nil, true)
		eq(t, reg.m["healthy"] != nil, true)
	})
	tw(t, f, "warns when a file breaks, not while it stays broken", func(t *testing.T) {
		r := newCARig(t)
		count := 0
		load := func() *agentRegistry {
			reg, w := loadCustomAgents(r.tmp)
			count += len(w)
			return reg
		}
		r.writeAgent("flip", "---\nname: flip\ndescription: Use this: that\n---\n\nBroken.")
		load()
		load()
		eq(t, count, 1)
		r.writeAgent("flip", "---\ndescription: Fixed\n---\n\nFixed.")
		eq(t, load().m["flip"].Description, "Fixed")
		r.writeAgent("flip", "---\nname: flip\ndescription: Use this: that\n---\n\nBroken.")
		load()
		eq(t, count, 2)
	})
	tw(t, f, "warns once per message, not on every reload", func(t *testing.T) {
		r := newCARig(t)
		count := 0
		r.writeAgent("noisy", "---\nname: noisy\ndescription: Use this: that\n---\n\nBroken body.")
		for i := 0; i < 3; i++ {
			_, w := loadCustomAgents(r.tmp)
			count += len(w)
		}
		eq(t, count, 1)
	})
	tw(t, f, "honors PI_CODING_AGENT_DIR for global custom agent discovery", func(t *testing.T) {
		r := newCARig(t)
		_ = r
		alt := t.TempDir()
		t.Setenv("PIG_CODING_AGENT_DIR", alt)
		os.MkdirAll(filepath.Join(alt, "agents"), 0o755)
		os.WriteFile(filepath.Join(alt, "agents", "via-env.md"), []byte("---\ndescription: Discovered via env var\n---\n\nTest body."), 0o644)
		a := r.agent("via-env")
		eq(t, a.Description, "Discovered via env var")
	})
}

func TestEjectRoundTrip(t *testing.T) {
	const f = "custom-agents"
	rt := func(t *testing.T, mod func(c *agentConfig)) *agentConfig {
		r := newCARig(t)
		c := &agentConfig{Description: "Round trip agent", SystemPrompt: "Body prompt.", PromptMode: "append", Enabled: true}
		mod(c)
		r.writeAgent("rt", serializeAgentFile(c))
		return r.agent("rt")
	}
	b := func(v bool) *bool { return &v }
	tw(t, f, "preserves an explicitly narrowed tool list", func(t *testing.T) {
		eq(t, rt(t, func(c *agentConfig) { c.BuiltinToolNames = []string{"read", "grep"} }).BuiltinToolNames, []string{"read", "grep"})
	})
	tw(t, f, "preserves the full built-in set", func(t *testing.T) {
		eq(t, rt(t, func(c *agentConfig) { c.BuiltinToolNames = append([]string{}, builtinToolNames...) }).BuiltinToolNames, builtinToolNames)
	})
	tw(t, f, "preserves an empty tool list instead of widening it to every built-in", func(t *testing.T) {
		eq(t, rt(t, func(c *agentConfig) { c.BuiltinToolNames = []string{} }).BuiltinToolNames, []string{})
	})
	tw(t, f, "preserves the scalar and list fields it writes", func(t *testing.T) {
		seven := 7
		l := rt(t, func(c *agentConfig) {
			c.DisplayName, c.Model, c.Thinking, c.MaxTurns = "RT", "anthropic/claude-haiku-4-5", "low", &seven
			c.AllowedSubagents = []string{"Explore"}
			c.ExcludeExtensions, c.DisallowedTools = []string{"ext-beta"}, []string{"write"}
			c.InheritContext, c.RunInBackground, c.OutputTranscript, c.Isolated = b(true), b(true), b(false), b(true)
			c.Memory, c.Isolation = "project", "worktree"
		})
		eq(t, l.DisplayName, "RT")
		eq(t, l.Model, "anthropic/claude-haiku-4-5")
		eq(t, l.Thinking, "low")
		eq(t, *l.MaxTurns, 7)
		eq(t, l.AllowedSubagents, []string{"Explore"})
		eq(t, l.ExcludeExtensions, []string{"ext-beta"})
		eq(t, l.DisallowedTools, []string{"write"})
		eq(t, *l.InheritContext, true)
		eq(t, *l.RunInBackground, true)
		eq(t, *l.OutputTranscript, false)
		eq(t, *l.Isolated, true)
		eq(t, l.Memory, "project")
		eq(t, l.Isolation, "worktree")
	})
	tw(t, f, "preserves an explicit run_in_background: false instead of dropping it", func(t *testing.T) {
		eq(t, *rt(t, func(c *agentConfig) { c.RunInBackground = b(false) }).RunInBackground, false)
	})
	tw(t, f, "leaves run_in_background unset when the config doesn't pin it", func(t *testing.T) {
		eq(t, rt(t, func(c *agentConfig) {}).RunInBackground == nil, true)
	})
	tw(t, f, "preserves the extension and skill list fields", func(t *testing.T) {
		l := rt(t, func(c *agentConfig) {
			c.Extensions, c.Skills, c.DisallowedTools = []string{"mcp", "pi-notify"}, []string{"planning", "review"}, []string{"write", "edit"}
		})
		eq(t, l.Extensions, []string{"mcp", "pi-notify"})
		eq(t, l.Skills, []string{"planning", "review"})
		eq(t, l.DisallowedTools, []string{"write", "edit"})
	})
	tw(t, f, "preserves the boolean forms of extensions and skills", func(t *testing.T) {
		l := rt(t, func(c *agentConfig) { c.Extensions, c.Skills = false, false })
		eq(t, l.Extensions, false)
		eq(t, l.Skills, false)
	})
	tw(t, f, "preserves allowed_subagents in both its list and `all` forms", func(t *testing.T) {
		eq(t, rt(t, func(c *agentConfig) { c.AllowedSubagents = "all" }).AllowedSubagents, "all")
		eq(t, rt(t, func(c *agentConfig) { c.AllowedSubagents = []string{"Explore", "Plan"} }).AllowedSubagents, []string{"Explore", "Plan"})
	})
	tw(t, f, "preserves a description containing a colon", func(t *testing.T) {
		eq(t, rt(t, func(c *agentConfig) { c.Description = "Scout: find things" }).Description, "Scout: find things")
	})
}
