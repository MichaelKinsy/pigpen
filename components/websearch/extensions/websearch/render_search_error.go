package websearch

import (
	"fmt"
	"math"
	"strings"
)

// Port of render-search-error.ts: the plan of how a failed or cancelled tool result is drawn. It
// is plain strings (no theme, no components) so it can be tested without a terminal; render.go
// applies the colors.

// CancelledQueryDetail is one query of a cancelled curation.
type CancelledQueryDetail struct {
	Query       string
	Provider    string // "" is null
	Error       string // "" is null: the query completed
	ResultCount int
}

// SearchErrorDetails are the details a failed or cancelled result carries.
type SearchErrorDetails struct {
	// Error is the headline; nil means the result has none.
	Error              *string
	Cancelled          bool
	CancelReason       string
	BrowserConnected   *bool
	LastHeartbeatAgeMs *float64
	QueryCount         float64
	CancelledQueries   []CancelledQueryDetail
	ExtraLines         []string
}

// SearchErrorPlan is how an error is shown: Expanded when expanded, Collapsed under the headline
// otherwise, and the hint that tells how many lines are hidden (nil when none).
type SearchErrorPlan struct {
	Expanded   []string
	Collapsed  []string
	ExpandHint *string
}

func truncateWithEllipsis(text string, max int) string {
	if jsLen(text) > max {
		return jsSlice(text, 0, max-1) + "\u2026"
	}
	return text
}

// BuildSearchErrorPlan is buildSearchErrorPlan: nil when the details carry no error or cancel
// signal, so the caller falls through to the success renderer.
func BuildSearchErrorPlan(d *SearchErrorDetails) *SearchErrorPlan {
	if d == nil || ((d.Error == nil || *d.Error == "") && !d.Cancelled) {
		return nil
	}
	headline := "Search cancelled."
	if d.Error != nil {
		headline = *d.Error
	}
	queries := d.CancelledQueries
	queryCount := len(queries)
	if d.QueryCount > 0 {
		queryCount = int(d.QueryCount)
	}
	done := len(queries)
	errored := 0
	for _, q := range queries {
		if q.Error != "" {
			errored++
		}
	}

	// Rich diagnostics only make sense when there is something to diagnose; a bare argument error
	// stays one clean line.
	extras := d.ExtraLines
	rich := d.Cancelled || len(queries) > 0 || len(extras) > 0
	if !rich {
		return &SearchErrorPlan{Expanded: []string{headline}, Collapsed: []string{}}
	}

	expanded := []string{headline, ""}
	if d.Cancelled || len(queries) > 0 {
		var diag []string
		if d.Cancelled {
			reason := d.CancelReason
			if reason == "" {
				reason = "unknown"
			}
			diag = append(diag, "cancel reason   : "+reason)
		}
		browser := "unknown"
		if d.BrowserConnected != nil {
			browser = "never connected"
			if *d.BrowserConnected {
				browser = "connected"
			}
		}
		diag = append(diag, "browser         : "+browser)
		if d.LastHeartbeatAgeMs != nil && !math.IsNaN(*d.LastHeartbeatAgeMs) && !math.IsInf(*d.LastHeartbeatAgeMs, 0) {
			diag = append(diag, fmt.Sprintf("last heartbeat  : %ss ago", jsNumber(jsRound(*d.LastHeartbeatAgeMs/1000))))
		}
		if queryCount > 0 {
			diag = append(diag, fmt.Sprintf("queries started : %d", queryCount), fmt.Sprintf("queries done    : %d", done))
			if errored > 0 {
				diag = append(diag, fmt.Sprintf("queries errored : %d", errored))
			}
		}
		expanded = append(expanded, "Diagnostics:")
		for _, line := range diag {
			expanded = append(expanded, "  "+line)
		}
	}

	if len(queries) > 0 {
		expanded = append(expanded, "", "Per-query results (gathered before cancel):")
		for _, q := range queries {
			tag := "[ok]  "
			tail := ""
			if q.Error != "" {
				tag = "[err] "
				tail = "\u2014 " + truncateWithEllipsis(q.Error, 60)
			} else {
				plural := "s"
				if q.ResultCount == 1 {
					plural = ""
				}
				tail = fmt.Sprintf("\u2014 %d source%s", q.ResultCount, plural)
			}
			provider := ""
			if q.Provider != "" {
				provider = " (" + q.Provider + ")"
			}
			expanded = append(expanded, fmt.Sprintf(`  %s"%s"%s %s`, tag, truncateWithEllipsis(q.Query, 52), provider, tail))
		}
	}

	if len(extras) > 0 {
		expanded = append(expanded, "", "Details:")
		for _, e := range extras {
			expanded = append(expanded, "  "+e)
		}
	}

	collapsed := []string{}
	var parts []string
	if queryCount > 0 {
		parts = append(parts, fmt.Sprintf("%d/%d queries completed", done, queryCount))
	}
	if errored > 0 {
		parts = append(parts, fmt.Sprintf("%d errored", errored))
	}
	if d.BrowserConnected != nil && !*d.BrowserConnected {
		parts = append(parts, "browser never connected")
	} else if d.CancelReason != "" {
		parts = append(parts, "reason: "+d.CancelReason)
	}
	if len(parts) > 0 {
		collapsed = append(collapsed, strings.Join(parts, "; ")+".")
	}
	if len(collapsed) == 0 && len(extras) > 0 {
		for _, e := range extras[:min(2, len(extras))] {
			collapsed = append(collapsed, truncateWithEllipsis(e, 100))
		}
	}

	hidden := max(0, len(expanded)-(1+len(collapsed)))
	var hint *string
	if hidden > 0 {
		h := fmt.Sprintf("... (%d more lines, %d total, ctrl+o to expand)", hidden, len(expanded))
		hint = &h
	}
	return &SearchErrorPlan{Expanded: expanded, Collapsed: collapsed, ExpandHint: hint}
}

// jsRound is Math.round: halves go up.
func jsRound(f float64) float64 { return math.Floor(f + 0.5) }
