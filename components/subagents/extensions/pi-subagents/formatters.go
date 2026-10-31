package pi_subagents

import (
	"os"
	"strings"
)

// The text helpers the original shares across its status surfaces
// (src/shared/formatters.ts, src/shared/status-format.ts), ported for the run
// statuses this extension renders.

// formatDuration is the original's formatDuration.
func formatDuration(ms int64) string {
	switch {
	case ms < 1000:
		return formatInt(ms) + "ms"
	case ms < 60000:
		return jsToFixed(float64(ms)/1000, 1) + "s"
	}
	return formatInt(ms/60000) + "m" + formatInt((ms%60000)/1000) + "s"
}

// formatTokens is the original's formatTokens (JS toFixed semantics through jsToFixed).
func formatTokens(n int64) string {
	switch {
	case n < 1000:
		return formatInt(n)
	case n < 10000:
		return jsToFixed(float64(n)/1000, 1) + "k"
	case n < 999500:
		return formatInt(int64(jsRound(float64(n)/1000))) + "k"
	}
	return jsToFixed(float64(n)/1000000, 1) + "M"
}

// shortenPath is the original's shortenPath: the home directory becomes `~`.
func shortenPath(p string) string {
	home := os.Getenv("HOME")
	if home != "" && strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}

// formatActivityAge is the original's (status-format.ts).
func formatActivityAge(ms int64) string {
	if ms < 1000 {
		return "now"
	}
	if ms < 60000 {
		return formatInt(ms/1000) + "s"
	}
	return formatInt(ms/60000) + "m"
}

// formatActivityLabel is the original's formatActivityLabel.
func formatActivityLabel(lastActivityAt *int64, now int64) string {
	if lastActivityAt == nil {
		return ""
	}
	age := formatActivityAge(maxInt64(0, now-*lastActivityAt))
	if age == "now" {
		return "active now"
	}
	return "active " + age + " ago"
}

// formatModelThinking is the original's formatModelThinking: the model without
// its provider prefix, plus a thinking suffix or level ("model · thinking high").
func formatModelThinking(model, thinking string) string {
	displayModel, suffix := splitThinkingSuffix(model)
	displayThinking := ""
	if suffix != "" {
		displayThinking = suffix[1:]
	} else if knownThinkingLevel(thinking) {
		displayThinking = strings.TrimSpace(thinking)
	}
	if displayModel != "" {
		if i := strings.LastIndex(displayModel, "/"); i != -1 {
			displayModel = displayModel[i+1:]
		}
	}
	parts := []string{}
	if displayModel != "" {
		parts = append(parts, displayModel)
	}
	if displayThinking != "" {
		parts = append(parts, "thinking "+displayThinking)
	}
	return strings.Join(parts, " · ")
}

// splitThinkingSuffix splits a known `:level` thinking suffix from a model id.
func splitThinkingSuffix(model string) (base, suffix string) {
	i := strings.LastIndex(model, ":")
	if i >= 0 && thinkingLevels[model[i+1:]] {
		return model[:i], model[i:]
	}
	return model, ""
}

// knownThinkingLevel reports whether level is one of the original's THINKING_LEVELS.
func knownThinkingLevel(level string) bool {
	return thinkingLevels[strings.TrimSpace(level)]
}

// stepStatusCompleted is the original's isCompletedStepStatus.
func stepStatusCompleted(status string) bool { return status == "complete" || status == "completed" }

// aggregateStepStatus is the original's aggregateStepStatus.
func aggregateStepStatus(statuses []string) string {
	running, failed, stopped, paused, complete := 0, 0, 0, 0, 0
	for _, s := range statuses {
		switch s {
		case "running":
			running++
		case "failed":
			failed++
		case "stopped":
			stopped++
		case "paused":
			paused++
		}
		if stepStatusCompleted(s) {
			complete++
		}
	}
	switch {
	case running > 0:
		return "running"
	case failed > 0:
		return "failed"
	case stopped > 0:
		return "stopped"
	case paused > 0:
		return "paused"
	case len(statuses) > 0 && complete == len(statuses):
		return "complete"
	}
	return "pending"
}

// formatParallelOutcome is the original's formatParallelOutcome.
func formatParallelOutcome(statuses []string, total int, showRunning bool) string {
	running, done, failed, stopped, paused := 0, 0, 0, 0, 0
	for _, s := range statuses {
		switch s {
		case "running":
			running++
		case "failed":
			failed++
		case "stopped":
			stopped++
		case "paused":
			paused++
		}
		if stepStatusCompleted(s) {
			done++
		}
	}
	parts := []string{}
	if showRunning && running > 0 {
		label := "1 agent running"
		if running != 1 {
			label = formatInt(int64(running)) + " agents running"
		}
		parts = append(parts, label)
	}
	parts = append(parts, formatInt(int64(done))+"/"+formatInt(int64(total))+" done")
	if failed > 0 {
		parts = append(parts, formatInt(int64(failed))+" failed")
	}
	if stopped > 0 {
		parts = append(parts, formatInt(int64(stopped))+" stopped")
	}
	if paused > 0 {
		parts = append(parts, formatInt(int64(paused))+" paused")
	}
	return strings.Join(parts, " · ")
}

func formatInt(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
