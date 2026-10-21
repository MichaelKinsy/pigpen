package pi_permission_system

import (
	"errors"
	"sync"
)

// Port of src/authority/authorizer.ts (selectAuthorizer and its deps) and src/authority/authorizer-selection.ts (the context-owning
// selection root).

// AuthorityContext is the part of Pi's ExtensionContext the selection reads: the UI gate and surface, the run mode, the working
// directory, the session manager and the optional system-prompt accessor.
type AuthorityContext struct {
	ForwarderContext
	// Mode is ctx.mode: "tui", "rpc", "json" or "print".
	Mode string
	// PromptUI is ctx.ui as the dialogs need it (select, input, custom, tool expansion). Only read when HasUI.
	PromptUI PermissionPromptUi
	// SessionName is ctx.sessionManager.getSessionName(): nil when the session has no name.
	SessionName func() *string
	// GetSystemPrompt is the host's optional ctx.getSystemPrompt accessor (nil when the host has none): ok false is a value that is
	// not a string, an error is the accessor's throw. ParentAuthorizer reads it to name the requesting agent.
	GetSystemPrompt func() (systemPrompt string, ok bool, err error)
}

// SelectedAuthority is the node's live-authority selection: who decides this node's asks, and whether this node adjudicates them
// with its own chain. The chain role is the selection's product, not a discriminator a consumer re-derives: a node with a UI decides
// locally unless it names another session that is draining its forwarded-permission inbox (#909).
type SelectedAuthority struct {
	// Terminal decides this node's asks, or relays them upward.
	Terminal TerminalAuthorizer
	// AdjudicatesLocally is false when the terminal relays the ask to a serving node (ParentAuthorizer): that node resolves the
	// request against its own recorded authority and escalates it through its chain over the same child-fixed facts (#635), so
	// resolving links here would adjudicate one ask twice.
	AdjudicatesLocally bool
	// RelayTarget is the target this selection itself verified as live, for the relay-transition record; nil on the headless relay
	// arm, where the target is resolved per ask by ParentAuthorizer rather than at selection.
	RelayTarget *PermissionForwardingTarget
}

// AuthorizerSelectionDeps are the construction inputs of SelectAuthorizer.
type AuthorizerSelectionDeps struct {
	// Detection is the single owner of subagent detection; the ParentAuthorizer-selection predicate.
	Detection SubagentDetector
	// Events is the bus LocalUserAuthorizer uses for the permissions:ui_prompt broadcast.
	Events PermissionEventBus
	// Dialogs is the session's dialog queue, threaded into LocalUserAuthorizer. Shared rather than owned by the terminal, because a
	// terminal is rebuilt on every activation and a per-activation queue would serialize nothing (#965).
	Dialogs AskDialogAdmission
	// GetPromptPreferences is read live at prompt time; threaded into LocalUserAuthorizer.
	GetPromptPreferences func() PromptPreferences
	// RequestPermissionDecision is injected for testability; production callers pass RequestPermissionDecision.
	RequestPermissionDecision RequestPermissionDecisionFunc
	// ForwardingDir is the directory ParentAuthorizer reads and writes request and response files under.
	ForwardingDir string
	// Registry is the in-process subagent session registry for forwarding target resolution; nil for none.
	Registry ParentChainRegistry
	// Serving says whether a forwarding target is draining its inbox, on whichever channel can say.
	Serving TargetServingLookup
	// GetForwardingTimeoutMs is read live so a config edit applies to the next ask.
	GetForwardingTimeoutMs func() float64
	Logger                 DebugReviewLogger
}

// SelectAuthorizer selects the live authority for the current context: the single owner of the three-way local / relay / deny
// dispatch, and of the chain role that dispatch implies. It is evaluated on every session activation, which is what lets a node with
// a UI follow its declared parent's liveness: the moment that parent stops serving, the next activation selects the local dialog
// again. The error is a failing subagent detection (a session-directory read), which the original lets propagate.
func SelectAuthorizer(ctx AuthorityContext, deps AuthorizerSelectionDeps) (SelectedAuthority, error) {
	if ctx.HasUI {
		relayTarget, err := resolveLiveRelayTarget(ctx, deps)
		if err != nil {
			return SelectedAuthority{}, err
		}
		if relayTarget == nil {
			return SelectedAuthority{
				Terminal: NewLocalUserAuthorizer(LocalUserAuthorizerDeps{
					UI:                        ctx.PromptUI,
					Mode:                      ctx.Mode,
					Events:                    deps.Events,
					Dialogs:                   deps.Dialogs,
					GetPromptPreferences:      deps.GetPromptPreferences,
					RequestPermissionDecision: deps.RequestPermissionDecision,
					DescribeSession: func() NotificationSession {
						var name *string
						if ctx.SessionName != nil {
							name = ctx.SessionName()
						}
						return NotificationSession{Name: name, Cwd: ctx.Cwd}
					},
				}),
				AdjudicatesLocally: true,
			}, nil
		}
		return SelectedAuthority{Terminal: buildParentAuthorizer(ctx, deps), AdjudicatesLocally: false, RelayTarget: relayTarget}, nil
	}
	isSubagent, err := deps.Detection.IsSubagent(detectionContext(ctx))
	if err != nil {
		return SelectedAuthority{}, err
	}
	if isSubagent {
		return SelectedAuthority{Terminal: buildParentAuthorizer(ctx, deps), AdjudicatesLocally: false}, nil
	}
	return SelectedAuthority{Terminal: DenyingAuthorizer{}, AdjudicatesLocally: true}, nil
}

func detectionContext(ctx AuthorityContext) SubagentDetectionContext {
	if ctx.SessionManager == nil {
		return SubagentDetectionContext{}
	}
	return SubagentDetectionContext{SessionManager: ctx.SessionManager}
}

// resolveLiveRelayTarget is the session a node with a UI should relay to, or nil when it should decide for itself. A human is present
// here, so only a definite "yes" relays: a target nobody can confirm is draining its inbox leaves the ask with the human who is
// already watching. That is the opposite burden of proof from ParentAuthorizer's serving check, where an unjudgeable target waits out
// the timeout because a headless child has no alternative.
func resolveLiveRelayTarget(ctx AuthorityContext, deps AuthorizerSelectionDeps) (*PermissionForwardingTarget, error) {
	sessionID := GetSessionID(ctx.ForwarderContext)
	isSubagent, err := deps.Detection.IsSubagent(detectionContext(ctx))
	if err != nil {
		return nil, err
	}
	target, found := ResolvePermissionForwardingTarget(ResolveTargetOptions{
		IsSubagent:       isSubagent,
		CurrentSessionID: sessionID,
		SessionID:        sessionID,
		Registry:         deps.Registry,
	})
	if !found {
		return nil, nil
	}
	if serving, known := deps.Serving.IsServing(target); !known || !serving {
		return nil, nil
	}
	return &target, nil
}

// buildParentAuthorizer is the relaying terminal for ctx, built from the selection's own deps.
func buildParentAuthorizer(ctx AuthorityContext, deps AuthorizerSelectionDeps) *ParentAuthorizer {
	return NewParentAuthorizer(ctx.ForwarderContext, ParentAuthorizerDeps{
		ForwardingDir:   deps.ForwardingDir,
		Registry:        deps.Registry,
		Serving:         deps.Serving,
		GetTimeoutMs:    deps.GetForwardingTimeoutMs,
		Logger:          deps.Logger,
		GetSystemPrompt: ctx.GetSystemPrompt,
	})
}

// AuthorizerSelectionLifecycle is the lifecycle slice of the selection owner that PermissionSession drives.
type AuthorizerSelectionLifecycle interface {
	Activate(ctx AuthorityContext) error
	Deactivate()
}

// AskEscalator is the ask-escalation seam the gate runner depends on: escalate a single ask to the session's selected authorizer and
// return its decision. There is no "can anyone answer" pre-check: absent authority is the DenyingAuthorizer, which answers by denying
// with a ConfirmationUnavailable marker. An error is the original's rejection.
type AskEscalator interface {
	Escalate(details *PromptPermissionDetails) (PermissionPromptDecision, error)
}

// AuthorizerSelectionConstructorDeps is everything AuthorizerSelection is constructed with: the AuthorizerSelectionDeps
// SelectAuthorizer itself needs, plus the collaborators only the type uses to resolve and run the chain.
type AuthorizerSelectionConstructorDeps struct {
	AuthorizerSelectionDeps
	Prompter PermissionPrompterApi
	// GetPermissionQuery is the session-scoped query injected into each chain link (ADR 0007 section 3).
	GetPermissionQuery func() PermissionQuery
	// AuthorizerRegistry is the read-only lookup of registered links by name.
	AuthorizerRegistry AuthorizerLookup
	// GetAuthorizerChain is the operator's configured link names, read live per ask.
	GetAuthorizerChain func() []string
	// ChainAudit is told about each configured name the registry could not resolve.
	ChainAudit UnregisteredLinkAuditor
}

// AuthorizerSelection is the context-owning selection root for the authorizer spine: it owns the stored context's selection, runs
// SelectAuthorizer once per activation, and implements AskEscalator by delegating to the selected authorizer via the prompter. It is
// safe for concurrent use: the gate's local ask, a forwarded ask a poll tick drains and an activation can overlap.
type AuthorizerSelection struct {
	deps AuthorizerSelectionConstructorDeps

	mu          sync.Mutex
	authority   *SelectedAuthority
	relayTarget *PermissionForwardingTarget
}

// NewAuthorizerSelection is `new AuthorizerSelection(deps)`.
func NewAuthorizerSelection(deps AuthorizerSelectionConstructorDeps) *AuthorizerSelection {
	return &AuthorizerSelection{deps: deps}
}

// Activate selects the live authority for ctx and stores it. The non-terminal chain is composed per ask in Escalate, not here: ADR
// 0007 section 4 lets a link register in a permissions:ready handler that may fire after activation, so link resolution is deferred
// to the session's first ask. On an error the previous selection stays in place.
func (s *AuthorizerSelection) Activate(ctx AuthorityContext) error {
	authority, err := SelectAuthorizer(ctx, s.deps.AuthorizerSelectionDeps)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recordRelayTransition(authority.RelayTarget)
	s.authority = &authority
	return nil
}

// recordRelayTransition records that this node started, stopped, or redirected its relaying. Activate runs on every turn event, so
// only a change is worth a line: the pair reads beside the serving node's own forwarded_permission.serving_started and
// serving_stopped, which is what makes a misdirected relay a one-line diff across the two sessions. A node that never relays writes
// nothing at all. The caller holds s.mu.
func (s *AuthorizerSelection) recordRelayTransition(target *PermissionForwardingTarget) {
	previous := s.relayTarget
	if (previous == nil) == (target == nil) && (previous == nil || previous.SessionID == target.SessionID) {
		return
	}
	s.relayTarget = target
	if previous != nil {
		entry := newObject()
		entry.set("targetSessionId", previous.SessionID)
		s.deps.Logger.Review("forwarded_permission.relay_stopped", entry)
	}
	if target != nil {
		entry := newObject()
		entry.set("targetSessionId", target.SessionID)
		entry.set("channel", string(target.Source))
		s.deps.Logger.Review("forwarded_permission.relay_started", entry)
	}
}

// linksFor is the chain links for this ask. A node that adjudicates locally resolves its configured names; a relaying node resolves
// none: its terminal hands the ask to a serving node, which resolves the request against its own recorded authority and escalates it
// through its own chain over the same child-fixed facts (#635), so running links here would adjudicate one ask twice, and a relaying
// node cannot host a link in the first place (#699). The delegation is recorded rather than reported as a fail-safe skip.
func (s *AuthorizerSelection) linksFor(authority SelectedAuthority, requestID string) []NamedAuthorizer {
	configured := s.deps.GetAuthorizerChain()
	if len(configured) == 0 {
		return nil
	}
	if !authority.AdjudicatesLocally {
		links := make([]any, len(configured))
		for i, name := range configured {
			links[i] = name
		}
		entry := newObject()
		entry.set("requestId", requestID)
		entry.set("links", links)
		s.deps.Logger.Review("authorizer_chain_delegated", entry)
		return nil
	}
	return s.resolveConfiguredLinks(configured, requestID)
}

// resolveConfiguredLinks resolves the operator's authorizerChain names to registered links, in config order (ADR 0007 invariant 1).
// An unregistered name is skipped fail-safe (invariant 2: more prompting, never less) and handed to the chain audit; each resolved
// link is wrapped in the bounded-delegation envelope so an allow on an excluded surface cannot exceed the operator's policy. The
// resolved names are recorded against the ask before any link runs: a link that defers decides nothing and would otherwise leave no
// evidence it was consulted at all.
func (s *AuthorizerSelection) resolveConfiguredLinks(configured []string, requestID string) []NamedAuthorizer {
	var links []NamedAuthorizer
	var resolved []any
	for _, name := range configured {
		authorize := s.deps.AuthorizerRegistry.Get(name)
		if authorize == nil {
			s.deps.ChainAudit.AuditUnregisteredLink(UnregisteredLink{RequestID: requestID, Name: name})
			continue
		}
		resolved = append(resolved, name)
		links = append(links, NamedAuthorizer{Name: name, Authorize: EncloseInDelegationEnvelope(authorize)})
	}
	if len(resolved) > 0 {
		entry := newObject()
		entry.set("requestId", requestID)
		entry.set("links", resolved)
		s.deps.Logger.Review("authorizer_chain_resolved", entry)
	}
	return links
}

// AdjudicatesLocally implements AdjudicationRole: whether this node adjudicates its own asks. It reports true with no selection
// stored (before activation, or after deactivation): production never reads it there, and "this node adjudicates" is the fail-soft
// answer, which tells a sibling to register, which a relaying node accepts and records rather than refusing (ADR 0012 decision 4).
func (s *AuthorizerSelection) AdjudicatesLocally() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.authority == nil {
		return true
	}
	return s.authority.AdjudicatesLocally
}

// Deactivate clears the stored selection.
func (s *AuthorizerSelection) Deactivate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recordRelayTransition(nil)
	s.authority = nil
}

// errNotActivated is Escalate's rejection before activation.
var errNotActivated = errors.New("escalate called before the session was activated")

// Escalate escalates an ask through the composed chain and returns its decision. It resolves this ask's links freshly (so a link
// registered any time before this first ask is honoured) and composes them ahead of the selected terminal. With zero links (no chain
// configured, or a relaying node that delegates adjudication to the serving node) the composed value IS the terminal instance, so
// behaviour is identical to a bare terminal escalation. It fails before the session was activated. Implements AskEscalator.
func (s *AuthorizerSelection) Escalate(details *PromptPermissionDetails) (PermissionPromptDecision, error) {
	s.mu.Lock()
	authority := s.authority
	s.mu.Unlock()
	if authority == nil {
		return PermissionPromptDecision{}, errNotActivated
	}
	chain := ComposeAuthorizerChain(s.linksFor(*authority, details.RequestID), authority.Terminal, s.deps.GetPermissionQuery(), s.deps.Logger)
	return s.deps.Prompter.Prompt(chain, details)
}
