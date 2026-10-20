package pi_goal_x_test

// The twin helpers of twin_test.go for the external test package (the rig's twins).

import "testing"

func tw(t *testing.T, file, title string, fn func(t *testing.T)) {
	t.Helper()
	t.Run(title, fn)
}

func tskip(t *testing.T, file, title, reason string) {
	t.Helper()
	t.Run(title, func(t *testing.T) { t.Skip(reason) })
}
