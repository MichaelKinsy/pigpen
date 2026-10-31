package pi_subagents

// Model-scope enforcement (src/runs/shared/model-scope.ts): optional settings
// restrictions on the model a subagent resolves. An explicitly supplied model
// (`--model`, a tool `model`) outside the scope is a hard error; an inherited
// model (agent frontmatter, defaultModel, the parent session) only warns,
// unless the scope is strict.

import (
	"fmt"
	"regexp"
	"strings"
)

// ModelScopeRule is one scope rule: enforce switches the check on, strict makes
// inherited models hard errors, allow holds glob patterns (only `*` is special)
// matched against `provider/id`.
type ModelScopeRule struct {
	Enforce *bool    `json:"enforce,omitempty"`
	Strict  *bool    `json:"strict,omitempty"`
	Allow   []string `json:"allow,omitempty"`
}

// modelScopeViolation is the original's ModelScopeViolation.
type modelScopeViolation struct {
	Model          string
	Severity       string // "warn" or "error"
	Message        string
	AllowedPattern []string
	Origin         string
}

// stripThinkingSuffix removes a known thinking suffix from a model id.
func stripThinkingSuffix(model string) string {
	base, _ := splitThinkingSuffix(model)
	return base
}

// scopePatternRe turns one glob pattern into the original's anchored,
// case-insensitive regular expression: every regex special except `*` is
// escaped, and `*` becomes `.*`.
func scopePatternRe(pattern string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString(`(?i)^`)
	for _, r := range pattern {
		switch r {
		case '.', '+', '^', '$', '{', '}', '(', ')', '|', '[', ']', '\\':
			b.WriteString(regexp.QuoteMeta(string(r)))
		case '*':
			b.WriteString(".*")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
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
