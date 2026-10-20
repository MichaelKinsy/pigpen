package pi_subagents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultExtensions(t *testing.T) {
	const f = "default-extensions"
	type rig struct{ home, project string }
	setup := func(t *testing.T) rig {
		home := withTempHome(t)
		t.Setenv("PI_OFFLINE", "true")
		clearAgentDiscoveryCache()
		t.Cleanup(clearAgentDiscoveryCache)
		return rig{home, tmp(t)}
	}
	userSettings := func(r rig) string { return filepath.Join(r.home, ".pi", "agent", "settings.json") }
	projSettings := func(r rig) string { return filepath.Join(r.project, ".pi", "settings.json") }
	writeAgent := func(t *testing.T, r rig, name, extra string) {
		body := "---\nname: " + name + "\ndescription: Test agent"
		if extra != "" {
			body += "\n" + extra
		}
		writeFile(t, filepath.Join(r.project, ".pi", "agents", name+".md"), body+"\n---\n\nTest agent.\n")
	}
	find := func(agents []AgentConfig, name string) *AgentConfig { return findAgent(agents, name) }
	scoutOf := func(t *testing.T, r rig) *AgentConfig {
		for _, a := range dAll(t, r.project).Builtin {
			if a.Name == "scout" {
				return &a
			}
		}
		t.Fatal("no scout")
		return nil
	}
	tw(t, f, "preserves ambient discovery when omitted and disables it when empty", func(t *testing.T) {
		r := setup(t)
		scout := scoutOf(t, r)
		eq(t, scout.Extensions == nil, true, "extensions unset")
		eq(t, scout.SubagentOnlyExtensions == nil, true, "child-only unset")
		wj(t, userSettings(r), js(t, `{"subagents":{"defaultExtensions":[]}}`))
		clearAgentDiscoveryCache()
		scout = scoutOf(t, r)
		eq(t, scout.Extensions, []string{}, "extensions")
	})
	tw(t, f, "preserves ambient loading unless extensions defaults are also configured", func(t *testing.T) {
		r := setup(t)
		wj(t, userSettings(r), js(t, `{"subagents":{"defaultSubagentOnlyExtensions":["  ./child.ts  "]}}`))
		scout := scoutOf(t, r)
		eq(t, scout.Extensions == nil, true, "extensions unset")
		eq(t, scout.SubagentOnlyExtensions, []string{"./child.ts"}, "child-only")
		plan := extensionPlanOf(t, *scout)
		eq(t, plan.DisableAmbientExtensions, false, "ambient")
		eq(t, contains(plan.ExtensionArgs, "./child.ts"), true, "child.ts in args")

		wj(t, userSettings(r), js(t, `{"subagents":{"defaultExtensions":["./allowlisted.ts"],"defaultSubagentOnlyExtensions":["./child.ts"]}}`))
		clearAgentDiscoveryCache()
		allow := extensionPlanOf(t, *scoutOf(t, r))
		eq(t, allow.DisableAmbientExtensions, true, "ambient disabled")
		eq(t, contains(allow.ExtensionArgs, "./allowlisted.ts"), true, "allowlisted")
		eq(t, contains(allow.ExtensionArgs, "./child.ts"), true, "child")
	})
	tw(t, f, "preserves an explicit child-only field and applies overrides after defaults", func(t *testing.T) {
		r := setup(t)
		wj(t, userSettings(r), js(t, `{"subagents":{"defaultSubagentOnlyExtensions":["./default.ts"],"agentOverrides":{"replaced":{"subagentOnlyExtensions":["./user-replacement.ts"]},"cleared":{"subagentOnlyExtensions":["./user-cleared.ts"]},"scout":{"subagentOnlyExtensions":[]}}}}`))
		wj(t, projSettings(r), js(t, `{"subagents":{"agentOverrides":{"replaced":{"subagentOnlyExtensions":["./replacement.ts"]},"cleared":{"subagentOnlyExtensions":false}}}}`))
		writeAgent(t, r, "empty", "subagentOnlyExtensions:")
		writeAgent(t, r, "explicit", "subagentOnlyExtensions: ./explicit.ts")
		writeAgent(t, r, "replaced", "")
		writeAgent(t, r, "cleared", "")
		agents := dAgents(t, r.project, ScopeBoth, "").Agents
		eq(t, find(agents, "empty").SubagentOnlyExtensions, []string{}, "empty")
		eq(t, find(agents, "explicit").SubagentOnlyExtensions, []string{filepath.Join(r.project, ".pi", "agents", "explicit.ts")}, "explicit")
		eq(t, find(agents, "replaced").SubagentOnlyExtensions, []string{"./replacement.ts"}, "replaced")
		eq(t, find(agents, "cleared").SubagentOnlyExtensions == nil, true, "cleared")
		eq(t, find(agents, "scout").SubagentOnlyExtensions, []string{}, "scout")
	})
	tw(t, f, "applies the allowlist only when an agent has no extensions field", func(t *testing.T) {
		r := setup(t)
		wj(t, userSettings(r), js(t, `{"subagents":{"defaultExtensions":["./shared.ts"]}}`))
		writeAgent(t, r, "inherited", "")
		writeAgent(t, r, "explicit", "extensions: ./explicit.ts")
		writeAgent(t, r, "disabled", "extensions:")
		agents := dAgents(t, r.project, ScopeBoth, "").Agents
		eq(t, find(agents, "inherited").Extensions, []string{"./shared.ts"}, "inherited")
		eq(t, find(agents, "explicit").Extensions, []string{filepath.Join(r.project, ".pi", "agents", "explicit.ts")}, "explicit")
		eq(t, find(agents, "disabled").Extensions, []string{}, "disabled")
	})
	tw(t, f, "supports per-agent extensions through agentOverrides", func(t *testing.T) {
		r := setup(t)
		wj(t, userSettings(r), js(t, `{"subagents":{"defaultExtensions":[],"agentOverrides":{"scout":{"extensions":["./scout.ts"]},"inherited":{"extensions":["./inherited.ts"]},"explicit":{"extensions":["./ignored.ts"]}}}}`))
		writeAgent(t, r, "inherited", "")
		writeAgent(t, r, "explicit", "extensions: ./frontmatter.ts")
		agents := dAgents(t, r.project, ScopeBoth, "").Agents
		eq(t, find(agents, "scout").Extensions, []string{"./scout.ts"}, "scout")
		eq(t, find(agents, "inherited").Extensions, []string{"./inherited.ts"}, "inherited")
		eq(t, find(agents, "explicit").Extensions, []string{"./ignored.ts"}, "explicit")
	})
	tw(t, f, "does not serialize settings-derived extensions during unrelated updates", func(t *testing.T) {
		r := setup(t)
		wj(t, projSettings(r), js(t, `{"subagents":{"defaultExtensions":["./default.ts"],"defaultSubagentOnlyExtensions":["./child-default.ts"],"agentOverrides":{"overridden":{"extensions":["./override.ts"]}}}}`))
		writeAgent(t, r, "defaulted", "")
		writeAgent(t, r, "overridden", "")
		writeAgent(t, r, "declared", "subagentOnlyExtensions: ./declared.ts")
		agents := dAgents(t, r.project, ScopeBoth, "").Agents
		for _, n := range []string{"defaulted", "overridden"} {
			eq(t, find(agents, n).SubagentOnlyExtensions, []string{"./child-default.ts"}, n)
		}
		eq(t, find(agents, "declared").SubagentOnlyExtensions, []string{filepath.Join(r.project, ".pi", "agents", "declared.ts")}, "declared")
		for _, n := range []string{"defaulted", "overridden", "declared"} {
			res := handleManagementAction("update", map[string]any{"agent": n, "config": obj("description", "Updated "+n)}, testMgmtCtx(r.project))
			eq(t, res.isError, false, n+": "+res.text)
			content := readFileText(t, filepath.Join(r.project, ".pi", "agents", n+".md"))
			polNotMatches(t, content, `(?m)^extensions:`)
			if n == "declared" {
				polMatches(t, content, `(?m)^subagentOnlyExtensions: \./declared\.ts$`)
			} else {
				polNotMatches(t, content, `(?m)^subagentOnlyExtensions:`)
			}
		}
	})
	tw(t, f, "persists per-agent extension overrides", func(t *testing.T) {
		base := OverrideBase{SystemPromptMode: "replace", InheritProjectContext: true, SystemPrompt: "Test agent", Extensions: []string{}}
		next := AgentConfig{SystemPromptMode: "replace", InheritProjectContext: true, SystemPrompt: "Test agent", Extensions: []string{"./agent.ts"}}
		cfg := buildBuiltinOverrideConfig(base, next)
		eq(t, jsonString(cfg), `{"extensions":["./agent.ts"]}`, "override")
	})
	tw(t, f, "uses project settings over user settings while respecting discovery scope", func(t *testing.T) {
		r := setup(t)
		wj(t, userSettings(r), js(t, `{"subagents":{"defaultExtensions":["./user.ts"],"defaultSubagentOnlyExtensions":["./user-child.ts"]}}`))
		wj(t, projSettings(r), js(t, `{"subagents":{"defaultExtensions":[]}}`))
		get := func(scope AgentScope) *AgentConfig { return find(dAgents(t, r.project, scope, "").Agents, "scout") }
		both, user, project := get(ScopeBoth), get(ScopeUser), get(ScopeProject)
		eq(t, both.Extensions, []string{}, "both ext")
		eq(t, both.SubagentOnlyExtensions, []string{"./user-child.ts"}, "both child")
		eq(t, user.Extensions, []string{"./user.ts"}, "user ext")
		eq(t, user.SubagentOnlyExtensions, []string{"./user-child.ts"}, "user child")
		eq(t, project.Extensions, []string{}, "project ext")
		eq(t, project.SubagentOnlyExtensions == nil, true, "project child")
		wj(t, projSettings(r), js(t, `{"subagents":{"defaultExtensions":[],"defaultSubagentOnlyExtensions":[]}}`))
		clearAgentDiscoveryCache()
		eq(t, get(ScopeBoth).SubagentOnlyExtensions, []string{}, "both child cleared")
	})
	tw(t, f, "rejects malformed values", func(t *testing.T) {
		r := setup(t)
		for _, name := range []string{"defaultExtensions", "defaultSubagentOnlyExtensions"} {
			for _, v := range []string{`"nope"`, `["   "]`, `[42]`, `["valid",42]`} {
				wj(t, userSettings(r), js(t, `{"subagents":{"`+name+`":`+v+`}}`))
				clearAgentDiscoveryCache()
				err := discErr(r.project, ScopeBoth)
				if err == nil || !strings.Contains(err.Error(), userSettings(r)) || !strings.Contains(err.Error(), name) {
					t.Errorf("%s=%s: error %v", name, v, err)
				}
			}
		}
	})
}

func readFileText(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// extensionPlanOf is the extension half of resolvePiLaunchToolPlan for one agent definition.
func extensionPlanOf(t *testing.T, a AgentConfig) *piLaunchToolPlan {
	t.Helper()
	plan, err := resolvePiLaunchToolPlan(piLaunchToolPlanInput{Tools: a.Tools, Extensions: a.Extensions, SubagentOnlyExtensions: a.SubagentOnlyExtensions, AgentName: a.Name, Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
