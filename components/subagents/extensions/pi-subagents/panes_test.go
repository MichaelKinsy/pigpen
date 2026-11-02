package pi_subagents

import (
	"path/filepath"
	"testing"
)

func TestHerdrPanesSetting(t *testing.T) {
	home := withTempHome(t)
	cwd := tmp(t)
	userSettings := filepath.Join(home, ".pi", "agent", "settings.json")
	projectSettings := filepath.Join(cwd, ".pi", "settings.json")

	d, err := subagentSettingsFor(cwd, ScopeBoth)
	eq(t, err, nil, "error")
	eq(t, d.herdrPanes == nil, true, "unset by default")

	writeFile(t, userSettings, `{"subagents": {"herdrPanes": true}}`)
	d, _ = subagentSettingsFor(cwd, ScopeBoth)
	eq(t, d.herdrPanes != nil && *d.herdrPanes, true, "the user's")

	writeFile(t, projectSettings, `{"subagents": {"herdrPanes": false}}`)
	d, _ = subagentSettingsFor(cwd, ScopeBoth)
	eq(t, d.herdrPanes != nil && !*d.herdrPanes, true, "the project's wins")
	d, _ = subagentSettingsFor(cwd, ScopeUser)
	eq(t, d.herdrPanes != nil && *d.herdrPanes, true, "scope user reads no project settings")

	writeFile(t, projectSettings, `{"subagents": {"herdrPanes": "yes"}}`)
	_, err = subagentSettingsFor(cwd, ScopeBoth)
	hasMatch(t, err, "Subagent settings in '"+projectSettings+"' have invalid 'herdrPanes'; expected true or false.")
}

func TestPanesEnabled(t *testing.T) {
	yes, no := true, false
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	both := map[string]string{"HC_TERMINAL_SOCKET": "/s", "HC_TERMINAL_ID": "t1"}
	for _, c := range []struct {
		name string
		set  *bool
		env  map[string]string
		goos string
		want bool
	}{
		{"darwin", &yes, both, "darwin", true},
		{"linux", &yes, both, "linux", true},
		{"unset", nil, both, "darwin", false},
		{"false", &no, both, "darwin", false},
		{"no socket", &yes, map[string]string{"HC_TERMINAL_ID": "t1"}, "darwin", false},
		{"no id", &yes, map[string]string{"HC_TERMINAL_SOCKET": "/s"}, "darwin", false},
		{"windows", &yes, both, "windows", false},
	} {
		eq(t, panesEnabled(subagentSettings{herdrPanes: c.set}, env(c.env), c.goos), c.want, c.name)
	}
}
