package tintinweb_subagents

import (
	"math"
	"math/big"
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
		return "~$" + jsToFixed(cost, 2)
	}
	rounded, _ := strconv.ParseFloat(jsToFixed(cost, 4), 64)
	text := strconv.FormatFloat(rounded, 'f', -1, 64)
	decimals := 0
	if _, frac, ok := strings.Cut(text, "."); ok {
		decimals = len(frac)
	}
	return "~$" + jsToFixed(rounded, max(2, decimals))
}

// jsToFixed is Number.prototype.toFixed for a finite x (a negative one is rounded by its magnitude, as the spec does): the exact binary value is rounded to `digits` decimals,
// and an exact tie takes the larger neighbor (Go's strconv and %f round a tie to even, so 1.25 gives 1.2 there and
// "1.3" in JavaScript).
func jsToFixed(x float64, digits int) string {
	if x == 0 {
		x = 0 // (-0).toFixed(1) is "0.0"
	}
	if x < 0 {
		return "-" + jsToFixed(-x, digits)
	}
	// Away from a tie the correctly rounded strconv result is JavaScript's; the exact arithmetic is for near ties only.
	scaled := x * math.Pow10(digits)
	if scaled < 1e9 && math.Abs(scaled-math.Floor(scaled)-0.5) > 1e-6 {
		return strconv.FormatFloat(x, 'f', digits, 64)
	}
	r := new(big.Rat).SetFloat64(x)
	if r == nil { // NaN or infinite: not a value any caller formats
		return strconv.FormatFloat(x, 'f', digits, 64)
	}
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(digits)), nil)
	r.Mul(r, new(big.Rat).SetInt(scale))
	r.Add(r, big.NewRat(1, 2))
	n := new(big.Int).Div(r.Num(), r.Denom()) // floor: r >= 0
	text := n.String()
	if digits == 0 {
		return text
	}
	if len(text) <= digits {
		text = strings.Repeat("0", digits-len(text)+1) + text
	}
	return text[:len(text)-digits] + "." + text[len(text)-digits:]
}
