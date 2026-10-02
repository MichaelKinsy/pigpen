package dirty_repo_guard_test

import (
	"encoding/json"
	"testing"

	guard "github.com/MichaelKinsy/pigpen/dirty-repo-guard"
)

// Layer-1 cases, one per branch of Pi's dirty-repo-guard.ts, through the fake
// host. The live Pi-versus-PiG traces (port/scenarios) are the equivalence proof;
// these cases add what a RPC-driven scenario cannot reach (no UI) and pin the
// wire shape of each host call.

const (
	yes = "Yes, proceed anyway"
	no  = "No, let me commit first"
)

type answers struct {
	stdout   string
	code     float64
	selected string
	ok       bool
}

func (a answers) onCall(method string, _ map[string]any) (map[string]any, string) {
	switch method {
	case "exec":
		return map[string]any{"stdout": a.stdout, "stderr": "", "code": a.code, "killed": false}, ""
	case "ui.select":
		return map[string]any{"selected": a.selected, "ok": a.ok}, ""
	}
	return nil, ""
}

func cancelled(t *testing.T, raw json.RawMessage) bool {
	t.Helper()
	if len(raw) == 0 || string(raw) == "null" {
		return false
	}
	var r struct {
		Cancel bool `json:"cancel"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("result %s: %v", raw, err)
	}
	return r.Cancel
}

func titleOf(t *testing.T, h *Host) string {
	t.Helper()
	calls := h.CallsTo("ui.select")
	if len(calls) != 1 {
		t.Fatalf("want 1 select, got %d", len(calls))
	}
	title, _ := calls[0].Args["title"].(string)
	return title
}

func TestRegistersOnlyTheTwoBeforeEvents(t *testing.T) {
	h := StartHost(t, guard.Extension(), HostOptions{})
	for _, e := range []string{"session_before_switch", "session_before_fork"} {
		if !h.Registered(e) {
			t.Errorf("no handler for %s", e)
		}
	}
	if h.Registered("session_start") || h.Registered("session_shutdown") {
		t.Error("registered an event the original does not handle")
	}
}

func TestRunsGitStatusPorcelain(t *testing.T) {
	h := StartHost(t, guard.Extension(), HostOptions{OnCall: answers{code: 0}.onCall})
	h.Fire("session_before_switch", map[string]any{"reason": "new"})
	calls := h.CallsTo("exec")
	if len(calls) != 1 || calls[0].Args["command"] != "git" {
		t.Fatalf("exec calls = %+v", calls)
	}
	args, _ := calls[0].Args["args"].([]any)
	if len(args) != 2 || args[0] != "status" || args[1] != "--porcelain" {
		t.Fatalf("git args = %v", args)
	}
}

func TestDirtyWithUIAsksAndDeclineCancels(t *testing.T) {
	h := StartHost(t, guard.Extension(), HostOptions{OnCall: answers{stdout: " M a\n?? b\n", selected: no, ok: true}.onCall})
	if !cancelled(t, h.Fire("session_before_switch", map[string]any{"reason": "new"})) {
		t.Error("declining did not cancel")
	}
	if got, want := titleOf(t, h), "You have 2 uncommitted file(s). new session anyway?"; got != want {
		t.Errorf("title = %q, want %q", got, want)
	}
	opts, _ := h.CallsTo("ui.select")[0].Args["options"].([]any)
	if len(opts) != 2 || opts[0] != yes || opts[1] != no {
		t.Errorf("options = %v", opts)
	}
	notes := h.CallsTo("ui.notify")
	if len(notes) != 1 || notes[0].Args["message"] != "Commit your changes first" || notes[0].Args["level"] != "warning" {
		t.Errorf("notifications = %+v", notes)
	}
}

func TestDirtyAllowedProceeds(t *testing.T) {
	h := StartHost(t, guard.Extension(), HostOptions{OnCall: answers{stdout: " M a\n", selected: yes, ok: true}.onCall})
	if cancelled(t, h.Fire("session_before_switch", map[string]any{"reason": "new"})) {
		t.Error("allowing still cancelled")
	}
	if n := len(h.CallsTo("ui.notify")); n != 0 {
		t.Errorf("%d notifications after allowing", n)
	}
}

func TestDismissedDialogCancels(t *testing.T) {
	h := StartHost(t, guard.Extension(), HostOptions{OnCall: answers{stdout: " M a\n", selected: "", ok: false}.onCall})
	if !cancelled(t, h.Fire("session_before_switch", map[string]any{"reason": "new"})) {
		t.Error("a dismissed dialog did not cancel")
	}
	if n := len(h.CallsTo("ui.notify")); n != 1 {
		t.Errorf("%d notifications, want the warning", n)
	}
}

func TestAnyOtherChoiceCancels(t *testing.T) {
	h := StartHost(t, guard.Extension(), HostOptions{OnCall: answers{stdout: " M a\n", selected: "yes, proceed anyway", ok: true}.onCall})
	if !cancelled(t, h.Fire("session_before_switch", map[string]any{"reason": "new"})) {
		t.Error("a choice that is not exactly the allow option must cancel")
	}
}

func TestActionWordingFollowsTheEvent(t *testing.T) {
	cases := []struct {
		event string
		data  map[string]any
		want  string
	}{
		{"session_before_switch", map[string]any{"reason": "new"}, "You have 1 uncommitted file(s). new session anyway?"},
		{"session_before_switch", map[string]any{"reason": "resume"}, "You have 1 uncommitted file(s). switch session anyway?"},
		{"session_before_switch", map[string]any{}, "You have 1 uncommitted file(s). switch session anyway?"},
		{"session_before_fork", map[string]any{}, "You have 1 uncommitted file(s). fork anyway?"},
	}
	for _, c := range cases {
		h := StartHost(t, guard.Extension(), HostOptions{OnCall: answers{stdout: " M a\n", selected: yes, ok: true}.onCall})
		h.Fire(c.event, c.data)
		if got := titleOf(t, h); got != c.want {
			t.Errorf("%s %v: title = %q, want %q", c.event, c.data, got, c.want)
		}
	}
}

func TestCleanOrFailingGitAllowsWithoutAsking(t *testing.T) {
	for name, a := range map[string]answers{
		"clean":           {stdout: "", code: 0},
		"whitespace only": {stdout: " \u00a0\ufeff\n", code: 0},
		"not a repo":      {stdout: "", code: 128},
		"failure output":  {stdout: " M a\n", code: 1},
	} {
		h := StartHost(t, guard.Extension(), HostOptions{OnCall: a.onCall})
		if cancelled(t, h.Fire("session_before_switch", map[string]any{"reason": "new"})) {
			t.Errorf("%s: cancelled", name)
		}
		if n := len(h.CallsTo("ui.select")); n != 0 {
			t.Errorf("%s: asked", name)
		}
	}
}

// ctx.hasUI is false in print and JSON mode, which RPC cannot reproduce: a dirty
// tree cancels without a dialog. Clean and failing git still allow.
func TestHeadlessDirtyCancelsWithoutAsking(t *testing.T) {
	for _, mode := range []string{"print", "json"} {
		h := StartHost(t, guard.Extension(), HostOptions{Mode: mode, OnCall: answers{stdout: " M a\n", code: 0}.onCall})
		if !cancelled(t, h.Fire("session_before_switch", map[string]any{"reason": "new"})) {
			t.Errorf("%s: dirty tree did not cancel", mode)
		}
		if n := len(h.CallsTo("ui.select")) + len(h.CallsTo("ui.notify")); n != 0 {
			t.Errorf("%s: touched the UI %d times", mode, n)
		}
	}
	h := StartHost(t, guard.Extension(), HostOptions{Mode: "print", OnCall: answers{code: 128}.onCall})
	if cancelled(t, h.Fire("session_before_fork", nil)) {
		t.Error("a failing git cancelled without UI")
	}
}

// Pi's exec never rejects (a failed spawn resolves with code 1). A host or
// transport failure is different: it must surface as the handler's error.
func TestHostFailureSurfacesAsError(t *testing.T) {
	h := StartHost(t, guard.Extension(), HostOptions{OnCall: func(method string, _ map[string]any) (map[string]any, string) {
		if method == "exec" {
			return nil, "host unavailable"
		}
		return nil, ""
	}})
	// Fire fails the test on a handler error, so observe the response directly.
	t.Run("handler error", func(t *testing.T) {
		raw := h.FireAllowError("session_before_switch", map[string]any{"reason": "new"})
		if raw == "" {
			t.Error("host failure was swallowed")
		}
	})
}
