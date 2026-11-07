//go:build windows

package posthorse

import "testing"

// runNoteWriteFailure: the upstream case is skipped on win32 (posthorse.test.ts:3338).
func runNoteWriteFailure(t *testing.T) {
	t.Skip("ulimit -f is not available on Windows; the upstream case is skipped on win32")
}
