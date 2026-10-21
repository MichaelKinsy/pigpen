package pi_permission_system

// Port of src/authority/local-user-authorizer.ts: the authorizer for a session with an active UI, which prompts the human here.

// LocalUserAuthorizerDeps are the dependencies of a LocalUserAuthorizer.
type LocalUserAuthorizerDeps struct {
	// UI is the active session's UI surface (select/input plus the inline custom dialog).
	UI PermissionPromptUi
	// Mode is the session run mode; the dispatcher renders the inline dialog only in "tui".
	Mode string
	// Events is the bus used for the permissions:ui_prompt broadcast.
	Events PermissionEventBus
	// Dialogs serializes this session's dialogs so no ask replaces another (#965).
	Dialogs AskDialogAdmission
	// GetPromptPreferences is read live at prompt time so a settings-modal toggle takes effect on the next prompt.
	GetPromptPreferences func() PromptPreferences
	// RequestPermissionDecision is injected for testability; production callers pass RequestPermissionDecision.
	RequestPermissionDecision RequestPermissionDecisionFunc
	// DescribeSession is read when each prompt opens, so a session named mid-session is picked up.
	DescribeSession func() NotificationSession
}

// LocalUserAuthorizer prompts the human here. It emits the permissions:ui_prompt broadcast before showing the dialog, so observers
// know a decision is imminent; this is the single emit site: a forwarded ask carries its provenance on details.Forwarding, which this
// type renders (populated forwarding context and the "(Subagent)" title) so the broadcast stays non-degraded (#292). Every ask goes
// through the session's AskDialogAdmission, because the host holds one inline dialog slot: a second presentation mounts over the first
// and strands its call (#965).
type LocalUserAuthorizer struct{ deps LocalUserAuthorizerDeps }

// NewLocalUserAuthorizer is `new LocalUserAuthorizer(deps)`.
func NewLocalUserAuthorizer(deps LocalUserAuthorizerDeps) *LocalUserAuthorizer {
	return &LocalUserAuthorizer{deps: deps}
}

// Authorize implements TerminalAuthorizer.
func (a *LocalUserAuthorizer) Authorize(details *PromptPermissionDetails) (PermissionPromptDecision, error) {
	return a.deps.Dialogs.RunDecision(func() (PermissionPromptDecision, error) { return a.present(details) }, unansweredDecision)
}

// present announces the imminent prompt, then shows it. Both live inside the queued region: permissions:ui_prompt is documented as
// firing immediately before the user-facing UI is invoked, so an emit at admission would alert a notification consumer for a dialog
// that is still minutes of deliberation away.
func (a *LocalUserAuthorizer) present(details *PromptPermissionDetails) (PermissionPromptDecision, error) {
	EmitUiPromptEvent(a.deps.Events, BuildUiPromptFromDetails(details))
	title := "Permission Required"
	if details.Forwarding != nil {
		title = "Permission Required (Subagent)"
	}
	request := details.Payload.Request
	view := PermissionPromptView{
		PromptPreferences: a.deps.GetPromptPreferences(),
		Mode:              a.deps.Mode,
		UI:                a.deps.UI,
		Notice: DescribePromptNotice(title, NoticeRequestFacts{ToolName: request.ToolName, Surface: request.Surface, Requester: request.Requester},
			a.deps.DescribeSession()),
	}
	return a.deps.RequestPermissionDecision(view, title, details.Payload, buildRequestOptions(details))
}

// unansweredDecision is the answer an ask gets when the session released it before a human ruled. It mirrors ParentAuthorizer's
// abandonment: ConfirmationUnavailable keeps it out of the "User denied" family, since a user who was never asked denied nothing
// (#719), and the agent-facing reason and the provenance record reuse one string so what the model is told and what the log
// attributes cannot drift (#726).
func unansweredDecision(reason string) PermissionPromptDecision {
	return PermissionPromptDecision{
		Approved:                false,
		State:                   DecisionDenied,
		ConfirmationUnavailable: true,
		DenialReason:            &reason,
		DecidedBy:               DecisionSource{Kind: "unavailable", Reason: &reason},
	}
}

// buildRequestOptions composes the dialog options this ask offers from three independent groups. The label names what the session
// grant covers: the proven direction and target for a directional path ask, else a gate-supplied label, else the target alone for a
// path ask that proves no direction. An ask whose grants all prove the same direction additionally offers the both-directions width
// (#813). A forwarded ask additionally offers the scope choice (subagent vs whole session). They compose rather than exclude, and an
// ask that qualifies for none yields nil so the dialog keeps its defaults.
func buildRequestOptions(details *PromptPermissionDetails) *RequestPermissionOptions {
	var grants []ApprovalGrant
	if details.SessionApproval != nil {
		grants = details.SessionApproval.Grants
	}
	var widths *DirectionalSessionLabels
	if direction, ok := ProvenDirectionOf(grants); ok {
		labels := BuildDirectionalSessionLabels(direction, DescribeGrantTarget(grants))
		widths = &labels
	}
	// `widths?.sessionLabel ?? details.sessionLabel ?? buildPathAccessSessionLabel(grants) ?? undefined`: the first one present wins.
	var sessionLabel *string
	switch {
	case widths != nil:
		sessionLabel = &widths.SessionLabel
	case details.SessionLabel != nil:
		sessionLabel = details.SessionLabel
	default:
		sessionLabel = BuildPathAccessSessionLabel(grants)
	}

	options := RequestPermissionOptions{}
	offered := false
	if sessionLabel != nil && *sessionLabel != "" {
		options.SessionLabel = sessionLabel
		offered = true
	}
	if widths != nil {
		options.SessionWidth = &SessionWidthOption{Label: widths.WidenedLabel}
		offered = true
	}
	if details.Forwarding != nil && len(grants) > 0 {
		scope := BuildForwardedScopeLabels(details.Forwarding.RequesterAgentName, grants)
		options.SessionScope = &SessionScopeOption{SubagentLabel: scope.SubagentLabel, ServingSessionLabel: scope.ServingSessionLabel}
		offered = true
	}
	if !offered {
		return nil
	}
	return &options
}
