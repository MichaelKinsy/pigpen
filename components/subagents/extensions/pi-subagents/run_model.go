package pi_subagents

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// resolveRunModel is the model a single run starts its child with, resolved against the session's model registry as the original
// does before it launches (resolveEffectiveSubagentModel, then resolveModelSelection: subagent-executor.ts runSinglePath and
// execution.ts, async-execution.ts for a background run). An unknown model is the original's error; a run that inherits the
// parent's model starts the child with it. Thinking is applied by the child's arguments, not as a suffix here.
func resolveRunModel(ctx sdk.Context, d Discovery, a *AgentConfig, params map[string]any) (string, error) {
	var parent *ParentModel
	if id := ctx.Model(); id != "" {
		parent = &ParentModel{Provider: ctx.ModelProvider(), ID: id}
	}
	available := availableModels(ctx)
	for i := range available {
		if available[i].FullID == "" {
			available[i].FullID = available[i].Provider + "/" + available[i].ID
		}
	}
	preferred := a.ModelProvider
	if preferred == "" && parent != nil {
		preferred = parent.Provider
	}
	var explicit, configured any
	if m, _ := params["model"].(string); m != "" {
		explicit = m
	}
	if a.Model != "" {
		configured = a.Model
	}
	scopes := resolveModelScopesForAgent(d.ModelScope, a.Name, parent)
	origin := resolveModelOrigin(explicit, configured, parent, false, "")
	source := "inherited"
	if origin == "explicit" {
		source = "explicit"
	}
	primary, err := resolveEffectiveSubagentModel(explicit, configured, parent, available, preferred, &ResolveSubagentModelOverrideOptions{Scope: scopes, Source: source})
	if err != nil {
		return "", err
	}
	selection, err := resolveModelSelection(primary, available, preferred, &ResolveModelSelectionOptions{Scope: scopes, Origin: origin})
	if err != nil {
		return "", err
	}
	return selection.Model, nil
}
