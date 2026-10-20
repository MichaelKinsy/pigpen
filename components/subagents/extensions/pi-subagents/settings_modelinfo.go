package pi_subagents

import (
	"fmt"
	"math"
	"strings"
)

// Model metadata helpers (src/shared/model-info.ts).

// thinkingLevelList is THINKING_LEVELS.
var thinkingLevelList = []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}

// CostTier is one tier of a model's pricing (pi-ai ModelCost.tiers).
type CostTier struct {
	InputTokensAbove                     float64
	Input, Output, CacheRead, CacheWrite float64
}

// ModelCost is a registry model's price per 1M tokens.
type ModelCost struct {
	Input, Output, CacheRead, CacheWrite float64
	Tiers                                []CostTier
}

// ModelInfo is model-info.ts ModelInfo. ThinkingLevelMap holds a level's mapped name; a present key with a nil value is the
// original's `null` (the level is unsupported), an absent key is undefined.
type ModelInfo struct {
	Provider, ID, FullID, API string
	Reasoning                 *bool
	ThinkingLevelMap          map[string]*string
	ContextWindow             float64
	MaxTokens                 float64
	Input                     []string
	Cost                      *ModelCost
}

// RegistryModel is the model-info.ts RegistryModelLike input of toModelInfo.
type RegistryModel struct {
	Provider, ID, API string
	Reasoning         *bool
	ThinkingLevelMap  map[string]*string
	ContextWindow     *float64
	MaxTokens         *float64
	Input             []string
	Cost              *ModelCost
}

func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

// toModelInfo is model-info.ts:toModelInfo.
func toModelInfo(m RegistryModel) ModelInfo {
	info := ModelInfo{Provider: m.Provider, ID: m.ID, FullID: m.Provider + "/" + m.ID, API: m.API, Reasoning: m.Reasoning, ThinkingLevelMap: m.ThinkingLevelMap}
	if m.ContextWindow != nil && finite(*m.ContextWindow) && *m.ContextWindow > 0 {
		info.ContextWindow = *m.ContextWindow
	}
	if m.MaxTokens != nil && finite(*m.MaxTokens) && *m.MaxTokens > 0 {
		info.MaxTokens = *m.MaxTokens
	}
	if len(m.Input) > 0 {
		info.Input = append([]string(nil), m.Input...)
	}
	if m.Cost != nil && finite(m.Cost.Input) && finite(m.Cost.Output) {
		c := *m.Cost
		if m.Cost.Tiers != nil {
			c.Tiers = append([]CostTier(nil), m.Cost.Tiers...)
		}
		info.Cost = &c
	}
	return info
}

// splitKnownThinkingSuffix is model-info.ts:splitKnownThinkingSuffix.
func splitKnownThinkingSuffix(model string) (baseModel, thinkingSuffix string) {
	i := strings.LastIndex(model, ":")
	if i == -1 {
		return model, ""
	}
	for _, l := range thinkingLevelList {
		if model[i+1:] == l {
			return model[:i], ":" + l
		}
	}
	return model, ""
}

// resolveEffectiveThinking is model-info.ts:resolveEffectiveThinking; configThinking is nil, false or a string.
func resolveEffectiveThinking(model string, configThinking any) string {
	if model == "" {
		return ""
	}
	if _, suffix := splitKnownThinkingSuffix(model); suffix != "" {
		return suffix[1:]
	}
	if s, ok := configThinking.(string); ok {
		for _, l := range thinkingLevelList {
			if l == s {
				return l
			}
		}
	}
	return ""
}

// childThinkingLevel is model-info.ts:childThinkingLevel over the recorded thinking strings.
func childThinkingLevel(sources ...string) string {
	for _, s := range sources {
		for _, l := range thinkingLevelList {
			if l == s {
				return l
			}
		}
	}
	return ""
}

// findModelInfo is model-info.ts:findModelInfo.
func findModelInfo(model string, available []ModelInfo, preferredProvider string) *ModelInfo {
	if model == "" || len(available) == 0 {
		return nil
	}
	base, _ := splitKnownThinkingSuffix(model)
	for i := range available {
		if available[i].FullID == base {
			return &available[i]
		}
	}
	var matches []*ModelInfo
	for i := range available {
		if available[i].ID == base {
			matches = append(matches, &available[i])
		}
	}
	if preferredProvider != "" {
		for _, m := range matches {
			if m.Provider == preferredProvider {
				return m
			}
		}
	}
	if len(matches) == 1 {
		return matches[0]
	}
	return nil
}

// getSupportedThinkingLevels is model-info.ts:getSupportedThinkingLevels; a nil model is the legacy list.
func getSupportedThinkingLevels(model *ModelInfo) []string {
	legacy := func() []string {
		var out []string
		for _, l := range thinkingLevelList {
			if l != "max" {
				out = append(out, l)
			}
		}
		return out
	}
	if model == nil {
		return legacy()
	}
	if model.Reasoning != nil && !*model.Reasoning {
		return []string{"off"}
	}
	if model.ThinkingLevelMap == nil {
		return legacy()
	}
	var out []string
	for _, l := range thinkingLevelList {
		mapped, present := model.ThinkingLevelMap[l]
		if present && mapped == nil {
			continue
		}
		if (l == "xhigh" || l == "max") && !present {
			continue
		}
		out = append(out, l)
	}
	return out
}

// thinkingLevelRank is the position of a level in thinkingLevelList (thinking-ceiling.ts thinkingLevelRanks).
func thinkingLevelRank(level string) (int, bool) {
	for i, l := range thinkingLevelList {
		if l == level {
			return i, true
		}
	}
	return 0, false
}

// compareThinkingLevels is thinking-ceiling.ts:compareThinkingLevels.
func compareThinkingLevels(left, right string) (int, error) {
	l, ok1 := thinkingLevelRank(left)
	r, ok2 := thinkingLevelRank(right)
	if !ok1 || !ok2 {
		return 0, fmt.Errorf("Invalid thinking level comparison; expected one of %s.", strings.Join(thinkingLevelList, ", "))
	}
	return l - r, nil
}

// intersectThinkingCeilings is thinking-ceiling.ts:intersectThinkingCeilings: the lowest set ceiling, "" for none.
func intersectThinkingCeilings(ceilings ...string) (string, error) {
	lowest := ""
	for _, c := range ceilings {
		if c == "" {
			continue
		}
		if lowest == "" {
			lowest = c
			continue
		}
		cmp, err := compareThinkingLevels(c, lowest)
		if err != nil {
			return "", err
		}
		if cmp < 0 {
			lowest = c
		}
	}
	return lowest, nil
}

// ThinkingCeilingCheck is thinking-ceiling.ts ThinkingCeilingCheck.
type ThinkingCeilingCheck struct {
	Model          string
	ConfigThinking any
	Ceiling        string
	Agent, RunID   string
}

// assertThinkingWithinCeiling is thinking-ceiling.ts:assertThinkingWithinCeiling.
func assertThinkingWithinCeiling(in ThinkingCeilingCheck) error {
	if in.Ceiling == "" {
		return nil
	}
	requested := resolveEffectiveThinking(in.Model, in.ConfigThinking)
	if requested == "" {
		return nil
	}
	level, err := parseThinkingLevel(requested, "requested thinking level")
	if err != nil {
		return err
	}
	cmp, err := compareThinkingLevels(level, in.Ceiling)
	if err != nil {
		return err
	}
	if cmp <= 0 {
		return nil
	}
	var subject []string
	if in.Agent != "" {
		subject = append(subject, "agent '"+in.Agent+"'")
	}
	if in.RunID != "" {
		subject = append(subject, "run '"+in.RunID+"'")
	}
	s := ""
	if len(subject) > 0 {
		s = " for " + strings.Join(subject, " ")
	}
	return fmt.Errorf("Thinking level '%s' exceeds configured maximum '%s'%s.", level, in.Ceiling, s)
}
