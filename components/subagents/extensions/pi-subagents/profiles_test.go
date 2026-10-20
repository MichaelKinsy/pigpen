package pi_subagents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type probeCall struct {
	command, cwd string
	args         []string
}

func TestProfiles(t *testing.T) {
	const f = "profiles"
	setup := func(t *testing.T) (home string) {
		home = withTempHome(t)
		t.Setenv("PI_OFFLINE", "true")
		clearAgentDiscoveryCache()
		t.Cleanup(clearAgentDiscoveryCache)
		t.Setenv("PIG_SUBAGENT_PIG_BINARY", filepath.Join(home, "pi-test-bin"))
		return home
	}
	models := func(ms ...map[string]any) func() []map[string]any { return func() []map[string]any { return ms } }
	okExec := func(string, []string, string, int) (string, string, int, bool, error) {
		return "OK\n", "", 0, false, nil
	}
	wp := func(t *testing.T, name string, v any) { wj(t, filepath.Join(profilesRootDir(), name), v) }
	read := func(t *testing.T, p string) *jsObject {
		o, err := readJSONObjectFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	m := func(kv ...any) map[string]any {
		out := map[string]any{}
		for i := 0; i < len(kv); i += 2 {
			out[kv[i].(string)] = kv[i+1]
		}
		return out
	}

	tw(t, f, "lists no profiles when the directory is empty", func(t *testing.T) {
		setup(t)
		names, err := listSubagentProfiles()
		if err != nil {
			t.Fatal(err)
		}
		eq(t, len(names), 0, "profiles")
		_, err = os.Stat(profilesRootDir())
		eq(t, err == nil, true, "directory created")
	})
	tw(t, f, "applies a saved profile while preserving unrelated subagent settings", func(t *testing.T) {
		home := setup(t)
		wp(t, "openai-codex.quota.json", js(t, `{"subagents":{"agentOverrides":{"scout":{"model":"openai-codex/gpt-5.3-codex-spark","thinking":"medium"},"reviewer":{"thinking":false}}}}`))
		settings := filepath.Join(home, ".pi", "agent", "settings.json")
		wj(t, settings, js(t, `{"defaultModel":"openai/gpt-5","subagents":{"disableBuiltins":true,"modelScope":{"enforce":true,"allow":["openai-codex/*"]},"agentOverrides":{"scout":{"model":"old"},"stale":{"model":"remove-me"}}}}`))
		file, path, err := applySubagentProfile("openai-codex.quota")
		if err != nil {
			t.Fatal(err)
		}
		eq(t, path, settings, "settingsPath")
		eq(t, strings.HasSuffix(file, "openai-codex.quota.json"), true, "filePath")
		written := read(t, settings)
		eq(t, jsonString(written.vals["defaultModel"]), `"openai/gpt-5"`, "defaultModel")
		sub := written.obj("subagents")
		eq(t, jsonString(sub.vals["disableBuiltins"]), `true`, "disableBuiltins")
		eq(t, jsonString(sub.vals["modelScope"]), `{"enforce":true,"allow":["openai-codex/*"]}`, "modelScope")
		eq(t, jsonString(sub.vals["agentOverrides"]), `{"scout":{"model":"openai-codex/gpt-5.3-codex-spark","thinking":"medium"},"reviewer":{"thinking":false}}`, "overrides")
	})
	tw(t, f, "keeps machine placement when a model profile replaces the override map", func(t *testing.T) {
		home := setup(t)
		wp(t, "quota.json", js(t, `{"subagents":{"agentOverrides":{"scout":{"model":"openai-codex/gpt-5.3-codex-spark"},"claude-code":{"thinking":false}}}}`))
		settings := filepath.Join(home, ".pi", "agent", "settings.json")
		wj(t, settings, js(t, `{"subagents":{"agentOverrides":{"claude-code":{"machine":"workmac","model":"old"},"codex-exec":{"machine":"workmac"},"stale":{"model":"remove-me"}}}}`))
		if _, _, err := applySubagentProfile("quota"); err != nil {
			t.Fatal(err)
		}
		eq(t, jsonString(read(t, settings).obj("subagents").vals["agentOverrides"]), `{"scout":{"model":"openai-codex/gpt-5.3-codex-spark"},"claude-code":{"thinking":false,"machine":"workmac"},"codex-exec":{"machine":"workmac"}}`, "overrides")
	})
	tw(t, f, "applies profile models and thinking to user agents without frontmatter pins", func(t *testing.T) {
		home := setup(t)
		wp(t, "azure-fallback.json", js(t, `{"subagents":{"disableBuiltins":true,"agentOverrides":{"worker":{"model":"bluebox-azure-openai/gpt-5_6-luna","thinking":"high"}}}}`))
		writeFile(t, filepath.Join(home, ".pi", "agent", "agents", "worker.md"), "---\nname: worker\ndescription: Profile-managed worker\n---\n\nDo work.\n")
		if _, _, err := applySubagentProfile("azure-fallback"); err != nil {
			t.Fatal(err)
		}
		agents := dAgents(t, tmp(t), ScopeBoth, "").Agents
		w := findAgent(agents, "worker")
		eq(t, w != nil && w.Source == SourceUser, true, "worker source")
		eq(t, w.Model, "bluebox-azure-openai/gpt-5_6-luna", "model")
		eq(t, w.Thinking, any("high"), "thinking")
		eq(t, w.Override != nil && w.Override.Scope == "user", true, "override scope")
		for _, a := range agents {
			eq(t, a.Source == SourceBuiltin, false, "builtin "+a.Name)
		}
	})
	tw(t, f, "rejects removed profile fallback models", func(t *testing.T) {
		setup(t)
		wp(t, "invalid.json", js(t, `{"subagents":{"agentOverrides":{"worker":{"fallbackModels":["openai/gpt-5",42]}}}}`))
		_, _, err := applySubagentProfile("invalid")
		hasMatch(t, err, `removed field fallbackModels`)
	})
	tw(t, f, "rejects profile and provider path traversal names", func(t *testing.T) {
		setup(t)
		_, _, err := applySubagentProfile("../escape")
		hasMatch(t, err, `safe file name`)
		_, err = providerModelsPath("../escape")
		hasMatch(t, err, `safe file name`)
		_, err = profileHost{exec: okExec, models: models()}.refreshProviderModelCatalog("../escape", refreshOptions{})
		hasMatch(t, err, `safe file name`)
	})
	tw(t, f, "refreshes a provider model catalog using the resolved Pi executable and writes a cache file", func(t *testing.T) {
		home := setup(t)
		piBinary := filepath.Join(home, "pi-wrapper.exe")
		t.Setenv("PIG_SUBAGENT_PIG_BINARY", piBinary)
		var calls []probeCall
		host := profileHost{exec: func(command string, args []string, cwd string, _ int) (string, string, int, bool, error) {
			calls = append(calls, probeCall{command, cwd, args})
			return "OK\n", "", 0, false, nil
		}, models: models(
			m("provider", "openai-codex", "id", "gpt-5.3-codex-spark", "reasoning", true, "name", "Spark"),
			m("provider", "openai-codex", "id", "gpt-5.4-mini", "reasoning", true, "name", "Mini"),
			m("provider", "openai-codex", "id", "gpt-5.4", "reasoning", true, "name", "Base"),
			m("provider", "openai-codex", "id", "gpt-5.5", "reasoning", true, "name", "Best"))}
		r, err := host.refreshProviderModelCatalog("openai-codex", refreshOptions{})
		if err != nil {
			t.Fatal(err)
		}
		eq(t, r.Reused, false, "reused")
		_, statErr := os.Stat(r.FilePath)
		eq(t, statErr == nil, true, "file")
		eq(t, len(r.Catalog.Models), 4, "models")
		eq(t, len(calls), 4, "probes")
		for _, c := range calls {
			eq(t, c.command, piBinary, "command")
			eq(t, strings.Contains(strings.Join(c.args, " "), "--no-tools"), true, "--no-tools")
			eq(t, strings.Contains(strings.Join(c.args, " "), "--tools read"), false, "--tools read")
			eq(t, c.cwd, os.TempDir(), "cwd")
		}
		var ids []string
		for _, mm := range r.Catalog.Models {
			ids = append(ids, mm.FullID)
		}
		eq(t, ids, []string{"openai-codex/gpt-5.3-codex-spark", "openai-codex/gpt-5.4-mini", "openai-codex/gpt-5.4", "openai-codex/gpt-5.5"}, "order")
		c, err := readProviderModelCatalog("openai-codex")
		eq(t, err == nil && c != nil && c.Provider == "openai-codex", true, "readProviderModelCatalog")
		eq(t, r.HeuristicFallbackCount, 4, "heuristic")
		eq(t, r.Catalog.Models[0].Derived.Sources, []string{"heuristic-name"}, "sources")
		eq(t, r.Catalog.Models[0].Warnings, []string{"Classification fell back to name heuristics."}, "warnings")
	})
	tw(t, f, "does not count heuristic fallback when official metadata is present", func(t *testing.T) {
		setup(t)
		host := profileHost{exec: okExec, models: models(
			m("provider", "openai", "id", "gpt-5-mini", "name", "GPT-5 Mini", "reasoning", true, "contextWindow", 128000.0, "maxTokens", 16000.0, "cost", m("input", 0.25, "output", 2.0)),
			m("provider", "openai", "id", "gpt-5", "name", "GPT-5", "reasoning", true, "contextWindow", 200000.0, "maxTokens", 32000.0, "cost", m("input", 1.25, "output", 10.0)))}
		r, err := host.refreshProviderModelCatalog("openai", refreshOptions{})
		if err != nil {
			t.Fatal(err)
		}
		eq(t, r.HeuristicFallbackCount, 0, "heuristic")
		eq(t, r.Catalog.Models[0].Derived.Sources, []string{"official-metadata", "heuristic-name"}, "sources")
		eq(t, len(r.Catalog.Models[0].Warnings), 0, "warnings")
	})
	tw(t, f, "reuses a fresh provider model catalog", func(t *testing.T) {
		setup(t)
		path, _ := providerModelsPath("openai-codex")
		wj(t, path, obj("provider", "openai-codex", "refreshedAt", time.Now().UTC().Format("2006-01-02T15:04:05.000Z"), "maxAgeDays", float64(defaultProviderModelsMaxAgeDays), "sources", []any{"runtime-registry"}, "models", []any{}))
		host := profileHost{exec: func(string, []string, string, int) (string, string, int, bool, error) {
			t.Error("should not probe fresh cache")
			return "", "", 0, false, nil
		}, models: models()}
		r, err := host.refreshProviderModelCatalog("openai-codex", refreshOptions{})
		if err != nil {
			t.Fatal(err)
		}
		eq(t, r.Reused, true, "reused")
	})
	tw(t, f, "generates quota and quality profiles from sorted provider models", func(t *testing.T) {
		setup(t)
		host := profileHost{exec: okExec, models: models(
			m("provider", "openai-codex", "id", "gpt-5.3-codex-spark", "reasoning", true), m("provider", "openai-codex", "id", "gpt-5.4-mini", "reasoning", true),
			m("provider", "openai-codex", "id", "gpt-5.4", "reasoning", true), m("provider", "openai-codex", "id", "gpt-5.5", "reasoning", true))}
		r, err := host.generateProfilesForProvider("openai-codex", refreshOptions{})
		if err != nil {
			t.Fatal(err)
		}
		eq(t, r.HeuristicFallbackCount, 4, "heuristic")
		eq(t, r.SelectedHeuristicFallbackCount, 4, "selected heuristic")
		eq(t, r.QuotaModels, tierModels{"openai-codex/gpt-5.3-codex-spark", "openai-codex/gpt-5.4-mini", "openai-codex/gpt-5.4-mini"}, "quota")
		eq(t, r.QualityModels, tierModels{"openai-codex/gpt-5.4-mini", "openai-codex/gpt-5.4", "openai-codex/gpt-5.5"}, "quality")
	})
	tw(t, f, "does not treat provider names like MiniMax as mini-tier tokens and prefers M3 over M2.7-highspeed for quality", func(t *testing.T) {
		setup(t)
		host := profileHost{exec: okExec, models: models(
			m("provider", "minimax", "id", "MiniMax-M2.7", "name", "MiniMax-M2.7", "reasoning", true, "contextWindow", 204800.0, "maxTokens", 131072.0, "cost", m("input", 0.3, "output", 1.2, "cacheRead", 0.06, "cacheWrite", 0.375)),
			m("provider", "minimax", "id", "MiniMax-M3", "name", "MiniMax-M3", "reasoning", true, "contextWindow", 512000.0, "maxTokens", 128000.0, "cost", m("input", 0.6, "output", 2.4, "cacheRead", 0.12, "cacheWrite", 0.0)),
			m("provider", "minimax", "id", "MiniMax-M2.7-highspeed", "name", "MiniMax-M2.7-highspeed", "reasoning", true, "contextWindow", 204800.0, "maxTokens", 131072.0, "cost", m("input", 0.6, "output", 2.4, "cacheRead", 0.06, "cacheWrite", 0.375)))}
		refreshed, err := host.refreshProviderModelCatalog("minimax", refreshOptions{})
		if err != nil {
			t.Fatal(err)
		}
		var highspeed, m3 *catalogModel
		for i := range refreshed.Catalog.Models {
			switch refreshed.Catalog.Models[i].FullID {
			case "minimax/MiniMax-M2.7-highspeed":
				highspeed = &refreshed.Catalog.Models[i]
			case "minimax/MiniMax-M3":
				m3 = &refreshed.Catalog.Models[i]
			}
		}
		eq(t, highspeed.Derived.LatencyTier, "fast", "latency")
		eq(t, m3.Derived.ProfileRank > highspeed.Derived.ProfileRank, true, "M3 outranks highspeed")
		r, err := host.generateProfilesForProvider("minimax", refreshOptions{Force: true})
		if err != nil {
			t.Fatal(err)
		}
		eq(t, r.QuotaModels, tierModels{"minimax/MiniMax-M2.7", "minimax/MiniMax-M2.7", "minimax/MiniMax-M2.7"}, "quota")
		eq(t, r.QualityModels.Strong, "minimax/MiniMax-M3", "quality strong")
		for _, id := range []string{r.QuotaModels.Cheap, r.QuotaModels.Medium, r.QuotaModels.Strong, r.QualityModels.Cheap, r.QualityModels.Medium, r.QualityModels.Strong} {
			eq(t, id == "minimax/MiniMax-M2.7-highspeed", false, "highspeed selected")
		}
	})
	tw(t, f, "checks a profile against the registry and live probe", func(t *testing.T) {
		setup(t)
		wp(t, "demo.json", js(t, `{"subagents":{"agentOverrides":{"scout":{"model":"openai-codex/gpt-5.3-codex-spark"},"worker":{"model":"openai-codex/gpt-5.9"}}}}`))
		host := profileHost{exec: func(_ string, args []string, _ string, _ int) (string, string, int, bool, error) {
			if args[2] == "openai-codex/gpt-5.9" {
				return "", "model unavailable", 1, false, nil
			}
			return "OK\n", "", 0, false, nil
		}, models: models(m("provider", "openai-codex", "id", "gpt-5.3-codex-spark"))}
		r, err := host.checkSubagentProfile("demo")
		if err != nil {
			t.Fatal(err)
		}
		eq(t, r.Results, []profileCheckEntry{
			{"scout", "openai-codex/gpt-5.3-codex-spark", true, probeResult{"ok", "OK"}},
			{"worker", "openai-codex/gpt-5.9", false, probeResult{"unavailable", "model unavailable"}},
		}, "results")
	})
	tw(t, f, "checks short model ids and thinking suffixes against the registry", func(t *testing.T) {
		setup(t)
		wp(t, "demo.json", js(t, `{"subagents":{"agentOverrides":{"scout":{"model":"gpt-5.3-codex-spark:high"}}}}`))
		var probed []string
		host := profileHost{exec: func(_ string, args []string, _ string, _ int) (string, string, int, bool, error) {
			probed = append(probed, args[2])
			eq(t, strings.Contains(strings.Join(args, " "), "--no-tools"), true, "--no-tools")
			return "OK\n", "", 0, false, nil
		}, models: models(m("provider", "openai-codex", "id", "gpt-5.3-codex-spark"))}
		r, err := host.checkSubagentProfile("demo")
		if err != nil {
			t.Fatal(err)
		}
		eq(t, r.Results[0].InRegistry, true, "inRegistry")
		eq(t, probed, []string{"openai-codex/gpt-5.3-codex-spark:high"}, "probed")
	})
}
