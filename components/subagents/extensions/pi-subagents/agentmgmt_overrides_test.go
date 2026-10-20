package pi_subagents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Twins of test/unit/agent-overrides.test.ts.

type m = map[string]any
type l = []any

func sub(v m) m { return m{"subagents": v} }

func ptrEq(t *testing.T, got *bool, want *bool, what string) {
	t.Helper()
	if (got == nil) != (want == nil) || (got != nil && *got != *want) {
		t.Errorf("%s: got %v, want %v", what, got, want)
	}
}

func TestAgentOverrides(t *testing.T) {
	const f = "agent-overrides"
	tw(t, f, "bundled builtin agents inherit the default model", func(t *testing.T) {
		e := newAmEnv(t)
		builtins := dAll(t, e.project).Builtin
		if len(builtins) == 0 {
			t.Fatal("no builtins")
		}
		for _, a := range builtins {
			if a.Model != "" {
				t.Errorf("%s has model %q", a.Name, a.Model)
			}
		}
	})
	tw(t, f, "applies subagents.defaultModel to builtin agents with explicit overrides winning", func(t *testing.T) {
		e := newAmEnv(t)
		wj(t, e.userSettings(), sub(m{"defaultModel": "deepseek-v4-flash", "agentOverrides": m{"oracle": m{"model": "deepseek-v4-pro"}, "reviewer": m{"model": false}}}))
		b := dAll(t, e.project).Builtin
		scout := findAgent(b, "scout")
		eq(t, scout.Model, "deepseek-v4-flash", "scout model")
		eq(t, scout.ModelSource.Type, "subagents.defaultModel", "type")
		eq(t, scout.ModelSource.Scope, "user", "scope")
		eq(t, findAgent(b, "worker").Model, "deepseek-v4-flash", "worker")
		oracle := findAgent(b, "oracle")
		eq(t, oracle.Model, "deepseek-v4-pro", "oracle")
		eq(t, oracle.ModelSource == nil, true, "oracle source")
		reviewer := findAgent(b, "reviewer")
		eq(t, reviewer.Model, "", "reviewer model")
		eq(t, reviewer.ModelSource == nil, true, "reviewer source")
	})
	tw(t, f, "applies machine placement overrides with project beating user and false clearing a pin", func(t *testing.T) {
		e := newAmEnv(t)
		wj(t, e.userSettings(), sub(m{"agentOverrides": m{"claude-code": m{"machine": "workmac"}, "codex-exec": m{"machine": "workmac"}, "cursor-agent": m{"machine": "workmac"}}}))
		wj(t, e.projectSettings(), sub(m{"agentOverrides": m{"codex-exec": m{"machine": "gpu-box"}, "cursor-agent": m{"machine": false}}}))
		b := dAll(t, e.project).Builtin
		eq(t, findAgent(b, "claude-code").Machine, "workmac", "claude-code")
		eq(t, findAgent(b, "codex-exec").Machine, "gpu-box", "codex-exec")
		cursor := findAgent(b, "cursor-agent")
		eq(t, cursor.Machine, "", "cursor-agent")
		eq(t, cursor.Override.Fields, []string{"machine"}, "fields")
		// The disable/reset rewrite keeps a placement: the override is rebuilt from the agent's current fields.
		claude := findAgent(b, "claude-code")
		got := buildBuiltinOverrideConfig(claude.Override.Base, *claude)
		eq(t, marshalJSON(got, ""), `{"machine":"workmac"}`, "rebuilt override")
		base := claude.Override.Base
		base.Machine = "pinned"
		c2 := *claude
		c2.Machine = ""
		eq(t, marshalJSON(buildBuiltinOverrideConfig(base, c2), ""), `{"machine":false}`, "cleared pin")
	})
	tw(t, f, "rejects malformed machine overrides", func(t *testing.T) {
		e := newAmEnv(t)
		wj(t, e.userSettings(), sub(m{"agentOverrides": m{"claude-code": m{"machine": 7.0}}}))
		_, err := discoverAgentsAll(e.project, "", DiscoveryOptions{})
		errHas(t, err, "field 'machine' must be a non-empty string or false")
	})
	tw(t, f, "replaces and clears custom-agent allowedAgents while preserving explicit deny-all", func(t *testing.T) {
		e := newAmEnv(t)
		e.projectAgent(t, "coordinator", "---\nname: coordinator\ndescription: Coordinator\nallowedAgents: scout\n---\n\nCoordinate.\n")
		wj(t, e.userSettings(), sub(m{"agentOverrides": m{"coordinator": m{"allowedAgents": false}}}))
		eq(t, findAgent(dAgents(t, e.project, ScopeBoth, "").Agents, "coordinator").AllowedAgents == nil, true, "cleared")
		wj(t, e.projectSettings(), sub(m{"agentOverrides": m{"coordinator": m{"allowedAgents": l{}}}}))
		eq(t, findAgent(dAgents(t, e.project, ScopeBoth, "").Agents, "coordinator").AllowedAgents, []string{}, "deny all")
		wj(t, e.projectSettings(), sub(m{"agentOverrides": m{"coordinator": m{"allowedAgents": l{"worker", "reviewer"}}}}))
		eq(t, findAgent(dAgents(t, e.project, ScopeBoth, "").Agents, "coordinator").AllowedAgents, []string{"reviewer", "worker"}, "sorted")
	})
	tw(t, f, "rejects removed fallbackModels in user agent overrides", func(t *testing.T) {
		e := newAmEnv(t)
		wj(t, e.userSettings(), sub(m{"agentOverrides": m{"worker": m{"fallbackModels": l{"model/backup"}}}}))
		_, err := discoverAgentsAll(e.project, "", DiscoveryOptions{})
		errHas(t, err, "removed field 'fallbackModels'; configure one model instead")
	})
	tw(t, f, "rejects removed fallbackModels in project agent overrides", func(t *testing.T) {
		e := newAmEnv(t)
		wj(t, e.projectSettings(), sub(m{"agentOverrides": m{"worker": m{"fallbackModels": l{"model/backup"}}}}))
		_, err := discoverAgentsAll(e.project, "", DiscoveryOptions{})
		errHas(t, err, "removed field 'fallbackModels'; configure one model instead")
	})
	tw(t, f, "lets a builtin agent inherit Pi's normal tools from an override", func(t *testing.T) {
		e := newAmEnv(t)
		wj(t, e.userSettings(), sub(m{"agentOverrides": m{"researcher": m{"tools": "inherit"}}}))
		r := findAgent(dAgents(t, e.project, ScopeBoth, "").Agents, "researcher")
		eq(t, r.Tools == nil, true, "tools")
		eq(t, r.McpDirectTools == nil, true, "mcp")
	})
	tw(t, f, "keeps strict builtin tools unless a role opts into inheritance", func(t *testing.T) {
		e := newAmEnv(t)
		wj(t, e.userSettings(), sub(m{"agentOverrides": m{"researcher": m{"tools": "inherit"}}}))
		b := dAll(t, e.project).Builtin
		eq(t, findAgent(b, "researcher").Tools == nil, true, "researcher")
		eq(t, findAgent(b, "reviewer").Tools, []string{"read", "grep", "find", "ls", "watchdog_diff", "contact_supervisor"}, "reviewer")
		eq(t, findAgent(b, "worker").Tools, []string{"read", "grep", "find", "ls", "bash", "edit", "write", "contact_supervisor"}, "worker")
		eq(t, findAgent(b, "scout").Tools, []string{"read", "grep", "find", "ls", "bash", "write", "contact_supervisor"}, "scout")
	})
	tw(t, f, "keeps explicit empty builtin tool allowlists distinct from inherited tools", func(t *testing.T) {
		e := newAmEnv(t)
		wj(t, e.userSettings(), sub(m{"agentOverrides": m{"researcher": m{"tools": l{}}}}))
		r := findAgent(dAgents(t, e.project, ScopeBoth, "").Agents, "researcher")
		eq(t, r.Tools, []string{}, "tools")
		eq(t, r.McpDirectTools == nil, true, "mcp")
	})
	tw(t, f, "applies excludeTools settings overrides to builtin agents", func(t *testing.T) {
		e := newAmEnv(t)
		wj(t, e.userSettings(), sub(m{"agentOverrides": m{"researcher": m{"excludeTools": l{"write", "unknown_tool"}}}}))
		r := findAgent(dAgents(t, e.project, ScopeBoth, "").Agents, "researcher")
		eq(t, r.ExcludeTools, []string{"write", "unknown_tool"}, "excludeTools")
	})
	tw(t, f, "surfaces invalid string tool override settings", func(t *testing.T) {
		e := newAmEnv(t)
		wj(t, e.userSettings(), sub(m{"agentOverrides": m{"researcher": m{"tools": "read"}}}))
		errHas(t, discErr(e.project, ScopeBoth), e.userSettings(), "researcher", "tools")
	})
	tw(t, f, "clears subagents.defaultModel provenance for same-value agent model overrides", func(t *testing.T) {
		e := newAmEnv(t)
		wj(t, e.userSettings(), sub(m{"defaultModel": "deepseek-v4-flash", "agentOverrides": m{"worker": m{"model": "deepseek-v4-flash"}}}))
		w := findAgent(dAll(t, e.project).Builtin, "worker")
		eq(t, w.Model, "deepseek-v4-flash", "model")
		eq(t, w.ModelSource == nil, true, "source")
	})
	tw(t, f, "prefers project subagents.defaultModel over user defaultModel", func(t *testing.T) {
		e := newAmEnv(t)
		e.mkProjectPi(t)
		wj(t, e.userSettings(), sub(m{"defaultModel": "deepseek-v4-flash"}))
		wj(t, e.projectSettings(), sub(m{"defaultModel": "deepseek-v4-pro"}))
		eq(t, findAgent(dAgents(t, e.project, ScopeBoth, "").Agents, "worker").Model, "deepseek-v4-pro", "model")
	})
	tw(t, f, "applies default providers with per-agent overrides", func(t *testing.T) {
		e := newAmEnv(t)
		e.mkProjectPi(t)
		wj(t, e.userSettings(), sub(m{"defaultModel": "llama-3", "defaultProvider": "gpu-a", "agentOverrides": m{"worker": m{"defaultProvider": "gpu-b"}, "reviewer": m{"defaultProvider": false}}}))
		wj(t, e.projectSettings(), sub(m{"defaultProvider": "gpu-project"}))
		b := dAll(t, e.project).Builtin
		scout := findAgent(b, "scout")
		eq(t, scout.Model, "llama-3", "model")
		eq(t, scout.ModelProvider, "gpu-project", "provider")
		eq(t, scout.ModelSource.DefaultProvider, "gpu-project", "source provider")
		eq(t, findAgent(b, "worker").ModelProvider, "gpu-b", "worker")
		eq(t, findAgent(b, "reviewer").ModelProvider, "", "reviewer")
	})
	tw(t, f, "applies default providers to custom agent frontmatter models", func(t *testing.T) {
		e := newAmEnv(t)
		wj(t, e.userSettings(), sub(m{"defaultProvider": "gpu-a"}))
		e.projectAgent(t, "worker", "---\nname: worker\ndescription: Project worker\nmodel: llama-3\n---\n\nWork.\n")
		w := findAgent(dAgents(t, e.project, ScopeBoth, "").Agents, "worker")
		eq(t, w.Model, "llama-3", "model")
		eq(t, w.ModelProvider, "gpu-a", "provider")
	})
	tw(t, f, "applies subagents.defaultThinking only when thinking is unset", func(t *testing.T) {
		e := newAmEnv(t)
		wj(t, e.userSettings(), sub(m{"defaultThinking": " low ", "agentOverrides": m{"delegate": m{"thinking": "xhigh"}}}))
		e.userAgent(t, "user-default", "---\nname: user-default\ndescription: User agent\n---\n\nUse the default.\n")
		e.projectAgent(t, "project-default", "---\nname: project-default\ndescription: Project agent\n---\n\nUse the default.\n")
		e.projectAgent(t, "explicit-off", "---\nname: explicit-off\ndescription: Explicitly disabled\nthinking: false\n---\n\nStay off.\n")
		d := dAll(t, e.project)
		eq(t, findAgent(d.Builtin, "delegate").Thinking, any("xhigh"), "delegate")
		eq(t, findAgent(d.Builtin, "reviewer").Thinking, any("high"), "reviewer")
		eq(t, findAgent(d.User, "user-default").Thinking, any("low"), "user")
		eq(t, findAgent(d.Project, "project-default").Thinking, any("low"), "project")
		eq(t, findAgent(d.Project, "explicit-off").Thinking, any(false), "explicit off")
	})
	tw(t, f, "prefers project subagents.defaultThinking over user defaultThinking", func(t *testing.T) {
		e := newAmEnv(t)
		e.mkProjectPi(t)
		wj(t, e.userSettings(), sub(m{"defaultThinking": "low"}))
		wj(t, e.projectSettings(), sub(m{"defaultThinking": "high"}))
		eq(t, findAgent(dAgents(t, e.project, ScopeBoth, "").Agents, "delegate").Thinking, any("high"), "delegate")
	})
	tw(t, f, "preserves custom-agent thinking when disableThinking clears builtin defaults", func(t *testing.T) {
		e := newAmEnv(t)
		wj(t, e.userSettings(), sub(m{"defaultThinking": "low", "disableThinking": true}))
		e.projectAgent(t, "custom-default", "---\nname: custom-default\ndescription: Custom default\n---\n\nUse the default.\n")
		e.projectAgent(t, "custom-explicit", "---\nname: custom-explicit\ndescription: Custom explicit\nthinking: high\n---\n\nUse the explicit level.\n")
		d := dAll(t, e.project)
		eq(t, findAgent(d.Builtin, "reviewer").Thinking, nil, "reviewer")
		eq(t, findAgent(d.Project, "custom-default").Thinking, any("low"), "default")
		eq(t, findAgent(d.Project, "custom-explicit").Thinking, any("high"), "explicit")
	})
	tw(t, f, "surfaces malformed defaultThinking settings", func(t *testing.T) {
		e := newAmEnv(t)
		for _, v := range []any{"", 42.0} {
			wj(t, e.userSettings(), sub(m{"defaultThinking": v}))
			errHas(t, discErr(e.project, ScopeBoth), e.userSettings(), "defaultThinking")
		}
	})
	tw(t, f, "surfaces malformed default provider settings", func(t *testing.T) {
		e := newAmEnv(t)
		for _, v := range []any{"", 42.0} {
			wj(t, e.userSettings(), sub(m{"defaultProvider": v}))
			errHas(t, discErr(e.project, ScopeBoth), e.userSettings(), "defaultProvider")
		}
		wj(t, e.userSettings(), sub(m{"agentOverrides": m{"worker": m{"defaultProvider": ""}}}))
		errHas(t, discErr(e.project, ScopeBoth), e.userSettings(), "worker", "defaultProvider")
	})
	tw(t, f, "applies subagents.defaultModel to custom agents without a frontmatter model", func(t *testing.T) {
		e := newAmEnv(t)
		wj(t, e.userSettings(), sub(m{"defaultModel": "deepseek-v4-flash", "agentOverrides": m{"implementer": m{"model": "deepseek-v4-pro"}}}))
		e.projectAgent(t, "implementer", "---\nname: implementer\ndescription: TDD implementer\n---\n\nDrive the failing test first.\n")
		e.projectAgent(t, "auditor", "---\nname: auditor\ndescription: Audit code\nmodel: google/gemini-3-pro\n---\n\nAudit the code.\n")
		e.projectAgent(t, "scout-copy", "---\nname: scout-copy\ndescription: Scout code\n---\n\nScout the code.\n")
		a := dAgents(t, e.project, ScopeBoth, "").Agents
		eq(t, findAgent(a, "implementer").Model, "deepseek-v4-pro", "implementer")
		eq(t, findAgent(a, "auditor").Model, "google/gemini-3-pro", "auditor")
		eq(t, findAgent(a, "scout-copy").Model, "deepseek-v4-flash", "scout-copy")
	})
	tw(t, f, "overrides builtin and custom agent descriptions from settings", func(t *testing.T) {
		e := newAmEnv(t)
		wj(t, e.userSettings(), sub(m{"agentOverrides": m{"reviewer": m{"description": " Priced reviewer "}, "implementer": m{"description": "Priced implementer"}}}))
		e.projectAgent(t, "implementer", "---\nname: implementer\ndescription: Original implementer\n---\n\nImplement it.\n")
		a := dAgents(t, e.project, ScopeBoth, "").Agents
		eq(t, findAgent(a, "reviewer").Description, "Priced reviewer", "reviewer")
		eq(t, findAgent(a, "implementer").Description, "Priced implementer", "implementer")
	})
	tw(t, f, "applies user settings overrides to builtin agents", func(t *testing.T) {
		e := newAmEnv(t)
		wj(t, e.userSettings(), sub(m{"agentOverrides": m{"reviewer": m{
			"model": "openai/gpt-5.4", "fast": true, "thinking": "xhigh", "systemPromptMode": "replace", "inheritProjectContext": true,
			"inheritSkills": true, "allowNestedSubagents": true, "acceptanceRole": "writer",
			"subagentOnlyExtensions": l{"./tools/child-review.ts"}, "mutationTools": l{"replace", "undo_last_replace"}}}}))
		r := findAgent(dAgents(t, e.project, ScopeBoth, "").Agents, "reviewer")
		eq(t, r.Source, SourceBuiltin, "source")
		eq(t, r.Model, "openai/gpt-5.4", "model")
		ptrEq(t, r.Fast, new(true), "fast")
		eq(t, r.Thinking, any("xhigh"), "thinking")
		eq(t, r.SystemPromptMode, "replace", "mode")
		eq(t, r.InheritProjectContext, true, "ipc")
		eq(t, r.InheritSkills, true, "skills")
		ptrEq(t, r.AllowNestedSubagents, new(true), "nested")
		eq(t, r.AcceptanceRole, "writer", "role")
		eq(t, r.SubagentOnlyExtensions, []string{"./tools/child-review.ts"}, "ext")
		eq(t, r.MutationTools, []string{"replace", "undo_last_replace"}, "mutation")
		eq(t, r.Override.Scope, "user", "scope")
		eq(t, r.Override.Path, e.userSettings(), "path")
	})
	tw(t, f, "globally disables builtin thinking suffix defaults from user settings", func(t *testing.T) {
		e := newAmEnv(t)
		wj(t, e.userSettings(), sub(m{"disableThinking": true}))
		b := dAll(t, e.project).Builtin
		if findAgent(b, "reviewer") == nil {
			t.Fatal("no reviewer")
		}
		for _, a := range b {
			if a.Thinking != nil {
				t.Errorf("%s keeps thinking %v", a.Name, a.Thinking)
			}
		}
		eq(t, findAgent(b, "reviewer").Override.Path, e.userSettings(), "path")
	})
	tw(t, f, "lets an explicit same-scope thinking override opt back in when global thinking is disabled", func(t *testing.T) {
		e := newAmEnv(t)
		wj(t, e.userSettings(), sub(m{"disableThinking": true, "agentOverrides": m{"reviewer": m{"thinking": "high"}}}))
		a := dAgents(t, e.project, ScopeBoth, "").Agents
		eq(t, findAgent(a, "reviewer").Thinking, any("high"), "reviewer")
		eq(t, findAgent(a, "worker").Thinking, nil, "worker")
	})
	tw(t, f, "lets project settings disable builtin thinking even when user overrides request it", func(t *testing.T) {
		e := newAmEnv(t)
		e.mkProjectPi(t)
		wj(t, e.userSettings(), sub(m{"agentOverrides": m{"reviewer": m{"thinking": "xhigh"}}}))
		wj(t, e.projectSettings(), sub(m{"disableThinking": true}))
		eq(t, findAgent(dAgents(t, e.project, ScopeBoth, "").Agents, "reviewer").Thinking, nil, "reviewer")
	})
	tw(t, f, "surfaces malformed subagent default model settings", func(t *testing.T) {
		e := newAmEnv(t)
		wj(t, e.userSettings(), sub(m{"defaultModel": ""}))
		errHas(t, discErr(e.project, ScopeBoth), e.userSettings(), "defaultModel")
	})
	tw(t, f, "surfaces malformed global thinking settings", func(t *testing.T) {
		e := newAmEnv(t)
		wj(t, e.userSettings(), sub(m{"disableThinking": "yes"}))
		errHas(t, discErr(e.project, ScopeBoth), e.userSettings(), "disableThinking")
	})
	tw(t, f, "prefers project settings overrides over user settings overrides", func(t *testing.T) {
		e := newAmEnv(t)
		e.mkProjectPi(t)
		wj(t, e.userSettings(), sub(m{"agentOverrides": m{"reviewer": m{"model": "openai/gpt-5.4"}}}))
		wj(t, e.projectSettings(), sub(m{"agentOverrides": m{"reviewer": m{"model": "openai-codex/gpt-5.4-mini", "thinking": "high"}}}))
		r := findAgent(dAgents(t, e.project, ScopeBoth, "").Agents, "reviewer")
		eq(t, r.Model, "openai-codex/gpt-5.4-mini", "model")
		eq(t, r.Thinking, any("high"), "thinking")
		eq(t, r.Override.Scope, "project", "scope")
		eq(t, r.Override.Path, e.projectSettings(), "path")
	})
	tw(t, f, "layers active-provider overrides over default agentOverrides", func(t *testing.T) {
		e := newAmEnv(t)
		wj(t, e.userSettings(), sub(m{
			"agentOverrides": m{"worker": m{"model": "openai/gpt-5-mini", "thinking": "low"}},
			"agentOverridesByProvider": m{
				"github-copilot": m{"worker": m{"model": "github-copilot/gpt-5-mini", "thinking": "high"}},
				"openrouter":     m{"worker": m{"model": "openrouter/openai/gpt-5-mini"}},
			}}))
		def := findAgent(dAgents(t, e.project, ScopeBoth, "").Agents, "worker")
		cop := findAgent(dAgents(t, e.project, ScopeBoth, "github-copilot").Agents, "worker")
		or := findAgent(dAgents(t, e.project, ScopeBoth, "openrouter").Agents, "worker")
		eq(t, def.Model, "openai/gpt-5-mini", "default model")
		eq(t, def.Thinking, any("low"), "default thinking")
		eq(t, cop.Model, "github-copilot/gpt-5-mini", "copilot model")
		eq(t, cop.Thinking, any("high"), "copilot thinking")
		eq(t, or.Model, "openrouter/openai/gpt-5-mini", "openrouter model")
		eq(t, or.Thinking, any("low"), "openrouter thinking")
	})
	tw(t, f, "keeps project precedence when provider-specific overrides are active", func(t *testing.T) {
		e := newAmEnv(t)
		wj(t, e.userSettings(), sub(m{"agentOverridesByProvider": m{"openrouter": m{"worker": m{"model": "openrouter/user-model", "thinking": "low"}}}}))
		wj(t, e.projectSettings(), sub(m{"agentOverrides": m{"worker": m{"thinking": "medium"}}, "agentOverridesByProvider": m{"openrouter": m{"worker": m{"model": "openrouter/project-model", "thinking": "high"}}}}))
		w := findAgent(dAgents(t, e.project, ScopeBoth, "openrouter").Agents, "worker")
		eq(t, w.Model, "openrouter/project-model", "model")
		eq(t, w.Thinking, any("high"), "thinking")
		eq(t, w.Override.Scope, "project", "scope")
	})
	tw(t, f, "keeps provider overrides unambiguous for agents named like override fields", func(t *testing.T) {
		e := newAmEnv(t)
		wj(t, e.userSettings(), sub(m{"agentOverridesByProvider": m{"openrouter": m{"model": m{"model": "openrouter/special-model"}, "tools": m{"thinking": "high"}}}}))
		for _, n := range []string{"model", "tools"} {
			e.userAgent(t, n, "---\nname: "+n+"\ndescription: "+n+"\n---\n")
		}
		a := dAgents(t, e.project, ScopeBoth, "openrouter").Agents
		eq(t, findAgent(a, "model").Model, "openrouter/special-model", "model agent")
		eq(t, findAgent(a, "tools").Thinking, any("high"), "tools agent")
	})
	tw(t, f, "layers a project override on top of a user override for a custom agent instead of discarding it", func(t *testing.T) {
		e := newAmEnv(t)
		wj(t, e.userSettings(), sub(m{"agentOverrides": m{"persona-reviewer": m{"model": "anthropic/claude-opus-4-8", "thinking": "high"}}}))
		e.mkProjectPi(t)
		wj(t, e.projectSettings(), sub(m{"agentOverrides": m{"persona-reviewer": m{"subagentOnlyExtensions": l{"./tools/child-only.ts"}}}}))
		e.userAgent(t, "persona-reviewer", "---\nname: persona-reviewer\ndescription: Shared review persona\n---\n\nReview the diff.\n")
		r := findAgent(dAgents(t, e.project, ScopeBoth, "").Agents, "persona-reviewer")
		eq(t, r.Model, "anthropic/claude-opus-4-8", "model")
		eq(t, r.Thinking, any("high"), "thinking")
		eq(t, r.SubagentOnlyExtensions, []string{"./tools/child-only.ts"}, "ext")
		eq(t, r.Override.Scope, "project", "scope")
	})
	tw(t, f, "lets a project override flip `disabled` on a custom agent even after a user override already set it", func(t *testing.T) {
		e := newAmEnv(t)
		wj(t, e.userSettings(), sub(m{"agentOverrides": m{"disable-flip-a": m{"disabled": true}, "disable-flip-b": m{"disabled": false}}}))
		e.mkProjectPi(t)
		wj(t, e.projectSettings(), sub(m{"agentOverrides": m{"disable-flip-a": m{"disabled": false}, "disable-flip-b": m{"disabled": true}}}))
		e.userAgent(t, "disable-flip-a", "---\nname: disable-flip-a\ndescription: User disables, project re-enables\n---\n\nBody.\n")
		e.userAgent(t, "disable-flip-b", "---\nname: disable-flip-b\ndescription: User enables, project disables\n---\n\nBody.\n")
		a := dAgents(t, e.project, ScopeBoth, "").Agents
		eq(t, findAgent(a, "disable-flip-a") != nil, true, "project override (disabled: false) must win over user override (disabled: true)")
		eq(t, findAgent(a, "disable-flip-b") == nil, true, "project override (disabled: true) must win over user override (disabled: false)")
	})
	tw(t, f, "applies acceptance role precedence and false clearing to builtin and custom agents", func(t *testing.T) {
		e := newAmEnv(t)
		wj(t, e.userSettings(), sub(m{"agentOverrides": m{"reviewer": m{"acceptanceRole": "read-only"}, "scout": m{"acceptanceRole": "read-only"}, "implementer": m{"acceptanceRole": "read-only"}}}))
		e.mkProjectPi(t)
		wj(t, e.projectSettings(), sub(m{"agentOverrides": m{"reviewer": m{"acceptanceRole": "writer"}, "scout": m{"acceptanceRole": false}, "implementer": m{"acceptanceRole": false}}}))
		e.projectAgent(t, "implementer", "---\nname: implementer\ndescription: TDD implementer\n---\n\nDrive the failing test first.\n")
		a := dAgents(t, e.project, ScopeBoth, "").Agents
		eq(t, findAgent(a, "reviewer").AcceptanceRole, "writer", "reviewer")
		eq(t, findAgent(a, "scout").AcceptanceRole, "", "scout")
		eq(t, findAgent(a, "implementer").AcceptanceRole, "", "implementer")
		eq(t, findAgent(a, "implementer").Override.Scope, "project", "scope")
	})
	tw(t, f, "does not apply project settings overrides when scope is user", func(t *testing.T) {
		e := newAmEnv(t)
		e.mkProjectPi(t)
		wj(t, e.userSettings(), sub(m{"agentOverrides": m{"reviewer": m{"model": "openai/gpt-5.4"}}}))
		wj(t, e.projectSettings(), sub(m{"agentOverrides": m{"reviewer": m{"model": "openai-codex/gpt-5.4-mini"}}}))
		r := findAgent(dAgents(t, e.project, ScopeUser, "").Agents, "reviewer")
		eq(t, r.Model, "openai/gpt-5.4", "model")
		eq(t, r.Override.Scope, "user", "scope")
	})
	tw(t, f, "does not apply user settings overrides when scope is project", func(t *testing.T) {
		e := newAmEnv(t)
		e.mkProjectPi(t)
		wj(t, e.userSettings(), sub(m{"agentOverrides": m{"reviewer": m{"model": "openai/gpt-5.4"}}}))
		r := findAgent(dAgents(t, e.project, ScopeProject, "").Agents, "reviewer")
		if r == nil || r.Model == "openai/gpt-5.4" {
			t.Errorf("reviewer %+v", r)
		}
		eq(t, r.Override == nil, true, "override")
	})
	tw(t, f, "does not read malformed out-of-scope settings files", func(t *testing.T) {
		e := newAmEnv(t)
		e.mkProjectPi(t)
		writeFile(t, e.userSettings(), `{"subagents":`)
		wj(t, e.projectSettings(), sub(m{"agentOverrides": m{"reviewer": m{"model": "openai-codex/gpt-5.4-mini"}}}))
		r := findAgent(dAgents(t, e.project, ScopeProject, "").Agents, "reviewer")
		eq(t, r.Model, "openai-codex/gpt-5.4-mini", "model")
		eq(t, r.Override.Scope, "project", "scope")
	})
	tw(t, f, "agentOverrides replace frontmatter for a shadowing project agent", func(t *testing.T) {
		e := newAmEnv(t)
		e.mkProjectPi(t)
		wj(t, e.projectSettings(), sub(m{"agentOverrides": m{"reviewer": m{"model": "openai/gpt-5.4"}}}))
		e.projectAgent(t, "reviewer", "---\nname: reviewer\ndescription: Project reviewer\nmodel: google/gemini-3-pro\n---\n\nUse the project reviewer.\n")
		r := findAgent(dAgents(t, e.project, ScopeBoth, "").Agents, "reviewer")
		eq(t, r.Source, SourceProject, "source")
		eq(t, r.Model, "openai/gpt-5.4", "model")
		eq(t, r.Override.Scope, "project", "scope")
	})
	tw(t, f, "applies project agentOverrides to a custom project agent", func(t *testing.T) {
		e := newAmEnv(t)
		e.mkProjectPi(t)
		wj(t, e.projectSettings(), sub(m{"agentOverrides": m{"implementer": m{
			"output": "artifacts/implementer.md", "outputMode": "file-only", "defaultReads": l{"CONTEXT.md", "docs/spec.md"},
			"model": "anthropic/claude-sonnet-4-6", "fast": true, "thinking": "high", "systemPromptMode": "append",
			"inheritProjectContext": true, "inheritSkills": true, "defaultContext": "fork", "acceptanceRole": "writer",
			"tools": l{"bash", "mcp:xcodebuild_list_sims"}, "skills": l{"tdd"}, "subagentOnlyExtensions": l{"./tools/child-review.ts"}}}}))
		e.projectAgent(t, "implementer", "---\nname: implementer\ndescription: TDD implementer\n---\n\nDrive the failing test first.\n")
		i := findAgent(dAgents(t, e.project, ScopeBoth, "").Agents, "implementer")
		eq(t, i.Source, SourceProject, "source")
		eq(t, i.Output, "artifacts/implementer.md", "output")
		eq(t, i.OutputMode, "file-only", "outputMode")
		eq(t, i.DefaultReads, []string{"CONTEXT.md", "docs/spec.md"}, "reads")
		eq(t, i.Model, "anthropic/claude-sonnet-4-6", "model")
		ptrEq(t, i.Fast, new(true), "fast")
		eq(t, i.Thinking, any("high"), "thinking")
		eq(t, i.SystemPromptMode, "append", "mode")
		eq(t, i.InheritProjectContext, true, "ipc")
		eq(t, i.InheritSkills, true, "skills")
		eq(t, i.DefaultContext, "fork", "context")
		eq(t, i.AcceptanceRole, "writer", "role")
		eq(t, i.Tools, []string{"bash"}, "tools")
		eq(t, i.McpDirectTools, []string{"xcodebuild_list_sims"}, "mcp")
		eq(t, i.Skills, []string{"tdd"}, "skills list")
		eq(t, i.SubagentOnlyExtensions, []string{"./tools/child-review.ts"}, "ext")
		eq(t, i.Override.Scope, "project", "scope")
		eq(t, i.Override.Path, e.projectSettings(), "path")
	})
	tw(t, f, "applies user agentOverrides to a custom user agent", func(t *testing.T) {
		e := newAmEnv(t)
		wj(t, e.userSettings(), sub(m{"agentOverrides": m{"implementer": m{"model": "anthropic/claude-sonnet-4-6"}}}))
		e.userAgent(t, "implementer", "---\nname: implementer\ndescription: TDD implementer\n---\n\nDrive the failing test first.\n")
		i := findAgent(dAgents(t, e.project, ScopeBoth, "").Agents, "implementer")
		eq(t, i.Source, SourceUser, "source")
		eq(t, i.Model, "anthropic/claude-sonnet-4-6", "model")
		eq(t, i.Override.Scope, "user", "scope")
	})
	tw(t, f, "applies user agentOverrides to a custom project agent when project settings have no entry", func(t *testing.T) {
		e := newAmEnv(t)
		wj(t, e.userSettings(), sub(m{"agentOverrides": m{"implementer": m{"model": "anthropic/claude-sonnet-4-6"}}}))
		e.projectAgent(t, "implementer", "---\nname: implementer\ndescription: TDD implementer\n---\n\nDrive the failing test first.\n")
		i := findAgent(dAgents(t, e.project, ScopeBoth, "").Agents, "implementer")
		eq(t, i.Source, SourceProject, "source")
		eq(t, i.Model, "anthropic/claude-sonnet-4-6", "model")
		eq(t, i.Override.Scope, "user", "scope")
	})
	tw(t, f, "prefers project agentOverrides over user agentOverrides on a custom project agent", func(t *testing.T) {
		e := newAmEnv(t)
		e.mkProjectPi(t)
		wj(t, e.userSettings(), sub(m{"agentOverrides": m{"implementer": m{"model": "anthropic/claude-sonnet-4-6", "output": "user.md", "defaultReads": l{"user.md"}}}}))
		wj(t, e.projectSettings(), sub(m{"agentOverrides": m{"implementer": m{"model": "openai/gpt-5.4", "output": "project.md", "defaultReads": l{"project.md"}}}}))
		e.projectAgent(t, "implementer", "---\nname: implementer\ndescription: TDD implementer\n---\n\nDrive the failing test first.\n")
		i := findAgent(dAgents(t, e.project, ScopeBoth, "").Agents, "implementer")
		eq(t, i.Model, "openai/gpt-5.4", "model")
		eq(t, i.Output, "project.md", "output")
		eq(t, i.DefaultReads, []string{"project.md"}, "reads")
		eq(t, i.Override.Scope, "project", "scope")
	})
	tw(t, f, "agentOverrides replace explicit custom frontmatter fields", func(t *testing.T) {
		e := newAmEnv(t)
		e.mkProjectPi(t)
		wj(t, e.projectSettings(), sub(m{"agentOverrides": m{"implementer": m{
			"output": "artifacts/override.md", "outputMode": "file-only", "defaultReads": l{"override.md"}, "model": "anthropic/claude-sonnet-4-6",
			"fast": true, "thinking": "high", "tools": l{"bash"}, "skills": l{"override-skill"}, "inheritProjectContext": true,
			"defaultContext": "fork", "acceptanceRole": "writer", "systemPrompt": "Override prompt"}}}))
		e.projectAgent(t, "implementer", "---\nname: implementer\ndescription: TDD implementer\noutput: artifacts/explicit.md\noutputMode: inline\ndefaultReads: explicit.md\nmodel: google/gemini-3-pro\nfast: false\nthinking: medium\ntools: read, mcp:local_tool\nskills: agent-skill\ninheritProjectContext: false\ndefaultContext: fresh\nacceptanceRole: read-only\n---\n\nDrive the failing test first.\n")
		i := findAgent(dAgents(t, e.project, ScopeBoth, "").Agents, "implementer")
		eq(t, i.Output, "artifacts/override.md", "output")
		eq(t, i.OutputMode, "file-only", "outputMode")
		eq(t, i.DefaultReads, []string{"override.md"}, "reads")
		eq(t, i.Model, "anthropic/claude-sonnet-4-6", "model")
		ptrEq(t, i.Fast, new(true), "fast")
		eq(t, i.Thinking, any("high"), "thinking")
		eq(t, i.Tools, []string{"bash"}, "tools")
		eq(t, i.McpDirectTools == nil, true, "mcp")
		eq(t, i.Skills, []string{"override-skill"}, "skills")
		eq(t, i.InheritProjectContext, true, "ipc")
		eq(t, i.DefaultContext, "fork", "context")
		eq(t, i.AcceptanceRole, "writer", "role")
		eq(t, i.SystemPrompt, "Override prompt", "prompt")
		eq(t, i.Override.Scope, "project", "scope")
	})
	tw(t, f, "lets false overrides clear explicit output and defaultReads frontmatter", func(t *testing.T) {
		e := newAmEnv(t)
		e.mkProjectPi(t)
		wj(t, e.projectSettings(), sub(m{"agentOverrides": m{"implementer": m{"output": false, "defaultReads": false}}}))
		e.projectAgent(t, "implementer", "---\nname: implementer\ndescription: TDD implementer\noutput: explicit.md\ndefaultReads: explicit.md\n---\n\nDrive the failing test first.\n")
		i := findAgent(dAgents(t, e.project, ScopeBoth, "").Agents, "implementer")
		eq(t, i.Output, "", "output")
		eq(t, i.DefaultReads == nil, true, "reads")
	})
	tw(t, f, "keeps an explicit empty defaultReads override distinct from false", func(t *testing.T) {
		e := newAmEnv(t)
		wj(t, e.userSettings(), sub(m{"agentOverrides": m{"implementer": m{"defaultReads": l{}}}}))
		e.projectAgent(t, "implementer", "---\nname: implementer\ndescription: TDD implementer\n---\n\nDrive the failing test first.\n")
		i := findAgent(dAgents(t, e.project, ScopeBoth, "").Agents, "implementer")
		eq(t, i.DefaultReads, []string{}, "reads")
	})
	tw(t, f, "leaves a custom agent untouched when no agentOverrides entry matches its name", func(t *testing.T) {
		e := newAmEnv(t)
		wj(t, e.userSettings(), sub(m{"agentOverrides": m{"reviewer": m{"model": "openai/gpt-5.4"}}}))
		e.projectAgent(t, "implementer", "---\nname: implementer\ndescription: TDD implementer\n---\n\nDrive the failing test first.\n")
		i := findAgent(dAgents(t, e.project, ScopeBoth, "").Agents, "implementer")
		eq(t, i.Model, "", "model")
		eq(t, i.Override == nil, true, "override")
	})
	tw(t, f, "disableBuiltins does not disable custom agents", func(t *testing.T) {
		e := newAmEnv(t)
		wj(t, e.userSettings(), sub(m{"disableBuiltins": true}))
		e.projectAgent(t, "implementer", "---\nname: implementer\ndescription: TDD implementer\n---\n\nDrive the failing test first.\n")
		i := findAgent(dAgents(t, e.project, ScopeBoth, "").Agents, "implementer")
		if i == nil || (i.Disabled != nil && *i.Disabled) {
			t.Errorf("implementer %+v", i)
		}
	})
	tw(t, f, "does not create a settings file when removing a non-existent override", func(t *testing.T) {
		e := newAmEnv(t)
		if _, err := os.Stat(e.userSettings()); err == nil {
			t.Fatal("settings exist")
		}
		if _, _, _, err := removeBuiltinAgentOverride(e.project, "reviewer", "user", false); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(e.userSettings()); err == nil {
			t.Error("a settings file was created")
		}
	})
	tw(t, f, "does not preserve empty or whitespace machine values when clearing other override fields", func(t *testing.T) {
		e := newAmEnv(t)
		for _, machine := range []string{"", " \t "} {
			wj(t, e.userSettings(), sub(m{"agentOverrides": m{"reviewer": m{"machine": machine, "model": "openai/gpt-5.4"}}}))
			if _, _, _, err := removeBuiltinAgentOverride(e.project, "reviewer", "user", true); err != nil {
				t.Fatal(err)
			}
			b, _ := os.ReadFile(e.userSettings())
			v, _ := parseJSON(b)
			if v.(*jsObject).has("subagents") {
				t.Errorf("machine %q kept: %s", machine, b)
			}
		}
	})
	tw(t, f, "surfaces malformed settings files instead of silently ignoring them", func(t *testing.T) {
		e := newAmEnv(t)
		writeFile(t, e.userSettings(), `{"subagents":`)
		errHas(t, discErr(e.project, ScopeBoth), e.userSettings(), "Failed to parse settings file")
	})
	tw(t, f, "surfaces settings read failures without mislabeling them as parse errors", func(t *testing.T) {
		e := newAmEnv(t)
		if err := os.MkdirAll(e.userSettings(), 0o755); err != nil {
			t.Fatal(err)
		}
		errHas(t, discErr(e.project, ScopeBoth), e.userSettings(), "Failed to read settings file")
	})
	tw(t, f, "surfaces malformed builtin override entries instead of silently ignoring them", func(t *testing.T) {
		e := newAmEnv(t)
		wj(t, e.userSettings(), sub(m{"agentOverrides": m{"reviewer": m{"inheritProjectContext": "true"}}}))
		errHas(t, discErr(e.project, ScopeBoth), e.userSettings(), "reviewer", "inheritProjectContext")
	})
	tw(t, f, "surfaces malformed acceptance role override values", func(t *testing.T) {
		e := newAmEnv(t)
		wj(t, e.userSettings(), sub(m{"agentOverrides": m{"reviewer": m{"acceptanceRole": "observer"}}}))
		errHas(t, discErr(e.project, ScopeBoth), e.userSettings(), "reviewer", "acceptanceRole")
	})
	tw(t, f, "surfaces malformed description override values", func(t *testing.T) {
		e := newAmEnv(t)
		for _, v := range []any{"", 42.0} {
			wj(t, e.userSettings(), sub(m{"agentOverrides": m{"reviewer": m{"description": v}}}))
			errHas(t, discErr(e.project, ScopeBoth), e.userSettings(), "reviewer", "description")
		}
	})
	tw(t, f, "rejects unsupported outputMode override values", func(t *testing.T) {
		e := newAmEnv(t)
		for _, v := range []any{"artifact-only", false} {
			wj(t, e.userSettings(), sub(m{"agentOverrides": m{"reviewer": m{"outputMode": v}}}))
			errHas(t, discErr(e.project, ScopeBoth), e.userSettings(), "reviewer", "outputMode")
		}
	})
	tw(t, f, "applies output and defaultReads overrides to bundled and package agents and supports false clears", func(t *testing.T) {
		e := newAmEnv(t)
		root := filepath.Join(e.project, "package-agents")
		wj(t, filepath.Join(root, "package.json"), m{"pi-subagents": m{"agents": l{"agents"}}})
		writeFile(t, filepath.Join(root, "agents", "package-scout.md"), "---\nname: package-scout\ndescription: Package scout\noutput: package-frontmatter.md\ndefaultReads: PACKAGE-FRONTMATTER.md\n---\n\nScout the package.\n")
		wj(t, e.projectSettings(), m{"packages": l{root}, "subagents": m{"agentOverrides": m{"package-scout": m{"output": "package.md", "defaultReads": l{"PACKAGE.md"}}}}})
		wj(t, e.userSettings(), sub(m{"agentOverrides": m{"scout": m{"output": "research-scout-results.md", "defaultReads": l{"AGENTS.md"}}, "reviewer": m{"output": false, "defaultReads": false}}}))
		a := dAgents(t, e.project, ScopeBoth, "").Agents
		eq(t, findAgent(a, "scout").Output, "research-scout-results.md", "scout output")
		eq(t, findAgent(a, "scout").DefaultReads, []string{"AGENTS.md"}, "scout reads")
		eq(t, findAgent(a, "reviewer").Output, "", "reviewer output")
		eq(t, findAgent(a, "reviewer").DefaultReads == nil, true, "reviewer reads")
		eq(t, findAgent(a, "package-scout").Output, "package.md", "package output")
		eq(t, findAgent(a, "package-scout").DefaultReads, []string{"PACKAGE.md"}, "package reads")
	})
	tw(t, f, "surfaces malformed output and defaultReads override values", func(t *testing.T) {
		e := newAmEnv(t)
		for _, c := range []struct {
			field string
			v     any
		}{{"output", 42.0}, {"output", ""}, {"output", "  "}, {"defaultReads", l{"ok", 42.0}}} {
			wj(t, e.userSettings(), sub(m{"agentOverrides": m{"reviewer": m{c.field: c.v}}}))
			errHas(t, discErr(e.project, ScopeBoth), e.userSettings(), "reviewer", c.field)
		}
	})
	tw(t, f, "builds description changes and false sentinels when an override clears builtin fields", func(t *testing.T) {
		e := newAmEnv(t)
		base := OverrideBase{Description: "Base description", Output: "base-output.md", DefaultReads: []string{"base-read.md"}, Model: "openai-codex/gpt-5.4-mini",
			Thinking: "high", SystemPromptMode: "append", InheritProjectContext: true, InheritGlobalContext: true, InheritSkills: false,
			DefaultContext: "fork", AcceptanceRole: "read-only", SystemPrompt: "Base prompt", Skills: []string{"safe-bash"}, Tools: []string{"bash"},
			McpDirectTools: []string{"xcodebuild_list_sims"}, SubagentOnlyExtensions: []string{"./tools/base-child.ts"}}
		draft := AgentConfig{Description: "Override description", SystemPromptMode: "replace", SystemPrompt: "Base prompt"}
		override := buildBuiltinOverrideConfig(base, draft)
		want := `{"description":"Override description","output":false,"defaultReads":false,"model":false,"thinking":false,"systemPromptMode":"replace","inheritProjectContext":false,"inheritGlobalContext":false,"defaultContext":false,"acceptanceRole":false,"skills":false,"tools":false,"subagentOnlyExtensions":false}`
		eq(t, marshalJSON(override, ""), want, "override")
		e.mkProjectPi(t)
		if _, err := saveBuiltinAgentOverride(e.project, "reviewer", "project", override); err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(e.projectSettings())
		if !strings.Contains(string(b), `"output": false`) || !strings.Contains(string(b), `"defaultReads": false`) {
			t.Errorf("saved: %s", b)
		}
		eq(t, findAgent(dAgents(t, e.project, ScopeBoth, "").Agents, "reviewer").Description, "Override description", "description")
		scoutO := newObject()
		scoutO.set("output", "research.md")
		scoutO.set("defaultReads", l{"CONTEXT.md"})
		if _, err := saveBuiltinAgentOverride(e.project, "scout", "project", scoutO); err != nil {
			t.Fatal(err)
		}
		scout := findAgent(dAgents(t, e.project, ScopeBoth, "").Agents, "scout")
		eq(t, scout.Output, "research.md", "scout output")
		eq(t, scout.DefaultReads, []string{"CONTEXT.md"}, "scout reads")
		ws := buildBuiltinOverrideConfig(OverrideBase{Description: "Base description"}, AgentConfig{Description: "   "})
		eq(t, ws == nil, true, "whitespace description")
	})
}
