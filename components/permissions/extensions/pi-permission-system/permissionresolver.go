package pi_permission_system

// Port of src/policy/permission-resolver.ts.

// ScopedPermissionResolver answers an AccessIntent a gate emits, applying the current session rules internally. A single Resolve entry
// point means adding a gate cannot widen the resolver surface (#478).
type ScopedPermissionResolver interface {
	Resolve(intent AccessIntent) PermissionCheckResult
}

// SessionRulesReader is the part of SessionRules the resolver reads (it only reads, never records).
type SessionRulesReader interface {
	GetRuleset() Ruleset
}

// PermissionResolver owns the resolution surface: it holds a ScopedPermissionManager and a session rules reader, so callers never
// thread the session ruleset by hand. It implements ScopedPermissionResolver and the skill permission checker.
type PermissionResolver struct {
	permissionManager ScopedPermissionManager
	sessionRules      SessionRulesReader
}

// NewPermissionResolver is the constructor.
func NewPermissionResolver(permissionManager ScopedPermissionManager, sessionRules SessionRulesReader) *PermissionResolver {
	return &PermissionResolver{permissionManager: permissionManager, sessionRules: sessionRules}
}

// toResolvedIntent reduces a gate-emitted AccessIntent to the string-based ResolvedAccessIntent the manager consumes. This is the sole
// MatchValues unwrap site: the single place the lexical and canonical alias set (#418) is derived (ADR 0002).
func toResolvedIntent(intent AccessIntent) ResolvedAccessIntent {
	switch it := intent.(type) {
	case AccessPathAccessIntent:
		return PathValuesAccessIntent{Surface: it.Surface, Values: it.Path.MatchValues(), AgentName: it.AgentName}
	case ToolAccessIntent:
		return it
	case BashCommandAccessIntent:
		return it
	}
	panic("permission resolver: unknown access intent")
}

// intentWithSurface is `{ ...resolved, surface }`.
func intentWithSurface(intent ResolvedAccessIntent, surface string) ResolvedAccessIntent {
	switch it := intent.(type) {
	case ToolAccessIntent:
		it.Surface = surface
		return it
	case PathValuesAccessIntent:
		it.Surface = surface
		return it
	case BashCommandAccessIntent:
		it.Surface = surface
		return it
	}
	panic("permission resolver: unknown access intent")
}

func intentSurface(intent ResolvedAccessIntent) string {
	switch it := intent.(type) {
	case ToolAccessIntent:
		return it.Surface
	case PathValuesAccessIntent:
		return it.Surface
	case BashCommandAccessIntent:
		return it.Surface
	}
	return ""
}

// Resolve answers a gate-emitted access intent, composing the current session ruleset. An intent naming a bare surface family (`path`,
// `external_directory`) is folded over the family's directional members, most-restrictive (ADR 0013 section 10's fail-closed base
// case). The fold lives here because this is the one entry point the gates, the service and the serving policy all share (#712).
func (r *PermissionResolver) Resolve(intent AccessIntent) PermissionCheckResult {
	return r.resolveResolved(toResolvedIntent(intent))
}

// ResolvePrefixed answers an already-resolved path-values intent (the forwarded-serving wire's producer, #597): a passthrough, since
// there is no AccessPath to unwrap. The gate-facing ScopedPermissionResolver stays narrow (AccessIntent only); this wider acceptance
// is on the concrete resolver only.
func (r *PermissionResolver) ResolvePrefixed(intent PathValuesAccessIntent) PermissionCheckResult {
	return r.resolveResolved(intent)
}

func (r *PermissionResolver) resolveResolved(resolved ResolvedAccessIntent) PermissionCheckResult {
	sessionRuleset := r.sessionRules.GetRuleset()
	members, ok := SurfaceFamilyMembers(intentSurface(resolved))
	if !ok {
		return r.permissionManager.Check(resolved, sessionRuleset)
	}
	results := make([]PermissionCheckResult, len(members))
	for i, surface := range members {
		results[i] = r.permissionManager.Check(intentWithSurface(resolved, surface), sessionRuleset)
	}
	return mostRestrictiveOf(results, func(c PermissionCheckResult) string { return c.State })
}

// CheckPermission is the raw permission check without session rules (the no-session-rules path consumed by the skill input gate).
// sessionRules nil is undefined.
func (r *PermissionResolver) CheckPermission(surface string, input any, agentName *string, sessionRules Ruleset) PermissionCheckResult {
	return r.permissionManager.Check(ToolAccessIntent{Surface: surface, Input: input, AgentName: agentName}, sessionRules)
}

// GetToolPermission is the published cross-extension catch-all query.
func (r *PermissionResolver) GetToolPermission(toolName string, agentName *string) string {
	return r.permissionManager.GetToolPermission(toolName, agentName)
}

// IsToolFullyDenied delegates to the manager.
func (r *PermissionResolver) IsToolFullyDenied(toolName string, agentName *string) bool {
	return r.permissionManager.IsToolFullyDenied(toolName, agentName)
}

// GetConfigIssues delegates to the manager.
func (r *PermissionResolver) GetConfigIssues(agentName *string) []string {
	return r.permissionManager.GetConfigIssues(agentName)
}
