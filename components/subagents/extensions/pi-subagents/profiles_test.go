package pi_subagents

// Twins of the original's test/unit/profiles.test.ts (the portable titles):
// listing, applying with unrelated settings preserved, machine survival,
// validation rejections and the path-traversal guard. The catalog titles
// (generate/refresh/check with live probes) stay deferred.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeProfileFixture(t *testing.T, name, content string) {
	t.Helper()
	writeFile(t, filepath.Join(getSubagentProfilesDir(), name), content)
}

func readSettingsMap(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile(userAgentSettingsPath())
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(b, &settings); err != nil {
		t.Fatal(err)
	}
	return settings
}

func TestProfiles(t *testing.T) {
	const f = "profiles"
	tw(t, f, "lists no profiles when the directory is empty", func(t *testing.T) {
		withTempHome(t)
		names, errText := listSubagentProfiles()
		if errText != "" {
			t.Fatal(errText)
		}
		if len(names) != 0 {
			t.Errorf("names %v", names)
		}
		if st, err := os.Stat(getSubagentProfilesDir()); err != nil || !st.IsDir() {
			t.Errorf("profiles dir should exist: %v", err)
		}
	})
	tw(t, f, "applies a saved profile while preserving unrelated subagent settings", func(t *testing.T) {
		home := withTempHome(t)
		writeProfileFixture(t, "openai-codex.quota.json", `{
 "subagents": {
  "agentOverrides": {
   "scout": {"model": "openai-codex/gpt-5.3-codex-spark", "thinking": "medium"},
   "reviewer": {"thinking": false}
  }
 }
}`)
		writeFile(t, filepath.Join(home, ".pi", "agent", "settings.json"), `{
 "defaultModel": "openai/gpt-5",
 "subagents": {
  "disableBuiltins": true,
  "modelScope": {"enforce": true, "allow": ["openai-codex/*"]},
  "agentOverrides": {"scout": {"model": "old"}, "stale": {"model": "remove-me"}}
 }
}`)
		filePath, settingsPath, errText := applySubagentProfile("openai-codex.quota")
		if errText != "" {
			t.Fatal(errText)
		}
		if settingsPath != userAgentSettingsPath() {
			t.Errorf("settingsPath %q", settingsPath)
		}
		if !strings.HasSuffix(filePath, "openai-codex.quota.json") {
			t.Errorf("filePath %q", filePath)
		}
		written := readSettingsMap(t)
		if written["defaultModel"] != "openai/gpt-5" {
			t.Errorf("defaultModel %v", written["defaultModel"])
		}
		subagents := written["subagents"].(map[string]any)
		if subagents["disableBuiltins"] != true {
			t.Errorf("disableBuiltins %v", subagents["disableBuiltins"])
		}
		wantScope := map[string]any{"enforce": true, "allow": []any{"openai-codex/*"}}
		gotScope := subagents["modelScope"].(map[string]any)
		if len(gotScope) != len(wantScope) || gotScope["enforce"] != true {
			t.Errorf("modelScope %v", gotScope)
		}
		overrides := subagents["agentOverrides"].(map[string]any)
		if len(overrides) != 2 {
			t.Fatalf("overrides %v", overrides)
		}
		scout := overrides["scout"].(map[string]any)
		if scout["model"] != "openai-codex/gpt-5.3-codex-spark" || scout["thinking"] != "medium" {
			t.Errorf("scout %v", scout)
		}
		reviewer := overrides["reviewer"].(map[string]any)
		if reviewer["thinking"] != false {
			t.Errorf("reviewer %v", reviewer)
		}
	})
	tw(t, f, "keeps machine placement when a model profile replaces the override map", func(t *testing.T) {
		home := withTempHome(t)
		writeProfileFixture(t, "quota.json",
			`{"subagents": {"agentOverrides": {"scout": {"model": "openai-codex/gpt-5.3-codex-spark"}, "claude-code": {"thinking": false}}}}`)
		writeFile(t, filepath.Join(home, ".pi", "agent", "settings.json"),
			`{"subagents": {"agentOverrides": {"claude-code": {"machine": "workmac", "model": "old"}, "codex-exec": {"machine": "workmac"}, "stale": {"model": "remove-me"}}}}`)
		if _, _, errText := applySubagentProfile("quota"); errText != "" {
			t.Fatal(errText)
		}
		overrides := readSettingsMap(t)["subagents"].(map[string]any)["agentOverrides"].(map[string]any)
		if len(overrides) != 3 {
			t.Fatalf("overrides %v", overrides)
		}
		scout := overrides["scout"].(map[string]any)
		if len(scout) != 1 || scout["model"] != "openai-codex/gpt-5.3-codex-spark" {
			t.Errorf("scout %v", scout)
		}
		code := overrides["claude-code"].(map[string]any)
		if code["thinking"] != false || code["machine"] != "workmac" {
			t.Errorf("claude-code %v", code)
		}
		if _, present := overrides["stale"]; present {
			t.Errorf("stale should be gone: %v", overrides)
		}
		exec := overrides["codex-exec"].(map[string]any)
		if exec["machine"] != "workmac" || len(exec) != 1 {
			t.Errorf("codex-exec %v", exec)
		}
	})
	tw(t, f, "applies profile models and thinking to user agents without frontmatter pins", func(t *testing.T) {
		home := withTempHome(t)
		writeProfileFixture(t, "azure-fallback.json",
			`{"subagents": {"disableBuiltins": true, "agentOverrides": {"worker": {"model": "bluebox-azure-openai/gpt-5_6-luna", "thinking": "high"}}}}`)
		writeFile(t, filepath.Join(home, ".pi", "agent", "agents", "worker.md"),
			"---\nname: worker\ndescription: Profile-managed worker\n---\n\nDo work.\n")
		if _, _, errText := applySubagentProfile("azure-fallback"); errText != "" {
			t.Fatal(errText)
		}
		project := t.TempDir()
		d := DiscoverAgents(project, ScopeBoth)
		var worker *AgentConfig
		builtins := false
		for i := range d.Agents {
			a := &d.Agents[i]
			if a.Name == "worker" {
				worker = a
			}
			if a.Source == SourceBuiltin {
				builtins = true
			}
		}
		if worker == nil {
			t.Fatal("worker not discovered")
		}
		if worker.Source != SourceUser {
			t.Errorf("source %q", worker.Source)
		}
		if worker.Model != "bluebox-azure-openai/gpt-5_6-luna" {
			t.Errorf("model %q", worker.Model)
		}
		if worker.Thinking != "high" {
			t.Errorf("thinking %+v", worker.Thinking)
		}
		if worker.OverrideScope != "user" {
			t.Errorf("override scope %q", worker.OverrideScope)
		}
		if builtins {
			t.Error("builtins should be hidden by disableBuiltins")
		}
	})
	tw(t, f, "rejects removed profile fallback models", func(t *testing.T) {
		withTempHome(t)
		writeProfileFixture(t, "invalid.json",
			`{"subagents": {"agentOverrides": {"worker": {"fallbackModels": ["openai/gpt-5", 42]}}}}`)
		_, _, errText := applySubagentProfile("invalid")
		if !strings.Contains(errText, "removed field fallbackModels") {
			t.Errorf("err %q", errText)
		}
	})
	tw(t, f, "rejects profile and provider path traversal names", func(t *testing.T) {
		withTempHome(t)
		_, _, errText := applySubagentProfile("../escape")
		if !strings.Contains(errText, "safe file name") {
			t.Errorf("apply err %q", errText)
		}
		if _, errText := normalizePathToken("../escape", "Provider"); !strings.Contains(errText, "safe file name") {
			t.Errorf("provider err %q", errText)
		}
	})
}
