//go:build windows

package tintinweb_subagents

import "testing"

// The chains of fake pigs (nested_process_test.go) need the process groups and signals of a Unix host.
func testSpendReachesTheTopThroughTheChain(t *testing.T) {
	t.Helper()
	t.Skip("a chain of fake pig processes needs the process groups and signals of a Unix host")
}
