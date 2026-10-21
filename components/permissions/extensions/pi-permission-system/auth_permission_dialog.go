package pi_permission_system

// Port of src/authority/permission-dialog.ts: the decision value types, the denial-reason helpers and the select/input fallback
// dialog.

// PermissionDecisionState is the outcome of a permission prompt.
type PermissionDecisionState string

// The five decision states.
const (
	DecisionApproved               PermissionDecisionState = "approved"
	DecisionApprovedForSession     PermissionDecisionState = "approved_for_session"
	DecisionApprovedForServingSess PermissionDecisionState = "approved_for_serving_session"
	DecisionDenied                 PermissionDecisionState = "denied"
	DecisionDeniedWithReason       PermissionDecisionState = "denied_with_reason"
)

// IsPermissionDecisionState is isPermissionDecisionState.
func IsPermissionDecisionState(v any) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	switch PermissionDecisionState(s) {
	case DecisionApproved, DecisionApprovedForSession, DecisionApprovedForServingSess, DecisionDenied, DecisionDeniedWithReason:
		return true
	}
	return false
}

// PermissionPromptDecision is the answer to an ask. DecidedBy is required (every producer names its decider); a nil DenialReason
// is undefined; ConfirmationUnavailable is the original's `confirmationUnavailable?: true`.
type PermissionPromptDecision struct {
	Approved                bool
	State                   PermissionDecisionState
	DenialReason            *string
	ConfirmationUnavailable bool
	// SessionGrantWidth is "" when the human chose no width (undefined): absent means "proven" wherever the value travels.
	SessionGrantWidth SessionGrantWidth
	DecidedBy         DecisionSource
}

// UnattributedDecision is a decision before its decider is known (UnattributedDecision = Omit<PermissionPromptDecision,
// "decidedBy">). It is a type of its own so a producer that states only the outcome cannot be handed on as a decided one: the
// site that chose the producer attributes it with Attribute.
type UnattributedDecision struct {
	Approved                bool
	State                   PermissionDecisionState
	DenialReason            *string
	ConfirmationUnavailable bool
	SessionGrantWidth       SessionGrantWidth
}

// Attribute stamps the decider on the decision (`{ ...decision, decidedBy }`).
func (d UnattributedDecision) Attribute(decidedBy DecisionSource) PermissionPromptDecision {
	return PermissionPromptDecision{
		Approved:                d.Approved,
		State:                   d.State,
		DenialReason:            d.DenialReason,
		ConfirmationUnavailable: d.ConfirmationUnavailable,
		SessionGrantWidth:       d.SessionGrantWidth,
		DecidedBy:               decidedBy,
	}
}

// NormalizePermissionDenialReason is normalizePermissionDenialReason for a string-or-undefined value: trimmed, or nil when empty or
// not a string.
func NormalizePermissionDenialReason(v *string) *string {
	if v == nil {
		return nil
	}
	t := jsTrim(*v)
	if len(t) == 0 {
		return nil
	}
	return &t
}

// CreateDeniedPermissionDecision is createDeniedPermissionDecision.
func CreateDeniedPermissionDecision(denialReason *string) UnattributedDecision {
	if reason := NormalizePermissionDenialReason(denialReason); reason != nil {
		return UnattributedDecision{Approved: false, State: DecisionDeniedWithReason, DenialReason: reason}
	}
	return UnattributedDecision{Approved: false, State: DecisionDenied}
}

// sessionApproval is the original's sessionApproval/sessionDecision: a session-granting decision, naming its width only when it is
// not the default, so a narrow grant serializes exactly as it did before the width option existed.
func sessionApprovalDecision(state PermissionDecisionState, width SessionGrantWidth) UnattributedDecision {
	d := UnattributedDecision{Approved: true, State: state}
	if width == "family" {
		d.SessionGrantWidth = width
	}
	return d
}

const (
	approveOption           = "Yes"
	approveForSessionOption = "Yes, for this session"
	denyOption              = "No"
	denyWithReasonOption    = "No, provide reason"
)

// SessionWidthOption is RequestPermissionOptions.sessionWidth: the extra option shown beside the proven-direction one.
type SessionWidthOption struct{ Label string }

// SessionScopeOption is RequestPermissionOptions.sessionScope (forwarded asks only).
type SessionScopeOption struct{ SubagentLabel, ServingSessionLabel string }

// RequestPermissionOptions are the ask-supplied dialog options. A nil pointer is undefined.
type RequestPermissionOptions struct {
	// SessionLabel overrides the "for this session" option label.
	SessionLabel *string
	// SessionWidth is present iff the ask's session grant can be widened to both directions (#813).
	SessionWidth *SessionWidthOption
	// SessionScope: when set, choosing the "for this session" option opens a second select asking whether the grant applies to the
	// requesting subagent only (the least-privilege default) or the whole serving session.
	SessionScope *SessionScopeOption
}

// RequestPermissionDecisionFromUi is requestPermissionDecisionFromUi: the select/input flow used outside TUI mode. An error is
// the original's rejection of a ui call.
func RequestPermissionDecisionFromUi(ui PermissionDecisionUi, title, message string, options *RequestPermissionOptions) (UnattributedDecision, error) {
	sessionOption := approveForSessionOption
	widthOption := ""
	if options != nil {
		if options.SessionLabel != nil {
			sessionOption = *options.SessionLabel
		}
		if options.SessionWidth != nil {
			widthOption = options.SessionWidth.Label
		}
	}
	decisionOptions := []string{approveOption, sessionOption}
	if widthOption != "" {
		decisionOptions = append(decisionOptions, widthOption)
	}
	decisionOptions = append(decisionOptions, denyOption, denyWithReasonOption)

	selected, ok, err := ui.Select(title+"\n"+message, decisionOptions)
	if err != nil {
		return UnattributedDecision{}, err
	}

	if ok && selected == approveOption {
		return UnattributedDecision{Approved: true, State: DecisionApproved}, nil
	}

	if ok && (selected == sessionOption || (widthOption != "" && selected == widthOption)) {
		// The two session options differ only in the width they grant; the scope question below is the same for both.
		width := SessionGrantWidth("proven")
		if widthOption != "" && selected == widthOption {
			width = "family"
		}
		if options != nil && options.SessionScope != nil {
			scope, scopeOK, err := ui.Select(title+"\nApply this session grant to:", []string{options.SessionScope.SubagentLabel, options.SessionScope.ServingSessionLabel})
			if err != nil {
				return UnattributedDecision{}, err
			}
			state := DecisionApprovedForSession
			// A cancelled scope select (undefined) falls back to the least-privilege subagent scope.
			if scopeOK && scope == options.SessionScope.ServingSessionLabel {
				state = DecisionApprovedForServingSess
			}
			return sessionApprovalDecision(state, width), nil
		}
		return sessionApprovalDecision(DecisionApprovedForSession, width), nil
	}

	if ok && selected == denyWithReasonOption {
		value, valueOK, err := ui.Input(title+"\nShare why this request was denied (optional).", "Reason shown back to the agent")
		if err != nil {
			return UnattributedDecision{}, err
		}
		var reason *string
		if valueOK {
			reason = &value
		}
		return CreateDeniedPermissionDecision(NormalizePermissionDenialReason(reason)), nil
	}

	return CreateDeniedPermissionDecision(nil), nil
}
