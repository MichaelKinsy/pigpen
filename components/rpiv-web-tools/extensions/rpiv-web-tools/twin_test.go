// SPDX-License-Identifier: MIT

package rpiv_web_tools

import "testing"

// The twin helpers of this port. Every upstream test title in port/upstream-tests.json is claimed by exactly one tw()
// or tskip() call, carrying the title unchanged.
//
//	pigeq twins list --tests port/oracle > port/upstream-tests.json
//	pigeq twins check --ledger port/upstream-tests.json --go extensions/rpiv-web-tools
func tw(t *testing.T, file, title string, fn func(t *testing.T)) {
	t.Helper()
	t.Run(title, fn)
}

// tskip records a named gap: the upstream case has no Go twin, with its own reason.
func tskip(t *testing.T, file, title, reason string) {
	t.Helper()
	t.Run(title, func(t *testing.T) { t.Skip(reason) })
}
