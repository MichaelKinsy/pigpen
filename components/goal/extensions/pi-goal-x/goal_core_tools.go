package pi_goal_x

import (
	"errors"
	"fmt"
	"math"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// ported from goal-core-tools.ts: get_goal, create_goal and update_goal.

// conciseTaskPointers is the current + first-pending (excluding current) task pointers for concise get_goal.
func conciseTaskPointers(goal *jsObject) (current, firstPending *jsObject) {
	tl := goal.obj("taskList")
	if tl == nil {
		return nil, nil
	}
	tasks, _ := tl.vals["tasks"].([]any)
	index := taskIndex(tasks)
	cur := gstr(goal, "currentTaskId")
	for _, t := range index.pending {
		if gstr(t, "id") != cur {
			firstPending = t
			break
		}
	}
	if cur != "" {
		current = index.byId[cur]
	}
	return current, firstPending
}

func registerCoreTools(c *core) {
	p := c.pi
	e := p.e
	e.RegisterTool(sdk.ToolDefinition{
		Name:             getGoalToolName,
		Label:            "Get Goal",
		Description:      "Read focused goal state. Default: compact summary. Retrieve full objective/contracts, tasks, or history in 4000-character pages with section and cursor. verbose preserves full legacy output.",
		PromptSnippet:    "Inspect goal state or retrieve omitted requirements.",
		PromptGuidelines: []string{},
		Parameters: schemaObject(schemaMap{
			"section":         schemaEnum("summary", "objective", "tasks", "history"),
			"task_id":         schemaString("With section=tasks, retrieve one task."),
			"cursor":          schemaMap{"type": "string", "maxLength": 256, "description": "Next page; repeat section/task."},
			"verbose":         schemaBool("Full detail mode."),
			"include_history": schemaBool(""),
		}, nil),
		Execute:      p.execute(executeGetGoal),
		RenderCall:   toolRenderCall("get_goal", ""),
		RenderResult: toolRenderResult(),
	})

	e.RegisterTool(sdk.ToolDefinition{
		Name:             createGoalToolName,
		Label:            "Create Goal",
		Description:      "Create and focus a persistent goal only on explicit user request; other open goals remain unchanged.",
		PromptSnippet:    "Create a goal only when explicitly requested.",
		PromptGuidelines: []string{"Never infer persistent goals, Sisyphus mode, or token budgets from an ordinary task. The objective must faithfully preserve all user requirements and ordered steps."},
		Parameters: schemaObject(schemaMap{
			"objective":    schemaString("Full objective; preserve ordered steps and done criteria."),
			"mode":         schemaMap{"type": "string", "enum": []string{"regular", "sisyphus"}, "description": "Default regular; sisyphus only if requested."},
			"token_budget": schemaMap{"type": "integer", "minimum": 1, "description": "Whole-token budget, only if supplied by the user."},
		}, []string{"objective"}),
		ExecutionMode: "sequential",
		Execute:       p.execute(executeCreateGoal),
		RenderCall: func(ctx sdk.Context, args map[string]any, _ sdk.ToolRenderContext, width int) ([]string, error) {
			prefix := "create_goal "
			if args["mode"] == "sisyphus" {
				prefix = "create_goal sisyphus "
			}
			objective, _ := args["objective"].(string)
			return toolRenderCallLines(sdkTheme{ctx.UITheme()}, prefix, objective, args, width), nil
		},
		RenderResult: toolRenderResult(),
	})

	e.RegisterTool(sdk.ToolDefinition{
		Name:             updateGoalToolName,
		Label:            "Update Goal",
		Description:      "Update lifecycle status or optionally save continuation. New waits require strictExecutionContract opt-in. Wait: future ISO deadline; reuse wait_id/deadline without polling on recheck. Completion is audited.",
		PromptSnippet:    "Complete, block, pause, or optionally save a scheduling decision.",
		PromptGuidelines: []string{},
		Parameters: schemaObject(schemaMap{
			"status": schemaMap{"type": "string", "enum": []string{"complete", "blocked", "paused"}, "description": "Exclusive with continuation."},
			"continuation": schemaMap{"anyOf": []any{
				schemaMap{"type": "object", "properties": schemaMap{"kind": schemaMap{"const": "ready", "type": "string"}}, "required": []string{"kind"}, "additionalProperties": false},
				schemaMap{"type": "object", "properties": schemaMap{
					"kind":     schemaMap{"const": "wait", "type": "string"},
					"reason":   schemaMap{"type": "string", "minLength": 1, "maxLength": 2000},
					"deadline": schemaMap{"type": "string"},
					"wait_id":  schemaMap{"type": "string"},
					"polling": schemaMap{"type": "object", "properties": schemaMap{
						"interval_seconds": schemaMap{"type": "integer", "minimum": 1, "maximum": 2147483},
						"max_checks":       schemaMap{"type": "integer", "minimum": 1, "maximum": 9007199254740991},
					}, "required": []string{"interval_seconds", "max_checks"}, "additionalProperties": false},
				}, "required": []string{"kind", "reason", "deadline"}, "additionalProperties": false},
			}},
			"reason":             schemaString("Required when status is paused or blocked: describe the concrete blocker."),
			"attempted_actions":  schemaMap{"type": "array", "items": schemaMap{"type": "string", "maxLength": 240}, "maxItems": 8, "description": "Actions attempted against the blocker."},
			"suggested_action":   schemaString("Optional suggested next step when status is paused."),
			"completion_summary": schemaString("Untrusted completion claim; never evidence."),
		}, nil),
		ExecutionMode: "sequential",
		Execute:       p.execute(executeUpdateGoal),
		RenderCall: func(ctx sdk.Context, args map[string]any, _ sdk.ToolRenderContext, width int) ([]string, error) {
			status, _ := args["status"].(string)
			return toolRenderCallLines(sdkTheme{ctx.UITheme()}, "update_goal ", status, args, width), nil
		},
		RenderResult: toolRenderResult(),
	})
}

// executeGetGoal is get_goal's execute.
func executeGetGoal(c *core, params map[string]any) (goalAgentResult, error) {
	c.reconcileFocusedGoalFromDisk(false)
	if c.state() != nil {
		c.syncGoalPromptFromDisk()
	}
	view := c.goalForDisplay()
	if view == nil {
		view = c.state()
	}
	section, _ := toolParamString(params, "section")
	taskID, hasTaskID := toolParamString(params, "task_id")
	cursor, _ := toolParamString(params, "cursor")
	verbose := promptProfile() == "legacy-v1" || params["verbose"] == true
	includeHistory := params["include_history"] == true || verbose
	otherCount := otherOpenGoalCount(c.goals, c.focused)
	if view == nil {
		var text string
		if n := otherOpenGoalCount(c.goals, nil); n > 0 {
			text = buildUnfocusedOpenGoalsSummary(n) + "\n\nCall create_goal with the objective to create and focus a new goal, or ask the user to run /goal-focus to choose an open goal."
		} else {
			text = "No goal is set in this session. Call create_goal with the objective when the user explicitly asks to start a persistent goal."
		}
		return goalAgentResult{text: text, details: goalDetails(view, "")}, nil
	}
	if section != "" && section != "summary" {
		if section != "objective" && section != "tasks" && section != "history" {
			return goalAgentResult{text: "Unknown goal section.", details: goalDetails(view, "")}, nil
		}
		var events []*jsObject
		if section == "history" {
			events = readGoalLedger(c.st).events
		}
		q := goalDetailQuery{section: section, cursor: cursor}
		if hasTaskID {
			q.taskId = &taskID
		}
		page := goalDetailPage(view, q, events)
		details := goalDetails(view, "")
		if page.ok {
			pg := newObject()
			pg.set("content", page.content)
			if page.hasNext {
				pg.set("nextCursor", page.nextCursor)
			} else {
				pg.set("nextCursor", undef{})
			}
			pg.set("totalChars", float64(page.totalChars))
			details.set("page", pg)
		}
		return goalAgentResult{text: page.text, details: details}, nil
	}
	if cursor != "" || hasTaskID && taskID != "" {
		return goalAgentResult{text: "Use section=objective, tasks, or history for detail retrieval; task_id requires tasks.", details: goalDetails(view, "")}, nil
	}
	settings := loadGoalSettings(c.h.Cwd())
	scheduler := schedulerSummary(view.obj("scheduler"), settings.maxAutonomousRuns, settings.showAutonomousRuns)
	mode := "regular"
	if gflag(view, "sisyphus") {
		mode = "sisyphus"
	}
	if verbose && section == "" {
		lines := []string{fmt.Sprintf("Goal %s: %s, %s", gstr(view, "id"), statusLabel(view), mode), scheduler}
		lines = append(lines, "Objective: "+gstr(view, "objective"), "")
		lines = append(lines, "Status: "+statusLabel(view))
		lines = append(lines, "Mode: "+mode)
		if gflag(view, "sisyphus") {
			if current, total, ok := sisyphusStepProgress(view); ok {
				lines = append(lines, fmt.Sprintf("At step: %d of %d", current, total))
			}
		}
		var usageBits []string
		usage := view.obj("usage")
		if usage != nil && gnum(usage, "activeSeconds") > 0 {
			usageBits = append(usageBits, formatDuration(gnum(usage, "activeSeconds")))
		}
		if usage != nil && gnum(usage, "tokensUsed") > 0 {
			usageBits = append(usageBits, formatTokenValue(gnum(usage, "tokensUsed")))
		}
		used := "none"
		if len(usageBits) > 0 {
			used = strings.Join(usageBits, " · ")
		}
		lines = append(lines, "Cumulative goal usage (not context occupancy): "+used)
		if budget, ok := modelBudgetLine(view); ok {
			lines = append(lines, "Budget: "+budget)
		}
		if tl := view.obj("taskList"); tl != nil {
			lines = append(lines, "Tasks: "+buildTaskSummary(tl))
			if detail := buildGoalTaskDetailBlock(view); detail != "" {
				lines = append(lines, "", detail)
			}
		}
		if vc := jsTrim(gstr(view, "verificationContract")); vc != "" {
			lines = append(lines, "Verification contract: "+vc)
		}
		if st := gstr(view, "status"); st == "paused" || st == "blocked" {
			if r := gstr(view, "pauseReason"); r != "" {
				lines = append(lines, "Blocker: "+r)
			}
			if r := gstr(view, "pauseSuggestedAction"); r != "" {
				lines = append(lines, "Suggested action: "+r)
			}
		}
		if p := gstr(view, "activePath"); p != "" {
			lines = append(lines, "Path: "+p)
		}
		if p := gstr(view, "archivedPath"); p != "" {
			lines = append(lines, "Archive: "+p)
		}
		if otherCount > 0 {
			lines = append(lines, fmt.Sprintf("Other open goals: %d (user can run /goal-list or /goal-focus)", otherCount))
		}
		lines = append(lines, "")
		lines = append(lines, "Lifecycle: call update_goal({status: \"complete\"}) only when every requirement is satisfied — the independent auditor verifies from actual evidence. Call update_goal({status: \"blocked\"}) only after the same blocker recurs on three consecutive goal turns. User commands handle pause/resume/clear/focus.")
		if includeHistory {
			if history := buildGoalHistoryBlock(view, readGoalLedger(c.st).events); history != "" {
				lines = append(lines, "", history)
			}
		}
		return goalAgentResult{text: strings.Join(lines, "\n"), details: goalDetails(view, "")}, nil
	}

	// Compact state read; full requirements remain available through detail pages.
	lines := []string{fmt.Sprintf("Goal %s: %s, %s", gstr(view, "id"), statusLabel(view), mode), scheduler}
	objective := gstr(view, "objective")
	more := ""
	if utf16Len(objective) > 180 {
		more = " (full: get_goal section=objective)"
	}
	lines = append(lines, "Objective: "+truncateText(objective, 180)+more)
	if tl := view.obj("taskList"); tl != nil {
		current, firstPending := conciseTaskPointers(view)
		if current != nil {
			contract := ""
			if vc := gstr(current, "verificationContract"); vc != "" {
				contract = " — contract: " + truncateText(vc, 240)
			}
			lines = append(lines, fmt.Sprintf("Current task: %s — %s%s", gstr(current, "id"), truncateText(gstr(current, "title"), 180), contract))
		}
		if firstPending != nil {
			lines = append(lines, fmt.Sprintf("Next pending: %s — %s", gstr(firstPending, "id"), truncateText(gstr(firstPending, "title"), 180)))
		}
		lines = append(lines, "Tasks: "+buildTaskSummary(tl))
	} else if id := gstr(view, "currentTaskId"); id != "" {
		lines = append(lines, "Current task: "+id)
	}
	if budget, ok := modelBudgetLine(view); ok {
		lines = append(lines, "Budget: "+budget)
	}
	if st := gstr(view, "status"); (st == "paused" || st == "blocked") && gstr(view, "pauseReason") != "" {
		lines = append(lines, "Blocker: "+gstr(view, "pauseReason"))
	}
	if params["include_history"] == true {
		if history := buildGoalHistoryBlock(view, readGoalLedger(c.st).events); history != "" {
			lines = append(lines, history)
		}
	}
	return goalAgentResult{text: strings.Join(lines, "\n"), details: goalDetails(view, "")}, nil
}

// executeCreateGoal is create_goal's execute.
func executeCreateGoal(c *core, params map[string]any) (goalAgentResult, error) {
	c.reconcileFocusedGoalFromDisk(false)
	rawObjective, _ := toolParamString(params, "objective")
	objective := jsTrim(rawObjective)
	if objective == "" {
		return goalAgentResult{}, errors.New("create_goal requires a non-empty objective.")
	}
	if max := loadGoalSettings(c.h.Cwd()).objectiveMaxChars; max > 0 && float64(utf16Len(objective)) > max {
		return c.textResult(fmt.Sprintf("create_goal objective exceeds %s characters (%d). Shorten the objective or raise the max objective length setting.", jsNumber(max), utf16Len(objective))), nil
	}
	sisyphus := params["mode"] == "sisyphus"
	tokenBudget := 0.0
	if toolParamPresent(params, "token_budget") {
		// Tool callers are untrusted: re-validate the budget beyond the schema.
		value, message := validateTokenBudgetInput(params["token_budget"])
		if message != "" {
			return c.textResult(message), nil
		}
		tokenBudget = value
	}
	cleaned, contract := extractVerificationContract(objective)
	c.replaceGoal(goalCreationConfig{objective: cleaned, autoContinue: true, sisyphus: sisyphus}, true, contract, tokenBudget)
	created := c.state()
	otherCount := otherOpenGoalCount(c.goals, c.focused)
	otherLine := ""
	if otherCount > 0 {
		plural := "s"
		if otherCount == 1 {
			plural = ""
		}
		otherLine = fmt.Sprintf("\n%d other open goal%s remain in .pi/goals — this goal is now the session focus.", otherCount, plural)
	}
	// F2: the agent path stays tool-driven — surface the derivable task plan as guidance so the agent proposes it via set_goal_tasks.
	bootstrap := ""
	if created == nil || created.obj("taskList") == nil {
		if derived := deriveTasksFromObjective(cleaned); len(derived) > 0 {
			plural := "s"
			if len(derived) == 1 {
				plural = ""
			}
			bootstrap = fmt.Sprintf("\n\nThe objective contains %d ordered step%s; propose them as the task tree with set_goal_tasks if the user wants tracked milestones.", len(derived), plural)
		}
	}
	reportObjective := objective
	var budget *float64
	if created != nil {
		reportObjective = gstr(created, "objective")
		if v, ok := created.num("tokenBudget"); ok {
			budget = &v
		}
	}
	text := buildGoalCreatedReport(goalCreatedReportArgs{objective: reportObjective, detailedSummary: detailedSummary(created), tokenBudget: budget}) + bootstrap + otherLine
	return goalAgentResult{text: text, details: goalDetails(created, ""), terminate: true}, nil
}

// decodeGoalContinuation is `params.continuation as GoalContinuation`.
func decodeGoalContinuation(m map[string]any) goalContinuation {
	in := goalContinuation{}
	in.kind, _ = m["kind"].(string)
	if r, ok := m["reason"].(string); ok {
		in.reason = &r
	}
	in.deadline, _ = m["deadline"].(string)
	in.waitID, _ = m["wait_id"].(string)
	if pm, ok := m["polling"].(map[string]any); ok {
		num := func(k string) float64 {
			if f, ok := pm[k].(float64); ok {
				return f
			}
			return math.NaN()
		}
		in.polling = &goalPolling{intervalSeconds: num("interval_seconds"), maxChecks: num("max_checks")}
	}
	return in
}

// executeUpdateGoal is update_goal's execute.
func executeUpdateGoal(c *core, params map[string]any) (goalAgentResult, error) {
	// P1-3: persist any buffered in-turn mutations now so the auditor and status transitions observe the current task/state, not the
	// stale disk.
	c.flushGoalTransaction()
	continuation, hasContinuation := params["continuation"].(map[string]any)
	if raw, ok := params["continuation"]; ok && raw != nil && !hasContinuation {
		hasContinuation = true // a truthy non-object
	}
	status, _ := toolParamString(params, "status")
	if hasContinuation == (status != "") || hasContinuation && (toolParamPresent(params, "reason") || toolParamPresent(params, "attempted_actions") || toolParamPresent(params, "suggested_action") || toolParamPresent(params, "completion_summary")) {
		return goalAgentResult{text: "Provide exactly one lifecycle status or continuation, without mixing their fields.", details: newObject()}, nil
	}
	if hasContinuation {
		return c.scheduler.declare(decodeGoalContinuation(continuation)), nil
	}
	reason, hasReason := toolParamString(params, "reason")
	_ = hasReason
	switch status {
	case "blocked":
		var attempted []string
		if arr, ok := params["attempted_actions"].([]any); ok {
			for _, a := range arr {
				if s, ok := a.(string); ok {
					attempted = append(attempted, s)
				}
			}
		}
		return runGoalBlockedFlow(c, reason, hasReason, attempted)
	case "paused":
		action, _ := toolParamString(params, "suggested_action")
		return runGoalAgentPauseFlow(c, reason, hasReason, action)
	}
	summary, _ := toolParamString(params, "completion_summary")
	return runGoalCompletionFlow(c, summary)
}

func falsePtr() *bool { f := false; return &f }

// runGoalBlockedFlow is the model's blocked outcome: an active goal becomes agent-blocked, after the optional Oracle consultation.
func runGoalBlockedFlow(c *core, reasonInput string, _ bool, attemptedActions []string) (goalAgentResult, error) {
	c.reconcileFocusedGoalFromDisk(false)
	if gate := validateGoalBlock(c.state()); !gate.ok {
		return c.textResult(gate.message), nil
	}
	if c.state() == nil {
		return goalAgentResult{}, errors.New("Goal disappeared during blocked validation.")
	}

	// Issue #26: the blocker reason is now required for update_goal(blocked) — it feeds the fingerprint, the ledger event, and the
	// user-facing report.
	reason := jsTrim(reasonInput)
	if reason == "" {
		return c.textResult(`update_goal({ status: "blocked" }) requires a "reason" describing the concrete blocker. The goal remains active.`), nil
	}

	commitBlocked := func() (goalAgentResult, error) {
		result, err := c.goalService.apply(c.st, goalMutationSpec{
			reconcile:       falsePtr(),
			refreshFromDisk: true,
			mutate: func(g *jsObject) *jsObject {
				return goalWith(g, "status", "blocked", "stopReason", "agent", "pauseReason", reason, "updatedAt", nowIso())
			},
			ledgerFn: func(written *jsObject) ([]*jsObject, error) {
				why := gstr(written, "pauseReason")
				if why == "" {
					why = "blocked"
				}
				return []*jsObject{ledgerEvent("goal_blocked", "goalId", gstr(written, "id"), "reason", why, "source", "agent", "at", gstr(written, "updatedAt"))}, nil
			},
		})
		if err != nil {
			return goalAgentResult{}, err
		}
		if !result.ok {
			return c.textResult(fmt.Sprintf("Goal blocked state update failed: %s. The goal was NOT marked blocked. Retry after resolving the conflict.", mutationMessage(result))), nil
		}
		c.clearContinuationState(true)
		c.clearActiveAccounting()
		if result.goal != nil {
			c.runtime.markTurnStopped(gstr(result.goal, "id"))
		}
		c.updateUI()
		r := c.textResult("Goal blocked. Continuation stopped; the goal is waiting for the user to resume, revise, or clear it. Stop now; do not start another tool call.")
		r.terminate = true
		return r, nil
	}

	// Oracle disabled: existing block transition, unchanged behavior.
	snapshot := loadSettingsSnapshot(c.h.Cwd())
	oracleSettings := snapshot.value.oracle
	if !oracleSettings.enabled {
		return commitBlocked()
	}

	// ── opt-in Oracle flow (issue #26) ──
	goalAtBlock := c.state()
	goalID := gstr(goalAtBlock, "id")
	focusToken := c.focusedOperationToken(goalID)
	fingerprint := buildBlockerFingerprint(goalAtBlock, reason)
	consult := goalOracleState(c.st, goalID, fingerprint)
	failedAttempts := int(gnum(consult, "failedAttempts"))

	appendOracle := func(event *jsObject) { c.goalService.appendEvents(c.st, []*jsObject{event}) }

	// One actionable result already exists.
	if result := consult.obj("result"); result != nil && gstr(result, "disposition") == "actionable" {
		if !gflag(consult, "followupAttempted") && !hasPendingOracleAdviceForFocusedGoal(goalID) {
			// Re-arm without consulting again and refuse the block.
			adviceText := renderOracleAdviceReminder(armedAdvice{goalID: goalID, fingerprint: fingerprint, adviceID: gstr(result, "adviceId"), text: gstr(result, "summary")})
			return goalAgentResult{text: adviceText + "\n\nThe goal was NOT marked blocked.", details: goalDetails(goalAtBlock, "")}, nil
		}
		return commitBlocked()
	}
	if result := consult.obj("result"); result != nil && (gstr(result, "disposition") == "needs_human" || gstr(result, "disposition") == "insufficient_context") {
		return commitBlocked()
	}
	if failedAttempts >= oracleSettings.maxFailedAttemptsPerBlocker {
		code := "provider"
		if lf := consult.obj("lastFailure"); lf != nil && gstr(lf, "errorCode") != "" {
			code = gstr(lf, "errorCode")
		}
		appendOracle(ledgerEvent("oracle_failed", "goalId", goalID, "fingerprint", fingerprint, "attempt", float64(failedAttempts), "errorCode", code,
			"message", "failure limit reached; blocking with durable Oracle failure annotation", "at", nowIso()))
		return commitBlocked()
	}

	// Consult once.
	orDefault := func(s string) string {
		if s == "" {
			return "(unresolved)"
		}
		return s
	}
	appendOracle(ledgerEvent("oracle_started", "goalId", goalID, "fingerprint", fingerprint, "provider", orDefault(oracleSettings.provider),
		"model", orDefault(oracleSettings.model), "thinkingLevel", optionalString(oracleSettings.thinkingLevel), "reason", utf16Prefix(reason, 1000), "at", nowIso()))

	attempted := attemptedActions
	if len(attempted) > 8 {
		attempted = attempted[:8]
	}
	var run oracleRunResult
	// The Oracle is a model run, not a host dialog: the core lock is released while it runs (the original's await), so commands and
	// events (/goal-unfocus, Esc) reach the core meanwhile; the focus is revalidated below.
	oracleArgs := runBlockerOracleArgs{ctx: hostAuditorCtx{c.h}, goal: goalAtBlock, reason: reason, attemptedActions: attempted,
		settings: oracleSettings, recentEvidence: "", createSession: c.deps.createSession}
	c.flush()
	c.awaiting(func() { c.await(func() { run = runBlockerOracle(oracleArgs) }) })

	if !c.isFocusedOperationCurrent(focusToken) {
		return c.focusedOperationCancelledResult("Blocker Oracle", focusToken), nil
	}

	if !run.ok {
		if run.errorCode == "aborted" {
			appendOracle(ledgerEvent("oracle_failed", "goalId", goalID, "fingerprint", fingerprint, "attempt", float64(failedAttempts+1), "errorCode", "aborted",
				"message", utf16Prefix(run.message, 300), "at", nowIso()))
			return goalAgentResult{text: "Oracle consultation was aborted; the goal remains active.", details: goalDetails(goalAtBlock, "")}, nil
		}
		appendOracle(ledgerEvent("oracle_failed", "goalId", goalID, "fingerprint", fingerprint, "attempt", float64(failedAttempts+1), "errorCode", run.errorCode,
			"message", utf16Prefix(run.message, 300), "at", nowIso()))
		retry := ""
		if failedAttempts+1 < oracleSettings.maxFailedAttemptsPerBlocker {
			retry = " The goal remains active; try again or continue working."
		}
		return goalAgentResult{text: fmt.Sprintf("Oracle consultation failed (%s): %s%s", run.errorCode, utf16Prefix(run.message, 200), retry), details: goalDetails(goalAtBlock, "")}, nil
	}

	// Persist bounded result + arm/remind per disposition.
	advice := *run.advice
	adviceID := armOracleAdvice(goalID, fingerprint, advice)
	var recommendedTitle any = undef{}
	if advice.recommendedIndex >= 0 && advice.recommendedIndex < len(advice.alternatives) {
		recommendedTitle = utf16Prefix(advice.alternatives[advice.recommendedIndex].title, 200)
	}
	appendOracle(ledgerEvent("oracle_result", "goalId", goalID, "fingerprint", fingerprint, "adviceId", adviceID, "disposition", advice.disposition,
		"summary", utf16Prefix(advice.diagnosis, 500), "recommendedTitle", recommendedTitle, "at", nowIso()))

	if advice.disposition == "needs_human" || advice.disposition == "insufficient_context" {
		consumeOracleFollowupMarker(goalID)
		return commitBlocked()
	}
	return goalAgentResult{text: renderActionableOracleAdvice(advice), details: goalDetails(goalAtBlock, "")}, nil
}

// mutationMessage is `result.message ?? "the state mutation was rejected"`.
func mutationMessage(r goalMutationOutcome) string {
	if r.message == "" {
		return "the state mutation was rejected"
	}
	return r.message
}

// runGoalAgentPauseFlow is the agent-initiated immediate pause (Stage 5.1-C): update_goal({status: "paused"}) with a required reason
// pauses an active goal right away and records goal_paused with source "agent". It is distinct from blocked (three-consecutive-turn
// gate) and from the user-owned pause/resume commands.
func runGoalAgentPauseFlow(c *core, reason string, _ bool, suggestedAction string) (goalAgentResult, error) {
	c.reconcileFocusedGoalFromDisk(false)
	if gate := validateGoalAgentPause(c.state()); !gate.ok {
		return c.textResult(gate.message), nil
	}
	trimmedReason := jsTrim(reason)
	if trimmedReason == "" {
		return c.textResult(`update_goal({ status: "paused" }) requires a "reason" describing why the work is pausing.`), nil
	}
	if c.state() == nil {
		return goalAgentResult{}, errors.New("Goal disappeared during pause validation.")
	}
	c.accountProgress(0)
	trimmedAction := jsTrim(suggestedAction)
	result, err := c.goalService.apply(c.st, goalMutationSpec{
		reconcile:       falsePtr(),
		refreshFromDisk: true,
		mutate: func(g *jsObject) *jsObject {
			return goalWith(g, "status", "paused", "autoContinue", false, "stopReason", "agent", "pauseReason", trimmedReason,
				"pauseSuggestedAction", optionalString(trimmedAction), "updatedAt", nowIso())
		},
		ledgerFn: func(written *jsObject) ([]*jsObject, error) {
			why := gstr(written, "pauseReason")
			if why == "" {
				why = trimmedReason
			}
			var action any = undef{}
			if a := gstr(written, "pauseSuggestedAction"); a != "" {
				action = a
			}
			return []*jsObject{ledgerEvent("goal_paused", "goalId", gstr(written, "id"), "reason", why, "suggestedAction", action, "source", "agent", "at", gstr(written, "updatedAt"))}, nil
		},
	})
	if err != nil {
		return goalAgentResult{}, err
	}
	if result.ok {
		c.clearContinuationState(true)
		c.clearActiveAccounting()
		if result.goal != nil {
			c.runtime.markTurnStopped(gstr(result.goal, "id"))
		}
		c.updateUI()
		suggestion := ""
		if trimmedAction != "" {
			suggestion = " Suggested next step: " + trimmedAction
		}
		detail := "Pause reason: " + trimmedReason
		if trimmedAction != "" {
			detail += "\nSuggested action: " + trimmedAction
		}
		return goalAgentResult{
			text:      fmt.Sprintf("Goal paused by the agent: %s.%s Stop now; the user can resume with /goal-resume or revise with /goal-tweak.", trimmedReason, suggestion),
			details:   goalDetails(c.state(), detail), // E7
			terminate: true,
		}, nil
	}
	// The mutation failed: surface the conflict instead of claiming the goal is paused, and keep the turn alive so the agent can retry.
	return c.textResult(fmt.Sprintf("Goal pause update failed: %s. The goal was NOT paused. Retry after resolving the conflict.", mutationMessage(result))), nil
}
