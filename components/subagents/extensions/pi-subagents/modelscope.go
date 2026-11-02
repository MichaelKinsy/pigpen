package pi_subagents

// Model scopes (src/runs/shared/model-scope.ts): optional settings restrictions
// on the model a subagent resolves. An explicitly supplied model (`--model`, a
// tool `model`) outside the scope is a hard error; an inherited model (agent
// frontmatter, defaultModel, the parent session) only warns, unless the scope
// is strict. Enforcement with no allow list is a no-op (the settings parser
// rejects that combination; this stays defensive for programmatic callers).

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// ModelScopeRule is one scope: enforce switches the check on, strict makes
// inherited models errors too, allow holds glob patterns (only `*` is special)
// matched case-insensitively against `provider/id`.
type ModelScopeRule struct {
	Enforce *bool    `json:"enforce,omitempty"`
	Strict  *bool    `json:"strict,omitempty"`
	Allow   []string `json:"allow,omitempty"`
}

// ModelScopeConfig is the parsed `subagents.modelScope` settings value: the
// global rule plus optional per-agent restrictions keyed by canonical name.
type ModelScopeConfig struct {
	Enforce *bool                      `json:"enforce,omitempty"`
	Strict  *bool                      `json:"strict,omitempty"`
	Allow   []string                   `json:"allow,omitempty"`
	Agents  map[string]*ModelScopeRule `json:"agents,omitempty"`
}

// modelScopeViolation is the original's ModelScopeViolation.
type modelScopeViolation struct {
	Model          string
	Severity       string // "warn" or "error"
	Message        string
	AllowedPattern []string
	Origin         string
}

// resolvedModelScope is one scope resolved for an agent at launch time.
type resolvedModelScope struct {
	Enforce bool
	Strict  bool
	Allow   []string
	Origin  string
}

// stripThinkingSuffix returns the model id without a known thinking suffix.
func stripThinkingSuffix(model string) string {
	base, _ := splitThinkingSuffix(model)
	return base
}

// scopePatternRe turns one glob pattern into the original's anchored,
// case-insensitive regular expression: regex specials are escaped, `*` becomes
// `.*` (globToRegExp in the original).
func scopePatternRe(pattern string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString(`(?i)^`)
	for _, r := range pattern {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?', '+', '.', '|', '(', ')', '[', ']', '{', '}', '^', '$', '\\', '#':
			b.WriteString(regexp.QuoteMeta(string(r)))
		default:
			b.WriteRune(r)
		}
	}
	b.WriteString(`$`)
	re, err := regexp.Compile(b.String())
	if err != nil {
		return regexp.MustCompile(`(?i)^$`)
	}
	return re
}

// matchesScopePattern reports whether the model matches one allow pattern; both
// sides are compared against the full `provider/id` with the thinking suffix
// stripped from the model.
func matchesScopePattern(model, pattern string) bool {
	return scopePatternRe(pattern).MatchString(stripThinkingSuffix(model))
}

// checkModelScope is the original's pure scope decision: a violation when the
// model is out of scope and enforcement is on, otherwise nil.
func checkModelScope(model string, scope *ModelScopeRule, origin, source string) *modelScopeViolation {
	if model == "" || scope == nil || scope.Enforce == nil || !*scope.Enforce || len(scope.Allow) == 0 {
		return nil
	}
	for _, pattern := range scope.Allow {
		if matchesScopePattern(model, pattern) {
			return nil
		}
	}
	base := stripThinkingSuffix(model)
	severity := "warn"
	if source == "explicit" || (scope.Strict != nil && *scope.Strict) {
		severity = "error"
	}
	if origin == "" {
		origin = "modelScope"
	}
	return &modelScopeViolation{
		Model:          base,
		Severity:       severity,
		AllowedPattern: scope.Allow,
		Origin:         origin,
		Message: fmt.Sprintf("Model '%s' is outside the configured subagent model scope (%s). Allowed patterns: %s.",
			base, origin, strings.Join(scope.Allow, ", ")),
	}
}

// assertScopeRecord validates one settings field is an object (assertRecord in
// the original).
func assertScopeRecord(value any, field, filePath, expected string) (map[string]any, string) {
	obj, ok := value.(map[string]any)
	if !ok || obj == nil {
		return nil, fmt.Sprintf("Subagent settings in '%s' have invalid '%s'; expected %s.", filePath, field, expected)
	}
	return obj, ""
}

// parseScopeAllowList validates one allow array (parseAllowList in the
// original).
func parseScopeAllowList(value any, field, filePath string) ([]string, string) {
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Sprintf("Subagent settings in '%s' have invalid '%s'; expected an array of strings.", filePath, field)
	}
	allow := []string{}
	for _, entry := range items {
		s, ok := entry.(string)
		if !ok {
			return nil, fmt.Sprintf("Subagent settings in '%s' have invalid '%s'; expected an array of strings.", filePath, field)
		}
		trimmed := jsTrim(s)
		if trimmed != "" {
			allow = append(allow, trimmed)
		}
	}
	if len(allow) == 0 {
		return nil, fmt.Sprintf("Subagent settings in '%s' have invalid '%s'; expected a non-empty array of patterns.", filePath, field)
	}
	return allow, ""
}

// parseScopeRule parses one rule object (parseScopeRule in the original).
func parseScopeRule(input map[string]any, field, filePath string) (*ModelScopeRule, string) {
	rule := &ModelScopeRule{}
	if v, present := input["enforce"]; present {
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Sprintf("Subagent settings in '%s' have invalid '%s.enforce'; expected a boolean.", filePath, field)
		}
		rule.Enforce = &b
	}
	if v, present := input["strict"]; present {
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Sprintf("Subagent settings in '%s' have invalid '%s.strict'; expected a boolean.", filePath, field)
		}
		rule.Strict = &b
	}
	if v, present := input["allow"]; present {
		allow, errText := parseScopeAllowList(v, field+".allow", filePath)
		if errText != "" {
			return nil, errText
		}
		rule.Allow = allow
	}
	return rule, ""
}

// parseModelScopeConfig validates and normalizes a raw `subagents.modelScope`
// value from settings (parseModelScopeConfig in the original). It returns the
// config and an error text; both empty/nil when the field is absent.
func parseModelScopeConfig(value any, filePath string) (*ModelScopeConfig, string) {
	if value == nil {
		return nil, ""
	}
	input, errText := assertScopeRecord(value, "modelScope", filePath, "an object")
	if errText != "" {
		return nil, errText
	}
	config := &ModelScopeConfig{}
	if v, present := input["enforce"]; present {
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Sprintf("Subagent settings in '%s' have invalid 'modelScope.enforce'; expected a boolean.", filePath)
		}
		config.Enforce = &b
	}
	if v, present := input["strict"]; present {
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Sprintf("Subagent settings in '%s' have invalid 'modelScope.strict'; expected a boolean.", filePath)
		}
		config.Strict = &b
	}
	if v, present := input["allow"]; present {
		allow, errText := parseScopeAllowList(v, "modelScope.allow", filePath)
		if errText != "" {
			return nil, errText
		}
		config.Allow = allow
	}
	if v, present := input["agents"]; present {
		agents, errText := assertScopeRecord(v, "modelScope.agents", filePath, "an object keyed by agent name")
		if errText != "" {
			return nil, errText
		}
		names := make([]string, 0, len(agents))
		for name := range agents {
			names = append(names, name)
		}
		sort.Strings(names)
		config.Agents = map[string]*ModelScopeRule{}
		for _, rawName := range names {
			name := jsTrim(rawName)
			field := "modelScope.agents." + name
			if name == "" {
				return nil, fmt.Sprintf("Subagent settings in '%s' have invalid 'modelScope.agents' key; expected a non-empty agent name.", filePath)
			}
			agentInput, errText := assertScopeRecord(agents[rawName], field, filePath, "an object")
			if errText != "" {
				return nil, errText
			}
			if _, nested := agentInput["agents"]; nested {
				return nil, fmt.Sprintf("Subagent settings in '%s' have invalid '%s.agents'; nested agent scopes are not supported.", filePath, field)
			}
			rule, errText := parseScopeRule(agentInput, field, filePath)
			if errText != "" {
				return nil, errText
			}
			config.Agents[name] = rule
		}
	}
	hasAnyAllow := len(config.Allow) > 0
	for _, scope := range config.Agents {
		if len(scope.Allow) > 0 {
			hasAnyAllow = true
		}
	}
	if config.Enforce != nil && *config.Enforce && !hasAnyAllow {
		return nil, fmt.Sprintf("Subagent settings in '%s' set modelScope.enforce without a non-empty 'allow' list; supply allowed model patterns or disable enforcement.", filePath)
	}
	if config.Enforce == nil && config.Strict == nil && len(config.Allow) == 0 && len(config.Agents) == 0 {
		return nil, ""
	}
	return config, ""
}

// expandInheritPattern resolves the `inherit` pattern against the parent model
// (expandInheritPattern in the original).
func expandInheritPattern(pattern, parentProvider, parentID string) string {
	if pattern == "inherit" && parentProvider != "" && parentID != "" {
		return parentProvider + "/" + parentID
	}
	return pattern
}

// resolveModelScopesForAgent merges the global and matching agent policies
// into launch-time checks (resolveModelScopesForAgent in the original).
func resolveModelScopesForAgent(config *ModelScopeConfig, agentName, parentProvider, parentID string) []resolvedModelScope {
	if config == nil {
		return nil
	}
	var scopes []resolvedModelScope
	if len(config.Allow) > 0 {
		rule := resolvedModelScope{Origin: "modelScope"}
		if config.Enforce != nil {
			rule.Enforce = *config.Enforce
		}
		if config.Strict != nil {
			rule.Strict = *config.Strict
		}
		for _, pattern := range config.Allow {
			rule.Allow = append(rule.Allow, expandInheritPattern(pattern, parentProvider, parentID))
		}
		scopes = append(scopes, rule)
	}
	if agentScope, ok := config.Agents[agentName]; ok && len(agentScope.Allow) > 0 {
		rule := resolvedModelScope{Origin: "modelScope.agents." + agentName}
		rule.Enforce = config.Enforce != nil && *config.Enforce
		rule.Strict = config.Strict != nil && *config.Strict
		if agentScope.Enforce != nil {
			rule.Enforce = *agentScope.Enforce
		}
		if agentScope.Strict != nil {
			rule.Strict = *agentScope.Strict
		}
		for _, pattern := range agentScope.Allow {
			rule.Allow = append(rule.Allow, expandInheritPattern(pattern, parentProvider, parentID))
		}
		scopes = append(scopes, rule)
	}
	return scopes
}

// checkResolvedScopes applies every resolved scope to one model
// (configuredScopes + the error/warn split in the original's resolution). It
// returns the first error-severity violation, or nil; warn-severity violations
// are reported through onWarn.
func checkResolvedScopes(model string, scopes []resolvedModelScope, source string, onWarn func(*modelScopeViolation)) *modelScopeViolation {
	var first *modelScopeViolation
	for _, scope := range scopes {
		rule := &ModelScopeRule{Enforce: boolPtr(scope.Enforce), Strict: boolPtr(scope.Strict), Allow: scope.Allow}
		if v := checkModelScope(model, rule, scope.Origin, source); v != nil {
			if v.Severity == "error" {
				return v
			}
			if onWarn != nil {
				onWarn(v)
			}
			if first == nil {
				first = v
			}
		}
	}
	return nil
}

func boolPtr(b bool) *bool { return &b }
