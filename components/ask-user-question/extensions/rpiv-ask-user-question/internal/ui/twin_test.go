package ui

// Twin helpers: every upstream test case gets one tw(...) call carrying the upstream title unchanged, or one
// tskip(...) with the reason it cannot be ported. `pigeq twins check` reads these calls. A parametrized upstream
// title (it contains `${`) is twinned once, with the template text unchanged.

import "testing"

// tw runs one exact twin: file is the upstream test file (its name without .test.ts), title the upstream test
// title, unchanged. For a nested describe the title is the one `pigeq twins list` records in port/upstream-tests.json.
func tw(t *testing.T, file, title string, fn func(t *testing.T)) {
	t.Helper()
	t.Run(title, fn)
}

// tskip records a named gap: the upstream case has no Go twin, with its own reason.
func tskip(t *testing.T, file, title, reason string) {
	t.Helper()
	t.Run(title, func(t *testing.T) { t.Skip(reason) })
}
