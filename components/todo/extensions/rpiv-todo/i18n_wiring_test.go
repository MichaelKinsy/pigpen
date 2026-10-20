package rpiv_todo_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain keeps the developer's own rpiv-i18n setup out of every test in the binary.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "rpiv-todo-i18n")
	if err != nil {
		panic(err)
	}
	os.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(dir, "agent"))
	os.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	os.Setenv("LANG", "")
	os.Setenv("LC_ALL", "")
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// isolateLocale points the agent and config directories at fresh temp directories and clears the locale environment.
func isolateLocale(t *testing.T) (agent, cfg string) {
	t.Helper()
	agent, cfg = t.TempDir(), t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", agent)
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("LANG", "")
	t.Setenv("LC_ALL", "")
	return agent, cfg
}

// The wiring: session_start reads Pi's settings, then /todos speaks the locale from locale.json (only when installed).
func TestSessionStartInstallsTheLocaleForTheCommand(t *testing.T) {
	for _, installed := range []bool{false, true} {
		agent, cfg := isolateLocale(t)
		writeFile(t, filepath.Join(cfg, "rpiv-i18n", "locale.json"), `{"locale":"de"}`)
		if installed {
			writeFile(t, filepath.Join(agent, "settings.json"), `{"packages":["npm:@juicesharp/rpiv-i18n@2.13.9"]}`)
		}
		r := startRig(t, HostOptions{})
		r.Fire("session_start", nil)
		r.Command("todos", "")
		want := "No todos yet. Ask the agent to add some!"
		if installed {
			want = "Noch keine Todos. Bitte den Agenten, welche hinzuzufügen!"
		}
		eq(t, at(r.notifies(), 0)["message"], want, "no todos message")
	}
}

func TestSessionStartReadsTheProjectSettings(t *testing.T) {
	_, cfg := isolateLocale(t)
	writeFile(t, filepath.Join(cfg, "rpiv-i18n", "locale.json"), `{"locale":"de"}`)
	cwd := t.TempDir()
	writeFile(t, filepath.Join(cwd, ".pi", "settings.json"), `{"packages":["npm:@juicesharp/rpiv-i18n"]}`)
	r := startRig(t, HostOptions{Cwd: cwd})
	r.Fire("session_start", nil)
	r.call(map[string]any{"action": "create", "subject": "a"})
	r.Command("todos", "")
	if !strings.Contains(at(r.notifies(), 0)["message"].(string), "── Ausstehend ──") {
		t.Fatalf("expected the German section title, got %v", r.notifies())
	}
}
