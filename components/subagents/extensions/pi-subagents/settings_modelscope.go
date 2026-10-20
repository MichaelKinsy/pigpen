package pi_subagents

import (
	"fmt"
	"regexp"
	"strings"
)

// Optional model-scope enforcement (src/runs/shared/model-scope.ts).

// ModelScopeRule is model-scope.ts ModelScopeRule.
type ModelScopeRule struct {
	Enforce *bool
	Strict  *bool
	Allow   []string
}

// ModelScopeConfig is model-scope.ts ModelScopeConfig.
type ModelScopeConfig struct {
	ModelScopeRule
	Agents map[string]ModelScopeRule
}

// ModelScopeCheckRule is a rule with the origin it reports.
type ModelScopeCheckRule struct {
	ModelScopeRule
	Origin string
}

// ModelScopeViolation is model-scope.ts ModelScopeViolation.
type ModelScopeViolation struct {
	Model           string
	Severity        string // "warn" or "error"
	Message         string
	AllowedPatterns []string
	Origin          string
}

// ParentModel is the {provider, id} of the current parent model.
type ParentModel struct{ Provider, ID string }

func stripThinkingSuffix(model string) string {
	b, _ := splitKnownThinkingSuffix(model)
	return b
}

// globToRegExp escapes RegExp specials except `*`, which becomes `.*`; the match is anchored and case-insensitive.
func globToRegExp(pattern string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("(?i)^")
	for _, r := range pattern {
		if r == '*' {
			b.WriteString(".*")
			continue
		}
		b.WriteString(regexp.QuoteMeta(string(r)))
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}

// matchesScopePattern is model-scope.ts:matchesScopePattern.
func matchesScopePattern(model, pattern string) bool {
	return globToRegExp(pattern).MatchString(stripThinkingSuffix(model))
}

// checkModelScope is model-scope.ts:checkModelScope; source is "explicit" or "inherited". A nil scope is no enforcement.
func checkModelScope(model string, scope *ModelScopeCheckRule, source string) *ModelScopeViolation {
	if model == "" || scope == nil || scope.Enforce == nil || !*scope.Enforce {
		return nil
	}
	if len(scope.Allow) == 0 {
		return nil
	}
	for _, p := range scope.Allow {
		if matchesScopePattern(model, p) {
			return nil
		}
	}
	base := stripThinkingSuffix(model)
	severity := "warn"
	if source == "explicit" || (scope.Strict != nil && *scope.Strict) {
		severity = "error"
	}
	origin := scope.Origin
	if origin == "" {
		origin = "modelScope"
	}
	return &ModelScopeViolation{
		Model: base, Severity: severity, AllowedPatterns: scope.Allow, Origin: origin,
		Message: fmt.Sprintf("Model '%s' is outside the configured subagent model scope (%s). Allowed patterns: %s.", base, origin, strings.Join(scope.Allow, ", ")),
	}
}

func expandInheritPattern(pattern string, parent *ParentModel) string {
	if pattern == "inherit" && parent != nil {
		return parent.Provider + "/" + parent.ID
	}
	return pattern
}

func expandInheritAll(patterns []string, parent *ParentModel) []string {
	out := make([]string, len(patterns))
	for i, p := range patterns {
		out[i] = expandInheritPattern(p, parent)
	}
	return out
}

// resolveModelScopesForAgent is model-scope.ts:resolveModelScopesForAgent.
func resolveModelScopesForAgent(config *ModelScopeConfig, agentName string, parent *ParentModel) []ModelScopeCheckRule {
	if config == nil {
		return nil
	}
	var scopes []ModelScopeCheckRule
	if config.Allow != nil {
		scopes = append(scopes, ModelScopeCheckRule{ModelScopeRule{Enforce: config.Enforce, Strict: config.Strict, Allow: expandInheritAll(config.Allow, parent)}, "modelScope"})
	}
	if as, ok := config.Agents[agentName]; ok && as.Allow != nil {
		r := ModelScopeRule{Enforce: as.Enforce, Strict: as.Strict, Allow: expandInheritAll(as.Allow, parent)}
		if r.Enforce == nil {
			r.Enforce = config.Enforce
		}
		if r.Strict == nil {
			r.Strict = config.Strict
		}
		scopes = append(scopes, ModelScopeCheckRule{r, "modelScope.agents." + agentName})
	}
	return scopes
}

func invalidScope(filePath, field, expected string) error {
	return fmt.Errorf("Subagent settings in '%s' have invalid '%s'; expected %s.", filePath, field, expected)
}

func assertRecord(v any, field, filePath, expected string) (*jsObject, error) {
	o, ok := v.(*jsObject)
	if !ok {
		return nil, invalidScope(filePath, field, expected)
	}
	return o, nil
}

func parseAllowList(v any, field, filePath string) ([]string, error) {
	arr, ok := v.([]any)
	if !ok {
		return nil, invalidScope(filePath, field, "an array of strings")
	}
	allow := []string{}
	for _, e := range arr {
		s, ok := e.(string)
		if !ok {
			return nil, invalidScope(filePath, field, "an array of strings")
		}
		if t := jsTrim(s); t != "" {
			allow = append(allow, t)
		}
	}
	if len(allow) == 0 {
		return nil, invalidScope(filePath, field, "a non-empty array of patterns")
	}
	return allow, nil
}

func parseScopeRule(in *jsObject, field, filePath string) (ModelScopeRule, error) {
	var r ModelScopeRule
	if _, ok := in.get("enforce"); ok {
		b, isB := in.flag("enforce")
		if !isB {
			return r, invalidScope(filePath, field+".enforce", "a boolean")
		}
		r.Enforce = &b
	}
	if _, ok := in.get("strict"); ok {
		b, isB := in.flag("strict")
		if !isB {
			return r, invalidScope(filePath, field+".strict", "a boolean")
		}
		r.Strict = &b
	}
	if v, ok := in.get("allow"); ok {
		a, err := parseAllowList(v, field+".allow", filePath)
		if err != nil {
			return r, err
		}
		r.Allow = a
	}
	return r, nil
}

// parseModelScopeConfig is model-scope.ts:parseModelScopeConfig; present is false for an absent field.
func parseModelScopeConfig(v any, present bool, filePath string) (*ModelScopeConfig, error) {
	if !present {
		return nil, nil
	}
	in, err := assertRecord(v, "modelScope", filePath, "an object")
	if err != nil {
		return nil, err
	}
	rule, err := parseScopeRule(in, "modelScope", filePath)
	if err != nil {
		return nil, err
	}
	cfg := &ModelScopeConfig{ModelScopeRule: rule}
	if av, ok := in.get("agents"); ok {
		ao, err := assertRecord(av, "modelScope.agents", filePath, "an object keyed by agent name")
		if err != nil {
			return nil, err
		}
		cfg.Agents = map[string]ModelScopeRule{}
		for _, rawName := range ao.order() {
			name := jsTrim(rawName)
			field := "modelScope.agents." + name
			if name == "" {
				return nil, fmt.Errorf("Subagent settings in '%s' have invalid 'modelScope.agents' key; expected a non-empty agent name.", filePath)
			}
			ai, err := assertRecord(ao.vals[rawName], field, filePath, "an object")
			if err != nil {
				return nil, err
			}
			if _, ok := ai.get("agents"); ok {
				return nil, invalidScope(filePath, field+".agents", "nested agent scopes are not supported")
			}
			r, err := parseScopeRule(ai, field, filePath)
			if err != nil {
				return nil, err
			}
			cfg.Agents[name] = r
		}
	}
	hasAny := len(cfg.Allow) > 0
	for _, r := range cfg.Agents {
		hasAny = hasAny || len(r.Allow) > 0
	}
	if cfg.Enforce != nil && *cfg.Enforce && !hasAny {
		return nil, fmt.Errorf("Subagent settings in '%s' set modelScope.enforce without a non-empty 'allow' list; supply allowed model patterns or disable enforcement.", filePath)
	}
	if cfg.Enforce == nil && cfg.Strict == nil && cfg.Allow == nil && cfg.Agents == nil {
		return nil, nil
	}
	return cfg, nil
}
