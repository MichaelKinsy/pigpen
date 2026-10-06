package ponytail

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type obj = map[string]any

func eq(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

func setEnv(t *testing.T, k, v string) { t.Helper(); t.Setenv(k, v) }
func unsetEnv(t *testing.T, k string)  { t.Helper(); t.Setenv(k, ""); os.Unsetenv(k) }

// configHome points XDG_CONFIG_HOME at a fresh directory and clears the ponytail variables.
func configHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	setEnv(t, "XDG_CONFIG_HOME", dir)
	for _, k := range []string{"PONYTAIL_DEFAULT_MODE", "PONYTAIL_QUIET_STARTUP", "PONYTAIL_HIDE_STATUS"} {
		unsetEnv(t, k)
	}
	return dir
}

func customEntry(mode string) obj {
	return obj{"type": "custom", "customType": "ponytail-mode", "data": obj{"mode": mode}}
}

func TestHelpers(t *testing.T) {
	const f = "helpers"
	tw(t, f, "parsePonytailCommand falls back to full when invoked bare and default is off", func(t *testing.T) {
		eq(t, parsePonytailCommand("", "off"), command{Type: "set-mode", Mode: "full"})
	})
	tw(t, f, "parsePonytailCommand parses modes, status, and default subcommand", func(t *testing.T) {
		eq(t, parsePonytailCommand("ultra", "full"), command{Type: "set-mode", Mode: "ultra"})
		eq(t, parsePonytailCommand("status", "full"), command{Type: "status"})
		eq(t, parsePonytailCommand("default lite", "full"), command{Type: "set-default", Mode: "lite"})
	})
	tw(t, f, "parsePonytailCommand rejects review as a default (session-only mode, #377)", func(t *testing.T) {
		eq(t, parsePonytailCommand("default review", "full"), command{Type: "invalid", Reason: "invalid-default-mode"})
	})
	tw(t, f, "resolveSessionMode still honors review as a session mode (not a default)", func(t *testing.T) {
		eq(t, resolveSessionMode([]obj{customEntry("review")}, "full"), "review")
	})
	tw(t, f, "resolveSessionMode prefers latest persisted session mode", func(t *testing.T) {
		eq(t, resolveSessionMode([]obj{customEntry("lite"), customEntry("ultra")}, "full"), "ultra")
	})
	tw(t, f, "resolveSessionMode returns fallback when entries is not an array", func(t *testing.T) {
		eq(t, resolveSessionMode(nil, "ultra"), "ultra")
		eq(t, resolveSessionMode(obj{}, "full"), "full")
		eq(t, resolveSessionMode("not an array", ""), "full") // DEFAULT_MODE fallback
	})
	tw(t, f, "readDefaultMode and writeDefaultMode use XDG config path", func(t *testing.T) {
		dir := configHome(t)
		path := filepath.Join(dir, "ponytail", "config.json")
		eq(t, getDefaultMode(), "full")
		w, err := writeDefaultMode("ultra")
		eq(t, err, nil)
		eq(t, w, "ultra")
		eq(t, getDefaultMode(), "ultra")
		data, err := os.ReadFile(path)
		eq(t, err, nil)
		var got obj
		json.Unmarshal(data, &got)
		eq(t, got, obj{"defaultMode": "ultra"})
	})
	tw(t, f, "readQuietStartup resolves env var, config file, and default in that order", func(t *testing.T) {
		dir := configHome(t)
		eq(t, getQuietStartup(), false)
		os.MkdirAll(filepath.Join(dir, "ponytail"), 0o755)
		os.WriteFile(filepath.Join(dir, "ponytail", "config.json"), []byte(`{"quietStartup":true}`), 0o644)
		eq(t, getQuietStartup(), true)
		setEnv(t, "PONYTAIL_QUIET_STARTUP", "false")
		eq(t, getQuietStartup(), false)
		setEnv(t, "PONYTAIL_QUIET_STARTUP", "1")
		eq(t, getQuietStartup(), true)
	})
	tw(t, f, "filterSkillBodyForMode keeps only requested intensity examples and rows", func(t *testing.T) {
		body := "---\nname: ponytail\n---\n| **lite** | keep lite |\n| **full** | keep full |\n| **ultra** | keep ultra |\n- lite: \"Lite example\"\n- full: \"Full example\"\n- ultra: \"Ultra example\"\nOther line"
		got := filterSkillBodyForMode(body, "ultra")
		for _, no := range []string{"keep lite", "keep full", "Lite example"} {
			eq(t, strings.Contains(got, no), false)
		}
		for _, yes := range []string{"keep ultra", "Ultra example", "Other line"} {
			eq(t, strings.Contains(got, yes), true)
		}
	})
	tw(t, f, "filterSkillBodyForMode does not drop a rule bullet whose label matches a mode name", func(t *testing.T) {
		body := "- Full: do not confuse this rule label with the mode name.\n- Lite: same risk, this is a real rule bullet.\n- lite: \"real worked example\"\n- ultra: \"real worked example\""
		got := filterSkillBodyForMode(body, "ultra")
		eq(t, strings.Contains(got, "Full: do not confuse"), true)
		eq(t, strings.Contains(got, "Lite: same risk"), true)
		eq(t, strings.Contains(got, "- lite:"), false)
		eq(t, strings.Contains(got, "ultra: \"real worked example\""), true)
	})
	tw(t, f, "filterSkillBodyForMode keeps rule bullets that contain a colon", func(t *testing.T) {
		got := filterSkillBodyForMode(string(skillBody), "full")
		for _, yes := range []string{"No unrequested abstractions", "Mark deliberate simplifications that cut a real corner", "`ponytail:` comment naming the ceiling and upgrade path", "full: \"`@lru_cache"} {
			eq(t, strings.Contains(got, yes), true)
		}
		eq(t, strings.Contains(got, "lite: \"Done"), false)
		eq(t, strings.Contains(got, "ultra: \"No cache"), false)
	})
}

func writeConfig(t *testing.T, dir, body string) {
	t.Helper()
	os.MkdirAll(filepath.Join(dir, "ponytail"), 0o755)
	if err := os.WriteFile(filepath.Join(dir, "ponytail", "config.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
