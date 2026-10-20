package pi_permission_system

import (
	"path/filepath"
	"testing"
)

// Twins of test/policy/permission-manager-unified.test.ts (part 1: platform, session rules, source and origin provenance, the
// in-memory loader, per-tool and cross-cutting path patterns, deny-with-reason, home expansion).

const unified = "policy/permission-manager-unified"

func TestPolicyUnifiedPlatformTwins(t *testing.T) {
	winAllow := Ruleset{sessionRule("external_directory_read", `C:\Users\Foo\pi\*`)}
	newPlatform := func(flavor PathFlavor) *PermissionManager {
		return NewPermissionManager(PermissionManagerOptions{PolicyLoaderOptions: PolicyLoaderOptions{
			GlobalConfigPath: "/nonexistent/config.json", AgentsDir: "/nonexistent/agents", McpServerNames: []string{}}, Flavor: flavor})
	}
	tw(t, unified, "win32 manager folds case for path-surface matching", func(t *testing.T) {
		r := checkValues(newPlatform(Win32PathFlavor), "external_directory_read", []string{`c:\users\foo\pi\docs\readme.md`}, "", winAllow)
		eq(t, r.State, "allow", "state")
	})
	tw(t, unified, "posix manager keeps path-surface matching case-sensitive", func(t *testing.T) {
		r := checkValues(newPlatform(PosixPathFlavor), "external_directory_read", []string{`c:\users\foo\pi\docs\readme.md`}, "", winAllow)
		eq(t, r.State != "allow", true, "not allow")
	})
	tw(t, unified, "win32: a /tmp* allow rule suppresses a Git Bash /tmp path (#533)", func(t *testing.T) {
		// End to end: the token `ls /tmp` is under win32, take its match values, and confirm a natural `/tmp*` external_directory
		// allow rule matches them. The win32 matcher folds separators on both the rule and the value (#653), so the as-typed literal
		// resolves without a backslash match alias. Adaptation: the bash program is not on this branch (access-intent/bash/program is the
		// bash program lane's); the values come from the same path normalizer call the program makes for the token.
		values := NewPathNormalizer(Win32PathFlavor, "C:/projects/app").ForBashToken("/tmp", nil).MatchValues()
		m := newPlatform(Win32PathFlavor)
		eq(t, checkValues(m, "external_directory_read", values, "", Ruleset{sessionRule("external_directory_read", "/tmp*")}).State, "allow", "allowed")
		eq(t, checkValues(m, "external_directory_read", values, "", Ruleset{}).State != "allow", true, "no rule")
	})
	tw(t, unified, "win32: a /dev/null path allow rule suppresses the Git Bash device prompt (#653)", func(t *testing.T) {
		// The reported repro: `echo hi > /dev/null` reaches the `path` surface with the device spelled as typed, so a rule written the
		// same way must win over a preceding universal ask. Adaptation as above: the redirect target's AccessPath.
		values := NewPathNormalizer(Win32PathFlavor, `C:\projects\app`).ForBashToken("/dev/null", nil).MatchValues()
		eq(t, values, []string{"/dev/null"}, "values")
		m := newPlatform(Win32PathFlavor)
		eq(t, checkValues(m, "path_read", values, "", Ruleset{sessionRule("path_read", "*", "ask"), sessionRule("path_read", "/dev/null")}).State, "allow", "allowed")
		eq(t, checkValues(m, "path_read", values, "", Ruleset{sessionRule("path_read", "*", "ask")}).State, "ask", "asked without rule")
	})
}

func TestPolicyUnifiedSessionRulesTwins(t *testing.T) {
	tw(t, unified, "session rule wins over the universal default (external_directory)", func(t *testing.T) {
		m := newMissingConfigManager()
		r := checkPathAs(m, "/other/project", "", "external_directory_read", "", Ruleset{sessionRule("external_directory_read", "/other/project")})
		eq(t, []string{r.State, r.Source, pat(r)}, []string{"allow", "session", "/other/project"}, "result")
	})
	tw(t, unified, "session rule wins over the universal default (skill)", func(t *testing.T) {
		r := checkToolAs(t, newMissingConfigManager(), "skill", `{"name":"librarian"}`, "", Ruleset{sessionRule("skill", "librarian")})
		eq(t, []string{r.State, r.Source, pat(r)}, []string{"allow", "session", "librarian"}, "result")
	})
	tw(t, unified, "session rule wins over the universal default (bash)", func(t *testing.T) {
		r := checkToolAs(t, newMissingConfigManager(), "bash", `{"command":"git status"}`, "", Ruleset{sessionRule("bash", "git status")})
		eq(t, []string{r.State, r.Source, pat(r)}, []string{"allow", "session", "git status"}, "result")
	})
	tw(t, unified, "session rule wins over the universal default (tool — read)", func(t *testing.T) {
		r := checkToolAs(t, newMissingConfigManager(), "read", "{}", "", Ruleset{sessionRule("read", "*")})
		eq(t, []string{r.State, r.Source}, []string{"allow", "session"}, "result")
	})
	tw(t, unified, "session rule wins over the universal default (mcp)", func(t *testing.T) {
		r := checkToolAs(t, newMissingConfigManager(), "mcp", "{}", "", Ruleset{sessionRule("mcp", "mcp_status")})
		eq(t, []string{r.State, r.Source}, []string{"allow", "session"}, "result")
	})
	tw(t, unified, "no session rules — falls through to default (ask)", func(t *testing.T) {
		r := checkToolAs(t, newMissingConfigManager(), "read", "{}", "", Ruleset{})
		eq(t, r.State, "ask", "state")
		eq(t, r.Source != "session", true, "source")
	})
	tw(t, unified, "session rule with narrower pattern does not block a broader command not in session", func(t *testing.T) {
		// Only "git status" is session-approved; "git push" should fall through to default.
		r := checkToolAs(t, newMissingConfigManager(), "bash", `{"command":"git push origin main"}`, "", Ruleset{sessionRule("bash", "git status")})
		eq(t, r.State, "ask", "state")
		eq(t, r.Source != "session", true, "source")
	})
	tw(t, unified, "session wildcard pattern matches multiple commands", func(t *testing.T) {
		m := newMissingConfigManager()
		rules := Ruleset{sessionRule("bash", "git *")}
		push := checkToolAs(t, m, "bash", `{"command":"git push origin main"}`, "", rules)
		status := checkToolAs(t, m, "bash", `{"command":"git status"}`, "", rules)
		eq(t, []string{push.State, push.Source, status.State, status.Source}, []string{"allow", "session", "allow", "session"}, "results")
	})
}

func TestPolicyUnifiedSourceDerivationTwins(t *testing.T) {
	// external_directory (special surface)
	tw(t, unified, "source is 'special' for a config-matched path", func(t *testing.T) {
		m := managerWithConfig(t, `{"*":"ask","external_directory":{"/trusted/*":"allow"}}`)
		r := checkPathOn(m, "/trusted/repo", "external_directory_read")
		eq(t, []string{r.State, r.Source, pat(r)}, []string{"allow", "special", "/trusted/*"}, "result")
	})
	tw(t, unified, "source is 'special' even for a default match (no config rule)", func(t *testing.T) {
		r := checkPathOn(newMissingConfigManager(), "/some/path", "external_directory")
		eq(t, []string{r.State, r.Source, pat(r)}, []string{"ask", "special", "<undefined>"}, "result")
	})
	tw(t, unified, "matchedPattern is undefined for a default match", func(t *testing.T) {
		eq(t, pat(checkPathOn(newMissingConfigManager(), "/unknown", "external_directory")), "<undefined>", "pattern")
	})

	// directional path surfaces (special by family): the original's it.each over four surfaces, not in the ledger
	t.Run("source is 'special' for a config-matched %s rule", func(t *testing.T) {
		for _, surface := range []string{"path_read", "path_write", "external_directory_read", "external_directory_write"} {
			t.Run(surface, func(t *testing.T) {
				m := managerWithConfig(t, `{"*":"ask","`+surface+`":{"/trusted/*":"allow"}}`)
				r := checkPathOn(m, "/trusted/repo", surface)
				eq(t, []string{r.State, r.Source, pat(r)}, []string{"allow", "special", "/trusted/*"}, "result")
			})
		}
	})
	tw(t, unified, "expands a bare path rule onto both directional surfaces at load", func(t *testing.T) {
		m := managerWithConfig(t, `{"*":"ask","path":{"/secret/*":"deny"}}`)
		for _, surface := range []string{"path_read", "path_write"} {
			r := checkPathOn(m, "/secret/x", surface)
			eq(t, []string{r.State, pat(r)}, []string{"deny", "/secret/*"}, surface)
		}
		// Nothing survives on the bare surface: the fold is the read path.
		eq(t, pat(checkPathOn(m, "/secret/x", "path")), "<undefined>", "bare surface")
	})
	tw(t, unified, "appends an explicit directional rule after the sugar-derived ones", func(t *testing.T) {
		m := managerWithConfig(t, `{"*":"ask","external_directory":{"*":"ask"},"external_directory_read":{"/dev-root/*":"allow"}}`)
		eq(t, checkPathOn(m, "/dev-root/x", "external_directory_read").State, "allow", "read")
		eq(t, checkPathOn(m, "/dev-root/x", "external_directory_write").State, "ask", "write")
	})

	// skill surface
	tw(t, unified, "source is 'skill' for a config-matched skill name", func(t *testing.T) {
		m := managerWithConfig(t, `{"*":"ask","skill":{"librarian":"allow"}}`)
		r := checkTool(t, m, "skill", `{"name":"librarian"}`)
		eq(t, []string{r.State, r.Source, pat(r)}, []string{"allow", "skill", "librarian"}, "result")
	})
	tw(t, unified, "source is 'skill' even for a default match", func(t *testing.T) {
		r := checkTool(t, newMissingConfigManager(), "skill", `{"name":"unknown"}`)
		eq(t, []string{r.State, r.Source}, []string{"ask", "skill"}, "result")
	})

	// bash surface
	tw(t, unified, "source is 'bash' and command is included in result", func(t *testing.T) {
		m := managerWithConfig(t, `{"*":"ask","bash":{"git *":"allow"}}`)
		r := checkTool(t, m, "bash", `{"command":"git status"}`)
		eq(t, []string{r.State, r.Source, commandOf(r), pat(r)}, []string{"allow", "bash", "git status", "git *"}, "result")
	})
	tw(t, unified, "source is 'bash' even for a default match, command is empty string", func(t *testing.T) {
		r := checkTool(t, newMissingConfigManager(), "bash", "{}")
		eq(t, []string{r.Source, commandOf(r), pat(r)}, []string{"bash", "", "<undefined>"}, "result")
	})

	// mcp surface
	tw(t, unified, "source is 'mcp' for a config-matched target", func(t *testing.T) {
		m := managerWithConfig(t, `{"*":"ask","mcp":{"exa_search":"allow"}}`, "exa")
		r := checkTool(t, m, "mcp", `{"tool":"exa:search","server":"exa"}`)
		eq(t, []string{r.State, r.Source, pat(r)}, []string{"allow", "mcp", "exa_search"}, "result")
		eq(t, r.Target != nil, true, "target defined")
	})
	tw(t, unified, "source is 'default' when all targets match only the synthesized default", func(t *testing.T) {
		r := checkTool(t, newMissingConfigManager(), "mcp", `{"tool":"exa:search"}`)
		eq(t, []string{r.State, r.Source, pat(r)}, []string{"ask", "default", "<undefined>"}, "result")
	})
	tw(t, unified, "target field is set for a matched mcp call", func(t *testing.T) {
		m := managerWithConfig(t, `{"*":"ask","mcp":{"mcp_status":"allow"}}`)
		r := checkTool(t, m, "mcp", "{}")
		eq(t, r.Target != nil, true, "target defined")
		eq(t, r.Source, "mcp", "source")
	})

	// tool surfaces
	tw(t, unified, "built-in tool: source is always 'tool' (config match)", func(t *testing.T) {
		r := checkTool(t, managerWithConfig(t, `{"*":"ask","read":"allow"}`), "read", "{}")
		eq(t, []string{r.State, r.Source}, []string{"allow", "tool"}, "result")
	})
	tw(t, unified, "built-in tool: source is 'tool' even for a default match", func(t *testing.T) {
		r := checkTool(t, newMissingConfigManager(), "read", "{}")
		eq(t, []string{r.State, r.Source}, []string{"ask", "tool"}, "result")
	})
	tw(t, unified, "extension tool: source is 'default' when no config rule matches", func(t *testing.T) {
		r := checkTool(t, newMissingConfigManager(), "my_custom_tool", "{}")
		eq(t, []string{r.State, r.Source}, []string{"ask", "default"}, "result")
	})
	tw(t, unified, "extension tool: source is 'tool' when a config rule matches", func(t *testing.T) {
		r := checkTool(t, managerWithConfig(t, `{"*":"ask","my_custom_tool":"allow"}`), "my_custom_tool", "{}")
		eq(t, []string{r.State, r.Source}, []string{"allow", "tool"}, "result")
	})

	// matchedPattern for session rules across surfaces
	tw(t, unified, "matchedPattern is the session rule pattern for a session match (bash)", func(t *testing.T) {
		r := checkToolAs(t, newMissingConfigManager(), "bash", `{"command":"git status"}`, "", Ruleset{sessionRule("bash", "git *")})
		eq(t, []string{pat(r), r.Source}, []string{"git *", "session"}, "result")
	})
	tw(t, unified, "matchedPattern is the session rule pattern for a session match (skill)", func(t *testing.T) {
		r := checkToolAs(t, newMissingConfigManager(), "skill", `{"name":"librarian"}`, "", Ruleset{sessionRule("skill", "librarian")})
		eq(t, pat(r), "librarian", "pattern")
	})
}

func TestPolicyUnifiedHomeExpansionTwins(t *testing.T) {
	useFakeHome(t)
	underHome := func(rel string) string { return filepath.Join(fakeHome, rel) }
	tw(t, unified, "~/glob pattern allows a path under the real home directory", func(t *testing.T) {
		m := managerWithConfig(t, `{"*":"ask","external_directory":{"~/trusted/*":"allow"}}`)
		r := checkPathOn(m, underHome("trusted/repo"), "external_directory_read")
		eq(t, []string{r.State, r.Source, pat(r)}, []string{"allow", "special", "~/trusted/*"}, "result")
	})
	tw(t, unified, "$HOME/glob pattern allows a path under the real home directory", func(t *testing.T) {
		m := managerWithConfig(t, `{"*":"ask","external_directory":{"$HOME/trusted/*":"allow"}}`)
		r := checkPathOn(m, underHome("trusted/repo"), "external_directory_read")
		eq(t, []string{r.State, r.Source, pat(r)}, []string{"allow", "special", "$HOME/trusted/*"}, "result")
	})
	tw(t, unified, "~/glob deny rule blocks a path under home", func(t *testing.T) {
		m := managerWithConfig(t, `{"*":"allow","external_directory":{"~/private/*":"deny"}}`)
		r := checkPathOn(m, underHome("private/secrets.txt"), "external_directory_read")
		eq(t, []string{r.State, pat(r)}, []string{"deny", "~/private/*"}, "result")
	})
	tw(t, unified, "~/glob pattern does not match a path outside home", func(t *testing.T) {
		m := managerWithConfig(t, `{"*":"ask","external_directory":{"~/trusted/*":"allow"}}`)
		r := checkPathOn(m, "/tmp/not-home/file", "external_directory_read")
		// Falls back to the "*": "ask" default: no allow from the ~/trusted/* rule.
		eq(t, []string{r.State, pat(r)}, []string{"ask", "<undefined>"}, "result")
	})
}

func TestPolicyUnifiedOriginTwins(t *testing.T) {
	tw(t, unified, "single-scope global: config rule has origin 'global'", func(t *testing.T) {
		r := checkTool(t, managerWithScopes(t, `{"read":"allow"}`, ""), "read", "{}")
		eq(t, []string{r.State, r.Origin}, []string{"allow", "global"}, "result")
	})
	tw(t, unified, "single-scope global with pattern map: origin is 'global'", func(t *testing.T) {
		r := checkTool(t, managerWithScopes(t, `{"bash":{"git *":"allow"}}`, ""), "bash", `{"command":"git status"}`)
		eq(t, []string{r.State, r.Origin}, []string{"allow", "global"}, "result")
	})
	tw(t, unified, "project overrides global: winning rule has origin 'project'", func(t *testing.T) {
		r := checkTool(t, managerWithScopes(t, `{"read":"ask"}`, `{"read":"allow"}`), "read", "{}")
		eq(t, []string{r.State, r.Origin}, []string{"allow", "project"}, "result")
	})
	tw(t, unified, "both-object merge: patterns retain their own origins", func(t *testing.T) {
		// global defines bash["git *"] = allow; project adds bash["rm *"] = deny. Both patterns survive with their own origins.
		m := managerWithScopes(t, `{"bash":{"git *":"allow"}}`, `{"bash":{"rm *":"deny"}}`)
		git := checkTool(t, m, "bash", `{"command":"git status"}`)
		eq(t, []string{git.State, git.Origin}, []string{"allow", "global"}, "git")
		rm := checkTool(t, m, "bash", `{"command":"rm -rf /"}`)
		eq(t, []string{rm.State, rm.Origin}, []string{"deny", "project"}, "rm")
	})
	tw(t, unified, "both-object merge: project pattern overrides global pattern for same key", func(t *testing.T) {
		r := checkTool(t, managerWithScopes(t, `{"bash":{"git *":"ask"}}`, `{"bash":{"git *":"allow"}}`), "bash", `{"command":"git status"}`)
		eq(t, []string{r.State, r.Origin}, []string{"allow", "project"}, "result")
	})
	tw(t, unified, "string replaces object: all patterns from replacing scope get origin 'project'", func(t *testing.T) {
		// The catch-all "*" now comes from the project scope.
		r := checkTool(t, managerWithScopes(t, `{"bash":{"git *":"ask","npm *":"ask"}}`, `{"bash":"allow"}`), "bash", `{"command":"anything"}`)
		eq(t, []string{r.State, r.Origin}, []string{"allow", "project"}, "result")
	})
	tw(t, unified, "object replaces string: all patterns from replacing scope get origin 'project'", func(t *testing.T) {
		r := checkTool(t, managerWithScopes(t, `{"read":"ask"}`, `{"read":{"*":"allow"}}`), "read", "{}")
		eq(t, []string{r.State, r.Origin}, []string{"allow", "project"}, "result")
	})
	tw(t, unified, "no config match: origin is 'builtin' (default layer)", func(t *testing.T) {
		r := checkTool(t, newMissingConfigManager(), "read", "{}")
		eq(t, []string{r.State, r.Origin}, []string{"ask", "builtin"}, "result")
	})
	tw(t, unified, "session rule: origin is 'session'", func(t *testing.T) {
		r := checkToolAs(t, newMissingConfigManager(), "read", "{}", "", Ruleset{sessionRule("read", "*")})
		eq(t, []string{r.State, r.Source, r.Origin}, []string{"allow", "session", "session"}, "result")
	})
	tw(t, unified, "universal fallback (*) set in global config carries origin 'global'", func(t *testing.T) {
		// No explicit surface rule: hits the synthesized default derived from "*".
		r := checkTool(t, managerWithScopes(t, `{"*":"allow"}`, ""), "read", "{}")
		eq(t, []string{r.State, r.Origin}, []string{"allow", "global"}, "result")
	})
	tw(t, unified, "universal fallback (*) overridden by project carries origin 'project'", func(t *testing.T) {
		r := checkTool(t, managerWithScopes(t, `{"*":"ask"}`, `{"*":"allow"}`), "read", "{}")
		eq(t, []string{r.State, r.Origin}, []string{"allow", "project"}, "result")
	})
	tw(t, unified, "built-in fallback (no * in any config): origin is 'builtin'", func(t *testing.T) {
		r := checkTool(t, newMissingConfigManager(), "read", "{}")
		eq(t, []string{r.State, r.Origin}, []string{"ask", "builtin"}, "result")
	})
}

func TestPolicyUnifiedInMemoryLoaderTwins(t *testing.T) {
	g := func(t *testing.T, permission string) polScopes { return polScopes{global: permScope(t, permission)} }

	// universal fallback
	tw(t, unified, "defaults to ask when no config is provided", func(t *testing.T) {
		r := checkTool(t, newInMemoryManager(polScopes{}), "read", "{}")
		eq(t, []string{r.State, r.Origin}, []string{"ask", "builtin"}, "result")
	})
	tw(t, unified, "respects permission['*'] = 'allow' from global config", func(t *testing.T) {
		r := checkTool(t, newInMemoryManager(g(t, `{"*":"allow"}`)), "read", "{}")
		eq(t, []string{r.State, r.Origin}, []string{"allow", "global"}, "result")
	})
	tw(t, unified, "respects permission['*'] = 'deny' from global config", func(t *testing.T) {
		eq(t, checkTool(t, newInMemoryManager(g(t, `{"*":"deny"}`)), "write", "{}").State, "deny", "state")
	})

	// surface routing
	tw(t, unified, "bash surface routes correctly", func(t *testing.T) {
		r := checkTool(t, newInMemoryManager(g(t, `{"*":"ask","bash":{"git *":"allow"}}`)), "bash", `{"command":"git status"}`)
		eq(t, []string{r.State, r.Source, pat(r)}, []string{"allow", "bash", "git *"}, "result")
	})
	tw(t, unified, "tool surface routes correctly for built-in tools", func(t *testing.T) {
		r := checkTool(t, newInMemoryManager(g(t, `{"*":"deny","read":"allow"}`)), "read", "{}")
		eq(t, []string{r.State, r.Source}, []string{"allow", "tool"}, "result")
	})
	tw(t, unified, "skill surface routes correctly", func(t *testing.T) {
		r := checkTool(t, newInMemoryManager(g(t, `{"*":"ask","skill":{"librarian":"allow"}}`)), "skill", `{"name":"librarian"}`)
		eq(t, []string{r.State, r.Source}, []string{"allow", "skill"}, "result")
	})
	tw(t, unified, "mcp surface routes correctly", func(t *testing.T) {
		r := checkTool(t, newInMemoryManager(g(t, `{"*":"ask","mcp":{"exa_search":"allow"}}`), "exa"), "mcp", `{"tool":"exa:search","server":"exa"}`)
		eq(t, []string{r.State, r.Source}, []string{"allow", "mcp"}, "result")
	})
	tw(t, unified, "external_directory surface routes correctly", func(t *testing.T) {
		r := checkPathOn(newInMemoryManager(g(t, `{"*":"ask","external_directory":{"/trusted/*":"allow"}}`)), "/trusted/repo", "external_directory_read")
		eq(t, []string{r.State, r.Source}, []string{"allow", "special"}, "result")
	})
	tw(t, unified, "extension tools use 'default' source when no config rule matches", func(t *testing.T) {
		r := checkTool(t, newInMemoryManager(g(t, `{"*":"ask"}`)), "my_custom_tool", "{}")
		eq(t, []string{r.State, r.Source}, []string{"ask", "default"}, "result")
	})

	// multi-scope merge
	tw(t, unified, "project overrides global", func(t *testing.T) {
		m := newInMemoryManager(polScopes{global: permScope(t, `{"read":"ask"}`), project: permScope(t, `{"read":"allow"}`)})
		r := checkTool(t, m, "read", "{}")
		eq(t, []string{r.State, r.Origin}, []string{"allow", "project"}, "result")
	})
	tw(t, unified, "agent overrides project", func(t *testing.T) {
		m := newInMemoryManager(polScopes{global: permScope(t, `{"read":"ask"}`), project: permScope(t, `{"read":"allow"}`),
			agent: map[string]*ScopeConfig{"coder": permScope(t, `{"read":"deny"}`)}})
		r := checkToolAs(t, m, "read", "{}", "coder", nil)
		eq(t, []string{r.State, r.Origin}, []string{"deny", "agent"}, "result")
	})
	tw(t, unified, "project-agent overrides agent", func(t *testing.T) {
		m := newInMemoryManager(polScopes{global: permScope(t, `{"read":"deny"}`),
			agent:        map[string]*ScopeConfig{"coder": permScope(t, `{"read":"deny"}`)},
			projectAgent: map[string]*ScopeConfig{"coder": permScope(t, `{"read":"allow"}`)}})
		r := checkToolAs(t, m, "read", "{}", "coder", nil)
		eq(t, []string{r.State, r.Origin}, []string{"allow", "project-agent"}, "result")
	})
	tw(t, unified, "deep-shallow merge preserves patterns from different scopes", func(t *testing.T) {
		m := newInMemoryManager(polScopes{global: permScope(t, `{"bash":{"git *":"allow"}}`), project: permScope(t, `{"bash":{"rm *":"deny"}}`)})
		git := checkTool(t, m, "bash", `{"command":"git status"}`)
		eq(t, []string{git.State, git.Origin}, []string{"allow", "global"}, "git")
		rm := checkTool(t, m, "bash", `{"command":"rm -rf /"}`)
		eq(t, []string{rm.State, rm.Origin}, []string{"deny", "project"}, "rm")
	})
	tw(t, unified, "string replaces object in override scope", func(t *testing.T) {
		m := newInMemoryManager(polScopes{global: permScope(t, `{"bash":{"git *":"ask","npm *":"ask"}}`), project: permScope(t, `{"bash":"allow"}`)})
		r := checkTool(t, m, "bash", `{"command":"anything"}`)
		eq(t, []string{r.State, r.Origin}, []string{"allow", "project"}, "result")
	})

	// session rule composition
	tw(t, unified, "session rule wins over config", func(t *testing.T) {
		r := checkToolAs(t, newInMemoryManager(g(t, `{"*":"deny"}`)), "read", "{}", "", Ruleset{sessionRule("read", "*")})
		eq(t, []string{r.State, r.Source}, []string{"allow", "session"}, "result")
	})
	tw(t, unified, "session rule does not bleed across surfaces", func(t *testing.T) {
		m := newInMemoryManager(g(t, `{"*":"ask"}`))
		rules := Ruleset{sessionRule("bash", "git *")}
		eq(t, checkToolAs(t, m, "bash", `{"command":"git status"}`, "", rules).State, "allow", "bash")
		eq(t, checkToolAs(t, m, "read", "{}", "", rules).State, "ask", "read")
	})

	// origin tracking
	tw(t, unified, "universal fallback from project carries origin 'project'", func(t *testing.T) {
		m := newInMemoryManager(polScopes{global: permScope(t, `{"*":"ask"}`), project: permScope(t, `{"*":"allow"}`)})
		r := checkTool(t, m, "read", "{}")
		eq(t, []string{r.State, r.Origin}, []string{"allow", "project"}, "result")
	})
	tw(t, unified, "session origin is 'session'", func(t *testing.T) {
		r := checkToolAs(t, newInMemoryManager(polScopes{}), "read", "{}", "", Ruleset{sessionRule("read", "*")})
		eq(t, r.Origin, "session", "origin")
	})

	// getToolPermission
	tw(t, unified, "returns tool-level state for built-in tools", func(t *testing.T) {
		m := newInMemoryManager(g(t, `{"*":"deny","read":"allow"}`))
		eq(t, m.GetToolPermission("read", nil), "allow", "read")
		eq(t, m.GetToolPermission("write", nil), "deny", "write")
	})
	tw(t, unified, "returns tool-level state for bash surface", func(t *testing.T) {
		eq(t, newInMemoryManager(g(t, `{"*":"deny","bash":"allow"}`)).GetToolPermission("bash", nil), "allow", "bash")
	})
	tw(t, unified, "reports the surface catch-all even when a nested pattern is permissive", func(t *testing.T) {
		eq(t, newInMemoryManager(g(t, `{"*":"ask","bash":{"*":"deny","git *":"ask"}}`)).GetToolPermission("bash", nil), "deny", "bash")
	})

	// isToolFullyDenied
	tw(t, unified, "is false when a nested pattern under a deny catch-all is reachable", func(t *testing.T) {
		eq(t, newInMemoryManager(g(t, `{"*":"ask","bash":{"*":"deny","git *":"ask"}}`)).IsToolFullyDenied("bash", nil), false, "denied")
	})
	tw(t, unified, "is true when the surface is denied outright", func(t *testing.T) {
		eq(t, newInMemoryManager(g(t, `{"*":"ask","bash":"deny"}`)).IsToolFullyDenied("bash", nil), true, "denied")
	})
	tw(t, unified, "is true when the exception is shadowed by a later deny catch-all", func(t *testing.T) {
		eq(t, newInMemoryManager(g(t, `{"*":"ask","bash":{"git *":"ask","*":"deny"}}`)).IsToolFullyDenied("bash", nil), true, "denied")
	})
	tw(t, unified, "is false for a path-bearing tool with a permissive path pattern", func(t *testing.T) {
		eq(t, newInMemoryManager(g(t, `{"*":"ask","read":{"*":"deny","~/notes/*":"allow"}}`)).IsToolFullyDenied("read", nil), false, "denied")
	})
	tw(t, unified, "is true for every tool under a universal deny", func(t *testing.T) {
		m := newInMemoryManager(g(t, `{"*":"deny"}`))
		eq(t, m.IsToolFullyDenied("bash", nil), true, "bash")
		eq(t, m.IsToolFullyDenied("third_party_tool", nil), true, "third party")
	})
	tw(t, unified, "trims the tool name and honors the agent scope", func(t *testing.T) {
		m := newInMemoryManager(polScopes{global: permScope(t, `{"*":"ask","bash":"deny"}`),
			agent: map[string]*ScopeConfig{"reviewer": permScope(t, `{"bash":{"*":"deny","git *":"ask"}}`)}})
		eq(t, m.IsToolFullyDenied(" bash ", nil), true, "trimmed")
		eq(t, m.IsToolFullyDenied("bash", sp("reviewer")), false, "agent")
	})

	// getComposedConfigRules
	tw(t, unified, "returns only config-layer rules", func(t *testing.T) {
		rules := newInMemoryManager(g(t, `{"*":"ask","bash":{"git *":"allow"}}`)).GetComposedConfigRules(nil)
		onlyConfig, hasGit := true, false
		for _, r := range rules {
			onlyConfig = onlyConfig && r.Layer == "config"
			hasGit = hasGit || (r.Surface == "bash" && r.Pattern == "git *")
		}
		eq(t, []bool{onlyConfig, hasGit}, []bool{true, true}, "rules")
	})
}

func TestPolicyUnifiedPerToolPathPatternTwins(t *testing.T) {
	tw(t, unified, "denies read of .env when path pattern matches", func(t *testing.T) {
		r := checkPathOn(managerWithConfig(t, `{"read":{"*":"allow","*.env":"deny"}}`), ".env", "read")
		eq(t, []string{r.State, pat(r)}, []string{"deny", "*.env"}, "result")
	})
	tw(t, unified, "allows read of non-.env file when .env is denied", func(t *testing.T) {
		eq(t, checkPathOn(managerWithConfig(t, `{"read":{"*":"allow","*.env":"deny"}}`), "src/main.ts", "read").State, "allow", "state")
	})
	tw(t, unified, "allows write to src/ when only src/ is allowed", func(t *testing.T) {
		r := checkPathOn(managerWithConfig(t, `{"write":{"*":"deny","src/*":"allow"}}`), "src/main.ts", "write")
		eq(t, []string{r.State, pat(r)}, []string{"allow", "src/*"}, "result")
	})
	tw(t, unified, "denies write outside src/ when only src/ is allowed", func(t *testing.T) {
		eq(t, checkPathOn(managerWithConfig(t, `{"write":{"*":"deny","src/*":"allow"}}`), "vendor/lib.ts", "write").State, "deny", "state")
	})
	tw(t, unified, "backward compat: 'read': 'allow' allows read of any path", func(t *testing.T) {
		eq(t, checkPathOn(managerWithConfig(t, `{"read":"allow"}`), ".env", "read").State, "allow", "state")
	})
	tw(t, unified, "backward compat: 'read': 'deny' denies read of any path", func(t *testing.T) {
		eq(t, checkPathOn(managerWithConfig(t, `{"read":"deny"}`), "src/main.ts", "read").State, "deny", "state")
	})
	tw(t, unified, "session rule for specific path overrides config deny", func(t *testing.T) {
		m := managerWithConfig(t, `{"read":{"*":"allow","*.env":"deny"}}`)
		r := checkPathAs(m, ".env", "", "read", "", Ruleset{sessionRule("read", ".env")})
		eq(t, []string{r.State, r.Source}, []string{"allow", "session"}, "result")
	})
	tw(t, unified, "falls back to '*' when input.path is missing", func(t *testing.T) {
		eq(t, checkTool(t, managerWithConfig(t, `{"read":{"*":"allow","*.env":"deny"}}`), "read", "{}").State, "allow", "state")
	})
	tw(t, unified, "getToolPermission still returns surface-level state (not path-specific)", func(t *testing.T) {
		eq(t, managerWithConfig(t, `{"read":{"*":"allow","*.env":"deny"}}`).GetToolPermission("read", nil), "allow", "state")
	})
}

func TestPolicyUnifiedCrossCuttingPathTwins(t *testing.T) {
	tw(t, unified, "denies .env via the path surface", func(t *testing.T) {
		r := checkPath(managerWithConfig(t, `{"path":{"*":"allow","*.env":"deny"},"read":"allow"}`), ".env")
		eq(t, []string{r.State, pat(r)}, []string{"deny", "*.env"}, "result")
	})
	tw(t, unified, "allows non-matching paths via the path surface", func(t *testing.T) {
		eq(t, checkPath(managerWithConfig(t, `{"path":{"*":"allow","*.env":"deny"},"read":"allow"}`), "README.md").State, "allow", "state")
	})
	tw(t, unified, "path surface does not interfere with per-tool rules", func(t *testing.T) {
		m := managerWithConfig(t, `{"path":{"*":"allow"},"read":{"*":"allow","*.secret":"deny"}}`)
		// path surface allows, per-tool denies
		eq(t, checkPathOn(m, "data.secret", "read").State, "deny", "read")
		// path surface also allows
		eq(t, checkPath(m, "data.secret").State, "allow", "path")
	})
	tw(t, unified, "getToolPermission('path') returns catch-all action", func(t *testing.T) {
		eq(t, managerWithConfig(t, `{"path":{"*":"allow","*.env":"deny"}}`).GetToolPermission("path_read", nil), "allow", "state")
	})
	tw(t, unified, "session approval on path surface overrides config deny", func(t *testing.T) {
		m := managerWithConfig(t, `{"path":{"*":"allow","*.env":"deny"}}`)
		r := checkPathAs(m, "/project/.env", "", "path_read", "", Ruleset{sessionRule("path_read", "/project/.env")})
		eq(t, []string{r.State, r.Source}, []string{"allow", "session"}, "result")
	})
	tw(t, unified, "configs without path key behave identically (no path gate fires)", func(t *testing.T) {
		// path surface falls through to universal default
		eq(t, checkPath(managerWithConfig(t, `{"read":"allow"}`), ".env").State, "ask", "state")
	})
	tw(t, unified, "universal default produces undefined matchedPattern for gate skip (#58)", func(t *testing.T) {
		m := managerWithConfig(t, `{"*":"ask","read":"allow","find":"allow"}`)
		// No explicit "path" key: matchedPattern must be undefined so the path gate skips.
		r := checkPath(m, "src/main.ts")
		eq(t, []string{r.State, pat(r)}, []string{"ask", "<undefined>"}, "path")
		// Meanwhile the tool-level check should allow read.
		read := checkPathOn(m, "src/main.ts", "read")
		eq(t, []string{read.State, pat(read)}, []string{"allow", "*"}, "read")
	})

	// deny-with-reason
	tw(t, unified, "deny-with-reason: reason threads through to PermissionCheckResult", func(t *testing.T) {
		r := checkTool(t, managerWithConfig(t, `{"bash":{"npm *":{"action":"deny","reason":"Use pnpm instead"}}}`), "bash", `{"command":"npm install"}`)
		eq(t, []string{r.State, reasonOf(r), pat(r)}, []string{"deny", "Use pnpm instead", "npm *"}, "result")
	})
	tw(t, unified, "deny-without-reason: reason is undefined in PermissionCheckResult", func(t *testing.T) {
		r := checkTool(t, managerWithConfig(t, `{"bash":{"rm -rf *":"deny"}}`), "bash", `{"command":"rm -rf /"}`)
		eq(t, []string{r.State, reasonOf(r)}, []string{"deny", "<undefined>"}, "result")
	})
	tw(t, unified, "deny-with-reason on a non-bash surface", func(t *testing.T) {
		r := checkPathOn(managerWithConfig(t, `{"read":{"*.env":{"action":"deny","reason":"Environment files contain secrets"}}}`), ".env", "read")
		eq(t, []string{r.State, reasonOf(r), pat(r)}, []string{"deny", "Environment files contain secrets", "*.env"}, "result")
	})
	tw(t, unified, "non-string reason falls through to the default (malformed config)", func(t *testing.T) {
		r := checkTool(t, managerWithConfig(t, `{"bash":{"npm *":{"action":"deny","reason":42}}}`), "bash", `{"command":"npm install"}`)
		eq(t, []string{r.State, reasonOf(r)}, []string{"ask", "<undefined>"}, "result")
	})

	// last-match-wins ordering
	tw(t, unified, "last-match-wins: catch-all after deny overrides the deny", func(t *testing.T) {
		// Classic misconfiguration: deny is before allow, so allow wins ("*" is last and matches .env).
		eq(t, checkPath(managerWithConfig(t, `{"path":{"*.env":"deny","*":"allow"}}`), ".env").State, "allow", "state")
	})
	tw(t, unified, "last-match-wins: deny after catch-all blocks the path", func(t *testing.T) {
		eq(t, checkPath(managerWithConfig(t, `{"path":{"*":"allow","*.env":"deny"}}`), ".env").State, "deny", "state")
	})
	tw(t, unified, ".env.example override: denies .env and .env.local, allows .env.example", func(t *testing.T) {
		m := managerWithConfig(t, `{"path":{"*":"allow","*.env":"deny","*.env.*":"deny","*.env.example":"allow"}}`)
		for path, want := range map[string]string{".env": "deny", ".env.local": "deny", ".env.production": "deny", "src/.env": "deny", ".env.example": "allow", "README.md": "allow"} {
			eq(t, checkPath(m, path).State, want, path)
		}
	})

	// universal fallback interaction
	tw(t, unified, "universal '*': 'allow' with no path key makes the path gate transparent", func(t *testing.T) {
		eq(t, checkPath(managerWithConfig(t, `{"*":"allow"}`), ".env").State, "allow", "state")
	})
	tw(t, unified, "universal '*': 'deny' with no path key denies via path surface too", func(t *testing.T) {
		eq(t, checkPath(managerWithConfig(t, `{"*":"deny"}`), ".env").State, "deny", "state")
	})
	tw(t, unified, "per-tool deny still blocks even when path surface allows", func(t *testing.T) {
		m := managerWithConfig(t, `{"path":{"*":"allow"},"read":"deny"}`)
		eq(t, checkPath(m, "secret.txt").State, "allow", "path")
		eq(t, checkPathOn(m, "secret.txt", "read").State, "deny", "read")
	})
}

func TestPolicyUnifiedHomeExpandedValuesTwins(t *testing.T) {
	useFakeHome(t)
	tw(t, unified, "~/... path value is denied by a ~/* rule (reported footgun)", func(t *testing.T) {
		r := checkPath(managerWithConfig(t, `{"path":{"*":"allow","~/.ssh/*":"deny"}}`), "~/.ssh/config")
		eq(t, []string{r.State, pat(r)}, []string{"deny", "~/.ssh/*"}, "result")
	})
	tw(t, unified, "$HOME/... path value is denied by a ~/* rule", func(t *testing.T) {
		r := checkPath(managerWithConfig(t, `{"path":{"*":"allow","~/.ssh/*":"deny"}}`), fakeHome+"/.ssh/config")
		eq(t, []string{r.State, pat(r)}, []string{"deny", "~/.ssh/*"}, "result")
	})
	tw(t, unified, "$HOME/... path value matches a $HOME/* pattern rule", func(t *testing.T) {
		r := checkPath(managerWithConfig(t, `{"path":{"*":"allow","$HOME/.ssh/*":"deny"}}`), "$HOME/.ssh/config")
		eq(t, []string{r.State, pat(r)}, []string{"deny", "$HOME/.ssh/*"}, "result")
	})
	tw(t, unified, "already-absolute home path is still denied by ~/* rule", func(t *testing.T) {
		eq(t, checkPath(managerWithConfig(t, `{"path":{"*":"allow","~/.ssh/*":"deny"}}`), fakeHome+"/.ssh/config").State, "deny", "state")
	})
	tw(t, unified, "non-home value is unchanged — .env still matches *.env", func(t *testing.T) {
		r := checkPath(managerWithConfig(t, `{"path":{"*":"allow","*.env":"deny"}}`), ".env")
		eq(t, []string{r.State, pat(r)}, []string{"deny", "*.env"}, "result")
	})
	tw(t, unified, "per-tool read surface denies ~/... path with a ~/* rule", func(t *testing.T) {
		r := checkPathOn(managerWithConfig(t, `{"*":"allow","read":{"*":"allow","~/.ssh/*":"deny"}}`), "~/.ssh/config", "read")
		eq(t, []string{r.State, pat(r)}, []string{"deny", "~/.ssh/*"}, "result")
	})
}
