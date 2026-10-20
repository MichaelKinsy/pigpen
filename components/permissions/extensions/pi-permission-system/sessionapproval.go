package pi_permission_system

// Port of src/session/session-approval.ts and session-approval-recorder.ts.

// SessionApproval is the value object for a session-scoped approval: one or more grants, each pairing a pattern with the surface it
// was proven on. Treat Grants as read-only.
type SessionApproval struct{ Grants []ApprovalGrant }

// SessionApprovalSingle is SessionApproval.single: an approval for a single pattern.
func SessionApprovalSingle(surface, pattern string) *SessionApproval {
	return &SessionApproval{Grants: []ApprovalGrant{{Surface: surface, Pattern: pattern}}}
}

// SessionApprovalForPatterns is SessionApproval.forPatterns: patterns all proven on one surface.
func SessionApprovalForPatterns(surface string, patterns []string) *SessionApproval {
	grants := make([]ApprovalGrant, len(patterns))
	for i, p := range patterns {
		grants[i] = ApprovalGrant{Surface: surface, Pattern: p}
	}
	return &SessionApproval{Grants: grants}
}

// SessionApprovalForGrants is SessionApproval.forGrants: grants that may name different surfaces; a defensive copy.
func SessionApprovalForGrants(grants []ApprovalGrant) *SessionApproval {
	return &SessionApproval{Grants: append([]ApprovalGrant{}, grants...)}
}

// IsRecordable is isRecordable: whether the approval carries anything for the session store to record.
func (a *SessionApproval) IsRecordable() bool { return len(a.Grants) > 0 }

// AtWidth is atWidth: "proven" is the approval itself, any other width folds each grant individually to its family.
func (a *SessionApproval) AtWidth(w SessionGrantWidth) *SessionApproval {
	if w == "proven" {
		return a
	}
	out := make([]ApprovalGrant, len(a.Grants))
	for i, g := range a.Grants {
		out[i] = WidenGrant(g)
	}
	return &SessionApproval{Grants: out}
}

// ToForwardedData is toForwardedData: the plain data shape for relaying the approval on a forwarded request; a defensive copy.
func (a *SessionApproval) ToForwardedData() ForwardedSessionApproval {
	return ForwardedSessionApproval{Grants: append([]ApprovalGrant{}, a.Grants...)}
}

// SessionApprovalRecorder records a granted session-scoped approval into the session ruleset.
type SessionApprovalRecorder interface {
	RecordSessionApproval(approval *SessionApproval)
}
