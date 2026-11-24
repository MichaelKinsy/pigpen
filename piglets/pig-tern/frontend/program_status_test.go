package frontend

import (
	"strings"
	"testing"
)

// PiG's program status reaches Tern as PiG writes it: each report once, in
// order, as its own write outside any TSP message. After a fallback PiG's
// own renderer reports, and after Close nothing is written.
func TestProgramStatusReachesTheTerminal(t *testing.T) {
	h := ready(t)
	h.out.take()
	working := "\x1b]7501;state=working:app=pig\x1b\\"
	blocked := "\x1b]7501;state=blocked:app=pig:kind=permission:msg=UnVuIGJhc2g/\x1b\\"
	h.session.ProgramStatus(working)
	h.session.ProgramStatus(blocked)
	if out := h.out.take(); out != working+blocked {
		t.Fatalf("reports = %q, want %q", out, working+blocked)
	}

	h.session.fallback("test")
	h.session.ProgramStatus(working)
	if out := h.out.take(); strings.Contains(out, "7501") {
		t.Fatalf("written after the fallback: %q", out)
	}

	h2 := ready(t)
	if err := h2.session.Close(); err != nil {
		t.Fatal(err)
	}
	h2.out.take()
	h2.session.ProgramStatus(working)
	if out := h2.out.take(); out != "" {
		t.Fatalf("written after Close: %q", out)
	}
}
