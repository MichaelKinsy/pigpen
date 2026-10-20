package pi_permission_system

// Port of src/handlers/gates/external-directory-policy.ts: the external_directory resolve the two external-directory gates share.

// UncoveredExternalPath is an external path whose resolved `external_directory` state is not "allow".
type UncoveredExternalPath struct {
	Path *AccessPath
	// Surface is the family member the path's own effect named, and the check answered.
	Surface string
	// Effect is what the effect was, and what established it: the review log's blame.
	Effect TokenEffect
	Check  PermissionCheckResult
}

// ExternalPathCoverage is how a set of external paths resolved: the uncovered ones, the worst among them, and which allowed paths a
// session grant covered.
type ExternalPathCoverage struct {
	Uncovered []UncoveredExternalPath
	// SessionCovered are allowed paths whose winning rule was a session grant. A path allowed by a config rule or the universal
	// fallback is in neither list.
	SessionCovered []*AccessPath
	// WorstCheck is the worst check among uncovered paths; nil only when none are uncovered.
	WorstCheck *PermissionCheckResult
}

// ResolveExternalDirectoryPolicy resolves one external path's policy on an `external_directory`-family surface: surface is the
// narrowest family member the caller can prove, the bare family name when it can prove nothing narrower (the resolver folds it over
// both directions, ADR 0013 section 10). It emits an `access-path` intent so a config pattern on either the typed or the
// symlink-resolved alias applies (#418).
func ResolveExternalDirectoryPolicy(path *AccessPath, resolver ScopedPermissionResolver, surface string, agentName *string) PermissionCheckResult {
	return resolver.Resolve(AccessPathAccessIntent{Surface: surface, Path: path, AgentName: agentName})
}

// SelectUncoveredExternalPaths resolves a set of external accesses and selects those not already allowed. Each access resolves on
// the narrowest family member its own effect names. Entries whose state is not "allow" are collected (filtering on state, not
// source, so a config-level allow suppresses the prompt just as a session-level one does), and the most restrictive uncovered check
// is returned so a config "deny" is not downgraded to the catch-all "ask". Allowed entries a session rule decided are reported apart.
func SelectUncoveredExternalPaths(accesses []BashExternalPath, resolver ScopedPermissionResolver, agentName *string) ExternalPathCoverage {
	coverage := ExternalPathCoverage{Uncovered: []UncoveredExternalPath{}, SessionCovered: []*AccessPath{}}
	for _, access := range accesses {
		surface := CapabilitySurfaceForEffect("external_directory", access.Effect.Effect)
		check := ResolveExternalDirectoryPolicy(access.Path, resolver, surface, agentName)
		if check.State != "allow" {
			coverage.Uncovered = append(coverage.Uncovered, UncoveredExternalPath{Path: access.Path, Surface: surface, Effect: access.Effect, Check: check})
		} else if check.Source == "session" {
			coverage.SessionCovered = append(coverage.SessionCovered, access.Path)
		}
	}
	if len(coverage.Uncovered) > 0 {
		worst := mostRestrictiveOf(coverage.Uncovered, func(u UncoveredExternalPath) string { return u.Check.State }).Check
		coverage.WorstCheck = &worst
	}
	return coverage
}
