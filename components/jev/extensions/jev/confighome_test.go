package jev_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Context.ConfigHome returns an error when no home directory is known (SDK 0.5). The user's config is
// then unreadable, and the project must not stand in for it: a relative "agent/" directory under the
// working directory is project-controlled, and a project-planted file there would otherwise switch
// the judge on with a project-chosen endpoint and key.
func TestNoConfigHomeNeverReadsAProjectPlantedUserConfig(t *testing.T) {
	e := newEnv(t)
	t.Setenv("PIG_CODING_AGENT_DIR", "")
	t.Setenv("PI_CODING_AGENT_DIR", "")
	t.Setenv("PIG_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "") // os.UserHomeDir reads it on Windows
	t.Setenv("TYPESAFE_API_KEY", testKey)

	srv := newFakeJev(t, always(gateBody(0.99, 0.04, 0.4, 0.5, 0.9)))
	planted := filepath.Join(e.cwd, "agent")
	if err := os.MkdirAll(planted, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := jevConfig(srv, map[string]any{"gate": map[string]any{"mode": "enforce"}})
	b, _ := json.Marshal(cfg)
	if err := os.WriteFile(filepath.Join(planted, "pi-jev.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(e.cwd)

	hs := newHostState()
	h := start(t, e, hs, HostOptions{})

	if block, reason := h.toolCall("bash", bash("rm -rf src")); block || reason != "" {
		t.Errorf("a project-planted user config judged a call: block=%v reason=%q", block, reason)
	}
	if n := srv.count(); n != 0 {
		t.Errorf("the planted config's endpoint received %d request(s)", n)
	}
	var told bool
	for _, note := range h.notifications() {
		if strings.Contains(note, "PiG config directory") {
			told = true
		}
	}
	if !told {
		t.Errorf("the user was not told the config directory is missing: %q", h.notifications())
	}
}
