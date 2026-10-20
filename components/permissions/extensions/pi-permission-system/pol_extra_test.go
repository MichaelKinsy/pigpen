package pi_permission_system

import (
	"os"
	"path/filepath"
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

func TestGrantTargetsOfNoGrantsIsEmptyNotNil(t *testing.T) {
	eq(t, GrantTargets(nil), []string{}, "targets")
}

// A project scope whose file changes while its stamp does not still follows ConfigureForCwd: the cache is cleared with the loader.
func TestPermissionManagerConfigureForCwdClearsTheCacheEvenWithEqualStamps(t *testing.T) {
	base := t.TempDir()
	agentDir := filepath.Join(base, "agent")
	polWriteFile(t, getGlobalConfigPath(agentDir), `{"permission":{}}`)
	cwdA, cwdB := filepath.Join(base, "a"), filepath.Join(base, "b")
	at := time.Unix(1_700_000_000, 0)
	for cwd, state := range map[string]string{cwdA: "allow", cwdB: "deny"} {
		path := getProjectConfigPath(cwd)
		polWriteFile(t, path, `{"permission":{"read":"`+state+`"}}`)
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	m := NewPermissionManager(PermissionManagerOptions{AgentDir: agentDir})
	m.ConfigureForCwd(cwdA)
	eq(t, checkTool(t, m, "read", "{}").State, "allow", "cwd a")
	m.ConfigureForCwd(cwdB)
	eq(t, checkTool(t, m, "read", "{}").State, "deny", "cwd b")
}

// The loader's issues are read after the load they come from: the first call on a fresh manager already has them.
func TestPermissionManagerFirstConfigIssuesCallSeesTheLoad(t *testing.T) {
	m := managerWithProject(t, `{"permission":{"bash":"allow"}}`, nil, projectFiles{config: `{"permission":{"bash":"maybe"}}`})
	issues := m.GetConfigIssues(nil)
	eq(t, len(issues), 2, "issues")
	eq(t, issues[0], "Invalid config value at 'permission.bash': Invalid input", "loader issue first")
}

// The tool-level state is the "*" catch-all value: a rule that matches the text "*" but not the empty string applies.
func TestPermissionManagerToolPermissionEvaluatesTheCatchAllText(t *testing.T) {
	m := newInMemoryManager(polScopes{global: permScope(t, `{"bash":{"*":"deny","?":"allow"}}`)})
	eq(t, m.GetToolPermission("bash", nil), "allow", "state")
}

// A tool or bash-command intent on a bare family surface is folded too (the fold is on the resolved surface, whatever the kind).
func TestPermissionResolverFoldsEveryIntentKind(t *testing.T) {
	pm := newFakeManager()
	r := NewPermissionResolver(pm, &SessionRules{})
	r.Resolve(ToolAccessIntent{Surface: "path", Input: jo(t, `{}`)})
	r.Resolve(BashCommandAccessIntent{Surface: "external_directory", Command: "ls"})
	eq(t, intentSurfaces(pm), []string{"path_read", "path_write", "external_directory_read", "external_directory_write"}, "surfaces")
	eq(t, pm.checks[1].intent, ResolvedAccessIntent(ToolAccessIntent{Surface: "path_write", Input: jo(t, `{}`)}), "tool member")
	eq(t, pm.checks[3].intent, ResolvedAccessIntent(BashCommandAccessIntent{Surface: "external_directory_write", Command: "ls"}), "bash member")
}

// The unwrap hands the manager every alias of the path, not its lexical form alone.
func TestPermissionResolverUnwrapsEveryMatchValue(t *testing.T) {
	pm := newFakeManager()
	ap := AccessPathForPath("src/a.ts", AccessPathForPathOptions{Cwd: "/proj", Flavor: PosixPathFlavor})
	if len(ap.MatchValues()) < 2 {
		t.Fatalf("the fixture needs a path with several aliases: %v", ap.MatchValues())
	}
	NewPermissionResolver(pm, &SessionRules{}).Resolve(AccessPathAccessIntent{Surface: "path_read", Path: ap})
	eq(t, pm.checks[0].intent, ResolvedAccessIntent(PathValuesAccessIntent{Surface: "path_read", Values: ap.MatchValues()}), "intent")
}

// The session remembers the name it resolved, from the system prompt too, and a session name always wins over a remembered one.
func TestPermissionSessionResolveAgentNameMemory(t *testing.T) {
	ctx, _ := makeSessionCtx("/p", true)
	r := makeRealSession(nil)
	agents := r.stubAgents()
	agents.fromPrompt = sp("prompt-agent")
	name, _ := r.session.ResolveAgentName(ctx, "p")
	eq(t, name, sp("prompt-agent"), "from prompt")
	agents.fromPrompt = nil
	name, _ = r.session.ResolveAgentName(ctx, "")
	eq(t, name, sp("prompt-agent"), "remembered from the prompt")
	agents.fromSession = sp("session-agent")
	name, _ = r.session.ResolveAgentName(ctx, "")
	eq(t, name, sp("session-agent"), "the session wins over the remembered name")
	agents.fromSession = nil
	name, _ = r.session.ResolveAgentName(ctx, "")
	eq(t, name, sp("session-agent"), "and is remembered in turn")
}

func TestPermissionSessionLogsPathsForTheActiveCwd(t *testing.T) {
	r := makeRealSession(nil)
	r.session.LogResolvedConfigPaths()
	ctx, _ := makeSessionCtx("/active", true)
	r.session.Activate(ctx)
	r.session.LogResolvedConfigPaths()
	eq(t, r.store.logged, []string{"", "/active"}, "cwds")
}

// SessionRules is written by the approval path while the resolver reads it; under go test -race this is the check of its lock.
func TestSessionRulesConcurrentUse(t *testing.T) {
	rules := &SessionRules{}
	done := make(chan struct{})
	for i := 0; i < 4; i++ {
		go func() {
			for j := 0; j < 200; j++ {
				rules.Approve("path", "/x/*")
				rules.RecordSessionApproval(SessionApprovalSingle("bash", "ls"))
				_ = rules.GetRuleset()
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 4; i++ {
		<-done
	}
	rules.Clear()
	eq(t, len(rules.GetRuleset()), 0, "cleared")
}
