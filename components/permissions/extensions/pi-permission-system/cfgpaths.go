package pi_permission_system

import "path/filepath"

// Port of src/config/config-paths.ts and src/config/extension-paths.ts.

const (
	debugLogFilename  = extensionID + "-debug.jsonl"
	reviewLogFilename = extensionID + "-permission-review.jsonl"
)

func getGlobalConfigDir(agentDir string) string {
	return filepath.Join(agentDir, "extensions", extensionID)
}

func getGlobalConfigPath(agentDir string) string {
	return filepath.Join(getGlobalConfigDir(agentDir), "config.json")
}

func getGlobalLogsDir(agentDir string) string {
	return filepath.Join(getGlobalConfigDir(agentDir), "logs")
}

func getProjectConfigPath(cwd string) string {
	return filepath.Join(cwd, ".pi", "extensions", extensionID, "config.json")
}

// getProjectAgentsDir is the directory holding project-scoped custom agent definition files (config-paths.ts:23-35): `<cwd>/.pi/agents`.
func getProjectAgentsDir(cwd string) string {
	return filepath.Join(cwd, ".pi", "agents")
}

func getLegacyGlobalPolicyPath(agentDir string) string {
	return filepath.Join(agentDir, "pi-permissions.jsonc")
}

func getLegacyProjectPolicyPath(cwd string) string {
	return filepath.Join(cwd, ".pi", "agent", "pi-permissions.jsonc")
}

func getLegacyExtensionConfigPath(extensionRoot string) string {
	return filepath.Join(extensionRoot, "config.json")
}

// ExtensionPaths are the immutable path constants derived from the agent directory (extension-paths.ts ExtensionPaths).
type ExtensionPaths struct {
	AgentDir            string
	SessionsDir         string
	SubagentSessionsDir string
	ForwardingDir       string
	GlobalLogsDir       string
	// PiInfrastructureDirs are the static Pi infrastructure roots used for external-directory read auto-allow; an entry may be a
	// directory or a single file.
	PiInfrastructureDirs []string
	// PiInfrastructureExcludedDirs are never auto-allowed as infrastructure reads: this package's own logs directory.
	PiInfrastructureExcludedDirs []string
}

// nodeModulesRootDiscovery answers discoverGlobalNodeModulesRoot() (path/node-modules-discovery.ts): the global node_modules root,
// "" for none. A seam: the paths lane ports the discovery itself, and the twins mock it as the original's test does.
var nodeModulesRootDiscovery = func() string { return "" }

// agentDirInfrastructureEntries are the entries under the agent directory that Pi's harness reads (extension-paths.ts
// AGENT_DIR_INFRASTRUCTURE_ENTRIES); everything else there stays behind the external_directory gate.
var agentDirInfrastructureEntries = []string{"agents", "extensions", "git", "npm", "prompts", "skills", "themes", "settings.json",
	"SYSTEM.md", "APPEND_SYSTEM.md", "AGENTS.md"}

// computeExtensionPaths computes all immutable path constants from agentDir. piPackageDir is Pi's own install directory, "" for
// none.
func computeExtensionPaths(agentDir, piPackageDir string) ExtensionPaths {
	sessionsDir := filepath.Join(agentDir, "sessions")
	globalLogsDir := getGlobalLogsDir(agentDir)
	var dirs []string
	for _, e := range agentDirInfrastructureEntries {
		dirs = append(dirs, filepath.Join(agentDir, e))
	}
	if root := nodeModulesRootDiscovery(); root != "" {
		dirs = append(dirs, root)
	}
	if piPackageDir != "" {
		dirs = append(dirs, piPackageDir)
	}
	return ExtensionPaths{
		AgentDir:                     agentDir,
		SessionsDir:                  sessionsDir,
		SubagentSessionsDir:          filepath.Join(agentDir, "subagent-sessions"),
		ForwardingDir:                filepath.Join(sessionsDir, "permission-forwarding"),
		GlobalLogsDir:                globalLogsDir,
		PiInfrastructureDirs:         dirs,
		PiInfrastructureExcludedDirs: []string{globalLogsDir},
	}
}
