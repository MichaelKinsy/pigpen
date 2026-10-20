package pi_subagents

import (
	"regexp"
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

func normalizeWatchdogEmissionText(value string) string { panic("not implemented") }

func watchdogWarningUnderlyingIdentity(w wdWarning) string { panic("not implemented") }

func watchdogWarningIdentity(w wdWarning) string { panic("not implemented") }

func wdIsContentFree(value string) bool { panic("not implemented") }

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

func newWatchdogEmissionGuard(maxWarnings *int) *watchdogEmissionGuard { panic("not implemented") }

func (g *watchdogEmissionGuard) startModelUpdate() { panic("not implemented") }

func (g *watchdogEmissionGuard) reset() { panic("not implemented") }

// evaluate decides on a warning. allowRepeatOf names one already-accepted identity that may repeat (boundary re-findings
// before stalemate).
func (g *watchdogEmissionGuard) evaluate(w wdWarning, allowRepeatOf string) watchdogEmissionDecision {
	panic("not implemented")
}
