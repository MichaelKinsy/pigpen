//go:build unix

package pi_subagents_test

import "testing"

// Shutting the session down removes the async widget.
func TestSessionShutdownClearsTheAsyncWidget(t *testing.T) {
	e := newAsyncEnv(t)
	h := e.host(t)
	h.FireAll("session_start", map[string]any{"reason": "startup"})
	before := asyncWidgetCleared(h)
	h.FireAll("session_shutdown", map[string]any{"reason": "quit"})
	eq(t, asyncWidgetCleared(h), before+1, "the widget cleared by the shutdown")
}
