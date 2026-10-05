package powerline_footer

import (
	"fmt"
	"regexp"
	"testing"
)

var ansiSGR = regexp.MustCompile("\x1b\\[[0-9;]*m")

func stripAnsi(text string) string { return ansiSGR.ReplaceAllString(text, "") }

// plainTheme colors nothing, like the original tests' plainTheme().
type plainTheme struct{}

func (plainTheme) Fg(_, text string) (string, error) { return text, nil }

// noTheme fails on any lookup, like a theme that must not be consulted.
type noTheme struct{}

func (noTheme) Fg(token, _ string) (string, error) {
	return "", fmt.Errorf("unexpected theme color lookup %q", token)
}

func f(v float64) *float64 { return &v }

// newCtx is createSegmentContext: an empty session with plain colors; mods adjust it.
func newCtx(opts segmentOptions, mods ...func(*segmentContext)) segmentContext {
	c := segmentContext{
		ThinkingLevel: "off", ContextTokens: f(0), ContextPercent: f(0), AutoCompactEnabled: true,
		SessionStart: clock(), HiddenStatusKeys: map[string]bool{}, CustomItems: map[string]customItem{},
		Options: opts, Theme: plainTheme{}, Colors: colorScheme{},
	}
	for _, m := range mods {
		m(&c)
	}
	return c
}

func hexAnsi(hex string) string {
	var r, g, b int
	fmt.Sscanf(hex[1:], "%02x%02x%02x", &r, &g, &b)
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r, g, b)
}

func withNerdFonts(t *testing.T, on string) { setenv(t, "POWERLINE_NERD_FONTS", &on) }

func TestThinkingSegment(t *testing.T) {
	level := func(l string, colors colorScheme) renderedSegment {
		return renderSegment("thinking", newCtx(segmentOptions{}, func(c *segmentContext) {
			c.ThinkingLevel, c.Colors, c.Theme = l, colors, noTheme{}
		}))
	}
	tw(t, "thinking-segment", "thinking segment uses per-level colors for off through medium", func(t *testing.T) {
		colors := colorScheme{"thinking": "#111111", "thinkingMinimal": "#222222", "thinkingLow": "#333333", "thinkingMedium": "#444444"}
		eq(t, level("off", colors).Content, hexAnsi("#111111")+"think:off\x1b[0m", "off")
		eq(t, level("minimal", colors).Content, hexAnsi("#222222")+"think:min\x1b[0m", "minimal")
		eq(t, level("low", colors).Content, hexAnsi("#333333")+"think:low\x1b[0m", "low")
		eq(t, level("medium", colors).Content, hexAnsi("#444444")+"think:med\x1b[0m", "medium")
	})
	tw(t, "thinking-segment", "thinking segment uses rainbow styling for high through max by default", func(t *testing.T) {
		for _, l := range []string{"high", "xhigh", "max"} {
			eq(t, level(l, colorScheme{"thinking": "#111111"}), renderedSegment{Content: rainbow("think:" + l), Visible: true}, l)
		}
	})
	tw(t, "thinking-segment", "thinking segment honors per-level colors for high through max", func(t *testing.T) {
		colors := colorScheme{"thinkingHigh": "#111111", "thinkingXhigh": "#222222", "thinkingMax": "#333333"}
		eq(t, level("high", colors).Content, hexAnsi("#111111")+"think:high\x1b[0m", "high")
		eq(t, level("xhigh", colors).Content, hexAnsi("#222222")+"think:xhigh\x1b[0m", "xhigh")
		eq(t, level("max", colors).Content, hexAnsi("#333333")+"think:max\x1b[0m", "max")
	})
}

func ctxUsage(tokens *float64, window float64, pct *float64, mods ...func(*segmentContext)) func(*segmentContext) {
	return func(c *segmentContext) {
		c.ContextTokens, c.ContextWindow, c.ContextPercent = tokens, window, pct
		for _, m := range mods {
			m(c)
		}
	}
}

func approx(c *segmentContext) { c.ContextApproximate = true }

func TestUsageDisplay(t *testing.T) {
	withNerd := func(t *testing.T) { withNerdFonts(t, "0") }
	pctOpts := segmentOptions{Context: &contextOptions{Format: s("percent")}}
	render := func(id string, c segmentContext) string { return stripAnsi(renderSegment(id, c).Content) }
	tw(t, "usage-display", "context_pct defaults to the full tokens/window rendering", func(t *testing.T) {
		withNerd(t)
		eq(t, render("context_pct", newCtx(segmentOptions{}, ctxUsage(f(12300), 200000, f(6.15)))), "◫ 12k/200k (6.2%) AC", "full")
	})
	tw(t, "usage-display", "context_pct percent format renders a bare rounded percentage", func(t *testing.T) {
		withNerd(t)
		eq(t, render("context_pct", newCtx(pctOpts, ctxUsage(f(12300), 200000, f(6.15)))), "6%", "percent")
	})
	tw(t, "usage-display", "context_pct renders unknown usage after compaction", func(t *testing.T) {
		withNerd(t)
		eq(t, render("context_pct", newCtx(segmentOptions{}, ctxUsage(nil, 200000, nil))), "◫ ?/200k AC", "full")
		eq(t, render("context_pct", newCtx(pctOpts, ctxUsage(nil, 200000, nil))), "?", "percent")
	})
	tw(t, "usage-display", "context_pct marks context estimates as approximate", func(t *testing.T) {
		withNerd(t)
		eq(t, render("context_pct", newCtx(segmentOptions{}, ctxUsage(f(18000), 272000, f(6.6176), approx))), "◫ ~18k/272k (6.6%) AC", "full")
		eq(t, render("context_pct", newCtx(pctOpts, ctxUsage(f(18000), 272000, f(6.6176), approx))), "~7%", "percent")
	})
	tw(t, "usage-display", "context_pct percent format keeps threshold colors and drops icons", func(t *testing.T) {
		withNerd(t)
		for _, c := range []struct {
			pct      float64
			expected string
		}{{69, "69%"}, {85, "85%"}, {95, "95%"}} {
			rendered := renderSegment("context_pct", newCtx(pctOpts, ctxUsage(f(c.pct), 100, f(c.pct))))
			eq(t, stripAnsi(rendered.Content), c.expected, "text")
			eq(t, contains(rendered.Content, "◫"), false, "no context icon in percent mode")
			eq(t, contains(rendered.Content, "AC"), false, "no auto-compact icon in percent mode")
		}
	})
	usage := func(in, out, cr, cw float64) func(*segmentContext) {
		return func(c *segmentContext) { c.Usage = usageStats{Input: in, Output: out, CacheRead: cr, CacheWrite: cw} }
	}
	cacheOpts := func(format string) segmentOptions {
		return segmentOptions{CacheRead: &cacheReadOptions{Format: s(format)}}
	}
	tw(t, "usage-display", "cache_read defaults to raw token count", func(t *testing.T) {
		withNerd(t)
		eq(t, render("cache_read", newCtx(segmentOptions{}, usage(1000, 0, 12300, 0))), "cache in: 12k", "default")
	})
	tw(t, "usage-display", "cache_read percent format renders the cache hit rate", func(t *testing.T) {
		withNerd(t)
		eq(t, render("cache_read", newCtx(cacheOpts("percent"), usage(2000, 0, 8000, 0))), "cache 80%", "percent")
	})
	tw(t, "usage-display", "cache_read both format renders raw token count and cache hit rate", func(t *testing.T) {
		withNerd(t)
		eq(t, render("cache_read", newCtx(cacheOpts("both"), usage(2000, 0, 8000, 0))), "cache in: 8.0k (80%)", "both")
	})
	tw(t, "usage-display", "cache_read percent and both formats handle zero input without NaN", func(t *testing.T) {
		withNerd(t)
		eq(t, render("cache_read", newCtx(cacheOpts("percent"), usage(0, 0, 5, 0))), "cache 100%", "percent")
		eq(t, render("cache_read", newCtx(cacheOpts("both"), usage(0, 0, 5, 0))), "cache in: 5 (100%)", "both")
		eq(t, renderSegment("cache_read", newCtx(cacheOpts("both"))), renderedSegment{Content: "", Visible: false}, "hidden")
	})
	tw(t, "usage-display", "queue segment hides when empty", func(t *testing.T) {
		withNerd(t)
		eq(t, renderSegment("queue", newCtx(segmentOptions{})), renderedSegment{Content: "", Visible: false}, "empty")
	})
	tw(t, "usage-display", "queue segment summarizes queued and blocked items", func(t *testing.T) {
		withNerd(t)
		c := newCtx(segmentOptions{}, func(c *segmentContext) { c.Queue = queueSummary{QueueCount: 2, BlockedCount: 1} })
		eq(t, render("queue", c), "q 2 · blocked 1", "summary")
	})
	tw(t, "usage-display", "queue segment highlights compaction-held prompts", func(t *testing.T) {
		withNerd(t)
		c := newCtx(segmentOptions{}, func(c *segmentContext) { c.Queue = queueSummary{QueueCount: 1, Compacting: true} })
		eq(t, render("queue", c), "compact q 1", "compacting")
	})
	tw(t, "usage-display", "parsePowerlineConfig accepts context and cache_read formats", func(t *testing.T) {
		cfg := parsePowerlineConfig(js(t, `{"context":{"format":"percent"},"cache_read":{"format":"both"}}`), []string{"default", "compact"})
		cx, cr := ctxOpts(cfg.SegmentOptions)
		eq(t, sv(cx), "percent", "context")
		eq(t, sv(cr), "both", "cache_read")
	})
	tw(t, "usage-display", "parsePowerlineConfig ignores invalid format values", func(t *testing.T) {
		cfg := parsePowerlineConfig(js(t, `{"context":{"format":"bogus"},"cache_read":{"format":42}}`), []string{"default", "compact"})
		cx, cr := ctxOpts(cfg.SegmentOptions)
		eq(t, cx == nil, true, "context")
		eq(t, cr == nil, true, "cache_read")
	})
	tw(t, "usage-display", "parsePowerlineConfig defaults to upstream rendering when options are absent", func(t *testing.T) {
		cfg := parsePowerlineConfig(js(t, `{}`), []string{"default", "compact"})
		eq(t, cfg.SegmentOptions.Context == nil, true, "context")
		eq(t, cfg.SegmentOptions.CacheRead == nil, true, "cache_read")
	})
	tw(t, "usage-display", "mergeSegmentOptions merges context and cache_read per key", func(t *testing.T) {
		merged := mergeSegmentOptions(
			segmentOptions{Context: &contextOptions{Format: s("percent")}, CacheRead: &cacheReadOptions{Format: s("percent")}},
			segmentOptions{Context: &contextOptions{Format: s("full")}, CacheRead: &cacheReadOptions{Format: s("both")}})
		cx, cr := ctxOpts(merged)
		eq(t, sv(cx), "full", "context")
		eq(t, sv(cr), "both", "cache_read")
	})
}
