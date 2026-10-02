package jev_test

import (
	jev "github.com/MichaelKinsy/pigpen/jev"
	"strings"
	"testing"
)

// The /jev command (src/index.ts registerCommand).

func cmdHost(t *testing.T, body any, extra map[string]any) (*Host, *fakeJev, *hostState) {
	t.Helper()
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(body))
	e.writeGlobal(t, jevConfig(srv, extra))
	hs := newHostState()
	return start(t, e, hs, HostOptions{}), srv, hs
}

func lastNote(h *Host) string {
	n := h.notifications()
	if len(n) == 0 {
		return "<none>"
	}
	return n[len(n)-1]
}

func TestCommand_OffAndOnToggleBothJudges(t *testing.T) {
	h, srv, hs := cmdHost(t, clearGate(), nil)
	hs.confirm = true
	h.Command("jev", "off")
	if got := lastNote(h); got != "info: pi-jev: gate off" || h.lastStatus() != "jev: off" {
		t.Errorf("off: note %q status %q", got, h.lastStatus())
	}
	h.toolCall("bash", bash("x"))
	h.toolResult("bash", bash("x"), text("y"), false)
	if srv.count() != 0 {
		t.Fatal("judged while off")
	}
	h.Command("jev", "on")
	if got := lastNote(h); got != "info: pi-jev: gate on" || h.lastStatus() != "jev: shadow" {
		t.Errorf("on: note %q status %q", got, h.lastStatus())
	}
	h.toolCall("bash", bash("x"))
	if srv.count() != 1 {
		t.Fatalf("requests = %d", srv.count())
	}
}

func TestCommand_ModeSwitch(t *testing.T) {
	h, _, _ := cmdHost(t, gateBody(0.99, 0, 0, 0, 0.9), nil)
	h.Command("jev", "mode enforce")
	if got := lastNote(h); got != "info: pi-jev: enforce (asks before running flagged calls)" {
		t.Errorf("note = %q", got)
	}
	if block, _ := h.toolCall("bash", bash("x")); !block {
		t.Error("enforce did not ask/decline")
	}
	h.Command("jev", "mode shadow")
	if got := lastNote(h); got != "info: pi-jev: shadow (reports, never blocks)" {
		t.Errorf("note = %q", got)
	}
	h.Command("jev", "mode bogus")
	if got := lastNote(h); got != "warning: pi-jev: mode is shadow (usage: /jev mode shadow|enforce)" {
		t.Errorf("note = %q", got)
	}
}

func TestCommand_LastVerdict(t *testing.T) {
	h, _, _ := cmdHost(t, flaggedGate(), nil)
	h.Command("jev", "last")
	if got := lastNote(h); got != "info: pi-jev: no verdicts yet" {
		t.Errorf("note = %q", got)
	}
	h.toolCall("bash", bash("rm -rf src"))
	h.Command("jev", "last")
	mustContain(t, "last", lastNote(h), "pi-jev: bash - destructive 0.99",
		"destructive=yes 0.99", "exfiltration=yes 0.79", "beyond_scope=yes 0.98", "impact=3.00/3 (conf 0.91)")
}

func TestCommand_OutputVerdict(t *testing.T) {
	h, _, _ := cmdHost(t, outBody(0.94, "transient", 0.8), nil)
	h.Command("jev", "output")
	if got := lastNote(h); got != "info: pi-jev: no tool output judged yet" {
		t.Errorf("note = %q", got)
	}
	h.toolResult("bash", bash("x"), text("out"), false)
	h.Command("jev", "output")
	if got := lastNote(h); got != "info: pi-jev output: bash - leak | leak 0.94 | class transient at 0.80" {
		t.Errorf("note = %q", got)
	}
}

// C9: the original recorded only verdicts that carried a notice, so a clean
// output was reported as "no tool output judged yet".
func TestCorrection_CleanOutputIsRecordedToo(t *testing.T) {
	h, _, _ := cmdHost(t, outBody(0.01, "no_failure", 0.99), nil)
	h.toolResult("bash", bash("x"), text("fine"), false)
	h.Command("jev", "output")
	if got := lastNote(h); got != "info: pi-jev output: bash - none | leak 0.01 | class no_failure at 0.99" {
		t.Errorf("note = %q", got)
	}
}

func TestCommand_CheckRunsTheGateQuestionsOnSuppliedText(t *testing.T) {
	h, srv, _ := cmdHost(t, gateBody(0.99, 0.1, 0.1, 1.0, 0.9), nil)
	h.Command("jev", "check Rm -RF /tmp/Build")
	reqs := srv.requests()
	if len(reqs) != 1 || reqs[0].Body["state"] != "Rm -RF /tmp/Build" {
		t.Fatalf("requests = %+v", reqs)
	}
	mustContain(t, "check", lastNote(h), "pi-jev check: destructive 0.99 | destructive=yes 0.99")
	h.Command("jev", "last")
	mustContain(t, "last after check", lastNote(h), "pi-jev: check - destructive 0.99")
	h.Command("jev", "check")
	if got := lastNote(h); got != "warning: pi-jev: usage /jev check <text>" {
		t.Errorf("note = %q", got)
	}
}

func TestCommand_CheckErrorIsReportedAsError(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, func(int, recordedRequest) jevReply { return jevReply{status: 422, body: "bad"} })
	e.writeGlobal(t, jevConfig(srv, nil))
	h := start(t, e, newHostState(), HostOptions{})
	h.Command("jev", "check hello")
	mustContain(t, "check error", lastNote(h), "error: pi-jev: ", "422")
}

func TestCommand_StatusShowsPolicyAndDestination(t *testing.T) {
	h, srv, _ := cmdHost(t, clearGate(), map[string]any{"display": "rich"})
	h.Command("jev", "")
	note := lastNote(h)
	mustContain(t, "status", note, "shadow", "jev-test", srv.srv.URL, "fails open", "bash", "TYPESAFE_API_KEY")
}

func TestCommand_StatusIsTruthfulAboutTheKeySource(t *testing.T) {
	h, _, _ := cmdHost(t, clearGate(), map[string]any{"display": "rich"})
	h.Command("jev", "")
	if strings.Contains(lastNote(h), "inline") {
		t.Errorf("an environment key reported as inline: %q", lastNote(h))
	}
}

func TestCommand_StatusWhenOffExplainsOptIn(t *testing.T) {
	e := newEnv(t)
	h := start(t, e, newHostState(), HostOptions{})
	h.Command("jev", "")
	mustContain(t, "status", lastNote(h), "off", "/jev on", "leave this machine")
}

// Opt-in at run time: /jev on asks first and says what leaves the machine.
func TestOptIn_SlashOnAsksForConsentWithDisclosure(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(clearGate()))
	cfg := jevConfig(srv, map[string]any{"display": "rich"})
	delete(cfg, "enabled")
	e.writeGlobal(t, cfg)
	hs := newHostState()
	h := start(t, e, hs, HostOptions{})
	h.Command("jev", "on") // declined: hs.confirm is false
	c := hs.confirmCalls()
	if len(c) != 1 {
		t.Fatalf("confirm calls = %d", len(c))
	}
	mustContain(t, "confirm message", c[0]["message"].(string), "working directory", "tool arguments", srv.srv.URL)
	h.toolCall("bash", bash("x"))
	if srv.count() != 0 {
		t.Fatal("declined consent still judged")
	}
	hs.mu.Lock()
	hs.confirm = true
	hs.mu.Unlock()
	h.Command("jev", "on")
	h.toolCall("bash", bash("x"))
	if srv.count() != 1 {
		t.Fatalf("requests after consent = %d", srv.count())
	}
}

func TestOptIn_ConsentIsPerSessionAndNeverWritesConfig(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(clearGate()))
	cfg := jevConfig(srv, nil)
	delete(cfg, "enabled")
	e.writeGlobal(t, cfg)
	hs := newHostState()
	hs.confirm = true
	h := start(t, e, hs, HostOptions{})
	h.Command("jev", "on")
	h2 := StartHost(t, jev.Extension(), HostOptions{Cwd: e.cwd, OnCall: hs.onCall})
	h2.Fire("session_start", map[string]any{"reason": "startup"})
	h2.toolCall("bash", bash("x"))
	if srv.count() != 0 {
		t.Error("consent leaked into another session")
	}
}

func TestCommand_PlainStatusNamesTheKeySource(t *testing.T) {
	h, _, _ := cmdHost(t, clearGate(), nil)
	h.Command("jev", "")
	mustContain(t, "status", lastNote(h), "key env TYPESAFE_API_KEY", "model jev-test", "judging bash/write/edit", "out tools bash")

	e := newEnv(t)
	srv := newFakeJev(t, always(clearGate()))
	e.writeGlobal(t, jevConfig(srv, nil))
	h2 := start(t, e, newHostState(), HostOptions{})
	h2.Command("jev", "")
	mustContain(t, "status without a key", lastNote(h2), "key missing (TYPESAFE_API_KEY)")
}
