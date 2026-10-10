package pigmodeltweaks_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	pigmodeltweaks "github.com/MichaelKinsy/pigpen/components/pig-model-tweaks/extensions/pig-model-tweaks"
)

// Context.ConfigHome fails when no config directory can be found (SDK 0.5). Both users of it must say so and must not
// write settings under a guessed relative directory (a project could plant one).
func noConfigHome(t *testing.T) string {
	t.Helper()
	for _, k := range []string{"PIG_HOME", "XDG_CONFIG_HOME", "HOME", "USERPROFILE", "PIG_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR"} {
		t.Setenv(k, "")
	}
	dir := t.TempDir()
	t.Chdir(dir)
	return dir
}

func assertNothingWritten(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("settings were written under the working directory without a config home: %v", entries)
	}
}

func TestSessionStartWithoutAConfigHomeFailsAndWritesNothing(t *testing.T) {
	dir := noConfigHome(t)
	h := StartHost(t, pigmodeltweaks.Extension(), HostOptions{})
	args, _ := json.Marshal(map[string]any{"type": "session_start", "reason": "startup"})
	_, failure := h.roundTrip(map[string]any{"method": "event", "event": "session_start", "handler_id": h.handlers["session_start"], "args": json.RawMessage(args)})
	if !strings.Contains(failure, "home directory") {
		t.Fatalf("session_start failure = %q, want the missing home directory", failure)
	}
	assertNothingWritten(t, dir)
}

// The settings were bound at session start while a home existed; the home then goes away (its environment changed), and
// a model selection must warn that it cannot remember the model instead of writing under a relative directory.
func TestRememberModelWhenTheConfigHomeIsGoneWarnsAndWritesNothing(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_CODING_AGENT_DIR", "")
	t.Setenv("PI_CODING_AGENT_DIR", "")
	h := StartHost(t, pigmodeltweaks.Extension(), HostOptions{})
	h.Fire("session_start", map[string]any{"reason": "startup"})
	dir := noConfigHome(t)
	fireEveryHandler(t, h, "model_select", map[string]any{"model": map[string]any{"provider": "acme", "id": "m-1"}})
	var warned bool
	for _, call := range h.CallsTo("ui.notify") {
		if msg, _ := call.Args["message"].(string); strings.Contains(msg, "cannot remember the model") && call.Args["level"] == "warning" {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("no warning that the model could not be remembered: %+v", h.CallsTo("ui.notify"))
	}
	assertNothingWritten(t, dir)
}

// fireEveryHandler delivers an event to every handler the extension registered for it, in order, as the host does.
func fireEveryHandler(t *testing.T, h *Host, event string, data map[string]any) {
	t.Helper()
	data["type"] = event
	args, _ := json.Marshal(data)
	for _, id := range h.allHandlers[event] {
		if _, failure := h.roundTrip(map[string]any{"method": "event", "event": event, "handler_id": id, "args": json.RawMessage(args)}); failure != "" {
			t.Fatalf("%s handler %d failed: %s", event, id, failure)
		}
	}
}
