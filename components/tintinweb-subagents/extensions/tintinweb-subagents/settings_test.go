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

func TestSettingsReachTheSession(t *testing.T) {
	r := startRig(t)
	cwd := r.Host.opts.Cwd
	writeSettings(t, filepath.Join(cwd, ".pi", "subagents.json"), `{"maxConcurrent":1,"maxConcurrentForeground":2,"fallbackSubagent":"none","defaultMaxTurns":9,"graceTurns":4,
		"defaultJoinMode":"group","backgroundByDefault":false,"scopeModels":true,"toolDescriptionMode":"compact","strictAgentFiles":false}`)
	r.app.startup(cwd)
	t.Cleanup(func() { setScopeModelsEnabled(false) })
	eq(t, r.app.mgr.getMaxConcurrent(), 1)
	eq(t, r.app.mgr.getMaxConcurrentForeground(), 2)
	rt := r.app.runtime()
	eq(t, [4]any{*rt.defaultMaxTurns, rt.graceTurns, rt.defaultJoinMode, rt.backgroundByDefault}, [4]any{9, 4, "group", false})
	eq(t, [2]any{isScopeModelsEnabled(), rt.toolDescriptionMode}, [2]any{true, "compact"})
	out := r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "typoo"})
	eq(t, out[:30], `Unknown or disabled agent type`)
	r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "Plan", "run_in_background": true})
	spec := r.fleet.wait(t, 1)[0].spec
	eq(t, [2]int{spec.MaxTurns, spec.GraceTurns}, [2]int{9, 4})
}

func TestSettingsOfOtherSlicesAreStoredNotApplied(t *testing.T) {
	r := startRig(t)
	cwd := r.Host.opts.Cwd
	writeSettings(t, filepath.Join(cwd, ".pi", "subagents.json"), `{"widgetMode":"all","maxSubagentDepth":3,"schedulingEnabled":false,"showCost":true}`)
	r.app.startup(cwd)
	v := settingsView()
	eq(t, [4]any{v.widgetMode(), v.maxSubagentDepth(), v.schedulingEnabled(), v.showCost()}, [4]any{"all", 3, false, true})
	eq(t, settingsView().workflowsEnabled(), true) // unset reads as the original's default
}
