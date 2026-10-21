package tintinweb_subagents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Twins of test/model-resolver.test.ts, test/model-scope.test.ts and test/enabled-models.test.ts.

type mdl = map[string]any

func m3(id, name, provider string) mdl { return mdl{"id": id, "name": name, "provider": provider} }

var resolverModels = []mdl{
	m3("claude-opus-4-6", "Claude Opus 4.6", "anthropic"),
	m3("claude-sonnet-4-6", "Claude Sonnet 4.6", "anthropic"),
	m3("claude-haiku-4-5-20251001", "Claude Haiku 4.5", "anthropic"),
	m3("gpt-4o", "GPT-4o", "openai"),
	m3("gemini-2.5-pro", "Gemini 2.5 Pro", "google"),
}

// makeRegistry: find is by provider and id, getAll all of models, getAvailable only when available is given.
func makeRegistry(models []mdl, available ...[]mdl) modelRegistry {
	r := modelRegistry{
		GetAll: func() []map[string]any { return models },
		Find:   func(provider, id string) map[string]any { return findModel(models, provider, id) },
	}
	if len(available) > 0 {
		r.GetAvailable = func() []map[string]any { return available[0] }
	}
	return r
}

func TestModelResolverTwins(t *testing.T) {
	const f = "model-resolver"
	ok := func(t *testing.T, input string, reg modelRegistry, want mdl) {
		t.Helper()
		got, msg := resolveModel(input, reg)
		eq(t, msg, "")
		eq(t, got, want)
	}
	tw(t, f, "resolves exact provider/modelId", func(t *testing.T) {
		ok(t, "anthropic/claude-opus-4-6", makeRegistry(resolverModels), resolverModels[0])
	})
	tw(t, f, "resolves another exact provider/modelId", func(t *testing.T) { ok(t, "openai/gpt-4o", makeRegistry(resolverModels), resolverModels[3]) })
	tw(t, f, "falls through to fuzzy when exact provider/modelId not found", func(t *testing.T) {
		ok(t, "anthropic/haiku", makeRegistry(resolverModels), resolverModels[2])
	})
	tw(t, f, "matches exact model id without provider", func(t *testing.T) { ok(t, "claude-opus-4-6", makeRegistry(resolverModels), resolverModels[0]) })
	tw(t, f, "is case-insensitive", func(t *testing.T) { ok(t, "Claude-Opus-4-6", makeRegistry(resolverModels), resolverModels[0]) })
	tw(t, f, "matches exact id for non-anthropic models", func(t *testing.T) { ok(t, "gpt-4o", makeRegistry(resolverModels), resolverModels[3]) })
	tw(t, f, "matches 'haiku' to claude-haiku model", func(t *testing.T) { ok(t, "haiku", makeRegistry(resolverModels), resolverModels[2]) })
	tw(t, f, "matches 'sonnet' to claude-sonnet model", func(t *testing.T) { ok(t, "sonnet", makeRegistry(resolverModels), resolverModels[1]) })
	tw(t, f, "matches 'opus' to claude-opus model", func(t *testing.T) { ok(t, "opus", makeRegistry(resolverModels), resolverModels[0]) })
	tw(t, f, "matches 'gemini' to gemini model", func(t *testing.T) { ok(t, "gemini", makeRegistry(resolverModels), resolverModels[4]) })
	tw(t, f, "is case-insensitive for substring", func(t *testing.T) { ok(t, "HAIKU", makeRegistry(resolverModels), resolverModels[2]) })

	// id uses dashes and the name carries no version number: the case that failed before separators were normalized
	haiku := m3("claude-haiku-4-5", "Claude Haiku", "anthropic")
	dashReg := makeRegistry([]mdl{haiku})
	tw(t, f, "matches a dotted query to a dashed id", func(t *testing.T) { ok(t, "claude-haiku-4.5", dashReg, haiku) })
	tw(t, f, "matches a dotted provider/id query to a dashed id", func(t *testing.T) { ok(t, "anthropic/claude-haiku-4.5", dashReg, haiku) })
	tw(t, f, "matches a dashed query to a dotted id", func(t *testing.T) { ok(t, "gemini-2-5-pro", makeRegistry(resolverModels), resolverModels[4]) })

	haikuDot := m3("claude-haiku-4.5", "Claude Haiku", "anthropic")
	tw(t, f, "matches a dated provider/id config to an undated registry id", func(t *testing.T) {
		ok(t, "anthropic/claude-haiku-4-5-20251001", makeRegistry([]mdl{haiku}), haiku)
	})
	tw(t, f, "matches a dated config to an undated *dotted* registry id (date + separator)", func(t *testing.T) {
		ok(t, "anthropic/claude-haiku-4-5-20251001", makeRegistry([]mdl{haikuDot}), haikuDot)
	})
	tw(t, f, "still prefers an exact dated id when the registry has it", func(t *testing.T) {
		dated := m3("claude-haiku-4-5-20251001", "Claude Haiku 4.5", "anthropic")
		ok(t, "anthropic/claude-haiku-4-5-20251001", makeRegistry([]mdl{dated}), dated)
	})

	gatewayHaiku := m3("claude-haiku-4-5", "Claude Haiku", "openrouter")
	anthropicHaiku := m3("claude-haiku-4-5", "Claude Haiku", "anthropic")
	tw(t, f, "falls back to another provider when the named one lacks the model", func(t *testing.T) {
		ok(t, "anthropic/claude-haiku-4-5", makeRegistry([]mdl{gatewayHaiku}), gatewayHaiku)
	})
	tw(t, f, "prefers the named provider when it has the model", func(t *testing.T) {
		ok(t, "anthropic/claude-haiku-4-5", makeRegistry([]mdl{gatewayHaiku, anthropicHaiku}), anthropicHaiku)
	})
	tw(t, f, "still errors when no provider has the model", func(t *testing.T) {
		_, msg := resolveModel("anthropic/nonexistent-xyz", makeRegistry([]mdl{gatewayHaiku}))
		eq(t, msg != "", true)
	})
	tw(t, f, "matches 'Opus 4.6' via model name", func(t *testing.T) { ok(t, "Opus 4.6", makeRegistry(resolverModels), resolverModels[0]) })
	tw(t, f, "matches 'Haiku 4.5' via model name", func(t *testing.T) { ok(t, "Haiku 4.5", makeRegistry(resolverModels), resolverModels[2]) })
	tw(t, f, "matches 'anthropic opus' across provider and id", func(t *testing.T) {
		ok(t, "anthropic opus", makeRegistry(resolverModels), resolverModels[0])
	})
	tw(t, f, "matches 'google pro' across provider and id", func(t *testing.T) { ok(t, "google pro", makeRegistry(resolverModels), resolverModels[4]) })
	tw(t, f, "prefers exact id over substring", func(t *testing.T) { ok(t, "gpt-4o", makeRegistry(resolverModels), resolverModels[3]) })
	tw(t, f, "substring match prefers shorter model id (tighter fit)", func(t *testing.T) {
		ok(t, "opus", makeRegistry(resolverModels), resolverModels[0])
		ok(t, "sonnet", makeRegistry(resolverModels), resolverModels[1])
	})
	tw(t, f, "returns error string for unknown model", func(t *testing.T) {
		got, msg := resolveModel("nonexistent-model", makeRegistry(resolverModels))
		eq(t, got == nil, true)
		eq(t, contains(msg, `Model not found: "nonexistent-model"`), true)
		eq(t, contains(msg, "Available models:"), true)
	})
	tw(t, f, "error lists available models", func(t *testing.T) {
		_, msg := resolveModel("xyz", makeRegistry(resolverModels))
		eq(t, contains(msg, "anthropic/claude-opus-4-6"), true)
		eq(t, contains(msg, "openai/gpt-4o"), true)
	})
	tw(t, f, "empty string matches a model (multi-part vacuous truth)", func(t *testing.T) {
		got, msg := resolveModel("", makeRegistry(resolverModels))
		eq(t, msg, "")
		eq(t, got != nil, true)
	})
	tw(t, f, "uses getAvailable when present (filters to configured models)", func(t *testing.T) {
		_, msg := resolveModel("sonnet", makeRegistry(resolverModels, []mdl{resolverModels[0], resolverModels[2]}))
		eq(t, contains(msg, "Model not found"), true) // sonnet is in getAll but not in getAvailable
	})
	tw(t, f, "exact match fails when model is not in getAvailable (no auth)", func(t *testing.T) {
		_, msg := resolveModel("anthropic/claude-sonnet-4-6", makeRegistry(resolverModels, []mdl{resolverModels[0]}))
		eq(t, contains(msg, "Model not found"), true)
	})
	tw(t, f, "fuzzy matches against available models only", func(t *testing.T) {
		ok(t, "haiku", makeRegistry(resolverModels, []mdl{resolverModels[2]}), resolverModels[2])
	})
	similar := []mdl{m3("claude-sonnet-4-6", "Claude Sonnet 4.6", "anthropic"), m3("claude-sonnet-4-5-20241022", "Claude Sonnet 4.5", "anthropic"), m3("claude-haiku-4-5-20251001", "Claude Haiku 4.5", "anthropic")}
	tw(t, f, "'sonnet' prefers tighter id match (shorter id)", func(t *testing.T) { ok(t, "sonnet", makeRegistry(similar), similar[0]) })
	tw(t, f, "'sonnet 4.5' resolves to the 4.5 model via name", func(t *testing.T) { ok(t, "sonnet 4.5", makeRegistry(similar), similar[1]) })
	tw(t, f, "'4-6' picks the 4.6 model", func(t *testing.T) { ok(t, "4-6", makeRegistry(similar), similar[0]) })
	tw(t, f, "returns error with empty available list", func(t *testing.T) {
		_, msg := resolveModel("haiku", makeRegistry([]mdl{}))
		eq(t, contains(msg, "Model not found"), true)
	})
	tw(t, f, "gives a short label and a canonical id", func(t *testing.T) {
		name, id := describeModel("anthropic", "claude-sonnet-4-6", "Claude Sonnet 4.6")
		eq(t, [2]string{name, id}, [2]string{"sonnet 4.6", "anthropic/claude-sonnet-4-6"})
	})
	tw(t, f, "falls back to the id when the model has no display name", func(t *testing.T) {
		name, id := describeModel("openai-codex", "gpt-5.6-sol", "")
		eq(t, [2]string{name, id}, [2]string{"gpt-5.6-sol", "openai-codex/gpt-5.6-sol"})
	})
}

// scopeEnv is a project directory and an agent directory holding Pi's settings.json files.
type scopeEnv struct {
	t       *testing.T
	project string
}

func newScopeEnv(t *testing.T) *scopeEnv {
	t.Helper()
	// resolveEnabledModels memoizes on the patterns and the files' mtime and size: a fresh project directory per test
	// keeps one case's allowlist from being served to the next.
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("PIG_USE_PI_DIRS", "")
	prev := isScopeModelsEnabled()
	t.Cleanup(func() { setScopeModelsEnabled(prev) })
	return &scopeEnv{t: t, project: t.TempDir()}
}

func (e *scopeEnv) setEnabledModels(models ...string) {
	e.t.Helper()
	writeJSONFile(e.t, filepath.Join(e.project, ".pi", "settings.json"), obj{"enabledModels": models})
}

var scopeModels = []mdl{m3("claude-opus-4-6", "Claude Opus 4.6", "anthropic"), m3("claude-haiku-4-5", "Claude Haiku 4.5", "anthropic"), m3("claude-sonnet-4-6", "Claude Sonnet 4.6", "anthropic")}

func (e *scopeEnv) check(mutate ...func(*modelScopeCheck)) modelScopeVerdict {
	c := modelScopeCheck{provider: "anthropic", id: "claude-opus-4-6", cwd: e.project, registry: makeRegistry(scopeModels), callerSupplied: true, agentLabel: "scout"}
	for _, fn := range mutate {
		fn(&c)
	}
	return checkModelScope(c)
}

func TestModelScopeTwins(t *testing.T) {
	const f = "model-scope"
	tw(t, f, "is a no-op while the feature is off, even for an out-of-scope model", func(t *testing.T) {
		e := newScopeEnv(t)
		setScopeModelsEnabled(false)
		e.setEnabledModels("anthropic/claude-haiku-4-5")
		eq(t, e.check().kind, "ok")
	})
	tw(t, f, "is a no-op when no model was resolved", func(t *testing.T) {
		e := newScopeEnv(t)
		setScopeModelsEnabled(true)
		e.setEnabledModels("anthropic/claude-haiku-4-5")
		eq(t, e.check(func(c *modelScopeCheck) { c.provider, c.id = "", "" }).kind, "ok")
	})
	tw(t, f, "is a no-op when the user has no enabledModels configured", func(t *testing.T) {
		e := newScopeEnv(t)
		setScopeModelsEnabled(true)
		eq(t, e.check().kind, "ok")
	})
	tw(t, f, "is a no-op when enabledModels resolves to nothing usable", func(t *testing.T) {
		// Globs and bare ids are deliberately unsupported; an unresolvable list must disable the check rather than lock
		// the user out of every model.
		e := newScopeEnv(t)
		setScopeModelsEnabled(true)
		e.setEnabledModels("anthropic/*", "haiku")
		eq(t, e.check().kind, "ok")
	})
	tw(t, f, "passes an in-scope model", func(t *testing.T) {
		e := newScopeEnv(t)
		setScopeModelsEnabled(true)
		e.setEnabledModels("anthropic/claude-opus-4-6")
		eq(t, e.check().kind, "ok")
	})
	outOfScope := func(t *testing.T) *scopeEnv {
		e := newScopeEnv(t)
		setScopeModelsEnabled(true)
		e.setEnabledModels("anthropic/claude-haiku-4-5", "anthropic/claude-sonnet-4-6")
		return e
	}
	tw(t, f, "refuses a caller-supplied choice and lists what is allowed", func(t *testing.T) {
		e := outOfScope(t)
		v := e.check(func(c *modelScopeCheck) { c.callerSupplied, c.modelInput = true, "anthropic/claude-opus-4-6" })
		eq(t, v.kind, "error")
		eq(t, v.message, "Model not in scope: \"anthropic/claude-opus-4-6\".\n\nAllowed models (from enabledModels):\n  anthropic/claude-haiku-4-5\n  anthropic/claude-sonnet-4-6")
	})
	tw(t, f, "only warns for a frontmatter-pinned choice, so the spawn still proceeds", func(t *testing.T) {
		e := outOfScope(t)
		v := e.check(func(c *modelScopeCheck) { c.callerSupplied, c.modelInput = false, "anthropic/claude-opus-4-6" })
		eq(t, v.kind, "warn")
		eq(t, v.message, `Agent "scout" using out-of-scope model "anthropic/claude-opus-4-6"`)
	})
	tw(t, f, "names the resolved model in the warning when there was no raw input", func(t *testing.T) {
		e := outOfScope(t) // parent-inherited: nothing was typed, so the label falls back to the resolved provider/id
		v := e.check(func(c *modelScopeCheck) { c.callerSupplied, c.modelInput = false, "" })
		eq(t, v.kind, "warn")
		eq(t, contains(v.message, "anthropic/claude-opus-4-6"), true)
		eq(t, contains(v.message, "undefined"), false)
	})
	tw(t, f, "stops enforcing as soon as the setting is turned back off", func(t *testing.T) {
		e := outOfScope(t)
		eq(t, e.check().kind, "error")
		setScopeModelsEnabled(false)
		eq(t, e.check().kind, "ok")
	})
	tw(t, f, "treats scope as case-insensitive on both sides", func(t *testing.T) {
		e := outOfScope(t)
		e.setEnabledModels("Anthropic/Claude-Opus-4-6")
		eq(t, e.check(func(c *modelScopeCheck) { c.provider, c.id = "anthropic", "claude-opus-4-6" }).kind, "ok")
		eq(t, e.check(func(c *modelScopeCheck) { c.provider, c.id = "anthropic", "claude-haiku-4-5" }).kind, "error")
	})
}

var enabledModelsRegistryModels = []mdl{
	m3("gemma-4-31b-it", "Gemma 4 31B", "google"), m3("claude-opus-4-6", "Claude Opus 4.6", "anthropic"), m3("claude-opus-4-5", "Claude Opus 4.5", "anthropic"),
	m3("claude-haiku-4-5", "Claude Haiku 4.5", "anthropic"), m3("claude-sonnet-4-6", "Claude Sonnet 4.6", "anthropic"),
}

func TestEnabledModelsTwins(t *testing.T) {
	const f = "enabled-models"
	cwd, _ := os.Getwd()
	// readEnabledModels: patterns, or nil for undefined
	type env struct{ agent, project string }
	newEnv := func(t *testing.T) env {
		e := env{agent: t.TempDir(), project: t.TempDir()}
		t.Setenv("PIG_CODING_AGENT_DIR", e.agent)
		t.Setenv("PIG_USE_PI_DIRS", "")
		return e
	}
	read := func(e env) any {
		p, ok := readEnabledModels(e.project)
		if !ok {
			return nil
		}
		return p
	}
	projectFile := func(e env) string { return filepath.Join(e.project, ".pi", "settings.json") }
	tw(t, f, "returns undefined when both settings files are missing", func(t *testing.T) { eq(t, read(newEnv(t)), nil) })
	tw(t, f, "returns undefined when field absent from both files", func(t *testing.T) {
		e := newEnv(t)
		writeJSONFile(t, filepath.Join(e.agent, "settings.json"), obj{"defaultProvider": "openai"})
		eq(t, read(e), nil)
	})
	tw(t, f, "returns enabledModels from global when project file absent", func(t *testing.T) {
		e := newEnv(t)
		writeJSONFile(t, filepath.Join(e.agent, "settings.json"), obj{"enabledModels": []string{"anthropic/claude-sonnet-4-6", "google/gemma-4-31b-it"}})
		eq(t, read(e), []string{"anthropic/claude-sonnet-4-6", "google/gemma-4-31b-it"})
	})
	tw(t, f, "returns enabledModels from project when global file absent", func(t *testing.T) {
		e := newEnv(t)
		writeJSONFile(t, projectFile(e), obj{"enabledModels": []string{"anthropic/claude-haiku-4-5"}})
		eq(t, read(e), []string{"anthropic/claude-haiku-4-5"})
	})
	tw(t, f, "project overrides global (array replaces wholly, mirrors pi's deep-merge)", func(t *testing.T) {
		e := newEnv(t)
		writeJSONFile(t, filepath.Join(e.agent, "settings.json"), obj{"enabledModels": []string{"anthropic/claude-sonnet-4-6", "anthropic/claude-opus-4-6"}})
		writeJSONFile(t, projectFile(e), obj{"enabledModels": []string{"anthropic/claude-haiku-4-5"}})
		eq(t, read(e), []string{"anthropic/claude-haiku-4-5"}) // the globals are NOT merged in
	})
	tw(t, f, "falls back to global when project file has no enabledModels field", func(t *testing.T) {
		e := newEnv(t)
		writeJSONFile(t, filepath.Join(e.agent, "settings.json"), obj{"enabledModels": []string{"anthropic/claude-sonnet-4-6"}})
		writeJSONFile(t, projectFile(e), obj{"defaultProvider": "anthropic"})
		eq(t, read(e), []string{"anthropic/claude-sonnet-4-6"})
	})
	tw(t, f, "returns undefined when global JSON is corrupt (try/catch swallow)", func(t *testing.T) {
		e := newEnv(t)
		_ = os.WriteFile(filepath.Join(e.agent, "settings.json"), []byte("not json {{{"), 0o644)
		eq(t, read(e), nil)
	})
	tw(t, f, "returns undefined when enabledModels is not an array (global)", func(t *testing.T) {
		e := newEnv(t)
		writeJSONFile(t, filepath.Join(e.agent, "settings.json"), obj{"enabledModels": "anthropic/claude-sonnet-4-6"})
		eq(t, read(e), nil)
	})
	tw(t, f, "returns undefined when enabledModels is not an array (project)", func(t *testing.T) {
		e := newEnv(t)
		writeJSONFile(t, projectFile(e), obj{"enabledModels": "anthropic/claude-haiku-4-5"})
		eq(t, read(e), nil)
	})

	resolve := func(patterns []string, has bool, avail ...[]mdl) map[string]bool {
		return resolveEnabledModels(patterns, has, makeRegistry(enabledModelsRegistryModels, avail...), cwd)
	}
	set := func(keys ...string) map[string]bool {
		out := map[string]bool{}
		for _, k := range keys {
			out[k] = true
		}
		return out
	}
	tw(t, f, "returns undefined for empty patterns", func(t *testing.T) {
		eq(t, resolve([]string{}, true) == nil, true)
		eq(t, resolve(nil, false) == nil, true)
	})
	tw(t, f, "returns undefined when no matches", func(t *testing.T) { eq(t, resolve([]string{"nonexistent/foo"}, true) == nil, true) })
	tw(t, f, "skips empty string patterns", func(t *testing.T) {
		eq(t, len(resolve([]string{"", "anthropic/claude-haiku-4-5", "anthropic/claude-sonnet-4-6"}, true)), 2)
	})
	tw(t, f, "skips whitespace-only patterns", func(t *testing.T) {
		eq(t, resolve([]string{"  ", "google/gemma-4-31b-it"}, true), set("google/gemma-4-31b-it"))
	})
	tw(t, f, "returns undefined when getAvailable returns empty array", func(t *testing.T) {
		eq(t, resolve([]string{"anthropic/claude-haiku-4-5"}, true, []mdl{}) == nil, true)
	})
	tw(t, f, "deduplicates duplicate patterns", func(t *testing.T) {
		eq(t, len(resolve([]string{"anthropic/claude-haiku-4-5", "anthropic/claude-haiku-4-5"}, true)), 1)
	})
	tw(t, f, "resolves exact match (key stored lowercase)", func(t *testing.T) {
		eq(t, resolve([]string{"google/gemma-4-31b-it"}, true), set("google/gemma-4-31b-it"))
	})
	tw(t, f, "resolves model id with colon (part of id, not split)", func(t *testing.T) {
		eq(t, resolve([]string{"anthropic/claude-opus-4-6"}, true), set("anthropic/claude-opus-4-6"))
	})
	tw(t, f, "is case-insensitive", func(t *testing.T) {
		eq(t, resolve([]string{"GOOGLE/GEMMA-4-31B-IT"}, true), set("google/gemma-4-31b-it"))
	})
	tw(t, f, "returns undefined for bare id (pi always writes provider/modelId)", func(t *testing.T) {
		eq(t, resolve([]string{"gemma-4-31b-it"}, true) == nil, true)
	})
	tw(t, f, "returns undefined for bare substring patterns", func(t *testing.T) { eq(t, resolve([]string{"Opus"}, true) == nil, true) })
	tw(t, f, "combines multiple exact provider/modelId in one call", func(t *testing.T) {
		got := resolve([]string{"google/gemma-4-31b-it", "anthropic/claude-haiku-4-5", "anthropic/claude-sonnet-4-6"}, true)
		eq(t, got, set("google/gemma-4-31b-it", "anthropic/claude-haiku-4-5", "anthropic/claude-sonnet-4-6"))
		eq(t, got["google/gemini-2.5-pro"] || got["anthropic/claude-opus-4-6"], false)
	})
	tw(t, f, "resolves only against available models when getAvailable present", func(t *testing.T) {
		got := resolve([]string{"anthropic/claude-haiku-4-5", "anthropic/claude-sonnet-4-6", "google/gemma-4-31b-it"}, true,
			[]mdl{enabledModelsRegistryModels[0], enabledModelsRegistryModels[3]})
		eq(t, got, set("anthropic/claude-haiku-4-5", "google/gemma-4-31b-it")) // sonnet is not available
	})
	_ = json.Marshal
}
