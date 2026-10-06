package tintinweb_subagents

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSettings(t *testing.T, path, body string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSettingsMergeAndSanitize(t *testing.T) {
	r := newCARig(t)
	global := filepath.Join(r.tmp, "g")
	t.Setenv("PIG_CODING_AGENT_DIR", global)
	writeSettings(t, filepath.Join(global, "subagents.json"), `{"maxConcurrent":3,"defaultMaxTurns":20,"fallbackSubagent":"router","disableDefaultAgents":true}`)
	writeSettings(t, filepath.Join(r.tmp, ".pi", "subagents.json"), `{"maxConcurrent":2,"fallbackSubagent":false}`)
	s := loadSettings(r.tmp)
	eq(t, s.MaxConcurrent, 2)
	eq(t, *s.DefaultMaxTurns, 20)
	eq(t, *s.FallbackSubagent, "none")
	eq(t, *s.DisableDefaultAgents, true)
	t.Run("a field of the wrong shape is dropped", func(t *testing.T) {
		s := sanitizeSettings(map[string]any{"maxConcurrent": 0.0, "defaultMaxTurns": -1.0, "fallbackSubagent": "  ", "disableDefaultAgents": "yes"})
		eq(t, s, settings{})
		eq(t, sanitizeSettings(map[string]any{"maxConcurrent": 1025.0}).MaxConcurrent, 0)
		eq(t, sanitizeSettings(map[string]any{"maxConcurrent": 2.5}).MaxConcurrent, 0)
		eq(t, *sanitizeSettings(map[string]any{"defaultMaxTurns": 0.0}).DefaultMaxTurns, 0)
		eq(t, sanitizeSettings("nope"), settings{})
	})
	t.Run("a file that is not JSON reads as empty", func(t *testing.T) {
		writeSettings(t, filepath.Join(r.tmp, "bad.json"), `{nope`)
		eq(t, readSettingsFile(filepath.Join(r.tmp, "bad.json")), settings{})
		eq(t, readSettingsFile(filepath.Join(r.tmp, "missing.json")), settings{})
	})
}

func TestSettingsReachTheSession(t *testing.T) {
	r := startRig(t)
	cwd := r.Host.opts.Cwd
	writeSettings(t, filepath.Join(cwd, ".pi", "subagents.json"), `{"maxConcurrent":1,"fallbackSubagent":"none","defaultMaxTurns":9}`)
	r.app.reload(cwd)
	eq(t, r.app.mgr.maxConcurrent, 1)
	out := r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "typoo"})
	eq(t, out[:30], `Unknown or disabled agent type`)
	r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "Plan"})
	eq(t, r.fleet.wait(t, 1)[0].spec.MaxTurns, 9)
}
