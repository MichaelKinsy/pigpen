package pi_goal_x

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ported from goal-completion.ts. The update_goal(complete) execution path: validates the completable state, runs the independent
// auditor (or the disabled/legacy-skip branches), and commits through the single completion transaction. The auditor derives the
// requirements from the objective and any verification contract and inspects actual workspace evidence. An optional
// completion_summary is forwarded as an UNTRUSTED executor claim — never evidence and never an approval bypass.

// goalAuditMessage is a transcript message the completion flow queues (pi.sendMessage({ customType, content, display, details })).
type goalAuditMessage struct {
	customType string
	content    string
	display    bool
	details    *jsObject
}

// completionSessionCtx is the host as the audit message queue sees it (goalSessionContext).
type completionSessionCtx struct{ h host }

func (s completionSessionCtx) sessionKey() any { return s.h.SessionID() }
func (s completionSessionCtx) isIdle() bool    { return s.h.IsIdle() }

func (c *core) enqueueAuditMessage(content string, details *jsObject) {
	c.auditMessages.enqueue(completionSessionCtx{c.h}, goalAuditMessage{customType: goalAuditEntry, content: content, display: true, details: details})
}

func auditPhaseDetails(phase, goalID string, auditor any) *jsObject {
	d := newObject()
	d.set("phase", phase)
	d.set("goalId", goalID)
	d.set("auditor", auditor)
	return d
}

// startAuditAnimation drives the spinner in the auditor widget: every 80 ms the elapsed time moves and the widget redraws.
func (c *core) startAuditAnimation(startedAt int64) {
	c.stopAuditAnimation()
	stopped := false
	var timer *time.Timer
	var tick func()
	arm := func() { timer = c.after(80*time.Millisecond, tick) }
	tick = func() {
		if stopped {
			return
		}
		if c.auditProgress == nil {
			c.stopAuditAnimation()
			return
		}
		c.auditProgress.elapsedMs = float64(clockMs() - startedAt)
		c.invalidateWidget()
		arm()
	}
	arm()
	c.auditAnimationStop = func() {
		stopped = true
		if timer != nil {
			timer.Stop()
		}
	}
}

type completionCommitOpts struct {
	goal               *jsObject
	completionFocus    goalFocusToken
	auditorReport      string
	auditSkippedReason string
	terminate          bool
	trailing           []string
}

// commitGoalCompletion is the single transaction for every successful completion commit — audit-approved, globally disabled, legacy
// per-goal skipped, or user-bypassed via Escape. Deferred archival: sets the goal complete in memory + writes the active file
// WITHOUT archiving; archival happens at turn_end so the agent can recognise the outcome before the goal is archived. ok=false when
// the mutation failed (stale focus, missing file, write failure, or invalid lifecycle state): never a completed report and never a
// termination request.
func commitGoalCompletion(c *core, opts completionCommitOpts) (r goalAgentResult, ok bool) {
	c.accountProgress(0)
	c.auditProgress = nil
	c.invalidateWidget()
	token := opts.completionFocus
	result, err := c.goalService.apply(c.st, goalMutationSpec{
		reconcile:  falsePtr(),
		focusToken: &token,
		mutate: func(*jsObject) *jsObject {
			return goalWith(opts.goal, "status", "complete", "stopReason", "agent", "updatedAt", nowIso())
		},
	})
	if err != nil {
		// The authoritative file write throws on failure; surface it as a typed mutation outcome so the caller can inspect it.
		result = goalMutationFail(err.Error())
	}
	if !result.ok {
		return c.textResult(fmt.Sprintf("Goal completion failed: %s. The goal was not completed.", mutationMessage(result))), false
	}
	if result.goal != nil {
		c.runtime.markTurnStopped(gstr(result.goal, "id"))
	}
	c.updateUI()
	taskSummary := ""
	if tl := c.state().obj("taskList"); tl != nil {
		taskSummary = buildTaskSummary(tl)
	}
	text := buildCompletionReport(completionReportArgs{detailedSummary: detailedSummary(c.state()), auditorReport: opts.auditorReport,
		auditSkippedReason: opts.auditSkippedReason, taskSummary: taskSummary})
	if len(opts.trailing) > 0 {
		text = strings.Join(append([]string{text, ""}, opts.trailing...), "\n")
	}
	res := c.textResult(text)
	res.terminate = opts.terminate
	return res, true
}

func configString(cfg *jsObject, key string) (string, bool) {
	if cfg == nil {
		return "", false
	}
	return cfg.str(key)
}

func runGoalCompletionFlow(c *core, completionSummary string) (goalAgentResult, error) {
	flushError, err := c.goalService.flushForAudit(c.st)
	if err != nil {
		return goalAgentResult{}, err
	}
	if flushError != "" {
		return c.textResult(flushError), nil
	}
	c.reconcileFocusedGoalFromDisk(false)

	// -- Completion --
	running := ""
	if c.runningGoalID != nil {
		running = *c.runningGoalID
	}
	if gate := validateGoalCompletion(c.state(), running); !gate.ok {
		return c.textResult(gate.message), nil
	}
	if c.state() == nil {
		return goalAgentResult{}, fmt.Errorf("Goal disappeared during completion validation.")
	}

	// Task gate: warn if blockCompletion is enabled and tasks remain pending
	if !loadGoalSettings(c.h.Cwd()).disableTasks {
		if tl := c.state().obj("taskList"); tl != nil {
			if warning := taskCompletionBlockWarning(tl); warning != "" {
				return c.textResult(warning), nil
			}
		}
	}

	auditTarget := c.st.mergeGoalPromptFromDisk(c.state())
	goalID := gstr(auditTarget, "id")
	completionFocus := c.focusedOperationToken(goalID)
	// Append ledger: completion requested
	c.goalService.appendEvents(c.st, []*jsObject{ledgerEvent("completion_requested", "goalId", goalID, "at", nowIso())})
	cfg := loadGoalSettingsFileConfig(c.h.Cwd())
	provider, _ := configString(cfg, "provider")
	model, _ := configString(cfg, "model")
	thinking, _ := configString(cfg, "thinkingLevel")
	optional := func(s string, has bool) any {
		if has && s != "" {
			return s
		}
		return undef{}
	}
	auditorLabel := "default"
	if provider != "" || model != "" || thinking != "" {
		or := func(s string) string {
			if s == "" {
				return "default"
			}
			return s
		}
		suffix := ""
		if thinking != "" {
			suffix = ":" + thinking
		}
		auditorLabel = or(provider) + "/" + or(model) + suffix
	}
	skipEvent := func(reason string) *jsObject {
		return ledgerEvent("audit_skipped", "goalId", goalID, "reason", reason, "provider", optional(provider, true), "model", optional(model, true),
			"thinkingLevel", optional(thinking, true), "at", nowIso())
	}

	// Legacy persisted skipAuditor:true records remain readable and honored for compatibility; no model tool or task dialog creates
	// new per-goal bypass state.
	if gflag(auditTarget, "skipAuditor") {
		c.enqueueAuditMessage("Goal completed — per-goal auditor disabled.", auditPhaseDetails("skipped", goalID, auditorLabel))
		c.goalService.appendEvents(c.st, []*jsObject{skipEvent("disabled")})
		r, _ := commitGoalCompletion(c, completionCommitOpts{goal: auditTarget, completionFocus: completionFocus, auditSkippedReason: "per-goal auditor disabled", terminate: true})
		return r, nil
	}

	// settings.disabled is an explicit user-owned setting: completion skips the auditor, records audit_skipped, and proceeds through
	// the normal deferred-completion path. No model-side bypass flag is required.
	if cfg != nil && cfg.vals["disabled"] == true {
		c.enqueueAuditMessage("Goal completed — auditor disabled in settings.", auditPhaseDetails("skipped", goalID, auditorLabel))
		c.goalService.appendEvents(c.st, []*jsObject{skipEvent("disabled")})
		r, _ := commitGoalCompletion(c, completionCommitOpts{goal: auditTarget, completionFocus: completionFocus, auditSkippedReason: "auditor disabled in settings", terminate: true})
		return r, nil
	}

	// Auditor is enabled — run the normal audit flow
	c.enqueueAuditMessage(strings.Join([]string{"Auditor: I am starting the independent completion audit.", "Goal id: " + goalID, "Auditor model: " + auditorLabel}, "\n"),
		auditPhaseDetails("started", goalID, auditorLabel))
	if !c.isFocusedOperationCurrent(completionFocus) {
		return c.focusedOperationCancelledResult("Goal completion", completionFocus), nil
	}
	// Append ledger: audit started
	c.goalService.appendEvents(c.st, []*jsObject{ledgerEvent("audit_started", "goalId", goalID, "provider", optional(provider, true), "model", optional(model, true),
		"thinkingLevel", optional(thinking, true), "at", nowIso())})
	// Set up auditor progress display (before createAgentSession)
	auditStartedAt := clockMs()
	c.auditProgress = &auditorWidgetProgress{recentOutput: []string{}, phase: "running", elapsedMs: 0, auditorLabel: auditorLabel}
	// Start animation timer for the spinner in the auditor widget
	c.startAuditAnimation(auditStartedAt)

	// Create a dedicated abort controller for the audit so it can be interrupted via Escape
	if c.auditAbortController != nil {
		c.auditAbortController() // Clean up any stale controller
	}
	auditCtx, cancelAudit := context.WithCancel(context.Background())
	c.auditAbortController = cancelAudit

	// P1-6: warm start — seed the auditor with the parent-rendered ledger tail (recent lifecycle + task evidence) so it does not
	// re-derive session facts.
	ledger := goalRuntimeEvents(c.st, goalID)
	warmTail := latestEventsForGoal(ledger, goalID, 8)
	previousAudit := latestAuditorResultForGoal(ledger, goalID)
	warmContext := ""
	if len(warmTail) > 0 {
		lines := make([]string, len(warmTail))
		for i, e := range warmTail {
			line := "- " + gstr(e, "at") + " " + gstr(e, "type")
			if e.has("taskId") {
				line += " (task " + gstr(e, "taskId") + ")"
			}
			if ev := gstr(e, "evidence"); e.has("evidence") && ev != "" {
				line += " evidence: " + ev
			}
			lines[i] = line
		}
		warmContext = "Recent goal events (from the shared ledger):\n" + strings.Join(lines, "\n")
	}
	if previousAudit != nil && gstr(previousAudit, "verdict") == "disapproved" {
		warmContext = warmContext + "\nPrevious rejection (verify whether resolved): " + utf16Prefix(gstr(previousAudit, "report"), 600)
	}

	settings := loadGoalSettings(c.h.Cwd())
	args := runGoalCompletionAuditorArgs{
		ctx:               hostAuditorCtx{c.h},
		goal:              auditTarget,
		detailedSummary:   detailedSummary(auditTarget),
		completionSummary: jsTrim(completionSummary),
		settings:          &settings,
		warmContext:       warmContext,
		signal:            auditCtx,
		createSession:     c.deps.createSession,
		onProgress: func(progress auditorProgress) {
			c.locked(func() {
				c.auditProgress = &auditorWidgetProgress{currentTool: progress.currentTool, currentToolArgs: progress.currentToolArgs,
					currentToolStartedAt: progress.currentToolStartedAt, recentOutput: progress.recentOutput, phase: progress.phase,
					elapsedMs: float64(clockMs() - auditStartedAt), label: progress.label, percentage: progress.percentage}
				c.invalidateWidget()
			})
		},
	}
	runAuditor := c.deps.runCompletionAuditor
	if runAuditor == nil {
		runAuditor = runGoalCompletionAuditor
	}
	var auditor goalAuditorResult
	c.awaiting(func() { auditor = runAuditor(args) })
	// Clear abort controller — audit finished on its own
	if c.auditAbortController != nil && auditCtx.Err() == nil {
		c.auditAbortController = nil
	}
	cancelAudit()
	// Clear auditor progress display
	c.stopAuditAnimation()
	if !c.isFocusedOperationCurrent(completionFocus) {
		c.auditProgress = nil
		c.invalidateWidget()
		return c.focusedOperationCancelledResult("Goal completion", completionFocus), nil
	}

	// If the audit was aborted by the user (Esc), show a TUI dialog letting the user choose: mark complete without audit, or continue
	// working. The low-level abort callback (core.abortAudit) only records transient runtime state; exactly one canonical ledger event
	// is appended here after the user's choice (follow-up Stage 2).
	if auditor.err == "Auditor aborted." {
		c.auditProgress = nil
		c.invalidateWidget()
		c.updateUI()

		c.enterGoalModal()
		var userChoice escapeDialogResult
		c.awaiting(func() { userChoice = showEscapeDialog(hostDialogCtx{c.h}, gstr(auditTarget, "objective")) })
		c.exitGoalModal()
		// Consume the transient abort state recorded by the low-level callback.
		c.auditAborted = false
		if !c.isFocusedOperationCurrent(completionFocus) {
			return c.focusedOperationCancelledResult("Goal completion", completionFocus), nil
		}

		if userChoice == escapeCompleteWithoutAudit {
			// ── Mark complete without audit ──
			c.enqueueAuditMessage("Goal completed — user bypassed audit via Escape.", auditPhaseDetails("skipped", goalID, auditorLabel))
			// The one canonical ledger outcome for this choice.
			c.goalService.appendEvents(c.st, []*jsObject{skipEvent("user_aborted")})
			// Deferred archival: set goal complete in memory + write the active file WITHOUT archiving; archival happens at turn_end so
			// the agent can recognise the skipped audit before the goal is archived.
			r, _ := commitGoalCompletion(c, completionCommitOpts{goal: auditTarget, completionFocus: completionFocus,
				auditSkippedReason: "auditor bypassed (user pressed Escape during audit)", terminate: false,
				trailing: []string{"The goal is complete. Provide a final summary of what was accomplished."}})
			return r, nil
		}
		// ── Continue working ── The goal stays active: no pause, no stop marker, no skip event.
		c.invalidateWidget()
		c.updateUI()
		return goalAgentResult{text: "Audit aborted — the goal remains active and work continues.", details: goalDetails(auditTarget, "")}, nil
	}

	// Show final audit output briefly before clearing
	if c.auditProgress != nil && auditor.output != "" {
		lines := strings.Split(auditor.output, "\n")
		if len(lines) > 8 {
			lines = lines[:8]
		}
		p := *c.auditProgress
		p.phase = "done"
		p.recentOutput = lines
		p.elapsedMs = float64(clockMs() - auditStartedAt)
		c.auditProgress = &p
		c.invalidateWidget()
	}
	// Append ledger: audit result
	verdict := "disapproved"
	if auditor.approved {
		verdict = "approved"
	} else if auditor.err != "" {
		verdict = "error"
	}
	report := auditor.output
	if report == "" {
		report = "Auditor produced no output."
	}
	c.goalService.appendEvents(c.st, []*jsObject{ledgerEvent("audit_result", "goalId", goalID, "verdict", verdict, "report", report, "at", nowIso())})
	modelLine := func() string {
		if auditor.model == "" {
			return ""
		}
		if auditor.thinkingLevel != "" {
			return "Auditor model: " + auditor.model + ":" + auditor.thinkingLevel
		}
		return "Auditor model: " + auditor.model
	}
	var auditorModel any = undef{}
	if auditor.model != "" {
		auditorModel = auditor.model
	}
	if !auditor.approved {
		// Clear auditor progress to restore normal widget state, then show the §15.4 result card briefly so the required next work is
		// visible before the normal dashboard returns (the goal stays open).
		c.auditProgress = nil
		kind := "disapproved"
		if auditor.err != "" {
			kind = "error"
		}
		c.setAuditResult(kind, report)
		c.invalidateWidget()
		lines := []string{"Goal audit rejected.", "", "Goal completion rejected by independent auditor."}
		if m := modelLine(); m != "" {
			lines = append(lines, m)
		}
		if auditor.err != "" {
			lines = append(lines, "Auditor error: "+auditor.err)
		}
		body := auditor.output
		if body == "" {
			body = "Auditor produced no approval marker."
		}
		lines = append(lines, "", body)
		rejection := strings.Join(lines, "\n")
		c.enqueueAuditMessage(rejection, auditPhaseDetails("rejected", goalID, auditorModel))
		return c.textResult(rejection), nil
	}
	lines := []string{"Auditor: I approve this completion claim."}
	if m := modelLine(); m != "" {
		lines = append(lines, m)
	}
	body := auditor.output
	if body == "" {
		body = "Auditor approved completion."
	}
	lines = append(lines, "", body)
	c.enqueueAuditMessage(strings.Join(lines, "\n"), auditPhaseDetails("approved", goalID, auditorModel))
	// §15.4: the approval card shows during the deferred archival window.
	c.setAuditResult("approved", body)
	// Deferred archival happens inside commitGoalCompletion; archival occurs at turn_end so the agent can see the auditor approval
	// before the goal is archived.
	r, _ := commitGoalCompletion(c, completionCommitOpts{goal: auditTarget, completionFocus: completionFocus, auditorReport: auditor.output, terminate: true})
	return r, nil
}
