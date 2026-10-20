package pi_goal_x

import (
	"strconv"
	"strings"
)

// ported from goal-status.ts: the /goal-status text builder. Renders the SAME shared dashboard model as the
// above-editor widget; pure (no ctx), so the exact text is unit-testable.

// goalStatusWidth is the fixed rendering width for the notify text (medium layout mode).
const goalStatusWidth = 78

// plainTheme is the identity theme: the box text is emitted as plain text for ctx.ui.notify.
type plainThemeT struct{}

func (plainThemeT) fg(_ string, value string) string { return value }
func (plainThemeT) bold(value string) string         { return value }

var plainTheme goalTheme = plainThemeT{}

type goalStatusTextOptions struct {
	maxAutonomousRuns *float64
	goal              *jsObject
	focused           bool
	otherOpenGoals    int
	ledgerEvents      []*jsObject
	ledgerMalformed   int
	verbose           bool
	health            bool
	// activeFilePresent is supplied by the command layer only for the explicit health check (nil = unknown).
	activeFilePresent *bool
	// checkpointHealth: persisted-checkpoint report for this session (health view only).
	checkpointHealth      *checkpointHealth
	checkpointSessionFile *string
	settingsReport        []string
	// width nil = goalStatusWidth.
	width *int
}

// buildGoalStatusText builds the /goal-status text (standard, verbose or health).
func buildGoalStatusText(options goalStatusTextOptions) string {
	width := goalStatusWidth
	if options.width != nil {
		width = *options.width
	}
	if options.health {
		return buildHealthStatus(options, width)
	}
	if options.goal == nil {
		if options.otherOpenGoals > 0 {
			return strings.Join(renderUnfocusedDashboard(options.otherOpenGoals, plainTheme, width), "\n")
		}
		return "No goal is set. Use /goal <objective> or /sisyphus <objective> to start immediately."
	}
	events := options.ledgerEvents
	model := deriveGoalDashboardModel(options.goal, goalDashboardModelOptions{
		focused: options.focused, maxAutonomousRuns: options.maxAutonomousRuns, otherOpenGoals: options.otherOpenGoals, ledgerEvents: events,
	})
	if model == nil {
		return "No goal is set."
	}
	if options.verbose {
		return buildVerboseStatus(options.goal, model, events, options.settingsReport)
	}

	hint := "Run /goal-status verbose for full detail"
	parts := []string{strings.Join(renderCompactDashboard(model, plainTheme, width, goalCompactOptions{footerHint: &hint}), "\n")}
	if model.currentTask != nil {
		parts = append(parts, strings.Join(renderCurrentTaskBlock(model, plainTheme, width), "\n"))
	}
	if len(model.recentActivity) > 0 {
		parts = append(parts, strings.Join(renderActivityBlock(model.recentActivity, plainTheme, width), "\n"))
	}
	if audit := latestAuditorResultForGoal(events, model.goalId); audit != nil {
		parts = append(parts, lastAuditBlock(audit, width))
	}
	return strings.Join(parts, "\n\n")
}

type healthCheck struct{ label, value, severity string }

func flattenHealthTasks(tasks []any) []*jsObject {
	result := []*jsObject{}
	for _, raw := range tasks {
		t, _ := raw.(*jsObject)
		if t == nil {
			continue
		}
		result = append(result, t)
		result = append(result, flattenHealthTasks(dashSubtasks(t))...)
	}
	return result
}

func statusLabelForHealth(goal *jsObject) string {
	if gstr(goal, "status") == "budget_limited" {
		return "budget limited"
	}
	return gstr(goal, "status")
}

func formatCompactTokensForHealth(value float64) string {
	switch {
	case value >= 1_000_000:
		return trimZero(jsToFixed(value/1_000_000, 1)) + "M"
	case value >= 1_000:
		return trimZero(jsToFixed(value/1_000, 1)) + "K"
	}
	return formatCompactTokensRaw(value)
}

func formatCompactTokensRaw(value float64) string {
	v := value
	if v < 0 {
		v = 0
	}
	return jsNumber(float64(int64(v)))
}

// buildHealthStatus renders a concise read-only integrity report: storage/runtime coherence only, never a completion verdict.
func buildHealthStatus(options goalStatusTextOptions, width int) string {
	goal := options.goal
	if goal == nil {
		focus := "OK — no goal is focused and there are no open goals."
		head := "OK"
		if options.otherOpenGoals > 0 {
			plural := "s"
			if options.otherOpenGoals == 1 {
				plural = ""
			}
			focus = "WARN — no goal is focused; " + strconv.Itoa(options.otherOpenGoals) + " open goal" + plural + " remain."
			head = "WARN"
		}
		return strings.Join([]string{"Goal health: " + head, "Focus: " + focus, "Run /goal-status for the normal dashboard."}, "\n")
	}

	checks := []healthCheck{}
	focusValue, focusSev := "not focused", "warn"
	if options.focused {
		focusValue, focusSev = "focused", "ok"
	}
	checks = append(checks, healthCheck{"Focus", focusValue, focusSev})
	auto := " · auto-continue off"
	if gflag(goal, "autoContinue") {
		auto = " · auto-continue on"
	}
	lifeSev := "error"
	if inList(gstr(goal, "status"), "active", "paused", "blocked", "budget_limited", "complete") {
		lifeSev = "ok"
	}
	checks = append(checks, healthCheck{"Lifecycle", statusLabelForHealth(goal) + auto, lifeSev})

	if gstr(goal, "status") != "complete" {
		fileSeverity := "warn"
		if options.activeFilePresent != nil {
			if *options.activeFilePresent {
				fileSeverity = "ok"
			} else {
				fileSeverity = "error"
			}
		}
		value := "missing active path"
		if ap := gstr(goal, "activePath"); ap != "" {
			value = ap
			if options.activeFilePresent != nil && !*options.activeFilePresent {
				value = "missing · " + ap
			}
		}
		checks = append(checks, healthCheck{"Goal file", value, fileSeverity})
	}

	malformed := options.ledgerMalformed
	ledgerValue, ledgerSev := "valid", "ok"
	if malformed > 0 {
		entries := "ies"
		if malformed == 1 {
			entries = "y"
		}
		ledgerValue, ledgerSev = strconv.Itoa(malformed)+" malformed entr"+entries, "warn"
	}
	checks = append(checks, healthCheck{"Ledger", ledgerValue, ledgerSev})

	if tl := goal.obj("taskList"); tl != nil {
		tasks := flattenHealthTasks(dashTaskList(goal))
		completed, contractedPending := 0, 0
		for _, t := range tasks {
			st := gstr(t, "status")
			if st == "complete" || st == "skipped" {
				completed++
			}
			if st == "pending" && jsTrim(gstr(t, "verificationContract")) != "" {
				contractedPending++
			}
		}
		pending := len(tasks) - completed
		value := strconv.Itoa(completed) + "/" + strconv.Itoa(len(tasks)) + " terminal · " + strconv.Itoa(pending) + " pending"
		if contractedPending > 0 {
			value += " · " + strconv.Itoa(contractedPending) + " contracted"
		}
		sev := "ok"
		if gflag(tl, "blockCompletion") && pending > 0 {
			sev = "warn"
		}
		checks = append(checks, healthCheck{"Tasks", value, sev})
	}

	if budget, ok := goal.num("tokenBudget"); ok && budget > 0 {
		used := dashUsageNum(goal, "tokensUsed")
		percentage := jsRound(float64(used / budget * 100))
		shown := percentage
		if shown < 0 {
			shown = 0
		}
		sev := "ok"
		if gstr(goal, "status") == "budget_limited" || percentage >= 100 || percentage >= 90 {
			sev = "warn"
		}
		checks = append(checks, healthCheck{"Budget", formatCompactTokensForHealth(used) + " / " + formatCompactTokensForHealth(budget) + " (" + jsNumber(shown) + "%)", sev})
	}

	overall := "OK"
	for _, c := range checks {
		if c.severity == "error" {
			overall = "ERROR"
			break
		}
	}
	if overall == "OK" {
		for _, c := range checks {
			if c.severity == "warn" {
				overall = "WARN"
				break
			}
		}
	}
	lines := []string{"Goal health: " + overall, "Goal: " + truncateText(gstr(goal, "objective"), dashMax(20, width-12))}
	for _, c := range checks {
		label := "OK"
		if c.severity == "error" {
			label = "ERROR"
		} else if c.severity == "warn" {
			label = "WARN"
		}
		lines = append(lines, label+" "+c.label+": "+c.value)
	}
	if options.checkpointHealth != nil && options.checkpointHealth.total > 0 {
		lines = append(lines, "")
		lines = append(lines, formatCheckpointHealthReport(options.checkpointHealth, options.checkpointSessionFile))
	}
	lines = append(lines, "", "This is a storage/runtime health check, not a completion verdict.")
	return strings.Join(lines, "\n")
}

func lastAuditBlock(audit *jsObject, width int) string {
	verdictLabel := "ERROR"
	switch gstr(audit, "verdict") {
	case "approved":
		verdictLabel = "APPROVED"
	case "disapproved":
		verdictLabel = "CHANGES REQUIRED"
	}
	line := "Last audit: " + verdictLabel + " (" + utf16Prefix(gstr(audit, "at"), 10) + ") — " + truncateText(wsRun.ReplaceAllString(gstr(audit, "report"), " "), 90)
	inner := dashMax(4, width-2)
	head := "─ Audit result ─ " + verdictLabel + " "
	body := truncateText(line, inner-2)
	return strings.Join([]string{
		"╭" + head + strings.Repeat("─", dashMax(1, inner-utf16Len(head)-1)) + "╮",
		"│ " + body + strings.Repeat(" ", dashMax(0, inner-2-utf16Len(body))) + "│",
		"╰" + strings.Repeat("─", inner) + "╯",
	}, "\n")
}

func buildVerboseStatus(goal *jsObject, model *goalDashboardModel, events []*jsObject, settingsReport []string) string {
	lines := []string{}
	lines = append(lines, "Goal id: "+gstr(goal, "id"))
	lines = append(lines, model.scheduling...)
	revision, _ := goal.num("revision")
	lines = append(lines, "Revision: "+jsNumber(revision))
	focus := " (not focused)"
	if model.focused {
		focus = " (focused)"
	}
	lines = append(lines, "Status: "+model.status.label+focus)
	mode, auto := "goal", "off"
	if gflag(goal, "sisyphus") {
		mode = "sisyphus"
	}
	if gflag(goal, "autoContinue") {
		auto = "on"
	}
	lines = append(lines, "Mode: "+mode+" · auto-continue: "+auto)
	lines = append(lines, "Usage: "+formatDashboardDuration(model.usage.activeSeconds)+" · "+formatCompactTokens(model.usage.tokens)+" tokens")
	if b := model.budget; b != nil {
		lines = append(lines, "Budget: "+formatCompactTokens(b.used)+" / "+formatCompactTokens(b.total)+" · "+strconv.Itoa(b.percentage)+"% used · "+formatCompactTokens(b.remaining)+" remaining")
	}
	if r := gstr(goal, "pauseReason"); r != "" {
		lines = append(lines, "Pause/blocker: "+r)
	}
	if a := gstr(goal, "pauseSuggestedAction"); a != "" {
		lines = append(lines, "Suggested action: "+a)
	}
	if p := gstr(goal, "activePath"); p != "" {
		lines = append(lines, "File: "+p)
	}
	if p := gstr(goal, "archivedPath"); p != "" {
		lines = append(lines, "Archive: "+p)
	}

	lines = append(lines, "", "Objective:", jsTrim(gstr(goal, "objective")))
	if vc := gstr(goal, "verificationContract"); vc != "" {
		lines = append(lines, "", "Verification contract: "+jsTrim(vc))
	}
	if goal.obj("taskList") != nil {
		tasks := dashTaskList(goal)
		lines = append(lines, "", "Tasks ("+strconv.Itoa(len(tasks))+" top-level, "+strconv.Itoa(dashCountDone(tasks))+" done):")
		lines = append(lines, renderTaskTreeVerbose(tasks, 0, gstr(goal, "currentTaskId"))...)
	}
	if ct := model.currentTask; ct != nil {
		inferred := ""
		if ct.inferred {
			inferred = " (inferred)"
		}
		lines = append(lines, "", "Current task: "+ct.id+" · "+ct.title+inferred)
		if ct.totalSubtasks > 0 {
			lines = append(lines, "  subtasks: "+strconv.Itoa(ct.completedSubtasks)+"/"+strconv.Itoa(ct.totalSubtasks)+" ("+strconv.Itoa(ct.subtaskPercentage)+"%)")
		}
		if ct.verificationContract != "" {
			lines = append(lines, "  contract: "+ct.verificationContract)
		}
		if ct.evidence != "" {
			lines = append(lines, "  evidence: "+ct.evidence)
		}
	}

	if recent := latestEventsForGoal(events, gstr(goal, "id"), 12); len(recent) > 0 {
		lines = append(lines, "", "Recent ledger:")
		for _, e := range recent {
			lines = append(lines, "  "+utf16Prefix(gstr(e, "at"), 19)+" "+gstr(e, "type"))
		}
	}
	if audit := latestAuditorResultForGoal(events, gstr(goal, "id")); audit != nil {
		lines = append(lines, "", "Last audit ("+utf16Prefix(gstr(audit, "at"), 10)+"): "+gstr(audit, "verdict"))
		lines = append(lines, truncateText(wsRun.ReplaceAllString(gstr(audit, "report"), " "), 400))
	}
	if len(settingsReport) > 0 {
		lines = append(lines, "", "Effective settings:")
		for _, l := range settingsReport {
			lines = append(lines, "  "+l)
		}
	}
	return strings.Join(lines, "\n")
}

func renderTaskTreeVerbose(tasks []any, depth int, currentTaskID string) []string {
	lines := []string{}
	indent := strings.Repeat("  ", depth)
	for _, raw := range tasks {
		t, _ := raw.(*jsObject)
		if t == nil {
			continue
		}
		status := gstr(t, "status")
		marker := "·"
		switch {
		case gstr(t, "id") == currentTaskID:
			marker = "▸"
		case status == "complete":
			marker = "✓"
		case status == "skipped":
			marker = "~"
		}
		contract, evidence := "", ""
		if c := gstr(t, "verificationContract"); c != "" {
			contract = " — contract: " + c
		}
		if e := gstr(t, "evidence"); status == "complete" && e != "" {
			evidence = " — evidence: " + e
		}
		lines = append(lines, indent+marker+" "+gstr(t, "id")+"  "+gstr(t, "title")+contract+evidence)
		if subs := dashSubtasks(t); len(subs) > 0 {
			lines = append(lines, renderTaskTreeVerbose(subs, depth+1, currentTaskID)...)
		}
	}
	return lines
}
