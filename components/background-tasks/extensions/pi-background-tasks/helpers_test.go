package pi_background_tasks_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// External test helpers: files, settings, equality, and shortcut invocation through the fake host.

func eq(t *testing.T, got, want any, what string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s:\n got  %#v\n want %#v", what, got, want)
	}
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

const configID = "@vanillagreen/pi-background-tasks"

// agentWith makes a temporary agent directory holding these package settings and points the process at it.
func agentWith(t *testing.T, config map[string]any) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PIG_EXT_HOST_INFO", "")
	t.Setenv("PIG_USE_PI_DIRS", "")
	t.Setenv("PIG_CODING_AGENT_DIR", dir)
	t.Setenv("PI_BG_TASK_DIR", t.TempDir())
	if config != nil {
		doc := map[string]any{"kendex": map[string]any{"extensionManager": map[string]any{"config": map[string]any{configID: config}}}}
		b, _ := json.Marshal(doc)
		writeFile(t, filepath.Join(dir, "settings.json"), string(b))
	}
	return dir
}

// shortcut runs a registered shortcut; known is false when no such shortcut is registered.
func (h *Host) shortcut(key string) (known bool, failure string) {
	_, failure = h.roundTrip(map[string]any{"method": "shortcut", "tool": key, "args": json.RawMessage(`{}`)})
	return !strings.Contains(failure, "unknown shortcut"), failure
}

func notifications(h *Host) []string {
	var out []string
	for _, c := range h.CallsTo("ui.notify") {
		out = append(out, c.Args["message"].(string))
	}
	return out
}
