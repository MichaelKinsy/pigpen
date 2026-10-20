package pi_permission_system

import (
	"os"
	"path/filepath"
	"testing"
)

// Twins of test/policy/permission-manager-unified.test.ts (part 2: configureForCwd and the agent directory, the project and agent
// scopes, MCP surface resolution, session-aware checks, resolved policy paths, cwd-aware path values, the unified check entry point,
// mcp last-match-wins, Pi MCP tools, forwarded mcp requests and bash spellings).

func agentFile(name, permissionYAML string) string {
	return "---\nname: " + name + "\npermission:\n" + permissionYAML + "---\n"
}

func TestPolicyUnifiedConfigureForCwdTwins(t *testing.T) {
	tw(t, unified, "ScopedPermissionManager is exported and PermissionManager satisfies it", func(t *testing.T) {
		var scoped ScopedPermissionManager = NewPermissionManager(PermissionManagerOptions{PolicyLoaderOptions: PolicyLoaderOptions{
			GlobalConfigPath: "/nonexistent/config.json", AgentsDir: "/nonexistent/agents"}})
		eq(t, scoped != nil, true, "scoped")
	})
	tw(t, unified, "construction with { agentDir } reads global config from getGlobalConfigPath(agentDir)", func(t *testing.T) {
		rig := newAgentDirHarness(t, `{"read":"deny"}`, "")
		m := NewPermissionManager(PermissionManagerOptions{AgentDir: rig.agentDir})
		eq(t, checkPathOn(m, "foo.txt", "read").State, "deny", "state")
	})
	tw(t, unified, "configureForCwd(cwd) applies project config (project overrides global)", func(t *testing.T) {
		rig := newAgentDirHarness(t, `{"read":"deny"}`, `{"read":"allow"}`)
		m := NewPermissionManager(PermissionManagerOptions{AgentDir: rig.agentDir})
		// Before configureForCwd: global policy applies
		eq(t, checkPathOn(m, "foo.txt", "read").State, "deny", "before")
		m.ConfigureForCwd(rig.cwd)
		// After configureForCwd: project override applies (last-match-wins)
		eq(t, checkPathOn(m, "foo.txt", "read").State, "allow", "after")
	})
	tw(t, unified, "configureForCwd(undefined) reverts to global-only", func(t *testing.T) {
		rig := newAgentDirHarness(t, `{"read":"deny"}`, `{"read":"allow"}`)
		m := NewPermissionManager(PermissionManagerOptions{AgentDir: rig.agentDir})
		m.ConfigureForCwd(rig.cwd)
		eq(t, checkPathOn(m, "foo.txt", "read").State, "allow", "project")
		m.ConfigureForCwd("")
		// After reverting: global policy applies again
		eq(t, checkPathOn(m, "foo.txt", "read").State, "deny", "global")
	})
	tw(t, unified, "configureForCwd clears the resolved-permissions cache", func(t *testing.T) {
		rig := newAgentDirHarness(t, `{"read":"allow"}`, "")
		m := NewPermissionManager(PermissionManagerOptions{AgentDir: rig.agentDir})
		// Warm the cache
		eq(t, checkPathOn(m, "foo.txt", "read").State, "allow", "warm")
		// Update global config on disk to deny read
		polWriteFile(t, rig.globalConfigPath, `{"permission":{"read":"deny"}}`)
		// configureForCwd clears cache + rebuilds loader
		m.ConfigureForCwd("")
		// Should pick up the changed global config
		eq(t, checkPathOn(m, "foo.txt", "read").State, "deny", "changed")
	})

	mcpCall := func(t *testing.T, m *PermissionManager, tool string) PermissionCheckResult {
		return m.Check(ToolAccessIntent{Surface: "mcp", Input: jo(t, `{"tool":"`+tool+`"}`)}, nil)
	}
	tw(t, unified, "derives a server configured only in the project's .pi/mcp.json once the cwd is set", func(t *testing.T) {
		rig := newAgentDirHarness(t, `{"*":"allow","mcp":{"*":"allow","github":"deny"}}`, "")
		polWriteFile(t, filepath.Join(rig.cwd, ".pi", "mcp.json"), `{"mcpServers":{"github":{}}}`)
		m := NewPermissionManager(PermissionManagerOptions{AgentDir: rig.agentDir})
		eq(t, mcpCall(t, m, "github_search").State, "allow", "before")
		m.ConfigureForCwd(rig.cwd)
		eq(t, mcpCall(t, m, "github_search").State, "deny", "with cwd")
		m.ConfigureForCwd("")
		eq(t, mcpCall(t, m, "github_search").State, "allow", "reverted")
	})
	tw(t, unified, "reads the global mcp.json from the agentDir it was given", func(t *testing.T) {
		rig := newAgentDirHarness(t, `{"*":"allow","mcp":{"*":"allow","github":"deny"}}`, "")
		polWriteFile(t, filepath.Join(rig.agentDir, "mcp.json"), `{"mcpServers":{"github":{}}}`)
		m := NewPermissionManager(PermissionManagerOptions{AgentDir: rig.agentDir})
		eq(t, mcpCall(t, m, "github_search").State, "deny", "state")
	})
	tw(t, unified, "configureForCwd(cwd) derives projectAgentsDir at <cwd>/.pi/agents (regression: #428)", func(t *testing.T) {
		// Bug: old code derived <cwd>/.pi/agent/agents instead of <cwd>/.pi/agents. This test pins the correct path and verifies
		// agentsDir is unchanged.
		rig := newAgentDirHarness(t, `{"read":"allow"}`, "")
		m := NewPermissionManager(PermissionManagerOptions{AgentDir: rig.agentDir})
		m.ConfigureForCwd(rig.cwd)
		paths := m.GetResolvedPolicyPaths()
		eq(t, paths.ProjectAgentsDir, sp(getProjectAgentsDir(rig.cwd)), "projectAgentsDir")
		eq(t, paths.AgentsDir, filepath.Join(rig.agentDir, "agents"), "agentsDir")
	})
	tw(t, unified, "configureForCwd(cwd) enforces permission: frontmatter from <cwd>/.pi/agents/<agent>.md (regression: #428)", func(t *testing.T) {
		// Bug: wrong directory meant project-agent frontmatter was never loaded.
		rig := newAgentDirHarness(t, `{"read":"allow"}`, "")
		polWriteFile(t, filepath.Join(getProjectAgentsDir(rig.cwd), "coder.md"), "---\npermission:\n  read: deny\n---\n# Coder\n")
		m := NewPermissionManager(PermissionManagerOptions{AgentDir: rig.agentDir})
		m.ConfigureForCwd(rig.cwd)
		// Without an agent name: global allow applies.
		eq(t, checkPathOn(m, "foo.txt", "read").State, "allow", "no agent")
		// With the "coder" agent: project-agent deny overrides global allow.
		eq(t, checkPathAs(m, "foo.txt", "", "read", "coder", nil).State, "deny", "coder")
	})
}

func TestPolicyUnifiedAgentScopeTwins(t *testing.T) {
	tw(t, unified, "Project-level config overrides base bash patterns", func(t *testing.T) {
		m := managerWithProject(t, `{"permission":{"*":"allow","bash":{"*":"ask","rm -rf *":"deny"}}}`, nil,
			projectFiles{config: `{"permission":{"bash":{"rm -rf build":"allow"}}}`})
		allowed := checkTool(t, m, "bash", `{"command":"rm -rf build"}`)
		eq(t, []string{allowed.State, pat(allowed)}, []string{"allow", "rm -rf build"}, "allowed")
		denied := checkTool(t, m, "bash", `{"command":"rm -rf node_modules"}`)
		eq(t, []string{denied.State, pat(denied)}, []string{"deny", "rm -rf *"}, "denied")
	})
	tw(t, unified, "System-agent config overrides project-level bash patterns", func(t *testing.T) {
		m := managerWithProject(t, `{"permission":{"*":"allow","bash":"ask"}}`,
			map[string]string{"reviewer": agentFile("reviewer", "  bash:\n    \"git log *\": allow\n")},
			projectFiles{config: `{"permission":{"bash":{"git *":"deny"}}}`})
		allowed := checkToolAs(t, m, "bash", `{"command":"git log --oneline"}`, "reviewer", nil)
		eq(t, []string{allowed.State, pat(allowed)}, []string{"allow", "git log *"}, "allowed")
		denied := checkToolAs(t, m, "bash", `{"command":"git status"}`, "reviewer", nil)
		eq(t, []string{denied.State, pat(denied)}, []string{"deny", "git *"}, "denied")
	})
	tw(t, unified, "Project-agent config overrides system-agent tool rules", func(t *testing.T) {
		m := managerWithProject(t, `{"permission":{"*":"ask"}}`,
			map[string]string{"reviewer": agentFile("reviewer", "  read: deny\n")},
			projectFiles{agentFiles: map[string]string{"reviewer": agentFile("reviewer", "  read: allow\n")}})
		r := checkToolAs(t, m, "read", "{}", "reviewer", nil)
		eq(t, []string{r.State, r.Source}, []string{"allow", "tool"}, "result")
	})
	tw(t, unified, "Full precedence chain base < project < system-agent < project-agent for universal default", func(t *testing.T) {
		m := managerWithProject(t, `{"permission":{"*":"deny"}}`,
			map[string]string{"reviewer": agentFile("reviewer", "  \"*\": ask\n")},
			projectFiles{config: `{"permission":{"*":"allow"}}`, agentFiles: map[string]string{"reviewer": agentFile("reviewer", "  \"*\": deny\n")}})
		reviewer := checkToolAs(t, m, "custom_extension_tool", "{}", "reviewer", nil)
		eq(t, []string{reviewer.State, reviewer.Source}, []string{"deny", "default"}, "reviewer")
		global := checkTool(t, m, "custom_extension_tool", "{}")
		eq(t, []string{global.State, global.Source}, []string{"allow", "default"}, "global")
	})
	tw(t, unified, "Project-agent applies even without a matching system-agent file", func(t *testing.T) {
		m := managerWithProject(t, `{"permission":{"*":"allow"}}`, nil,
			projectFiles{agentFiles: map[string]string{"reviewer": agentFile("reviewer", "  read: deny\n")}})
		agent := checkToolAs(t, m, "read", "{}", "reviewer", nil)
		eq(t, []string{agent.State, agent.Source}, []string{"deny", "tool"}, "agent")
		global := checkTool(t, m, "read", "{}")
		eq(t, []string{global.State, global.Source}, []string{"allow", "tool"}, "global")
	})
}

func TestPolicyUnifiedSurfaceResolutionTwins(t *testing.T) {
	tw(t, unified, "PermissionManager canonical built-in permission checking", func(t *testing.T) {
		m, _ := createManager(t, `{"permission":{"*":"deny","read":"allow"}}`, nil)
		read := checkTool(t, m, "read", "{}")
		eq(t, []string{read.State, read.Source}, []string{"allow", "tool"}, "read")
		write := checkTool(t, m, "write", "{}")
		eq(t, []string{write.State, write.Source}, []string{"deny", "tool"}, "write")
	})
	tw(t, unified, "multiline bash command resolves to allow via universal fallback", func(t *testing.T) {
		// Regression test for #73: node -e "..." with embedded newlines was falling through to the hard-coded 'ask' default because
		// wildcardMatch used /^.*$/ (no dotAll), which does not match '\n'.
		m, _ := createManager(t, `{"permission":{"*":"allow","bash":{"rm -rf *":"deny","sudo *":"ask"}}}`, nil)
		command := "node -e \"\nimport('x').then(() => {\n  console.log('done');\n});\n\""
		o := newObject()
		o.set("command", command)
		r := m.Check(ToolAccessIntent{Surface: "bash", Input: o}, nil)
		eq(t, r.State, "allow", "state")
	})
	tw(t, unified, "Bash specific deny patterns override catch-all within the same config", func(t *testing.T) {
		// In the flat format, patterns within a surface map are ordered by insertion. Last-match-wins means specific patterns placed
		// AFTER the catch-all override it.
		m, _ := createManager(t, `{"permission":{"*":"ask","bash":{"*":"allow","rm -rf *":"deny"}}}`, nil)
		denied := checkTool(t, m, "bash", `{"command":"rm -rf build"}`)
		eq(t, []string{denied.State, denied.Source, pat(denied)}, []string{"deny", "bash", "rm -rf *"}, "denied")
		allowed := checkTool(t, m, "bash", `{"command":"echo hello"}`)
		eq(t, []string{allowed.State, allowed.Source, pat(allowed)}, []string{"allow", "bash", "*"}, "allowed")
	})
	tw(t, unified, "MCP wildcard matching uses the registered mcp tool", func(t *testing.T) {
		m, _ := createManager(t, `{"permission":{"*":"ask","mcp":{"*":"deny","research_*":"ask","research_query-*":"allow"}}}`, nil)
		queryDocs := checkTool(t, m, "mcp", `{"tool":"research:query-docs"}`)
		eq(t, []string{queryDocs.State, queryDocs.Source, pat(queryDocs), targetOf(queryDocs)}, []string{"allow", "mcp", "research_query-*", "research_query-docs"}, "queryDocs")
		resolve2 := checkTool(t, m, "mcp", `{"tool":"research:resolve-context"}`)
		eq(t, []string{resolve2.State, pat(resolve2), targetOf(resolve2)}, []string{"ask", "research_*", "research_resolve-context"}, "resolve")
		unknown := checkTool(t, m, "mcp", `{"tool":"search:provider"}`)
		eq(t, []string{unknown.State, pat(unknown), targetOf(unknown)}, []string{"deny", "*", "search_provider"}, "unknown")
	})
	tw(t, unified, "Arbitrary extension tools use exact-name tool permissions instead of MCP fallback", func(t *testing.T) {
		m, _ := createManager(t, `{"permission":{"*":"deny","third_party_tool":"allow","mcp":{"*":"deny"}}}`, nil)
		allowed := checkTool(t, m, "third_party_tool", "{}")
		eq(t, []string{allowed.State, allowed.Source}, []string{"allow", "tool"}, "allowed")
		// another_extension_tool has no explicit rule: falls through to the universal default with source "default".
		fallback := checkTool(t, m, "another_extension_tool", "{}")
		eq(t, []string{fallback.State, fallback.Source}, []string{"deny", "default"}, "fallback")
	})
	tw(t, unified, "Skill permission matching", func(t *testing.T) {
		m, _ := createManager(t, `{"permission":{"*":"ask","skill":{"*":"ask","web-*":"deny","requesting-code-review":"allow"}}}`, nil)
		allowed := checkTool(t, m, "skill", `{"name":"requesting-code-review"}`)
		eq(t, []string{allowed.State, pat(allowed), allowed.Source}, []string{"allow", "requesting-code-review", "skill"}, "allowed")
		denied := checkTool(t, m, "skill", `{"name":"web-design-guidelines"}`)
		eq(t, []string{denied.State, pat(denied)}, []string{"deny", "web-*"}, "denied")
		fallback := checkTool(t, m, "skill", `{"name":"unknown-skill"}`)
		eq(t, []string{fallback.State, pat(fallback)}, []string{"ask", "*"}, "fallback")
	})
	tw(t, unified, "MCP proxy tool infers server-prefixed aliases from configured server names", func(t *testing.T) {
		m, _ := createManager(t, `{"permission":{"*":"ask","mcp":{"exa_*":"deny","exa_get_code_context_exa":"allow"}}}`, nil, "exa")
		r := checkTool(t, m, "mcp", `{"tool":"get_code_context_exa"}`)
		eq(t, []string{r.State, r.Source, pat(r), targetOf(r)}, []string{"allow", "mcp", "exa_get_code_context_exa", "exa_get_code_context_exa"}, "result")
	})
	tw(t, unified, "MCP server names in settings.json are not used — only mcp.json is consulted", func(t *testing.T) {
		base := t.TempDir()
		globalConfigPath := filepath.Join(base, "pi-permissions.jsonc")
		mcpConfigPath := filepath.Join(base, "mcp.json")
		agentsDir := filepath.Join(base, "agents")
		if err := os.MkdirAll(agentsDir, 0o777); err != nil {
			t.Fatal(err)
		}
		polWriteFile(t, globalConfigPath, `{"permission":{"*":"ask","mcp":{"legacy-server_*":"allow"}}}`+"\n")
		polWriteFile(t, mcpConfigPath, `{"mcpServers":{}}`)
		polWriteFile(t, filepath.Join(base, "settings.json"), `{"mcpServers":{"legacy-server":{}}}`)
		m := NewPermissionManager(PermissionManagerOptions{PolicyLoaderOptions: PolicyLoaderOptions{
			GlobalConfigPath: globalConfigPath, AgentsDir: agentsDir, GlobalMcpConfigPath: mcpConfigPath}})
		eq(t, checkTool(t, m, "mcp", `{"tool":"some_tool_legacy-server"}`).State, "ask", "state")
	})
	tw(t, unified, "MCP describe mode normalizes qualified tool names without duplicating server prefixes", func(t *testing.T) {
		m, _ := createManager(t, `{"permission":{"*":"ask","mcp":{"exa_*":"deny","exa_web_search_exa":"allow"}}}`, nil, "exa")
		r := checkTool(t, m, "mcp", `{"describe":"exa:web_search_exa","server":"exa"}`)
		eq(t, []string{r.State, r.Source, pat(r), targetOf(r)}, []string{"allow", "mcp", "exa_web_search_exa", "exa_web_search_exa"}, "result")
	})
	tw(t, unified, "Canonical tools map directly without legacy aliases", func(t *testing.T) {
		m, _ := createManager(t, `{"permission":{"*":"ask","find":"allow","ls":"deny"}}`, nil)
		find := checkTool(t, m, "find", "{}")
		eq(t, []string{find.State, find.Source}, []string{"allow", "tool"}, "find")
		ls := checkTool(t, m, "ls", "{}")
		eq(t, []string{ls.State, ls.Source}, []string{"deny", "tool"}, "ls")
	})
	tw(t, unified, "mcp catch-all acts as fallback for unmatched MCP targets", func(t *testing.T) {
		m, _ := createManager(t, `{"permission":{"*":"ask"}}`, map[string]string{"reviewer": agentFile("reviewer", "  mcp: allow\n")})
		r := checkToolAs(t, m, "mcp", `{"tool":"exa:web_search_exa"}`, "reviewer", nil)
		eq(t, []string{r.State, r.Source, targetOf(r)}, []string{"allow", "mcp", "exa_web_search_exa"}, "result")
	})
	tw(t, unified, "specific MCP rules override mcp catch-all", func(t *testing.T) {
		m, _ := createManager(t, `{"permission":{"*":"ask"}}`,
			map[string]string{"reviewer": agentFile("reviewer", "  mcp:\n    \"*\": allow\n    exa_web_search_exa: deny\n")}, "exa")
		r := checkToolAs(t, m, "mcp", `{"tool":"web_search_exa"}`, "reviewer", nil)
		eq(t, []string{r.State, r.Source, pat(r), targetOf(r)}, []string{"deny", "mcp", "exa_web_search_exa", "exa_web_search_exa"}, "result")
	})
	tw(t, unified, "specific MCP rules still win when mcp catch-all is deny", func(t *testing.T) {
		m, _ := createManager(t, `{"permission":{"*":"ask"}}`,
			map[string]string{"reviewer": agentFile("reviewer", "  mcp:\n    \"*\": deny\n    exa_web_search_exa: allow\n")}, "exa")
		allowed := checkToolAs(t, m, "mcp", `{"tool":"web_search_exa"}`, "reviewer", nil)
		eq(t, []string{allowed.State, allowed.Source, pat(allowed), targetOf(allowed)}, []string{"allow", "mcp", "exa_web_search_exa", "exa_web_search_exa"}, "allowed")
		fallback := checkToolAs(t, m, "mcp", `{"tool":"other_exa"}`, "reviewer", nil)
		eq(t, []string{fallback.State, fallback.Source, targetOf(fallback)}, []string{"deny", "mcp", "exa_other_exa"}, "fallback")
	})
	tw(t, unified, "mcp catch-all in agent frontmatter overrides global default", func(t *testing.T) {
		m, _ := createManager(t, `{"permission":{"*":"deny"}}`, map[string]string{"reviewer": agentFile("reviewer", "  mcp: allow\n")})
		read := checkToolAs(t, m, "read", "{}", "reviewer", nil)
		eq(t, []string{read.State, read.Source}, []string{"deny", "tool"}, "read")
		mcp := checkToolAs(t, m, "mcp", `{"tool":"exa:web_search_exa"}`, "reviewer", nil)
		eq(t, []string{mcp.State, mcp.Source}, []string{"allow", "mcp"}, "mcp")
	})
	tw(t, unified, "Agent frontmatter canonical tools resolve correctly", func(t *testing.T) {
		m, _ := createManager(t, `{"permission":{"*":"deny"}}`, map[string]string{"reviewer": agentFile("reviewer", "  find: allow\n  ls: deny\n")})
		find := checkToolAs(t, m, "find", "{}", "reviewer", nil)
		eq(t, []string{find.State, find.Source}, []string{"allow", "tool"}, "find")
		ls := checkToolAs(t, m, "ls", "{}", "reviewer", nil)
		eq(t, []string{ls.State, ls.Source}, []string{"deny", "tool"}, "ls")
	})
	tw(t, unified, "All surface names work in agent frontmatter flat permission format", func(t *testing.T) {
		m, _ := createManager(t, `{"permission":{"*":"deny"}}`, map[string]string{"reviewer": agentFile("reviewer", "  find: allow\n  task: allow\n  mcp: allow\n")})
		find := checkToolAs(t, m, "find", "{}", "reviewer", nil)
		eq(t, []string{find.State, find.Source}, []string{"allow", "tool"}, "find")
		task := checkToolAs(t, m, "task", "{}", "reviewer", nil)
		eq(t, []string{task.State, task.Source}, []string{"allow", "tool"}, "task")
		eq(t, checkToolAs(t, m, "mcp", `{"tool":"exa:web_search_exa"}`, "reviewer", nil).State, "allow", "mcp")
	})
	tw(t, unified, "task uses exact-name tool permissions like any registered extension tool", func(t *testing.T) {
		m, _ := createManager(t, `{"permission":{"*":"deny","task":"allow"}}`, nil)
		r := checkTool(t, m, "task", "{}")
		eq(t, []string{r.State, r.Source}, []string{"allow", "tool"}, "result")
	})
	tw(t, unified, "getToolPermission returns tool-level policy for canonical and extension tools", func(t *testing.T) {
		m, _ := createManager(t, `{"permission":{"*":"ask"}}`, map[string]string{"reviewer": agentFile("reviewer", "  bash: deny\n  read: deny\n  task: allow\n")})
		eq(t, m.GetToolPermission("bash", sp("reviewer")), "deny", "bash")
		eq(t, m.GetToolPermission("task", sp("reviewer")), "allow", "task")
		eq(t, m.GetToolPermission("read", sp("reviewer")), "deny", "read")
		eq(t, m.GetToolPermission("bash", nil), "ask", "default bash")
		m2, _ := createManager(t, `{"permission":{"*":"deny","bash":"allow"}}`, nil)
		eq(t, m2.GetToolPermission("bash", nil), "allow", "global bash")
	})
	tw(t, unified, "getToolPermission supports arbitrary extension tool names", func(t *testing.T) {
		m, _ := createManager(t, `{"permission":{"*":"deny","third_party_tool":"allow"}}`, nil)
		eq(t, m.GetToolPermission("third_party_tool", nil), "allow", "explicit")
		eq(t, m.GetToolPermission("missing_extension_tool", nil), "deny", "fallback")
	})
}

func TestPolicyUnifiedExternalDirectoryTwins(t *testing.T) {
	useFakeHome(t)
	tw(t, unified, "external_directory permission falls back to universal default when not explicitly configured", func(t *testing.T) {
		m, _ := createManager(t, `{"permission":{}}`, nil)
		r := checkTool(t, m, "external_directory_read", "{}")
		eq(t, []string{r.State, r.Source, pat(r)}, []string{"ask", "special", "<undefined>"}, "result")
	})
	tw(t, unified, "external_directory permission respects explicit deny", func(t *testing.T) {
		m, _ := createManager(t, `{"permission":{"*":"allow","external_directory":"deny"}}`, nil)
		r := checkTool(t, m, "external_directory_read", "{}")
		eq(t, []string{r.State, r.Source, pat(r)}, []string{"deny", "special", "*"}, "result")
	})
	tw(t, unified, "external_directory permission can be explicitly allowed", func(t *testing.T) {
		m, _ := createManager(t, `{"permission":{"*":"allow","external_directory":"allow"}}`, nil)
		r := checkTool(t, m, "external_directory_read", "{}")
		eq(t, []string{r.State, r.Source, pat(r)}, []string{"allow", "special", "*"}, "result")
	})
	tw(t, unified, "external_directory permission respects per-agent override", func(t *testing.T) {
		m, _ := createManager(t, `{"permission":{"*":"allow","external_directory":"deny"}}`, map[string]string{"trusted": agentFile("trusted", "  external_directory: allow\n")})
		eq(t, checkTool(t, m, "external_directory_read", "{}").State, "deny", "global")
		agent := checkToolAs(t, m, "external_directory_read", "{}", "trusted", nil)
		eq(t, []string{agent.State, agent.Source}, []string{"allow", "special"}, "agent")
	})
	tw(t, unified, "external_directory permission is not affected by unrelated surface keys", func(t *testing.T) {
		m, _ := createManager(t, `{"permission":{"*":"allow","external_directory":"allow"}}`, nil)
		r := checkTool(t, m, "external_directory_read", "{}")
		eq(t, []string{r.State, pat(r)}, []string{"allow", "*"}, "result")
	})
	tw(t, unified, "skill pattern map in agent frontmatter overrides global skill policy", func(t *testing.T) {
		m, _ := createManager(t, `{"permission":{"*":"deny","skill":"deny"}}`,
			map[string]string{"reviewer": agentFile("reviewer", "  skill:\n    \"*\": ask\n    \"pi-*\": allow\n")})
		allowed := checkToolAs(t, m, "skill", `{"name":"pi-code-review"}`, "reviewer", nil)
		eq(t, []string{allowed.State, pat(allowed), allowed.Source}, []string{"allow", "pi-*", "skill"}, "allowed")
		asked := checkToolAs(t, m, "skill", `{"name":"other-skill"}`, "reviewer", nil)
		eq(t, []string{asked.State, pat(asked)}, []string{"ask", "*"}, "asked")
		denied := checkTool(t, m, "skill", `{"name":"pi-code-review"}`)
		eq(t, []string{denied.State, denied.Source}, []string{"deny", "skill"}, "denied")
	})
	tw(t, unified, "external_directory pattern map in agent frontmatter overrides global policy", func(t *testing.T) {
		m, _ := createManager(t, `{"permission":{"*":"allow","external_directory":"deny"}}`,
			map[string]string{"trusted": agentFile("trusted", "  external_directory:\n    \"*\": deny\n    \"~/Downloads/*\": allow\n")})
		allowed := checkPathAs(m, fakeHome+"/Downloads/file.txt", "", "external_directory_read", "trusted", nil)
		eq(t, []string{allowed.State, pat(allowed), allowed.Source}, []string{"allow", "~/Downloads/*", "special"}, "allowed")
		denied := checkPathAs(m, fakeHome+"/Documents/secret.txt", "", "external_directory_read", "trusted", nil)
		eq(t, []string{denied.State, pat(denied)}, []string{"deny", "*"}, "denied")
		globalDenied := checkTool(t, m, "external_directory_read", "{}")
		eq(t, []string{globalDenied.State, globalDenied.Source}, []string{"deny", "special"}, "global")
	})
	tw(t, unified, "project-agent frontmatter skill rules override global-agent frontmatter skill rules", func(t *testing.T) {
		m := managerWithProject(t, `{"permission":{"*":"deny"}}`,
			map[string]string{"analyst": agentFile("analyst", "  skill:\n    \"*\": ask\n")},
			projectFiles{agentFiles: map[string]string{"analyst": agentFile("analyst", "  skill:\n    \"pi-*\": allow\n    \"*\": deny\n")}})
		allowed := checkToolAs(t, m, "skill", `{"name":"pi-code-review"}`, "analyst", nil)
		eq(t, []string{allowed.State, pat(allowed)}, []string{"allow", "pi-*"}, "allowed")
		denied := checkToolAs(t, m, "skill", `{"name":"other-skill"}`, "analyst", nil)
		eq(t, []string{denied.State, pat(denied)}, []string{"deny", "*"}, "denied")
	})
	tw(t, unified, "project-agent frontmatter external_directory rules override global-agent frontmatter rules", func(t *testing.T) {
		m := managerWithProject(t, `{"permission":{"*":"allow","external_directory":"deny"}}`,
			map[string]string{"analyst": agentFile("analyst", "  external_directory: ask\n")},
			projectFiles{agentFiles: map[string]string{"analyst": agentFile("analyst", "  external_directory: allow\n")}})
		r := checkToolAs(t, m, "external_directory_read", "{}", "analyst", nil)
		eq(t, []string{r.State, r.Source}, []string{"allow", "special"}, "result")
		eq(t, checkTool(t, m, "external_directory_read", "{}").State, "deny", "global")
	})

	tw(t, unified, "PermissionManager reads config from PI_CODING_AGENT_DIR when set", func(t *testing.T) {
		base := t.TempDir()
		polWriteFile(t, getGlobalConfigPath(base), `{"permission":{"*":"deny","read":"allow"}}`)
		t.Setenv("PI_CODING_AGENT_DIR", base)
		m := NewPermissionManager(PermissionManagerOptions{})
		eq(t, checkTool(t, m, "read", "{}").State, "allow", "read")
		eq(t, checkTool(t, m, "write", "{}").State, "deny", "write")
	})

	tw(t, unified, "PermissionManager.getConfigIssues returns empty array for clean config", func(t *testing.T) {
		m, _ := createManager(t, `{"permission":{"*":"ask","external_directory":"ask"}}`, nil)
		eq(t, len(m.GetConfigIssues(nil)), 0, "issues")
	})
	tw(t, unified, "PermissionManager.getConfigIssues returns empty array for empty config", func(t *testing.T) {
		m, _ := createManager(t, `{}`, nil)
		eq(t, len(m.GetConfigIssues(nil)), 0, "issues")
	})
}

func TestPolicyUnifiedSessionAwareTwins(t *testing.T) {
	tw(t, unified, "checkPermission returns source 'session' when session rules cover the external_directory path", func(t *testing.T) {
		m, _ := createManager(t, `{"permission":{"*":"allow"}}`, nil)
		r := checkPathAs(m, "/other/project/src/foo.ts", "", "external_directory_read", "", Ruleset{sessionRule("external_directory_read", "/other/project/*")})
		eq(t, []string{r.State, r.Source, pat(r)}, []string{"allow", "session", "/other/project/*"}, "result")
	})
	tw(t, unified, "checkPermission falls back to config policy when session rules do not cover the path", func(t *testing.T) {
		m, _ := createManager(t, `{"permission":{"*":"allow","external_directory":"deny"}}`, nil)
		r := checkToolAs(t, m, "external_directory_read", `{"path":"/completely/different/path.ts"}`, "", Ruleset{sessionRule("external_directory_read", "/other/project/*")})
		eq(t, []string{r.State, r.Source}, []string{"deny", "special"}, "result")
	})
	tw(t, unified, "checkPermission with empty session rules is identical to call without sessionRules arg", func(t *testing.T) {
		m, _ := createManager(t, `{"permission":{"*":"allow","external_directory":"deny"}}`, nil)
		withEmpty := checkPathAs(m, "/other/project/foo.ts", "", "external_directory_read", "", Ruleset{})
		withoutArg := checkPathAs(m, "/other/project/foo.ts", "", "external_directory_read", "", nil)
		want := PermissionCheckResult{ToolName: "external_directory_read", State: "deny", MatchedPattern: sp("*"), Source: "special", Origin: "global"}
		eq(t, withEmpty, want, "with empty")
		eq(t, withoutArg, want, "without arg")
	})
	tw(t, unified, "session rules for one surface do not affect checks on other surfaces", func(t *testing.T) {
		m, _ := createManager(t, `{"permission":{}}`, nil)
		rules := Ruleset{sessionRule("external_directory_read", "/other/project/*")}
		bash := checkToolAs(t, m, "bash", `{"command":"git status"}`, "", rules)
		eq(t, []string{bash.State, bash.Source}, []string{"ask", "bash"}, "bash")
		mcp := checkToolAs(t, m, "mcp", `{"tool":"exa:search"}`, "", rules)
		eq(t, []string{mcp.State, mcp.Source}, []string{"ask", "default"}, "mcp")
	})
	tw(t, unified, "session rules override config deny for external_directory", func(t *testing.T) {
		m, _ := createManager(t, `{"permission":{"*":"allow","external_directory":"deny"}}`, nil)
		r := checkPathAs(m, "/other/project/src/foo.ts", "", "external_directory_read", "", Ruleset{sessionRule("external_directory_read", "/other/project/*")})
		eq(t, []string{r.State, r.Source}, []string{"allow", "session"}, "result")
	})
	tw(t, unified, "checkPermission returns source 'session' for bash when session rules match", func(t *testing.T) {
		m, _ := createManager(t, `{"permission":{}}`, nil)
		r := checkToolAs(t, m, "bash", `{"command":"git status --short"}`, "", Ruleset{sessionRule("bash", "git *")})
		eq(t, []string{r.State, r.Source, pat(r)}, []string{"allow", "session", "git *"}, "result")
	})
	tw(t, unified, "checkPermission returns source 'session' for bash when session rule is exact match", func(t *testing.T) {
		m, _ := createManager(t, `{"permission":{}}`, nil)
		r := checkToolAs(t, m, "bash", `{"command":"ls"}`, "", Ruleset{sessionRule("bash", "ls")})
		eq(t, []string{r.State, r.Source}, []string{"allow", "session"}, "result")
	})
	tw(t, unified, "checkPermission falls back to config for bash when session rules do not match the command", func(t *testing.T) {
		m, _ := createManager(t, `{"permission":{"bash":"deny"}}`, nil)
		r := checkToolAs(t, m, "bash", `{"command":"npm run build"}`, "", Ruleset{sessionRule("bash", "git *")})
		eq(t, []string{r.State, r.Source}, []string{"deny", "bash"}, "result")
	})
	tw(t, unified, "checkPermission returns source 'session' for mcp when session rules match the target", func(t *testing.T) {
		m, _ := createManager(t, `{"permission":{}}`, nil)
		r := checkToolAs(t, m, "mcp", `{"tool":"exa:search"}`, "", Ruleset{sessionRule("mcp", "exa:*")})
		eq(t, []string{r.State, r.Source}, []string{"allow", "session"}, "result")
	})
	tw(t, unified, "checkPermission returns source 'session' for skill when session rules match", func(t *testing.T) {
		m, _ := createManager(t, `{"permission":{}}`, nil)
		r := checkToolAs(t, m, "skill", `{"name":"librarian"}`, "", Ruleset{sessionRule("skill", "librarian")})
		eq(t, []string{r.State, r.Source, pat(r)}, []string{"allow", "session", "librarian"}, "result")
	})
	tw(t, unified, "checkPermission returns source 'session' for tool surface when session rules match", func(t *testing.T) {
		m, _ := createManager(t, `{"permission":{}}`, nil)
		r := checkToolAs(t, m, "read", "{}", "", Ruleset{sessionRule("read", "*")})
		eq(t, []string{r.State, r.Source}, []string{"allow", "session"}, "result")
	})
	tw(t, unified, "bash session rules do not bleed into mcp checks", func(t *testing.T) {
		m, _ := createManager(t, `{"permission":{}}`, nil)
		r := checkToolAs(t, m, "mcp", `{"tool":"exa:search"}`, "", Ruleset{sessionRule("bash", "git *")})
		eq(t, r.Source != "session", true, "source")
	})
}

func TestPolicyUnifiedResolvedPolicyPathsTwins(t *testing.T) {
	tw(t, unified, "getResolvedPolicyPaths returns correct paths and existence when files exist", func(t *testing.T) {
		dir := t.TempDir()
		globalConfigPath := filepath.Join(dir, "pi-permissions.jsonc")
		agentsDir := filepath.Join(dir, "agents")
		projectConfigPath := filepath.Join(dir, "project", "pi-permissions.jsonc")
		projectAgentsDir := filepath.Join(dir, "project", "agents")
		polWriteFile(t, globalConfigPath, "{}")
		for _, d := range []string{agentsDir, filepath.Join(dir, "project"), projectAgentsDir} {
			if err := os.MkdirAll(d, 0o777); err != nil {
				t.Fatal(err)
			}
		}
		polWriteFile(t, projectConfigPath, "{}")
		pm := NewPermissionManager(PermissionManagerOptions{PolicyLoaderOptions: PolicyLoaderOptions{
			GlobalConfigPath: globalConfigPath, AgentsDir: agentsDir, ProjectGlobalConfigPath: projectConfigPath, ProjectAgentsDir: projectAgentsDir}})
		eq(t, pm.GetResolvedPolicyPaths(), ResolvedPolicyPaths{
			GlobalConfigPath: globalConfigPath, GlobalConfigExists: true, ProjectConfigPath: &projectConfigPath, ProjectConfigExists: true,
			AgentsDir: agentsDir, AgentsDirExists: true, ProjectAgentsDir: &projectAgentsDir, ProjectAgentsDirExists: true}, "paths")
	})
	tw(t, unified, "getResolvedPolicyPaths returns false for missing files and null for absent project paths", func(t *testing.T) {
		dir := t.TempDir()
		globalConfigPath := filepath.Join(dir, "does-not-exist.jsonc")
		agentsDir := filepath.Join(dir, "no-agents")
		pm := NewPermissionManager(PermissionManagerOptions{PolicyLoaderOptions: PolicyLoaderOptions{GlobalConfigPath: globalConfigPath, AgentsDir: agentsDir}})
		eq(t, pm.GetResolvedPolicyPaths(), ResolvedPolicyPaths{GlobalConfigPath: globalConfigPath, AgentsDir: agentsDir}, "paths")
	})
}

func TestPolicyUnifiedCwdAwareTwins(t *testing.T) {
	const cwd = "/workspace/project"
	tw(t, unified, "matches a relative read input against an absolute allowlist", func(t *testing.T) {
		m := managerWithConfig(t, `{"read":{"*":"ask","`+cwd+`/*":"allow"}}`)
		m.ConfigureForCwd(cwd)
		r := checkPathAs(m, "src/App.jsx", cwd, "read", "", nil)
		eq(t, []string{r.State, pat(r)}, []string{"allow", cwd + "/*"}, "result")
	})
	tw(t, unified, "keeps legacy relative path rules working after configureForCwd", func(t *testing.T) {
		m := managerWithConfig(t, `{"read":{"*":"allow","src/*":"deny"}}`)
		m.ConfigureForCwd(cwd)
		r := checkPathAs(m, "src/App.jsx", cwd, "read", "", nil)
		eq(t, []string{r.State, pat(r)}, []string{"deny", "src/*"}, "result")
	})
	tw(t, unified, "preserves last-match-wins across absolute and relative aliases", func(t *testing.T) {
		m := managerWithConfig(t, `{"read":{"*":"ask","`+cwd+`/*":"allow","src/*":"deny"}}`)
		m.ConfigureForCwd(cwd)
		r := checkPathAs(m, "src/App.jsx", cwd, "read", "", nil)
		// The later "src/*" deny wins over the earlier absolute allow.
		eq(t, []string{r.State, pat(r)}, []string{"deny", "src/*"}, "result")
	})
	tw(t, unified, "matches the cross-cutting path surface against absolute allowlists", func(t *testing.T) {
		m := managerWithConfig(t, `{"path":{"*":"ask","`+cwd+`/*":"allow"}}`)
		m.ConfigureForCwd(cwd)
		r := checkPathAs(m, "src/App.jsx", cwd, "path_read", "", nil)
		eq(t, []string{r.State, pat(r)}, []string{"allow", cwd + "/*"}, "result")
	})
}

func TestPolicyUnifiedCheckPathPolicyTwins(t *testing.T) {
	const cwd = "/workspace/project"
	vals := []string{cwd + "/src/App.jsx", "src/App.jsx"}
	tw(t, unified, "evaluates precomputed policy values against the path surface", func(t *testing.T) {
		m := managerWithConfig(t, `{"path":{"*":"ask","`+cwd+`/*":"allow"}}`)
		r := checkValues(m, "path_read", vals, "", nil)
		eq(t, []string{r.State, pat(r), r.Source, r.ToolName}, []string{"allow", cwd + "/*", "special", "path_read"}, "result")
	})
	tw(t, unified, "preserves last-match-wins across the provided aliases", func(t *testing.T) {
		m := managerWithConfig(t, `{"path":{"*":"ask","`+cwd+`/*":"allow","src/*":"deny"}}`)
		r := checkValues(m, "path_read", vals, "", nil)
		eq(t, []string{r.State, pat(r)}, []string{"deny", "src/*"}, "result")
	})
	tw(t, unified, "applies session rules over config", func(t *testing.T) {
		m := managerWithConfig(t, `{"path":{"*":"ask","src/*":"deny"}}`)
		r := checkValues(m, "path_read", []string{"src/App.jsx"}, "", Ruleset{sessionRule("path_read", "src/*")})
		eq(t, []string{r.State, r.Source}, []string{"allow", "session"}, "result")
	})
	tw(t, unified, "falls back to the catch-all for an empty value list", func(t *testing.T) {
		r := checkValues(managerWithConfig(t, `{"path":{"*":"deny"}}`), "path_read", []string{}, "", nil)
		eq(t, []string{r.State, pat(r)}, []string{"deny", "*"}, "result")
	})
	tw(t, unified, "evaluates against the external_directory surface when one is provided", func(t *testing.T) {
		m := managerWithConfig(t, `{"external_directory":{"*":"ask","/tmp/*":"allow"}}`)
		r := checkValues(m, "external_directory_read", []string{"/tmp/x"}, "", nil)
		eq(t, []string{r.State, pat(r), r.Source, r.ToolName}, []string{"allow", "/tmp/*", "special", "external_directory_read"}, "result")
	})
	tw(t, unified, "defaults to the path surface when no surface is provided", func(t *testing.T) {
		m := managerWithConfig(t, `{"external_directory":{"*":"ask","/tmp/*":"allow"},"path":{"*":"allow"}}`)
		// No path rule denies; the external_directory allow must NOT apply here.
		r := checkPath(m, "/tmp/x")
		eq(t, []string{r.ToolName, r.State, pat(r)}, []string{"path_read", "allow", "*"}, "result")
	})
}

func TestPolicyUnifiedCheckIntentTwins(t *testing.T) {
	const cwd = "/workspace/project"
	vals := []string{cwd + "/src/App.jsx", "src/App.jsx"}
	// check: tool intent
	tw(t, unified, "resolves a tool call on the bash surface", func(t *testing.T) {
		m := managerWithConfig(t, `{"bash":{"*":"allow","git push":"deny"}}`)
		r := m.Check(ToolAccessIntent{Surface: "bash", Input: jo(t, `{"command":"git push"}`)}, nil)
		eq(t, []string{r.State, r.ToolName, r.Source}, []string{"deny", "bash", "bash"}, "result")
	})
	tw(t, unified, "resolves a tool call on the read surface", func(t *testing.T) {
		m := managerWithConfig(t, `{"read":"deny"}`)
		r := m.Check(ToolAccessIntent{Surface: "read", Input: jo(t, `{"path":"/some/file.txt"}`)}, nil)
		eq(t, []string{r.State, r.ToolName, r.Source}, []string{"deny", "read", "tool"}, "result")
	})
	tw(t, unified, "applies session rules via the tool intent", func(t *testing.T) {
		m := managerWithConfig(t, `{"bash":{"*":"deny"}}`)
		r := m.Check(ToolAccessIntent{Surface: "bash", Input: jo(t, `{"command":"echo hello"}`)}, Ruleset{sessionRule("bash", "echo *")})
		eq(t, []string{r.State, r.Source}, []string{"allow", "session"}, "result")
	})
	tw(t, unified, "threads agentName through the tool intent", func(t *testing.T) {
		m, _ := createManager(t, `{"permission":{"bash":"deny"}}`, map[string]string{"agent-a": "---\nname: agent-a\npermission:\n  bash: allow\n---\n"})
		r := m.Check(ToolAccessIntent{Surface: "bash", Input: jo(t, `{"command":"echo hi"}`), AgentName: sp("agent-a")}, nil)
		eq(t, r.State, "allow", "state")
	})
	// check: path-values intent
	tw(t, unified, "evaluates precomputed policy values against the path surface", func(t *testing.T) {
		m := managerWithConfig(t, `{"path":{"*":"ask","`+cwd+`/*":"allow"}}`)
		r := m.Check(PathValuesAccessIntent{Surface: "path_read", Values: vals}, nil)
		eq(t, []string{r.State, pat(r), r.Source, r.ToolName}, []string{"allow", cwd + "/*", "special", "path_read"}, "result")
	})
	tw(t, unified, "evaluates against the external_directory surface", func(t *testing.T) {
		m := managerWithConfig(t, `{"external_directory":{"*":"ask","/tmp/*":"allow"}}`)
		r := m.Check(PathValuesAccessIntent{Surface: "external_directory_read", Values: []string{"/tmp/x"}}, nil)
		eq(t, []string{r.State, pat(r), r.Source, r.ToolName}, []string{"allow", "/tmp/*", "special", "external_directory_read"}, "result")
	})
	tw(t, unified, "falls back to the catch-all for an empty value list", func(t *testing.T) {
		m := managerWithConfig(t, `{"path":{"*":"deny"}}`)
		r := m.Check(PathValuesAccessIntent{Surface: "path_read", Values: []string{}}, nil)
		eq(t, []string{r.State, pat(r)}, []string{"deny", "*"}, "result")
	})
	tw(t, unified, "applies session rules via the path-values intent", func(t *testing.T) {
		m := managerWithConfig(t, `{"path":{"*":"ask","src/*":"deny"}}`)
		r := m.Check(PathValuesAccessIntent{Surface: "path_read", Values: []string{"src/App.jsx"}}, Ruleset{sessionRule("path_read", "src/*")})
		eq(t, []string{r.State, r.Source}, []string{"allow", "session"}, "result")
	})
	tw(t, unified, "last-match-wins across the provided aliases", func(t *testing.T) {
		m := managerWithConfig(t, `{"path":{"*":"ask","`+cwd+`/*":"allow","src/*":"deny"}}`)
		r := m.Check(PathValuesAccessIntent{Surface: "path_read", Values: vals}, nil)
		eq(t, []string{r.State, pat(r)}, []string{"deny", "src/*"}, "result")
	})
}

func TestPolicyUnifiedMcpLastMatchWinsTwins(t *testing.T) {
	documented := `{"mcp":{"*":"ask","mcp_status":"allow","mcp_list":"allow","myServer:*":"ask","dangerousServer":"deny"}}`
	tw(t, unified, "grants the documented mcp_list allow for a server listing", func(t *testing.T) {
		r := checkTool(t, managerWithConfig(t, documented, "myServer", "dangerousServer"), "mcp", `{"server":"myServer"}`)
		eq(t, []string{r.State, pat(r)}, []string{"allow", "mcp_list"}, "result")
	})
	tw(t, unified, "enforces the documented dangerousServer deny for an explicit server", func(t *testing.T) {
		r := checkTool(t, managerWithConfig(t, documented, "myServer", "dangerousServer"), "mcp", `{"tool":"wipe","server":"dangerousServer"}`)
		eq(t, []string{r.State, pat(r)}, []string{"deny", "dangerousServer"}, "result")
	})

	// a catch-all above a specific rule no longer masks it
	tw(t, unified, "allows a discovery operation named after the catch-all", func(t *testing.T) {
		// The shape reported in #687: the wildcard matches the tool-name candidate before the `mcp_describe` candidate is reached.
		r := checkTool(t, managerWithConfig(t, `{"mcp":{"*":"ask","mcp_describe":"allow"}}`, "github"), "mcp", `{"describe":"github_list_issues"}`)
		eq(t, []string{r.State, pat(r)}, []string{"allow", "mcp_describe"}, "result")
	})
	inputs := []struct{ label, input string }{
		{"a suffix-named tool", `{"tool":"search_code_github"}`},
		{"a qualified tool name", `{"tool":"github:search_code"}`},
		{"an explicit server argument", `{"tool":"search_code","server":"github"}`},
	}
	// the original's it.each blocks: not in the ledger
	t.Run("denies the named server for %s", func(t *testing.T) {
		for _, c := range inputs {
			t.Run(c.label, func(t *testing.T) {
				r := checkTool(t, managerWithConfig(t, `{"mcp":{"*":"allow","github":"deny"}}`, "github"), "mcp", c.input)
				eq(t, []string{r.State, pat(r)}, []string{"deny", "github"}, "result")
			})
		}
	})
	// rule position decides, not rule specificity: a catch-all written last still wins
	t.Run("lets a trailing catch-all override the server rule for %s", func(t *testing.T) {
		for _, c := range inputs {
			t.Run(c.label, func(t *testing.T) {
				r := checkTool(t, managerWithConfig(t, `{"mcp":{"github":"deny","*":"ask"}}`, "github"), "mcp", c.input)
				eq(t, []string{r.State, pat(r)}, []string{"ask", "*"}, "result")
			})
		}
	})

	tw(t, unified, "honors a session grant matching a later candidate than the config rule", func(t *testing.T) {
		m := managerWithConfig(t, `{"mcp":{"exa_search":"deny"}}`, "exa")
		r := checkToolAs(t, m, "mcp", `{"tool":"exa_search"}`, "", Ruleset{sessionRule("mcp", "mcp_call")})
		eq(t, []string{r.State, r.Source}, []string{"allow", "session"}, "result")
	})

	// baseline discovery auto-allow
	tw(t, unified, "attributes a describe to the server rule once a candidate names it", func(t *testing.T) {
		// Prefix derivation (#928) gives this call a `github` candidate, so the config rule outranks the baseline.
		r := checkTool(t, managerWithConfig(t, `{"mcp":{"github":"allow"}}`, "github"), "mcp", `{"describe":"github_search_code"}`)
		eq(t, []string{r.State, pat(r)}, []string{"allow", "github"}, "result")
	})
	tw(t, unified, "still auto-allows a discovery operation no config rule can name", func(t *testing.T) {
		// The baseline's own constituency: a granted server plus a describe of a tool belonging to some other, unconfigured server.
		r := checkTool(t, managerWithConfig(t, `{"mcp":{"github":"allow"}}`, "github"), "mcp", `{"describe":"unconfigured_server_tool"}`)
		eq(t, []string{r.State, pat(r), targetOf(r)}, []string{"allow", "<undefined>", "mcp_describe"}, "result")
	})
}

func TestPolicyUnifiedPiMcpToolTwins(t *testing.T) {
	const toolName = "mcp__danger_srv__wipe"
	resolveWith := func(t *testing.T, permission string, servers ...string) PermissionCheckResult {
		if servers == nil {
			servers = []string{"danger-srv"}
		}
		return checkTool(t, managerWithConfig(t, permission, servers...), toolName, `{"target":"prod"}`)
	}

	// the original's it.each: not in the ledger
	t.Run("denies on an mcp rule naming %s", func(t *testing.T) {
		for _, c := range []struct{ label, rules, target string }{
			{"the configured spelling", `"danger-srv":"deny"`, "danger-srv"},
			{"Pi's spelling", `"danger_srv":"deny"`, "danger_srv"},
			{"a server-qualified tool", `"danger-srv:wipe":"deny"`, "danger-srv:wipe"},
			{"the full Pi name", `"mcp__danger_srv__*":"deny"`, "mcp__danger_srv__wipe"},
		} {
			t.Run(c.label, func(t *testing.T) {
				r := resolveWith(t, `{"*":"allow","mcp":{"*":"allow",`+c.rules+`}}`)
				eq(t, []string{r.State, r.Source, targetOf(r), r.ToolName}, []string{"deny", "mcp", c.target, toolName}, "result")
			})
		}
	})
	tw(t, unified, "applies an mcp catch-all over the universal fallback", func(t *testing.T) {
		r := resolveWith(t, `{"*":"ask","mcp":"allow"}`)
		eq(t, []string{r.State, r.Source}, []string{"allow", "mcp"}, "result")
	})
	tw(t, unified, "falls back to the universal default when no mcp rule matches", func(t *testing.T) {
		r := resolveWith(t, `{"*":"ask"}`)
		eq(t, []string{r.State, r.Source, targetOf(r)}, []string{"ask", "default", "danger-srv_wipe"}, "result")
	})
	tw(t, unified, "derives Pi's spelling for a server no mcp.json names", func(t *testing.T) {
		r := resolveWith(t, `{"*":"allow","mcp":{"danger_srv":"deny"}}`, []string{}...)
		eq(t, r.State, "deny", "state")
	})
}

func TestPolicyUnifiedRelocatedMcpKeyTwins(t *testing.T) {
	const toolName = "mcp__danger_srv__wipe"
	const portNotice = `Top-level permission keys naming Pi MCP tools are applied as "mcp" rules: "mcp__danger_srv__wipe". ` +
		`Move them under "mcp" — see https://github.com/gotgenes/pi-packages/blob/main/packages/pi-permission-system/docs/migration/1001-pi-mcp-tools-on-mcp-surface.md`
	with := func(t *testing.T, permission string) *PermissionManager {
		return managerWithConfig(t, permission, "danger-srv")
	}

	t.Run("denies when the key is written %s the mcp catch-all", func(t *testing.T) {
		for order, config := range map[string]string{
			"before": `{"*":"allow","` + toolName + `":"deny","mcp":{"*":"allow"}}`,
			"after":  `{"*":"allow","mcp":{"*":"allow"},"` + toolName + `":"deny"}`,
		} {
			t.Run(order, func(t *testing.T) {
				r := checkTool(t, with(t, config), toolName, `{"target":"prod"}`)
				eq(t, []string{r.State, pat(r), r.Source}, []string{"deny", toolName, "mcp"}, "result")
			})
		}
	})
	tw(t, unified, "applies a wildcard key to every tool it names", func(t *testing.T) {
		m := with(t, `{"*":"allow","mcp__danger_srv__*":"deny"}`)
		eq(t, checkTool(t, m, toolName, "{}").State, "deny", "named")
		eq(t, checkTool(t, m, "mcp__other__x", "{}").State, "allow", "other")
	})
	tw(t, unified, "does not let a relocated allow open the proxy's discovery targets", func(t *testing.T) {
		m := with(t, `{"*":"ask","`+toolName+`":"allow"}`)
		eq(t, checkTool(t, m, toolName, "{}").State, "allow", "tool")
		eq(t, checkTool(t, m, "mcp", "{}").State, "ask", "proxy")
	})
	tw(t, unified, "asks the operator to port the key", func(t *testing.T) {
		eq(t, with(t, `{"*":"allow","`+toolName+`":"deny"}`).GetConfigIssues(nil), []string{portNotice}, "issues")
	})
	t.Run("still denies a non-Pi tool named mcp__foo through %s", func(t *testing.T) {
		for label, config := range map[string]string{"an exact key": `{"*":"allow","mcp__foo":"deny"}`, "a wildcard key": `{"*":"allow","mcp__*":"deny"}`} {
			t.Run(label, func(t *testing.T) {
				m := with(t, config)
				eq(t, checkTool(t, m, "mcp__foo", "{}").State, "deny", "check")
				eq(t, m.IsToolFullyDenied("mcp__foo", nil), true, "fully denied")
			})
		}
	})
	tw(t, unified, "raises no notice for a key that can name no Pi MCP tool", func(t *testing.T) {
		eq(t, len(with(t, `{"*":"allow","mcp__foo":"deny"}`).GetConfigIssues(nil)), 0, "issues")
	})
	tw(t, unified, "raises no notice when no such key exists", func(t *testing.T) {
		eq(t, len(with(t, `{"*":"allow","mcp":{"danger-srv":"deny"}}`).GetConfigIssues(nil)), 0, "issues")
	})
}

func TestPolicyUnifiedToolExposureTwins(t *testing.T) {
	with := func(t *testing.T, permission string) *PermissionManager {
		return managerWithConfig(t, permission, "danger-srv")
	}
	tw(t, unified, "withholds a tool whose server an mcp rule denies, and only that server's", func(t *testing.T) {
		m := with(t, `{"*":"allow","mcp":{"danger-srv":"deny"}}`)
		eq(t, m.IsToolFullyDenied("mcp__danger_srv__wipe", nil), true, "denied tool")
		eq(t, m.GetToolPermission("mcp__danger_srv__wipe", nil), "deny", "permission")
		eq(t, m.IsToolFullyDenied("mcp__other__x", nil), false, "other tool")
		eq(t, m.GetToolPermission("mcp__other__x", nil), "allow", "other permission")
	})
	tw(t, unified, "withholds a tool a relocated top-level key denies", func(t *testing.T) {
		eq(t, with(t, `{"*":"allow","mcp__danger_srv__*":"deny"}`).IsToolFullyDenied("mcp__danger_srv__wipe", nil), true, "denied")
	})
	tw(t, unified, "keeps a tool an exception after an mcp deny catch-all allows", func(t *testing.T) {
		m := with(t, `{"*":"allow","mcp":{"*":"deny","danger-srv":"allow"}}`)
		eq(t, m.IsToolFullyDenied("mcp__danger_srv__wipe", nil), false, "exception")
		eq(t, m.IsToolFullyDenied("mcp__other__x", nil), true, "other")
	})
}

func TestPolicyUnifiedForwardedMcpTwins(t *testing.T) {
	serve := func(t *testing.T, values ...string) PermissionCheckResult {
		m := managerWithConfig(t, `{"*":"ask","mcp":{"github":"allow","danger":"deny"}}`, "github", "danger")
		return m.Check(BuildResolvedIntentFromMatchValues("mcp", values, "Explore"), nil)
	}
	tw(t, unified, "denies a target on a denied server rather than allowing the status probe", func(t *testing.T) {
		r := serve(t, "danger")
		eq(t, []string{r.State, targetOf(r), pat(r)}, []string{"deny", "danger", "danger"}, "result")
	})
	tw(t, unified, "asks for a target no rule names rather than allowing the status probe", func(t *testing.T) {
		r := serve(t, "danger_wipe")
		eq(t, []string{r.State, targetOf(r)}, []string{"ask", "danger_wipe"}, "result")
	})
}

func TestPolicyUnifiedBashSpellingTwins(t *testing.T) {
	useFakeHome(t)
	home := fakeHome
	checkCommand := func(t *testing.T, bash, command string, spellings []string, rules Ruleset) PermissionCheckResult {
		m := newInMemoryManager(polScopes{global: permScope(t, `{"bash":`+bash+`}`)})
		return m.Check(BashCommandAccessIntent{Surface: "bash", Command: command, Spellings: spellings}, rules)
	}
	tw(t, unified, "matches a home-anchored rule through the spelling, reporting the command as typed", func(t *testing.T) {
		r := checkCommand(t, `{"*":"ask","~/bin/x":"allow"}`, "~/bin/x", []string{home + "/bin/x"}, nil)
		eq(t, []string{r.State, pat(r), commandOf(r)}, []string{"allow", "~/bin/x", "~/bin/x"}, "result")
	})
	tw(t, unified, "matches only the typed text when there are no spellings", func(t *testing.T) {
		r := checkCommand(t, `{"*":"ask","~/bin/x":"allow"}`, "~/bin/x", []string{}, nil)
		eq(t, []string{r.State, pat(r)}, []string{"ask", "*"}, "result")
	})
	tw(t, unified, "lets rule position decide, not which spelling a rule matched", func(t *testing.T) {
		r := checkCommand(t, `{"~/bin/x":"allow","*":"ask"}`, "~/bin/x", []string{home + "/bin/x"}, nil)
		eq(t, []string{r.State, pat(r)}, []string{"ask", "*"}, "result")
	})
	tw(t, unified, "lets a later rule matching only the spelling override an earlier one matching only the typed text", func(t *testing.T) {
		// `?/bin/x` matches the typed `~/bin/x` and not the spelling, so a first-value-wins evaluation would stop at its allow.
		r := checkCommand(t, `{"*":"ask","?/bin/x":"allow","~/bin/x":"deny"}`, "~/bin/x", []string{home + "/bin/x"}, nil)
		eq(t, []string{r.State, pat(r)}, []string{"deny", "~/bin/x"}, "result")
	})
	tw(t, unified, "reaches a home-anchored deny through the spelling", func(t *testing.T) {
		r := checkCommand(t, `{"*":"allow","~/bin/danger *":"deny"}`, "~/bin/danger --now", []string{home + "/bin/danger --now"}, nil)
		eq(t, []string{r.State, pat(r)}, []string{"deny", "~/bin/danger *"}, "result")
	})
	tw(t, unified, "matches a session grant recorded for the typed command", func(t *testing.T) {
		r := checkCommand(t, `{"*":"ask"}`, "~/bin/x", []string{home + "/bin/x"}, Ruleset{sessionRule("bash", "~/bin/x")})
		eq(t, []string{r.State, r.Source}, []string{"allow", "session"}, "result")
	})
}
