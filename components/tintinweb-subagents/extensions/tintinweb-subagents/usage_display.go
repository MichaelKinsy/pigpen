package tintinweb_subagents

import (
	"fmt"
	"strconv"
	"strings"
)

// The display readers of the usage accumulator. These carry the same names and signatures as the copies in the UI
// lane's ui_usage.go (the lane lead keeps one at merge). upstream: src/usage.ts getLifetimeTotal, getLifetimeCost;
// src/ui/agent-widget.ts formatCost.

// lifetimeUsage is the usage components accumulated over message_end events; it survives compaction. upstream:
// usage.ts LifetimeUsage (cacheRead and cost are absent there when never seen; here that reads as 0).
type lifetimeUsage struct {
	Input, Output, CacheWrite, CacheRead int
	Cost                                 float64
}

// getLifetimeTotal is the DISPLAY token total: input + output + cacheWrite (cacheRead and cost excluded).
func getLifetimeTotal(u lifetimeUsage) int { return u.Input + u.Output + u.CacheWrite }

// getLifetimeCost is the accumulated cost in USD, 0 when unpriced.
func getLifetimeCost(u lifetimeUsage) float64 { return u.Cost }

// formatCost is `~$0.0042`, or "" when there is nothing to show (zero and unpriced models read the same).
func formatCost(cost float64) string {
	if !(cost > 0) {
		return ""
	}
	if cost < 0.0001 {
		return "<$0.0001"
	}
	if cost >= 1 {
		return fmt.Sprintf("~$%.2f", cost)
	}
	rounded, _ := strconv.ParseFloat(strconv.FormatFloat(cost, 'f', 4, 64), 64)
	text := strconv.FormatFloat(rounded, 'f', -1, 64)
	decimals := 0
	if _, frac, ok := strings.Cut(text, "."); ok {
		decimals = len(frac)
	}
	return fmt.Sprintf("~$%.*f", max(2, decimals), rounded)
}
