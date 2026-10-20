package pi_permission_system

import (
	"os"
	"path/filepath"
	"strings"
)

// Port of src/config/config-store.ts, config-issue-reporter.ts, config-reporter.ts and status.ts.

// extensionRoot is EXTENSION_ROOT: the directory a legacy extension-local config.json lived in. The Go port has no install
// directory with one, so it is empty and the legacy extension config is never looked for (see PORT.md).
var extensionRoot = ""

// ConfigReader is a read-only view of the current config, for consumers that only read.
type ConfigReader interface {
	Current() PermissionSystemExtensionConfig
}

// ConfigUI is the part of the command context the store writes to: Notify and SetStatus (sdk.Context has both).
type ConfigUI interface {
	Notify(message, level string)
	SetStatus(key, text string)
}

// ConfigLogger is the log seam of the store: debug and review entries with their fields in order.
type ConfigLogger interface {
	Debug(event string, details *jsObject)
	Review(event string, details *jsObject)
}

// ResolvedPolicyPathProvider is the narrow view of the policy manager's resolved paths.
type ResolvedPolicyPathProvider interface {
	GetResolvedPolicyPaths() ResolvedPolicyPaths
}

// ConfigStoreDeps are what a ConfigStore is built from.
type ConfigStoreDeps struct {
	AgentDir    string
	PolicyPaths ResolvedPolicyPathProvider
	Logger      ConfigLogger
}

// configStoreHooks are the store's file system and loader calls, replaceable by the twins as the original's tests mock them.
type configStoreHooks struct {
	loadAndMerge func(agentDir, cwd, extensionRoot string, opts MergeOptions) MergedConfigResult
	loadUnified  func(path string) UnifiedConfigLoadResult
	syncStatus   func(ui ConfigUI, c PermissionSystemExtensionConfig)
	buildEntry   func(o BuildResolvedConfigLogEntryOptions) ResolvedConfigLogEntry
	exists       func(path string) bool
	mkdirAll     func(path string) error
	writeFile    func(path, data string) error
	rename       func(from, to string) error
	remove       func(path string) error
}

func defaultStoreHooks() configStoreHooks {
	return configStoreHooks{
		loadAndMerge: loadAndMergeConfigs,
		loadUnified:  loadUnifiedConfig,
		syncStatus:   syncPermissionSystemStatus,
		buildEntry:   buildResolvedConfigLogEntry,
		exists:       pathExists,
		mkdirAll:     func(p string) error { return os.MkdirAll(p, 0o777) },
		writeFile:    func(p, d string) error { return os.WriteFile(p, []byte(d), 0o666) },
		rename:       os.Rename,
		remove:       os.Remove,
	}
}

// ConfigStore owns the mutable extension config and the operations that read and write it, and the issue list the last load
// produced.
type ConfigStore struct {
	deps         ConfigStoreDeps
	hooks        configStoreHooks
	config       PermissionSystemExtensionConfig
	configIssues []string
}

// NewConfigStore returns a store holding the default config.
func NewConfigStore(deps ConfigStoreDeps) *ConfigStore {
	return &ConfigStore{deps: deps, hooks: defaultStoreHooks(), config: defaultExtensionConfig(), configIssues: []string{}}
}

// Current returns the current extension config.
func (s *ConfigStore) Current() PermissionSystemExtensionConfig { return s.config }

// GetConfigIssues is what is wrong with the config as of the last Refresh: a legacy-file notice, a schema violation, and the
// cross-cutting detectors. The store answers; ConfigIssueReporter decides whether the operator has heard it yet (#933).
func (s *ConfigStore) GetConfigIssues() []string { return s.configIssues }

// Refresh reloads the merged config from disk. cwd scopes the project-level lookup ("" when no session cwd is known yet); with
// projectTrusted false the project scope is withheld so an untrusted repository cannot loosen the operator's global config (#644).
// It takes no UI on purpose: a load that holds one acquires side effects it cannot honor when there is no session yet (#933).
func (s *ConfigStore) Refresh(cwd string, projectTrusted bool) {
	merge := s.hooks.loadAndMerge(s.deps.AgentDir, cwd, extensionRoot, MergeOptions{IncludeProjectScope: &projectTrusted})
	s.config = normalizePermissionSystemConfig(merge.Merged)
	s.configIssues = merge.Issues
	details := newObject()
	if len(merge.Issues) > 0 {
		details.set("warning", strings.Join(merge.Issues, "\n"))
	} else {
		details.set("warning", nil)
	}
	details.set("debugLog", s.config.DebugLog)
	details.set("permissionReviewLog", s.config.PermissionReviewLog)
	details.set("yoloMode", s.config.YoloMode)
	details.set("projectTrusted", projectTrusted)
	s.deps.Logger.Debug("config.loaded", details)
}

// Save writes the updated runtime knobs to the global config file (every other key it holds is kept), then updates the current
// config and syncs the UI status. A failed write is reported with an error notification and changes nothing.
func (s *ConfigStore) Save(next PermissionSystemExtensionConfig, ui ConfigUI) {
	normalized := next // the booleans are always set in Go; normalizePermissionSystemConfig would copy the same values
	globalPath := getGlobalConfigPath(s.deps.AgentDir)
	existing := s.hooks.loadUnified(globalPath)
	merged := existing.Config.toObject()
	merged.set("debugLog", normalized.DebugLog)
	merged.set("permissionReviewLog", normalized.PermissionReviewLog)
	merged.set("yoloMode", normalized.YoloMode)

	tmpPath := globalPath + ".tmp"
	failure := ""
	err := s.hooks.mkdirAll(filepath.Dir(globalPath))
	if err != nil {
		failure = nodeFsMessage(err, "mkdir", filepath.Dir(globalPath))
	} else if err = s.hooks.writeFile(tmpPath, marshalJSON(merged, "  ")+"\n"); err != nil {
		failure = nodeFsMessage(err, "open", tmpPath)
	} else if err = s.hooks.rename(tmpPath, globalPath); err != nil {
		failure = strings.Replace(nodeFsMessage(err, "rename", tmpPath), "'"+tmpPath+"'", "'"+tmpPath+"' -> '"+globalPath+"'", 1)
	}
	if err != nil {
		if s.hooks.exists(tmpPath) {
			_ = s.hooks.remove(tmpPath) // ignore cleanup failures
		}
		ui.Notify("Failed to save permission-system config at '"+globalPath+"': "+failure, "error")
		return
	}
	s.config = normalized
	s.hooks.syncStatus(ui, normalized)
	details := newObject()
	details.set("debugLog", normalized.DebugLog)
	details.set("permissionReviewLog", normalized.PermissionReviewLog)
	details.set("yoloMode", normalized.YoloMode)
	s.deps.Logger.Debug("config.saved", details)
}

// LogResolvedPaths writes the resolved config path set to the review and debug logs.
func (s *ConfigStore) LogResolvedPaths(cwd string) {
	policyPaths := s.deps.PolicyPaths.GetResolvedPolicyPaths()
	agentDir := s.deps.AgentDir
	legacyGlobal := s.hooks.exists(getLegacyGlobalPolicyPath(agentDir))
	legacyProject := cwd != "" && s.hooks.exists(getLegacyProjectPolicyPath(cwd))
	legacyExt := false
	if extensionRoot != "" {
		p := getLegacyExtensionConfigPath(extensionRoot)
		legacyExt = filepath.Clean(p) != filepath.Clean(getGlobalConfigPath(agentDir)) && s.hooks.exists(p)
	}
	entry := s.hooks.buildEntry(BuildResolvedConfigLogEntryOptions{
		PolicyPaths: policyPaths, LegacyGlobalPolicyDetected: legacyGlobal, LegacyProjectPolicyDetected: legacyProject,
		LegacyExtensionConfigDetected: legacyExt})
	s.deps.Logger.Review("config.resolved", entry.toObject())
	s.deps.Logger.Debug("config.resolved", entry.toObject())
}

// ---- config-reporter.ts ----

// ResolvedConfigLogEntry is the `config.resolved` log entry: the policy paths and which legacy files were found.
type ResolvedConfigLogEntry struct {
	ResolvedPolicyPaths
	LegacyGlobalPolicyDetected    bool
	LegacyProjectPolicyDetected   bool
	LegacyExtensionConfigDetected bool
}

// BuildResolvedConfigLogEntryOptions are the inputs of buildResolvedConfigLogEntry; the legacy flags default to false.
type BuildResolvedConfigLogEntryOptions struct {
	PolicyPaths                   ResolvedPolicyPaths
	LegacyGlobalPolicyDetected    bool
	LegacyProjectPolicyDetected   bool
	LegacyExtensionConfigDetected bool
}

func buildResolvedConfigLogEntry(o BuildResolvedConfigLogEntryOptions) ResolvedConfigLogEntry {
	return ResolvedConfigLogEntry{o.PolicyPaths, o.LegacyGlobalPolicyDetected, o.LegacyProjectPolicyDetected, o.LegacyExtensionConfigDetected}
}

// toObject is the entry as the logger writes it, its fields in the original's order (a nil project path is null).
func (e ResolvedConfigLogEntry) toObject() *jsObject {
	o := newObject()
	nullable := func(p *string) any {
		if p == nil {
			return nil
		}
		return *p
	}
	o.set("globalConfigPath", e.GlobalConfigPath)
	o.set("globalConfigExists", e.GlobalConfigExists)
	o.set("projectConfigPath", nullable(e.ProjectConfigPath))
	o.set("projectConfigExists", e.ProjectConfigExists)
	o.set("agentsDir", e.AgentsDir)
	o.set("agentsDirExists", e.AgentsDirExists)
	o.set("projectAgentsDir", nullable(e.ProjectAgentsDir))
	o.set("projectAgentsDirExists", e.ProjectAgentsDirExists)
	o.set("legacyGlobalPolicyDetected", e.LegacyGlobalPolicyDetected)
	o.set("legacyProjectPolicyDetected", e.LegacyProjectPolicyDetected)
	o.set("legacyExtensionConfigDetected", e.LegacyExtensionConfigDetected)
	return o
}

// ---- config-issue-reporter.ts ----

// ConfigIssueSource is the config seam the reporter reads: the issues current as of the last load.
type ConfigIssueSource interface {
	GetConfigIssues() []string
}

// ConfigIssueWarner is the log seam the reporter writes: one operator-facing warning.
type ConfigIssueWarner interface {
	Warn(message string)
}

// ConfigIssueReporting is the seam the session-start and turn-prep handlers drive.
type ConfigIssueReporting interface {
	Report()
}

// ConfigIssueReporter tells the operator what is wrong with their config once per issue, for as long as it is wrong. The latch is
// per issue and the delivery per report; `reported` is replaced on every report, so an issue fixed and then reintroduced is
// announced again.
type ConfigIssueReporter struct {
	source   ConfigIssueSource
	log      ConfigIssueWarner
	reported map[string]bool
}

func NewConfigIssueReporter(source ConfigIssueSource, log ConfigIssueWarner) *ConfigIssueReporter {
	return &ConfigIssueReporter{source: source, log: log, reported: map[string]bool{}}
}

func (r *ConfigIssueReporter) Report() {
	current := r.source.GetConfigIssues()
	var unreported []string
	for _, issue := range current {
		if !r.reported[issue] {
			unreported = append(unreported, issue)
		}
	}
	if len(unreported) > 0 {
		r.log.Warn(strings.Join(unreported, "\n"))
	}
	r.reported = map[string]bool{}
	for _, issue := range current {
		r.reported[issue] = true
	}
}

// ---- status.ts ----

const (
	permissionSystemStatusKey       = extensionID
	permissionSystemYoloStatusValue = "yolo"
)

// getPermissionSystemStatus is the status text: only yolo mode is exposed, "" otherwise (undefined clears the status).
func getPermissionSystemStatus(c PermissionSystemExtensionConfig) string {
	if isYoloModeEnabled(c) {
		return permissionSystemYoloStatusValue
	}
	return ""
}

func syncPermissionSystemStatus(ui ConfigUI, c PermissionSystemExtensionConfig) {
	ui.SetStatus(permissionSystemStatusKey, getPermissionSystemStatus(c))
}
