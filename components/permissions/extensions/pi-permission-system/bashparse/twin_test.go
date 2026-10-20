package bashparse

// Twin helpers (see the module root's twin_test.go): every upstream test case gets one tw(...) call
// carrying the upstream title unchanged, or one tskip(...) with the reason it cannot be ported.
// `pigeq twins check` reads these calls from every test file under the module, subpackages included.

import "testing"

// tw runs one exact twin: file is the upstream test file (its name without .test.ts), title the
// upstream test title, unchanged.
func tw(t *testing.T, file, title string, fn func(t *testing.T)) {
	t.Helper()
	t.Run(title, fn)
}

// tskip records a named gap: the upstream case has no Go twin, with its own reason.
func tskip(t *testing.T, file, title, reason string) {
	t.Helper()
	t.Run(title, func(t *testing.T) { t.Skip(reason) })
}
