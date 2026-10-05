package herdr_test

// A report herdr does not take must not stall or fail the agent, and it must not
// vanish without a trace either: a herdr that never hears about a pane is
// otherwise impossible to tell from one that was told. The reporter writes one
// line to stderr (which PiG collects as the extension's output) naming the cause.
// This came from the herdr-agent-state extension of pigpen pull request 2.

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	herdr "github.com/MichaelKinsy/pigpen/components/herdr/extensions/herdr"
)

// captureStderr swaps os.Stderr for a pipe and returns what was written so far.
func captureStderr(t *testing.T) func() string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = orig })
	return func() string {
		settle()
		_ = w.SetDeadline(time.Now())
		os.Stderr = orig
		_ = w.Close()
		b, _ := io.ReadAll(r)
		return string(b)
	}
}

func TestRefusedReportsAreExplained(t *testing.T) {
	t.Run("a refused report names the cause", func(t *testing.T) {
		host, h := startTUI(t)
		h.failWithMessage(1, "error: pane p_9 not found")
		read := captureStderr(t)
		host.fire("session_start", map[string]any{"reason": "startup"})
		h.waitForCalls(1)
		got := read()
		if !strings.Contains(got, "herdr: pane report-agent failed") || !strings.Contains(got, "pane p_9 not found") {
			t.Fatalf("stderr = %q", got)
		}
	})

	t.Run("a herdr that keeps refusing is reported once, not once per report", func(t *testing.T) {
		host, h := startTUI(t)
		h.failWithMessage(1, "error: failed to connect to herdr socket")
		read := captureStderr(t)
		host.fire("session_start", map[string]any{"reason": "startup"})
		for range 3 {
			host.fire("agent_start", nil)
			host.fire("agent_settled", nil)
		}
		h.waitForCalls(2)
		settle()
		if n := strings.Count(read(), "failed to connect to herdr socket"); n != 1 {
			t.Fatalf("the same failure was written %d times", n)
		}
	})

	t.Run("a herdr binary that is gone is reported", func(t *testing.T) {
		installFakeHerdr(t)
		missing := filepath.Join(t.TempDir(), "missing")
		t.Setenv("HERDR_BIN_PATH", missing)
		host := startHost(t, herdr.Extension(), "tui")
		read := captureStderr(t)
		host.fire("session_start", map[string]any{"reason": "startup"})
		if got := read(); !strings.Contains(got, "herdr: pane report-agent failed") || !strings.Contains(got, "missing") {
			t.Fatalf("stderr = %q", got)
		}
	})

	t.Run("a healthy herdr and an old herdr that refuses only the resume command stay silent", func(t *testing.T) {
		host, h := startTUI(t)
		h.refuseResume("old")
		read := captureStderr(t)
		host.fire("session_start", map[string]any{"reason": "startup"})
		host.fire("agent_start", nil)
		h.waitForCalls(2)
		if got := read(); got != "" {
			t.Fatalf("stderr = %q, want nothing", got)
		}
	})
}
