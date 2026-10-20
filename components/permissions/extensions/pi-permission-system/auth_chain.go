package pi_permission_system

// Port of src/authority/authorizer-chain.ts.

// chainAuthorizer is the composed chain: links in order, then the terminal.
type chainAuthorizer struct {
	links    []NamedAuthorizer
	terminal TerminalAuthorizer
	query    PermissionQuery
	log      AuthorizerLog
}

// ComposeAuthorizerChain composes the live-authority chain (ADR 0007): try each non-terminal link in order, and on defer fall
// through to the next link, ending at the context-selected terminal that always decides. Each link is handed the session-scoped
// query and the review-log log at authorize time; the terminal receives neither. With zero links the composed chain IS the terminal
// instance (identity), so behaviour is byte-identical to a bare terminal escalation.
func ComposeAuthorizerChain(links []NamedAuthorizer, terminal TerminalAuthorizer, query PermissionQuery, log AuthorizerLog) TerminalAuthorizer {
	if len(links) == 0 {
		return terminal
	}
	return &chainAuthorizer{links: links, terminal: terminal, query: query, log: log}
}

// Authorize implements TerminalAuthorizer. A link's error is the original's rejection and decides nothing: it is returned, never
// turned into an allow, and never falls through to the next link.
func (c *chainAuthorizer) Authorize(details *PromptPermissionDetails) (PermissionPromptDecision, error) {
	for _, link := range c.links {
		verdict, err := link.Authorize(details, c.query, c.log)
		if err != nil {
			return PermissionPromptDecision{}, err
		}
		if decision, decided := decideFromVerdict(link.Name, verdict); decided {
			return decision, nil
		}
		// defer: try the next link.
	}
	return c.terminal.Authorize(details)
}

// decideFromVerdict maps a link's decisive verdict to a decision; defer is not decisive. The deciding link is named on the decision:
// a link ahead of it that deferred decided nothing and must not be credited (#726). Any verdict other than allow or deny defers
// (the original's switch is exhaustive over a closed union, so nothing else exists there).
func decideFromVerdict(name string, verdict AuthorizerVerdict) (PermissionPromptDecision, bool) {
	switch verdict.Kind {
	case "allow":
		// A link grant is non-persistent (state approved, never approved_for_session), per ADR 0007's off-by-default envelope.
		return PermissionPromptDecision{Approved: true, State: DecisionApproved, DecidedBy: decidedByLink(name, "allow", nil)}, true
	case "deny":
		denied := CreateDeniedPermissionDecision(verdict.Reason).Attribute(decidedByLink(name, "deny", verdict.Reason))
		return denied, true
	}
	return PermissionPromptDecision{}, false
}

func decidedByLink(name, verdict string, reason *string) DecisionSource {
	return DecisionSource{Kind: "authorizer", Name: name, Verdict: verdict, Reason: reason}
}
