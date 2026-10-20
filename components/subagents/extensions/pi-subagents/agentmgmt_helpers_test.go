package pi_subagents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Helpers shared by the twins of the agent settings, discovery and management tests.

type amEnv struct{ home, project string }

// newAmEnv isolates HOME and creates an empty project, as the original's beforeEach does.
func newAmEnv(t *testing.T) amEnv {
	t.Helper()
	home := withTempHome(t)
	clearAgentDiscoveryCache()
	t.Cleanup(clearAgentDiscoveryCache)
	return amEnv{home: home, project: tmp(t)}
}

func (e amEnv) userSettings() string { return filepath.Join(e.home, ".pi", "agent", "settings.json") }
func (e amEnv) projectSettings() string {
	return filepath.Join(e.project, ".pi", "settings.json")
}

// wj writes v (decoded-JSON shapes: map[string]any, []any, string, float64, bool) as a JSON file.
func wj(t *testing.T, p string, v any) {
	t.Helper()
	writeFile(t, p, marshalJSON(v, "  "))
}

func (e amEnv) projectAgent(t *testing.T, name, body string) {
	t.Helper()
	writeFile(t, filepath.Join(e.project, ".pi", "agents", name+".md"), body)
}

func (e amEnv) userAgent(t *testing.T, name, body string) {
	t.Helper()
	writeFile(t, filepath.Join(e.home, ".pi", "agent", "agents", name+".md"), body)
}

func (e amEnv) mkProjectPi(t *testing.T) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(e.project, ".pi"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func dAll(t *testing.T, project string) DiscoveryAll {
	t.Helper()
	a, err := discoverAgentsAll(project, "", DiscoveryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func dAgents(t *testing.T, project string, scope AgentScope, provider string) Discovery {
	t.Helper()
	d, err := discoverAgents(project, scope, provider, DiscoveryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// errHas asserts an error whose message contains every substring.
func errHas(t *testing.T, err error, subs ...string) {
	t.Helper()
	if err == nil {
		t.Errorf("no error, want one containing %q", subs)
		return
	}
	for _, s := range subs {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("error %q does not contain %q", err.Error(), s)
		}
	}
}

func discErr(project string, scope AgentScope) error {
	_, err := discoverAgents(project, scope, "", DiscoveryOptions{})
	return err
}
