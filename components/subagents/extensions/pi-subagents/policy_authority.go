package pi_subagents

import (
	"fmt"
	"strings"
)

// Port of src/policy/authority.ts.

var authorityActions = []string{"discardWorktree", "destructiveCleanup", "spawnBudgetGrant", "scheduleCreate", "stopRun", "steerRun", "inspectorOpen", "projectOpen"}

var defaultAuthorityPolicy = map[string]string{
	"discardWorktree": "confirm", "destructiveCleanup": "confirm", "spawnBudgetGrant": "confirm", "scheduleCreate": "auto",
	"stopRun": "auto", "steerRun": "auto", "inspectorOpen": "auto", "projectOpen": "confirm",
}

// authorityPolicy maps an action to "auto", "confirm" or "forbid".
type authorityPolicy map[string]string

// resolveAuthorityDecision is the configured decision for an action, else the default.
func resolveAuthorityDecision(action string, policy authorityPolicy) string {
	if d, ok := policy[action]; ok {
		return d
	}
	return defaultAuthorityPolicy[action]
}

// validateAuthorityPolicy is the original's validateAuthorityPolicy (nil for undefined).
func validateAuthorityPolicy(value any, label string) (authorityPolicy, error) {
	if isUndefined(value) {
		return nil, nil
	}
	obj, ok := asObject(value)
	if !ok {
		return nil, fmt.Errorf("%s must be a JSON object", label)
	}
	allowed := setOf(authorityActions...)
	policy := authorityPolicy{}
	for _, action := range obj.order() {
		if !allowed[action] {
			return nil, fmt.Errorf("%s.%s is unknown; expected one of %s", label, action, strings.Join(authorityActions, ", "))
		}
		d, _ := obj.vals[action].(string)
		if d != "auto" && d != "confirm" && d != "forbid" {
			return nil, fmt.Errorf(`%s.%s must be "auto", "confirm", or "forbid"`, label, action)
		}
		policy[action] = d
	}
	return policy, nil
}
