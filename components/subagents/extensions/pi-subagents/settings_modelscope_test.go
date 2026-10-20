package pi_subagents

import (
	"strings"
	"testing"
)

// Twins of test/unit/model-scope.test.ts and test/unit/model-scope-settings.test.ts.

func scopeOf(v any) (*ModelScopeConfig, error) {
	if v == nil {
		return parseModelScopeConfig(nil, false, "~/.pi/agent/settings.json")
	}
	return parseModelScopeConfig(v, true, "~/.pi/agent/settings.json")
}

func parseScope(t *testing.T, js string) (*ModelScopeConfig, error) {
	t.Helper()
	v, err := parseJSON([]byte(js))
	if err != nil {
		t.Fatal(err)
	}
	return scopeOf(v)
}

func errMatch(t *testing.T, err error, sub string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), sub) {
		t.Errorf("error %v, want %q", err, sub)
	}
}

func TestModelScope(t *testing.T) {
	const f = "model-scope"
	tw(t, f, "matches an exact provider/id", func(t *testing.T) {
		eq(t, matchesScopePattern("anthropic/claude-sonnet-4", "anthropic/claude-sonnet-4"), true, "exact")
	})
	tw(t, f, "matches a provider wildcard", func(t *testing.T) {
		eq(t, matchesScopePattern("anthropic/claude-sonnet-4", "anthropic/*"), true, "a")
		eq(t, matchesScopePattern("anthropic/claude-haiku-4-5", "anthropic/*"), true, "b")
	})
	tw(t, f, "matches a model-prefix wildcard", func(t *testing.T) {
		eq(t, matchesScopePattern("openai/gpt-5-mini", "openai/gpt-5-*"), true, "a")
		// The dash in the pattern is literal, so the bare id without a trailing part does not match.
		eq(t, matchesScopePattern("openai/gpt-5", "openai/gpt-5-*"), false, "b")
		eq(t, matchesScopePattern("openai/gpt-5", "openai/gpt-5*"), true, "c")
	})
	tw(t, f, "does not match across providers", func(t *testing.T) {
		eq(t, matchesScopePattern("deepseek/deepseek-v4", "anthropic/*"), false, "a")
		eq(t, matchesScopePattern("openai/gpt-5-mini", "anthropic/*"), false, "b")
	})
	tw(t, f, "ignores case on both sides", func(t *testing.T) {
		eq(t, matchesScopePattern("Anthropic/Claude-Sonnet-4", "anthropic/*"), true, "a")
		eq(t, matchesScopePattern("anthropic/claude-sonnet-4", "ANTHROPIC/CLAUDE-*"), true, "b")
	})
	tw(t, f, "strips a thinking suffix before matching", func(t *testing.T) {
		eq(t, matchesScopePattern("anthropic/claude-sonnet-4:high", "anthropic/claude-sonnet-4"), true, "a")
		eq(t, matchesScopePattern("anthropic/claude-sonnet-4:high", "anthropic/*"), true, "b")
	})
	tw(t, f, "does not treat arbitrary colon text as a thinking suffix", func(t *testing.T) {
		eq(t, matchesScopePattern("anthropic:claude-opus", "anthropic"), false, "a")
		eq(t, matchesScopePattern("anthropic/claude-sonnet-4:experimental", "anthropic/claude-sonnet-4"), false, "b")
	})
	tw(t, f, "requires a full match, not a substring", func(t *testing.T) {
		eq(t, matchesScopePattern("anthropic/claude-sonnet-4", "anthropic/claude"), false, "a")
		eq(t, matchesScopePattern("anthropic/claude-sonnet-4", "*claude*"), true, "b")
	})

	scope := &ModelScopeCheckRule{ModelScopeRule: ModelScopeRule{Enforce: new(true), Allow: []string{"anthropic/*", "openai/gpt-5-*"}}}
	tw(t, f, "returns undefined when enforcement is off", func(t *testing.T) {
		eq(t, checkModelScope("deepseek/deepseek-v4", &ModelScopeCheckRule{ModelScopeRule: ModelScopeRule{Enforce: new(false), Allow: []string{"anthropic/*"}}}, "explicit") == nil, true, "off")
		eq(t, checkModelScope("deepseek/deepseek-v4", nil, "explicit") == nil, true, "nil")
	})
	tw(t, f, "returns undefined when no allow list is configured", func(t *testing.T) {
		eq(t, checkModelScope("deepseek/deepseek-v4", &ModelScopeCheckRule{ModelScopeRule: ModelScopeRule{Enforce: new(true)}}, "explicit") == nil, true, "none")
		eq(t, checkModelScope("deepseek/deepseek-v4", &ModelScopeCheckRule{ModelScopeRule: ModelScopeRule{Enforce: new(true), Allow: []string{}}}, "explicit") == nil, true, "empty")
	})
	tw(t, f, "returns undefined when the model is in scope", func(t *testing.T) {
		eq(t, checkModelScope("anthropic/claude-sonnet-4", scope, "explicit") == nil, true, "a")
		eq(t, checkModelScope("openai/gpt-5-mini:high", scope, "inherited") == nil, true, "b")
	})
	tw(t, f, "returns an error violation for an explicit out-of-scope model", func(t *testing.T) {
		v := checkModelScope("deepseek/deepseek-v4", scope, "explicit")
		eq(t, v.Severity, "error", "severity")
		eq(t, v.Model, "deepseek/deepseek-v4", "model")
		eq(t, v.AllowedPatterns, []string{"anthropic/*", "openai/gpt-5-*"}, "patterns")
		eq(t, v.Origin, "modelScope", "origin")
		eq(t, strings.Contains(v.Message, "outside the configured subagent model scope"), true, "message")
	})
	tw(t, f, "returns a warn violation for an inherited out-of-scope model", func(t *testing.T) {
		eq(t, checkModelScope("deepseek/deepseek-v4", scope, "inherited").Severity, "warn", "severity")
	})
	tw(t, f, "returns an error violation for an inherited model in strict mode", func(t *testing.T) {
		strict := *scope
		strict.Strict = new(true)
		eq(t, checkModelScope("deepseek/deepseek-v4", &strict, "inherited").Severity, "error", "severity")
	})
	tw(t, f, "defaults to inherited (warn) severity when source is omitted-ish via inherited", func(t *testing.T) {
		// Caller passes "inherited" for frontmatter/parent-inherited models.
		eq(t, checkModelScope("meta/llama-4", scope, "inherited").Severity, "warn", "severity")
	})
	tw(t, f, "strips the thinking suffix from the reported model", func(t *testing.T) {
		eq(t, checkModelScope("deepseek/deepseek-v4:high", scope, "explicit").Model, "deepseek/deepseek-v4", "model")
	})
	tw(t, f, "returns undefined for an undefined model", func(t *testing.T) {
		eq(t, checkModelScope("", scope, "explicit") == nil, true, "undefined")
	})
	tw(t, f, "returns independent global and agent checks with inherited flags", func(t *testing.T) {
		got := resolveModelScopesForAgent(&ModelScopeConfig{ModelScopeRule: ModelScopeRule{Enforce: new(true), Strict: new(true), Allow: []string{"openai/*"}},
			Agents: map[string]ModelScopeRule{"worker": {Allow: []string{"openai/gpt-5-mini"}}}}, "worker", &ParentModel{"openai", "gpt-5"})
		eq(t, got, []ModelScopeCheckRule{
			{ModelScopeRule{Enforce: new(true), Strict: new(true), Allow: []string{"openai/*"}}, "modelScope"},
			{ModelScopeRule{Enforce: new(true), Strict: new(true), Allow: []string{"openai/gpt-5-mini"}}, "modelScope.agents.worker"},
		}, "scopes")
	})
	tw(t, f, "expands inherit against the current parent model", func(t *testing.T) {
		scopes := resolveModelScopesForAgent(&ModelScopeConfig{ModelScopeRule: ModelScopeRule{Enforce: new(true), Allow: []string{"inherit"}},
			Agents: map[string]ModelScopeRule{"reviewer": {Allow: []string{"inherit"}}}}, "reviewer", &ParentModel{"anthropic", "claude-sonnet-4"})
		eq(t, [][]string{scopes[0].Allow, scopes[1].Allow}, [][]string{{"anthropic/claude-sonnet-4"}, {"anthropic/claude-sonnet-4"}}, "allow")
	})
	tw(t, f, "keeps inherit literal when no parent model is available so enforcement fails closed", func(t *testing.T) {
		scopes := resolveModelScopesForAgent(&ModelScopeConfig{ModelScopeRule: ModelScopeRule{Enforce: new(true), Allow: []string{"inherit"}}}, "worker", nil)
		v := checkModelScope("openai/gpt-5-mini", &scopes[0], "explicit")
		eq(t, v.Severity, "error", "severity")
		eq(t, v.AllowedPatterns, []string{"inherit"}, "patterns")
	})
	tw(t, f, "does not apply another agent's scope", func(t *testing.T) {
		got := resolveModelScopesForAgent(&ModelScopeConfig{ModelScopeRule: ModelScopeRule{Enforce: new(true)}, Agents: map[string]ModelScopeRule{"reviewer": {Allow: []string{"inherit"}}}}, "worker", &ParentModel{"openai", "gpt-5"})
		eq(t, len(got), 0, "none")
	})
	tw(t, f, "returns undefined when the field is absent", func(t *testing.T) {
		c, err := scopeOf(nil)
		eq(t, c == nil && err == nil, true, "absent")
	})
	tw(t, f, "parses a well-formed config", func(t *testing.T) {
		c, err := parseScope(t, `{"enforce":true,"strict":true,"allow":["anthropic/*","openai/gpt-5-*"]}`)
		eq(t, err == nil, true, "err")
		eq(t, *c, ModelScopeConfig{ModelScopeRule: ModelScopeRule{Enforce: new(true), Strict: new(true), Allow: []string{"anthropic/*", "openai/gpt-5-*"}}}, "config")
	})
	tw(t, f, "parses per-agent scopes and permits agent-only enforcement", func(t *testing.T) {
		c, err := parseScope(t, `{"enforce":true,"strict":true,"agents":{"worker":{"allow":[" openai/gpt-5-mini "]},"unknown":{"enforce":false}}}`)
		eq(t, err == nil, true, "err")
		eq(t, *c, ModelScopeConfig{ModelScopeRule: ModelScopeRule{Enforce: new(true), Strict: new(true)},
			Agents: map[string]ModelScopeRule{"worker": {Allow: []string{"openai/gpt-5-mini"}}, "unknown": {Enforce: new(false)}}}, "config")
	})
	tw(t, f, "parses enforce alone", func(t *testing.T) {
		c, _ := parseScope(t, `{"enforce":false}`)
		eq(t, *c, ModelScopeConfig{ModelScopeRule: ModelScopeRule{Enforce: new(false)}}, "config")
	})
	tw(t, f, "trims allow patterns and drops empties", func(t *testing.T) {
		c, _ := parseScope(t, `{"enforce":true,"allow":["  anthropic/*  ","","  "]}`)
		eq(t, *c, ModelScopeConfig{ModelScopeRule: ModelScopeRule{Enforce: new(true), Allow: []string{"anthropic/*"}}}, "config")
	})
	tw(t, f, "rejects a non-object value", func(t *testing.T) {
		_, err := parseScope(t, `"anthropic/*"`)
		errMatch(t, err, "invalid 'modelScope'")
		_, err = parseScope(t, `[]`)
		errMatch(t, err, "invalid 'modelScope'")
	})
	tw(t, f, "rejects a non-boolean enforce", func(t *testing.T) {
		_, err := parseScope(t, `{"enforce":"yes"}`)
		errMatch(t, err, "invalid 'modelScope.enforce'")
	})
	tw(t, f, "rejects a non-boolean strict value", func(t *testing.T) {
		_, err := parseScope(t, `{"strict":"yes"}`)
		errMatch(t, err, "invalid 'modelScope.strict'")
	})
	tw(t, f, "rejects a non-array allow", func(t *testing.T) {
		_, err := parseScope(t, `{"allow":"anthropic/*"}`)
		errMatch(t, err, "invalid 'modelScope.allow'")
	})
	tw(t, f, "rejects an allow array with non-string entries", func(t *testing.T) {
		_, err := parseScope(t, `{"enforce":true,"allow":["anthropic/*",42]}`)
		errMatch(t, err, "invalid 'modelScope.allow'")
	})
	tw(t, f, "rejects enforce without a non-empty allow list", func(t *testing.T) {
		_, err := parseScope(t, `{"enforce":true}`)
		errMatch(t, err, "without a non-empty 'allow'")
		_, err = parseScope(t, `{"enforce":true,"allow":[]}`)
		errMatch(t, err, "non-empty array of patterns")
	})
	tw(t, f, "rejects invalid agent scope shapes with the full field path", func(t *testing.T) {
		_, err := parseScope(t, `{"enforce":true,"agents":{"worker":[]}}`)
		errMatch(t, err, "modelScope.agents.worker")
		_, err = parseScope(t, `{"agents":{"worker":{"allow":[]}}}`)
		errMatch(t, err, "modelScope.agents.worker.allow")
		_, err = parseScope(t, `{"agents":{"worker":{"agents":{}}}}`)
		errMatch(t, err, "modelScope.agents.worker.agents")
		_, err = parseScope(t, `{"agents":{" ":{"allow":["inherit"]}}}`)
		errMatch(t, err, "non-empty agent name")
	})
}

func TestModelScopeSettings(t *testing.T) {
	const f = "model-scope-settings"
	us := func(e amEnv, v m) { wj(t, e.userSettings(), sub(m{"modelScope": v})) }
	tw(t, f, "exposes a user modelScope from discoverAgents", func(t *testing.T) {
		e := newAmEnv(t)
		us(e, m{"enforce": true, "strict": true, "allow": l{"anthropic/*", "openai/gpt-5-*"}, "agents": m{"reviewer": m{"allow": l{"inherit"}}}})
		d := dAgents(t, e.project, ScopeBoth, "")
		eq(t, *d.ModelScope, ModelScopeConfig{ModelScopeRule: ModelScopeRule{Enforce: new(true), Strict: new(true), Allow: []string{"anthropic/*", "openai/gpt-5-*"}},
			Agents: map[string]ModelScopeRule{"reviewer": {Allow: []string{"inherit"}}}}, "scope")
	})
	tw(t, f, "accepts enforcement with only named agent allow lists", func(t *testing.T) {
		e := newAmEnv(t)
		us(e, m{"enforce": true, "agents": m{"worker": m{"allow": l{"openai/gpt-5-mini"}}}})
		eq(t, *dAgents(t, e.project, ScopeBoth, "").ModelScope, ModelScopeConfig{ModelScopeRule: ModelScopeRule{Enforce: new(true)}, Agents: map[string]ModelScopeRule{"worker": {Allow: []string{"openai/gpt-5-mini"}}}}, "scope")
	})
	tw(t, f, "returns undefined when no modelScope is configured", func(t *testing.T) {
		e := newAmEnv(t)
		eq(t, dAgents(t, e.project, ScopeBoth, "").ModelScope == nil, true, "none")
	})
	tw(t, f, "prefers project modelScope over user modelScope", func(t *testing.T) {
		e := newAmEnv(t)
		us(e, m{"enforce": true, "allow": l{"anthropic/*"}, "agents": m{"reviewer": m{"allow": l{"inherit"}}}})
		wj(t, e.projectSettings(), sub(m{"modelScope": m{"enforce": true, "agents": m{"worker": m{"allow": l{"deepseek/*"}}}}}))
		eq(t, *dAgents(t, e.project, ScopeBoth, "").ModelScope, ModelScopeConfig{ModelScopeRule: ModelScopeRule{Enforce: new(true)}, Agents: map[string]ModelScopeRule{"worker": {Allow: []string{"deepseek/*"}}}}, "scope")
	})
	tw(t, f, "falls back to user modelScope when project does not set one", func(t *testing.T) {
		e := newAmEnv(t)
		us(e, m{"enforce": true, "allow": l{"anthropic/*"}})
		eq(t, *dAgents(t, e.project, ScopeBoth, "").ModelScope, ModelScopeConfig{ModelScopeRule: ModelScopeRule{Enforce: new(true), Allow: []string{"anthropic/*"}}}, "scope")
	})
	tw(t, f, "rejects an invalid modelScope config at discovery time", func(t *testing.T) {
		e := newAmEnv(t)
		us(e, m{"enforce": true})
		errMatch(t, discErr(e.project, ScopeBoth), "without a non-empty 'allow'")
	})
	tw(t, f, "rejects a malformed modelScope value at discovery time", func(t *testing.T) {
		e := newAmEnv(t)
		wj(t, e.userSettings(), sub(m{"modelScope": "anthropic/*"}))
		errMatch(t, discErr(e.project, ScopeBoth), "invalid 'modelScope'")
	})
	tw(t, f, "rejects malformed per-agent scopes at discovery time", func(t *testing.T) {
		e := newAmEnv(t)
		us(e, m{"agents": m{"worker": m{"allow": l{}}}})
		errMatch(t, discErr(e.project, ScopeBoth), "modelScope.agents.worker.allow")
	})
}
