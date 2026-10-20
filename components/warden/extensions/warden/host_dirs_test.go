package warden

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Twins of tests/host-dirs.test.ts. Upstream resolves Pi's agent directory from PI_CODING_AGENT_DIR; the port's files live
// under the PiG config home (D5), so the variable is PIG_HOME and the project directory is `.pig` (see host_dirs.go).

func env(pairs ...string) func(string) string {
	m := map[string]string{}
	for i := 0; i < len(pairs); i += 2 {
		m[pairs[i]] = pairs[i+1]
	}
	return func(k string) string { return m[k] }
}

// twin: tests/host-dirs.test.ts:23
func TestDefaultHostDirsUnsetFallsBackToPiGsConfigHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	dirs := DefaultHostDirs(env())
	eq(t, "configHome", dirs.ConfigHome, filepath.Join(home, ".pig"))
	eq(t, "configDirName", dirs.ConfigDirName, ".pig")
	eq(t, "shared Pi directories", DefaultHostDirs(env("PIG_USE_PI_DIRS", "1")).ConfigDirName, ".pi")
	eq(t, "any other value", DefaultHostDirs(env("PIG_USE_PI_DIRS", "0")).ConfigDirName, ".pig")
}

// twin: tests/host-dirs.test.ts:30
func TestDefaultHostDirsExplicitPathWinsTildeExpandsBlankFallsBack(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	for in, want := range map[string]string{
		"/opt/agent": "/opt/agent",
		"~":          home,
		"~/x":        filepath.Join(home, "x"),
		"  ":         filepath.Join(home, ".pig"),
		"~/x ":       filepath.Join(home, "x"),
	} {
		eq(t, "PIG_HOME="+in, DefaultHostDirs(env("PIG_HOME", in)).ConfigHome, want)
	}
}

// twin: tests/host-dirs.test.ts:92
func TestLoadConfigReadUserConfigAndSetUserSettingHonourInjectedDirs(t *testing.T) {
	temp := t.TempDir()
	dirs := HostDirs{ConfigHome: temp, ConfigDirName: ".omp"}
	user := dirs.UserConfigPath()
	eq(t, "user path", user, filepath.Join(temp, "pigpen-warden", "config.json"))
	if _, present := ReadUserConfig(user)["enabled"]; present {
		t.Error("no file yet")
	}
	if err := SetUserSetting(user, "enabled", false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(user)
	if !strings.Contains(string(data), `"enabled": false`) {
		t.Errorf("file %q", data)
	}
	cfg, err := LoadConfig(user)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "enabled", cfg.Enabled, false)
	// A project file under the injected configDirName is read when trusted.
	proj := filepath.Join(temp, "proj")
	if err := os.MkdirAll(filepath.Join(proj, ".omp"), 0o755); err != nil {
		t.Fatal(err)
	}
	projectPath := dirs.ProjectConfigPath(proj)
	eq(t, "project path", projectPath, filepath.Join(proj, ".omp", "pi-warden.json"))
	if err := os.WriteFile(projectPath, []byte(`{"action":{"offTask":{"warn":0.3,"steer":0.4}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	withProject, err := LoadConfigWithProject(user, projectPath, true)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "project file under .omp is read", withProject.Action.OffTask.Warn, 0.3)
	// WriteUserConfig round-trips through dirs as well.
	if err := WriteUserConfig(user, map[string]any{"enabled": true}); err != nil {
		t.Fatal(err)
	}
	eq(t, "round trip", ReadUserConfig(user)["enabled"], true)
}

// The hostile shapes of the files themselves: a user file that is not an object is the defaults; a directory where the
// file should be is an error; an unreadable project file is reported and the user file still applies.
func TestConfigFileShapes(t *testing.T) {
	temp := t.TempDir()
	user := filepath.Join(temp, "u.json")
	for _, body := range []string{`[]`, `"x"`, `3`, `null`} {
		if err := os.WriteFile(user, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadConfig(user)
		if err != nil || !reflectEqualConfig(cfg, DefaultConfig()) {
			t.Errorf("%s: %v", body, err)
		}
	}
	if err := os.WriteFile(user, []byte(`{"mode":"advise"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfigWithProject(user, filepath.Join(temp, "p.json"), true)
	if err != nil || cfg.Mode != "advise" {
		t.Errorf("a missing project file is fine: %v %v", cfg.Mode, err)
	}
	if err := os.WriteFile(filepath.Join(temp, "p.json"), []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadConfigWithProject(user, filepath.Join(temp, "p.json"), true)
	if err == nil || cfg.Mode != "advise" {
		t.Errorf("a broken project file is reported and the user file applies: %v %v", cfg.Mode, err)
	}
}
