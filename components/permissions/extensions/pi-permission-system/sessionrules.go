package pi_permission_system

import "sync"

// Port of src/session/session-rules.ts.

// SessionRules is the ephemeral in-memory store of session-scoped permission approvals. Each approval is a Rule with action "allow",
// so the ruleset is directly usable with Evaluate. Cleared on session shutdown, never persisted. Not safe for concurrent use, as the
// original.
type SessionRules struct{ rules Ruleset }

// Approve is approve: record a wildcard pattern as approved for the surface. A bare family surface expands to one rule per
// directional member (ADR 0013 section 4); a directional surface stays a single rule.
func (s *SessionRules) Approve(surface, pattern string) {
	targets := []string{surface}
	if members, ok := SurfaceFamilyMembers(surface); ok {
		targets = members[:]
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, target := range targets {
		s.rules = append(s.rules, Rule{Surface: target, Pattern: pattern, Action: "allow", Layer: "session", Origin: "session"})
	}
}

// GetRuleset is getRuleset: a defensive copy of the recorded rules (never nil).
func (s *SessionRules) GetRuleset() Ruleset {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append(Ruleset{}, s.rules...)
}

// RecordSessionApproval is recordSessionApproval: every grant, each on the surface it names. Implements SessionApprovalRecorder.
func (s *SessionRules) RecordSessionApproval(approval *SessionApproval) {
	for _, g := range approval.Grants {
		s.Approve(g.Surface, g.Pattern)
	}
}

// Clear is clear: remove all session approvals.
func (s *SessionRules) Clear() {
	s.mu.Lock()
	s.rules = nil
	s.mu.Unlock()
}
