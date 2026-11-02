package pi_subagents

import (
	"fmt"
	"os"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// disable, enable and reset (src/agents/agent-management.ts): they write the
// settings overrides of builtin/package agents and the custom definition files,
// with the original's texts.

const projectOverrideMissing = "Project override is not available here: no project config root (.pi or .agents) was found above the cwd. Use agentScope: 'user' or run from inside a project."

func init() {
	registerAction("disable", []string{"agent", "agentScope"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		return manageOutcome(disableAction(ctx.Cwd(), params))
	})
	registerAction("enable", []string{"agent", "agentScope"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		return manageOutcome(enableAction(ctx.Cwd(), params))
	})
	registerAction("reset", []string{"agent", "agentScope"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		return manageOutcome(resetAction(ctx.Cwd(), params))
	})
}

// resolveEffectiveAgent is the original's: the highest-precedence definition of
// the name, disabled ones included, so disable/enable can locate hidden targets.
func resolveEffectiveAgent(d *DiscoveryAll, name string) (*AgentConfig, string) {
	matches := allAgents(*d)
	a, msg := ResolveAgentName(name, matches)
	if a == nil && msg == "" && sanitizeName(name) != name {
		a, msg = ResolveAgentName(sanitizeName(name), matches)
	}
	if msg != "" {
		return nil, msg
	}
	if a == nil {
		return nil, ""
	}
	var best *AgentConfig
	for i := range matches {
		if matches[i].Name == a.Name {
			if best == nil || sourceRank[matches[i].Source] > sourceRank[best.Source] {
				best = &matches[i]
			}
		}
	}
	return best, ""
}

// disableAction is `subagent({action:"disable", agent, agentScope})`.
func disableAction(cwd string, params map[string]any) manageResult {
	name, _ := params["agent"].(string)
	if jsTrim(name) == "" {
		return manageResult{"Specify 'agent' for disable.", true}
	}
	scope, scopeErr := actionScope(params["agentScope"], "disable")
	if scopeErr != "" {
		return manageResult{scopeErr, true}
	}
	if scope == "project" && projectAgentSettingsPath(cwd) == "" {
		return manageResult{projectOverrideMissing, true}
	}
	d := DiscoverAgentsAll(cwd)
	effective, effErr := resolveEffectiveAgent(&d, name)
	if effErr != "" {
		return manageResult{effErr, true}
	}
	if effective == nil {
		return manageResult{fmt.Sprintf("Agent '%s' not found. Available: %s.", name, availableNames(cwd)), true}
	}
	runtimeName := effective.Name
	settingsPath, errText := mergeBuiltinAgentOverride(cwd, runtimeName, scope, map[string]any{"disabled": true})
	if errText != "" {
		return manageResult{errText, true}
	}
	after := DiscoverAgentsAll(cwd)
	ea, _ := resolveEffectiveAgent(&after, name)
	if ea != nil && ea.Disabled {
		return manageResult{text: fmt.Sprintf("Disabled agent '%s' via %s settings override at %s. It is now hidden from runtime discovery and { action: \"list\" }.", runtimeName, scope, settingsPath)}
	}
	otherScope := "project"
	if ea != nil && ea.OverrideScope != "" {
		otherScope = ea.OverrideScope
	}
	return manageResult{fmt.Sprintf("Wrote a disabled override for '%s' at %s, but the agent is still enabled. A higher-precedence %s override is likely winning. Try agentScope: '%s'.", runtimeName, settingsPath, otherScope, otherScope), true}
}

// enableAction is `subagent({action:"enable", agent, agentScope})`.
func enableAction(cwd string, params map[string]any) manageResult {
	name, _ := params["agent"].(string)
	if jsTrim(name) == "" {
		return manageResult{"Specify 'agent' for enable.", true}
	}
	scope, scopeErr := actionScope(params["agentScope"], "enable")
	if scopeErr != "" {
		return manageResult{scopeErr, true}
	}
	if scope == "project" && projectAgentSettingsPath(cwd) == "" {
		return manageResult{projectOverrideMissing, true}
	}
	d := DiscoverAgentsAll(cwd)
	effective, effErr := resolveEffectiveAgent(&d, name)
	if effErr != "" {
		return manageResult{effErr, true}
	}
	if effective == nil {
		return manageResult{fmt.Sprintf("Agent '%s' not found. Available: %s.", name, availableNames(cwd)), true}
	}
	runtimeName := effective.Name
	settingsPath, removed, errText := removeBuiltinAgentOverrideFields(cwd, runtimeName, scope, []string{"disabled"})
	if errText != "" {
		return manageResult{errText, true}
	}
	after := DiscoverAgentsAll(cwd)
	ea, _ := resolveEffectiveAgent(&after, name)
	if ea != nil && !ea.Disabled {
		if removed {
			return manageResult{text: fmt.Sprintf("Enabled agent '%s' (removed disabled override at %s).", runtimeName, settingsPath)}
		}
		return manageResult{text: fmt.Sprintf("Agent '%s' is already enabled.", runtimeName)}
	}
	if ea != nil && ea.OverrideScope != "" && ea.OverrideScope != scope {
		return manageResult{fmt.Sprintf("Agent '%s' is still disabled via a %s scope override at %s. Specify agentScope: '%s' to enable it.", runtimeName, ea.OverrideScope, ea.OverridePath, ea.OverrideScope), true}
	}
	stillScope, stillPath := scope, settingsPath
	if ea != nil {
		if ea.OverrideScope != "" {
			stillScope = ea.OverrideScope
		}
		if ea.OverridePath != "" {
			stillPath = ea.OverridePath
		}
	}
	return manageResult{fmt.Sprintf("Agent '%s' is still disabled after removing the %s disabled override. It may be hidden via subagents.disableBuiltins in %s settings at %s.", runtimeName, scope, stillScope, stillPath), true}
}

// resetAction is `subagent({action:"reset", agent, agentScope})`: back to the bundled default.
func resetAction(cwd string, params map[string]any) manageResult {
	name, _ := params["agent"].(string)
	if jsTrim(name) == "" {
		return manageResult{"Specify 'agent' for reset.", true}
	}
	raw := jsTrim(name)
	sanitized := sanitizeName(raw)
	scope, scopeErr := actionScope(params["agentScope"], "reset")
	if scopeErr != "" {
		return manageResult{scopeErr, true}
	}
	if scope == "project" && projectAgentSettingsPath(cwd) == "" {
		return manageResult{projectOverrideMissing, true}
	}
	d := DiscoverAgentsAll(cwd)
	var bundled *AgentConfig
	for _, set := range [][]AgentConfig{d.Package, d.Builtin} {
		for i := range set {
			if set[i].Name == raw || set[i].Name == sanitized {
				bundled = &set[i]
				break
			}
		}
		if bundled != nil {
			break
		}
	}
	if bundled == nil {
		for _, set := range [][]AgentConfig{d.Project, d.User} {
			for i := range set {
				if set[i].Name == raw || set[i].Name == sanitized {
					custom := set[i]
					return manageResult{fmt.Sprintf("Agent '%s' has no bundled default to reset to. Use { action: \"delete\", agent: \"%s\" } to remove the custom %s agent.", raw, custom.Name, custom.Source), true}
				}
			}
		}
		return manageResult{fmt.Sprintf("Agent '%s' not found. Available: %s.", raw, availableNames(cwd)), true}
	}
	runtimeName := bundled.Name
	custom := findCustomInScope(scopeSet(d, scope), raw, sanitized)
	var lines []string
	if custom != nil {
		if err := os.Remove(custom.FilePath); err != nil {
			return manageResult{err.Error(), true}
		}
		lines = append(lines, fmt.Sprintf("Deleted custom %s agent file at %s.", scope, custom.FilePath))
	}
	removalPath, removed, machinePreserved, errText := removeBuiltinAgentOverride(cwd, runtimeName, scope, true)
	if errText != "" {
		return manageResult{errText, true}
	}
	if removed {
		verb := "Removed"
		if machinePreserved {
			verb = "Cleared customization in"
		}
		suffix := ""
		if machinePreserved {
			suffix = " Retained machine placement."
		}
		lines = append(lines, fmt.Sprintf("%s %s settings override at %s.%s", verb, scope, removalPath, suffix))
	}
	if len(lines) == 0 {
		otherScope := "project"
		if scope == "project" {
			otherScope = "user"
		}
		otherCustom := findCustomInScope(scopeSet(d, otherScope), raw, sanitized)
		note := ""
		if otherCustom != nil {
			note = fmt.Sprintf(" Customization exists in %s scope; specify agentScope: '%s' to reset it.", otherScope, otherScope)
		}
		return manageResult{text: fmt.Sprintf("Agent '%s' has no %s customization to reset.%s It is at its bundled %s default.", runtimeName, scope, note, bundled.Source)}
	}
	lines = append(lines, fmt.Sprintf("Reset agent '%s' to its bundled %s default.", runtimeName, bundled.Source))
	return manageResult{text: strings.Join(lines, "\n")}
}

func findCustomInScope(set []AgentConfig, raw, sanitized string) *AgentConfig {
	for i := range set {
		if set[i].Name == raw || set[i].Name == sanitized {
			return &set[i]
		}
	}
	return nil
}
