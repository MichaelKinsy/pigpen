package powerline_footer

import "math"

// Context window usage. upstream: context-usage.ts.

type contextUsage struct {
	Tokens  *float64 // nil: unknown (right after a compaction)
	Window  float64
	Percent *float64
}

func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

// readCoreContextUsage reads the host's context usage object ({tokens, contextWindow, percent}); nil when it is absent or unusable.
func readCoreContextUsage(usage map[string]any) *contextUsage {
	if usage == nil {
		return nil
	}
	window, ok := usage["contextWindow"].(float64)
	if !ok || !finite(window) || window <= 0 {
		return nil
	}
	raw, present := usage["tokens"]
	if !present {
		return nil
	}
	if raw == nil {
		return &contextUsage{Window: window}
	}
	tokens, ok := raw.(float64)
	if !ok || !finite(tokens) {
		return nil
	}
	percent := tokens / window * 100
	if p, ok := usage["percent"].(float64); ok && finite(p) {
		percent = p
	}
	return &contextUsage{Tokens: &tokens, Window: window, Percent: &percent}
}

// resolveDisplayContextUsage picks what the footer shows: the host's usage, an approximate estimate while the host's is unknown,
// or the last assistant message's usage.
func resolveDisplayContextUsage(core, unknownFallback *contextUsage, fallbackTokens, fallbackWindow float64) contextUsage {
	if core != nil && core.Tokens == nil && unknownFallback != nil {
		return *unknownFallback
	}
	if core != nil {
		return *core
	}
	percent := 0.0
	if fallbackWindow > 0 {
		percent = fallbackTokens / fallbackWindow * 100
	}
	return contextUsage{Tokens: &fallbackTokens, Window: fallbackWindow, Percent: &percent}
}

// estimateInitialContextTokens is Pi's conservative character estimate of the system prompt (four characters a token).
func estimateInitialContextTokens(systemPrompt *string) *int {
	if systemPrompt == nil || jsTrim(*systemPrompt) == "" {
		return nil
	}
	n := (utf16Len(*systemPrompt) + 3) / 4
	return &n
}

type usageSource interface {
	GetLeafID() (*string, error)
	ReadUsage() map[string]any
}

// coreContextUsageCache holds the host's usage for one leaf of the session.
type coreContextUsageCache struct {
	source usageSource
	leaf   *string
	usage  *contextUsage
	loaded bool
}

func (c *coreContextUsageCache) get(src usageSource) *contextUsage {
	leaf, err := src.GetLeafID()
	if err != nil {
		return readCoreContextUsage(src.ReadUsage())
	}
	if !c.loaded || c.source != src || !sameString(c.leaf, leaf) {
		c.source, c.leaf, c.usage, c.loaded = src, copyString(leaf), readCoreContextUsage(src.ReadUsage()), true
	}
	return c.usage
}

func (c *coreContextUsageCache) reset() { *c = coreContextUsageCache{} }
