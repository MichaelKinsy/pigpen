package pi_background_tasks

import (
	"fmt"
	"math"
	"math/big"
	"strings"
)

// Text formatting of the task runtime. upstream: extensions/format.ts.

// jsTrimEnd is String.prototype.trimEnd.
func jsTrimEnd(s string) string { return strings.TrimRightFunc(s, isJSSpace) }

// jsToInt is ToIntegerOrInfinity for the finite values settings produce: truncation toward zero, NaN as 0.
func jsToInt(f float64) int {
	if math.IsNaN(f) {
		return 0
	}
	t := math.Trunc(f)
	switch {
	case t > math.MaxInt32:
		return math.MaxInt32
	case t < math.MinInt32:
		return math.MinInt32
	}
	return int(t)
}

// jsSliceLast is text.slice(-n) for a settings value n: n>0 the last n units, 0 the whole text (slice(-0) is slice(0)),
// n<0 the text without its first -n units.
func jsSliceLast(text string, n float64) string {
	i := jsToInt(n)
	switch {
	case i > 0:
		return jsTail(text, i)
	case i == 0:
		return text
	}
	return jsSliceFrom(text, -i)
}

// jsToFixed is Number.prototype.toFixed: the decimal expansion of the double itself rounded to d places, ties going to the
// larger value.
func jsToFixed(x float64, d int) string {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return jsNumberString(x)
	}
	neg := x < 0
	if neg {
		x = -x
	}
	exact := new(big.Float).SetPrec(1100).SetFloat64(x).Text('f', 1100)
	intPart, frac, _ := strings.Cut(exact, ".")
	digits := []byte(intPart + frac[:d])
	if len(frac) > d && frac[d] >= '5' {
		for i := len(digits) - 1; ; i-- {
			if i < 0 {
				digits = append([]byte{'1'}, digits...)
				break
			}
			if digits[i] == '9' {
				digits[i] = '0'
				continue
			}
			digits[i]++
			break
		}
	}
	n := len(digits) - d
	out := string(digits[:n])
	if d > 0 {
		out += "." + string(digits[n:])
	}
	if neg {
		out = "-" + out
	}
	return out
}

// tailText is the original's tailText: the whole text, or "[...truncated]\n" and its last maxChars units. truncated
// forces the marker (the text was cut on disk before it got here).
func tailText(text string, maxChars float64, truncated bool) string {
	if !truncated && float64(jsLen(text)) <= maxChars {
		return text
	}
	return "[...truncated]\n" + jsSliceLast(text, maxChars)
}

// logTruncation is BackgroundLogTruncation.
type logTruncation struct {
	Direction      string `json:"direction"`
	FullOutputPath string `json:"fullOutputPath"`
	ShownChars     int    `json:"shownChars"`
	TotalChars     int    `json:"totalChars"`
	Truncated      bool   `json:"truncated"`
}

// truncateForTranscript bounds a transcript-facing field: the head and an ellipsis past maxChars units.
func truncateForTranscript(value string, maxChars int) string {
	if jsLen(value) <= maxChars {
		return value
	}
	return jsHead(value, max(0, maxChars-1)) + "…"
}

func taskLogTruncation(output, logFile, cwd string) *logTruncation {
	maxChars := max(1, jsToInt(math.Floor(settingNumber("logTailMaxChars", defaultLogTailMaxChars, cwd))))
	total := jsLen(output)
	if total <= maxChars {
		return nil
	}
	return &logTruncation{Direction: "tail", FullOutputPath: truncateForTranscript(logFile, wakeManifestFieldMaxChars), ShownChars: maxChars, TotalChars: total, Truncated: true}
}

func formatTaskLog(output, logFile, cwd string) string {
	if output == "" {
		return "(empty)"
	}
	t := taskLogTruncation(output, logFile, cwd)
	if t == nil {
		return output
	}
	return fmt.Sprintf("[...truncated]\n%s\n\n[Background log truncated. Showing last %d of %d character(s). Full log: %s]", jsTail(output, t.ShownChars), t.ShownChars, t.TotalChars, truncateForTranscript(logFile, wakeManifestFieldMaxChars))
}

// trimOutputBuffer keeps the in-memory output within outputBufferMaxChars, moving the announced offset with it.
func trimOutputBuffer(output string, lastAnnounced int) (string, int) {
	maxChars := settingNumber("outputBufferMaxChars", defaultOutputBufferMaxChars, "")
	length := jsLen(output)
	if float64(length) <= maxChars {
		return output, lastAnnounced
	}
	overflow := length - jsToInt(maxChars)
	return jsSliceLast(output, maxChars), max(0, lastAnnounced-overflow)
}

func formatDuration(ms float64) string {
	safe := math.Max(0, ms)
	if safe < 1000 {
		return jsNumberString(safe) + "ms"
	}
	seconds := safe / 1000
	if seconds < 60 {
		digits := 1
		if seconds >= 10 {
			digits = 0
		}
		return jsToFixed(seconds, digits) + "s"
	}
	minutes := math.Floor(seconds / 60)
	remSeconds := math.Floor(math.Mod(seconds, 60))
	if minutes < 60 {
		if remSeconds > 0 {
			return fmt.Sprintf("%dm %ds", int(minutes), int(remSeconds))
		}
		return fmt.Sprintf("%dm", int(minutes))
	}
	hours := math.Floor(minutes / 60)
	remMinutes := int(minutes) % 60
	if remMinutes > 0 {
		return fmt.Sprintf("%dh %dm", int(hours), remMinutes)
	}
	return fmt.Sprintf("%dh", int(hours))
}

func formatRelativeTime(timestamp, now float64) string {
	diff := timestamp - now
	abs := math.Abs(diff)
	suffix := "ago"
	if diff >= 0 {
		suffix = "from now"
	}
	switch {
	case abs < 1000:
		if diff >= 0 {
			return "now"
		}
		return "just now"
	case abs < 60_000:
		return fmt.Sprintf("%ds %s", int(math.Floor(abs/1000)), suffix)
	case abs < 3_600_000:
		return fmt.Sprintf("%dm %s", int(math.Floor(abs/60_000)), suffix)
	case abs < 86_400_000:
		return fmt.Sprintf("%dh %s", int(math.Floor(abs/3_600_000)), suffix)
	}
	return fmt.Sprintf("%dd %s", int(math.Floor(abs/86_400_000)), suffix)
}

func summarizeTaskStatus(status string, exitCode *int, terminationReason string) string {
	base := baseStatusLabel(status, exitCode)
	if terminationReason == "" || terminationReason == termSelfExit {
		return base
	}
	return base + " (" + terminationReason + ")"
}

func baseStatusLabel(status string, exitCode *int) string {
	switch status {
	case statusRunning:
		return "running"
	case statusCompleted:
		if exitCode == nil {
			return "completed (exit 0)"
		}
		return fmt.Sprintf("completed (exit %d)", *exitCode)
	case statusFailed:
		if exitCode == nil {
			return "failed (exit ?)"
		}
		return fmt.Sprintf("failed (exit %d)", *exitCode)
	case statusTimedOut:
		if exitCode == nil {
			return "timed out"
		}
		return fmt.Sprintf("timed out (exit %d)", *exitCode)
	case statusStopped:
		if exitCode == nil {
			return "stopped"
		}
		return fmt.Sprintf("stopped (exit %d)", *exitCode)
	}
	return ""
}

// taskDisplayName is the title, else the command, trimmed.
func taskDisplayName(title, command string) string {
	if t := jsTrim(title); t != "" {
		return t
	}
	return jsTrim(command)
}

func taskDisplayNameForTranscript(title, command string) string {
	return compactText(taskDisplayName(title, command), taskDisplayNameMaxChars)
}

func buildTaskSummaryLine(s *snapshot, now int64) string {
	activityAt := s.UpdatedAt
	if s.LastOutputAt != nil {
		activityAt = *s.LastOutputAt
	}
	return fmt.Sprintf("%s · %s · pid %d · %s · %s", s.ID, summarizeTaskStatus(s.Status, s.ExitCode, s.TerminationReason), s.Pid,
		taskDisplayNameForTranscript(s.Title, s.Command), formatRelativeTime(float64(activityAt), float64(now)))
}

func taskActivityAt(s *snapshot) int64 {
	if s.LastOutputAt != nil {
		return *s.LastOutputAt
	}
	return s.UpdatedAt
}

func taskElapsedMs(s *snapshot, now int64) int64 {
	if s.Status == statusRunning {
		return now - s.StartedAt
	}
	return s.UpdatedAt - s.StartedAt
}

func formatShortcutHint(shortcut string) string { return strings.ToLower(shortcut) }

// lineCount is the number of lines of a log: a final line break does not start another.
func lineCount(text string) int {
	if text == "" {
		return 0
	}
	normalized := text
	if strings.HasSuffix(normalized, "\r\n") {
		normalized = normalized[:len(normalized)-2]
	} else if strings.HasSuffix(normalized, "\n") {
		normalized = normalized[:len(normalized)-1]
	}
	if normalized == "" {
		return 0
	}
	return len(jsSplitLines(normalized))
}

// takeTailLines is the last maxLines lines of text (trailing space trimmed), the count hidden and the total.
func takeTailLines(text string, maxLines float64) (lines []string, hidden, total int) {
	trimmed := jsTrimEnd(text)
	var all []string
	if len(trimmed) > 0 {
		all = jsSplitLines(trimmed)
	}
	limit := max(1, jsToInt(math.Floor(maxLines)))
	start := max(0, len(all)-limit)
	return all[start:], max(0, len(all)-limit), len(all)
}
