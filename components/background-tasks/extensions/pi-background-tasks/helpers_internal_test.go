package pi_background_tasks

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Internal test helpers: a temporary agent directory, a settings store with a hand-driven clock, and equality
// with a message.

func eq(t *testing.T, got, want any, what string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s:\n got  %#v\n want %#v", what, got, want)
	}
}

// testAgent points the agent directory at a new temporary one and the host-info variable away, so the test never reads
// a real configuration. It returns the directory.
func testAgent(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PIG_EXT_HOST_INFO", "")
	t.Setenv("PIG_USE_PI_DIRS", "")
	t.Setenv("PIG_CODING_AGENT_DIR", dir)
	t.Setenv("PI_BG_TASK_DIR", "")
	settings.clear()
	t.Cleanup(settings.clear)
	return dir
}

func writeSettings(t *testing.T, path string, config map[string]any) {
	t.Helper()
	doc := map[string]any{"kendex": map[string]any{"extensionManager": map[string]any{"config": map[string]any{configID: config}}}}
	b, _ := json.Marshal(doc)
	writeFile(t, path, string(b))
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

// clockedStore is a settings store whose clock the test advances.
type clockedStore struct {
	*settingsStore
	now time.Duration
}

func newClockedStore(userDir string) *clockedStore {
	s := &clockedStore{settingsStore: newSettingsStore()}
	s.userDir = func() string { return userDir }
	s.clock = func() time.Duration { return s.now }
	return s
}

func containsStr(s, sub string) bool { return strings.Contains(s, sub) }

func jsonLen(v any) int {
	b, _ := json.Marshal(v)
	return len(b)
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func newCommand(name string, args ...string) *exec.Cmd { return exec.Command(name, args...) }

func ioReadFull(r io.Reader, buf []byte) (int, error) { return io.ReadFull(r, buf) }
