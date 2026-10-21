package pi_background_tasks

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The settings reader: the original reads `kendex.extensionManager.config["@vanillagreen/pi-background-tasks"]` from the
// user settings file and, once Pi trusts the project, from `.pi/settings.json`. These are the port's own tests of
// package-config.ts and settings.ts (the original has no direct tests of them; its suites exercise them through
// readResourceControlSettings and the settings fixtures).

func TestUserSettingsAreRead(t *testing.T) {
	agent := testAgent(t)
	cwd := t.TempDir()
	writeSettings(t, filepath.Join(agent, "settings.json"), map[string]any{"outputSettleMs": 5, "glyphStyle": "ascii"})
	eq(t, settingNumber("outputSettleMs", 2000, cwd), 5.0, "number")
	eq(t, settingEnum("glyphStyle", []string{"unicode", "ascii"}, "unicode", cwd), "ascii", "enum")
	eq(t, settingNumber("forceKillGraceMs", 5000, cwd), 5000.0, "an absent key is the fallback")
}

func TestProjectSettingsApplyOnlyOnceTheProjectIsTrusted(t *testing.T) {
	agent := testAgent(t)
	cwd := t.TempDir()
	writeSettings(t, filepath.Join(agent, "settings.json"), map[string]any{"outputSettleMs": 5, "showWidget": true})
	writeSettings(t, filepath.Join(cwd, ".pi", "settings.json"), map[string]any{"outputSettleMs": 9})
	eq(t, settingNumber("outputSettleMs", 0, cwd), 5.0, "untrusted project is not read")
	settings.recordProjectTrust(cwd, func() (bool, error) { return true, nil })
	eq(t, settingNumber("outputSettleMs", 0, cwd), 9.0, "a trusted project overrides the user value key by key")
	eq(t, settingBoolean("showWidget", false, cwd), true, "a key the project does not set keeps the user value")
	settings.recordProjectTrust(cwd, func() (bool, error) { return false, nil })
	eq(t, settingNumber("outputSettleMs", 0, cwd), 5.0, "a trust answer recorded later applies at once")
	settings.recordProjectTrust(cwd, func() (bool, error) { return true, os.ErrPermission })
	eq(t, settingNumber("outputSettleMs", 0, cwd), 5.0, "a host error is not trust")
}

func TestMalformedSettingsFileContributesNothing(t *testing.T) {
	agent := testAgent(t)
	cwd := t.TempDir()
	writeFile(t, filepath.Join(agent, "settings.json"), "{not json")
	eq(t, settingNumber("outputSettleMs", 7, cwd), 7.0, "a user file that does not parse")
	writeFile(t, filepath.Join(agent, "settings.json"), "[1,2]")
	eq(t, settingNumber("outputSettleMs", 7, cwd), 7.0, "a document that is not an object reads as empty")
}

func TestSettingValueTypesFollowTheOriginal(t *testing.T) {
	agent := testAgent(t)
	cwd := t.TempDir()
	writeSettings(t, filepath.Join(agent, "settings.json"), map[string]any{
		"a": "12", "b": " 0x10 ", "c": "", "d": "abc", "e": "Infinity", "f": true, "g": "  spaced  ", "h": "   ", "i": 3.5, "j": "ascii", "k": "bogus",
	})
	for key, want := range map[string]float64{"a": 12, "b": 16, "c": 0, "d": -1, "e": -1, "f": -1, "i": 3.5} {
		eq(t, settingNumber(key, -1, cwd), want, "number "+key)
	}
	eq(t, settingString("g", "x", cwd), "spaced", "a string is trimmed")
	eq(t, settingString("h", "x", cwd), "x", "a blank string is unset")
	eq(t, settingString("i", "x", cwd), "x", "a number is not a string")
	eq(t, settingBoolean("f", false, cwd), true, "boolean")
	eq(t, settingBoolean("a", true, cwd), true, "a string is not a boolean")
	eq(t, settingEnum("j", []string{"unicode", "ascii"}, "unicode", cwd), "ascii", "enum member")
	eq(t, settingEnum("k", []string{"unicode", "ascii"}, "unicode", cwd), "unicode", "enum outsider")
}

func TestSettingsAreServedForOneSecondThenReadAgain(t *testing.T) {
	agent := testAgent(t)
	cwd := t.TempDir()
	s := newClockedStore(agent)
	path := filepath.Join(agent, "settings.json")
	writeSettings(t, path, map[string]any{"outputSettleMs": 1})
	get := func() any { return s.config(configID, cwd)["outputSettleMs"] }
	eq(t, get(), 1.0, "first read")
	writeSettings(t, path, map[string]any{"outputSettleMs": 2})
	s.now = 999 * time.Millisecond
	eq(t, get(), 1.0, "inside the window the answer comes from memory")
	s.now = time.Second
	eq(t, get(), 2.0, "a hand edit applies once the window closes")
	writeSettings(t, path, map[string]any{"outputSettleMs": 3})
	s.clear()
	eq(t, get(), 3.0, "clear drops every answer")
}

func TestProjectSettingsPathStopsAtMarkers(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	eq(t, projectSettingsPath(deep), filepath.Join(deep, ".pi", "settings.json"), "no marker anywhere: the cwd's own path")
	writeFile(t, filepath.Join(root, ".kendex-lock.json"), "{}")
	eq(t, projectSettingsPath(deep), filepath.Join(root, ".pi", "settings.json"), "a .kendex-lock.json stops the walk")
	writeFile(t, filepath.Join(root, "a", ".pi", "settings.json"), "{}")
	eq(t, projectSettingsPath(deep), filepath.Join(root, "a", ".pi", "settings.json"), "the nearest existing settings file wins")
	if err := os.MkdirAll(filepath.Join(deep, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	eq(t, projectSettingsPath(deep), filepath.Join(deep, ".pi", "settings.json"), ".git stops the walk")
}

func TestTaskDirPrecedence(t *testing.T) {
	agent := testAgent(t)
	cwd := t.TempDir()
	t.Chdir(cwd)
	tmp := filepath.Join(os.TempDir(), "kendex-pi-bg")
	eq(t, taskDir(), tmp, "default")
	writeSettings(t, filepath.Join(agent, "settings.json"), map[string]any{"taskDir": "  rel/logs  "})
	settings.clear()
	abs, _ := filepath.Abs("rel/logs")
	eq(t, taskDir(), abs, "the setting is made absolute")
	t.Setenv("PI_BG_TASK_DIR", "  /elsewhere  ")
	eq(t, taskDir(), "/elsewhere", "the environment variable overrides the setting, trimmed")
	eq(t, taskLanesRoot(), filepath.Join("/elsewhere", "lanes"), "lanes root")
	eq(t, taskLaneDir("a b/c:d"), filepath.Join("/elsewhere", "lanes", "a_b_c_d"), "one directory per session")
	eq(t, logFilePath("/l", "bg-1", 1700), filepath.Join("/l", "bg-1-1700.log"), "log file")
	eq(t, safeLabel("!!!"), "task", "an empty label")
	eq(t, safeLabel("a b!c"), "a-b-c", "runs collapse to one hyphen")
}

func TestAgentDirectoryRule(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PIG_EXT_HOST_INFO", "")
	t.Setenv("PIG_USE_PI_DIRS", "")
	t.Setenv("PIG_CODING_AGENT_DIR", "")
	t.Setenv("PIG_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("PI_CODING_AGENT_DIR", "/pi/agent")
	eq(t, agentDirFromEnv(), filepath.Join(home, ".pig", "agent"), "default is PiG's own tree")
	t.Setenv("PIG_HOME", "~/ph")
	eq(t, agentDirFromEnv(), filepath.Join(home, "ph", "agent"), "PIG_HOME")
	t.Setenv("PIG_CODING_AGENT_DIR", "/x/agent")
	eq(t, agentDirFromEnv(), "/x/agent", "PIG_CODING_AGENT_DIR wins")
	t.Setenv("PIG_USE_PI_DIRS", "1")
	eq(t, agentDirFromEnv(), "/pi/agent", "PIG_USE_PI_DIRS selects Pi's directory")
	t.Setenv("PI_CODING_AGENT_DIR", "")
	eq(t, agentDirFromEnv(), filepath.Join(home, ".pi", "agent"), "and Pi's default")
}

func TestTaskEnvPutsTheAgentBinFirst(t *testing.T) {
	agent := testAgent(t)
	t.Setenv("PATH", "/usr/bin"+string(os.PathListSeparator)+"/bin")
	find := func() string {
		for _, kv := range taskEnv() {
			if len(kv) > 5 && kv[:5] == "PATH=" {
				return kv[5:]
			}
		}
		return ""
	}
	eq(t, find(), "/usr/bin"+string(os.PathListSeparator)+"/bin", "no bin directory: PATH unchanged")
	bin := filepath.Join(agent, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	eq(t, find(), bin+string(os.PathListSeparator)+"/usr/bin"+string(os.PathListSeparator)+"/bin", "bin first")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin")
	eq(t, find(), bin+string(os.PathListSeparator)+"/usr/bin", "already present: unchanged")
}
