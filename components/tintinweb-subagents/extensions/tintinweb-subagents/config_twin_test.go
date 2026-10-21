package tintinweb_subagents

import (
	"testing"
)

// Twins of test/documented-defaults.test.ts and test/invocation-config.test.ts.

func TestDocumentedDefaultsTwins(t *testing.T) {
	const f = "documented-defaults"
	tw(t, f, "grace turns after the soft limit default to 5", func(t *testing.T) {
		eq(t, defaultRuntimeSettings().graceTurns, 5)
		eq(t, newApp(nil, nil).runtime().graceTurns, 5) // a fresh activation, before any setting is applied
	})
	tw(t, f, "max turns is unlimited by default", func(t *testing.T) {
		eq(t, defaultRuntimeSettings().defaultMaxTurns == nil, true)
	})
	tw(t, f, "nested subagent depth defaults to 2", func(t *testing.T) { eq(t, settings{}.maxSubagentDepth(), 2) })
	tw(t, f, "background concurrency defaults to 10", func(t *testing.T) {
		m := newManager(nil, nil)
		defer m.dispose()
		eq(t, m.getMaxConcurrent(), 10)
	})
	tw(t, f, "foreground concurrency is unlimited by default", func(t *testing.T) {
		m := newManager(nil, nil)
		defer m.dispose()
		eq(t, m.getMaxConcurrentForeground(), 0)
	})
	tw(t, f, "top-level spawns default to background, nested spawns to foreground", func(t *testing.T) {
		yes, no := true, false
		eq(t, resolveInvocationConfig(nil, invocationParams{}, invocationOptions{defaultRunInBackground: true}).runInBackground, true)
		eq(t, resolveInvocationConfig(nil, invocationParams{}, invocationOptions{defaultRunInBackground: false}).runInBackground, false)
		eq(t, resolveInvocationConfig(nil, invocationParams{RunInBackground: &no}, invocationOptions{defaultRunInBackground: true}).runInBackground, false)
		eq(t, resolveInvocationConfig(nil, invocationParams{RunInBackground: &yes}, invocationOptions{defaultRunInBackground: false}).runInBackground, true)
		eq(t, defaultRuntimeSettings().backgroundByDefault, true) // the setting's default, which is what a top-level call passes
	})
	tw(t, f, "model scope is off by default", func(t *testing.T) {
		// Off is the safe default: on, an unconfigured enabledModels would start refusing spawns.
		eq(t, isScopeModelsEnabled(), false)
	})
}

func invConfig(mutate ...func(*agentConfig)) *agentConfig {
	no := false
	c := &agentConfig{Name: "Explore", Description: "Explore", BuiltinToolNames: []string{"read"}, Extensions: false, Skills: false, SystemPrompt: "Test agent",
		PromptMode: "replace", InheritContext: &no, RunInBackground: &no, Isolated: &no}
	for _, m := range mutate {
		m(c)
	}
	return c
}

func TestInvocationConfigTwins(t *testing.T) {
	const f = "invocation-config"
	yes, no := true, false
	n42, n1, n3 := 42, 1, 3
	tw(t, f, "prefers agent config over tool-call params for locked fields", func(t *testing.T) {
		r := resolveInvocationConfig(invConfig(func(c *agentConfig) {
			c.Model, c.Thinking, c.MaxTurns, c.InheritContext, c.RunInBackground, c.Isolated, c.Isolation = "provider/config-model", "high", &n42, &no, &no, &no, "worktree"
		}), invocationParams{Model: "provider/param-model", Thinking: "minimal", MaxTurns: &n1, InheritContext: &yes, RunInBackground: &yes, Isolated: &yes, Isolation: "worktree"}, invocationOptions{})
		eq(t, [2]any{r.modelInput, r.modelFromParams}, [2]any{"provider/config-model", false})
		eq(t, r.thinking, "high")
		eq(t, *r.maxTurns, 42)
		eq(t, [4]any{r.inheritContext, r.runInBackground, r.isolated, r.isolation}, [4]any{false, false, false, "worktree"})
	})
	tw(t, f, "uses tool-call params when no agent config is available", func(t *testing.T) {
		r := resolveInvocationConfig(nil, invocationParams{Model: "provider/param-model", Thinking: "minimal", MaxTurns: &n3, InheritContext: &yes, RunInBackground: &yes, Isolated: &yes, Isolation: "worktree"}, invocationOptions{})
		eq(t, [2]any{r.modelInput, r.modelFromParams}, [2]any{"provider/param-model", true})
		eq(t, r.thinking, "minimal")
		eq(t, *r.maxTurns, 3)
		eq(t, [4]any{r.inheritContext, r.runInBackground, r.isolated, r.isolation}, [4]any{true, true, true, "worktree"})
	})
	tw(t, f, "lets parent fill in booleans when config leaves them undefined", func(t *testing.T) {
		r := resolveInvocationConfig(invConfig(func(c *agentConfig) { c.InheritContext, c.RunInBackground, c.Isolated = nil, nil, nil }),
			invocationParams{InheritContext: &yes, RunInBackground: &yes, Isolated: &yes}, invocationOptions{})
		eq(t, [3]bool{r.inheritContext, r.runInBackground, r.isolated}, [3]bool{true, true, true})
	})
	tw(t, f, "defaults booleans to false when neither config nor params set them", func(t *testing.T) {
		r := resolveInvocationConfig(invConfig(func(c *agentConfig) { c.InheritContext, c.RunInBackground, c.Isolated = nil, nil, nil }), invocationParams{}, invocationOptions{})
		eq(t, [3]bool{r.inheritContext, r.runInBackground, r.isolated}, [3]bool{false, false, false})
		// the caller's default for an unqualified spawn applies to run_in_background only
		r = resolveInvocationConfig(invConfig(func(c *agentConfig) { c.RunInBackground = nil }), invocationParams{}, invocationOptions{defaultRunInBackground: true})
		eq(t, r.runInBackground, true)
	})
	tw(t, f, "collapses a param isolation of \"off\" to undefined", func(t *testing.T) {
		eq(t, resolveInvocationConfig(invConfig(func(c *agentConfig) { c.Isolation = "" }), invocationParams{Isolation: "off"}, invocationOptions{}).isolation, "")
	})
	tw(t, f, "lets a config isolation of \"off\" veto a param \"worktree\"", func(t *testing.T) {
		eq(t, resolveInvocationConfig(invConfig(func(c *agentConfig) { c.Isolation = "off" }), invocationParams{Isolation: "worktree"}, invocationOptions{}).isolation, "")
	})
	tw(t, f, "still honours a param \"worktree\" when the config leaves isolation unset", func(t *testing.T) {
		eq(t, resolveInvocationConfig(invConfig(func(c *agentConfig) { c.Isolation = "" }), invocationParams{Isolation: "worktree"}, invocationOptions{}).isolation, "worktree")
	})
	tw(t, f, "drops worktree isolation when the project disallows it", func(t *testing.T) {
		eq(t, resolveInvocationConfig(invConfig(func(c *agentConfig) { c.Isolation = "worktree" }), invocationParams{Isolation: "worktree"}, invocationOptions{worktreeDisallowed: true}).isolation, "")
	})
	tw(t, f, "keeps worktree isolation when the project allows it", func(t *testing.T) {
		eq(t, resolveInvocationConfig(invConfig(func(c *agentConfig) { c.Isolation = "worktree" }), invocationParams{}, invocationOptions{}).isolation, "worktree")
	})
	tw(t, f, "returns the global default for background agents", func(t *testing.T) {
		eq(t, resolveJoinMode("smart", true), "smart")
		eq(t, resolveJoinMode("async", true), "async")
	})
	tw(t, f, "ignores join mode for foreground agents", func(t *testing.T) {
		eq(t, resolveJoinMode("smart", false), "")
		eq(t, resolveJoinMode("group", false), "")
	})
	tw(t, f, "records the caller's values when the agent file outranks them", func(t *testing.T) {
		r := resolveInvocationConfig(invConfig(func(c *agentConfig) { c.Model, c.Thinking = "provider/config-model", "low" }),
			invocationParams{Model: "provider/param-model", Thinking: "max"}, invocationOptions{})
		eq(t, [2]string{r.overriddenThinking, r.overriddenModel}, [2]string{"max", "provider/param-model"})
	})
	tw(t, f, "records nothing when the caller got what they asked for", func(t *testing.T) {
		r := resolveInvocationConfig(invConfig(func(c *agentConfig) { c.Model, c.Thinking = "provider/same", "high" }), invocationParams{Model: "provider/same", Thinking: "high"}, invocationOptions{})
		eq(t, r.hasOverride(), false)
	})
	tw(t, f, "records nothing when only one side named a value", func(t *testing.T) {
		// Config-only is the agent's own default, not an override; param-only won outright.
		eq(t, resolveInvocationConfig(invConfig(func(c *agentConfig) { c.Model, c.Thinking = "provider/config-model", "low" }), invocationParams{}, invocationOptions{}).hasOverride(), false)
		eq(t, resolveInvocationConfig(invConfig(), invocationParams{Model: "provider/param-model", Thinking: "max"}, invocationOptions{}).hasOverride(), false)
	})
	tw(t, f, "records each field independently", func(t *testing.T) {
		r := resolveInvocationConfig(invConfig(func(c *agentConfig) { c.Thinking = "low" }), invocationParams{Model: "provider/param-model", Thinking: "max"}, invocationOptions{})
		eq(t, [2]string{r.overriddenThinking, r.overriddenModel}, [2]string{"max", ""})
	})
}
