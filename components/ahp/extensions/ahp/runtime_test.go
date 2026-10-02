package ahp

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/ahp/internal/pi"
)

func TestLoopbackHosts(t *testing.T) {
	for host, want := range map[string]bool{
		"127.0.0.1": true, "::1": true, "localhost": true, "127.1.2.3": true,
		"0.0.0.0": false, "::": false, "192.168.1.5": false, "example.com": false, "": false,
	} {
		if got := isLoopback(host); got != want {
			t.Errorf("isLoopback(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestRemoteHostNeedsAToken(t *testing.T) {
	if err := requireTokenOffLoopback("s.json", "0.0.0.0", ""); err == nil || !strings.Contains(err.Error(), "token is required") {
		t.Fatalf("a public host without a token was accepted: %v", err)
	}
	for _, c := range []struct{ host, token string }{{"0.0.0.0", "secret"}, {"127.0.0.1", ""}, {"localhost", ""}} {
		if err := requireTokenOffLoopback("s.json", c.host, c.token); err != nil {
			t.Errorf("%+v refused: %v", c, err)
		}
	}
}

func TestSessionDeletionIsGuarded(t *testing.T) {
	var removed []string
	remove := func(p string) (pi.SessionFileDeletionResult, error) {
		removed = append(removed, p)
		return pi.SessionFileDeletionResult{OK: true}, nil
	}
	live := func() string { return "/sessions/./live.jsonl" }

	off := guardedDelete(false, live, remove)
	if r, _ := off("/sessions/other.jsonl"); r.OK || r.Error == "" {
		t.Fatalf("deletion is off by default, got %+v", r)
	}
	on := guardedDelete(true, live, remove)
	if r, _ := on("/sessions/live.jsonl"); r.OK || !strings.Contains(r.Error, "running") {
		t.Fatalf("the running session's file must never be deleted, got %+v", r)
	}
	if len(removed) != 0 {
		t.Fatalf("removed %v before any allowed deletion", removed)
	}
	if r, _ := on("/sessions/other.jsonl"); !r.OK {
		t.Fatalf("an allowed deletion failed: %+v", r)
	}
	if len(removed) != 1 || removed[0] != "/sessions/other.jsonl" {
		t.Fatalf("removed = %v", removed)
	}
	if r, _ := guardedDelete(true, func() string { return "" }, remove)("/sessions/x.jsonl"); !r.OK {
		t.Fatalf("with no live file everything else may go: %+v", r)
	}
}
