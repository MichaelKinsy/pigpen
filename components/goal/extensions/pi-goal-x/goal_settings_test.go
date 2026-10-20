package pi_goal_x

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Tests ported from pi-goal-x tests/goal-settings.test.ts.

type senv = map[string]string

// isolateHome points the home directory at an empty temp dir so that the global settings file of the machine running the
// tests is never read (the original's tests read ~/.pi/agent/pi-goal-x-settings.json when no override is given).
func isolateHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	for _, k := range []string{"PI_CODING_AGENT_DIR", piGoalSettingsFileEnv, piGoalGlobalSettingsFileEnv, "PI_GOAL_ROOT",
		"PI_GOAL_DISABLE_TASKS", "PI_GOAL_DISABLE_CONTRACTS", "PI_GOAL_OBJECTIVE_MAX_CHARS",
		"PI_GOAL_NETWORK_RECOVERY_MAX_ATTEMPTS", "PI_GOAL_NETWORK_RECOVERY_MAX_DELAY_MS"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	invalidateGoalSettingsCache()
}

func settingsPJ(t *testing.T, text string) any {
	t.Helper()
	v, err := parseJSON([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// settingsCanon turns ordered objects into plain maps, so a comparison ignores key order (assert.deepEqual does).
func settingsCanon(v any) any {
	switch x := v.(type) {
	case *jsObject:
		m := map[string]any{}
		for _, k := range x.keys {
			if _, u := x.vals[k].(undef); !u {
				m[k] = settingsCanon(x.vals[k])
			}
		}
		return m
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = settingsCanon(e)
		}
		return out
	}
	return v
}

func settingsDeepEqual(t *testing.T, got *jsObject, want string, msg string) {
	t.Helper()
	if !reflect.DeepEqual(settingsCanon(got), settingsCanon(settingsPJ(t, want))) {
		t.Errorf("%s: got %s, want %s", msg, marshalJSON(got, ""), want)
	}
}

func mustParse(t *testing.T, raw string) *jsObject {
	t.Helper()
	l, err := parseGoalSettings(settingsPJ(t, raw))
	if err != nil {
		t.Fatalf("parseGoalSettings(%s): %v", raw, err)
	}
	return l
}

func settingsWrite(t *testing.T, file, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func flagOf(o *jsObject, k string) any {
	if v, ok := o.flag(k); ok {
		return v
	}
	return nil
}

// settingsStubGuard turns the panic of a symbol owned by another slice (still a stub) into a red twin instead of a crashed binary.
func settingsStubGuard(t *testing.T) {
	if r := recover(); r != nil {
		t.Fatalf("%v", r)
	}
}

func TestGoalSettingsTwins(t *testing.T) {
	const f = "goal-settings"
	tw(t, f, "parseGoalSettings: null/undefined returns empty defaults", func(t *testing.T) {
		for _, raw := range []any{nil, "", 42.0, []any{}} {
			l, err := parseGoalSettings(raw)
			if err != nil {
				t.Fatal(err)
			}
			settingsDeepEqual(t, l, `{}`, "empty")
		}
	})
	tw(t, f, "parseGoalSettings: empty object returns empty defaults", func(t *testing.T) {
		settingsDeepEqual(t, mustParse(t, `{}`), `{}`, "empty")
	})
	tw(t, f, "parseGoalSettings: both flags false returns false defaults", func(t *testing.T) {
		r := mustParse(t, `{"disableTasks":false,"disableContracts":false}`)
		eq(t, flagOf(r, "disableTasks"), false, "disableTasks")
		eq(t, flagOf(r, "disableContracts"), false, "disableContracts")
	})
	tw(t, f, "parseGoalSettings: both flags true", func(t *testing.T) {
		r := mustParse(t, `{"disableTasks":true,"disableContracts":true}`)
		eq(t, flagOf(r, "disableTasks"), true, "disableTasks")
		eq(t, flagOf(r, "disableContracts"), true, "disableContracts")
	})
	tw(t, f, "parseGoalSettings: boolean false stored correctly", func(t *testing.T) {
		r := mustParse(t, `{"disableTasks":false,"disableContracts":true}`)
		eq(t, flagOf(r, "disableTasks"), false, "disableTasks")
		eq(t, flagOf(r, "disableContracts"), true, "disableContracts")
	})
	tw(t, f, "parseGoalSettings: string true/false values accepted", func(t *testing.T) {
		settingsDeepEqual(t, mustParse(t, `{"disableTasks":"true","disableContracts":"false"}`), `{"disableTasks":true,"disableContracts":false}`, "strings")
	})
	tw(t, f, "parseGoalSettings: autoSelectSingleGoal accepted as bool or string; explicit false preserved (layering)", func(t *testing.T) {
		settingsDeepEqual(t, mustParse(t, `{"autoSelectSingleGoal":true}`), `{"autoSelectSingleGoal":true}`, "true")
		settingsDeepEqual(t, mustParse(t, `{"autoSelectSingleGoal":"true"}`), `{"autoSelectSingleGoal":true}`, "string true")
		// Issue #27 layering requirement: explicit false must survive parsing so a project can override a global true.
		settingsDeepEqual(t, mustParse(t, `{"autoSelectSingleGoal":false}`), `{"autoSelectSingleGoal":false}`, "false")
		settingsDeepEqual(t, mustParse(t, `{"autoSelectSingleGoal":"false"}`), `{"autoSelectSingleGoal":false}`, "string false")
	})
	tw(t, f, "parseGoalSettings: unknown keys rejected", func(t *testing.T) {
		_, err := parseGoalSettings(settingsPJ(t, `{"disableTasks":true,"disableContracts":false,"foo":"bar"}`))
		if err == nil || !strings.Contains(err.Error(), "Unknown pi-goal-x-settings.json key") {
			t.Errorf("want the unknown key error, got %v", err)
		}
	})
	tw(t, f, "formatGoalKeybinding: renders readable labels for named keys", func(t *testing.T) {
		for in, want := range map[string]string{
			"ctrl+shift+t":      "Ctrl+Shift+T",
			"ctrl+shift+up":     "Ctrl+Shift+↑",
			"ctrl+shift+pageUp": "Ctrl+Shift+PageUp",
			"ctrl+alt+pageDown": "Ctrl+Alt+PageDown",
			"ctrl+home":         "Ctrl+Home",
			"ctrl+alt+enter":    "Ctrl+Alt+Enter",
		} {
			eq(t, formatGoalKeybinding(in), want, in)
		}
	})
	tw(t, f, "parseGoalSettings: task keybindings use pi-tui key names", func(t *testing.T) {
		isolateHome(t)
		const kb = `{"keybindings":{"dashboard":{"toggleExpand":"ctrl+shift+t","scrollUp":"ctrl+shift+up","scrollDown":"ctrl+shift+down"}}}`
		settingsDeepEqual(t, mustParse(t, kb), kb, "keybindings")
		eq(t, loadGoalSettings("/tmp/does-not-exist", senv{}).keybindings.dashboard.toggleExpand, "ctrl+shift+t", "default toggleExpand")
	})
	tw(t, f, "parseGoalSettings: unknown nested task keybindings become diagnostics, valid siblings still apply", func(t *testing.T) {
		layer, diags := parseSettingsLayer(settingsPJ(t, `{"keybindings":{"dashboard":{"expand":"ctrl+t","scrollUp":"ctrl+u"}}}`), settingsScopeProject, "t.json")
		eq(t, layerLeaf(layer, "keybindings", "dashboard", "scrollUp"), "ctrl+u", "valid sibling key still applies")
		found := false
		for _, d := range diags {
			found = found || (d.code == "unknown_key" && d.settingPath == "keybindings.dashboard.expand")
		}
		if !found {
			t.Errorf("no unknown_key diagnostic: %+v", diags)
		}
	})
	tw(t, f, "parseGoalSettings: multiple unknown keys rejected with the full list", func(t *testing.T) {
		_, err := parseGoalSettings(settingsPJ(t, `{"disableTasks":true,"foo":"bar","baz":42}`))
		if err == nil || !strings.Contains(err.Error(), "foo, baz") {
			t.Errorf("want the list foo, baz, got %v", err)
		}
	})
	tw(t, f, "goalSettingsPath: resolves under .pi/ with new filename", func(t *testing.T) {
		isolateHome(t)
		p := goalSettingsPath("/tmp/project")
		if !strings.HasSuffix(p, filepath.Join(".pi", "pi-goal-x-settings.json")) || !strings.HasPrefix(p, "/tmp/project") {
			t.Errorf("got %s", p)
		}
	})
	tw(t, f, "goalSettingsPath: respects PI_GOAL_SETTINGS_FILE env var", func(t *testing.T) {
		eq(t, goalSettingsPath("/tmp/project", senv{"PI_GOAL_SETTINGS_FILE": "custom-settings.json"}), filepath.Join("/tmp/project", "custom-settings.json"), "relative")
		eq(t, goalSettingsPath("/tmp/project", senv{"PI_GOAL_SETTINGS_FILE": "/etc/pi/settings.json"}), "/etc/pi/settings.json", "absolute")
		def := goalSettingsPath("/tmp/project", senv{})
		if !strings.HasSuffix(def, filepath.Join(".pi", "pi-goal-x-settings.json")) {
			t.Errorf("default: %s", def)
		}
	})
	tw(t, f, "loadGoalSettingsFileConfig: missing file returns empty defaults", func(t *testing.T) {
		isolateHome(t)
		settingsDeepEqual(t, loadGoalSettingsFileConfig(t.TempDir()), `{}`, "missing")
	})
	tw(t, f, "loadGoalSettingsFileConfig: reads valid file config", func(t *testing.T) {
		isolateHome(t)
		dir := t.TempDir()
		settingsWrite(t, goalSettingsPath(dir), `{"disableTasks":true,"disableContracts":false}`)
		r := loadGoalSettingsFileConfig(dir)
		eq(t, flagOf(r, "disableTasks"), true, "disableTasks")
		eq(t, flagOf(r, "disableContracts"), false, "disableContracts")
	})
	tw(t, f, "loadGoalSettingsFileConfig: malformed JSON returns empty defaults", func(t *testing.T) {
		isolateHome(t)
		dir := t.TempDir()
		settingsWrite(t, goalSettingsPath(dir), "not-json")
		settingsDeepEqual(t, loadGoalSettingsFileConfig(dir), `{}`, "malformed")
	})
	tw(t, f, "loadGoalSettingsFileConfig: unknown keys diagnosed while known values still load (layered rewrite)", func(t *testing.T) {
		isolateHome(t)
		dir := t.TempDir()
		settingsWrite(t, goalSettingsPath(dir), `{"disableTasks":true,"extra":"bad"}`)
		eq(t, flagOf(loadGoalSettingsFileConfig(dir), "disableTasks"), true, "the valid known key still applies")
	})
	tw(t, f, "loadGoalSettings: no file, no env vars -> defaults false", func(t *testing.T) {
		isolateHome(t)
		r := loadGoalSettings(t.TempDir(), senv{})
		eq(t, r.disableTasks, false, "disableTasks")
		eq(t, r.disableContracts, false, "disableContracts")
		eq(t, r.autoSelectSingleGoal, false, "autoSelectSingleGoal defaults to false")
	})
	tw(t, f, "loadGoalSettings: env vars override file config", func(t *testing.T) {
		isolateHome(t)
		dir := t.TempDir()
		settingsWrite(t, goalSettingsPath(dir), `{"disableTasks":true,"disableContracts":true}`)
		r := loadGoalSettings(dir, senv{"PI_GOAL_DISABLE_TASKS": "false", "PI_GOAL_DISABLE_CONTRACTS": "true"})
		eq(t, r.disableTasks, false, "env override should win")
		eq(t, r.disableContracts, true, "file value used when no env override")
	})
	tw(t, f, "loadGoalSettings: env var true overrides file false", func(t *testing.T) {
		isolateHome(t)
		dir := t.TempDir()
		settingsWrite(t, goalSettingsPath(dir), `{"disableTasks":false,"disableContracts":false}`)
		r := loadGoalSettings(dir, senv{"PI_GOAL_DISABLE_TASKS": "true"})
		eq(t, r.disableTasks, true, "disableTasks")
		eq(t, r.disableContracts, false, "disableContracts")
	})
	tw(t, f, "loadGoalSettings: env var absent falls back to file", func(t *testing.T) {
		isolateHome(t)
		dir := t.TempDir()
		settingsWrite(t, goalSettingsPath(dir), `{"disableTasks":true,"disableContracts":true}`)
		r := loadGoalSettings(dir, senv{"SOME_OTHER_VAR": "x"})
		eq(t, r.disableTasks, true, "disableTasks")
		eq(t, r.disableContracts, true, "disableContracts")
	})
	tw(t, f, "loadGoalSettings: env var non-true values treated as absent", func(t *testing.T) {
		isolateHome(t)
		dir := t.TempDir()
		settingsWrite(t, goalSettingsPath(dir), `{"disableTasks":false,"disableContracts":false}`)
		r := loadGoalSettings(dir, senv{"PI_GOAL_DISABLE_TASKS": "1", "PI_GOAL_DISABLE_CONTRACTS": ""})
		eq(t, r.disableTasks, false, "1 is not 'true'")
		eq(t, r.disableContracts, false, "empty string treated as absent")
	})
	tw(t, f, "loadGoalSettings: no file, env var true", func(t *testing.T) {
		isolateHome(t)
		r := loadGoalSettings(t.TempDir(), senv{"PI_GOAL_DISABLE_TASKS": "true", "PI_GOAL_DISABLE_CONTRACTS": "true"})
		eq(t, r.disableTasks, true, "disableTasks")
		eq(t, r.disableContracts, true, "disableContracts")
	})
	tw(t, f, "loadGoalSettings: both flags disabled via file", func(t *testing.T) {
		isolateHome(t)
		dir := t.TempDir()
		settingsWrite(t, goalSettingsPath(dir), `{"disableTasks":true,"disableContracts":true}`)
		r := loadGoalSettings(dir, senv{})
		eq(t, r.disableTasks, true, "disableTasks")
		eq(t, r.disableContracts, true, "disableContracts")
	})
	tw(t, f, "parseGoalSettings: objectiveMaxChars accepted as number or string, 0 allowed", func(t *testing.T) {
		settingsDeepEqual(t, mustParse(t, `{"objectiveMaxChars":0}`), `{"objectiveMaxChars":0}`, "0")
		settingsDeepEqual(t, mustParse(t, `{"objectiveMaxChars":5000}`), `{"objectiveMaxChars":5000}`, "5000")
		settingsDeepEqual(t, mustParse(t, `{"objectiveMaxChars":"2500"}`), `{"objectiveMaxChars":2500}`, "string")
		settingsDeepEqual(t, mustParse(t, `{"objectiveMaxChars":-1}`), `{}`, "negative rejected (invalid settings ignored)")
		settingsDeepEqual(t, mustParse(t, `{"objectiveMaxChars":1.5}`), `{}`, "non-integer rejected")
	})
	tw(t, f, "loadGoalSettings: objectiveMaxChars defaults to no limit and honors the env override", func(t *testing.T) {
		isolateHome(t)
		dir := t.TempDir()
		settingsWrite(t, goalSettingsPath(dir), `{"objectiveMaxChars":2000}`)
		eq(t, loadGoalSettings(dir, senv{}).objectiveMaxChars, 2000.0, "file config read")
		eq(t, loadGoalSettings(dir, senv{"PI_GOAL_OBJECTIVE_MAX_CHARS": "8000"}).objectiveMaxChars, 8000.0, "env var overrides file")
		eq(t, loadGoalSettings("/tmp/does-not-exist", senv{}).objectiveMaxChars, 0.0, "unset resolves to the default 0 (no limit)")
	})
	tw(t, f, "parseGoalSettings: networkRecovery accepts valid layers and rejects invalid leaves", func(t *testing.T) {
		settingsDeepEqual(t, mustParse(t, `{"networkRecovery":{"maxAttempts":0}}`), `{"networkRecovery":{"maxAttempts":0}}`, "0")
		settingsDeepEqual(t, mustParse(t, `{"networkRecovery":{"maxAttempts":8,"maxDelayMs":30000}}`), `{"networkRecovery":{"maxAttempts":8,"maxDelayMs":30000}}`, "both")
		settingsDeepEqual(t, mustParse(t, `{"networkRecovery":{"maxAttempts":-1}}`), `{}`, "negative attempts rejected")
		settingsDeepEqual(t, mustParse(t, `{"networkRecovery":{"maxDelayMs":500}}`), `{}`, "plateau below 1s rejected")
		settingsDeepEqual(t, mustParse(t, `{"networkRecovery":"nope"}`), `{}`, "non-object rejected")
	})
	tw(t, f, "loadGoalSettings: networkRecovery defaults to unbounded and honors file + env layers", func(t *testing.T) {
		isolateHome(t)
		dir := t.TempDir()
		settingsWrite(t, goalSettingsPath(dir), `{"networkRecovery":{"maxAttempts":3}}`)
		eq(t, loadGoalSettings(dir, senv{}).networkRecovery, resolvedGoalNetworkRecoverySettings{maxAttempts: 3, maxDelayMs: 80000}, "file config read with default plateau")
		eq(t, loadGoalSettings(dir, senv{"PI_GOAL_NETWORK_RECOVERY_MAX_ATTEMPTS": "10", "PI_GOAL_NETWORK_RECOVERY_MAX_DELAY_MS": "20000"}).networkRecovery,
			resolvedGoalNetworkRecoverySettings{maxAttempts: 10, maxDelayMs: 20000}, "env vars override file")
		eq(t, loadGoalSettings("/tmp/does-not-exist", senv{}).networkRecovery, resolvedGoalNetworkRecoverySettings{maxAttempts: 0, maxDelayMs: 80000}, "unset resolves to unbounded default policy")
	})
	tw(t, f, "saveGoalSettingsFileConfig: task keybindings round-trip", func(t *testing.T) {
		isolateHome(t)
		dir := t.TempDir()
		const kb = `{"dashboard":{"toggleExpand":"ctrl+shift+t","scrollUp":"ctrl+shift+up","scrollDown":"ctrl+shift+down"}}`
		if _, err := saveGoalSettingsFileConfig(dir, mustParse(t, `{"keybindings":`+kb+`}`)); err != nil {
			t.Fatal(err)
		}
		settingsDeepEqual(t, loadGoalSettingsFileConfig(dir).obj("keybindings"), kb, "round trip")
	})
	tw(t, f, "saveGoalSettingsFileConfig: objectiveMaxChars persists (including 0) and clears", func(t *testing.T) {
		isolateHome(t)
		dir := t.TempDir()
		save := func(raw string) {
			t.Helper()
			if _, err := saveGoalSettingsFileConfig(dir, mustParse(t, raw)); err != nil {
				t.Fatal(err)
			}
		}
		save(`{"objectiveMaxChars":5000}`)
		eq(t, layerLeaf(loadGoalSettingsFileConfig(dir), "objectiveMaxChars"), 5000.0, "persisted value round-trips")
		save(`{"objectiveMaxChars":0}`)
		eq(t, layerLeaf(loadGoalSettingsFileConfig(dir), "objectiveMaxChars"), 0.0, "0 (no limit) persists explicitly")
		save(`{}`)
		eq(t, layerLeaf(loadGoalSettingsFileConfig(dir), "objectiveMaxChars"), nil, "cleared when omitted")
	})
	tw(t, f, "effectiveSettingsReport: objectiveMaxChars row shows the effective value and provenance", func(t *testing.T) {
		isolateHome(t)
		dir := t.TempDir()
		settingsWrite(t, goalSettingsPath(dir), `{"objectiveMaxChars":3000}`)
		var row string
		for _, l := range effectiveSettingsReport(dir, senv{}) {
			if strings.HasPrefix(l, "  max objective length") {
				row = l
			}
		}
		if row == "" || !strings.Contains(row, "3000 (project)") {
			t.Errorf("row = %q", row)
		}
	})

	// ── Integration: prompt suppression with settings (functions owned by GoalPrompts) ──
	withTasks := func() *jsObject {
		g := createGoal("Test goal", true, false, false, 0)
		g.set("taskList", settingsPJ(t, `{"tasks":[{"id":"t1","title":"Task 1","status":"pending"}],"blockCompletion":false,"proposedAt":"2026-05-27T00:00:00.000Z"}`))
		return g
	}
	withContract := func() *jsObject {
		g := createGoal("Test goal", true, false, false, 0)
		g.set("verificationContract", "Must verify X")
		return g
	}
	tw(t, f, "taskListBlock: suppressed when disableTasks is true", func(t *testing.T) {
		defer settingsStubGuard(t)
		eq(t, taskListBlock(withTasks(), &goalSettings{disableTasks: true}), "", "should be empty when tasks disabled")
	})
	tw(t, f, "taskListBlock: present when disableTasks is false", func(t *testing.T) {
		defer settingsStubGuard(t)
		if !strings.Contains(taskListBlock(withTasks(), &goalSettings{disableTasks: false}), "Task 1") {
			t.Error("should contain task when tasks enabled")
		}
	})
	tw(t, f, "taskListBlock: not suppressed when settings is undefined (backward compat)", func(t *testing.T) {
		defer settingsStubGuard(t)
		if !strings.Contains(taskListBlock(withTasks(), nil), "Task 1") {
			t.Error("should contain task when no settings")
		}
	})
	tw(t, f, "verificationContractBlock: suppressed when disableContracts is true", func(t *testing.T) {
		defer settingsStubGuard(t)
		eq(t, verificationContractBlock(withContract(), &goalSettings{disableContracts: true}), "", "should be empty when contracts disabled")
	})
	tw(t, f, "verificationContractBlock: present when disableContracts is false", func(t *testing.T) {
		defer settingsStubGuard(t)
		if !strings.Contains(verificationContractBlock(withContract(), &goalSettings{disableContracts: false}), "Must verify X") {
			t.Error("should contain contract when contracts enabled")
		}
	})
	tw(t, f, "verificationContractBlock: not suppressed when settings is undefined (backward compat)", func(t *testing.T) {
		defer settingsStubGuard(t)
		if !strings.Contains(verificationContractBlock(withContract(), nil), "Must verify X") {
			t.Error("should contain contract when no settings")
		}
	})
	tw(t, f, "goalPrompt: contract block suppressed when disableContracts is true", func(t *testing.T) {
		defer settingsStubGuard(t)
		if strings.Contains(goalPrompt(withContract(), &goalSettings{disableContracts: true}), "VERIFICATION CONTRACT") {
			t.Error("contract section suppressed from goalPrompt")
		}
	})
	tw(t, f, "goalPrompt: task list suppressed when disableTasks is true", func(t *testing.T) {
		defer settingsStubGuard(t)
		if strings.Contains(goalPrompt(withTasks(), &goalSettings{disableTasks: true}), "TASK LIST") {
			t.Error("task list suppressed from goalPrompt")
		}
	})
	tw(t, f, "goalPrompt: contract block shown when settings undefined (backward compat)", func(t *testing.T) {
		defer settingsStubGuard(t)
		if !strings.Contains(goalPrompt(withContract(), nil), "VERIFICATION CONTRACT") {
			t.Error("contract shown when no settings")
		}
	})
	tw(t, f, "continuationPrompt: contract block suppressed when disableContracts is true", func(t *testing.T) {
		defer settingsStubGuard(t)
		if strings.Contains(continuationPrompt(withContract(), &goalSettings{disableContracts: true}), "VERIFICATION CONTRACT") {
			t.Error("contract section suppressed from continuationPrompt")
		}
	})
	tw(t, f, "continuationPrompt: task list suppressed when disableTasks is true", func(t *testing.T) {
		defer settingsStubGuard(t)
		if strings.Contains(continuationPrompt(withTasks(), &goalSettings{disableTasks: true}), "TASK LIST") {
			t.Error("task list suppressed from continuationPrompt")
		}
	})

	// ── subtaskDepth ──
	tw(t, f, "parseGoalSettings: parses subtaskDepth as number", func(t *testing.T) {
		eq(t, layerLeaf(mustParse(t, `{"disableTasks":false,"disableContracts":false,"subtaskDepth":2}`), "subtaskDepth"), 2.0, "subtaskDepth")
	})
	tw(t, f, "parseGoalSettings: parses subtaskDepth string", func(t *testing.T) {
		eq(t, layerLeaf(mustParse(t, `{"disableTasks":false,"disableContracts":false,"subtaskDepth":"3"}`), "subtaskDepth"), 3.0, "subtaskDepth")
	})
	tw(t, f, "parseGoalSettings: rejects subtaskDepth below 1", func(t *testing.T) {
		eq(t, layerLeaf(mustParse(t, `{"disableTasks":false,"disableContracts":false,"subtaskDepth":0}`), "subtaskDepth"), nil, "subtaskDepth")
	})
	tw(t, f, "parseGoalSettings: rejects non-numeric subtaskDepth", func(t *testing.T) {
		eq(t, layerLeaf(mustParse(t, `{"disableTasks":false,"disableContracts":false,"subtaskDepth":"abc"}`), "subtaskDepth"), nil, "subtaskDepth")
	})
	tw(t, f, "loadGoalSettings: default subtaskDepth is 1", func(t *testing.T) {
		isolateHome(t)
		eq(t, loadGoalSettings(t.TempDir(), senv{}).subtaskDepth, 1.0, "subtaskDepth")
	})
	tw(t, f, "loadGoalSettings: reads subtaskDepth from file", func(t *testing.T) {
		isolateHome(t)
		dir := t.TempDir()
		settingsWrite(t, goalSettingsPath(dir), `{"disableTasks":false,"disableContracts":false,"subtaskDepth":3}`)
		eq(t, loadGoalSettings(dir, senv{}).subtaskDepth, 3.0, "subtaskDepth")
	})
	tw(t, f, "loadGoalSettings respects various subtaskDepth edge cases", func(t *testing.T) {
		isolateHome(t)
		dir := t.TempDir()
		eq(t, loadGoalSettings(dir, senv{}).subtaskDepth, 1.0, "no file = default 1")
		file := goalSettingsPath(dir)
		for _, depth := range []string{"0", "1.5", "-1"} {
			invalidateGoalSettingsCache() // the original keeps the first (missing) read cached and passes vacuously; dropping it makes each case real
			settingsWrite(t, file, `{"disableTasks":false,"disableContracts":false,"subtaskDepth":`+depth+`}`)
			eq(t, loadGoalSettings(dir, senv{}).subtaskDepth, 1.0, "subtaskDepth "+depth+" rejected")
		}
	})
	tw(t, f, "normalizeTaskList handles subtasks", func(t *testing.T) {
		raw := settingsPJ(t, `{"tasks":[{"id":"t1","title":"Parent","status":"pending","subtasks":[{"id":"t1a","title":"Child","status":"pending"}]}],"blockCompletion":false,"proposedAt":"2026-05-27T00:00:00.000Z"}`)
		result := normalizeTaskList(raw)
		if result == nil {
			t.Fatal("nil task list")
		}
		tasks := result.vals["tasks"].([]any)
		eq(t, len(tasks), 1, "tasks")
		subs, ok := tasks[0].(*jsObject).vals["subtasks"].([]any)
		if !ok {
			t.Fatal("no subtasks")
		}
		child := subs[0].(*jsObject)
		eq(t, gstr(child, "id"), "t1a", "id")
		eq(t, gstr(child, "title"), "Child", "title")

		item := normalizeTaskItem(settingsPJ(t, `{"id":"x","title":"X","status":"pending","subtasks":[{"id":"xa","title":"XA","status":"complete"}]}`).(*jsObject))
		if item == nil {
			t.Fatal("nil item")
		}
		xs := item.vals["subtasks"].([]any)
		eq(t, len(xs), 1, "subtasks")
		eq(t, gstr(xs[0].(*jsObject), "id"), "xa", "id")
		eq(t, gstr(xs[0].(*jsObject), "status"), "complete", "status")
	})
	tw(t, f, "normalizeTaskItem preserves lightweightSubtasks flag", func(t *testing.T) {
		item := normalizeTaskItem(settingsPJ(t, `{"id":"t1","title":"T1","status":"pending","lightweightSubtasks":true,"subtasks":[{"id":"t1a","title":"A","status":"pending"}]}`).(*jsObject))
		if item == nil {
			t.Fatal("nil item")
		}
		eq(t, item.vals["lightweightSubtasks"], true, "lightweightSubtasks")
		eq(t, len(item.vals["subtasks"].([]any)), 1, "subtasks")
	})
}
