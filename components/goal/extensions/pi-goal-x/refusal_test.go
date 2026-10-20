package pi_goal_x_test

import (
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
