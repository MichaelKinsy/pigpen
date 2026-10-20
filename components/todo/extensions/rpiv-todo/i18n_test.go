package rpiv_todo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// useTables replaces the locale tables for one test and restores the locale state afterwards.
func useTables(t *testing.T, tables map[string]map[string]string) {
	t.Helper()
	resetLocaleState(t)
	i18nMu.Lock()
	localeTables = tables
	i18nMu.Unlock()
}

// resetLocaleState isolates the process-wide locale state: no override, not installed, tables reloaded from the embedded files.
func resetLocaleState(t *testing.T) {
	t.Helper()
	clear := func() {
		i18nMu.Lock()
		localeOverride, i18nInstalled, localeTables = "", false, nil
		i18nMu.Unlock()
	}
	clear()
	t.Cleanup(clear)
}

// bridgeTestTables are the tables the original's i18n-bridge.test.ts registers.
func bridgeTestTables() map[string]map[string]string {
	return map[string]map[string]string{
		"en": {"status.pending": "pending", "status.in_progress": "in progress", "status.completed": "completed", "status.deleted": "deleted"},
		"de": {"status.in_progress": "in Bearbeitung", "status.completed": "erledigt"},
	}
}

// installRpivI18n isolates the agent directory, the config directory and the locale environment, and writes a settings
// file that installs rpiv-i18n. It returns the config directory.
func installRpivI18n(t *testing.T, installed bool) (project, configDir string) {
	t.Helper()
	resetLocaleState(t)
	agent, project, configDir := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", agent)
	t.Setenv("XDG_CONFIG_HOME", configDir)
	t.Setenv("LANG", "")
	t.Setenv("LC_ALL", "")
	if installed {
		writeSettings(t, agent, `{"packages": ["npm:@juicesharp/rpiv-i18n@2.13.9"]}`)
	}
	setI18nInstalled(project)
	return project, configDir
}

func writeSettings(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeLocaleJSON(t *testing.T, configDir, content string) {
	t.Helper()
	dir := filepath.Join(configDir, "rpiv-i18n")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "locale.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Without rpiv-i18n installed every string is English, whatever locale.json and LANG say (the original's bridge when
// the dynamic import of the SDK fails).
func TestNotInstalledStaysEnglish(t *testing.T) {
	_, cfg := installRpivI18n(t, false)
	writeLocaleJSON(t, cfg, `{"locale":"de"}`)
	t.Setenv("LANG", "fr_FR.UTF-8")
	eq(t, tr("overlay.expandHint", "{key} to expand"), "{key} to expand", "fallback")
	eq(t, formatStatusLabel(statusCompleted), "completed", "status")
}

func TestInstalledResolvesLocaleJSONThenLangThenLcAll(t *testing.T) {
	_, cfg := installRpivI18n(t, true)
	eq(t, formatStatusLabel(statusCompleted), "completed", "no locale source is English")
	t.Setenv("LC_ALL", "ru_RU.UTF-8")
	eq(t, formatStatusLabel(statusCompleted), tableValue(t, "ru", "status.completed"), "LC_ALL")
	t.Setenv("LANG", "de_DE.UTF-8")
	eq(t, formatStatusLabel(statusCompleted), "erledigt", "LANG before LC_ALL")
	writeLocaleJSON(t, cfg, `{"locale":"es"}`)
	eq(t, formatStatusLabel(statusCompleted), tableValue(t, "es", "status.completed"), "locale.json before LANG")
	writeLocaleJSON(t, cfg, `{"locale":"pt-BR"}`)
	eq(t, tr("overlay.heading", "Todos"), tableValue(t, "pt-BR", "overlay.heading"), "region code")
	writeLocaleJSON(t, cfg, `{not json`)
	eq(t, formatStatusLabel(statusCompleted), "erledigt", "malformed locale.json falls through to LANG")
	writeLocaleJSON(t, cfg, `{"locale":""}`)
	eq(t, formatStatusLabel(statusCompleted), "erledigt", "empty locale falls through")
}

func TestLangCAndPosixAreEnglish(t *testing.T) {
	installRpivI18n(t, true)
	for _, v := range []string{"C", "POSIX", "C.UTF-8", ""} {
		t.Setenv("LANG", v)
		eq(t, formatStatusLabel(statusCompleted), "completed", "LANG="+v)
	}
}

func TestUnknownLocaleAndEmptyValuesFallBackToEnglish(t *testing.T) {
	_, cfg := installRpivI18n(t, true)
	writeLocaleJSON(t, cfg, `{"locale":"xx"}`)
	eq(t, formatStatusLabel(statusCompleted), "completed", "unknown locale")
	useTables(t, map[string]map[string]string{"en": {"k": "english"}, "de": {"k": ""}})
	setLocale("de")
	eq(t, tr("k", "inline"), "english", "an empty translation is absent")
	eq(t, tr("other", "inline"), "inline", "unknown key")
}

func TestLocaleFilesEmbeddedForEverySupportedLocale(t *testing.T) {
	resetLocaleState(t)
	en := tableValueMap(t, "en")
	for _, code := range supportedLocales {
		m := tableValueMap(t, code)
		if len(m) == 0 {
			t.Fatalf("locale %s is empty", code)
		}
		if code == "en" {
			continue
		}
		for key := range en {
			if strings.HasPrefix(key, "_") {
				continue
			}
			if m[key] == "" {
				t.Errorf("locale %s lacks %s", code, key)
			}
		}
	}
}

func TestTranslatedViews(t *testing.T) {
	installRpivI18n(t, true)
	setLocale("de")
	eq(t, formatStatusLabel(statusPending), "ausstehend", "pending")
	eq(t, tr("command.section.in_progress", "── In Progress ──"), "── In Bearbeitung ──", "section")
	eq(t, tr("overlay.expandHint", "{key} to expand"), "{key} zum Ausklappen", "hint")
	eq(t, tr("command.no_todos", msgNoTodos), "Noch keine Todos. Bitte den Agenten, welche hinzuzufügen!", "no todos")
}

func TestRpivI18nInstalledReadsPiSettings(t *testing.T) {
	resetLocaleState(t)
	agent, project := t.TempDir(), t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", agent)
	if rpivI18nInstalled(project) {
		t.Fatal("no settings installs nothing")
	}
	writeSettings(t, agent, `{"packages": ["npm:@juicesharp/rpiv-todo", "npm:@other/pkg"]}`)
	if rpivI18nInstalled(project) {
		t.Fatal("another rpiv package is not rpiv-i18n")
	}
	writeSettings(t, agent, `{"packages": ["npm:@juicesharp/rpiv-i18n@2.13.9"]}`)
	if !rpivI18nInstalled(project) {
		t.Fatal("a package string naming rpiv-i18n installs it")
	}
	writeSettings(t, agent, `{"packages": [{"source": "npm:@juicesharp/rpiv-i18n", "extensions": []}]}`)
	if !rpivI18nInstalled(project) {
		t.Fatal("a package object with that source installs it")
	}
	writeSettings(t, agent, `{"extensions": ["/home/u/.pi/agent/extensions/rpiv-i18n/index.ts"]}`)
	if !rpivI18nInstalled(project) {
		t.Fatal("an extension path naming rpiv-i18n installs it")
	}
	writeSettings(t, agent, `{not json`)
	if rpivI18nInstalled(project) {
		t.Fatal("malformed settings install nothing")
	}
	writeSettings(t, filepath.Join(project, ".pi"), `{"packages": ["npm:@juicesharp/rpiv-i18n"]}`)
	if !rpivI18nInstalled(project) {
		t.Fatal("the project's .pi/settings.json installs it too")
	}
	if rpivI18nInstalled("") {
		t.Fatal("an empty project directory skips the project settings")
	}
}

func TestAgentDirResolution(t *testing.T) {
	t.Setenv("PIG_CODING_AGENT_DIR", "/a")
	t.Setenv("PIG_HOME", "/h")
	eq(t, agentDir(), "/a", "agent dir env")
	t.Setenv("PIG_CODING_AGENT_DIR", "")
	eq(t, agentDir(), filepath.Join("/h", "agent"), "pig home")
}

func tableValueMap(t *testing.T, code string) map[string]string {
	t.Helper()
	i18nMu.Lock()
	defer i18nMu.Unlock()
	return loadLocaleTables()[code]
}

func tableValue(t *testing.T, code, key string) string {
	t.Helper()
	v := tableValueMap(t, code)[key]
	if v == "" {
		t.Fatalf("locale %s has no %s", code, key)
	}
	return v
}
