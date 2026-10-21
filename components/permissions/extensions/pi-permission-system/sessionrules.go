package pi_permission_system

import (
	"slices"
	"sync"
)

// Port of src/session/session-rules.ts.

// SessionRules is the ephemeral in-memory store of session-scoped permission approvals. Each approval is a Rule with action "allow",
// so the ruleset is directly usable with Evaluate. Cleared on session shutdown, never persisted. Unlike the original's single-threaded
// one it is safe for concurrent use: the approval path writes it while the resolver reads it.
type SessionRules struct {
	mu    sync.Mutex
	rules Ruleset
}

// Approve is approve: record a wildcard pattern as approved for the surface. A bare family surface expands to one rule per
// directional member (ADR 0013 section 4); a directional surface stays a single rule.
func (s *SessionRules) Approve(surface, pattern string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.approveLocked(surface, pattern)
}

// approveLocked appends the rules of one approval; the caller holds mu. The family members are ranged over as the array they are,
// so the expansion allocates nothing beyond the member names.
func (s *SessionRules) approveLocked(surface, pattern string) {
	if members, ok := SurfaceFamilyMembers(surface); ok {
		for _, target := range members {
			s.rules = append(s.rules, sessionAllowRule(target, pattern))
		}
		return
	}
	s.rules = append(s.rules, sessionAllowRule(surface, pattern))
}

func sessionAllowRule(surface, pattern string) Rule {
	return Rule{Surface: surface, Pattern: pattern, Action: "allow", Layer: "session", Origin: "session"}
}

// GetRuleset is getRuleset: a defensive copy of the recorded rules (never nil).
func (s *SessionRules) GetRuleset() Ruleset {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append(Ruleset{}, s.rules...)
}

// RecordSessionApproval is recordSessionApproval: every grant, each on the surface it names. Implements SessionApprovalRecorder.
func (s *SessionRules) RecordSessionApproval(approval *SessionApproval) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// one growth for the whole approval (a family grant is two rules), and one lock: the approval lands as a unit
	s.rules = slices.Grow(s.rules, 2*len(approval.Grants))
	for _, g := range approval.Grants {
		s.approveLocked(g.Surface, g.Pattern)
	}
}

// Clear is clear: remove all session approvals.
func (s *SessionRules) Clear() {
	s.mu.Lock()
	s.rules = nil
	s.mu.Unlock()
}
