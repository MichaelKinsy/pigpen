// Package twin records which upstream (pi-ahp) test cases have a Go twin.
//
// Every upstream case has a twin named after it: [Run] runs one, [Skip] declares a case
// that cannot be ported and says why. Both take the upstream file (without the
// .test.ts suffix) and the exact case title as string literals, which the
// completeness test (../../twins_test.go) reads from the source and compares with
// testdata/upstream-tests.json. A skipped twin is the visible record of a gap.
package twin

import "testing"

// Run runs fn as the subtest for the upstream case (file, title).
func Run(t *testing.T, file, title string, fn func(t *testing.T)) {
	t.Helper()
	t.Run(title, func(t *testing.T) {
		t.Helper()
		fn(t)
	})
}

// Skip declares the upstream case (file, title) as a skipped twin with a named reason.
func Skip(t *testing.T, file, title, reason string) {
	t.Helper()
	t.Run(title, func(t *testing.T) {
		t.Skip(reason)
	})
}
