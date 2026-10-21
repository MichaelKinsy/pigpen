package tintinweb_subagents

import (
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
)

// The `scopeModels` policy, shared by the Agent tool and (when nested delegation lands) the nested tools, so a nested
// spawn cannot escape the allowlist. The state lives here and not in the app for the same reason disableDefaults
// lives in agent_types.go. upstream: src/model-scope.ts.

var scopeModelsEnabled atomic.Bool

func isScopeModelsEnabled() bool         { return scopeModelsEnabled.Load() }
func setScopeModelsEnabled(enabled bool) { scopeModelsEnabled.Store(enabled) }

// modelScopeVerdict: ok (in scope, or nothing to validate against), error (a choice the caller made: refuse the
// spawn with the message) or warn (pinned in an agent file, or inherited from the parent: proceed, but tell the user).
type modelScopeVerdict struct {
	kind    string // "ok", "error" or "warn"
	message string
}

type modelScopeCheck struct {
	provider, id   string // the effective model; empty when none was resolved
	cwd            string
	registry       modelRegistry
	callerSupplied bool   // the model came from the tool call rather than an agent file
	agentLabel     string // for the warning
	modelInput     string // the raw `model:` input, when there was one
}

// checkModelScope checks the effective model against the user's enabledModels. scopeModels guards against what the
// LLM chooses at run time, not against the user's own configuration: an out-of-scope model the orchestrator picked is
// refused (it can pick again), one the user pinned in an agent file or inherited from the parent only warns.
// upstream: model-scope.ts checkModelScope.
func checkModelScope(c modelScopeCheck) modelScopeVerdict {
	if !isScopeModelsEnabled() || c.id == "" {
		return modelScopeVerdict{kind: "ok"}
	}
	patterns, has := readEnabledModels(c.cwd)
	allowed := resolveEnabledModels(patterns, has, c.registry, c.cwd)
	if allowed == nil || isModelInScope(c.provider, c.id, allowed) {
		return modelScopeVerdict{kind: "ok"}
	}
	if c.callerSupplied {
		list := make([]string, 0, len(allowed))
		for m := range allowed {
			list = append(list, "  "+m)
		}
		sort.Strings(list)
		return modelScopeVerdict{kind: "error", message: fmt.Sprintf("Model not in scope: %q.\n\nAllowed models (from enabledModels):\n%s", c.modelInput, strings.Join(list, "\n"))}
	}
	label := c.modelInput
	if label == "" {
		label = c.provider + "/" + c.id
	}
	return modelScopeVerdict{kind: "warn", message: fmt.Sprintf("Agent %q using out-of-scope model %q", c.agentLabel, label)}
}
