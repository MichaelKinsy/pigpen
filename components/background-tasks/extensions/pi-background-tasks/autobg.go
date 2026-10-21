package pi_background_tasks

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Auto-backgrounding of blocking bash monitors. upstream: extensions/auto-background.ts.

// bashBackgroundDecision is what the original decides for one bash command.
type bashBackgroundDecision struct {
	forced        bool
	notifyOnExit  bool
	notifyOnOut   bool
	notifyPattern string
	reason        string
	title         string
}

// foldASCII maps the two non-ASCII characters Go's (?i) folds onto ASCII letters (U+017F to s, U+212A to k) to U+FFFD.
// JavaScript's non-unicode `i` flag never lets a non-ASCII character match an ASCII letter, so after this the Go
// patterns below match exactly what the original's do.
func foldASCII(s string) string {
	if !strings.ContainsAny(s, "\u017f\u212a") {
		return s
	}
	return strings.NewReplacer("\u017f", "\ufffd", "\u212a", "\ufffd").Replace(s)
}

var (
	reWatch        = regexp.MustCompile(`(?i)(?:^|[;&|]\s*)watch(?:\s|$)`)
	reFollow       = regexp.MustCompile(`(?i)\b(?:tail|journalctl)\b[^\n;|&]*\s-[^\s;|&]*f\b`)
	reSleepDelay   = regexp.MustCompile(`(?i)\bsleep\s+((?:\d+(?:\.\d+)?)|(?:\.\d+))\s*([smhd])?\b`)
	reMonitor      = regexp.MustCompile(`(?i)\b(?:pi-bridge|tmux|capture-pane|list-panes|has-session|delegate-state|subagent|session)\b`)
	reLoopKeyword  = regexp.MustCompile(`(?i)\b(?:for|while|until)\b`)
	reDo           = regexp.MustCompile(`(?i)\bdo\b`)
	reDone         = regexp.MustCompile(`(?i)\bdone\b`)
	reSleepAny     = regexp.MustCompile(`(?i)\bsleep\s+(?:\d+(?:\.\d+)?|\.\d+)`)
	reWhileForever = regexp.MustCompile(`(?i)\bwhile\s+(?:true|:)\b`)
	reUntil        = regexp.MustCompile(`(?i)\buntil\b`)
	reSeq          = regexp.MustCompile(`(?i)\$\(\s*seq\s+(?:(\d+)\s+)?(\d+)\s*\)`)
)

// collapseSpace is s.replace(/\s+/g, " ") with JavaScript's \s.
func collapseSpace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inSpace := false
	for _, r := range s {
		if isJSSpace(r) {
			if !inSpace {
				b.WriteByte(' ')
			}
			inSpace = true
			continue
		}
		inSpace = false
		b.WriteRune(r)
	}
	return b.String()
}

// normalizedCommand is command.replace(/\s+/g, " ").trim().
func normalizedCommand(command string) string { return jsTrim(collapseSpace(command)) }

// compactText is the original's compactText: whitespace collapsed, trimmed, cut to maxChars UTF-16 units with an ellipsis.
func compactText(value string, maxChars int) string {
	compact := jsTrim(collapseSpace(value))
	if jsLen(compact) > maxChars {
		return jsHead(compact, max(0, maxChars-1)) + "…"
	}
	return compact
}

// shellQuote is the original's shellQuote: single quotes, an embedded one as '\”.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func loopIterationCount(command string) (float64, bool) {
	m := reSeq.FindStringSubmatch(command)
	if m == nil {
		return 0, false
	}
	start := 1.0
	if m[1] != "" {
		start, _ = strconv.ParseFloat(m[1], 64)
	}
	end, _ := strconv.ParseFloat(m[2], 64)
	if math.IsInf(start, 0) || math.IsInf(end, 0) {
		return 0, false
	}
	return math.Abs(end-start) + 1, true
}

func sleepSeconds(command string) (float64, bool) {
	m := reSleepDelay.FindStringSubmatch(command)
	if m == nil {
		return 0, false
	}
	value, err := strconv.ParseFloat(m[1], 64)
	if err != nil || math.IsInf(value, 0) {
		return 0, false
	}
	unit := "s"
	if m[2] != "" {
		unit = strings.ToLower(m[2])
	}
	switch unit {
	case "d":
		return value * 86400, true
	case "h":
		return value * 3600, true
	case "m":
		return value * 60, true
	}
	return value, true
}

func looksLikeSessionMonitor(command string) bool { return reMonitor.MatchString(command) }

// hasUserPatterns reports whether the `autoBackgroundPatterns` setting holds a pattern line: a non-blank line that does
// not start with #.
func hasUserPatterns(raw string) bool {
	for _, line := range jsSplitLines(raw) {
		trimmed := jsTrim(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			return true
		}
	}
	return false
}

// builtinAutoBackgroundDecision is autoBackgroundDecision without the user's extra patterns: the built-in watch, follow,
// delayed-monitor and polling-loop rules. nil: the command is not diverted.
func builtinAutoBackgroundDecision(command string) *bashBackgroundDecision {
	normalized := foldASCII(normalizedCommand(command))
	if normalized == "" {
		return nil
	}
	if reWatch.MatchString(normalized) {
		return &bashBackgroundDecision{notifyOnExit: true, reason: "watch-style command", title: "watch: " + compactText(normalized, 72)}
	}
	if reFollow.MatchString(normalized) {
		return &bashBackgroundDecision{notifyOnExit: true, reason: "follow-mode log command", title: "follow: " + compactText(normalized, 72)}
	}
	if delay, ok := sleepSeconds(normalized); ok && delay >= 5 && looksLikeSessionMonitor(normalized) {
		return &bashBackgroundDecision{notifyOnExit: true, reason: "delayed session/tmux monitoring command", title: "monitor: " + compactText(normalized, 72)}
	}
	hasShellLoop := reLoopKeyword.MatchString(normalized) && reDo.MatchString(normalized) && reDone.MatchString(normalized)
	hasSleep := reSleepAny.MatchString(normalized)
	if hasShellLoop && hasSleep {
		iterations, finite := loopIterationCount(normalized)
		monitor := looksLikeSessionMonitor(normalized)
		longFinite := finite && iterations >= 30
		openEnded := reWhileForever.MatchString(normalized) || reUntil.MatchString(normalized)
		if monitor || longFinite || openEnded {
			reason := "long-running polling loop"
			if monitor {
				reason = "session/tmux monitoring loop"
			}
			return &bashBackgroundDecision{notifyOnExit: true, reason: reason, title: "monitor: " + compactText(normalized, 72)}
		}
	}
	return nil
}
