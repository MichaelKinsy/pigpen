package pi_permission_system

// Port of src/handlers/lifecycle.ts: session start, reload and shutdown.

// SessionEndedReason is what a permission request still open at shutdown is answered with. It reaches the agent as the block reason
// and the review log as the decider's reason, so both name the same thing (#726).
const SessionEndedReason = "The session ended before this permission request was answered"

// UntrustedProjectMessage is shown when project config is skipped because the project is untrusted, so the reduced (global-only)
// scope is never silent (#644).
const UntrustedProjectMessage = "pi-permission-system: project is not trusted \u2014 skipping project-scoped " +
	"permission configuration. Only global policy applies. Grant project trust " +
	"to load this project's permission rules."

// SessionStartPayload is the minimal subset of SessionStartEvent the handler uses.
type SessionStartPayload struct{ Reason string }

// ResourcesDiscoverPayload is the minimal subset of ResourcesDiscoverEvent the handler uses.
type ResourcesDiscoverPayload struct{ Reason string }

// LifecycleSession is the part of PermissionSession the lifecycle handler drives.
type LifecycleSession interface {
	ResetForNewSession(ctx SessionContext, projectTrusted bool) error
	RefreshConfig(ctx *SessionContext, projectTrusted bool)
	LogResolvedConfigPaths()
	ResolveAgentName(ctx ActiveAgentContext, systemPrompt string) (*string, error)
	Reload(projectTrusted bool)
	GetRuntimeContext() *SessionContext
	Shutdown()
}

// LifecycleResolver is the part of the resolver the handler reads: the policy's config issues for the active agent.
type LifecycleResolver interface {
	GetConfigIssues(agentName *string) []string
}

// SessionLifecycleHandler handles the session lifecycle events: start, reload and shutdown. Its dependencies: the session
// (all mutable state and lifecycle operations), the resolver (config issues), the service lifecycle (the process-global service
// publication: Activate publishes and emits ready, Teardown unsubscribes and unpublishes), the logger, the per-session decision
// audit (its summary is written on shutdown), the config issue reporter (latched per issue, driven here and on every turn, so an
// issue already on disk when the session opens is shown, #933) and the dialog queue (released on shutdown so a gate waiting on a
// dialog nobody can answer any more is unblocked, #965).
type SessionLifecycleHandler struct {
	session          LifecycleSession
	resolver         LifecycleResolver
	serviceLifecycle ServiceLifecycle
	logger           SessionLogger
	audit            DecisionSummaryWriter
	configIssues     ConfigIssueReporting
	dialogs          AskDialogRelease
}

// NewSessionLifecycleHandler is the constructor.
func NewSessionLifecycleHandler(session LifecycleSession, resolver LifecycleResolver, serviceLifecycle ServiceLifecycle, logger SessionLogger,
	audit DecisionSummaryWriter, configIssues ConfigIssueReporting, dialogs AskDialogRelease) *SessionLifecycleHandler {
	return &SessionLifecycleHandler{session: session, resolver: resolver, serviceLifecycle: serviceLifecycle, logger: logger, audit: audit, configIssues: configIssues, dialogs: dialogs}
}

// HandleSessionStart resets the session for the new context, refreshes and reports the config, warns about an untrusted project and
// the policy's config issues, and publishes the node's service. An error is the original's rejection (a failing session reset or a
// failing active-agent lookup): the steps after it do not run.
func (h *SessionLifecycleHandler) HandleSessionStart(event SessionStartPayload, ctx SessionContext) error {
	projectTrusted := ctx.ProjectTrusted()
	// Reset first: it activates the session, binding the context that PermissionSession.Notify, and therefore logger.Warn, delivers
	// through. A refresh before activation has no UI to report into, which is how a config warning present at session start went
	// unseen (#933).
	if err := h.session.ResetForNewSession(ctx, projectTrusted); err != nil {
		return err
	}
	h.session.RefreshConfig(&ctx, projectTrusted)
	h.session.LogResolvedConfigPaths()
	// The config was just re-read above, and the session is activated, so a warning has a UI to reach.
	h.configIssues.Report()
	if !projectTrusted {
		h.warnProjectUntrusted(ctx, "session_start")
	}

	agentName, err := h.session.ResolveAgentName(ctx, "")
	if err != nil {
		return err
	}
	for _, issue := range h.resolver.GetConfigIssues(agentName) {
		h.logger.Warn(issue)
	}

	if event.Reason == "reload" {
		details := newObject()
		details.set("triggeredBy", "session_start")
		details.set("reason", event.Reason)
		details.set("cwd", ctx.Cwd)
		h.logger.Debug("lifecycle.reload", details)
	}

	// Publish the process-global service now that a ctx (and therefore the session id) is available, so an in-process subagent child
	// can be identified and excluded. Emitting ready here keeps the service-resolvable-when-ready ordering contract.
	h.serviceLifecycle.Activate(ctx)
	return nil
}

// HandleResourcesDiscover reloads the session's policy scopes on a reload; any other reason is ignored.
func (h *SessionLifecycleHandler) HandleResourcesDiscover(event ResourcesDiscoverPayload, ctx SessionContext) {
	if event.Reason != "reload" {
		return
	}
	projectTrusted := ctx.ProjectTrusted()
	h.session.Reload(projectTrusted)
	if !projectTrusted {
		h.warnProjectUntrusted(ctx, "resources_discover")
	}
	details := newObject()
	details.set("triggeredBy", "resources_discover")
	details.set("reason", event.Reason)
	if runtime := h.session.GetRuntimeContext(); runtime != nil {
		details.set("cwd", runtime.Cwd)
	} else {
		details.set("cwd", nil)
	}
	h.logger.Debug("lifecycle.reload", details)
}

// warnProjectUntrusted records the project-trust skip in the review log and surfaces a loud warning to the user, so the reduced
// scope is never silent (#644).
func (h *SessionLifecycleHandler) warnProjectUntrusted(ctx SessionContext, phase string) {
	details := newObject()
	details.set("cwd", ctx.Cwd)
	details.set("phase", phase)
	h.logger.Review("project_trust.skipped", details)
	h.logger.Warn(UntrustedProjectMessage)
}

// HandleSessionShutdown clears the status, writes the decision summary, releases every open ask, shuts the session down and tears the
// service down.
func (h *SessionLifecycleHandler) HandleSessionShutdown() {
	if ctx := h.session.GetRuntimeContext(); ctx != nil && ctx.Notifier != nil {
		ctx.Notifier.SetStatus(permissionSystemStatusKey, "")
	}
	h.audit.WriteSummary(h.logger)
	// Ahead of the session shutdown that stops forwarding: a drain awaiting a forwarded ask can only write its response file once
	// that ask is settled.
	h.dialogs.ReleaseAll(SessionEndedReason)
	h.session.Shutdown()
	h.serviceLifecycle.Teardown()
}
