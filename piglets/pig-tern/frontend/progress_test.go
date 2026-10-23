package frontend

import (
	"strings"
	"testing"
	"time"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
)

// Tern reads an agent's working state from OSC 9;4 progress, so the member
// reports progress exactly while a Working node shows: the active sequence
// when one appears, again every keepalive tick, and the clear sequence when
// it goes, after which no tick writes. An update writes no progress, and
// closing the session while working clears it. Each sequence is one write
// of its own, never inside a TSP message.
func TestWorkingReportsTerminalProgress(t *testing.T) {
	h := ready(t)
	h.session.keepalive = 5 * time.Millisecond
	working := fe.Working{Kind: fe.WorkingAgent, Message: "Working"}
	h.apply(workingOp(fe.Insert, working))
	time.Sleep(40 * time.Millisecond)
	working.Message = "Reading files"
	h.apply(workingOp(fe.Update, working))
	h.apply(fe.Op{Kind: fe.Remove, Region: fe.RegionDock, ID: "working"})
	out := h.out.take()
	if n := strings.Count(out, progressActive); n < 2 {
		t.Fatalf("active sequences while working = %d, want the first and keepalive ticks", n)
	}
	if !strings.HasPrefix(out, progressActive) || strings.Count(out, progressClear) != 1 {
		t.Fatalf("progress = %q, want active first and one clear", out)
	}
	if last := strings.LastIndex(out, progressActive); last > strings.Index(out, progressClear) {
		t.Fatalf("an active sequence followed the clear: %q", out)
	}
	for _, part := range strings.Split(out, "\x1b_tsp;")[1:] {
		if body, _, _ := strings.Cut(part, "\x1b\\"); strings.Contains(body, "\x1b]9;4") {
			t.Fatalf("progress inside a TSP message: %q", part)
		}
	}
	time.Sleep(20 * time.Millisecond)
	if out := h.out.peek(); out != "" {
		t.Fatalf("written after the indicator went: %q", out)
	}

	h.apply(workingOp(fe.Insert, working))
	if err := h.session.Close(); err != nil {
		t.Fatal(err)
	}
	if out := h.out.peek(); !strings.Contains(out, progressClear) {
		t.Fatalf("close while working = %q, want the clear sequence", out)
	}
}
