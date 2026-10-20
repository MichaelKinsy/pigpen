package pi_subagents

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Helpers shared by the twins of the agent settings, discovery and management tests.

type amEnv struct{ home, project string }

// newAmEnv isolates HOME and creates an empty project, as the original's beforeEach does.
func newAmEnv(t *testing.T) amEnv {
	t.Helper()
	home := withTempHome(t)
	// The original's tests that do not pin PI_OFFLINE would ask the host's npm for its global root; the twins stay offline.
	t.Setenv("PI_OFFLINE", "true")
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

func agentNames(agents []AgentConfig) []string {
	out := make([]string, len(agents))
	for i, a := range agents {
		out[i] = a.Name
	}
	return out
}

func hasName(agents []AgentConfig, name string) bool { return findAgent(agents, name) != nil }

// writeAgentFile writes `<dir>/<name>.md` the way the original's discovery tests do and returns its path.
func writeAgentFile(t *testing.T, dir, name, description string) string {
	t.Helper()
	p := filepath.Join(dir, name+".md")
	writeFile(t, p, "---\nname: "+name+"\ndescription: "+description+"\n---\n\n"+description+".\n")
	return p
}

func jsonString(v any) string { return marshalJSON(v, "") }

// diffValues reports the first paths at which two values differ (reflect.DeepEqual granularity).
func diffValues(t *testing.T, path string, got, want reflect.Value) {
	t.Helper()
	if reflect.DeepEqual(got.Interface(), want.Interface()) {
		return
	}
	switch got.Kind() {
	case reflect.Struct:
		for i := 0; i < got.NumField(); i++ {
			if got.Type().Field(i).IsExported() {
				diffValues(t, path+"."+got.Type().Field(i).Name, got.Field(i), want.Field(i))
			}
		}
	case reflect.Slice:
		if got.Len() != want.Len() {
			t.Errorf("%s: length %d vs %d", path, got.Len(), want.Len())
			return
		}
		for i := 0; i < got.Len(); i++ {
			diffValues(t, fmt.Sprintf("%s[%d]", path, i), got.Index(i), want.Index(i))
		}
	case reflect.Ptr:
		if got.IsNil() || want.IsNil() {
			t.Errorf("%s: %v vs %v", path, got.Interface(), want.Interface())
			return
		}
		diffValues(t, path, got.Elem(), want.Elem())
	default:
		t.Errorf("%s: %#v vs %#v", path, got.Interface(), want.Interface())
	}
}
