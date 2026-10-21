package tintinweb_subagents

import (
	"strings"
	"testing"
)

// Twins of the original's test/prompts.test.ts and test/env.test.ts. The prompt builder is a pure function, so the
// cases run unchanged; the env cases run git for real, as the original's do.

func TestBuildAgentPromptTwins(t *testing.T) {
	const f = "prompts"
	env := envInfo{IsGitRepo: true, Branch: "main", Platform: "darwin"}
	envNoGit := envInfo{IsGitRepo: false, Branch: "", Platform: "linux"}
	def := func(name string) *agentConfig {
		registerAgents(newRegistry())
		return getAgentConfig(name)
	}
	cfg := func(name, system, mode string) *agentConfig {
		c := &agentConfig{Name: name, Description: name, BuiltinToolNames: []string{}, Extensions: true, Skills: true, SystemPrompt: system, PromptMode: mode, Enabled: true}
		fl := false
		c.InheritContext, c.RunInBackground, c.Isolated = &fl, &fl, &fl
		return c
	}
	has := func(t *testing.T, s string, subs ...string) {
		t.Helper()
		for _, sub := range subs {
			if !strings.Contains(s, sub) {
				t.Fatalf("%q does not contain %q", s, sub)
			}
		}
	}
	not := func(t *testing.T, s string, subs ...string) {
		t.Helper()
		for _, sub := range subs {
			if strings.Contains(s, sub) {
				t.Fatalf("%q contains %q", s, sub)
			}
		}
	}
	none := promptExtras{}

	tw(t, f, "includes cwd and git info", func(t *testing.T) {
		has(t, buildAgentPrompt(def("general-purpose"), "/workspace", env, "", none), "/workspace", "Branch: main", "darwin")
	})
	tw(t, f, "handles non-git repos", func(t *testing.T) {
		p := buildAgentPrompt(def("Explore"), "/workspace", envNoGit, "", none)
		has(t, p, "Not a git repository")
		not(t, p, "Branch:")
	})
	tw(t, f, "Explore prompt is read-only", func(t *testing.T) {
		has(t, buildAgentPrompt(def("Explore"), "/workspace", env, "", none), "READ-ONLY", "file search specialist")
	})
	tw(t, f, "Plan prompt is read-only", func(t *testing.T) {
		has(t, buildAgentPrompt(def("Plan"), "/workspace", env, "", none), "READ-ONLY", "software architect")
	})
	tw(t, f, "general-purpose uses append mode (parent twin)", func(t *testing.T) {
		p := buildAgentPrompt(def("general-purpose"), "/workspace", env, "You are a parent coding agent with full powers.", none)
		has(t, p, "parent coding agent with full powers", "<sub_agent_context>")
		not(t, p, "<inherited_system_prompt>", "READ-ONLY", "<agent_instructions>")
	})
	tw(t, f, "general-purpose without parent prompt falls back to generic base", func(t *testing.T) {
		p := buildAgentPrompt(def("general-purpose"), "/workspace", env, "", none)
		has(t, p, "general-purpose coding agent")
		not(t, p, "READ-ONLY")
	})
	tw(t, f, "append mode with parent prompt includes parent + custom instructions", func(t *testing.T) {
		p := buildAgentPrompt(cfg("appender", "Extra custom instructions here.", "append"), "/workspace", env, "You are a parent coding agent with special powers.", none)
		has(t, p, "/workspace", "parent coding agent with special powers", "<sub_agent_context>", "<agent_instructions>", "Extra custom instructions here.")
		not(t, p, "<inherited_system_prompt>")
	})
	tw(t, f, "append mode without parent prompt falls back to generic base", func(t *testing.T) {
		has(t, buildAgentPrompt(cfg("appender", "Extra custom instructions here.", "append"), "/workspace", env, "", none), "/workspace", "general-purpose coding agent", "Extra custom instructions here.")
	})
	tw(t, f, "append mode with empty systemPrompt is a pure parent clone", func(t *testing.T) {
		p := buildAgentPrompt(cfg("clone", "", "append"), "/workspace", env, "You are a parent coding agent.", none)
		has(t, p, "parent coding agent", "<sub_agent_context>")
		not(t, p, "<inherited_system_prompt>", "<agent_instructions>")
	})
	tw(t, f, "replace mode uses config systemPrompt directly", func(t *testing.T) {
		has(t, buildAgentPrompt(cfg("custom", "You are a specialized agent.", "replace"), "/workspace", env, "", none), "You are a specialized agent.", "/workspace", "You are a pi coding agent sub-agent")
	})
	tw(t, f, "replace mode ignores parent prompt", func(t *testing.T) {
		p := buildAgentPrompt(cfg("standalone", "You are a standalone agent.", "replace"), "/workspace", env, "SECRET parent prompt content", none)
		has(t, p, "You are a standalone agent.")
		not(t, p, "SECRET parent prompt content", "<sub_agent_context>")
	})
	tw(t, f, "append mode bridge contains tool reminders", func(t *testing.T) {
		has(t, buildAgentPrompt(def("general-purpose"), "/workspace", env, "Parent prompt.", none), "Use the read tool instead of cat", "Use the edit tool instead of sed", "Use the grep tool instead of")
	})
	tw(t, f, "append mode without parent prompt still has bridge", func(t *testing.T) {
		p := buildAgentPrompt(cfg("no-parent", "Extra stuff.", "append"), "/workspace", env, "", none)
		has(t, p, "<sub_agent_context>", "Use the read tool instead of cat", "general-purpose coding agent", "Extra stuff.")
		not(t, p, "<inherited_system_prompt>")
	})
	tw(t, f, "injects memory block in replace mode", func(t *testing.T) {
		p := buildAgentPrompt(cfg("mem-agent", "You are a memory agent.", "replace"), "/workspace", env, "", promptExtras{MemoryBlock: "# Agent Memory\nYou have persistent memory at /tmp/mem/"})
		has(t, p, "You are a memory agent.", "Agent Memory", "persistent memory")
	})
	tw(t, f, "injects memory block in append mode", func(t *testing.T) {
		p := buildAgentPrompt(cfg("mem-append", "Custom instructions.", "append"), "/workspace", env, "Parent prompt.", promptExtras{MemoryBlock: "# Agent Memory\nPersistent memory here."})
		has(t, p, "<sub_agent_context>", "Agent Memory", "Custom instructions.")
	})
	tw(t, f, "injects preloaded skill blocks", func(t *testing.T) {
		p := buildAgentPrompt(cfg("skill-agent", "You are a skill agent.", "replace"), "/workspace", env, "", promptExtras{SkillBlocks: []preloadedSkill{
			{Name: "api-conventions", Content: "Use REST endpoints."}, {Name: "error-handling", Content: "Handle errors gracefully."}}})
		has(t, p, "Preloaded Skill: api-conventions", "Use REST endpoints.", "Preloaded Skill: error-handling", "Handle errors gracefully.")
	})
	tw(t, f, "injects both memory and skills", func(t *testing.T) {
		p := buildAgentPrompt(cfg("full-agent", "Full agent.", "replace"), "/workspace", env, "", promptExtras{MemoryBlock: "# Memory\nRemember this.", SkillBlocks: []preloadedSkill{{Name: "skill1", Content: "Skill content."}}})
		has(t, p, "# Memory", "Preloaded Skill: skill1")
	})
	tw(t, f, "no extras means no extra sections", func(t *testing.T) {
		p := buildAgentPrompt(cfg("plain", "Plain agent.", "replace"), "/workspace", env, "", none)
		not(t, p, "Agent Memory", "Preloaded Skill")
	})
	tw(t, f, "tag is present at start of prompt in replace mode", func(t *testing.T) {
		if !strings.HasPrefix(buildAgentPrompt(cfg("my-agent", "You are a test agent.", "replace"), "/workspace", env, "", none), `<active_agent name="my-agent"/>`) {
			t.Fatal("no tag at the start")
		}
	})
	tw(t, f, "tag follows the cacheable inherited prefix in append mode", func(t *testing.T) {
		p := buildAgentPrompt(cfg("my-agent", "Custom instructions.", "append"), "/workspace", env, "Parent prompt.", none)
		if !strings.HasPrefix(p, "Parent prompt.") {
			t.Fatal("the parent prompt is not the verbatim prefix")
		}
		ctxIdx, tagIdx := strings.Index(p, "<sub_agent_context>"), strings.Index(p, `<active_agent name="my-agent"/>`)
		if ctxIdx <= -1 || tagIdx <= ctxIdx {
			t.Fatalf("the tag (%d) must follow the bridge (%d)", tagIdx, ctxIdx)
		}
	})
	tw(t, f, "tag uses agent name verbatim", func(t *testing.T) {
		has(t, buildAgentPrompt(cfg("Some Agent With Spaces", "Test.", "replace"), "/workspace", env, "", none), `<active_agent name="Some Agent With Spaces"/>`)
	})
	tw(t, f, "tag appears before the env block in both modes", func(t *testing.T) {
		for _, mode := range []string{"replace", "append"} {
			p := buildAgentPrompt(cfg("test-agent", "Test.", mode), "/workspace", env, "Parent.", none)
			if strings.Index(p, `<active_agent name="test-agent"/>`) >= strings.Index(p, "# Environment") {
				t.Fatalf("%s: the tag does not come before the env block", mode)
			}
		}
	})
	wt := func(mode string) *agentConfig { return cfg("test-agent", "Custom instructions.", mode) }
	tw(t, f, "is absent without a worktree base", func(t *testing.T) {
		for _, mode := range []string{"replace", "append"} {
			not(t, buildAgentPrompt(wt(mode), "/wt/copy", env, "Parent.", none), "<worktree_isolation>")
		}
	})
	tw(t, f, "names the parent checkout and follows the env block in both modes", func(t *testing.T) {
		for _, mode := range []string{"replace", "append"} {
			p := buildAgentPrompt(wt(mode), "/wt/copy", env, "Parent.", promptExtras{WorktreeBase: "/repo"})
			has(t, p, "isolated git worktree copy of /repo", "never in /repo, even if other instructions name that path")
			if strings.Index(p, "<worktree_isolation>") <= strings.Index(p, "Working directory: /wt/copy") {
				t.Fatal("the block must follow the env block")
			}
		}
	})
	tw(t, f, "stays out of the cacheable inherited prefix", func(t *testing.T) {
		p := buildAgentPrompt(wt("append"), "/wt/copy", env, "Parent prompt.", promptExtras{WorktreeBase: "/repo"})
		if !strings.HasPrefix(p, "Parent prompt.") || strings.Index(p, "<worktree_isolation>") <= strings.Index(p, "<sub_agent_context>") {
			t.Fatal("the block leaked into the inherited prefix")
		}
	})
	tw(t, f, "is absent for an ordinary subagent, whose output a person reads", func(t *testing.T) {
		for _, mode := range []string{"replace", "append"} {
			not(t, buildAgentPrompt(wt(mode), "/workspace", env, "Parent.", none), "<workflow_child>")
		}
	})
	tw(t, f, "tells a workflow child its final message is the return value, in both modes", func(t *testing.T) {
		for _, mode := range []string{"replace", "append"} {
			p := buildAgentPrompt(wt(mode), "/workspace", env, "Parent.", promptExtras{WorkflowChild: true})
			has(t, p, "<workflow_child>", "Your final message IS the return value", "no preamble")
			if strings.Index(p, "<workflow_child>") <= strings.Index(p, "Working directory: /workspace") {
				t.Fatal("the block must follow the env block")
			}
		}
	})
	tw(t, f, "stays out of the cacheable inherited prefix", func(t *testing.T) {
		p := buildAgentPrompt(wt("append"), "/workspace", env, "Parent prompt.", promptExtras{WorkflowChild: true})
		if !strings.HasPrefix(p, "Parent prompt.") || strings.Index(p, "<workflow_child>") <= strings.Index(p, "<sub_agent_context>") {
			t.Fatal("the block leaked into the inherited prefix")
		}
	})
	tw(t, f, "composes with worktree isolation rather than displacing it", func(t *testing.T) {
		p := buildAgentPrompt(wt("append"), "/wt/copy", env, "Parent.", promptExtras{WorktreeBase: "/repo", WorkflowChild: true})
		has(t, p, "<worktree_isolation>")
		if strings.Index(p, "<workflow_child>") <= strings.Index(p, "<worktree_isolation>") {
			t.Fatal("the workflow block must follow the worktree block")
		}
	})
}
