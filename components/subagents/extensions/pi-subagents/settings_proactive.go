package pi_subagents

import (
	"fmt"
	"sort"
	"strings"
)

// Proactive skill-subagent suggestions (src/agents/proactive-skills.ts).

const (
	defaultMinReferences      = 2
	defaultMaxRecommendations = 3
	defaultPreferredAgent     = "reviewer"
	maxRecommendationCap      = 5
)

var fallbackAgentOrder = []string{"reviewer", "delegate"}

// ProactiveSkillSubagentsConfig is types.ts ProactiveSkillSubagentsConfig (the `false` form is passed as the bool false).
type ProactiveSkillSubagentsConfig struct {
	Enabled            *bool
	MinReferences      *float64
	MaxRecommendations *float64
	PreferredAgent     *string
}

type resolvedProactiveConfig struct {
	enabled                           bool
	minReferences, maxRecommendations int
	preferredAgent                    string
}

// ProactiveSkillSubagentRecommendation is proactive-skills.ts ProactiveSkillSubagentRecommendation.
type ProactiveSkillSubagentRecommendation struct {
	Skill, Agent string
	References   int
	Sources      []string
	Description  *string
	Reason       string
}

// ProactiveAvailableSkill is the skill metadata a recommendation reads.
type ProactiveAvailableSkill struct {
	Name                   string
	Description            *string
	DisableModelInvocation *bool
}

func positiveInteger(v *float64) (int, bool) {
	if v == nil || !isIntegerNum(*v) || *v < 1 {
		return 0, false
	}
	return int(*v), true
}

// resolveProactiveSkillSubagentsConfig resolves a config: nil, the bool false, or *ProactiveSkillSubagentsConfig.
func resolveProactiveSkillSubagentsConfig(config any) resolvedProactiveConfig {
	if b, ok := config.(bool); ok && !b {
		return resolvedProactiveConfig{false, defaultMinReferences, defaultMaxRecommendations, defaultPreferredAgent}
	}
	c, _ := config.(*ProactiveSkillSubagentsConfig)
	if c == nil {
		c = &ProactiveSkillSubagentsConfig{}
	}
	maxRec := defaultMaxRecommendations
	if n, ok := positiveInteger(c.MaxRecommendations); ok {
		maxRec = n
	}
	minRef := defaultMinReferences
	if n, ok := positiveInteger(c.MinReferences); ok {
		minRef = n
	}
	if maxRec > maxRecommendationCap {
		maxRec = maxRecommendationCap
	}
	r := resolvedProactiveConfig{enabled: true, minReferences: minRef, maxRecommendations: maxRec, preferredAgent: defaultPreferredAgent}
	if c.Enabled != nil {
		r.enabled = *c.Enabled
	}
	if c.PreferredAgent != nil && jsTrim(*c.PreferredAgent) != "" {
		r.preferredAgent = jsTrim(*c.PreferredAgent)
	}
	return r
}

func normalizeSkillNames(v any) []string {
	switch x := v.(type) {
	case []any:
		var parts []string
		for _, e := range x {
			if s, ok := e.(string); ok {
				parts = append(parts, s)
			}
		}
		return dedupeTrimmed(parts)
	case []string:
		return dedupeTrimmed(x)
	case string:
		return dedupeTrimmed(strings.Split(x, ","))
	}
	return nil
}

func collectJSONStepSkills(step *jsObject, out map[string]bool, order *[]string) {
	v := step.vals["skills"]
	if v == nil || isUndefined(v) {
		v = step.vals["skill"]
	}
	for _, s := range normalizeSkillNames(v) {
		if !out[s] {
			out[s] = true
			*order = append(*order, s)
		}
	}
	switch p := step.vals["parallel"].(type) {
	case []any:
		for _, c := range p {
			if o, ok := c.(*jsObject); ok {
				collectJSONStepSkills(o, out, order)
			}
		}
	case *jsObject:
		collectJSONStepSkills(p, out, order)
	}
}

func chainSkills(c ChainConfig) []string {
	seen := map[string]bool{}
	var order []string
	add := func(items []string) {
		for _, s := range dedupeTrimmed(items) {
			if !seen[s] {
				seen[s] = true
				order = append(order, s)
			}
		}
	}
	for _, step := range c.Steps {
		add(step.Skills.Items)
	}
	for _, s := range c.JSONSteps {
		if o, ok := s.(*jsObject); ok {
			collectJSONStepSkills(o, seen, &order)
		}
	}
	return order
}

func chooseRecommendationAgent(agents []AgentConfig, preferred string) string {
	var enabled []AgentConfig
	for _, a := range agents {
		if a.Disabled == nil || !*a.Disabled {
			enabled = append(enabled, a)
		}
	}
	has := func(n string) bool {
		for _, a := range enabled {
			if a.Name == n {
				return true
			}
		}
		return false
	}
	if has(preferred) {
		return preferred
	}
	for _, n := range fallbackAgentOrder {
		if has(n) {
			return n
		}
	}
	if len(enabled) > 0 {
		return enabled[0].Name
	}
	return ""
}

// recommendProactiveSkillSubagents is proactive-skills.ts:109. availableSkills nil means unrestricted.
func recommendProactiveSkillSubagents(agents []AgentConfig, chains []ChainConfig, availableSkills []ProactiveAvailableSkill, config any) []ProactiveSkillSubagentRecommendation {
	cfg := resolveProactiveSkillSubagentsConfig(config)
	if !cfg.enabled {
		return nil
	}
	agent := chooseRecommendationAgent(agents, cfg.preferredAgent)
	if agent == "" {
		return nil
	}
	var available map[string]ProactiveAvailableSkill
	if availableSkills != nil {
		available = map[string]ProactiveAvailableSkill{}
		for _, s := range availableSkills {
			available[s.Name] = s
		}
	}
	counts := map[string]map[string]bool{}
	var order []string
	add := func(skill, source string) {
		if skill == subagentOrchestrationSkill {
			return
		}
		if counts[skill] == nil {
			counts[skill] = map[string]bool{}
			order = append(order, skill)
		}
		counts[skill][source] = true
	}
	for _, a := range agents {
		if a.Disabled != nil && *a.Disabled {
			continue
		}
		for _, s := range a.Skills {
			add(s, "agent:"+a.Name)
		}
	}
	for _, c := range chains {
		for _, s := range chainSkills(c) {
			add(s, "chain:"+c.Name)
		}
	}
	var out []ProactiveSkillSubagentRecommendation
	for _, skill := range order {
		sources := counts[skill]
		meta, ok := available[skill]
		if len(sources) < cfg.minReferences || (available != nil && !ok) || (ok && meta.DisableModelInvocation != nil && *meta.DisableModelInvocation) {
			continue
		}
		var list []string
		for s := range sources {
			list = append(list, s)
		}
		sort.SliceStable(list, func(i, j int) bool { return localeLess(list[i], list[j]) })
		var desc *string
		if ok {
			desc = meta.Description
		}
		out = append(out, ProactiveSkillSubagentRecommendation{Skill: skill, Agent: agent, References: len(sources), Sources: list, Description: desc,
			Reason: fmt.Sprintf("referenced by %d configured agents/chains", len(sources))})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].References != out[j].References {
			return out[i].References > out[j].References
		}
		return localeLess(out[i].Skill, out[j].Skill)
	})
	if len(out) > cfg.maxRecommendations {
		out = out[:cfg.maxRecommendations]
	}
	return out
}

// formatProactiveSkillSubagentRecommendations is proactive-skills.ts:162.
func formatProactiveSkillSubagentRecommendations(recs []ProactiveSkillSubagentRecommendation) []string {
	if len(recs) == 0 {
		return nil
	}
	lines := []string{"Proactive skill subagent suggestions:"}
	for _, r := range recs {
		n := len(r.Sources)
		if n > 3 {
			n = 3
		}
		sample := strings.Join(r.Sources[:n], ", ")
		extra := ""
		if len(r.Sources) > 3 {
			extra = fmt.Sprintf(", +%d more", len(r.Sources)-3)
		}
		desc := ""
		if r.Description != nil && *r.Description != "" {
			desc = " - " + *r.Description
		}
		lines = append(lines, fmt.Sprintf("- %s via %s (%s; %s%s)%s", r.Skill, r.Agent, r.Reason, sample, extra, desc))
	}
	lines = append(lines, "Guardrails: use these for broad tasks where a skill-specialist pass is useful; keep fanout small, use fresh context unless private/session context is explicitly needed, and skip when the user asks for a direct answer.")
	return lines
}

// buildProactiveSkillSubagentRecommendationLines is proactive-skills.ts:178: discovery is skipped when disabled and a discovery
// failure is no suggestions.
func buildProactiveSkillSubagentRecommendationLines(agents []AgentConfig, chains []ChainConfig, config any, discover func() ([]ProactiveAvailableSkill, error)) []string {
	if !resolveProactiveSkillSubagentsConfig(config).enabled {
		return nil
	}
	available, err := discover()
	if err != nil || available == nil {
		available = []ProactiveAvailableSkill{}
	}
	return formatProactiveSkillSubagentRecommendations(recommendProactiveSkillSubagents(agents, chains, available, config))
}
