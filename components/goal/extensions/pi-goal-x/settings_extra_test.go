package pi_goal_x

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Unit checks of goal-settings.ts behavior that no upstream test pins (the port's own, beyond the twins).

func TestSettingsMutationFileFormat(t *testing.T) {
	dir := layeredDir(t)
	env := senv{piGoalGlobalSettingsFileEnv: filepath.Join(dir, "g.json")}
	target := filepath.Join(dir, "g.json")
	mut := func(op string, value any, path ...string) error {
		_, err := mutateSettingsLayer(settingsMutateInput{scope: settingsScopeGlobal, cwd: dir, env: env, mutation: settingsMutation{op: op, path: path, value: value}})
		return err
	}
	settingsWrite(t, target, `{"provider":"p","thinkingLevel":"low","model":"m"}`)
	if err := mut("set", "x", "model"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(target)
	// the layer is rewritten from its parsed form: the alias moves to the end under its persisted spelling
	eq(t, string(data), "{\n  \"provider\": \"p\",\n  \"model\": \"x\",\n  \"thinking_level\": \"low\"\n}\n", "file bytes")
	if fi, _ := os.Stat(target); fi.Mode().Perm() != 0o644 {
		t.Errorf("an existing file keeps its mode, got %v", fi.Mode().Perm())
	}
	// nested set, then unset prunes the emptied parents; thinkingLevel unset clears both spellings
	if err := mut("set", "ctrl+x", "keybindings", "dashboard", "scrollUp"); err != nil {
		t.Fatal(err)
	}
	if err := mut("unset", nil, "keybindings", "dashboard", "scrollUp"); err != nil {
		t.Fatal(err)
	}
	if err := mut("unset", nil, "thinking_level"); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(target)
	eq(t, string(data), "{\n  \"provider\": \"p\",\n  \"model\": \"x\"\n}\n", "pruned and cleared")
	if err := mut("set", "high", "thinkingLevel"); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(target)
	if !strings.Contains(string(data), `"thinking_level": "high"`) {
		t.Errorf("alias persisted as thinking_level: %s", data)
	}
	// an invalid result is refused and the file stays
	err := mut("set", "loud", "thinkingLevel")
	var me *settingsMutationError
	if err == nil || !strings.Contains(err.Error(), "Mutation produced invalid global settings") {
		t.Fatalf("err = %v", err)
	}
	_ = me
	if err := mut("set", 1.0); err == nil || err.Error() != "empty settings path" {
		t.Errorf("empty path: %v", err)
	}
	// a new file gets mode 0600 and no lock is left behind
	fresh := senv{piGoalGlobalSettingsFileEnv: filepath.Join(dir, "new", "n.json")}
	if _, err := mutateSettingsLayer(settingsMutateInput{scope: settingsScopeGlobal, cwd: dir, env: fresh, mutation: settingsMutation{op: "set", path: []string{"model"}, value: "m"}}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, "new", "n.json"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("new file mode: %v %v", fi, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "new", "n.json.lock")); err == nil {
		t.Error("lock left behind")
	}
}

func TestSettingsLockTimeoutAndLiveHolder(t *testing.T) {
	dir := layeredDir(t)
	lockPath := filepath.Join(dir, "x.lock")
	settingsWrite(t, lockPath, `{"pid":`+jsNumber(float64(os.Getpid()))+`}`)
	_, err := acquirePathLock(lockPath, pathLockOptions{attempts: 3, retryMs: 1})
	if err == nil || err.Error() != "Timed out acquiring the settings lock at "+lockPath+". Another writer may hold it." {
		t.Errorf("a live holder blocks: %v", err)
	}
	// a non-numeric pid is not "dead"; only the TTL frees it
	settingsWrite(t, lockPath, `{"pid":"x"}`)
	if _, err := acquirePathLock(lockPath, pathLockOptions{attempts: 3, retryMs: 1}); err == nil {
		t.Error("an unreadable pid is not stale before the TTL")
	}
	old := clockMs
	clockMs = func() int64 { return old() + 60000 }
	defer func() { clockMs = old }()
	l, err := acquirePathLock(lockPath, pathLockOptions{attempts: 3, retryMs: 1})
	if err != nil {
		t.Fatalf("stale by TTL: %v", err)
	}
	l.release()
	l.release()
	if _, err := os.Stat(lockPath); err == nil {
		t.Error("released")
	}
}

func TestSettingsSaveAndPersistedForm(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()
	in := mustParse(t, `{"provider":"p","thinkingLevel":"high","disabled":false,"subtaskDepth":2,"goalsRoot":"/g","networkRecovery":{"maxAttempts":2},"oracle":{"enabled":true,"thinkingLevel":"low","maxFailedAttemptsPerBlocker":3},"strictExecutionContract":true,"maxAutonomousRuns":0,"keybindings":{"dashboard":{"scrollUp":"ctrl+u"}}}`)
	out, err := saveGoalSettingsFileConfig(dir, in)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, layerLeaf(out, "thinkingLevel"), "high", "returned shape uses thinkingLevel")
	eq(t, out.has("thinking_level"), false, "no persisted spelling in the result")
	data, _ := os.ReadFile(goalSettingsPath(dir))
	eq(t, string(data), "{\n  \"provider\": \"p\",\n  \"thinking_level\": \"high\",\n  \"disabled\": false,\n  \"subtaskDepth\": 2,\n  \"goalsRoot\": \"/g\",\n  \"networkRecovery\": {\n    \"maxAttempts\": 2\n  },\n  \"oracle\": {\n    \"enabled\": true,\n    \"thinking_level\": \"low\",\n    \"maxFailedAttemptsPerBlocker\": 3\n  },\n  \"strictExecutionContract\": true,\n  \"maxAutonomousRuns\": 0,\n  \"keybindings\": {\n    \"dashboard\": {\n      \"scrollUp\": \"ctrl+u\"\n    }\n  }\n}\n", "persisted bytes")
	fi, _ := os.Stat(goalSettingsPath(dir))
	eq(t, fi.Mode().Perm(), os.FileMode(0o600), "mode")
	// empty strings and zero values for the truthy keys are not persisted
	if _, err := saveGoalSettingsFileConfig(dir, settingsPJ(t, `{"provider":"","model":"","thinkingLevel":""}`).(*jsObject)); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(goalSettingsPath(dir))
	eq(t, string(data), "{}\n", "falsy strings omitted")
	// an invalid project file is never overwritten
	settingsWrite(t, goalSettingsPath(dir), "{nope")
	invalidateGoalSettingsCache()
	if _, err := saveGoalSettingsFileConfig(dir, newObject()); err == nil || !strings.Contains(err.Error(), "Refusing to overwrite invalid project settings at") {
		t.Errorf("err = %v", err)
	}
}

func TestSettingsParseDiagnosticsAndLeaves(t *testing.T) {
	_, d := parseSettingsLayer(settingsPJ(t, `{"disableTasks":"maybe","subtaskDepth":"0","maxAutonomousRuns":"12x","goalsRoot":" ","thinking_level":"loud","stallTimeoutMinutes":-1,"keybindings":{"dashboard":{"scrollUp":""},"other":1},"oracle":{"maxFailedAttemptsPerBlocker":4,"bogus":1,"enabled":"x"},"networkRecovery":{"maxDelayMs":999,"maxAttempts":-1,"bogus":1},"nope":1}`), settingsScopeGlobal, "f")
	want := map[string]string{
		"disableTasks": "disableTasks must be true or false", "subtaskDepth": "subtaskDepth must be an integer >= 1",
		"maxAutonomousRuns": "maxAutonomousRuns must be a nonnegative safe integer (0 disables automatic continuation)",
		"goalsRoot":         "goalsRoot must be a non-empty string", "thinking_level": "thinking_level must be one of: off, minimal, low, medium, high, xhigh",
		"stallTimeoutMinutes":                "stallTimeoutMinutes must be an integer >= 0",
		"keybindings.dashboard.scrollUp":     "scrollUp must be a non-empty key id",
		"keybindings.other":                  "unknown keybindings key: other",
		"oracle.maxFailedAttemptsPerBlocker": "oracle.maxFailedAttemptsPerBlocker must be an integer between 1 and 3",
		"oracle.bogus":                       "unknown oracle key: bogus",
		"oracle.enabled":                     "oracle.enabled must be true or false",
		"networkRecovery.maxDelayMs":         "networkRecovery.maxDelayMs must be an integer >= 1000",
		"networkRecovery.maxAttempts":        "networkRecovery.maxAttempts must be an integer >= 0 (0 = unbounded)",
		"networkRecovery.bogus":              "unknown networkRecovery key: bogus",
		"nope":                               "unknown settings key: nope",
	}
	got := map[string]string{}
	for _, x := range d {
		got[x.settingPath] = x.message
		eq(t, x.scope, settingsScopeGlobal, "scope")
		eq(t, x.path, "f", "path")
	}
	eq(t, got, want, "diagnostics")
	layer, d := parseSettingsLayer(settingsPJ(t, `[1]`), settingsScopeProject, "f")
	eq(t, len(layer.keys), 0, "array layer")
	eq(t, d[0].code, "not_object", "array diagnostic")
	eq(t, d[0].message, "settings file must contain a JSON object", "message")
	layer, _ = parseSettingsLayer(settingsPJ(t, `{"maxAutonomousRuns":" 007 ","provider":"  p  ","thinking_level":"low","oracle":{"thinking_level":"high","provider":" o "},"networkRecovery":{},"keybindings":{"dashboard":{"toggleExpand":" k "}}}`), settingsScopeProject, "f")
	eq(t, marshalJSON(layer, ""), `{"maxAutonomousRuns":7,"provider":"p","thinkingLevel":"low","oracle":{"thinkingLevel":"high","provider":"o"},"keybindings":{"dashboard":{"toggleExpand":"k"}}}`, "layer")
	// numeric strings parse like parseInt
	eq(t, layerLeaf(mustParse(t, `{"subtaskDepth":" +3x"}`), "subtaskDepth"), 3.0, "parseInt prefix")
	eq(t, layerLeaf(mustParse(t, `{"subtaskDepth":9007199254740993}`), "subtaskDepth"), 9007199254740992.0, "large integer")
}

func TestSettingsResolutionDetails(t *testing.T) {
	dir := layeredDir(t)
	g := filepath.Join(dir, "g.json")
	env := senv{piGoalGlobalSettingsFileEnv: g, "PI_GOAL_ROOT": "", "PI_GOAL_NETWORK_RECOVERY_MAX_DELAY_MS": "2500"}
	settingsWrite(t, g, `{"model":"gm","disabled":true,"oracle":{"enabled":true,"provider":"op","maxFailedAttemptsPerBlocker":3},"maxAutonomousRuns":4,"stallTimeoutMinutes":9,"showAutonomousRuns":false}`)
	settingsWrite(t, filepath.Join(dir, ".pi", "pi-goal-x-settings.json"), `{"provider":"pp","hideUnfocusedBanner":true,"hideUnfocusedPrompt":true,"auditorProjectResources":true,"oracle":{"projectResources":true}}`)
	invalidateGoalSettingsCache()
	s := loadSettingsSnapshot(dir, env)
	v := s.value
	eq(t, []string{v.provider, v.model, v.thinkingLevel}, []string{"pp", "gm", ""}, "strings")
	eq(t, []bool{v.disabled, v.hideUnfocusedBanner, v.hideUnfocusedPrompt, v.auditorProjectResources, v.showAutonomousRuns}, []bool{true, true, true, true, false}, "bools")
	eq(t, *v.maxAutonomousRuns, 4.0, "maxAutonomousRuns")
	eq(t, v.stallTimeoutMinutes, 9.0, "stallTimeoutMinutes")
	eq(t, v.oracle, resolvedGoalOracleSettings{enabled: true, provider: "op", projectResources: true, maxFailedAttemptsPerBlocker: 3}, "oracle")
	eq(t, v.networkRecovery.maxDelayMs, 2500.0, "env delay")
	if v.goalsRoot == nil || *v.goalsRoot != "" {
		t.Error("PI_GOAL_ROOT set to the empty string is a defined value")
	}
	eq(t, s.provenance["goalsRoot"].source, "environment", "goalsRoot source")
	eq(t, s.provenance["oracle.provider"].source, "global", "oracle provider source")
	eq(t, s.provenance["oracle.projectResources"].source, "project", "oracle projectResources source")
	eq(t, s.provenance["model"].source, "global", "model source")
	// snapshots are caller-owned: editing one does not change the next
	*s.value.maxAutonomousRuns = 99
	s.provenance["model"].source = "x"
	again := loadSettingsSnapshot(dir, env)
	eq(t, *again.value.maxAutonomousRuns, 4.0, "copy of maxAutonomousRuns")
	eq(t, again.provenance["model"].source, "global", "copy of provenance")
	// both layers' diagnostics, global first
	settingsWrite(t, g, `{"g1":1}`)
	settingsWrite(t, filepath.Join(dir, ".pi", "pi-goal-x-settings.json"), `{"p1":1}`)
	invalidateGoalSettingsCache()
	d := loadSettingsSnapshot(dir, env).diagnostics
	eq(t, []string{d[0].settingPath, d[1].settingPath}, []string{"g1", "p1"}, "diagnostic order")
	// an environment change is seen without invalidating the cache
	eq(t, loadGoalSettings(dir, senv{piGoalGlobalSettingsFileEnv: g, "PI_GOAL_DISABLE_TASKS": "true"}).disableTasks, true, "env a")
	eq(t, loadGoalSettings(dir, senv{piGoalGlobalSettingsFileEnv: g}).disableTasks, false, "env b")
	// a file edit is not seen until the cache is invalidated (zero-op steady state)
	settingsWrite(t, g, `{"disableTasks":true}`)
	eq(t, loadGoalSettings(dir, senv{piGoalGlobalSettingsFileEnv: g}).disableTasks, false, "cached")
	invalidateGoalSettingsCache()
	eq(t, loadGoalSettings(dir, senv{piGoalGlobalSettingsFileEnv: g}).disableTasks, true, "fresh")
	// the process environment is the default
	t.Setenv("PI_GOAL_DISABLE_CONTRACTS", "true")
	eq(t, loadGoalSettings(dir).disableContracts, true, "process env")
	eq(t, loadGoalSettings(dir, senv{}).disableContracts, false, "explicit empty env")
}

func TestSettingsReportAndOverrides(t *testing.T) {
	dir := layeredDir(t)
	env := senv{piGoalGlobalSettingsFileEnv: filepath.Join(dir, "g.json"), "PI_GOAL_DISABLE_TASKS": "true", piGoalSettingsFileEnv: "p.json"}
	settingsWrite(t, filepath.Join(dir, "p.json"), `{"maxAutonomousRuns":0,"provider":"pp","subtaskDepth":2}`)
	lines := effectiveSettingsReport(dir, env)
	eq(t, lines, []string{
		"Settings (provenance):",
		"  autoSelectSingleGoal: false (default)",
		"  disableContracts: false (default)",
		"  disableTasks: true (env (PI_GOAL_DISABLE_TASKS))",
		"  subtaskDepth: 2 (project)",
		"  auditor disabled: false (default)",
		"  provider: pp (project)",
		"  model: (default) (default)",
		"  thinking_level: (default) (default)",
		"  auditor project resources: false (default)",
		"  hide unfocused banner: false (default)",
		"  show autonomous runs line: true (default)",
		"  goals root (reload to change): .pi/goals (default)",
		"  hide unfocused prompt: false (default)",
		"  explicit execution contracts (opt-in): false (default)",
		"  autonomous run allowance: 0 (disabled) (project)",
		"  stall timeout (minutes): 0 (default)",
		"  max objective length (0 = none): 0 (default)",
		"  network recovery attempts (0 = unbounded): 0 (default)",
		"  network recovery max delay (ms): 80000 (default)",
		"  dashboard keybindings: ctrl+shift+t, ctrl+shift+up, ctrl+shift+down (default)",
		"  project settings file: " + filepath.Join(dir, "p.json"),
		"  global settings file: " + filepath.Join(dir, "g.json"),
		"  (settings file overridden by PI_GOAL_SETTINGS_FILE)",
	}, "report")
	settingsWrite(t, filepath.Join(dir, "p.json"), `{"maxAutonomousRuns":5}`)
	invalidateGoalSettingsCache()
	if r := effectiveSettingsReport(dir, senv{piGoalGlobalSettingsFileEnv: filepath.Join(dir, "g.json"), piGoalSettingsFileEnv: "p.json"}); r[15] != "  autonomous run allowance: 5 (project)" {
		t.Errorf("allowance row: %q", r[15])
	}
	eq(t, effectiveSettingsReport(dir, senv{piGoalGlobalSettingsFileEnv: filepath.Join(dir, "g.json"), piGoalSettingsFileEnv: "none.json"})[15], "  autonomous run allowance: unlimited (default) (default)", "unlimited row")

	for _, c := range []struct{ key, name string }{
		{"goalsRoot", "PI_GOAL_ROOT"}, {"disableTasks", "PI_GOAL_DISABLE_TASKS"}, {"disableContracts", "PI_GOAL_DISABLE_CONTRACTS"},
		{"objectiveMaxChars", "PI_GOAL_OBJECTIVE_MAX_CHARS"}, {"networkRecovery", "PI_GOAL_NETWORK_RECOVERY_MAX_ATTEMPTS"},
		{"networkRecovery", "PI_GOAL_NETWORK_RECOVERY_MAX_DELAY_MS"}, {"settingsFile", "PI_GOAL_SETTINGS_FILE"},
	} {
		eq(t, envOverrideFor(c.key, senv{c.name: ""}), c.name, c.key+" set to empty is still an override")
		eq(t, envOverrideFor(c.key, senv{}), "", c.key+" unset")
	}
	eq(t, envOverrideFor("provider", senv{"PI_GOAL_ROOT": "x"}), "", "other key")
	eq(t, isAuditorEnabledByDefault(goalSettings{}), true, "auditor enabled by default")
	eq(t, isAuditorEnabledByDefault(goalSettings{disabled: true}), false, "auditor disabled")
}

func TestSettingsPathDetails(t *testing.T) {
	eq(t, resolveAgentDir(senv{"PI_CODING_AGENT_DIR": "  /opt/a/../b/  "}, "/h"), "/opt/b/", "trimmed, normalized, trailing slash kept")
	eq(t, goalGlobalSettingsPath(senv{"PI_GOAL_GLOBAL_SETTINGS_FILE": " "}, "/h"), filepath.Join("/h", ".pi", "agent", "pi-goal-x-settings.json"), "blank override ignored")
	eq(t, goalGlobalSettingsPath(senv{piGoalGlobalSettingsFileEnv: "/e/../f.json"}, "/h"), "/f.json", "normalized absolute override")
	eq(t, goalSettingsPath("/p", senv{piGoalSettingsFileEnv: "/e/../f.json"}), "/e/../f.json", "project absolute override kept as written")
	eq(t, formatGoalKeybinding("Control+Left"), "Ctrl+←", "case-insensitive labels")
	eq(t, fingerprintOf(""), "0:811c9dc5", "empty fingerprint")
	eq(t, fingerprintOf("a😀"), "3:"+fingerprintOf("a😀")[2:], "UTF-16 length")
}
