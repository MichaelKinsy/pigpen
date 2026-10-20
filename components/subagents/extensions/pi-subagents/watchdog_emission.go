package pi_subagents

import (
	"regexp"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// The emission guard (watchdog/emission-guard.ts): what a review may say once.

type watchdogEmissionDecision struct {
	Accepted           bool
	Reason             string // "content-free", "duplicate", "max-warnings" or "update-budget" when not accepted
	Identity           string
	UnderlyingIdentity string
	Escalation         bool
}

var wdContentFreePhrases = map[string]bool{
	"stop": true, "done": true, "complete": true, "completed": true, "no issue": true, "no issues": true, "no concern": true,
	"no concerns": true, "nothing to add": true, "lgtm": true, "looks good": true, "looks good to me": true, "all good": true,
	"ok": true, "okay": true, "none": true, "n a": true,
}

var (
	wdQuoteChars   = regexp.MustCompile("[’'`]")
	wdNonWordRuns  = regexp.MustCompile(`[^\p{L}\p{N}]+`)
	wdWhitespaceRe = regexp.MustCompile(`\s+`)
)

func normalizeWatchdogEmissionText(value string) string {
	v := norm.NFKC.String(value)
	v = strings.ToLower(v)
	v = wdQuoteChars.ReplaceAllString(v, "")
	v = wdNonWordRuns.ReplaceAllString(v, " ")
	v = wdWhitespaceRe.ReplaceAllString(v, " ")
	return jsTrim(v)
}

func watchdogWarningUnderlyingIdentity(w wdWarning) string {
	return normalizeWatchdogEmissionText(w.Summary) + "\n" + normalizeWatchdogEmissionText(w.Evidence)
}

func watchdogWarningIdentity(w wdWarning) string {
	return w.Severity + "\n" + watchdogWarningUnderlyingIdentity(w)
}

func wdIsContentFree(value string) bool {
	n := normalizeWatchdogEmissionText(value)
	return n == "" || wdContentFreePhrases[n]
}

type watchdogEmissionGuard struct {
	maxWarnings        *int
	dedupeHistoryLimit int
	acceptedCount      int
	accepted           map[string]string
	historyOrder       []string
	updateIdentity     string
	updateSeverity     string
	updateStarted      bool
}

func newWatchdogEmissionGuard(maxWarnings *int) *watchdogEmissionGuard {
	return &watchdogEmissionGuard{maxWarnings: maxWarnings, dedupeHistoryLimit: 200, accepted: map[string]string{}}
}

func (g *watchdogEmissionGuard) startModelUpdate() {
	g.updateIdentity, g.updateSeverity, g.updateStarted = "", "", false
}

func (g *watchdogEmissionGuard) reset() {
	g.acceptedCount = 0
	g.accepted = map[string]string{}
	g.historyOrder = nil
	g.startModelUpdate()
}

// evaluate decides on a warning. allowRepeatOf names one already-accepted identity that may repeat (boundary re-findings
// before stalemate).
func (g *watchdogEmissionGuard) evaluate(w wdWarning, allowRepeatOf string) watchdogEmissionDecision {
	if wdIsContentFree(w.Summary) || wdIsContentFree(w.Evidence) || wdIsContentFree(w.RecommendedAction) {
		return watchdogEmissionDecision{Reason: "content-free"}
	}
	underlying := watchdogWarningUnderlyingIdentity(w)
	identity := watchdogWarningIdentity(w)
	prior, seen := g.accepted[underlying]
	escalation := seen && prior == "concern" && w.Severity == "blocker"
	if g.updateStarted {
		updateEscalation := g.updateIdentity == underlying && g.updateSeverity == "concern" && w.Severity == "blocker"
		if !updateEscalation {
			return watchdogEmissionDecision{Reason: "update-budget", Identity: identity, UnderlyingIdentity: underlying}
		}
	}
	repeat := seen && allowRepeatOf == identity
	if seen && !escalation && !repeat {
		return watchdogEmissionDecision{Reason: "duplicate", Identity: identity, UnderlyingIdentity: underlying}
	}
	if g.maxWarnings != nil && g.acceptedCount >= *g.maxWarnings && !escalation && !repeat {
		return watchdogEmissionDecision{Reason: "max-warnings", Identity: identity, UnderlyingIdentity: underlying}
	}
	g.accepted[underlying] = w.Severity
	if !seen {
		g.acceptedCount++
		g.historyOrder = append(g.historyOrder, underlying)
	}
	for len(g.historyOrder) > g.dedupeHistoryLimit {
		stale := g.historyOrder[0]
		g.historyOrder = g.historyOrder[1:]
		if stale != "" {
			delete(g.accepted, stale)
		}
	}
	g.updateIdentity, g.updateSeverity, g.updateStarted = underlying, w.Severity, true
	return watchdogEmissionDecision{Accepted: true, Identity: identity, UnderlyingIdentity: underlying, Escalation: escalation}
}
