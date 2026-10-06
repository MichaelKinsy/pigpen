package tintinweb_subagents

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCustomAgentsFields(t *testing.T) {
	const f = "custom-agents"
	tw(t, f, "leaves extSelectors undefined when tools: has no ext: entries", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("plain", "---\ntools: read, bash\n---\nPlain tools.")
		eq(t, r.agent("plain").BuiltinToolNames, []string{"read", "bash"})
		eq(t, r.agent("plain").ExtSelectors, []string(nil))
	})
	tw(t, f, "passes through thinking level as-is (no validation)", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("anythink", "---\nthinking: turbo\n---\nAny thinking.")
		eq(t, r.agent("anythink").Thinking, "turbo")
	})
	tw(t, f, "loads thinking: max (pi 0.80's top level) unchanged (#147)", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("deepthink", "---\nthinking: max\n---\nThink hard.")
		eq(t, r.agent("deepthink").Thinking, "max")
	})
	tw(t, f, "accepts max_turns: 0 as unlimited", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("unlimited", "---\nmax_turns: 0\n---\nUnlimited turns.")
		eq(t, *r.agent("unlimited").MaxTurns, 0)
	})
	tw(t, f, "rejects negative max_turns", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("negturns", "---\nmax_turns: -5\n---\nNegative turns.")
		if r.agent("negturns").MaxTurns != nil {
			t.Fatal("a negative max_turns was kept")
		}
	})
	tw(t, f, "handles prompt_mode: append", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("appender", "---\nprompt_mode: append\n---\nExtra instructions.")
		eq(t, r.agent("appender").PromptMode, "append")
	})
	tw(t, f, "defaults unknown prompt_mode to replace", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("badmode", "---\nprompt_mode: merge\n---\nUnknown mode.")
		eq(t, r.agent("badmode").PromptMode, "replace")
	})
	tw(t, f, "loads multiple agents", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("agent1", "---\ndescription: First\n---\nFirst agent.")
		r.writeAgent("agent2", "---\ndescription: Second\n---\nSecond agent.")
		reg := r.load()
		eq(t, len(reg.keys), 2)
		eq(t, reg.m["agent1"] != nil && reg.m["agent2"] != nil, true)
	})
	tw(t, f, "skips non-.md files", func(t *testing.T) {
		r := newCARig(t)
		dir := filepath.Join(r.tmp, ".pi", "agents")
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not an agent"), 0o644)
		os.WriteFile(filepath.Join(dir, "real.md"), []byte("---\ndescription: Real Agent\n---\nReal."), 0o644)
		reg := r.load()
		eq(t, len(reg.keys), 1)
		eq(t, reg.m["real"] != nil, true)
	})
	tw(t, f, "allows agents with names matching defaults (overrides them)", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("Explore", "---\ndescription: Custom Explore\n---\nCustom explore agent.")
		r.writeAgent("custom", "---\ndescription: Custom Agent\n---\nShould be loaded.")
		reg := r.load()
		eq(t, reg.m["Explore"].Description, "Custom Explore")
		eq(t, reg.m["custom"] != nil, true)
	})
	tw(t, f, "handles empty body with frontmatter", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("nobody", "---\ndescription: No body\ntools: read\n---\n")
		eq(t, r.agent("nobody").SystemPrompt, "")
	})
	tw(t, f, "supports inherit_extensions as alternative to extensions", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("altkey", "---\ninherit_extensions: false\ninherit_skills: false\n---\nAlt keys.")
		eq(t, r.agent("altkey").Extensions, false)
		eq(t, r.agent("altkey").Skills, false)
	})
	tw(t, f, "extensions: none → false", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("extnone", "---\nextensions: none\nskills: none\n---\nNone.")
		eq(t, r.agent("extnone").Extensions, false)
		eq(t, r.agent("extnone").Skills, false)
	})
	tw(t, f, "extensions: true → true (inherit all)", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("exttrue", "---\nextensions: true\nskills: true\n---\nAll.")
		eq(t, r.agent("exttrue").Extensions, true)
		eq(t, r.agent("exttrue").Skills, true)
	})
}
