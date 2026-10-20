package tintinweb_subagents

import (
	"encoding/json"
	"strings"
	"testing"
)

// Twins of test/invocation-config.test.ts and test/isolation-param.test.ts. upstream: src/invocation-config.ts.

func bp(b bool) *bool { return &b }
func ip(i int) *int   { return &i }
func cfgWith(mod ...func(*agentConfig)) *agentConfig {
	c := &agentConfig{Name: "Explore", Description: "Explore", BuiltinToolNames: []string{"read"}, Extensions: false, Skills: false,
		SystemPrompt: "Test agent", PromptMode: "replace", InheritContext: bp(false), RunInBackground: bp(false), Isolated: bp(false), Enabled: true}
	for _, m := range mod {
		m(c)
	}
	return c
}

func TestInvocationConfigTwin(t *testing.T) {
	const f = "invocation-config"
	t.Run("resolveAgentInvocationConfig", func(t *testing.T) {
		tw(t, f, "prefers agent config over tool-call params for locked fields", func(t *testing.T) {
			r := resolveAgentInvocationConfig(cfgWith(func(c *agentConfig) {
				c.Model, c.Thinking, c.MaxTurns = "provider/config-model", "high", ip(42)
				c.InheritContext, c.RunInBackground, c.Isolated, c.Isolation = bp(false), bp(false), bp(false), "worktree"
			}), invocationParams{Model: ptr("provider/param-model"), Thinking: ptr("minimal"), MaxTurns: ip(1), InheritContext: bp(true),
				RunInBackground: bp(true), Isolated: bp(true), Isolation: "worktree"}, resolveOptions{})
			eq(t, r.ModelInput, "provider/config-model")
			eq(t, r.ModelFromParams, false)
			eq(t, r.Thinking, "high")
			eq(t, *r.MaxTurns, 42)
			eq(t, r.InheritContext, false)
			eq(t, r.RunInBackground, false)
			eq(t, r.Isolated, false)
			eq(t, r.Isolation, "worktree")
		})
		tw(t, f, "uses tool-call params when no agent config is available", func(t *testing.T) {
			r := resolveAgentInvocationConfig(nil, invocationParams{Model: ptr("provider/param-model"), Thinking: ptr("minimal"), MaxTurns: ip(3),
				InheritContext: bp(true), RunInBackground: bp(true), Isolated: bp(true), Isolation: "worktree"}, resolveOptions{})
			eq(t, r.ModelInput, "provider/param-model")
			eq(t, r.ModelFromParams, true)
			eq(t, r.Thinking, "minimal")
			eq(t, *r.MaxTurns, 3)
			eq(t, r.InheritContext, true)
			eq(t, r.RunInBackground, true)
			eq(t, r.Isolated, true)
			eq(t, r.Isolation, "worktree")
		})
		tw(t, f, "lets parent fill in booleans when config leaves them undefined", func(t *testing.T) {
			r := resolveAgentInvocationConfig(cfgWith(func(c *agentConfig) { c.InheritContext, c.RunInBackground, c.Isolated = nil, nil, nil }),
				invocationParams{InheritContext: bp(true), RunInBackground: bp(true), Isolated: bp(true)}, resolveOptions{})
			eq(t, [3]bool{r.InheritContext, r.RunInBackground, r.Isolated}, [3]bool{true, true, true})
		})
		tw(t, f, "defaults booleans to false when neither config nor params set them", func(t *testing.T) {
			r := resolveAgentInvocationConfig(cfgWith(func(c *agentConfig) { c.InheritContext, c.RunInBackground, c.Isolated = nil, nil, nil }), invocationParams{}, resolveOptions{})
			eq(t, [3]bool{r.InheritContext, r.RunInBackground, r.Isolated}, [3]bool{false, false, false})
			// the caller's default for an unqualified spawn applies to run_in_background only
			r = resolveAgentInvocationConfig(cfgWith(func(c *agentConfig) { c.RunInBackground = nil }), invocationParams{}, resolveOptions{DefaultRunInBackground: true})
			eq(t, r.RunInBackground, true)
		})
		tw(t, f, `collapses a param isolation of "off" to undefined`, func(t *testing.T) {
			eq(t, resolveAgentInvocationConfig(cfgWith(), invocationParams{Isolation: "off"}, resolveOptions{}).Isolation, "")
		})
		tw(t, f, `lets a config isolation of "off" veto a param "worktree"`, func(t *testing.T) {
			eq(t, resolveAgentInvocationConfig(cfgWith(func(c *agentConfig) { c.Isolation = "off" }), invocationParams{Isolation: "worktree"}, resolveOptions{}).Isolation, "")
		})
		tw(t, f, `still honours a param "worktree" when the config leaves isolation unset`, func(t *testing.T) {
			eq(t, resolveAgentInvocationConfig(cfgWith(), invocationParams{Isolation: "worktree"}, resolveOptions{}).Isolation, "worktree")
		})
		tw(t, f, "drops worktree isolation when the project disallows it", func(t *testing.T) {
			r := resolveAgentInvocationConfig(cfgWith(func(c *agentConfig) { c.Isolation = "worktree" }), invocationParams{Isolation: "worktree"}, resolveOptions{WorktreeAllowed: bp(false)})
			eq(t, r.Isolation, "")
		})
		tw(t, f, "keeps worktree isolation when the project allows it", func(t *testing.T) {
			r := resolveAgentInvocationConfig(cfgWith(func(c *agentConfig) { c.Isolation = "worktree" }), invocationParams{}, resolveOptions{WorktreeAllowed: bp(true)})
			eq(t, r.Isolation, "worktree")
			// unset means allowed
			eq(t, resolveAgentInvocationConfig(cfgWith(func(c *agentConfig) { c.Isolation = "worktree" }), invocationParams{}, resolveOptions{}).Isolation, "worktree")
		})
	})
	t.Run("resolveJoinMode", func(t *testing.T) {
		tw(t, f, "returns the global default for background agents", func(t *testing.T) {
			eq(t, resolveJoinMode("smart", true), "smart")
			eq(t, resolveJoinMode("async", true), "async")
		})
		tw(t, f, "ignores join mode for foreground agents", func(t *testing.T) {
			eq(t, resolveJoinMode("smart", false), "")
			eq(t, resolveJoinMode("group", false), "")
		})
	})
	t.Run("resolveAgentInvocationConfig — overridden params (#182)", func(t *testing.T) {
		tw(t, f, "records the caller's values when the agent file outranks them", func(t *testing.T) {
			r := resolveAgentInvocationConfig(cfgWith(func(c *agentConfig) { c.Model, c.Thinking = "provider/config-model", "low" }),
				invocationParams{Model: ptr("provider/param-model"), Thinking: ptr("max")}, resolveOptions{})
			eq(t, *r.Overridden, overriddenParams{Thinking: "max", Model: "provider/param-model"})
		})
		tw(t, f, "records nothing when the caller got what they asked for", func(t *testing.T) {
			r := resolveAgentInvocationConfig(cfgWith(func(c *agentConfig) { c.Model, c.Thinking = "provider/same", "high" }),
				invocationParams{Model: ptr("provider/same"), Thinking: ptr("high")}, resolveOptions{})
			eq(t, r.Overridden == nil, true)
		})
		tw(t, f, "records nothing when only one side named a value", func(t *testing.T) {
			eq(t, resolveAgentInvocationConfig(cfgWith(func(c *agentConfig) { c.Model, c.Thinking = "provider/config-model", "low" }), invocationParams{}, resolveOptions{}).Overridden == nil, true)
			eq(t, resolveAgentInvocationConfig(cfgWith(), invocationParams{Model: ptr("provider/param-model"), Thinking: ptr("max")}, resolveOptions{}).Overridden == nil, true)
		})
		tw(t, f, "records each field independently", func(t *testing.T) {
			r := resolveAgentInvocationConfig(cfgWith(func(c *agentConfig) { c.Thinking = "low" }), invocationParams{Model: ptr("provider/param-model"), Thinking: ptr("max")}, resolveOptions{})
			eq(t, *r.Overridden, overriddenParams{Thinking: "max"})
		})
	})
}

func TestIsolationParamTwin(t *testing.T) {
	const f = "isolation-param"
	values := func() []any {
		s, _ := isolationParam(true)["isolation"].(map[string]any)
		var out []any
		for _, v := range s["anyOf"].([]any) {
			out = append(out, v.(map[string]any)["const"])
		}
		return out
	}
	tw(t, f, "offers a value meaning 'no isolation', not just 'worktree'", func(t *testing.T) {
		v := values()
		eq(t, v[0] == "off" || v[1] == "off", true)
		eq(t, v[0] == "worktree" || v[1] == "worktree", true)
	})
	tw(t, f, "lists the inert value first", func(t *testing.T) { eq(t, values()[0], "off") })
	tw(t, f, "warns that a worktree cannot see uncommitted work", func(t *testing.T) {
		b, _ := json.Marshal(isolationParam(true)["isolation"])
		if !strings.Contains(string(b), "uncommitted or staged") {
			t.Fatal(string(b))
		}
	})
	tw(t, f, "omits the parameter entirely when the project disabled worktrees", func(t *testing.T) {
		eq(t, len(isolationParam(false)), 0)
	})
}
