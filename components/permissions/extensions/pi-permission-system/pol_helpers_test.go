package pi_permission_system

import (
	"os"
	"path/filepath"
	"testing"
)

// Harness for the permission manager twins (test/helpers/manager-harness.ts).

// polScopes are the in-memory scopes of memLoader.
type polScopes struct {
	global, project     *ScopeConfig
	agent, projectAgent map[string]*ScopeConfig
}

// memLoader is the in-memory PolicyLoader: merge and evaluation logic without touching the file system.
type memLoader struct {
	scopes polScopes
	mcp    []string
	issues []string
}

func scopeOrEmpty(c *ScopeConfig) *ScopeConfig {
	if c == nil {
		return &ScopeConfig{}
	}
	return c
}

func (l *memLoader) LoadGlobalConfig() *ScopeConfig  { return scopeOrEmpty(l.scopes.global) }
func (l *memLoader) LoadProjectConfig() *ScopeConfig { return scopeOrEmpty(l.scopes.project) }
func (l *memLoader) LoadAgentConfig(name string) *ScopeConfig {
	if name != "" {
		return scopeOrEmpty(l.scopes.agent[name])
	}
	return &ScopeConfig{}
}
func (l *memLoader) LoadProjectAgentConfig(name string) *ScopeConfig {
	if name != "" {
		return scopeOrEmpty(l.scopes.projectAgent[name])
	}
	return &ScopeConfig{}
}
func (l *memLoader) GetConfiguredMcpServerNames() []string { return l.mcp }
func (l *memLoader) GetCacheStamp(string) string           { return "in-memory" }
func (l *memLoader) GetConfigIssues() []string             { return l.issues }
func (l *memLoader) GetResolvedPolicyPaths() ResolvedPolicyPaths {
	return ResolvedPolicyPaths{GlobalConfigPath: "/in-memory/config.json", GlobalConfigExists: true, AgentsDir: "/in-memory/agents"}
}

// permScope is `{ permission: <json> }` as a ScopeConfig.
func permScope(t *testing.T, permissionJSON string) *ScopeConfig {
	t.Helper()
	return &ScopeConfig{Permission: jo(t, permissionJSON)}
}

func newInMemoryManager(s polScopes, mcp ...string) *PermissionManager {
	return NewPermissionManager(PermissionManagerOptions{PolicyLoader: &memLoader{scopes: s, mcp: mcp, issues: []string{}}})
}

// newMissingConfigManager is the manager backed by nonexistent config paths: the universal default is "ask".
func newMissingConfigManager(mcp ...string) *PermissionManager {
	return NewPermissionManager(PermissionManagerOptions{PolicyLoaderOptions: PolicyLoaderOptions{
		GlobalConfigPath: "/nonexistent/config.json", AgentsDir: "/nonexistent/agents", McpServerNames: append([]string{}, mcp...)}})
}

// sessionRule builds a session-layer rule (default action "allow").
func sessionRule(surface, pattern string, action ...string) Rule {
	a := "allow"
	if len(action) > 0 {
		a = action[0]
	}
	return Rule{Surface: surface, Pattern: pattern, Action: a, Layer: "session", Origin: "session"}
}

func polWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o666); err != nil {
		t.Fatal(err)
	}
}

// createManager writes a real config file and agents directory to a temp directory (the config is the file's JSON text). The
// original reads the host's mcp.json when no server names are given; the harness pins an empty list instead so a run does not depend on
// the host.
func createManager(t *testing.T, configJSON string, agentFiles map[string]string, mcp ...string) (*PermissionManager, string) {
	t.Helper()
	base := t.TempDir()
	globalConfigPath := filepath.Join(base, "pi-permissions.jsonc")
	agentsDir := filepath.Join(base, "agents")
	if err := os.MkdirAll(agentsDir, 0o777); err != nil {
		t.Fatal(err)
	}
	polWriteFile(t, globalConfigPath, configJSON+"\n")
	for name, content := range agentFiles {
		polWriteFile(t, filepath.Join(agentsDir, name+".md"), content)
	}
	return NewPermissionManager(PermissionManagerOptions{PolicyLoaderOptions: PolicyLoaderOptions{
		GlobalConfigPath: globalConfigPath, AgentsDir: agentsDir, McpServerNames: append([]string{}, mcp...)}}), globalConfigPath
}

// managerWithConfig is a manager backed by a temp config holding only a permission map.
func managerWithConfig(t *testing.T, permissionJSON string, mcp ...string) *PermissionManager {
	t.Helper()
	m, _ := createManager(t, `{"permission":`+permissionJSON+`}`, nil, mcp...)
	return m
}

// projectFiles are the project scope of managerWithProject.
type projectFiles struct {
	config     string
	agentFiles map[string]string
}

func managerWithProject(t *testing.T, configJSON string, agentFiles map[string]string, project projectFiles, mcp ...string) *PermissionManager {
	t.Helper()
	base := t.TempDir()
	globalConfigPath := filepath.Join(base, "pi-permissions.jsonc")
	agentsDir := filepath.Join(base, "agents")
	projectRoot := filepath.Join(base, "project")
	projectConfigPath := filepath.Join(projectRoot, "pi-permissions.jsonc")
	projectAgentsDir := filepath.Join(projectRoot, "agents")
	for _, d := range []string{agentsDir, projectAgentsDir} {
		if err := os.MkdirAll(d, 0o777); err != nil {
			t.Fatal(err)
		}
	}
	polWriteFile(t, globalConfigPath, configJSON+"\n")
	if project.config != "" {
		polWriteFile(t, projectConfigPath, project.config+"\n")
	}
	for name, content := range agentFiles {
		polWriteFile(t, filepath.Join(agentsDir, name+".md"), content)
	}
	for name, content := range project.agentFiles {
		polWriteFile(t, filepath.Join(projectAgentsDir, name+".md"), content)
	}
	return NewPermissionManager(PermissionManagerOptions{PolicyLoaderOptions: PolicyLoaderOptions{
		GlobalConfigPath: globalConfigPath, AgentsDir: agentsDir, ProjectGlobalConfigPath: projectConfigPath, ProjectAgentsDir: projectAgentsDir,
		McpServerNames: append([]string{}, mcp...)}})
}

// managerWithScopes is a manager backed by a global permission map plus an optional project map ("" for none).
func managerWithScopes(t *testing.T, globalPermission, projectPermission string) *PermissionManager {
	t.Helper()
	project := projectFiles{}
	if projectPermission != "" {
		project.config = `{"permission":` + projectPermission + `}`
	}
	return managerWithProject(t, `{"permission":`+globalPermission+`}`, nil, project)
}

// agentDirHarness builds a temp agentDir with a global config and a cwd with an optional project config.
type agentDirRig struct {
	agentDir, cwd, globalConfigPath, projectConfigPath string
}

func newAgentDirHarness(t *testing.T, globalPermission, projectPermission string) agentDirRig {
	t.Helper()
	base := t.TempDir()
	r := agentDirRig{agentDir: filepath.Join(base, "agent"), cwd: filepath.Join(base, "project")}
	r.globalConfigPath = getGlobalConfigPath(r.agentDir)
	polWriteFile(t, r.globalConfigPath, `{"permission":`+globalPermission+`}`)
	r.projectConfigPath = getProjectConfigPath(r.cwd)
	if err := os.MkdirAll(filepath.Dir(r.projectConfigPath), 0o777); err != nil {
		t.Fatal(err)
	}
	if projectPermission != "" {
		polWriteFile(t, r.projectConfigPath, `{"permission":`+projectPermission+`}`)
	}
	return r
}

// ---- the adapters that build an AccessIntent and call the unified check entry point ----

func inputObject(t *testing.T, input string) any {
	t.Helper()
	if input == "" {
		input = "{}"
	}
	return jo(t, input)
}

func optAgent(agent string) *string {
	if agent == "" {
		return nil
	}
	return &agent
}

func checkToolAs(t *testing.T, m *PermissionManager, tool, input, agent string, sessionRules Ruleset) PermissionCheckResult {
	t.Helper()
	return m.Check(ToolAccessIntent{Surface: tool, Input: inputObject(t, input), AgentName: optAgent(agent)}, sessionRules)
}

func checkTool(t *testing.T, m *PermissionManager, tool, input string) PermissionCheckResult {
	t.Helper()
	return checkToolAs(t, m, tool, input, "", nil)
}

func checkValues(m *PermissionManager, surface string, values []string, agent string, sessionRules Ruleset) PermissionCheckResult {
	return m.Check(PathValuesAccessIntent{Surface: surface, Values: values, AgentName: optAgent(agent)}, sessionRules)
}

func checkPathAs(m *PermissionManager, path, cwd, surface, agent string, sessionRules Ruleset) PermissionCheckResult {
	opts := PathPolicyValueOptions{}
	if cwd != "" {
		opts.Cwd = &cwd
	}
	return checkValues(m, surface, GetPathPolicyValues(path, opts, PosixPathFlavor), agent, sessionRules)
}

func checkPath(m *PermissionManager, path string) PermissionCheckResult {
	return checkPathAs(m, path, "", "path_read", "", nil)
}

func checkPathOn(m *PermissionManager, path, surface string) PermissionCheckResult {
	return checkPathAs(m, path, "", surface, "", nil)
}

// pat is the matched pattern, "<undefined>" when there is none.
func pat(r PermissionCheckResult) string {
	if r.MatchedPattern == nil {
		return "<undefined>"
	}
	return *r.MatchedPattern
}

// reasonOf is the custom denial reason, "<undefined>" when there is none.
func reasonOf(r PermissionCheckResult) string {
	if r.Reason == nil {
		return "<undefined>"
	}
	return *r.Reason
}

func targetOf(r PermissionCheckResult) string {
	if r.Target == nil {
		return "<undefined>"
	}
	return *r.Target
}

func commandOf(r PermissionCheckResult) string {
	if r.Command == nil {
		return "<undefined>"
	}
	return *r.Command
}
