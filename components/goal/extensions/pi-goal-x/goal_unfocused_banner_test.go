package pi_goal_x

import (
	"os"
	"path/filepath"
	"testing"
)

// Twins of tests/goal-unfocused-banner.test.ts (layered hideUnfocusedBanner / hideUnfocusedPrompt with live refresh, and
// the model-facing [PI GOAL UNFOCUSED] safety invariant). The original passes env maps; here the same variables are set in
// the process environment, which the settings loader reads through the same paths.

func ubWriteJSON(t *testing.T, file string, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(value), 0o644); err != nil {
		t.Fatal(err)
	}
}

func ubEnv(t *testing.T, dir string) map[string]string {
	t.Helper()
	env := map[string]string{"PI_GOAL_GLOBAL_SETTINGS_FILE": filepath.Join(dir, "g.json")}
	t.Setenv("PI_GOAL_GLOBAL_SETTINGS_FILE", env["PI_GOAL_GLOBAL_SETTINGS_FILE"])
	t.Setenv("PI_GOAL_SETTINGS_FILE", "")
	return env
}

func ubDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestGoalUnfocusedBanner(t *testing.T) {
	const f = "goal-unfocused-banner"

	tw(t, f, "parses true/false booleans (and string forms); default is false", func(t *testing.T) {
		layer, _ := parseSettingsLayer(parseAny(t, `{"hideUnfocusedBanner":true}`), settingsScopeProject, "t.json")
		v, _ := layer.get("hideUnfocusedBanner")
		assertEq(t, v, any(true), "true parses")
		layer, _ = parseSettingsLayer(parseAny(t, `{"hideUnfocusedBanner":"false"}`), settingsScopeProject, "t.json")
		v, _ = layer.get("hideUnfocusedBanner")
		assertEq(t, v, any(false), "string false parses")
		dir := ubDir(t)
		invalidateGoalSettingsCache()
		t.Setenv("PI_GOAL_GLOBAL_SETTINGS_FILE", filepath.Join(dir, "none.json"))
		assertEq(t, loadGoalSettings(dir).hideUnfocusedBanner, false, "default false")
	})

	tw(t, f, "layering: global true hides; project false overrides and shows; project true over global false hides", func(t *testing.T) {
		dir := ubDir(t)
		env := ubEnv(t, dir)
		projectFile := filepath.Join(dir, ".pi", "pi-goal-x-settings.json")

		ubWriteJSON(t, projectFile, `{}`)
		invalidateGoalSettingsCache()
		assertEq(t, loadGoalSettings(dir).hideUnfocusedBanner, false, "absent + absent -> visible")

		ubWriteJSON(t, filepath.Join(dir, "g.json"), `{"hideUnfocusedBanner":true}`)
		invalidateGoalSettingsCache()
		assertEq(t, loadGoalSettings(dir).hideUnfocusedBanner, true, "global true -> hidden")

		ubWriteJSON(t, projectFile, `{"hideUnfocusedBanner":false}`)
		invalidateGoalSettingsCache()
		assertEq(t, loadGoalSettings(dir).hideUnfocusedBanner, false, "project false overrides global true -> visible")

		ubWriteJSON(t, filepath.Join(dir, "g.json"), `{"hideUnfocusedBanner":false}`)
		ubWriteJSON(t, projectFile, `{"hideUnfocusedBanner":true}`)
		invalidateGoalSettingsCache()
		assertEq(t, loadGoalSettings(dir).hideUnfocusedBanner, true, "project true over global false -> hidden")

		snap := loadSettingsSnapshot(dir, env)
		assertEq(t, snap.provenance["hideUnfocusedBanner"].source, "project", "provenance")
	})

	tw(t, f, "save/unset round trip through the scoped mutation API", func(t *testing.T) {
		dir := ubDir(t)
		env := ubEnv(t, dir)
		if _, err := mutateSettingsLayer(settingsMutateInput{scope: settingsScopeProject, cwd: dir, env: env, mutation: settingsMutation{op: "set", path: []string{"hideUnfocusedBanner"}, value: true}}); err != nil {
			t.Fatal(err)
		}
		assertEq(t, loadGoalSettings(dir).hideUnfocusedBanner, true, "set")
		if _, err := mutateSettingsLayer(settingsMutateInput{scope: settingsScopeProject, cwd: dir, env: env, mutation: settingsMutation{op: "unset", path: []string{"hideUnfocusedBanner"}}}); err != nil {
			t.Fatal(err)
		}
		assertEq(t, loadGoalSettings(dir).hideUnfocusedBanner, false, "unset returns to inherited/default")
	})

	tw(t, f, "parses true/false booleans (and string forms); default is false", func(t *testing.T) {
		layer, _ := parseSettingsLayer(parseAny(t, `{"hideUnfocusedPrompt":true}`), settingsScopeProject, "t.json")
		v, _ := layer.get("hideUnfocusedPrompt")
		assertEq(t, v, any(true), "true parses")
		layer, _ = parseSettingsLayer(parseAny(t, `{"hideUnfocusedPrompt":"false"}`), settingsScopeProject, "t.json")
		v, _ = layer.get("hideUnfocusedPrompt")
		assertEq(t, v, any(false), "string false parses")
		invalid, diagnostics := parseSettingsLayer(parseAny(t, `{"hideUnfocusedPrompt":"sometimes"}`), settingsScopeProject, "t.json")
		assertTrue(t, !invalid.has("hideUnfocusedPrompt"), "invalid value is dropped")
		found := false
		for _, d := range diagnostics {
			if d.settingPath == "hideUnfocusedPrompt" && d.code == "invalid_value" {
				found = true
			}
		}
		assertTrue(t, found, "invalid_value diagnostic")
		dir := ubDir(t)
		invalidateGoalSettingsCache()
		t.Setenv("PI_GOAL_GLOBAL_SETTINGS_FILE", filepath.Join(dir, "none.json"))
		assertEq(t, loadGoalSettings(dir).hideUnfocusedPrompt, false, "default false")
	})

	tw(t, f, "layers project > global > default and resolves independently of hideUnfocusedBanner", func(t *testing.T) {
		dir := ubDir(t)
		env := ubEnv(t, dir)
		projectFile := filepath.Join(dir, ".pi", "pi-goal-x-settings.json")

		ubWriteJSON(t, filepath.Join(dir, "g.json"), `{"hideUnfocusedPrompt":true}`)
		ubWriteJSON(t, projectFile, `{}`)
		invalidateGoalSettingsCache()
		assertEq(t, loadGoalSettings(dir).hideUnfocusedPrompt, true, "global true -> hidden")

		ubWriteJSON(t, projectFile, `{"hideUnfocusedPrompt":false}`)
		invalidateGoalSettingsCache()
		assertEq(t, loadGoalSettings(dir).hideUnfocusedPrompt, false, "project false overrides global true")

		ubWriteJSON(t, filepath.Join(dir, "g.json"), `{"hideUnfocusedBanner":true}`)
		ubWriteJSON(t, projectFile, `{"hideUnfocusedPrompt":true}`)
		invalidateGoalSettingsCache()
		resolved := loadGoalSettings(dir)
		assertEq(t, resolved.hideUnfocusedPrompt, true, "project prompt true")
		assertEq(t, resolved.hideUnfocusedBanner, true, "banner layer is untouched by the prompt setting")

		ubWriteJSON(t, filepath.Join(dir, "g.json"), `{"hideUnfocusedBanner":true}`)
		ubWriteJSON(t, projectFile, `{"hideUnfocusedBanner":false}`)
		invalidateGoalSettingsCache()
		independent := loadGoalSettings(dir)
		assertEq(t, independent.hideUnfocusedBanner, false, "banner project false")
		assertEq(t, independent.hideUnfocusedPrompt, false, "banner layers never set the prompt setting")
		snap := loadSettingsSnapshot(dir, env)
		assertEq(t, snap.provenance["hideUnfocusedPrompt"].source, "default", "provenance")
	})

	tw(t, f, "save/unset round trip through the scoped mutation API", func(t *testing.T) {
		dir := ubDir(t)
		env := ubEnv(t, dir)
		if _, err := mutateSettingsLayer(settingsMutateInput{scope: settingsScopeProject, cwd: dir, env: env, mutation: settingsMutation{op: "set", path: []string{"hideUnfocusedPrompt"}, value: true}}); err != nil {
			t.Fatal(err)
		}
		assertEq(t, loadGoalSettings(dir).hideUnfocusedPrompt, true, "set")
		assertEq(t, loadGoalSettings(dir).hideUnfocusedBanner, false, "setting the prompt flag leaves the banner flag alone")
		if _, err := mutateSettingsLayer(settingsMutateInput{scope: settingsScopeProject, cwd: dir, env: env, mutation: settingsMutation{op: "unset", path: []string{"hideUnfocusedPrompt"}}}); err != nil {
			t.Fatal(err)
		}
		assertEq(t, loadGoalSettings(dir).hideUnfocusedPrompt, false, "unset returns to inherited/default")
	})

	tw(t, f, "[PI GOAL UNFOCUSED] prompt text is unchanged by the banner setting", func(t *testing.T) {
		prompt := unfocusedOpenGoalsPrompt(2)
		assertMatch(t, prompt, `\[PI GOAL UNFOCUSED\]`)
		assertMatch(t, prompt, `Do not choose or switch focus autonomously`)
		assertMatch(t, prompt, `Ask the user to run /goal-focus`)
		// The prompt builder takes only the open-goal count — the setting cannot reach it structurally.
		assertEq(t, unfocusedOpenGoalsPrompt(1), unfocusedOpenGoalsPrompt(1), "deterministic")
	})
}
