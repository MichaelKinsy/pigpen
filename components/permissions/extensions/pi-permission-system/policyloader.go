package pi_permission_system

import (
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Port of src/config/policy-loader.ts: the file I/O behind the permission policy, cached by modification time.

// ScopeConfig is one config scope's permission map. Invalid is true when the scope's file was present but could not be loaded or
// validated: it drives the fail-closed allow-to-ask clamp for non-global scopes (#646).
type ScopeConfig struct {
	Permission *jsObject
	Invalid    bool
}

// ResolvedPolicyPaths are the paths the policy was loaded from, for the `/permission-system` command and the logs. A nil project
// path is null in the original.
type ResolvedPolicyPaths struct {
	GlobalConfigPath       string
	GlobalConfigExists     bool
	ProjectConfigPath      *string
	ProjectConfigExists    bool
	AgentsDir              string
	AgentsDirExists        bool
	ProjectAgentsDir       *string
	ProjectAgentsDirExists bool
}

// PolicyLoader is the abstraction over file I/O for loading permission policy from disk: implementations handle caching, path
// resolution and config-issue accumulation.
type PolicyLoader interface {
	LoadGlobalConfig() *ScopeConfig
	LoadProjectConfig() *ScopeConfig
	LoadAgentConfig(agentName string) *ScopeConfig
	LoadProjectAgentConfig(agentName string) *ScopeConfig
	GetConfiguredMcpServerNames() []string
	// GetCacheStamp is a combined mtime stamp for cache invalidation.
	GetCacheStamp(agentName string) string
	// GetConfigIssues is the config-parse issues accumulated across all loads.
	GetConfigIssues() []string
	// GetResolvedPolicyPaths answers the /permission-system show command.
	GetResolvedPolicyPaths() ResolvedPolicyPaths
}

// PolicyLoaderOptions configure a FilePolicyLoader; an empty path is not set (the global ones default to the agent directory's).
type PolicyLoaderOptions struct {
	GlobalConfigPath        string
	AgentsDir               string
	ProjectGlobalConfigPath string
	ProjectAgentsDir        string
	GlobalMcpConfigPath     string
	// ProjectMcpConfigPath is the project's `.pi/mcp.json`, read beside the global file the way Pi's built-in MCP reads it. Set only
	// for a trusted project.
	ProjectMcpConfigPath string
	// McpServerNames, when non-nil (even empty), replaces reading the mcp.json files.
	McpServerNames []string
}

type fileCacheEntry[T any] struct {
	stamp string
	value T
}

// FilePolicyLoader is the production PolicyLoader: it reads config files from disk with mtime-based caching.
type FilePolicyLoader struct {
	globalConfigPath        string
	agentsDir               string
	projectGlobalConfigPath string
	projectAgentsDir        string
	globalMcpConfigPath     string
	projectMcpConfigPath    string
	mcpOverride             []string
	hasMcpOverride          bool

	mu                       sync.Mutex
	globalConfigCache        *fileCacheEntry[*ScopeConfig]
	projectGlobalConfigCache *fileCacheEntry[*ScopeConfig]
	agentConfigCache         map[string]fileCacheEntry[*ScopeConfig]
	projectAgentConfigCache  map[string]fileCacheEntry[*ScopeConfig]
	mcpNamesCache            *fileCacheEntry[[]string]
	accumulatedConfigIssues  []string
}

// NewFilePolicyLoader builds a loader from options.
func NewFilePolicyLoader(o PolicyLoaderOptions) *FilePolicyLoader {
	l := &FilePolicyLoader{
		globalConfigPath: o.GlobalConfigPath, agentsDir: o.AgentsDir,
		projectGlobalConfigPath: o.ProjectGlobalConfigPath, projectAgentsDir: o.ProjectAgentsDir,
		globalMcpConfigPath: o.GlobalMcpConfigPath, projectMcpConfigPath: o.ProjectMcpConfigPath,
		agentConfigCache:        map[string]fileCacheEntry[*ScopeConfig]{},
		projectAgentConfigCache: map[string]fileCacheEntry[*ScopeConfig]{},
	}
	if l.globalConfigPath == "" {
		l.globalConfigPath = getGlobalConfigPath(agentDir())
	}
	if l.agentsDir == "" {
		l.agentsDir = filepath.Join(agentDir(), "agents")
	}
	if l.globalMcpConfigPath == "" {
		l.globalMcpConfigPath = filepath.Join(agentDir(), "mcp.json")
	}
	if o.McpServerNames != nil {
		l.hasMcpOverride = true
		seen := map[string]bool{}
		l.mcpOverride = []string{}
		for _, n := range o.McpServerNames {
			n = jsTrim(n)
			if n != "" && !seen[n] {
				seen[n] = true
				l.mcpOverride = append(l.mcpOverride, n)
			}
		}
	}
	return l
}

func (l *FilePolicyLoader) accumulateConfigIssues(issues []string) {
	for _, issue := range issues {
		found := false
		for _, have := range l.accumulatedConfigIssues {
			found = found || have == issue
		}
		if !found {
			l.accumulatedConfigIssues = append(l.accumulatedConfigIssues, issue)
		}
	}
}

// GetConfigIssues returns a copy of the issues accumulated across all loads.
func (l *FilePolicyLoader) GetConfigIssues() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string{}, l.accumulatedConfigIssues...)
}

func (l *FilePolicyLoader) LoadGlobalConfig() *ScopeConfig {
	l.mu.Lock()
	defer l.mu.Unlock()
	stamp := fileStamp(l.globalConfigPath)
	if l.globalConfigCache != nil && l.globalConfigCache.stamp == stamp {
		return l.globalConfigCache.value
	}
	r := loadUnifiedConfig(l.globalConfigPath)
	l.accumulateConfigIssues(r.Issues)
	value := &ScopeConfig{Permission: r.Config.Permission}
	l.globalConfigCache = &fileCacheEntry[*ScopeConfig]{stamp, value}
	return value
}

func (l *FilePolicyLoader) LoadProjectConfig() *ScopeConfig {
	if l.projectGlobalConfigPath == "" {
		return &ScopeConfig{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	stamp := fileStamp(l.projectGlobalConfigPath)
	if l.projectGlobalConfigCache != nil && l.projectGlobalConfigCache.stamp == stamp {
		return l.projectGlobalConfigCache.value
	}
	r := loadUnifiedConfig(l.projectGlobalConfigPath)
	l.accumulateConfigIssues(r.Issues)
	// A present-but-rejected file yields issues (parse error or schema rejection); an absent file yields none. Fail closed on the
	// former.
	value := &ScopeConfig{Permission: r.Config.Permission, Invalid: len(r.Issues) > 0}
	l.projectGlobalConfigCache = &fileCacheEntry[*ScopeConfig]{stamp, value}
	return value
}

func (l *FilePolicyLoader) loadScopeConfigFrom(dir string, cache map[string]fileCacheEntry[*ScopeConfig], agentName string) *ScopeConfig {
	if dir == "" || agentName == "" {
		return &ScopeConfig{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	filePath := filepath.Join(dir, agentName+".md")
	stamp := fileStamp(filePath)
	if cached, ok := cache[agentName]; ok && cached.stamp == stamp {
		return cached.value
	}
	// An absent file (stat failed) is a legitimately-empty scope, not invalid; only a present-but-unreadable file fails closed.
	if stamp == "missing" {
		value := &ScopeConfig{}
		cache[agentName] = fileCacheEntry[*ScopeConfig]{stamp, value}
		return value
	}
	var value *ScopeConfig
	markdown, err := readTextFile(filePath)
	if err != nil {
		// The file exists but could not be read: fail closed for this scope (#646).
		value = &ScopeConfig{Invalid: true}
	} else if frontmatter := extractFrontmatter(markdown); frontmatter == "" {
		value = &ScopeConfig{}
	} else {
		// Agent frontmatter carries non-config keys (name, description, model, ...) alongside `permission`, so it is not validated by
		// the strict config-file schema; only the `permission` block is extracted, and its malformed entries are dropped.
		value = &ScopeConfig{Permission: normalizeFlatPermissionValue(parseSimpleYamlMap(frontmatter).vals["permission"])}
	}
	cache[agentName] = fileCacheEntry[*ScopeConfig]{stamp, value}
	return value
}

func (l *FilePolicyLoader) LoadAgentConfig(agentName string) *ScopeConfig {
	return l.loadScopeConfigFrom(l.agentsDir, l.agentConfigCache, agentName)
}

func (l *FilePolicyLoader) LoadProjectAgentConfig(agentName string) *ScopeConfig {
	return l.loadScopeConfigFrom(l.projectAgentsDir, l.projectAgentConfigCache, agentName)
}

// readConfiguredMcpServerNames reads the server names of one mcp.json (`mcpServers`, or `mcp-servers`), [] when it cannot be read.
func readConfiguredMcpServerNames(configPath string) []string {
	raw, err := readTextFile(configPath)
	if err != nil {
		return []string{}
	}
	stripped := stripJSONComments(raw)
	if v8JSONError(stripped) != "" {
		return []string{}
	}
	parsed, err := parseJSON([]byte(stripped))
	if err != nil {
		return []string{}
	}
	root, _ := parsed.(*jsObject)
	if root == nil {
		return []string{}
	}
	servers, present := root.vals["mcpServers"]
	if !present || servers == nil {
		servers = root.vals["mcp-servers"]
	}
	record, _ := servers.(*jsObject)
	names := []string{}
	if record == nil {
		return names
	}
	for _, k := range record.order() {
		if name := jsTrim(k); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func configuredMcpServerNamesFromPaths(paths []string) []string {
	seen := map[string]bool{}
	var names []string
	for _, p := range paths {
		for _, n := range readConfiguredMcpServerNames(p) {
			if !seen[n] {
				seen[n] = true
				names = append(names, n)
			}
		}
	}
	sort.SliceStable(names, func(i, j int) bool { return mcpNameLess(names[i], names[j]) })
	if names == nil {
		names = []string{}
	}
	return names
}

func (l *FilePolicyLoader) GetConfiguredMcpServerNames() []string {
	if l.hasMcpOverride {
		return l.mcpOverride
	}
	paths := []string{l.globalMcpConfigPath}
	if l.projectMcpConfigPath != "" {
		paths = append(paths, l.projectMcpConfigPath)
	}
	var parts []string
	for _, p := range paths {
		parts = append(parts, p+":"+fileStamp(p))
	}
	stamp := strings.Join(parts, "|")
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.mcpNamesCache != nil && l.mcpNamesCache.stamp == stamp {
		return l.mcpNamesCache.value
	}
	value := configuredMcpServerNamesFromPaths(paths)
	l.mcpNamesCache = &fileCacheEntry[[]string]{stamp, value}
	return value
}

// GetCacheStamp combines the mtimes of the global config, the project config and the agent's two files.
func (l *FilePolicyLoader) GetCacheStamp(agentName string) string {
	agentStamp := "missing"
	if agentName != "" {
		agentStamp = fileStamp(filepath.Join(l.agentsDir, agentName+".md"))
	}
	projectStamp := "none"
	if l.projectGlobalConfigPath != "" {
		projectStamp = fileStamp(l.projectGlobalConfigPath)
	}
	projectAgentStamp := "none"
	if l.projectAgentsDir != "" && agentName != "" {
		projectAgentStamp = fileStamp(filepath.Join(l.projectAgentsDir, agentName+".md"))
	}
	return fileStamp(l.globalConfigPath) + "|" + projectStamp + "|" + agentStamp + "|" + projectAgentStamp
}

func (l *FilePolicyLoader) GetResolvedPolicyPaths() ResolvedPolicyPaths {
	r := ResolvedPolicyPaths{
		GlobalConfigPath: l.globalConfigPath, GlobalConfigExists: pathExists(l.globalConfigPath),
		AgentsDir: l.agentsDir, AgentsDirExists: pathExists(l.agentsDir),
	}
	if l.projectGlobalConfigPath != "" {
		p := l.projectGlobalConfigPath
		r.ProjectConfigPath, r.ProjectConfigExists = &p, pathExists(p)
	}
	if l.projectAgentsDir != "" {
		p := l.projectAgentsDir
		r.ProjectAgentsDir, r.ProjectAgentsDirExists = &p, pathExists(p)
	}
	return r
}

// derivePolicyLoaderOptions derives the loader options from an agent directory and an optional cwd (permission-manager.ts
// derivePolicyLoaderOptions): Pi's built-in MCP reads the two mcp.json files.
func derivePolicyLoaderOptions(agentDir, cwd string) PolicyLoaderOptions {
	o := PolicyLoaderOptions{
		GlobalConfigPath:    getGlobalConfigPath(agentDir),
		AgentsDir:           filepath.Join(agentDir, "agents"),
		GlobalMcpConfigPath: filepath.Join(agentDir, "mcp.json"),
	}
	if cwd != "" {
		o.ProjectGlobalConfigPath = getProjectConfigPath(cwd)
		o.ProjectAgentsDir = getProjectAgentsDir(cwd)
		o.ProjectMcpConfigPath = filepath.Join(cwd, ".pi", "mcp.json")
	}
	return o
}
