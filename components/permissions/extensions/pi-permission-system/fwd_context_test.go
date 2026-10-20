package pi_permission_system

import (
	"errors"
	"testing"
)

// Go-specific: forwarder-context.ts getSessionId reads the live id trimmed, and "unknown" for any session manager that cannot answer.
func TestGetSessionID(t *testing.T) {
	ctx := func(sm ForwarderSessionManager) ForwarderContext { return ForwarderContext{SessionManager: sm} }
	for name, c := range map[string]struct {
		sm   ForwarderSessionManager
		want string
	}{
		"plain":      {&fwSessionManager{sessionID: fwID("sess-1")}, "sess-1"},
		"padded":     {&fwSessionManager{sessionID: fwID("  sess-1\t")}, "sess-1"},
		"unicode":    {&fwSessionManager{sessionID: fwID("\u00a0sess-1\ufeff")}, "sess-1"},
		"blank":      {&fwSessionManager{sessionID: fwID("   ")}, "unknown"},
		"empty":      {&fwSessionManager{sessionID: fwID("")}, "unknown"},
		"error":      {&fwSessionManager{sessionID: func() (string, error) { return "sess-1", errors.New("gone") }}, "unknown"},
		"no manager": {nil, "unknown"},
	} {
		if got := GetSessionID(ctx(c.sm)); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
	if GetCwd(ForwarderContext{Cwd: "/x"}) != "/x" {
		t.Fatal("cwd")
	}
}
