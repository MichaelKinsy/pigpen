package pi_permission_system

// Port of src/authority/decision-resolution.ts.

// DecisionOutcome is what became of the request, as the gate that ran it observed.
type DecisionOutcome struct{ Approved, ForSession bool }

// ResolutionFor is resolutionFor: name how a decision was reached, from the decider stamped on it.
func ResolutionFor(decidedBy DecisionSource, outcome DecisionOutcome) PermissionDecisionResolution {
	decider := EffectiveDecider(decidedBy)
	switch decider.Kind {
	case "rule":
		if outcome.Approved {
			return "policy_allow"
		}
		return "policy_deny"
	case "session_approval":
		return "session_approved"
	case "infrastructure_read":
		return "infrastructure_auto_allowed"
	case "yolo":
		return "auto_approved"
	case "authorizer":
		if outcome.Approved {
			return "authorizer_allowed"
		}
		return "authorizer_denied"
	case "unavailable":
		return "confirmation_unavailable"
	case "gate_error":
		return "gate_error"
	}
	// "user", or a "forwarded" frame whose responder named no decider (an older parent).
	if !outcome.Approved {
		return "user_denied"
	}
	if outcome.ForSession {
		return "user_approved_for_session"
	}
	return "user_approved"
}
