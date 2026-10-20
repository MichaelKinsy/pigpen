package pi_goal_x_test

import (
	"strings"
	"testing"
)

// What the port does not do yet is refused with one message, never ignored (port/PORT.md, "Refusals").

func want(feature string) string {
	return "error: @pi-in-go/pigpen-goal (Go port) does not support " + feature + " yet. Install the original extension, npm:pi-goal-x@0.32.3, which runs unchanged in PiG."
}

func lastNotice(t *testing.T, h *Host) string {
	t.Helper()
	n := notices(h)
	if len(n) == 0 {
		t.Fatal("no notification")
	}
	return n[len(n)-1]
}

func TestRefusesAnAutomaticRunAtSessionStart(t *testing.T) {
	a, at := seedGoal("seed-a", "Left by the original")
	at = strings.Replace(at, `"autoContinue": true,`, `"autoContinue": true, "scheduler": {"version": 1, "owner": "`+sessionID+`", "generation": "g1", "used": 0, "phase": "ready", "repairUsed": false, "decision": {"kind": "ready", "purpose": "kickoff"}},`, 1)
	h, _ := rig{settings: `{}`, branch: []any{focusEntry("seed-a")}, seed: map[string]string{a: at}}.start(t)
	// The session-start scheduler restore found a goal that the original would run: it is reported, not run.
	found := false
	for _, n := range notices(h) {
		if n == want("automatic goal continuation (maxAutonomousRuns other than 0)") {
			found = true
		}
	}
	if !found {
		t.Errorf("notices %q", notices(h))
	}
}
