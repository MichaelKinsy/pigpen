package pi_goal_x_test
import "testing"


// tw and tskip for the external test package (the goalRig twins); the same contract as twin_test.go.

// tw runs one exact twin: file is the upstream test file (its name without .test.mjs), title
// the upstream test title, unchanged.
func tw(t *testing.T, file, title string, fn func(t *testing.T)) {
	t.Helper()
	t.Run(title, fn)
}

// tskip records a named gap: the upstream case has no Go twin, with its own reason.
func tskip(t *testing.T, file, title, reason string) {
	t.Helper()
	t.Run(title, func(t *testing.T) { t.Skip(reason) })
}
