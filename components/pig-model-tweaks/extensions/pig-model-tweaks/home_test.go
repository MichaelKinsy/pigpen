package pigmodeltweaks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// fire delivers one event and returns the handler's failure ("" on success).
func fire(h *Host, event string, data map[string]any) string {
	h.mu.Lock()
	id := h.handlers[event]
	h.mu.Unlock()
	data["type"] = event
	args, _ := json.Marshal(data)
	_, failure := h.roundTrip(map[string]any{"method": "event", "event": event, "handler_id": id, "args": json.RawMessage(args)})
	return failure
}

// rememberOnly is an extension with just the remember-model handlers, so a
// model_select reaches them and not the guard's.
func rememberOnly() *sdk.Extension {
	ext := sdk.New("pig-model-tweaks")
	registerRememberModel(ext, newStore())
	return ext
}

func selectModel() map[string]any {
	return map[string]any{"model": map[string]any{"provider": "acme", "id": "model-1"}, "source": "set"}
}

func withoutConfigHome(t *testing.T) string {
	t.Helper()
	cwd := t.TempDir()
	t.Chdir(cwd)
	for _, name := range []string{"PIG_HOME", "XDG_CONFIG_HOME", "HOME", "USERPROFILE"} {
		t.Setenv(name, "")
	}
	return cwd
}

// Without a config home there is no agent directory: session start and a
// remembered model selection fail instead of writing settings under the working
// directory.
func TestMissingConfigHomeFailsSessionStart(t *testing.T) {
	cwd := withoutConfigHome(t)
	h := StartHost(t, Extension(), HostOptions{Cwd: cwd})
	if failure := fire(h, "session_start", map[string]any{"reason": "startup"}); !strings.Contains(failure, "home directory") {
		t.Fatalf("session_start failure = %q, want the missing home directory", failure)
	}
	if _, err := os.Stat(filepath.Join(cwd, "agent")); err == nil {
		t.Fatal("settings written under the working directory")
	}
}

func TestMissingConfigHomeFailsARememberedSelection(t *testing.T) {
	cwd := withoutConfigHome(t)
	h := StartHost(t, rememberOnly(), HostOptions{Cwd: cwd})
	if failure := fire(h, "model_select", selectModel()); !strings.Contains(failure, "home directory") {
		t.Fatalf("model_select failure = %q, want the missing home directory", failure)
	}
	if _, err := os.Stat(filepath.Join(cwd, "agent")); err == nil {
		t.Fatal("settings written under the working directory")
	}
}

// With PIG_HOME, a selection is remembered in PiG's own settings.json, which the
// extension updates but never creates.
func TestModelSelectRemembersTheDefaultUnderTheConfigHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "agent", "settings.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := StartHost(t, rememberOnly(), HostOptions{})
	if failure := fire(h, "model_select", selectModel()); failure != "" {
		t.Fatalf("model_select: %s", failure)
	}
	raw, err := os.ReadFile(filepath.Join(home, "agent", "settings.json"))
	if err != nil || !strings.Contains(string(raw), `"model-1"`) {
		t.Fatalf("settings.json = %s, %v; want the selected model", raw, err)
	}
}
