package pi_permission_system

// Port of src/authority/delegation-envelope.ts: the bounded-delegation enforcement checkpoint (ADR 0007 section 5).
//
// The chain owner caps every registered link's verdict so a buggy or over-eager external judge can never exceed the operator's
// policy: a link's allow on an excluded surface is downgraded to defer, letting the ask fall through to the terminal (a prompt). The
// checkpoint only ever tightens a verdict: it never turns a defer or deny into an allow.

// DelegationExcludedSurfaces are the surface families on which a link may never grant an allow (ADR 0007 section 5).
var DelegationExcludedSurfaces = map[string]bool{"external_directory": true, "path": true}

// EncloseInDelegationEnvelope wraps a link's authorize so an allow on an excluded surface is capped to defer. Every other verdict,
// and allows on non-excluded surfaces, pass through unchanged; details, the injected query, and the review-log seam are forwarded
// as-is. A link's error passes through as it is.
func EncloseInDelegationEnvelope(authorize AuthorizeFunc) AuthorizeFunc {
	return func(details *PromptPermissionDetails, query PermissionQuery, log AuthorizerLog) (AuthorizerVerdict, error) {
		verdict, err := authorize(details, query, log)
		if err != nil {
			return verdict, err
		}
		if verdict.Kind == "allow" && isExcludedSurface(details) {
			return AuthorizerVerdict{Kind: "defer"}, nil
		}
		return verdict, nil
	}
}

// isExcludedSurface reads the gate-authoritative accessIntent.surface, falling back to the display surface. Fail-safe: an ask whose
// surface cannot be determined is treated as excluded (more prompting, never less: ADR 0007 invariant 2).
func isExcludedSurface(details *PromptPermissionDetails) bool {
	switch {
	case details.AccessIntent != nil:
		return DelegationExcludedSurfaces[SurfaceFamilyOf(details.AccessIntent.Surface)]
	case details.Surface != nil:
		return DelegationExcludedSurfaces[SurfaceFamilyOf(*details.Surface)]
	}
	return true
}
