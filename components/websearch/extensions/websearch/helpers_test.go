package websearch

// Helpers the websearch twins share besides the twin template (twin_test.go): environment isolation, config
// files and the `adapted` marker for twins whose upstream fixture uses a provider this port does not have.

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"
)

// adapted marks a twin whose upstream fixture uses a provider this port does not have (OpenAI,
// XCrawl, AnySearch, SerpBase): the behaviour under test is provider-independent, so the case runs
// with a ported provider and `note` says what changed. Every adapted case is listed in PORT.md.
func adapted(t *testing.T, note string) {
	t.Helper()
	t.Log("ADAPTED: " + note)
}

// unsetenv removes variables for the test and restores them afterwards.
func unsetenv(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
}

// isolate points every config lookup at an empty temp home and clears provider credentials.
func isolate(t *testing.T) (home, agentDir string) {
	t.Helper()
	ResetCaches()
	t.Cleanup(ResetCaches)
	home = t.TempDir()
	agentDir = home + "/agent"
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PIG_HOME", home+"/pig")
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	unsetenv(t, "PI_CODING_AGENT_DIR", "XDG_CONFIG_HOME", "PI_WEB_ACCESS_CACHE_ROOT",
		"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy")
	for _, e := range os.Environ() {
		k, _, _ := strings.Cut(e, "=")
		if strings.HasSuffix(k, "_API_KEY") || strings.HasSuffix(k, "_BASE_URL") {
			unsetenv(t, k)
		}
	}
	return home, agentDir
}

// writeConfig writes web-search.json into the isolated agent dir.
func writeConfig(t *testing.T, agentDir, json string) {
	t.Helper()
	if err := os.WriteFile(agentDir+"/web-search.json", []byte(json), 0o600); err != nil {
		t.Fatal(err)
	}
}

// wantErr requires err to match the regular expression, like assert.rejects(p, /re/).
func wantErr(t *testing.T, err error, re string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error matching %q, got nil", re)
	}
	if !regexp.MustCompile(re).MatchString(err.Error()) {
		t.Fatalf("error %q does not match %q", err.Error(), re)
	}
}

func noErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func bg() context.Context { return context.Background() }
