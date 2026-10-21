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

// remembered writes a remembered model into the settings file under home.
func rememberedHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	dir := filepath.Join(home, "agent")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, settingsFileName),
		[]byte(`{"version":1,"rememberModel":{"enabled":true,"last":{"provider":"acme","modelId":"model-1"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

func restoreHost(t *testing.T, entries []map[string]any) *Host {
	t.Helper()
	h := StartHost(t, Extension(), HostOptions{OnCall: func(method string, args map[string]any) (map[string]any, string) {
		switch method {
		case "getModel":
			return map[string]any{"id": "model-1", "provider": "acme"}, ""
		case "watchSessionLog":
			cursor, _ := args["cursor"].(float64)
			page := entries[min(int(cursor), len(entries)):]
			return map[string]any{"entries": page, "entryCount": len(entries), "hasMore": false, "leafId": "m1"}, ""
		}
		return map[string]any{}, ""
	}})
	return h
}

// The original restores the remembered model on a fresh startup as well as on
// a new session, because settings.json cannot name a locked `:provider` model.
func TestStartupWithAnEmptyBranchRestoresTheRememberedModel(t *testing.T) {
	rememberedHome(t)
	h := restoreHost(t, nil)
	h.FireAll("session_start", map[string]any{"reason": "startup"})
	if calls := h.CallsTo("setModel"); len(calls) != 1 || calls[0].Args["model"] != "acme/model-1" {
		t.Fatalf("setModel calls = %v, want one for acme/model-1", calls)
	}
}

func TestStartupWithMessagesKeepsTheSessionsOwnModel(t *testing.T) {
	rememberedHome(t)
	h := restoreHost(t, []map[string]any{{"id": "m1", "type": "message", "role": "user", "content": "hi"}})
	h.FireAll("session_start", map[string]any{"reason": "startup"})
	if calls := h.CallsTo("setModel"); len(calls) != 0 {
		t.Fatalf("a session with history was switched: %v", calls)
	}
}

func TestResumeNeverRestoresTheRememberedModel(t *testing.T) {
	rememberedHome(t)
	h := restoreHost(t, nil)
	h.FireAll("session_start", map[string]any{"reason": "resume"})
	if calls := h.CallsTo("setModel"); len(calls) != 0 {
		t.Fatalf("resume was switched: %v", calls)
	}
}
