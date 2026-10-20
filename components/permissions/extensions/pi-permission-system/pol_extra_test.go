package pi_permission_system

import (
	"os"
	"testing"
	"time"
)

// Tests of the port's own seams that the original's tests do not reach (each one kills a mutation of port/mutations-policy.json).

// A config file that changes on disk reaches the next check without a reconfigure: the cache is keyed by the loader's stamp.
func TestPermissionManagerCacheFollowsTheConfigFile(t *testing.T) {
	rig := newAgentDirHarness(t, `{"read":"allow"}`, "")
	m := NewPermissionManager(PermissionManagerOptions{AgentDir: rig.agentDir})
	eq(t, checkTool(t, m, "read", "{}").State, "allow", "before")
	polWriteFile(t, rig.globalConfigPath, `{"permission":{"read":"deny"}}`)
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(rig.globalConfigPath, later, later); err != nil {
		t.Fatal(err)
	}
	eq(t, checkTool(t, m, "read", "{}").State, "deny", "after the file changed")
}

// The cache keeps one entry per agent: an agent's rules never answer for the global scope, or for another agent.
func TestPermissionManagerCacheIsPerAgent(t *testing.T) {
	m := newInMemoryManager(polScopes{global: permScope(t, `{"read":"allow"}`),
		agent: map[string]*ScopeConfig{"a": permScope(t, `{"read":"deny"}`), "b": permScope(t, `{"read":"ask"}`)}})
	eq(t, checkToolAs(t, m, "read", "{}", "a", nil).State, "deny", "a")
	eq(t, checkTool(t, m, "read", "{}").State, "allow", "global")
	eq(t, checkToolAs(t, m, "read", "{}", "b", nil).State, "ask", "b")
	eq(t, checkToolAs(t, m, "read", "{}", "a", nil).State, "deny", "a again")
	eq(t, m.GetToolPermission("read", sp("a")), "deny", "a permission")
	eq(t, m.GetToolPermission("read", nil), "allow", "global permission")
}

// The loader's own issues and the manager's notices come out in order, the loader's first.
func TestPermissionManagerConfigIssuesOrder(t *testing.T) {
	loader := &memLoader{scopes: polScopes{global: permScope(t, `{"bash":"allow","mcp__a__b":"deny"}`), project: &ScopeConfig{Invalid: true}}, issues: []string{"from the loader"}}
	issues := NewPermissionManager(PermissionManagerOptions{PolicyLoader: loader}).GetConfigIssues(nil)
	eq(t, len(issues), 3, "issues")
	eq(t, issues[0], "from the loader", "loader first")
	eq(t, issues[1][:len("Invalid project")], "Invalid project", "fail-closed notice")
	eq(t, issues[2][:len("Top-level permission keys")], "Top-level permission keys", "port notice")
	eq(t, len(loader.issues), 1, "loader's list untouched")
}

// Several invalid scopes are all named, in scope order.
func TestPermissionManagerFailClosedNamesEveryInvalidScope(t *testing.T) {
	inv := &ScopeConfig{Invalid: true}
	m := newInMemoryManager(polScopes{global: permScope(t, `{"bash":"allow"}`), project: inv,
		agent: map[string]*ScopeConfig{"a": inv}, projectAgent: map[string]*ScopeConfig{"a": inv}})
	issues := m.GetConfigIssues(sp("a"))
	eq(t, issues, []string{"Invalid project, agent, project-agent configuration detected \u2014 failing closed: 'allow' rules " +
		"are clamped to 'ask' for this session until the configuration is corrected."}, "issues")
	// each scope alone floors on its own
	for name, scopes := range map[string]polScopes{
		"project":       {global: permScope(t, `{"bash":"allow"}`), project: inv},
		"agent":         {global: permScope(t, `{"bash":"allow"}`), agent: map[string]*ScopeConfig{"a": inv}},
		"project-agent": {global: permScope(t, `{"bash":"allow"}`), projectAgent: map[string]*ScopeConfig{"a": inv}},
	} {
		r := checkToolAs(t, newInMemoryManager(scopes), "bash", `{"command":"x"}`, "a", nil)
		eq(t, []string{r.State, r.Origin}, []string{"ask", "fail-closed"}, name)
		eq(t, newInMemoryManager(scopes).GetConfigIssues(sp("a"))[0][:len("Invalid "+name+" configuration")], "Invalid "+name+" configuration", name+" notice")
	}
}

// An agent definition that names no permission, or a data-less active_agent entry, never resets the agent: scanning continues.
func TestActiveAgentEntryWithoutNameKeepsScanning(t *testing.T) {
	eq(t, agentName(t, activeEntry(map[string]any{"name": "first"}), activeEntry(map[string]any{})), sp("first"), "empty data")
	eq(t, agentName(t, activeEntry(map[string]any{"name": "first"}), SessionEntryView{"type": "custom", "customType": "active_agent"}), sp("first"), "no data")
	eq(t, agentName(t, activeEntry(map[string]any{"name": "first"}), activeEntry(map[string]any{"name": 7})), sp("first"), "non-string name")
	eq(t, agentName(t, activeEntry(jo(t, `{"name":"from-object"}`))), sp("from-object"), "ordered object data")
	eq(t, agentName(t, activeEntry(jo(t, `{"name":null}`)), activeEntry(map[string]any{"name": "x"})), sp("x"), "later entry wins over an earlier null")
	eq(t, agentName(t, activeEntry(map[string]any{"name": "x"}), activeEntry(jo(t, `{"name":null}`))), (*string)(nil), "null object resets")
}

func TestPermissionSessionShellToolAliases(t *testing.T) {
	r := makeRealSession(nil)
	eq(t, r.session.GetShellToolAliases() == nil, true, "none configured")
	r.store.current.ShellTools = jo(t, `{"exec_command":{"commandArgument":"cmd","workdirArgument":"workdir"},"run":{"commandArgument":"c"},"broken":{"workdirArgument":"w"},"odd":3}`)
	eq(t, r.session.GetShellToolAliases(), ShellToolsConfig{
		"exec_command": {CommandArgument: "cmd", WorkdirArgument: "workdir"},
		"run":          {CommandArgument: "c"},
	}, "aliases")
}

// A session context without a session manager is an error, never an empty session.
func TestSessionContextWithoutSessionManager(t *testing.T) {
	ctx, _ := makeSessionCtx("/p", true)
	if _, err := ctx.GetEntries(); err == nil {
		t.Fatal("a context without a session manager must not read as an empty session")
	}
	if _, err := GetActiveAgentName(ctx); err == nil {
		t.Fatal("the active agent of such a context must be an error")
	}
}

// Walking the session context's own entries finds the agent.
func TestSessionContextEntriesReachTheActiveAgent(t *testing.T) {
	ctx, _ := makeSessionCtx("/p", true)
	ctx.SessionManager = entriesManager{activeEntry(map[string]any{"name": "bot"})}
	name, err := GetActiveAgentName(ctx)
	eq(t, []any{name, err}, []any{sp("bot"), error(nil)}, "agent")
}

type entriesManager []SessionEntryView

func (e entriesManager) GetSessionID() (string, error)           { return "s", nil }
func (e entriesManager) GetSessionDir() (string, error)          { return "", nil }
func (e entriesManager) GetEntries() ([]SessionEntryView, error) { return e, nil }

// The resolver forwards an agent name and the session ruleset on every path.
func TestPermissionResolverAgentAndSessionThreading(t *testing.T) {
	pm := newFakeManager()
	sr := &SessionRules{}
	sr.Approve("bash", "ls")
	r := NewPermissionResolver(pm, sr)
	r.CheckPermission("bash", jo(t, `{}`), sp("a"), Ruleset{{Surface: "x"}})
	r.Resolve(BashCommandAccessIntent{Surface: "bash", Command: "ls", AgentName: sp("a")})
	eq(t, intentAgentName(pm.checks[0].intent), sp("a"), "raw check agent")
	eq(t, pm.checks[0].rules, Ruleset{{Surface: "x"}}, "raw check rules")
	eq(t, intentAgentName(pm.checks[1].intent), sp("a"), "bash intent agent")
	eq(t, len(pm.checks[1].rules), 1, "session ruleset")
	r.Resolve(AccessPathAccessIntent{Surface: "path_write", Path: AccessPathForPath("/x", AccessPathForPathOptions{Cwd: "/w", Flavor: PosixPathFlavor}), AgentName: sp("a")})
	eq(t, pm.checks[2].intent, ResolvedAccessIntent(PathValuesAccessIntent{Surface: "path_write", Values: AccessPathForPath("/x", AccessPathForPathOptions{Cwd: "/w", Flavor: PosixPathFlavor}).MatchValues(), AgentName: sp("a")}), "unwrapped surface and agent")
}
