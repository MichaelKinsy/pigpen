package pi_subagents

import (
	"fmt"
	"strings"
)

// The extension part of the child launch plan (src/runs/shared/child-tool-plan.ts resolvePiLaunchToolPlan): which extensions a
// child loads and whether ambient extension discovery is switched off. The rest of the plan (tool allowlists, MCP selection,
// runtime extensions of the original's own TypeScript, required extensions) belongs to the child launch pipeline.

// builtinChildTools are the tool names that are never extension paths.
var builtinChildTools = map[string]bool{"read": true, "bash": true, "edit": true, "write": true, "grep": true, "find": true, "ls": true}

type extensionLaunchPlan struct {
	// ExtensionArgs are the extension paths handed to the child, without duplicates.
	ExtensionArgs []string
	// DisableAmbientExtensions is set when the agent declares an extension allowlist (even an empty one) or the ceiling denies extensions.
	DisableAmbientExtensions bool
	Warnings                 []string
}

func uniqueJoin(lists ...[]string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, l := range lists {
		for _, s := range l {
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	return out
}

// resolveExtensionLaunchPlan is the extension decisions of resolvePiLaunchToolPlan for one agent definition.
func resolveExtensionLaunchPlan(a AgentConfig, denyExtensions bool) extensionLaunchPlan {
	var toolExt []string
	for _, t := range a.Tools {
		if !builtinChildTools[t] && (strings.Contains(t, "/") || strings.HasSuffix(t, ".ts") || strings.HasSuffix(t, ".js")) {
			toolExt = append(toolExt, t)
		}
	}
	disable := denyExtensions || a.Extensions != nil
	var plan extensionLaunchPlan
	plan.DisableAmbientExtensions = disable
	if !denyExtensions && a.Extensions != nil && len(a.Extensions) == 0 {
		label := ""
		if a.Name != "" {
			label = " for agent '" + a.Name + "'"
		}
		plan.Warnings = append(plan.Warnings, "extensions: [] override"+label+" disables ALL ambient extensions for this child (not just \"adds nothing\"), "+
			"including any model-provider extension needed to resolve a provider-qualified model. "+
			"List the extensions this child actually needs instead of an empty array.")
	}
	if disable {
		var configured []string
		if !denyExtensions {
			configured = append(append(append(configured, toolExt...), a.Extensions...), a.SubagentOnlyExtensions...)
		}
		plan.ExtensionArgs = uniqueJoin(configured)
	} else {
		plan.ExtensionArgs = uniqueJoin(toolExt, a.SubagentOnlyExtensions)
	}
	return plan
}

const fastModeProviderPrefix = "openai-codex/"

// applyThinkingSuffix appends a thinking level to a model unless it already carries one (replaceExisting swaps it); a model
// without a level request, and a thinking that is off or unset, are returned unchanged.
func applyThinkingSuffix(model string, thinking any, replaceExisting bool) string {
	level, ok := thinking.(string)
	if model == "" || !ok || level == "" {
		return model
	}
	if base, suffix := splitKnownThinkingSuffix(model); suffix != "" {
		if replaceExisting {
			return base + ":" + level
		}
		return model
	}
	return model + ":" + level
}

// resolveFastModeExtension says whether a child needs the fast mode runtime extension: only native OpenAI-Codex models support it.
func resolveFastModeExtension(fast bool, model, agentName string) (needed bool, err error) {
	if !fast {
		return false, nil
	}
	if model == "" {
		label := ""
		if agentName != "" {
			label = " for agent '" + agentName + "'"
		}
		return false, fmt.Errorf("fast mode requires an explicit supported native OpenAI-Codex model%s.", label)
	}
	base, _ := splitKnownThinkingSuffix(model)
	if !strings.HasPrefix(base, fastModeProviderPrefix) {
		return false, fmt.Errorf("fast mode supports only native %s* models; unsupported model: %s.", fastModeProviderPrefix, base)
	}
	return true, nil
}
