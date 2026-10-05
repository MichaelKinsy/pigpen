package pi_subagents

import "testing"

func TestResolveExecutionAgentScope(t *testing.T) {
	const f = "agent-scope"
	tw(t, f, "defaults to both when scope is omitted", func(t *testing.T) {
		eq(t, ResolveExecutionAgentScope(nil), ScopeBoth, "scope")
	})
	tw(t, f, "passes through explicit scopes", func(t *testing.T) {
		eq(t, ResolveExecutionAgentScope("user"), ScopeUser, "user")
		eq(t, ResolveExecutionAgentScope("project"), ScopeProject, "project")
		eq(t, ResolveExecutionAgentScope("both"), ScopeBoth, "both")
	})
	tw(t, f, "falls back to both for invalid scopes", func(t *testing.T) {
		eq(t, ResolveExecutionAgentScope("invalid"), ScopeBoth, "invalid")
		eq(t, ResolveExecutionAgentScope(""), ScopeBoth, "empty")
		eq(t, ResolveExecutionAgentScope(7), ScopeBoth, "number")
	})
}

func mk(name string, source AgentSource, prompt string) AgentConfig {
	return AgentConfig{Name: name, Description: name + " agent", SystemPromptMode: "replace", SystemPrompt: prompt, Source: source, FilePath: "/" + string(source) + "/" + name + ".md"}
}

func TestMergeAgentsForScope(t *testing.T) {
	const f = "agent-selection"
	one := func(n, s, p string) []AgentConfig { return []AgentConfig{mk(n, AgentSource(s), p)} }
	tw(t, f, "returns project agents when scope is project", func(t *testing.T) {
		r := MergeAgentsForScope(ScopeProject, one("shared", "user", "user prompt"), one("shared", "project", "project prompt"), nil, nil)
		eq(t, len(r), 1, "len")
		eq(t, r[0].Source, SourceProject, "source")
	})
	tw(t, f, "returns user agents when scope is user", func(t *testing.T) {
		r := MergeAgentsForScope(ScopeUser, one("shared", "user", "user prompt"), one("shared", "project", "project prompt"), nil, nil)
		eq(t, len(r), 1, "len")
		eq(t, r[0].Source, SourceUser, "source")
	})
	tw(t, f, "prefers project agents on name collisions when scope is both", func(t *testing.T) {
		r := MergeAgentsForScope(ScopeBoth, one("shared", "user", "user prompt"), one("shared", "project", "project prompt"), nil, nil)
		eq(t, len(r), 1, "len")
		eq(t, r[0].Source, SourceProject, "source")
		eq(t, r[0].SystemPrompt, "project prompt", "prompt")
	})
	tw(t, f, "keeps agents from both scopes when names are distinct", func(t *testing.T) {
		r := MergeAgentsForScope(ScopeBoth, one("user-only", "user", "user prompt"), one("project-only", "project", "project prompt"), nil, nil)
		eq(t, len(r), 2, "len")
		eq(t, []string{r[0].Name, string(r[0].Source), r[1].Name, string(r[1].Source)}, []string{"user-only", "user", "project-only", "project"}, "agents")
	})
	tw(t, f, "includes builtin agents when no user or project override exists", func(t *testing.T) {
		r := MergeAgentsForScope(ScopeBoth, nil, nil, one("scout", "builtin", "builtin prompt"), nil)
		eq(t, len(r), 1, "len")
		eq(t, r[0].Source, SourceBuiltin, "source")
	})
	tw(t, f, "user agents override builtins with the same name", func(t *testing.T) {
		r := MergeAgentsForScope(ScopeBoth, one("scout", "user", "custom prompt"), nil, one("scout", "builtin", "builtin prompt"), nil)
		eq(t, len(r), 1, "len")
		eq(t, r[0].Source, SourceUser, "source")
		eq(t, r[0].SystemPrompt, "custom prompt", "prompt")
	})
	tw(t, f, "package agents override builtins but not user or project agents", func(t *testing.T) {
		b, p := one("scout", "builtin", "builtin prompt"), one("scout", "package", "package prompt")
		u, pr := one("scout", "user", "user prompt"), one("scout", "project", "project prompt")
		eq(t, MergeAgentsForScope(ScopeBoth, nil, nil, b, p)[0].Source, SourcePackage, "both")
		eq(t, MergeAgentsForScope(ScopeUser, u, nil, b, p)[0].Source, SourceUser, "user")
		eq(t, MergeAgentsForScope(ScopeProject, nil, pr, b, p)[0].Source, SourceProject, "project")
	})
	tw(t, f, "project agents override builtins with the same name", func(t *testing.T) {
		r := MergeAgentsForScope(ScopeBoth, nil, one("scout", "project", "project prompt"), one("scout", "builtin", "builtin prompt"), nil)
		eq(t, len(r), 1, "len")
		eq(t, r[0].Source, SourceProject, "source")
	})
}
