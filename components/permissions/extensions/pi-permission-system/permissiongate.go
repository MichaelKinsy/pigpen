package pi_permission_system

// Port of src/policy/permission-gate.ts: the deny/ask/allow permission gate. A pure decision function: all IO is injected.

// SessionGrant is set when the human granted the ask for the whole session, carrying the width to record it at.
type SessionGrant struct{ Width SessionGrantWidth }

// PermissionGateResult is the result of applying the permission gate. Both arms name what decided: the gate is the one place that
// knows whether recorded authority answered or an escalation did (#772). Action "allow" may carry SessionGrant; "block" carries
// Reason.
type PermissionGateResult struct {
	Action    string
	DecidedBy DecisionSource
	// SessionGrant is non-nil only on an allow granted for the whole session.
	SessionGrant *SessionGrant
	// Reason is what the agent is told on a block.
	Reason string
}

// PermissionGateMessages are the message strings and factories for each outcome.
type PermissionGateMessages struct {
	// DenyReason is what the agent is told when recorded authority denied the request.
	DenyReason string
	// RefusedReason is what the agent is told when an escalation refused it. One factory rather than one per outcome: which sentence a
	// refusal earns follows from the decision's own decider (#772).
	RefusedReason func(decision PermissionPromptDecision) string
}

// PermissionGateParams is everything the gate needs, with no dependency on the host context.
type PermissionGateParams struct {
	// State is the resolved permission state: "allow", "deny" or "ask".
	State string
	// PromptForApproval escalates the ask to the session's authorizer; called for every ask. An error is the original's rejection and
	// propagates.
	PromptForApproval func() (PermissionPromptDecision, error)
	// CanGrantForSession is whether this ask has a session-approval suggestion to record when the user selects "for this session".
	CanGrantForSession bool
	// WriteLog writes a review-log entry; called for the deny path.
	WriteLog func(event string, extra *jsObject)
	// LogContext holds the log fields shared across all log calls for this gate.
	LogContext *jsObject
	// DecidedByRule is the rule that resolved this gate: the decider for both arms that never escalate.
	DecidedByRule DecisionSource
	Messages      PermissionGateMessages
}

// ApplyPermissionGate applies the deny/ask/allow permission gate.
func ApplyPermissionGate(p PermissionGateParams) (PermissionGateResult, error) {
	if p.State == "deny" {
		extra := newObject()
		if p.LogContext != nil {
			extra = p.LogContext.clone()
		}
		extra.set("resolution", "policy_denied")
		extra.set("decidedBy", p.DecidedByRule.JS())
		p.WriteLog("permission_request.blocked", extra)
		return PermissionGateResult{Action: "block", DecidedBy: p.DecidedByRule, Reason: p.Messages.DenyReason}, nil
	}

	if p.State == "ask" {
		decision, err := p.PromptForApproval()
		if err != nil {
			return PermissionGateResult{}, err
		}
		decidedBy := decision.DecidedBy
		if !decision.Approved {
			// The gate writes no review entry for an ask denial: the prompter brackets it (waiting/denied).
			return PermissionGateResult{Action: "block", DecidedBy: decidedBy, Reason: p.Messages.RefusedReason(decision)}, nil
		}
		if decision.State == DecisionApprovedForSession && p.CanGrantForSession {
			width := decision.SessionGrantWidth
			if width == "" {
				// Absent means the width every producer chose before #813.
				width = "proven"
			}
			return PermissionGateResult{Action: "allow", DecidedBy: decidedBy, SessionGrant: &SessionGrant{Width: width}}, nil
		}
		return PermissionGateResult{Action: "allow", DecidedBy: decidedBy}, nil
	}

	if p.State != "allow" {
		// The original's type admits only the three states; a state outside them is never an allow here (fail closed).
		return PermissionGateResult{Action: "block", DecidedBy: p.DecidedByRule, Reason: p.Messages.DenyReason}, nil
	}
	return PermissionGateResult{Action: "allow", DecidedBy: p.DecidedByRule}, nil
}
