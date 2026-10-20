package pi_permission_system

import (
	"errors"
	"sync"
)

// Port of src/session/permission-session.ts: all mutable session state behind operations instead of fields.

// SessionContext is the slice of the host's ExtensionContext a PermissionSession reads: the authority context (the forwarder context
// with cwd, hasUI, the dialog UI and the session manager whose entries the active-agent lookup reads, plus the run mode, the prompt UI
// and the system-prompt accessor the authorizer selection reads) and the notify and status UI. Notifier is nil when the host has no UI.
type SessionContext struct {
	AuthorityContext
	Notifier ConfigUI
}

// GetEntries makes the context an ActiveAgentContext: the session manager's entries. A context with no session manager is an error,
// never an empty session (the caller must not read it as "no agent").
func (c SessionContext) GetEntries() ([]SessionEntryView, error) {
	if c.SessionManager == nil {
		return nil, errors.New("the session context has no session manager")
	}
	return c.SessionManager.GetEntries()
}

// SessionConfigStore is the part of ConfigStore the session reads: refresh, the current config and the path log. ConfigStore
// satisfies it.
type SessionConfigStore interface {
	Current() PermissionSystemExtensionConfig
	Refresh(cwd string, projectTrusted bool)
	LogResolvedPaths(cwd string)
}

// ToolSurfaceObservation is what the host reports about the tool surface this turn.
type ToolSurfaceObservation struct {
	Active     []string
	Registered map[string]bool
}

// ToolSurfaceResolution is the tool-surface baseline's answer: the exposed, withheld and restored tools and whether anything changed.
type ToolSurfaceResolution = surfaceResolution

// PermissionSession encapsulates all mutable session state: it owns the manager's cwd binding, the session rules' lifetime, the
// skill entries, the last known agent name, the path normalizer and the tool-surface baseline. It is safe for concurrent use.
type PermissionSession struct {
	paths               ExtensionPaths
	forwarding          ForwardingController
	permissionManager   ScopedPermissionManager
	sessionRules        *SessionRules
	configStore         SessionConfigStore
	authorizerSelection AuthorizerSelectionLifecycle
	flavor              PathFlavor

	mu                  sync.Mutex
	context             *SessionContext
	skillEntries        []SkillPromptEntry
	knownAgentName      *string
	pathNormalizer      *PathNormalizer
	toolSurfaceBaseline surfaceBaseline

	// the agent-name lookups are replaceable so a twin can stand in for the active-agent module, as the original's test mocks it
	agentNameLookup       func(ctx ActiveAgentContext) (*string, error)
	promptAgentNameLookup func(systemPrompt string) *string
}

// NewPermissionSession is the constructor.
func NewPermissionSession(paths ExtensionPaths, forwarding ForwardingController, permissionManager ScopedPermissionManager, sessionRules *SessionRules,
	configStore SessionConfigStore, authorizerSelection AuthorizerSelectionLifecycle, flavor PathFlavor) *PermissionSession {
	return &PermissionSession{
		paths: paths, forwarding: forwarding, permissionManager: permissionManager, sessionRules: sessionRules,
		configStore: configStore, authorizerSelection: authorizerSelection, flavor: flavor,
		// Placeholder until the first Activate binds the real cwd; every gate evaluation runs after activate.
		pathNormalizer:        NewPathNormalizer(flavor, ""),
		agentNameLookup:       GetActiveAgentName,
		promptAgentNameLookup: GetActiveAgentNameFromSystemPrompt,
	}
}

// Activate stores the current context, rebuilds the path normalizer for its cwd, starts forwarding and activates the gateway. The
// normalizer is rebuilt here so it always tracks the active context's cwd, which closes the fail-open gap if a tool call ever arrives
// before session_start.
// The error is a failing subagent detection the authorizer selection raises, which the original lets propagate out of activate.
func (s *PermissionSession) Activate(ctx SessionContext) error {
	s.mu.Lock()
	s.context = &ctx
	s.pathNormalizer = NewPathNormalizer(s.flavor, ctx.Cwd)
	s.mu.Unlock()
	s.forwarding.Start(ctx.ForwarderContext)
	return s.authorizerSelection.Activate(ctx.AuthorityContext)
}

// Deactivate clears the context, stops forwarding and deactivates the authorizer selection.
func (s *PermissionSession) Deactivate() {
	s.mu.Lock()
	s.context = nil
	s.mu.Unlock()
	s.forwarding.Stop()
	s.authorizerSelection.Deactivate()
}

// GetRuntimeContext returns the current runtime context, nil if not activated.
func (s *PermissionSession) GetRuntimeContext() *SessionContext {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.context
}

// Notify surfaces a warning to the user through the active UI context, if any.
func (s *PermissionSession) Notify(message string) {
	if ctx := s.GetRuntimeContext(); ctx != nil && ctx.Notifier != nil {
		ctx.Notifier.Notify(message, "warning")
	}
}

// ResetForNewSession resets all mutable state for a new session: it configures the manager for ctx.Cwd (or global-only when
// projectTrusted is false, withholding the project cwd so an untrusted project's policy scopes are not loaded, #644), clears the skill
// entries and the tool-surface baseline, and activates the new context.
func (s *PermissionSession) ResetForNewSession(ctx SessionContext, projectTrusted bool) error {
	cwd := ""
	if projectTrusted {
		cwd = ctx.Cwd
	}
	s.permissionManager.ConfigureForCwd(cwd)
	s.mu.Lock()
	s.skillEntries = nil
	s.toolSurfaceBaseline.reset()
	s.mu.Unlock()
	return s.Activate(ctx)
}

// Shutdown clears the session rules, the skill entries and the tool-surface baseline, and deactivates context and forwarding.
func (s *PermissionSession) Shutdown() {
	s.sessionRules.Clear()
	s.mu.Lock()
	s.skillEntries = nil
	s.toolSurfaceBaseline.reset()
	s.mu.Unlock()
	s.Deactivate()
}

// Reload reloads the permission manager and clears the skill entries for the current context (a config reload). With projectTrusted
// false the project cwd is withheld, so a reload in an untrusted project reloads only global policy; a trust grant on a later reload
// re-includes the project scope (#644). The tool-surface baseline survives a reload: reseeding from the already-filtered active set
// would strand the tool a relaxed policy just un-denied (#873).
func (s *PermissionSession) Reload(projectTrusted bool) {
	cwd := ""
	if projectTrusted {
		if ctx := s.GetRuntimeContext(); ctx != nil {
			cwd = ctx.Cwd
		}
	}
	s.permissionManager.ConfigureForCwd(cwd)
	s.mu.Lock()
	s.skillEntries = nil
	s.mu.Unlock()
}

// ResolveExposedTools answers which tools the agent may see this turn, applying isExposed to the session's pre-filter tool surface
// rather than to the previous turn's filtered result.
func (s *PermissionSession) ResolveExposedTools(observation ToolSurfaceObservation, isExposed func(toolName string) bool) ToolSurfaceResolution {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.toolSurfaceBaseline.resolveExposed(observation.Active, observation.Registered, isExposed)
}

// GetActiveSkillEntries returns the skill entries of this session.
func (s *PermissionSession) GetActiveSkillEntries() []SkillPromptEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.skillEntries
}

// SetActiveSkillEntries replaces the skill entries.
func (s *PermissionSession) SetActiveSkillEntries(entries []SkillPromptEntry) {
	s.mu.Lock()
	s.skillEntries = entries
	s.mu.Unlock()
}

// ResolveAgentName resolves the active agent name from the session context, the system prompt, or the last known name, updating the
// last known name as a side effect. nil is null. An error is the session manager's throw: the caller must not read it as "no agent"
// (the global scope is the least restrictive one an agent scope can only tighten).
func (s *PermissionSession) ResolveAgentName(ctx ActiveAgentContext, systemPrompt string) (*string, error) {
	fromSession, err := s.agentNameLookup(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if fromSession != nil {
		s.knownAgentName = fromSession
		return fromSession, nil
	}
	if fromPrompt := s.promptAgentNameLookup(systemPrompt); fromPrompt != nil {
		s.knownAgentName = fromPrompt
		return fromPrompt, nil
	}
	return s.knownAgentName, nil
}

// LastKnownActiveAgentName is read by the config modal adapter: `permissionManager.getComposedConfigRules(lastKnownActiveAgentName)`.
func (s *PermissionSession) LastKnownActiveAgentName() *string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.knownAgentName
}

// RefreshConfig reloads the merged config from disk, then brings the status bar in step with it. With projectTrusted false the project
// scope is withheld (#644). ctx nil is the original's undefined: the store refreshes with no cwd and nothing is synced. The status sync
// lives here, a UI side effect keyed on the session's context (#933).
func (s *PermissionSession) RefreshConfig(ctx *SessionContext, projectTrusted bool) {
	cwd := ""
	if ctx != nil {
		cwd = ctx.Cwd
	}
	s.configStore.Refresh(cwd, projectTrusted)
	if ctx != nil && ctx.HasUI && ctx.Notifier != nil {
		syncPermissionSystemStatus(ctx.Notifier, s.configStore.Current())
	}
}

// LogResolvedConfigPaths writes the resolved config path set to the review and debug logs.
func (s *PermissionSession) LogResolvedConfigPaths() {
	cwd := ""
	if ctx := s.GetRuntimeContext(); ctx != nil {
		cwd = ctx.Cwd
	}
	s.configStore.LogResolvedPaths(cwd)
}

// Config reads the current extension config.
func (s *PermissionSession) Config() PermissionSystemExtensionConfig { return s.configStore.Current() }

// GetInfrastructureReadScope is where infrastructure reads are auto-allowed: the static roots from ExtensionPaths plus the
// config-derived piInfrastructureReadPaths.
func (s *PermissionSession) GetInfrastructureReadScope() InfrastructureReadScope {
	dirs := append([]string{}, s.paths.PiInfrastructureDirs...)
	dirs = append(dirs, s.Config().PiInfrastructureReadPaths...)
	return InfrastructureReadScope{Dirs: dirs, ExcludedDirs: s.paths.PiInfrastructureExcludedDirs}
}

// GetPathNormalizer is the session's PathNormalizer, carrying the host path flavor and the session cwd; rebuilt on every Activate.
func (s *PermissionSession) GetPathNormalizer() *PathNormalizer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pathNormalizer
}

// GetToolPreviewLimits is the resolved tool-preview formatter options, the built-in limits regardless of config.
func (s *PermissionSession) GetToolPreviewLimits() ToolPreviewFormatterOptions {
	return ResolveToolPreviewLimits()
}

// GetShellToolAliases is the configured shell-tool aliases (`shellTools`), mapping a non-bash tool name to the input arguments holding
// its command and optional working directory; nil when none are configured. An entry without a string commandArgument is dropped
// (the config schema rejects it).
func (s *PermissionSession) GetShellToolAliases() ShellToolsConfig {
	raw := s.Config().ShellTools
	if raw == nil {
		return nil
	}
	aliases := ShellToolsConfig{}
	for _, name := range raw.order() {
		entry, _ := raw.vals[name].(*jsObject)
		if entry == nil {
			continue
		}
		cmd, ok := entry.str("commandArgument")
		if !ok {
			continue
		}
		workdir, _ := entry.str("workdirArgument")
		aliases[name] = ShellToolAlias{CommandArgument: cmd, WorkdirArgument: workdir}
	}
	return aliases
}
