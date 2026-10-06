package tintinweb_tasks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestMain points HOME and the agent directory at a scratch directory, as the original's test setup does
// (test/setup.ts): session task files, named shared lists and the global tasks-config all resolve below them.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "pi-tasks-test-home-")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	os.Setenv("USERPROFILE", home)
	for _, k := range []string{"PI_CODING_AGENT_DIR", "PIG_CODING_AGENT_DIR", "PIG_HOME", "PIG_USE_PI_DIRS", "XDG_CONFIG_HOME", "PI_TASKS", "PI_TASKS_DEBUG"} {
		os.Unsetenv(k)
	}
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}

// scratch returns a fresh directory removed with the test.
func scratch(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readJSON(t *testing.T, path string) any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// eq fails the test unless got and want are deeply equal.
func eq(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

// obj and arr build JSON values the way a decoded config holds them.
type obj = map[string]any
type arr = []any

func ids(tasks []task) []string {
	out := []string{}
	for _, t := range tasks {
		out = append(out, t.ID)
	}
	return out
}
