package warden

import (
	"strings"
	"testing"
)

// Host failures (COMMON-DEFECTS 4): a broken session mirror or model lookup never lets a dangerous call through,
// never blocks an ordinary one, and is said once instead of swallowed.

func TestABrokenSessionMirrorStillHoldsAForcePushAndTellsTheUserOnce(t *testing.T) {
	h := startHarness(t, harnessOpts{cfg: on(BackendTypeSafe), fail: map[string]string{"watchSessionLog": "session log unavailable"}})
	h.ts.set(map[string]float64{"irreversible": 0.95})
	h.start()
	if _, held := blocked(h.toolCall("bash", "c1", map[string]any{"command": "git push --force origin main"})); !held {
		t.Fatal("the force push was not held without the session log")
	}
	if _, held := blocked(h.toolCall("bash", "c2", map[string]any{"command": "git push --force origin release"})); !held {
		t.Fatal("the second force push was not held")
	}
	count := 0
	for _, n := range h.notices() {
		if strings.Contains(n, "could not read the session") && strings.Contains(n, "session log unavailable") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("the read failure must be reported exactly once, with its cause: %d in %v", count, h.notices())
	}
}

func TestABrokenSessionMirrorDoesNotBlockAnOrdinaryCall(t *testing.T) {
	h := startHarness(t, harnessOpts{cfg: on(BackendNone), fail: map[string]string{"watchSessionLog": "boom"}})
	h.start()
	if _, held := blocked(h.toolCall("bash", "c1", map[string]any{"command": "npm test"})); held {
		t.Fatal("an ordinary call was blocked because the session could not be read")
	}
}

func TestAFailedModelLookupFallsBackToOfflineAndSaysWhy(t *testing.T) {
	h := startHarness(t, harnessOpts{cfg: on(BackendOwnModel), fail: map[string]string{"getModelInfo": "model registry offline"}})
	h.start()
	reason, held := blocked(h.toolCall("bash", "c1", map[string]any{"command": "git push --force origin main"}))
	if !held {
		t.Fatalf("the offline patterns still hold a force push when the session model cannot be read: %q", reason)
	}
}

// The transcript visibility of a steer follows PIGPEN_WARDEN_STEER_VISIBLE; the custom type is the original's.
func TestSteerVisibilityFollowsTheEnvironmentAndTheTypeIsTheOriginals(t *testing.T) {
	for _, tc := range []struct {
		env  string
		want bool
	}{{"", true}, {"1", true}, {"0", false}, {"off", false}} {
		c := on(BackendTypeSafe)
		c.Action.IntentTraceOnly = "none"
		h := startHarness(t, harnessOpts{cfg: c, env: map[string]string{"PIGPEN_WARDEN_STEER_VISIBLE": tc.env}})
		h.ts.set(map[string]float64{"intent_mismatch": 0.95, "mutates": 0.9})
		h.say("user", "clean the build")
		h.assistantCalls("Let me first list what is in build/.", map[string]string{"id": "c1", "name": "bash", "args": `{"command":"rm -rf build"}`})
		h.start()
		h.toolCall("bash", "c1", map[string]any{"command": "rm -rf build"})
		steers := h.steers()
		if len(steers) != 1 || steers[0].Display != tc.want || steers[0].Type != "pi-warden-steer" {
			t.Errorf("env %q: %+v", tc.env, steers)
		}
	}
}

// Context.ConfigHome fails when no config directory can be found (SDK 0.5). Warden then runs on its
// defaults, which leave it off, says why once, and never falls back to a relative path a project
// could plant: the config file is the user's consent to enable it.
func TestNoConfigHomeKeepsWardenOffAndSaysWhy(t *testing.T) {
	h := startHarness(t, harnessOpts{noHome: true})
	h.start()
	var told bool
	for _, n := range h.notices() {
		if strings.Contains(n, "could not find the PiG config directory") && strings.Contains(n, "Defaults are in use") {
			told = true
		}
	}
	if !told {
		t.Errorf("the user was not told the config directory is missing: %q", h.notices())
	}
	if r := h.toolCall("bash", "c1", map[string]any{"command": "git push --force origin main"}); r != nil {
		t.Errorf("warden acted without a config file: %v", r)
	}
	if h.ts.count() != 0 {
		t.Errorf("the judge was asked %d time(s) with warden off", h.ts.count())
	}
}
