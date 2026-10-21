package pi_permission_system

// Port of src/policy/synthesize.ts.

// SynthesizeDefaults is synthesizeDefaults: the single universal catch-all rule from the universal default. Per-surface catch-alls
// are regular config rules sitting later in the composed ruleset, so they override this one by last-match-wins. origin "" is
// "builtin".
func SynthesizeDefaults(universalDefault, origin string) Ruleset {
	if origin == "" {
		origin = "builtin"
	}
	return Ruleset{{Surface: "*", Pattern: "*", Action: universalDefault, Layer: "default", Origin: origin}}
}

// mcpBaselineTargets are the MCP metadata operation targets auto-allowed when any explicit MCP allow rule exists.
var mcpBaselineTargets = []string{"mcp_status", "mcp_list", "mcp_search", "mcp_describe", "mcp_connect"}

// SynthesizeBaseline is synthesizeBaseline: allow rules for the five MCP metadata targets, only when configRules holds at least one
// `mcp` allow rule. They sit before the config rules, so an explicit config deny still overrides them.
func SynthesizeBaseline(configRules Ruleset) Ruleset {
	hasAnyMcpAllow := false
	for _, r := range configRules {
		if r.Surface == "mcp" && r.Action == "allow" {
			hasAnyMcpAllow = true
			break
		}
	}
	if !hasAnyMcpAllow {
		return Ruleset{}
	}
	rules := make(Ruleset, len(mcpBaselineTargets))
	for i, target := range mcpBaselineTargets {
		rules[i] = Rule{Surface: "mcp", Pattern: target, Action: "allow", Layer: "baseline", Origin: "baseline"}
	}
	return rules
}

// ComposeRuleset is composeRuleset: defaults, then baseline, then config (lowest to highest priority). Session rules are not
// included: they are appended at check time so the cached composed ruleset stays session-agnostic.
func ComposeRuleset(defaults, baseline, config Ruleset) Ruleset {
	out := make(Ruleset, 0, len(defaults)+len(baseline)+len(config))
	out = append(out, defaults...)
	out = append(out, baseline...)
	return append(out, config...)
}
