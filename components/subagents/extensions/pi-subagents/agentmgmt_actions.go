package pi_subagents

import (
	"os"
	"path/filepath"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The management actions of the `subagent` tool, registered with the refusal seam (refuse.go).

func init() {
	for _, a := range []struct {
		name   string
		params []string
	}{
		{"list", []string{"agentScope", "capabilities"}},
		{"get", []string{"agent", "agentScope"}},
		{"models", []string{"agent", "agentScope"}},
		{"create", []string{"config"}},
		{"update", []string{"agent", "config", "agentScope"}},
		{"delete", []string{"agent", "agentScope"}},
		{"eject", []string{"agent", "agentScope"}},
		{"disable", []string{"agent", "agentScope"}},
		{"enable", []string{"agent", "agentScope"}},
		{"reset", []string{"agent", "agentScope"}},
	} {
		name := a.name
		registerAction(name, a.params, func(ctx sdk.Context, params map[string]any) (any, error) {
			return manageOutcome(handleManagementAction(name, params, newMgmtContext(ctx)))
		})
	}
}

// manageOutcome is the tool result of a management action: Pi reports a refused action as a failed tool call (the content, empty
// details, the error flag on the event); a successful one carries {mode: "management", results: [], ...details}.
func manageOutcome(r manageResult) (any, error) {
	if r.isError {
		return nil, sdk.NewToolError(r.text)
	}
	details := obj("mode", "management", "results", []any{})
	if r.details != nil {
		for _, k := range r.details.keys {
			details.set(k, r.details.vals[k])
		}
	}
	return explicitOK{Content: r.text, Details: details}, nil
}

// newMgmtContext builds the management context of a tool call from the host's session.
func newMgmtContext(ctx sdk.Context) mgmtContext {
	c := mgmtContext{cwd: ctx.Cwd()}
	if id := ctx.Model(); id != "" {
		c.model = &ParentModel{Provider: ctx.ModelProvider(), ID: id}
	}
	if sid, err := ctx.GetSessionID(); err == nil {
		c.sessionID = sid
	}
	c.available = func() []ModelInfo { return availableModels(ctx) }
	c.proactive = loadProactiveSkillConfig()
	c.onAgentsChanged = adv.refresh
	c.discoverAll = func(cwd, provider string) (DiscoveryAll, error) {
		return discoverAgentsAll(cwd, provider, adv.options())
	}
	return c
}

// availableModels lists the chat models of the session's registry that have working credentials (modelRegistry.getAvailable()).
func availableModels(ctx sdk.Context) []ModelInfo {
	models, err := ctx.ModelRegistry().GetAvailableOfType(sdk.ModelTypeChat)
	if err != nil {
		return nil
	}
	out := make([]ModelInfo, 0, len(models))
	for _, m := range models {
		out = append(out, modelInfoFromRegistry(m))
	}
	return out
}

func numField(m map[string]any, k string) *float64 {
	if f, ok := m[k].(float64); ok {
		return &f
	}
	return nil
}

// modelInfoFromRegistry is model-info.ts:toModelInfo over a registry model decoded from the host.
func modelInfoFromRegistry(m map[string]any) ModelInfo {
	r := RegistryModel{ContextWindow: numField(m, "contextWindow"), MaxTokens: numField(m, "maxTokens")}
	r.Provider, _ = m["provider"].(string)
	r.ID, _ = m["id"].(string)
	r.API, _ = m["api"].(string)
	if b, ok := m["reasoning"].(bool); ok {
		r.Reasoning = &b
	}
	if in, ok := m["input"].([]any); ok {
		r.Input = toStrings(in)
	}
	if tm, ok := m["thinkingLevelMap"].(map[string]any); ok {
		r.ThinkingLevelMap = map[string]*string{}
		for k, v := range tm {
			if s, ok := v.(string); ok {
				r.ThinkingLevelMap[k] = &s
			} else if v == nil {
				r.ThinkingLevelMap[k] = nil
			}
		}
	}
	if cost, ok := m["cost"].(map[string]any); ok {
		c := ModelCost{}
		c.Input, _ = cost["input"].(float64)
		c.Output, _ = cost["output"].(float64)
		c.CacheRead, _ = cost["cacheRead"].(float64)
		c.CacheWrite, _ = cost["cacheWrite"].(float64)
		if tiers, ok := cost["tiers"].([]any); ok {
			for _, t := range tiers {
				if tm, ok := t.(map[string]any); ok {
					var tier CostTier
					tier.InputTokensAbove, _ = tm["inputTokensAbove"].(float64)
					tier.Input, _ = tm["input"].(float64)
					tier.Output, _ = tm["output"].(float64)
					tier.CacheRead, _ = tm["cacheRead"].(float64)
					tier.CacheWrite, _ = tm["cacheWrite"].(float64)
					c.Tiers = append(c.Tiers, tier)
				}
			}
		}
		r.Cost = &c
	}
	return toModelInfo(r)
}

// subagentConfigPath is config.ts:getConfigPath.
func subagentConfigPath() string {
	return filepath.Join(agentDir(), "extensions", "subagent", "config.json")
}

// loadProactiveSkillConfig reads `proactiveSkillSubagents` from the extension config (config.ts): nil when the file or the key is
// absent or unreadable, the bool false, or the parsed settings.
func loadProactiveSkillConfig() any {
	b, err := os.ReadFile(subagentConfigPath())
	if err != nil {
		return nil
	}
	v, msg := jsJSONParse(string(b))
	o, _ := v.(*jsObject)
	if msg != "" || o == nil {
		return nil
	}
	switch x := o.vals["proactiveSkillSubagents"].(type) {
	case bool:
		if !x {
			return false
		}
	case *jsObject:
		c := &ProactiveSkillSubagentsConfig{}
		if e, ok := x.flag("enabled"); ok {
			c.Enabled = &e
		}
		if f, ok := x.num("minReferences"); ok {
			c.MinReferences = &f
		}
		if f, ok := x.num("maxRecommendations"); ok {
			c.MaxRecommendations = &f
		}
		if s, ok := x.str("preferredAgent"); ok {
			c.PreferredAgent = &s
		}
		return c
	}
	return nil
}
