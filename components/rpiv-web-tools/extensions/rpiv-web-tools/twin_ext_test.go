package rpiv_web_tools_test

// Twin helpers for a port whose original has tests. Every upstream test case gets one
// tw(...) call carrying the upstream title unchanged, or one tskip(...) with the reason it
// cannot be ported. `pigeq twins check` reads these calls and fails on a missing title, an
// unknown one (a misspelling) or one reason shared by many skips.
//
//	pigeq twins list --tests <upstream>/test > port/upstream-tests.json
//	pigeq twins check --ledger port/upstream-tests.json --go extensions/<name> [--files a,b]
//
// Copy this file into the port as twin_test.go and replace tintinweb_tasks with the package name (in
// an external test package use `tintinweb_tasks_test`). A parametrized upstream title (it contains
// `${`) is twinned once, with the template text unchanged.

import "testing"

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
