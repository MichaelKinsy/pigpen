package pi_permission_system

// Port of src/session/approval-grant.ts.

// ApprovalGrant is one session-approval grant: a wildcard pattern approved on one surface. A gate proves a direction per accessed
// path (ADR 0013 section 7), so an ask whose paths disagree records a surface per pattern rather than one for all of them (#810).
type ApprovalGrant struct{ Surface, Pattern string }

// SessionGrantWidth is how wide a session grant is recorded, relative to what the gate proved: "proven" records each grant on the
// surface the gate named (the least-privilege default), "family" folds a directional surface to its bare family, which
// SessionRules.Approve sugar-expands onto both members (ADR 0013 section 4).
type SessionGrantWidth string

// IsSessionGrantWidth is isSessionGrantWidth: whether an off-disk value names a width this node understands. An unrecognized value
// is dropped rather than trusted, which lands the grant at "proven", the least-privilege width.
func IsSessionGrantWidth(v any) bool { return v == "proven" || v == "family" }

// WidenGrant is widenGrant: the same grant on its bare family surface, or the grant itself when it already names one (or names no
// directional family at all). Widening changes the surface, never the pattern.
func WidenGrant(g ApprovalGrant) ApprovalGrant {
	if family := SurfaceFamilyOf(g.Surface); family != g.Surface {
		return ApprovalGrant{Surface: family, Pattern: g.Pattern}
	}
	return g
}

// ProvenDirectionOf is provenDirectionOf: the one direction every grant proves, ok false when they disagree, any of them proves
// none, or there are no grants.
func ProvenDirectionOf(grants []ApprovalGrant) (CapabilityDirection, bool) {
	if len(grants) == 0 {
		return "", false
	}
	direction, ok := CapabilityDirectionOf(grants[0].Surface)
	if !ok {
		return "", false
	}
	for _, g := range grants {
		if d, ok := CapabilityDirectionOf(g.Surface); !ok || d != direction {
			return "", false
		}
	}
	return direction, true
}

// contentsSuffixes are a separator (either alphabet) followed by the wildcard.
var contentsSuffixes = []string{"/*", "\\*"}

// GrantTargets is grantTargets: the distinct targets an approval covers, for naming it in a prompt. A directory's exact grant folds
// into its contents grant, and identical patterns collapse to one.
func GrantTargets(grants []ApprovalGrant) []string {
	targets := []string{}
	seen := map[string]bool{}
	for _, g := range grants {
		if hasContentsSibling(g, grants) || seen[g.Pattern] {
			continue
		}
		seen[g.Pattern] = true
		targets = append(targets, g.Pattern)
	}
	return targets
}

// hasContentsSibling reports whether grants also holds g's contents pattern on the same surface.
func hasContentsSibling(g ApprovalGrant, grants []ApprovalGrant) bool {
	for _, other := range grants {
		if other.Surface != g.Surface {
			continue
		}
		for _, suffix := range contentsSuffixes {
			if other.Pattern == g.Pattern+suffix {
				return true
			}
		}
	}
	return false
}
