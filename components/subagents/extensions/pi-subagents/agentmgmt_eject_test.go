package pi_subagents

import (
	"os"
	"path/filepath"
	"testing"
)

// Twins of test/unit/agent-eject-disable.test.ts.

type ejectEnv struct{ dir string }

func newEjectEnv(t *testing.T) ejectEnv {
	t.Helper()
	withTempHome(t)
	t.Setenv("PI_OFFLINE", "true")
	dir := tmp(t)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(dir, "agent-home"))
	clearSkillCache()
	clearAgentDiscoveryCache()
	t.Cleanup(func() { clearSkillCache(); clearAgentDiscoveryCache() })
	return ejectEnv{dir}
}

func (e ejectEnv) userSettings() string    { return filepath.Join(e.dir, "agent-home", "settings.json") }
func (e ejectEnv) projectSettings() string { return filepath.Join(e.dir, ".pi", "settings.json") }
func (e ejectEnv) userAgent(n string) string {
	return filepath.Join(e.dir, "agent-home", "agents", n+".md")
}
func (e ejectEnv) projectAgent(n string) string {
	return filepath.Join(e.dir, ".pi", "agents", n+".md")
}
func (e ejectEnv) packageAgent(n string) string {
	return filepath.Join(e.dir, "packaged-agents", n+".md")
}

func (e ejectEnv) writePackageAgent(t *testing.T, name string) {
	t.Helper()
	wjc(t, filepath.Join(e.dir, "package.json"), m{"name": "test-package", "pi": m{"subagents": m{"agents": l{"./packaged-agents"}}}})
	writeFile(t, e.packageAgent(name), "---\nname: "+name+"\ndescription: Packaged agent\n---\n\nPackaged.\n")
}

func (e ejectEnv) act(action string, params map[string]any) manageResult {
	return handleManagementAction(action, params, testMgmtCtx(e.dir))
}

func (e ejectEnv) agents(t *testing.T) []AgentConfig { return dAgents(t, e.dir, ScopeBoth, "").Agents }

func fileExists2(p string) bool { _, err := os.Stat(p); return err == nil }

func readJSONFileT(t *testing.T, p string) *jsObject {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	v, err := parseJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	return v.(*jsObject)
}

func overrideOf(t *testing.T, settingsPath, name string) *jsObject {
	t.Helper()
	return readJSONFileT(t, settingsPath).obj("subagents").obj("agentOverrides").obj(name)
}

func TestAgentEjectDisable(t *testing.T) {
	const f = "agent-eject-disable"
	tw(t, f, "copies a bundled builtin to user scope verbatim and shadows the builtin", func(t *testing.T) {
		e := newEjectEnv(t)
		builtin := findAgent(dAll(t, e.dir).Builtin, "reviewer")
		r := e.act("eject", map[string]any{"agent": "reviewer"})
		eq(t, r.isError, false, "error")
		hasRe(t, r.text, `Ejected agent 'reviewer' from builtin to user scope`)
		want, _ := builtinFS.ReadFile(builtin.FilePath)
		got, _ := os.ReadFile(e.userAgent("reviewer"))
		eq(t, string(got), string(want), "verbatim")
		eff := findAgent(e.agents(t), "reviewer")
		eq(t, []any{eff.Source, eff.FilePath}, []any{SourceUser, e.userAgent("reviewer")}, "effective")
	})
	tw(t, f, "ejects to project scope when agentScope is project", func(t *testing.T) {
		e := newEjectEnv(t)
		mkdirAll(t, filepath.Join(e.dir, ".pi"))
		r := e.act("eject", map[string]any{"agent": "scout", "agentScope": "project"})
		eq(t, r.isError, false, "error")
		hasRe(t, r.text, "to project scope")
		eq(t, fileExists2(e.projectAgent("scout")), true, "file")
		eq(t, findAgent(dAll(t, e.dir).Project, "scout").Source, SourceProject, "source")
	})
	tw(t, f, "copies a package agent that shadows a builtin by runtime precedence", func(t *testing.T) {
		e := newEjectEnv(t)
		e.writePackageAgent(t, "reviewer")
		eq(t, findAgent(e.agents(t), "reviewer").Source, SourcePackage, "package")
		r := e.act("eject", map[string]any{"agent": "reviewer"})
		eq(t, r.isError, false, "error")
		hasRe(t, r.text, "from package to user scope")
		a, _ := os.ReadFile(e.userAgent("reviewer"))
		b, _ := os.ReadFile(e.packageAgent("reviewer"))
		eq(t, string(a), string(b), "copy")
	})
	tw(t, f, "refuses invalid management scopes without writing user files", func(t *testing.T) {
		e := newEjectEnv(t)
		r := e.act("eject", map[string]any{"agent": "reviewer", "agentScope": "workspace"})
		eq(t, r.isError, true, "error")
		hasRe(t, r.text, "agentScope must be 'user' or 'project'")
		eq(t, fileExists2(e.userAgent("reviewer")), false, "file")
	})
	tw(t, f, "refuses to eject when a custom agent already exists", func(t *testing.T) {
		e := newEjectEnv(t)
		writeFile(t, e.userAgent("reviewer"), "---\nname: reviewer\ndescription: Mine\n---\n\nMine.\n")
		r := e.act("eject", map[string]any{"agent": "reviewer"})
		eq(t, r.isError, true, "error")
		hasRe(t, r.text, "already a custom user agent")
	})
	tw(t, f, "refuses to eject an unknown agent", func(t *testing.T) {
		e := newEjectEnv(t)
		r := e.act("eject", map[string]any{"agent": "no-such-agent"})
		eq(t, r.isError, true, "error")
		hasRe(t, r.text, `not found or is not a bundled/package agent`)
	})
	tw(t, f, "hides a builtin from runtime discovery via a user settings override", func(t *testing.T) {
		e := newEjectEnv(t)
		r := e.act("disable", map[string]any{"agent": "reviewer"})
		eq(t, r.isError, false, "error")
		hasRe(t, r.text, "Disabled agent 'reviewer' via user settings override")
		eq(t, findAgent(e.agents(t), "reviewer") == nil, true, "hidden")
		all := findAgent(dAll(t, e.dir).Builtin, "reviewer")
		ptrEq(t, all.Disabled, new(true), "disabled")
		eq(t, all.Override.Scope, "user", "scope")
		d, _ := overrideOf(t, e.userSettings(), "reviewer").get("disabled")
		eq(t, d, true, "settings")
	})
	tw(t, f, "merges disabled into an existing override without dropping other fields", func(t *testing.T) {
		e := newEjectEnv(t)
		wjc(t, e.userSettings(), sub(m{"agentOverrides": m{"reviewer": m{"model": "openai/gpt-5.4"}}}))
		e.act("disable", map[string]any{"agent": "reviewer"})
		eq(t, marshalJSON(overrideOf(t, e.userSettings(), "reviewer"), ""), `{"model":"openai/gpt-5.4","disabled":true}`, "override")
	})
	tw(t, f, "hides a disabled agent from agent-facing list output", func(t *testing.T) {
		e := newEjectEnv(t)
		e.act("disable", map[string]any{"agent": "reviewer"})
		noRe(t, e.act("list", map[string]any{}).text, `(?m)^- reviewer `)
	})
	tw(t, f, "writes a project-scoped override when agentScope is project", func(t *testing.T) {
		e := newEjectEnv(t)
		mkdirAll(t, filepath.Join(e.dir, ".pi"))
		r := e.act("disable", map[string]any{"agent": "reviewer", "agentScope": "project"})
		eq(t, r.isError, false, "error")
		hasRe(t, r.text, "via project settings override")
		eq(t, findAgent(dAll(t, e.dir).Builtin, "reviewer").Override.Scope, "project", "scope")
		eq(t, fileExists2(e.projectSettings()), true, "settings")
	})
	tw(t, f, "refuses to disable an unknown agent", func(t *testing.T) {
		e := newEjectEnv(t)
		r := e.act("disable", map[string]any{"agent": "no-such-agent"})
		eq(t, r.isError, true, "error")
		hasRe(t, r.text, "not found")
	})
	tw(t, f, "also disables a custom agent via a settings override", func(t *testing.T) {
		e := newEjectEnv(t)
		writeFile(t, e.userAgent("helper"), "---\nname: helper\ndescription: Helper\n---\n\nHelp.\n")
		r := e.act("disable", map[string]any{"agent": "helper"})
		eq(t, r.isError, false, "error")
		eq(t, findAgent(e.agents(t), "helper") == nil, true, "hidden")
	})
	tw(t, f, "also disables a package agent via a settings override", func(t *testing.T) {
		e := newEjectEnv(t)
		e.writePackageAgent(t, "packaged-reviewer")
		eq(t, findAgent(e.agents(t), "packaged-reviewer").Source, SourcePackage, "package")
		r := e.act("disable", map[string]any{"agent": "packaged-reviewer"})
		eq(t, r.isError, false, "error")
		eq(t, findAgent(e.agents(t), "packaged-reviewer") == nil, true, "hidden")
		all := findAgent(dAll(t, e.dir).Package, "packaged-reviewer")
		ptrEq(t, all.Disabled, new(true), "disabled")
		eq(t, all.Override.Scope, "user", "scope")
	})
	tw(t, f, "restores a previously disabled builtin to runtime discovery", func(t *testing.T) {
		e := newEjectEnv(t)
		e.act("disable", map[string]any{"agent": "reviewer"})
		eq(t, findAgent(e.agents(t), "reviewer") == nil, true, "hidden")
		r := e.act("enable", map[string]any{"agent": "reviewer"})
		eq(t, r.isError, false, "error")
		hasRe(t, r.text, "Enabled agent 'reviewer'")
		eq(t, findAgent(e.agents(t), "reviewer") != nil, true, "restored")
		sub := readJSONFileT(t, e.userSettings()).obj("subagents")
		eq(t, sub == nil || sub.obj("agentOverrides") == nil || sub.obj("agentOverrides").obj("reviewer") == nil, true, "override removed")
	})
	tw(t, f, "preserves other override fields when removing the disabled flag", func(t *testing.T) {
		e := newEjectEnv(t)
		wjc(t, e.userSettings(), sub(m{"agentOverrides": m{"reviewer": m{"model": "openai/gpt-5.4", "disabled": true}}}))
		e.act("enable", map[string]any{"agent": "reviewer"})
		eq(t, marshalJSON(overrideOf(t, e.userSettings(), "reviewer"), ""), `{"model":"openai/gpt-5.4"}`, "override")
		eq(t, findAgent(e.agents(t), "reviewer") != nil, true, "enabled")
	})
	tw(t, f, "reports already enabled and makes no changes when nothing is disabled", func(t *testing.T) {
		e := newEjectEnv(t)
		r := e.act("enable", map[string]any{"agent": "reviewer"})
		eq(t, r.isError, false, "error")
		hasRe(t, r.text, "already enabled")
		eq(t, fileExists2(e.userSettings()), false, "settings")
	})
	tw(t, f, "points to the disabling scope when enabling the wrong scope", func(t *testing.T) {
		e := newEjectEnv(t)
		mkdirAll(t, filepath.Join(e.dir, ".pi"))
		e.act("disable", map[string]any{"agent": "reviewer", "agentScope": "project"})
		r := e.act("enable", map[string]any{"agent": "reviewer"})
		eq(t, r.isError, true, "error")
		hasRe(t, r.text, "still disabled via a project scope override")
		hasRe(t, r.text, "agentScope: 'project'")
	})
	tw(t, f, "refuses to enable an unknown agent", func(t *testing.T) {
		e := newEjectEnv(t)
		r := e.act("enable", map[string]any{"agent": "no-such-agent"})
		eq(t, r.isError, true, "error")
		hasRe(t, r.text, "not found")
	})
	tw(t, f, "deletes a custom shadow file and restores the bundled builtin", func(t *testing.T) {
		e := newEjectEnv(t)
		e.act("eject", map[string]any{"agent": "reviewer"})
		eq(t, findAgent(e.agents(t), "reviewer").Source, SourceUser, "ejected")
		r := e.act("reset", map[string]any{"agent": "reviewer"})
		eq(t, r.isError, false, "error")
		hasRe(t, r.text, "Deleted custom user agent file")
		hasRe(t, r.text, "Reset agent 'reviewer' to its bundled builtin default")
		eq(t, fileExists2(e.userAgent("reviewer")), false, "file")
		eq(t, findAgent(e.agents(t), "reviewer").Source, SourceBuiltin, "builtin")
	})
	tw(t, f, "removes a settings override and restores the pristine builtin", func(t *testing.T) {
		e := newEjectEnv(t)
		e.act("disable", map[string]any{"agent": "reviewer"})
		eq(t, findAgent(e.agents(t), "reviewer") == nil, true, "hidden")
		r := e.act("reset", map[string]any{"agent": "reviewer"})
		eq(t, r.isError, false, "error")
		hasRe(t, r.text, "Removed user settings override")
		eq(t, findAgent(e.agents(t), "reviewer") != nil, true, "restored")
		eq(t, readJSONFileT(t, e.userSettings()).has("subagents"), false, "subagents")
	})
	tw(t, f, "retains machine placement while clearing other settings customization", func(t *testing.T) {
		e := newEjectEnv(t)
		wjc(t, e.userSettings(), sub(m{"agentOverrides": m{"reviewer": m{"machine": "workmac", "model": "openai/gpt-5.4", "thinking": "high"}}}))
		r := e.act("reset", map[string]any{"agent": "reviewer"})
		eq(t, r.isError, false, "error")
		hasRe(t, r.text, "Retained machine placement")
		eq(t, marshalJSON(overrideOf(t, e.userSettings(), "reviewer"), ""), `{"machine":"workmac"}`, "override")
		eq(t, findAgent(dAll(t, e.dir).Builtin, "reviewer").Machine, "workmac", "machine")
	})
	tw(t, f, "retains a false machine clear so an inherited placement does not reactivate", func(t *testing.T) {
		e := newEjectEnv(t)
		wjc(t, e.userSettings(), sub(m{"agentOverrides": m{"claude-code": m{"machine": "workmac"}}}))
		wjc(t, e.projectSettings(), sub(m{"agentOverrides": m{"claude-code": m{"machine": false, "model": "openai/gpt-5.4"}}}))
		r := e.act("reset", map[string]any{"agent": "claude-code", "agentScope": "project"})
		eq(t, r.isError, false, "error")
		eq(t, marshalJSON(overrideOf(t, e.projectSettings(), "claude-code"), ""), `{"machine":false}`, "override")
		eq(t, findAgent(dAll(t, e.dir).Builtin, "claude-code").Machine, "", "machine")
	})
	tw(t, f, "removes both a custom file and a settings override in one reset", func(t *testing.T) {
		e := newEjectEnv(t)
		e.act("eject", map[string]any{"agent": "reviewer"})
		e.act("disable", map[string]any{"agent": "reviewer"})
		r := e.act("reset", map[string]any{"agent": "reviewer"})
		eq(t, r.isError, false, "error")
		hasRe(t, r.text, "Deleted custom user agent file")
		hasRe(t, r.text, "Removed user settings override")
		eq(t, fileExists2(e.userAgent("reviewer")), false, "file")
		eq(t, findAgent(e.agents(t), "reviewer") != nil, true, "restored")
	})
	tw(t, f, "reports a no-op when there is nothing to reset in the target scope", func(t *testing.T) {
		e := newEjectEnv(t)
		r := e.act("reset", map[string]any{"agent": "reviewer"})
		eq(t, r.isError, false, "error")
		hasRe(t, r.text, "no user customization to reset")
		hasRe(t, r.text, "at its bundled builtin default")
	})
	tw(t, f, "points to delete for a custom agent with no bundled default", func(t *testing.T) {
		e := newEjectEnv(t)
		writeFile(t, e.userAgent("solo-helper"), "---\nname: solo-helper\ndescription: Solo\n---\n\nSolo.\n")
		r := e.act("reset", map[string]any{"agent": "solo-helper"})
		eq(t, r.isError, true, "error")
		hasRe(t, r.text, "no bundled default to reset to")
		hasRe(t, r.text, `action: "delete"`)
		eq(t, fileExists2(e.userAgent("solo-helper")), true, "file")
	})
	tw(t, f, "refuses to reset an unknown agent", func(t *testing.T) {
		e := newEjectEnv(t)
		r := e.act("reset", map[string]any{"agent": "no-such-agent"})
		eq(t, r.isError, true, "error")
		hasRe(t, r.text, "not found")
	})
}
