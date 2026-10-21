package ask_user_question

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSettings(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The original is English unless @juicesharp/rpiv-i18n is installed; here "installed" is a settings entry.
func TestRpivI18nInstalledReadsPiSettings(t *testing.T) {
	agent, project := t.TempDir(), t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", agent)
	if rpivI18nInstalled(project) {
		t.Fatal("no settings installs nothing")
	}
	writeSettings(t, agent, `{"packages": ["npm:@juicesharp/rpiv-ask-user-question", "npm:@other/pkg"]}`)
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
