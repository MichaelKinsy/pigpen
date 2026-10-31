package pi_subagents

// Twins of the original's test/unit/model-scope.test.ts (33 titles): the model
// scope's pattern matching, pure decision, per-agent resolution and settings
// parsing.

import (
	"reflect"
	"strings"
	"testing"
)

func boolPtrT(b bool) *bool { return &b }

func TestModelScope(t *testing.T) {
	const f = "model-scope"
	tw(t, f, "matches an exact provider/id", func(t *testing.T) {
		if !matchesScopePattern("anthropic/claude-sonnet-4", "anthropic/claude-sonnet-4") {
			t.Error("exact id should match")
		}
	})
	tw(t, f, "matches a provider wildcard", func(t *testing.T) {
		if !matchesScopePattern("anthropic/claude-sonnet-4", "anthropic/*") {
			t.Error("provider wildcard should match sonnet")
		}
		if !matchesScopePattern("anthropic/claude-haiku-4-5", "anthropic/*") {
			t.Error("provider wildcard should match haiku")
		}
	})
	tw(t, f, "matches a model-prefix wildcard", func(t *testing.T) {
		if !matchesScopePattern("openai/gpt-5-mini", "openai/gpt-5-*") {
			t.Error("prefix wildcard should match gpt-5-mini")
		}
		// The dash in the pattern is literal, so the bare id without a trailing part does not match.
		if matchesScopePattern("openai/gpt-5", "openai/gpt-5-*") {
			t.Error("gpt-5 should not match gpt-5-*")
		}
		if !matchesScopePattern("openai/gpt-5", "openai/gpt-5*") {
			t.Error("gpt-5 should match gpt-5*")
		}
	})
	tw(t, f, "does not match across providers", func(t *testing.T) {
		if matchesScopePattern("deepseek/deepseek-v4", "anthropic/*") {
			t.Error("cross provider should not match")
		}
		if matchesScopePattern("openai/gpt-5-mini", "anthropic/*") {
			t.Error("cross provider should not match")
		}
	})
	tw(t, f, "ignores case on both sides", func(t *testing.T) {
		if !matchesScopePattern("Anthropic/Claude-Sonnet-4", "anthropic/*") {
			t.Error("model case should be ignored")
		}
		if !matchesScopePattern("anthropic/claude-sonnet-4", "ANTHROPIC/CLAUDE-*") {
			t.Error("pattern case should be ignored")
		}
	})
	tw(t, f, "strips a thinking suffix before matching", func(t *testing.T) {
		if !matchesScopePattern("anthropic/claude-sonnet-4:high", "anthropic/claude-sonnet-4") {
			t.Error("suffix should be stripped for the exact pattern")
		}
		if !matchesScopePattern("anthropic/claude-sonnet-4:high", "anthropic/*") {
			t.Error("suffix should be stripped for the wildcard")
		}
	})
	tw(t, f, "does not treat arbitrary colon text as a thinking suffix", func(t *testing.T) {
		if matchesScopePattern("anthropic:claude-opus", "anthropic") {
			t.Error("arbitrary colon text must not be treated as a suffix")
		}
		if matchesScopePattern("anthropic/claude-sonnet-4:experimental", "anthropic/claude-sonnet-4") {
			t.Error("an unknown level must not be stripped")
		}
	})
	tw(t, f, "requires a full match, not a substring", func(t *testing.T) {
		if matchesScopePattern("anthropic/claude-sonnet-4", "anthropic/claude") {
			t.Error("a substring pattern must not match")
		}
		if !matchesScopePattern("anthropic/claude-sonnet-4", "*claude*") {
			t.Error("a surrounded wildcard should match")
		}
	})

	scope := &ModelScopeRule{Enforce: boolPtrT(true), Allow: []string{"anthropic/*", "openai/gpt-5-*"}}
	tw(t, f, "returns undefined when enforcement is off", func(t *testing.T) {
		if v := checkModelScope("deepseek/deepseek-v4", &ModelScopeRule{Enforce: boolPtrT(false), Allow: []string{"anthropic/*"}}, "modelScope", "explicit"); v != nil {
			t.Errorf("enforcement off: %+v", v)
		}
		if v := checkModelScope("deepseek/deepseek-v4", nil, "modelScope", "explicit"); v != nil {
			t.Errorf("no scope: %+v", v)
		}
	})
	tw(t, f, "returns undefined when no allow list is configured", func(t *testing.T) {
		if v := checkModelScope("deepseek/deepseek-v4", &ModelScopeRule{Enforce: boolPtrT(true)}, "modelScope", "explicit"); v != nil {
			t.Errorf("no allow list: %+v", v)
		}
		if v := checkModelScope("deepseek/deepseek-v4", &ModelScopeRule{Enforce: boolPtrT(true), Allow: []string{}}, "modelScope", "explicit"); v != nil {
			t.Errorf("empty allow list: %+v", v)
		}
	})
	tw(t, f, "returns undefined when the model is in scope", func(t *testing.T) {
		if v := checkModelScope("anthropic/claude-sonnet-4", scope, "modelScope", "explicit"); v != nil {
			t.Errorf("in scope: %+v", v)
		}
		if v := checkModelScope("openai/gpt-5-mini:high", scope, "modelScope", "inherited"); v != nil {
			t.Errorf("in scope with suffix: %+v", v)
		}
	})
	tw(t, f, "returns an error violation for an explicit out-of-scope model", func(t *testing.T) {
		v := checkModelScope("deepseek/deepseek-v4", scope, "modelScope", "explicit")
		if v == nil {
			t.Fatal("expected a violation")
		}
		if v.Severity != "error" {
			t.Errorf("severity %q", v.Severity)
		}
		if v.Model != "deepseek/deepseek-v4" {
			t.Errorf("model %q", v.Model)
		}
		if !reflect.DeepEqual(v.AllowedPattern, scope.Allow) {
			t.Errorf("patterns %v", v.AllowedPattern)
		}
		if v.Origin != "modelScope" {
			t.Errorf("origin %q", v.Origin)
		}
		if !strings.Contains(v.Message, "outside the configured subagent model scope") {
			t.Errorf("message %q", v.Message)
		}
	})
	tw(t, f, "returns a warn violation for an inherited out-of-scope model", func(t *testing.T) {
		v := checkModelScope("deepseek/deepseek-v4", scope, "modelScope", "inherited")
		if v == nil || v.Severity != "warn" {
			t.Errorf("expected warn, got %+v", v)
		}
	})
	tw(t, f, "returns an error violation for an inherited model in strict mode", func(t *testing.T) {
		strict := &ModelScopeRule{Enforce: boolPtrT(true), Strict: boolPtrT(true), Allow: scope.Allow}
		v := checkModelScope("deepseek/deepseek-v4", strict, "modelScope", "inherited")
		if v == nil || v.Severity != "error" {
			t.Errorf("expected error, got %+v", v)
		}
	})
	tw(t, f, "defaults to inherited (warn) severity when source is omitted-ish via inherited", func(t *testing.T) {
		v := checkModelScope("meta/llama-4", scope, "modelScope", "inherited")
		if v == nil || v.Severity != "warn" {
			t.Errorf("expected warn, got %+v", v)
		}
	})
	tw(t, f, "strips the thinking suffix from the reported model", func(t *testing.T) {
		v := checkModelScope("deepseek/deepseek-v4:high", scope, "modelScope", "explicit")
		if v == nil || v.Model != "deepseek/deepseek-v4" {
			t.Errorf("expected stripped model, got %+v", v)
		}
	})
	tw(t, f, "returns undefined for an undefined model", func(t *testing.T) {
		if v := checkModelScope("", scope, "modelScope", "explicit"); v != nil {
			t.Errorf("empty model: %+v", v)
		}
	})

	tw(t, f, "returns independent global and agent checks with inherited flags", func(t *testing.T) {
		config := &ModelScopeConfig{
			Enforce: boolPtrT(true), Strict: boolPtrT(true), Allow: []string{"openai/*"},
			Agents: map[string]*ModelScopeRule{"worker": {Allow: []string{"openai/gpt-5-mini"}}},
		}
		got := resolveModelScopesForAgent(config, "worker", "openai", "gpt-5")
		want := []resolvedModelScope{
			{Enforce: true, Strict: true, Allow: []string{"openai/*"}, Origin: "modelScope"},
			{Enforce: true, Strict: true, Allow: []string{"openai/gpt-5-mini"}, Origin: "modelScope.agents.worker"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %+v want %+v", got, want)
		}
	})
	tw(t, f, "expands inherit against the current parent model", func(t *testing.T) {
		config := &ModelScopeConfig{
			Enforce: boolPtrT(true), Allow: []string{"inherit"},
			Agents: map[string]*ModelScopeRule{"reviewer": {Allow: []string{"inherit"}}},
		}
		scopes := resolveModelScopesForAgent(config, "reviewer", "anthropic", "claude-sonnet-4")
		if len(scopes) != 2 ||
			!reflect.DeepEqual(scopes[0].Allow, []string{"anthropic/claude-sonnet-4"}) ||
			!reflect.DeepEqual(scopes[1].Allow, []string{"anthropic/claude-sonnet-4"}) {
			t.Errorf("scopes %+v", scopes)
		}
	})
	tw(t, f, "keeps inherit literal when no parent model is available so enforcement fails closed", func(t *testing.T) {
		config := &ModelScopeConfig{Enforce: boolPtrT(true), Allow: []string{"inherit"}}
		scopes := resolveModelScopesForAgent(config, "worker", "", "")
		v := checkModelScope("openai/gpt-5-mini", ruleOf(scopes[0]), scopes[0].Origin, "explicit")
		if v == nil || v.Severity != "error" {
			t.Fatalf("expected error, got %+v", v)
		}
		if !reflect.DeepEqual(v.AllowedPattern, []string{"inherit"}) {
			t.Errorf("patterns %v", v.AllowedPattern)
		}
	})
	tw(t, f, "does not apply another agent's scope", func(t *testing.T) {
		config := &ModelScopeConfig{Enforce: boolPtrT(true), Agents: map[string]*ModelScopeRule{"reviewer": {Allow: []string{"inherit"}}}}
		if scopes := resolveModelScopesForAgent(config, "worker", "openai", "gpt-5"); len(scopes) != 0 {
			t.Errorf("scopes %+v", scopes)
		}
	})

	const filePath = "~/.pi/agent/settings.json"
	tw(t, f, "returns undefined when the field is absent", func(t *testing.T) {
		config, errText := parseModelScopeConfig(nil, filePath)
		if config != nil || errText != "" {
			t.Errorf("absent field: %+v %q", config, errText)
		}
	})
	tw(t, f, "parses a well-formed config", func(t *testing.T) {
		config, errText := parseModelScopeConfig(map[string]any{
			"enforce": true, "strict": true, "allow": []any{"anthropic/*", "openai/gpt-5-*"},
		}, filePath)
		if errText != "" {
			t.Fatal(errText)
		}
		want := &ModelScopeConfig{Enforce: boolPtrT(true), Strict: boolPtrT(true), Allow: []string{"anthropic/*", "openai/gpt-5-*"}}
		if !reflect.DeepEqual(config, want) {
			t.Errorf("got %+v want %+v", config, want)
		}
	})
	tw(t, f, "parses per-agent scopes and permits agent-only enforcement", func(t *testing.T) {
		config, errText := parseModelScopeConfig(map[string]any{
			"enforce": true, "strict": true,
			"agents": map[string]any{
				"worker":  map[string]any{"allow": []any{" openai/gpt-5-mini "}},
				"unknown": map[string]any{"enforce": false},
			},
		}, filePath)
		if errText != "" {
			t.Fatal(errText)
		}
		want := &ModelScopeConfig{
			Enforce: boolPtrT(true), Strict: boolPtrT(true),
			Agents: map[string]*ModelScopeRule{
				"worker":  {Allow: []string{"openai/gpt-5-mini"}},
				"unknown": {Enforce: boolPtrT(false)},
			},
		}
		if !reflect.DeepEqual(config, want) {
			t.Errorf("got %+v want %+v", config, want)
		}
	})
	tw(t, f, "parses enforce alone", func(t *testing.T) {
		config, errText := parseModelScopeConfig(map[string]any{"enforce": false}, filePath)
		if errText != "" {
			t.Fatal(errText)
		}
		if config == nil || config.Enforce == nil || *config.Enforce {
			t.Errorf("got %+v", config)
		}
	})
	tw(t, f, "trims allow patterns and drops empties", func(t *testing.T) {
		config, errText := parseModelScopeConfig(map[string]any{
			"enforce": true, "allow": []any{"  anthropic/*  ", "", "  "},
		}, filePath)
		if errText != "" {
			t.Fatal(errText)
		}
		if !reflect.DeepEqual(config.Allow, []string{"anthropic/*"}) {
			t.Errorf("allow %v", config.Allow)
		}
	})
	tw(t, f, "rejects a non-object value", func(t *testing.T) {
		if _, errText := parseModelScopeConfig("anthropic/*", filePath); !strings.Contains(errText, "invalid 'modelScope'") {
			t.Errorf("err %q", errText)
		}
		if _, errText := parseModelScopeConfig([]any{}, filePath); !strings.Contains(errText, "invalid 'modelScope'") {
			t.Errorf("err %q", errText)
		}
	})
	tw(t, f, "rejects a non-boolean enforce", func(t *testing.T) {
		if _, errText := parseModelScopeConfig(map[string]any{"enforce": "yes"}, filePath); !strings.Contains(errText, "invalid 'modelScope.enforce'") {
			t.Errorf("err %q", errText)
		}
	})
	tw(t, f, "rejects a non-boolean strict value", func(t *testing.T) {
		if _, errText := parseModelScopeConfig(map[string]any{"strict": "yes"}, filePath); !strings.Contains(errText, "invalid 'modelScope.strict'") {
			t.Errorf("err %q", errText)
		}
	})
	tw(t, f, "rejects a non-array allow", func(t *testing.T) {
		if _, errText := parseModelScopeConfig(map[string]any{"allow": "anthropic/*"}, filePath); !strings.Contains(errText, "invalid 'modelScope.allow'") {
			t.Errorf("err %q", errText)
		}
	})
	tw(t, f, "rejects an allow array with non-string entries", func(t *testing.T) {
		if _, errText := parseModelScopeConfig(map[string]any{"enforce": true, "allow": []any{"anthropic/*", 42}}, filePath); !strings.Contains(errText, "invalid 'modelScope.allow'") {
			t.Errorf("err %q", errText)
		}
	})
	tw(t, f, "rejects enforce without a non-empty allow list", func(t *testing.T) {
		if _, errText := parseModelScopeConfig(map[string]any{"enforce": true}, filePath); !strings.Contains(errText, "without a non-empty 'allow'") {
			t.Errorf("err %q", errText)
		}
		if _, errText := parseModelScopeConfig(map[string]any{"enforce": true, "allow": []any{}}, filePath); !strings.Contains(errText, "non-empty array of patterns") {
			t.Errorf("err %q", errText)
		}
	})
	tw(t, f, "rejects invalid agent scope shapes with the full field path", func(t *testing.T) {
		if _, errText := parseModelScopeConfig(map[string]any{"enforce": true, "agents": map[string]any{"worker": []any{}}}, filePath); !strings.Contains(errText, "modelScope.agents.worker") {
			t.Errorf("err %q", errText)
		}
		if _, errText := parseModelScopeConfig(map[string]any{"agents": map[string]any{"worker": map[string]any{"allow": []any{}}}}, filePath); !strings.Contains(errText, "modelScope.agents.worker.allow") {
			t.Errorf("err %q", errText)
		}
		if _, errText := parseModelScopeConfig(map[string]any{"agents": map[string]any{"worker": map[string]any{"agents": map[string]any{}}}}, filePath); !strings.Contains(errText, "modelScope.agents.worker.agents") {
			t.Errorf("err %q", errText)
		}
		if _, errText := parseModelScopeConfig(map[string]any{"agents": map[string]any{" ": map[string]any{"allow": []any{"inherit"}}}}, filePath); !strings.Contains(errText, "non-empty agent name") {
			t.Errorf("err %q", errText)
		}
	})
}

// ruleOf adapts a resolved scope back to a check rule.
func ruleOf(scope resolvedModelScope) *ModelScopeRule {
	return &ModelScopeRule{Enforce: boolPtrT(scope.Enforce), Strict: boolPtrT(scope.Strict), Allow: scope.Allow}
}
