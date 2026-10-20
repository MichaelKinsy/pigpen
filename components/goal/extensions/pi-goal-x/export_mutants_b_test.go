package pi_goal_x

// Probe hooks for mutants_b_test.go (lane B of the mutation survivors): reads and fakes the original's tests do through the
// extension's internals.

import "context"

// RigBlockerFingerprint is buildBlockerFingerprint(focused goal, reason).
func (p *Probe) RigBlockerFingerprint(reason string) string {
	var fp string
	p.c.locked(func() { fp = buildBlockerFingerprint(p.c.state(), reason) })
	return fp
}

// RigOraclePending is hasPendingOracleAdviceForFocusedGoal(goalID).
func RigOraclePending(goalID string) bool { return hasPendingOracleAdviceForFocusedGoal(goalID) }

// RigDisarmOracle drops every armed advice (the marker is process-global).
func RigDisarmOracle() { armedByGoalID = map[string]armedAdvice{} }

// SetOracleSession makes the blocker Oracle's nested session a fake: run gets the prompt and the submit tool (the advice as JSON).
func (p *Probe) SetOracleSession(run func(prompt string, submit func(adviceJSON string)) error) {
	p.c.deps.createSession = auditorSessionFactoryFunc(func(o auditorSessionOptions) (auditorSession, error) {
		var submitTool *goalOracleTool
		for _, tl := range o.customTools {
			if tl.name == "submit_goal_oracle_advice" {
				submitTool = tl
			}
		}
		return &fakeAuditorSession{onPrompt: func(prompt string) error {
			return run(prompt, func(adviceJSON string) {
				v, err := parseJSON([]byte(adviceJSON))
				if err != nil {
					panic(err)
				}
				submitTool.execute("call-1", v.(*jsObject))
			})
		}}, nil
	})
}

// RigAuditProgress is the auditor widget's progress now: its elapsed time and phase (ok=false when none).
func (p *Probe) RigAuditProgress() (elapsedMs float64, phase string, ok bool) {
	p.c.locked(func() {
		if ap := p.c.auditProgress; ap != nil {
			elapsedMs, phase, ok = ap.elapsedMs, ap.phase, true
		}
	})
	return
}

// RigAuditAborted is core.auditAborted.
func (p *Probe) RigAuditAborted() bool {
	var v bool
	p.c.locked(func() { v = p.c.auditAborted })
	return v
}

// RigUnfocusInsideRun drops the focus without taking the lock: for a fake Oracle session, which runs inside the tool call's handler
// with the core locked (the Oracle wait does not release it).
func (p *Probe) RigUnfocusInsideRun() { p.c.assignFocused(nil) }

var _ = context.Background
