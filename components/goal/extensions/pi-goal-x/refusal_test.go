package pi_goal_x_test

import (
	"testing"
)

// What the port does not do yet is refused with one message, never ignored (port/PORT.md, "Refusals").

func want(feature string) string {
	return "error: @pi-in-go/pigpen-goal (Go port) does not support " + feature + " yet. Install the original extension, npm:pi-goal-x@0.32.3, which runs unchanged in PiG."
}

func lastNotice(t *testing.T, h *Host) string {
	t.Helper()
	n := notices(h)
	if len(n) == 0 {
		t.Fatal("no notification")
	}
	return n[len(n)-1]
}

func TestRefusesEveryUnportedCommand(t *testing.T) {
	for _, c := range []struct{ name, feature string }{
		{"goal", "guided goal drafting (/goal)"},
		{"sisyphus", "guided Sisyphus goal drafting (/sisyphus)"},
		{"goal-cancel", "guided goal drafting (/goal-cancel)"},
		{"goal-status", "the goal dashboard (/goal-status)"},
		{"goal-refresh", "/goal-refresh"},
		{"goal-recovery", "/goal-recovery"},
		{"goal-focus", "/goal-focus"},
		{"goal-settings", "the goal settings menu (/goal-settings)"},
		{"goal-tweak", "goal revision (/goal-tweak)"},
		{"goal-resume", "automatic goal continuation (/goal-resume)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			h, cwd := rig{}.start(t)
			if failure := h.Command(c.name, "anything"); failure != "" {
				t.Fatal(failure)
			}
			if got := lastNotice(t, h); got != want(c.feature) {
				t.Errorf("notice %q", got)
			}
			if n := len(goalFiles(t, cwd, ".pi/goals")); n != 0 {
				t.Errorf("%d goal files written by a refused command", n)
			}
		})
	}
}

func TestRefusesAnAllowanceThatNeedsRuns(t *testing.T) {
	for name, settings := range map[string]string{
		"unset":   `{}`,
		"finite":  `{"maxAutonomousRuns": 3}`,
		"global":  `{"disableContracts": false}`,
		"runtime": `{"maxAutonomousRuns": 1000000}`,
	} {
		t.Run(name, func(t *testing.T) {
			for _, cmd := range []string{"goal-direct", "sisyphus-direct"} {
				h, cwd := rig{settings: settings}.start(t)
				h.Command(cmd, "1) Write it, done when written. 2) Check it, done when checked.")
				if got := lastNotice(t, h); got != want("automatic goal continuation (maxAutonomousRuns other than 0)") {
					t.Errorf("%s: notice %q", cmd, got)
				}
				if n := len(goalFiles(t, cwd, ".pi/goals")); n != 0 {
					t.Errorf("%s: %d goal files written by a refused command", cmd, n)
				}
				if e := h.CallsTo("appendEntry"); len(e) != 0 {
					t.Errorf("%s: focus entry written by a refused command", cmd)
				}
			}
		})
	}
}

func TestRefusesASettingThePortDoesNotImplement(t *testing.T) {
	for _, c := range []struct{ settings, feature string }{
		{`{"maxAutonomousRuns": 0, "goalsRoot": "/tmp/x"}`, "the setting goalsRoot"},
		{`{"maxAutonomousRuns": 0, "model": "m", "oracle": {"enabled": true}}`, "the settings model, oracle"},
		{`{"maxAutonomousRuns": 0, "networkRecovery": {"maxAttempts": 2}}`, "the setting networkRecovery"},
		{`{"maxAutonomousRuns": 0, "objectiveMaxChars": 100}`, "the setting objectiveMaxChars"},
	} {
		h, cwd := rig{settings: c.settings}.start(t)
		h.Command("goal-direct", "Do the thing")
		if got := lastNotice(t, h); got != want(c.feature) {
			t.Errorf("%s: notice %q", c.settings, got)
		}
		if n := len(goalFiles(t, cwd, ".pi/goals")); n != 0 {
			t.Errorf("%s: %d goal files written by a refused command", c.settings, n)
		}
	}
}

func TestRefusesAnEnvironmentSettingThePortDoesNotImplement(t *testing.T) {
	t.Setenv("PI_GOAL_ROOT", "/tmp/elsewhere")
	h, cwd := rig{}.start(t)
	h.Command("goal-direct", "Do the thing")
	if got := lastNotice(t, h); got != want("the setting PI_GOAL_ROOT") {
		t.Errorf("notice %q", got)
	}
	if n := len(goalFiles(t, cwd, ".pi/goals")); n != 0 {
		t.Errorf("%d goal files written by a refused command", n)
	}
}
