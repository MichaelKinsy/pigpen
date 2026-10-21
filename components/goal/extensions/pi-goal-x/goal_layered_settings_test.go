package pi_goal_x

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Tests ported from pi-goal-x tests/goal-layered-settings.test.ts.

func layeredDir(t *testing.T) string {
	t.Helper()
	isolateHome(t)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func hasDiag(diags []settingsDiagnostic, code string, scope settingsScope, settingPath string) bool {
	for _, d := range diags {
		if d.code == code && (scope == "" || d.scope == scope) && d.settingPath == settingPath {
			return true
		}
	}
	return false
}

func TestLayeredSettingsTwins(t *testing.T) {
	const f = "goal-layered-settings"
	tw(t, f, "global path defaults to ~/.pi/agent/pi-goal-x-settings.json", func(t *testing.T) {
		eq(t, goalGlobalSettingsPath(senv{}, "/home/u"), filepath.Join("/home/u", ".pi", "agent", "pi-goal-x-settings.json"), "default")
	})
	tw(t, f, "PI_CODING_AGENT_DIR overrides the agent dir (absolute and home-relative)", func(t *testing.T) {
		eq(t, resolveAgentDir(senv{"PI_CODING_AGENT_DIR": "/opt/agent"}, "/home/u"), "/opt/agent", "absolute")
		eq(t, goalGlobalSettingsPath(senv{"PI_CODING_AGENT_DIR": "agent-rel"}, "/home/u"), filepath.Join("/home/u", "agent-rel", "pi-goal-x-settings.json"), "home-relative")
	})
	tw(t, f, "PI_GOAL_GLOBAL_SETTINGS_FILE overrides the whole path (absolute and home-relative)", func(t *testing.T) {
		eq(t, goalGlobalSettingsPath(senv{"PI_GOAL_GLOBAL_SETTINGS_FILE": "/etc/g.json"}, "/home/u"), "/etc/g.json", "absolute")
		eq(t, goalGlobalSettingsPath(senv{"PI_GOAL_GLOBAL_SETTINGS_FILE": "g.json"}, "/home/u"), filepath.Join("/home/u", "g.json"), "home-relative")
	})
	tw(t, f, "project path honors PI_GOAL_SETTINGS_FILE relative to cwd", func(t *testing.T) {
		eq(t, goalSettingsPath("/tmp/p", senv{"PI_GOAL_SETTINGS_FILE": "custom.json"}), filepath.Join("/tmp/p", "custom.json"), "relative")
	})

	projectFile := func(dir string) string { return filepath.Join(dir, ".pi", "pi-goal-x-settings.json") }

	tw(t, f, "no global file behaves identically to project-only resolution", func(t *testing.T) {
		dir := layeredDir(t)
		env := senv{"PI_GOAL_GLOBAL_SETTINGS_FILE": filepath.Join(dir, "no-global.json")}
		settingsWrite(t, projectFile(dir), `{"disableTasks":true}`)
		invalidateGoalSettingsCache()
		eq(t, loadGoalSettings(dir, env).disableTasks, true, "disableTasks")
		eq(t, loadSettingsSnapshot(dir, env).global.status, "missing", "global status")
	})
	tw(t, f, "global-only setting applies when project absent", func(t *testing.T) {
		dir := layeredDir(t)
		globalFile := filepath.Join(dir, "global.json")
		settingsWrite(t, globalFile, `{"subtaskDepth":4}`)
		invalidateGoalSettingsCache()
		eq(t, loadGoalSettings(dir, senv{"PI_GOAL_GLOBAL_SETTINGS_FILE": globalFile}).subtaskDepth, 4.0, "subtaskDepth")
	})
	tw(t, f, "project overrides global for booleans, integers, strings", func(t *testing.T) {
		dir := layeredDir(t)
		env := senv{"PI_GOAL_GLOBAL_SETTINGS_FILE": filepath.Join(dir, "g.json")}
		settingsWrite(t, filepath.Join(dir, "g.json"), `{"disableTasks":true,"objectiveMaxChars":5000,"provider":"globalprov"}`)
		settingsWrite(t, projectFile(dir), `{"disableTasks":false,"objectiveMaxChars":0,"provider":"projprov"}`)
		invalidateGoalSettingsCache()
		s := loadGoalSettings(dir, env)
		eq(t, s.disableTasks, false, "explicit project false overrides global true")
		eq(t, s.objectiveMaxChars, 0.0, "explicit project zero overrides global positive")
		eq(t, s.provider, "projprov", "provider")
	})
	tw(t, f, "environment overrides both layers", func(t *testing.T) {
		dir := layeredDir(t)
		env := senv{"PI_GOAL_GLOBAL_SETTINGS_FILE": filepath.Join(dir, "g.json"), "PI_GOAL_DISABLE_TASKS": "true", "PI_GOAL_OBJECTIVE_MAX_CHARS": "99"}
		settingsWrite(t, filepath.Join(dir, "g.json"), `{"disableTasks":false}`)
		settingsWrite(t, projectFile(dir), `{"disableTasks":false}`)
		invalidateGoalSettingsCache()
		s := loadGoalSettings(dir, env)
		eq(t, s.disableTasks, true, "disableTasks")
		eq(t, s.objectiveMaxChars, 99.0, "objectiveMaxChars")
	})
	tw(t, f, "nested keybindings inherit per leaf; partial override does not reset other leaves", func(t *testing.T) {
		dir := layeredDir(t)
		env := senv{"PI_GOAL_GLOBAL_SETTINGS_FILE": filepath.Join(dir, "g.json")}
		settingsWrite(t, filepath.Join(dir, "g.json"), `{"keybindings":{"dashboard":{"toggleExpand":"ctrl+g","scrollUp":"ctrl+up"}}}`)
		settingsWrite(t, projectFile(dir), `{"keybindings":{"dashboard":{"toggleExpand":"ctrl+p"}}}`)
		invalidateGoalSettingsCache()
		kb := loadGoalSettings(dir, env).keybindings.dashboard
		eq(t, kb.toggleExpand, "ctrl+p", "project leaf wins")
		eq(t, kb.scrollUp, "ctrl+up", "unset project leaf inherits global")
		eq(t, kb.scrollDown, "ctrl+shift+down", "missing everywhere falls back to default")
	})
	tw(t, f, "provenance records the source of every leaf", func(t *testing.T) {
		dir := layeredDir(t)
		env := senv{"PI_GOAL_GLOBAL_SETTINGS_FILE": filepath.Join(dir, "g.json"), "PI_GOAL_DISABLE_CONTRACTS": "false"}
		settingsWrite(t, filepath.Join(dir, "g.json"), `{"disableContracts":true,"subtaskDepth":2}`)
		settingsWrite(t, projectFile(dir), `{"subtaskDepth":5}`)
		invalidateGoalSettingsCache()
		prov := loadSettingsSnapshot(dir, env).provenance
		eq(t, prov["disableContracts"].source, "environment", "disableContracts source")
		eq(t, prov["disableContracts"].envVar, "PI_GOAL_DISABLE_CONTRACTS", "disableContracts envVar")
		eq(t, prov["subtaskDepth"].source, "project", "subtaskDepth source")
		eq(t, prov["autoSelectSingleGoal"].source, "default", "autoSelectSingleGoal source")
		eq(t, prov["keybindings.dashboard.toggleExpand"].source, "default", "toggleExpand source")
	})
	tw(t, f, "unknown keys produce diagnostics while known values still apply", func(t *testing.T) {
		dir := layeredDir(t)
		env := senv{"PI_GOAL_GLOBAL_SETTINGS_FILE": filepath.Join(dir, "g.json")}
		settingsWrite(t, projectFile(dir), `{"disableTasks":true,"totallyUnknown":1}`)
		invalidateGoalSettingsCache()
		snap := loadSettingsSnapshot(dir, env)
		eq(t, snap.value.disableTasks, true, "valid known key still loads")
		if !hasDiag(snap.diagnostics, "unknown_key", settingsScopeProject, "totallyUnknown") {
			t.Errorf("no diagnostic: %+v", snap.diagnostics)
		}
	})
	tw(t, f, "malformed global with valid project still resolves the project layer", func(t *testing.T) {
		dir := layeredDir(t)
		env := senv{"PI_GOAL_GLOBAL_SETTINGS_FILE": filepath.Join(dir, "g.json")}
		settingsWrite(t, filepath.Join(dir, "g.json"), "{not json")
		settingsWrite(t, projectFile(dir), `{"disableContracts":true}`)
		invalidateGoalSettingsCache()
		snap := loadSettingsSnapshot(dir, env)
		eq(t, snap.global.status, "invalid", "global status")
		eq(t, snap.value.disableContracts, true, "disableContracts")
	})
	tw(t, f, "invalid values are diagnosed without erasing sibling keys", func(t *testing.T) {
		dir := layeredDir(t)
		env := senv{"PI_GOAL_GLOBAL_SETTINGS_FILE": filepath.Join(dir, "nonexistent-g.json")}
		settingsWrite(t, projectFile(dir), `{"subtaskDepth":-3,"provider":"ok"}`)
		invalidateGoalSettingsCache()
		snap := loadSettingsSnapshot(dir, env)
		eq(t, snap.project.status, "invalid", "project status")
		eq(t, snap.value.provider, "ok", "provider")
		if !hasDiag(snap.diagnostics, "invalid_value", "", "subtaskDepth") {
			t.Errorf("no diagnostic: %+v", snap.diagnostics)
		}
	})

	scopeTarget := func(dir string, scope settingsScope) string {
		if scope == settingsScopeGlobal {
			return filepath.Join(dir, "g.json")
		}
		return projectFile(dir)
	}
	setPath := func(scope settingsScope, dir string, env senv, op string, value any, path ...string) (*settingsSnapshot, error) {
		return mutateSettingsLayer(settingsMutateInput{scope: scope, cwd: dir, env: env, mutation: settingsMutation{op: op, path: path, value: value}})
	}

	tw(t, f, "set/unset round trip restores inheritance and returns a fresh snapshot", func(t *testing.T) {
		dir := layeredDir(t)
		env := senv{"PI_GOAL_GLOBAL_SETTINGS_FILE": filepath.Join(dir, "g.json")}
		settingsWrite(t, filepath.Join(dir, "g.json"), `{"disableTasks":true}`)
		invalidateGoalSettingsCache()
		if _, err := setPath(settingsScopeProject, dir, env, "set", false, "disableTasks"); err != nil {
			t.Fatal(err)
		}
		snap := loadSettingsSnapshot(dir, env)
		eq(t, snap.value.disableTasks, false, "after set")
		eq(t, snap.provenance["disableTasks"].source, "project", "source after set")
		if _, err := setPath(settingsScopeProject, dir, env, "unset", nil, "disableTasks"); err != nil {
			t.Fatal(err)
		}
		snap = loadSettingsSnapshot(dir, env)
		eq(t, snap.value.disableTasks, true, "unset restores inheritance")
		eq(t, snap.provenance["disableTasks"].source, "global", "source after unset")
	})
	tw(t, f, "refuses to overwrite an invalid layer without explicit repair", func(t *testing.T) {
		dir := layeredDir(t)
		env := senv{"PI_GOAL_GLOBAL_SETTINGS_FILE": filepath.Join(dir, "g.json")}
		settingsWrite(t, scopeTarget(dir, settingsScopeGlobal), "{broken")
		invalidateGoalSettingsCache()
		_, err := setPath(settingsScopeGlobal, dir, env, "set", "x", "provider")
		var me *settingsMutationError
		if !errors.As(err, &me) || !strings.Contains(me.message, "Refusing to overwrite invalid global settings") {
			t.Errorf("err = %v", err)
		}
		data, _ := os.ReadFile(scopeTarget(dir, settingsScopeGlobal))
		if !strings.Contains(string(data), "{broken") {
			t.Errorf("file changed: %s", data)
		}
	})
	tw(t, f, "stale lock (dead pid) is recovered and the mutation succeeds", func(t *testing.T) {
		dir := layeredDir(t)
		env := senv{"PI_GOAL_GLOBAL_SETTINGS_FILE": filepath.Join(dir, "g.json")}
		target := scopeTarget(dir, settingsScopeGlobal)
		settingsWrite(t, target, `{}`)
		started := isoFromMs(time.Now().UnixMilli() - 60000)
		settingsWrite(t, target+".lock", `{"pid":999999999,"startedAt":"`+started+`"}`)
		invalidateGoalSettingsCache()
		if _, err := setPath(settingsScopeGlobal, dir, env, "set", "m", "model"); err != nil {
			t.Fatal(err)
		}
		eq(t, layerLeaf(readSettingsLayer(target, settingsScopeGlobal).layer, "model"), "m", "model")
	})
	tw(t, f, "failed write preserves the original file (fault injection)", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("a read-only directory does not stop root; the fault cannot be injected")
		}
		dir := layeredDir(t)
		env := senv{"PI_GOAL_GLOBAL_SETTINGS_FILE": filepath.Join(dir, "g.json")}
		target := scopeTarget(dir, settingsScopeGlobal)
		settingsWrite(t, target, `{"provider":"keep"}`)
		invalidateGoalSettingsCache()
		// Make the directory read-only AFTER the original file exists so the temp-file creation fails.
		if err := os.Chmod(dir, 0o555); err != nil {
			t.Fatal(err)
		}
		_, err := setPath(settingsScopeGlobal, dir, env, "set", "changed", "provider")
		_ = os.Chmod(dir, 0o755)
		if err == nil {
			t.Error("the mutation must fail")
		}
		invalidateGoalSettingsCache()
		eq(t, layerLeaf(readSettingsLayer(target, settingsScopeGlobal).layer, "provider"), "keep", "original preserved after failed write")
		data, _ := os.ReadFile(target)
		eq(t, settingsPJ(t, string(data)).(*jsObject).vals["provider"], "keep", "file content")
	})
	tw(t, f, "refuses a symlink target", func(t *testing.T) {
		dir := layeredDir(t)
		env := senv{"PI_GOAL_GLOBAL_SETTINGS_FILE": filepath.Join(dir, "g.json")}
		real, link := filepath.Join(dir, "real.json"), filepath.Join(dir, "g.json")
		settingsWrite(t, real, `{}`)
		if err := os.Symlink(real, link); err != nil {
			t.Fatal(err)
		}
		invalidateGoalSettingsCache()
		_, err := setPath(settingsScopeGlobal, dir, env, "set", "m", "model")
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "symlink") {
			t.Errorf("err = %v", err)
		}
	})
	tw(t, f, "external edits to either layer are picked up after cache invalidation with separate fingerprints", func(t *testing.T) {
		dir := layeredDir(t)
		env := senv{"PI_GOAL_GLOBAL_SETTINGS_FILE": filepath.Join(dir, "g.json")}
		settingsWrite(t, filepath.Join(dir, "g.json"), `{"model":"one"}`)
		settingsWrite(t, projectFile(dir), `{"provider":"p1"}`)
		invalidateGoalSettingsCache()
		before := loadSettingsSnapshot(dir, env)
		if before.global.fingerprint == before.project.fingerprint {
			t.Error("the layers must have separate fingerprints")
		}
		settingsWrite(t, filepath.Join(dir, "g.json"), `{"model":"two"}`)
		invalidateGoalSettingsCache()
		after := loadSettingsSnapshot(dir, env)
		eq(t, after.value.model, "two", "model")
		eq(t, after.project.fingerprint, before.project.fingerprint, "untouched layer keeps its fingerprint")
	})
}
