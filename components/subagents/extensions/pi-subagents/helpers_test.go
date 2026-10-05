package pi_subagents

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func eq(t *testing.T, got, want any, what string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: got %#v, want %#v", what, got, want)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// withTempHome isolates HOME (and the variables that move the agent directory) the way the original's tests do;
// it returns the home. Tests that do not name a home in the original still run isolated here, so the machine's own
// agents never leak into an assertion.
func withTempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	for _, k := range []string{"HOME", "USERPROFILE"} {
		t.Setenv(k, home)
	}
	for _, k := range []string{"PI_CODING_AGENT_DIR", "PI_SUBAGENT_EXTRA_AGENT_DIRS"} {
		old, had := os.LookupEnv(k)
		os.Unsetenv(k)
		t.Cleanup(func() {
			if had {
				os.Setenv(k, old)
			}
		})
	}
	return home
}

func tmp(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func findAgent(agents []AgentConfig, name string) *AgentConfig {
	for i := range agents {
		if agents[i].Name == name {
			return &agents[i]
		}
	}
	return nil
}

func hasMatch(t *testing.T, err error, pattern string) {
	t.Helper()
	if err == nil {
		t.Errorf("no error, want one matching %q", pattern)
		return
	}
	if !strings.Contains(err.Error(), pattern) {
		t.Errorf("error %q does not contain %q", err.Error(), pattern)
	}
}
