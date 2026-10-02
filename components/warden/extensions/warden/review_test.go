package warden

import (
	"encoding/json"
	"strings"
	"testing"
)

// Tests added by the review of the port (rev-pigpen-warden).

// The disclosure says the user's request is sent redacted. The stuck and done checks send it too, so it must be
// redacted there as well (the action request already redacts it: guard.go BuildRequest).
func TestTheStuckAndDoneRequestsRedactTheUsersRequest(t *testing.T) {
	task := "deploy with TOKEN=supersecretvalue1"
	stuck, _ := json.Marshal(BuildStuckRequest(nil, task).State)
	if strings.Contains(string(stuck), "supersecretvalue1") {
		t.Errorf("the stuck request carries the secret: %s", stuck)
	}
	done, _ := json.Marshal(BuildDoneRequest(task, "Done.", EmptyEvidence()).State)
	if strings.Contains(string(done), "supersecretvalue1") {
		t.Errorf("the done request carries the secret: %s", done)
	}
}

// A write's content excerpt and the earlier messages leave the machine redacted (disclosure: "redacted").
func TestTheWriteExcerptAndTheEarlierMessagesAreRedacted(t *testing.T) {
	j := actionJudge(0.1, 0.1, "expected_step", nil)
	eval(ActionInput{Tool: "write", Input: map[string]any{"path": "notes.txt", "content": "export API_KEY=sk-abcdefgh12345678\n"}, Cwd: cwd, Task: "write the notes",
		Context: []TaskMessage{{Role: "user", Text: "use password=hunter2hunter2"}}}, EvaluateOptions{Config: cfg(), Judge: j})
	calls := j.calls()
	if len(calls) != 1 {
		t.Fatalf("judged %d times", len(calls))
	}
	raw, _ := json.Marshal(calls[0].State)
	for _, leak := range []string{"sk-abcdefgh12345678", "hunter2hunter2"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("the request state carries %q: %s", leak, raw)
		}
	}
}

// D7: a failing TypeSafe request is not retried (a late judgment is worth less than the call it gates).
func TestTypeSafeFailuresAreNotRetried(t *testing.T) {
	f := newFakeTypeSafe(t)
	f.mu.Lock()
	f.status = 500
	f.mu.Unlock()
	v := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "npm test"}, Task: "t"}, EvaluateOptions{Config: cfg(), Judge: typesafeJudge(t, f)})
	if v.Source != "error" {
		t.Fatalf("verdict %+v", v)
	}
	if n := f.count(); n != 1 {
		t.Errorf("a failing request was sent %d times", n)
	}
}

// The own-model backend spends the same per-session budget as TypeSafe.
func TestTheOwnModelBackendIsBudgetLimited(t *testing.T) {
	reg := newRegistry()
	b := &Budget{Max: 1}
	j, err := NewOwnModelJudge(reg, "prov", "mod", b)
	if err != nil {
		t.Fatal(err)
	}
	in := ActionInput{Tool: "bash", Input: map[string]any{"command": "npm test"}, Task: "t"}
	eval(in, EvaluateOptions{Config: cfg(), Judge: j})
	v := eval(in, EvaluateOptions{Config: cfg(), Judge: j})
	if v.Source != "error" || v.ErrorCode != "budget" {
		t.Errorf("second call past the budget: %+v", v)
	}
	if n := len(reg.systemPrompts()); n != 1 {
		t.Errorf("session-model requests %d, want 1", n)
	}
}

// Setting PIGPEN_WARDEN_ENABLED / _BACKEND is consent for that run only. A command that saves the settings in such
// a run must not write the environment's choice into the config file, or a later session without the environment
// would send to the backend without the dialog.
func TestTheEnvironmentsConsentIsNotWrittenToTheConfigFile(t *testing.T) {
	h := startHarness(t, harnessOpts{env: map[string]string{"PIGPEN_WARDEN_ENABLED": "1", "PIGPEN_WARDEN_BACKEND": "typesafe", "PIGPEN_WARDEN_STEER_VISIBLE": "0"}})
	h.start()
	h.host.Command("warden", "mode advise")
	cfg, err := LoadConfig(ConfigPath(h.home))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != "advise" {
		t.Errorf("the mode the user chose was not saved: %q", cfg.Mode)
	}
	if cfg.Enabled || cfg.Backend != BackendNone || cfg.Consent != "" || !cfg.SteerVisible {
		t.Errorf("the environment's settings were persisted: enabled %v backend %q consent %q steerVisible %v", cfg.Enabled, cfg.Backend, cfg.Consent, cfg.SteerVisible)
	}
	// The running session still honours the environment.
	h.ts.set(map[string]float64{"irreversible": 0.95})
	h.say("user", "push")
	h.toolCall("bash", "c1", map[string]any{"command": "npm test"})
	if h.ts.count() != 1 {
		t.Errorf("the environment's backend stopped working in this run: %d requests", h.ts.count())
	}
}

// The status line says when the chosen judge is not in use: without consent, or TypeSafe without a key, warden runs
// offline and must not claim otherwise.
func TestTheStatusLineSaysWhenTheJudgeIsNotInUse(t *testing.T) {
	noKey := startHarness(t, harnessOpts{cfg: on(BackendTypeSafe), env: map[string]string{"TYPESAFE_API_KEY": ""}})
	noKey.start()
	if got := noKey.status(); !strings.HasPrefix(got, "warden: offline (typesafe: no TYPESAFE_API_KEY) · steer ·") {
		t.Errorf("status without a key %q", got)
	}
	c := DefaultConfig()
	c.Enabled, c.Backend = true, BackendTypeSafe // no Consent
	noConsent := startHarness(t, harnessOpts{cfg: &c})
	noConsent.start()
	if got := noConsent.status(); !strings.HasPrefix(got, "warden: offline (typesafe not agreed to) · steer ·") {
		t.Errorf("status without consent %q", got)
	}
	ready := startHarness(t, harnessOpts{cfg: on(BackendTypeSafe)})
	ready.start()
	if got := ready.status(); !strings.HasPrefix(got, "warden: typesafe · steer ·") {
		t.Errorf("status when ready %q", got)
	}
}
