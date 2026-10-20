package tintinweb_subagents

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/pigpen/components/pitui/jsstring"
)

// Usage readers and the cost/token formatters the surfaces share. upstream: src/usage.ts (the display readers) and
// ui/agent-widget.ts (formatCost, formatSessionTokens, formatTurns).

// lifetimeUsage is upstream LifetimeUsage: input, output and cache-write tokens and the cost of every assistant message
// of an agent (cacheRead is kept for billing, out of the display total).
type lifetimeUsage struct {
	Input, Output, CacheWrite, CacheRead int
	Cost                                 float64
}

// getLifetimeTotal is the lifetime token total for display: input + output + cacheWrite. cacheRead and cost are out.
func getLifetimeTotal(u lifetimeUsage) int { return u.Input + u.Output + u.CacheWrite }

// getLifetimeCost is the accumulated cost in USD, 0 when unpriced.
func getLifetimeCost(u lifetimeUsage) float64 { return u.Cost }

// sessionStats is what the surfaces read of a session's stats. upstream: usage.ts SessionStatsLike.
type sessionStats struct {
	Input, Output, CacheWrite int
	ContextPercent            *float64
}

// sessionLike is upstream SessionLike (getSessionStats may throw: it returns an error here).
type sessionLike interface{ GetSessionStats() (sessionStats, error) }

// getSessionTokens is input + output + cacheWrite of the current session window, 0 without a session or on a failure.
func getSessionTokens(s sessionLike) int {
	if s == nil {
		return 0
	}
	st, err := s.GetSessionStats()
	if err != nil {
		return 0
	}
	return st.Input + st.Output + st.CacheWrite
}

// getSessionContextPercent is the context-window utilization (0-100), nil when unavailable.
func getSessionContextPercent(s sessionLike) *float64 {
	if s == nil {
		return nil
	}
	st, err := s.GetSessionStats()
	if err != nil {
		return nil
	}
	return st.ContextPercent
}

// widgetTokens is a token count compactly: "33.8k token", "1.2M token". upstream: ui/agent-widget.ts formatTokens.
func widgetTokens(count int) string {
	switch {
	case count >= 1_000_000:
		return jsstring.ToFixed(float64(count)/1_000_000, 1) + "M token"
	case count >= 1_000:
		return jsstring.ToFixed(float64(count)/1_000, 1) + "k token"
	}
	return strconv.Itoa(count) + " token"
}

// formatCost is a cost as `~$0.0042`, or "" when there is nothing to show (zero, negative or NaN: an unpriced model).
// A real cost below four decimals reads `<$0.0001`. upstream: ui/agent-widget.ts formatCost.
func formatCost(cost float64) string {
	if !(cost > 0) {
		return ""
	}
	if cost < 0.0001 {
		return "<$0.0001"
	}
	if cost >= 1 {
		return "~$" + jsstring.ToFixed(cost, 2)
	}
	// Under a dollar: cents at minimum, four decimals at most, nothing trailing.
	rounded, _ := strconv.ParseFloat(jsstring.ToFixed(cost, 4), 64)
	str := strconv.FormatFloat(rounded, 'f', -1, 64)
	decimals := 0
	if i := strings.Index(str, "."); i >= 0 {
		decimals = len(str) - i - 1
	}
	return "~$" + jsstring.ToFixed(rounded, max(2, decimals))
}

var ansiReset = regexp.MustCompile("\x1b\\[(?:0|39)m")

// fgPreservingNestedStyles applies a foreground color and restores it after nested foreground/full ANSI resets.
// upstream: ui/agent-widget.ts fgPreservingNestedStyles.
func fgPreservingNestedStyles(th uiTheme, color, text string) string {
	styledEmpty := th.Fg(color, "")
	styleStart := ansiReset.ReplaceAllString(styledEmpty, "")
	return th.Fg(color, ansiReset.ReplaceAllStringFunc(text, func(reset string) string { return reset + styleStart }))
}

// formatSessionTokens is the token count with an optional context-fill % (<70 dim, 70-85 warning, >=85 error) and a
// compaction count `⇊N`. upstream: ui/agent-widget.ts formatSessionTokens.
func formatSessionTokens(tokens int, percent *float64, th uiTheme, compactions int) string {
	tokenStr := widgetTokens(tokens)
	var annot []string
	if percent != nil {
		color := "dim"
		switch {
		case *percent >= 85:
			color = "error"
		case *percent >= 70:
			color = "warning"
		}
		annot = append(annot, th.Fg(color, strconv.FormatFloat(jsRound(*percent), 'f', -1, 64)+"%"))
	}
	if compactions > 0 {
		annot = append(annot, th.Fg("dim", "⇊"+strconv.Itoa(compactions)))
	}
	if len(annot) == 0 {
		return tokenStr
	}
	return tokenStr + " (" + strings.Join(annot, " · ") + ")"
}

// formatTurns is the turn count with an optional limit: "↻5≤30" or "↻5". upstream: formatTurns.
func formatTurns(turnCount int, maxTurns *int) string {
	if maxTurns != nil {
		return "↻" + strconv.Itoa(turnCount) + "≤" + strconv.Itoa(*maxTurns)
	}
	return "↻" + strconv.Itoa(turnCount)
}
