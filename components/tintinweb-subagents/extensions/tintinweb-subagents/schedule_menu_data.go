package tintinweb_subagents

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf16"
)

// The data layer of `/agents → Scheduled jobs` (list the jobs, one line per job, details for the cancel confirm). The
// menu itself (the picker and the confirm dialog) belongs to the UI slice; it uses these, and the scheduler's isActive,
// list and removeJob. upstream: src/ui/schedule-menu.ts relTime, statusIcon, formatJob, formatDetails, and the
// "Scheduled jobs (N)" entry of showAgentsMenu in src/index.ts.

// scheduleMenuEntry is the `/agents` menu entry of the scheduled jobs, present when the scheduler is active.
func (a *app) scheduleMenuEntry() (string, bool) {
	if !a.scheduler().isActive() {
		return "", false
	}
	return fmt.Sprintf("Scheduled jobs (%d)", len(a.scheduler().list())), true
}

// scheduleListing is the scheduled jobs as lines of the text `/agents` (which is a listing, not the original's menu):
// the menu entry, one row per job, and the refusal for what only the interactive menu does, cancelling a job.
func (a *app) scheduleListing(nowMs int64) []string {
	entry, ok := a.scheduleMenuEntry()
	if !ok {
		return nil
	}
	jobs := a.scheduler().list()
	lines := []string{"", entry + ":"}
	if len(jobs) == 0 {
		return append(lines, "  (none)")
	}
	for _, j := range jobs {
		lines = append(lines, "  "+a.scheduleFormatJob(j, nowMs))
	}
	return append(lines, "  "+refusalText("cancelling a scheduled job (the /agents Scheduled jobs menu)"))
}

// scheduleRelTime formats an ISO timestamp as relative time ("in 4h", "2d ago", "—"). nowMs is Date.now().
func scheduleRelTime(iso string, nowMs int64) string {
	if iso == "" {
		return "—"
	}
	t, ok := parseJSDate(iso)
	if !ok {
		return "—"
	}
	diff := t - float64(nowMs)
	abs := math.Abs(diff)
	future := diff > 0
	pick := func(futureText, pastText string) string {
		if future {
			return futureText
		}
		return pastText
	}
	if abs < 60_000 {
		return pick("in <1m", "<1m ago")
	}
	round := func(v float64) int { return int(math.Floor(v + 0.5)) } // Math.round: halves go up
	if m := round(abs / 60_000); m < 60 {
		return pick(fmt.Sprintf("in %dm", m), fmt.Sprintf("%dm ago", m))
	}
	if h := round(abs / 3_600_000); h < 24 {
		return pick(fmt.Sprintf("in %dh", h), fmt.Sprintf("%dh ago", h))
	}
	d := round(abs / 86_400_000)
	return pick(fmt.Sprintf("in %dd", d), fmt.Sprintf("%dd ago", d))
}

// scheduleStatusIcon is the one-character status of a job.
func scheduleStatusIcon(j scheduledSubagent) string {
	switch {
	case !j.Enabled:
		return "✗"
	case j.LastStatus == "error":
		return "!"
	case j.LastStatus == "running":
		return "⋯"
	}
	return "✓"
}

// utf16Fit is s.padEnd(n).slice(0, n) in UTF-16 code units, as JavaScript measures strings.
func utf16Fit(s string, n int) string {
	units := utf16.Encode([]rune(s))
	for len(units) < n {
		units = append(units, ' ')
	}
	return string(utf16.Decode(units[:n]))
}

// scheduleFormatJob is the selectable row of a job: status, name, schedule, agent type, next and last run, run count.
func (a *app) scheduleFormatJob(j scheduledSubagent, nowMs int64) string {
	next, _ := a.scheduler().getNextRun(j.ID)
	return strings.Join([]string{
		scheduleStatusIcon(j), utf16Fit(j.Name, 18), utf16Fit(j.Schedule, 14), "[" + j.SubagentType + "]",
		"next " + scheduleRelTime(next, nowMs), "last " + scheduleRelTime(j.LastRun, nowMs), "runs " + strconv.Itoa(j.RunCount),
	}, "  ")
}

// scheduleFormatDetails is the multi-line details block of the cancel confirm.
func (a *app) scheduleFormatDetails(j scheduledSubagent) string {
	next, ok := a.scheduler().getNextRun(j.ID)
	if !ok {
		next = "—"
	}
	prompt := utf16.Encode([]rune(j.Prompt))
	shown := j.Prompt
	if len(prompt) > 200 {
		shown = string(utf16.Decode(prompt[:200])) + "…"
	}
	lastRun, lastStatus := "—", "—"
	if j.LastRun != "" {
		lastRun = j.LastRun
	}
	if j.LastStatus != "" {
		lastStatus = j.LastStatus
	}
	return strings.Join([]string{
		"name:      " + j.Name,
		"schedule:  " + j.Schedule + " (" + j.ScheduleType + ")",
		"agent:     " + j.SubagentType,
		"prompt:    " + shown,
		"created:   " + j.CreatedAt,
		"last run:  " + lastRun + " (" + lastStatus + ")",
		"next run:  " + next,
		"runs:      " + strconv.Itoa(j.RunCount),
	}, "\n")
}
