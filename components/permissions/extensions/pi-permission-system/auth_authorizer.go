package pi_permission_system

// Port of the type half of src/authority/authorizer.ts (the chain-link and terminal contracts), denying-authorizer.ts and the
// narrow service.ts seams a chain link is handed (PermissionQuery, AuthorizerLog).

// AuthorizerVerdict is a non-terminal chain link's ruling on an ask: Kind "allow", "deny" (with an optional teaching Reason the
// invoking model sees) or "defer" (pass the ask to the next link).
type AuthorizerVerdict struct {
	Kind   string
	Reason *string
}

// PermissionQuery is the narrow, read-only projection of the permissions service an authorizer link is handed at authorize time
// (service.ts PermissionQuery): a policy query for a surface, and a tool-level state. It never exposes the registration surface.
type PermissionQuery interface {
	// CheckPermission queries the policy for a surface and an optional value and agent name.
	CheckPermission(surface string, value, agentName *string) PermissionCheckResult
	// GetToolPermission is a surface's catch-all permission state: "allow", "deny" or "ask".
	GetToolPermission(toolName string, agentName *string) string
}

// AuthorizerLog is the narrow review-log seam handed to a chain link (service.ts AuthorizerLog): a durable review entry and a
// debug entry gated behind the debugLog toggle. The session's own logger is passed straight through.
type AuthorizerLog = DebugReviewLogger

// AuthorizeFunc is Authorizer["authorize"]: a non-terminal link in the live-authority chain reviews an ask and may decide it or
// defer to the next link (ADR 0007). An error is the original's rejection.
type AuthorizeFunc func(details *PromptPermissionDetails, query PermissionQuery, log AuthorizerLog) (AuthorizerVerdict, error)

// NamedAuthorizer is a resolved chain link together with the operator-configured name it came from, so a decision record can say
// which link decided rather than only which links were consulted.
type NamedAuthorizer struct {
	Name      string
	Authorize AuthorizeFunc
}

// TerminalAuthorizer is the terminal link: on an ask it rules on a single request and is told the decision. It cannot defer: it
// always returns a full PermissionPromptDecision (ADR 0007's terminal-cannot-defer invariant). An error is the original's rejection.
type TerminalAuthorizer interface {
	Authorize(details *PromptPermissionDetails) (PermissionPromptDecision, error)
}

// noAuthorityReason is why DenyingAuthorizer denies: the provenance record's reason (#726).
const noAuthorityReason = "No live authority was reachable for this session"

// DenyingAuthorizer is the least-privilege authorizer: no authority is reachable for this session (no UI, not a subagent), so every
// ask is denied. The denial carries the ConfirmationUnavailable marker so the ask path can tell "nobody could answer" from an
// interactive user denial.
type DenyingAuthorizer struct{}

// Authorize implements TerminalAuthorizer.
func (DenyingAuthorizer) Authorize(*PromptPermissionDetails) (PermissionPromptDecision, error) {
	reason := noAuthorityReason
	return PermissionPromptDecision{
		Approved:                false,
		State:                   DecisionDenied,
		ConfirmationUnavailable: true,
		DecidedBy:               DecisionSource{Kind: "unavailable", Reason: &reason},
	}, nil
}
