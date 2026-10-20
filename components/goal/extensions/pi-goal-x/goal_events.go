package pi_goal_x

import (
	"encoding/json"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The events (goal-events.ts): the extension's lifecycle handlers. All state flows through the core; the handlers run under the
// core's lock with the event's context bound as the core's host (goalPi.on).

// eventJS decodes an SDK event value (decoded JSON) into the ordered-object representation the goal code works on.
func eventJS(v any) any {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	out, err := parseJSON(b)
	if err != nil {
		return nil
	}
	return out
}

func eventJSList(v any) []any {
	list, _ := eventJS(v).([]any)
	return list
}

func eventStr(data map[string]any, key string) string {
	s, _ := data[key].(string)
	return s
}

// compactGoalCheckpointContext normalizes checkpoints independently: deleting old markers shifts the cached prefix. A nil result is
// the original's null: no checkpoint to rewrite.
func compactGoalCheckpointContext(messages []any) []any {
	var output []any
	for i, m := range messages {
		id, ok := goalEventMessageId(m)
		if !ok {
			if output != nil {
				output = append(output, m)
			}
			continue
		}
		if output == nil {
			output = append([]any{}, messages[:i]...)
		}
		next := newObject()
		if rec := asRecord(m); rec != nil {
			next = rec.clone()
		}
		next.set("content", checkpointTriggerPrompt(id))
		next.set("display", false)
		details := newObject()
		details.set("version", 2.0)
		details.set("kind", "checkpoint")
		details.set("goalId", id)
		next.set("details", details)
		output = append(output, next)
	}
	return output
}

// eventSessionKey is sessionKeyFor(ctx): transients are keyed per session so the context hook of session B never overwrites the
// set that session A's provider request will consume.
func (c *core) eventSessionKey() string {
	id := c.h.SessionID()
	if id == "" {
		return "unidentified"
	}
	var provider, model any
	if m := c.h.Model(); m != nil {
		provider, model = m.provider, m.id
	}
	return marshalJSON([]any{id, provider, model}, "")
}

// eventAuditCtx and eventAuditSender are the host as GoalAuditMessages sees it.
type eventAuditCtx struct{ h host }

func (a eventAuditCtx) sessionKey() any { return a.h.SessionID() }
func (a eventAuditCtx) isIdle() bool    { return a.h.IsIdle() }

type eventAuditSender struct{ h host }

func (s eventAuditSender) sendMessage(message any, triggerTurn bool) {
	if m, ok := message.(goalAuditMessage); ok {
		_ = s.h.SendMessage(m.customType, m.content, m.display, m.details, triggerTurn, "")
	}
}

func registerGoalEvents(c *core) {
	// Transients are keyed per session (eventSessionKey).
	liveRetention := newLiveTailRetention()
	var continuationAfterSettleFor, networkErrorRecoveryAfterSettleFor *string

	c.pi.on("before_provider_request", func(c *core, data map[string]any) (any, error) {
		contents, _ := liveRetention.contents(c.eventSessionKey())
		if out := cacheGoalHistory(eventJS(data["payload"]), contents); out != nil {
			return out, nil
		}
		return nil, nil
	})

	// currentGoalContextParts is the request-only goal state split into a rarely-changing policy block and per-turn counters.
	// Other branches are stable-only. ok=false is the original's undefined.
	currentGoalContextParts := func(c *core) (state string, counters *string, ok bool) {
		checkpoint := c.runtime.getCheckpointGoalId()
		if checkpoint != nil && !c.isActionableContinuationGoal(checkpoint) {
			return staleContinuationPrompt(*checkpoint, c.state()), nil, true
		}
		// Several prompt enrichments may need the same ledger snapshot: one local read per hook.
		var promptLedger []*jsObject
		haveLedger := false
		getPromptLedger := func() []*jsObject {
			if !haveLedger {
				haveLedger = true
				if g := c.state(); g != nil {
					promptLedger = goalRuntimeEvents(c.st, gstr(g, "id"))
				}
			}
			return promptLedger
		}
		g := c.state()
		if g == nil {
			// Issue #72: hideUnfocusedPrompt opts out of the model-facing unfocused reminder only.
			if loadGoalSettings(c.h.Cwd()).hideUnfocusedPrompt {
				return "", nil, false
			}
			if openCount := otherOpenGoalCount(c.goals, nil); openCount > 0 {
				return unfocusedOpenGoalsPrompt(openCount), nil, true
			}
			return "", nil, false
		}
		switch gstr(g, "status") {
		case "complete":
			return "", nil, false
		case "paused":
			var pauseExtras []string
			if gstr(g, "stopReason") == "agent" {
				reason := "(unknown)"
				if v, has := g.str("pauseReason"); has {
					reason = v
				}
				pauseExtras = append(pauseExtras, "", "Pause reason: "+reason)
				if a := gstr(g, "pauseSuggestedAction"); a != "" {
					pauseExtras = append(pauseExtras, "Suggested action: "+a)
				}
			}
			// Inject durable auditor feedback if available.
			auditorExtra := ""
			if r := latestAuditorResultForGoal(getPromptLedger(), gstr(g, "id")); r != nil && gstr(r, "verdict") == "disapproved" {
				auditorExtra = "\n\n[AUDITOR REJECTION] An independent auditor previously rejected a completion request for this goal. Reason: " + utf16Prefix(gstr(r, "report"), 300) + "\nAddress the auditor's objections before requesting completion again."
			}
			return "[PI GOAL PAUSED goalId=" + gstr(g, "id") + "]\n" + untrustedObjectiveBlock(g) + strings.Join(pauseExtras, "\n") + auditorExtra + "\n\nThe goal is paused. Do not autonomously continue substantive work unless the user resumes it with /goal-resume. If the user explicitly asks to finish the paused goal and the objective is already satisfied based on available evidence, you may call update_goal({status: \"complete\"}). To abandon a goal, the user runs /goal-clear. Do not report the goal blocked in response to a pause.", nil, true
		case "budget_limited":
			budgetText, hasBudget := modelBudgetLine(g)
			// E4: surface the remaining-vs-overshoot fact in the wrap-up steering.
			balanceText := ""
			if remaining, ok := budgetRemaining(g); ok {
				if remaining < 0 {
					balanceText = " — " + formatTokenValue(-remaining) + " over the budget"
				} else {
					balanceText = " — " + formatTokenValue(remaining) + " remaining"
				}
			}
			reminder := ""
			if c.runtime.consumePostBudgetReminder() {
				detail := ""
				if hasBudget && budgetText != "" {
					detail = " (" + budgetText + balanceText + ")"
				}
				reminder = "\n\n[TOKEN BUDGET REACHED goalId=" + gstr(g, "id") + "]\nThe goal's lifetime token-spending cap (not context capacity) has been reached" + detail + ". Wrap up the current work in one final response: summarize what was accomplished and what remains, do not start new substantive work, and do not claim the goal is complete unless it actually is. To continue, the user can use /goal-tweak to raise or remove the token budget."
			}
			line := ""
			if hasBudget && budgetText != "" {
				line = "\n" + budgetText
			}
			return "[PI GOAL BUDGET LIMITED goalId=" + gstr(g, "id") + "]\n" + untrustedObjectiveBlock(g) + line + reminder, nil, true
		case "blocked":
			blocker := "unspecified"
			if v, has := g.str("pauseReason"); has {
				blocker = v
			}
			return "[PI GOAL BLOCKED goalId=" + gstr(g, "id") + "]\n" + untrustedObjectiveBlock(g) + "\nBlocker: " + blocker + "\nThe goal is blocked; the user must run /goal-resume before goal work continues.", nil, true
		}
		settings := loadGoalSettings(c.h.Cwd())
		parts := goalPromptParts(g, &settings, c.h.ContextUsage())
		// Rare transitional steering rides in the stable block; per-turn usage and scheduling counters stay in the volatile block.
		state = parts.state
		// F5: [GOAL STALLED] steering note when the detector fired.
		state += c.checkStall()
		// Inject durable auditor feedback if the latest result was a rejection.
		events := getPromptLedger()
		if r := latestAuditorResultForGoal(events, gstr(g, "id")); r != nil && gstr(r, "verdict") == "disapproved" {
			requested := false
			for _, e := range events {
				if gstr(e, "type") == "completion_requested" && gstr(e, "goalId") == gstr(g, "id") {
					requested = true
					break
				}
			}
			if requested {
				state += "\n\n[AUDITOR REJECTION goalId=" + gstr(g, "id") + "]\nAn independent auditor previously rejected a completion request for this goal. Reason: " + utf16Prefix(gstr(r, "report"), 300) + "\nAddress the auditor's objections before requesting completion again."
			}
		}
		if c.runtime.isPostCompactReminderPending() && shouldInjectPostCompactReminder(true, g) {
			c.runtime.clearPostCompactReminder()
			// PR E §62: post-compaction DELTA: only what compaction may have lost.
			otherOpenCount := 0
			for _, o := range c.openGoals() {
				if gstr(o, "id") != gstr(g, "id") {
					otherOpenCount++
				}
			}
			state += "\n\n" + buildPostCompactionGoalDelta(postCompactionDeltaArgs{goal: g, ledgerEvents: events, otherOpenCount: otherOpenCount})
		}
		counters = &parts.counters
		return state, counters, true
	}

	c.pi.on("context", func(c *core, data map[string]any) (any, error) {
		msgs := eventJSList(data["messages"])
		filtered := filterGoalSessionContext(msgs, false)
		src := msgs
		if filtered != nil {
			src = filtered
		}
		messages := compactGoalCheckpointContext(src)
		if messages == nil {
			messages = filtered
		}
		base := msgs
		if messages != nil {
			base = messages
		}
		sessionKey := c.eventSessionKey()
		if sessionKey == "unidentified" {
			liveRetention.clear(&sessionKey)
		}
		if state, counters, ok := currentGoalContextParts(c); ok {
			// Retain previously sent tails verbatim at their anchors so this request extends the previous one.
			retained := liveRetention.apply(sessionKey, base, &freshLiveTails{state: state, counters: counters})
			return map[string]any{"messages": retained.messages}, nil
		}
		liveRetention.clear(&sessionKey)
		if messages == nil {
			return nil, nil
		}
		return map[string]any{"messages": messages}, nil
	})

	c.pi.on("agent_start", func(c *core, _ map[string]any) (any, error) {
		c.scheduler.begin()
		return nil, nil
	})
	c.pi.on("message_start", func(c *core, data map[string]any) (any, error) {
		c.scheduler.message(eventJS(data["message"]))
		return nil, nil
	})
	c.pi.on("turn_start", func(c *core, _ map[string]any) (any, error) {
		c.scheduler.turn()
		// Per-turn flag resets (#4 + C9 fix).
		c.advanceTurnSeq()
		c.goalWorkToolCalledThisTurn = false
		c.beginAccounting()
		id := ""
		if c.focused != nil {
			id = *c.focused
		}
		c.goalService.beginTurn(c.st, id) // P1-3 transaction buffer
		c.touchGoalActivity()             // F5
		c.updateUI()
		return nil, nil
	})

	// #4 + C9 fix + Phase 5 C3: gate in-turn tool calls based on lifecycle state.
	c.pi.on("tool_call", func(c *core, data map[string]any) (any, error) {
		toolName := eventStr(data, "toolName")
		stoppedGoalID := c.currentTurnStoppedGoalId()
		if c.scheduler.isDenied() {
			return map[string]any{"block": true, "reason": "Stale goal dispatch; use /goal-resume."}, nil
		}
		// Post-stop in-turn block: after update_goal / set_goal_tasks (or a user lifecycle command) fires in this turn, block all
		// subsequent tool calls except read-only inspection.
		if stoppedGoalID != nil && c.runtime.isStaleCheckpointBlocked(toolName) {
			return map[string]any{"block": true, "reason": "The goal was already stopped earlier in this turn (goalId=" + *stoppedGoalID + "). " +
				"Do not call more tools; end the turn with a brief summary and yield to the user."}, nil
		}
		// Stale checkpoint guard: a turn triggered by a queued continuation for a goal that is no longer active/autoContinue.
		if checkpoint := c.runtime.getCheckpointGoalId(); checkpoint != nil && !c.isActionableContinuationGoal(checkpoint) && c.isStaleCheckpointBlockedToolCall(toolName) {
			return map[string]any{"block": true, "reason": "Cannot call " + toolName + ": the goal checkpoint that triggered this turn is no longer active. " +
				"Goal " + *checkpoint + " has been paused, cleared, or replaced. " +
				"End the turn with a brief summary and yield to the user."}, nil
		}
		// Track Oracle work attempts; this does not authorize scheduling.
		if isMeaningfulProgressToolCall(toolName, eventJS(data["args"])) {
			c.goalWorkToolCalledThisTurn = true
			// Issue #26: record a meaningful work attempt against armed Oracle advice.
			if c.focused != nil && hasPendingOracleAdviceForFocusedGoal(*c.focused) {
				if armed, ok := consumeOracleFollowupMarker(*c.focused); ok {
					c.goalService.appendEvents(c.st, []*jsObject{ledgerEvent("oracle_followup_attempted",
						"goalId", armed.goalID, "fingerprint", armed.fingerprint, "adviceId", armed.adviceID, "firstToolName", toolName, "at", nowIso())})
				}
			}
		}
		return nil, nil
	})

	c.pi.on("tool_execution_end", func(c *core, _ map[string]any) (any, error) {
		c.touchGoalActivity() // F5
		c.accountProgress(0)
		return nil, nil
	})

	c.pi.on("turn_end", func(c *core, data map[string]any) (any, error) {
		message := eventJS(data["message"])
		tokens := assistantTurnTokens(message)
		c.touchGoalActivity() // F5
		c.accountProgress(tokens)

		if isAbortedAssistantMessage(message) {
			// Pause only on a genuine user abort (signal fired). A provider- or transport-side abort without the signal routes into
			// recovery via agent_end instead of stranding the goal.
			if c.h.Signal().Err() != nil {
				c.pauseActiveGoal()
			}
			return nil, nil
		}
		// Provider failures are not completed work: keep the display reconciled, never queue a continuation (danim47c pattern).
		if isErrorAssistantMessage(message) {
			c.refreshGoalDisplayFromDisk()
			c.updateUI()
			return nil, nil
		}
		c.refreshGoalDisplayFromDisk()

		// Archive a goal that was marked complete but whose archival was deferred so the agent could see the audit result first.
		if g := c.state(); g != nil && gstr(g, "status") == "complete" && !truthy(g.vals["archivedPath"]) {
			c.archiveDeferredCompletion(g)
		}

		c.goalService.endTurn(c.st) // P1-3: single flush (lock + write + ledger batch)
		return nil, nil
	})

	c.pi.on("message_end", func(c *core, data map[string]any) (any, error) {
		message := eventJS(data["message"])
		// Signal-aware: see turn_end: only user aborts pause; provider-side aborts are handled by agent_end's recovery path.
		if isAbortedAssistantMessage(message) && c.h.Signal().Err() != nil {
			c.pauseActiveGoal()
		}
		if raw := asRecord(message); raw != nil {
			if role, _ := raw.str("role"); role == "custom" {
				if custom, _ := raw.str("customType"); custom == goalEventEntry && raw.vals["display"] != any(false) {
					next := raw.clone()
					next.set("display", false)
					return map[string]any{"message": next}, nil
				}
			}
		}
		return nil, nil
	})

	c.pi.on("session_start", func(c *core, data map[string]any) (any, error) {
		c.auditMessages.clear()
		// NAF: the zero-op read caches are session-scoped: a new session always re-reads settings/pool/ledger fresh from disk.
		invalidateGoalSettingsCache()
		invalidateGoalPoolCache()
		invalidateGoalLedgerCache()
		_, _ = c.goalService.flushTurn(c.st) // P1-3: persist any buffered transaction before reload
		c.loadState()
		c.flush() // the original awaits the load: the queued UI update runs here
		c.installGoalToolProfile(!loadGoalSettings(c.h.Cwd()).disableTasks)
		c.rehydrateDraft()
		c.syncTerminalInputPause()
		resume := eventStr(data, "reason") == "resume"
		if resume && c.state() == nil && !c.explicit && otherOpenGoalCount(c.goals, nil) > 1 && c.h.HasUI() {
			// Prompt the user to pick which open goal to focus (mirrors /goal-focus).
			open := c.openGoals()
			labels := make([]string, len(open))
			byLabel := map[string]string{}
			for i, item := range open {
				labels[i] = goalSelectorLabel(item, c.focused)
				byLabel[labels[i]] = gstr(item, "id")
			}
			c.enterGoalModal()
			var selected string
			var ok bool
			c.awaiting(func() { selected, ok = c.h.Select("Focus open goal", labels) })
			if ok && selected != "" {
				if id, found := byLabel[selected]; found && id != "" {
					c.setFocusedGoalId(&id, "selected", true)
					c.armFocusedContinuation()
				}
			}
			c.exitGoalModal()
		}
		// Codex behavior: prompt before reactivating a paused goal on resume.
		if g := c.state(); resume && g != nil && gstr(g, "status") == "paused" && c.h.HasUI() {
			var shouldResume bool
			c.awaiting(func() { shouldResume = c.h.Confirm("Resume paused goal?", "Goal: "+gstr(g, "objective")) })
			if shouldResume {
				c.scheduler.resume()
			}
		}
		c.beginAccounting()
		c.scheduler.restore()
		return nil, nil
	})

	c.pi.on("session_before_compact", func(c *core, _ map[string]any) (any, error) {
		c.accountProgress(0)
		return nil, nil
	})

	c.pi.on("session_compact", func(c *core, _ map[string]any) (any, error) {
		_, _ = c.goalService.flushTurn(c.st) // P1-3: persist any buffered transaction before reload
		if c.state() != nil {
			c.persist()
		}
		c.beginAccounting()
		// Arm a deterministic compaction summary for the next agent turn.
		if shouldArmPostCompactReminder(c.state()) {
			c.runtime.armPostCompactReminder()
		}
		c.queueContinuation()
		return nil, nil
	})

	// session_compact_failed exists from pi 1.0.0; on older hosts the handler is simply never called.
	c.pi.on("session_compact_failed", func(c *core, data map[string]any) (any, error) {
		// session_before_compact already charged progress to the goal, so flush and persist first: otherwise the goal record and
		// the ledger can disagree about a compaction that never happened.
		_, _ = c.goalService.flushTurn(c.st)
		if c.state() != nil {
			c.persist()
		}
		if aborted, _ := data["aborted"].(bool); aborted || c.state() == nil {
			return nil, nil
		}
		detail := ""
		if msg, _ := data["errorMessage"].(string); msg != "" {
			detail = ": " + msg
		}
		c.h.Notify("Goal context compaction failed ("+eventStr(data, "reason")+")"+detail+". The goal stays active; retry with /compact or keep working.", "warning")
		return nil, nil
	})

	c.pi.on("session_tree", func(c *core, _ map[string]any) (any, error) {
		c.auditMessages.clear()
		_, _ = c.goalService.flushTurn(c.st) // P1-3: persist any buffered transaction before reload
		c.loadState()
		c.flush()
		c.rehydrateDraft()
		c.syncTerminalInputPause()
		c.beginAccounting()
		c.scheduler.restore()
		return nil, nil
	})

	c.pi.on("before_agent_start", func(c *core, data map[string]any) (any, error) {
		c.advanceTurnSeq()
		if !c.hasActiveDraft() {
			c.installGoalToolProfile(!loadGoalSettings(c.h.Cwd()).disableTasks)
		}
		incoming, isCheckpoint := extractGoalIdFromInjectedMessage(eventStr(data, "prompt"))
		// A turn triggered by a hidden goal checkpoint that no longer matches the active goal aborts the whole turn instead of
		// letting the model act on a stale instruction.
		if isCheckpoint {
			// Reconcile from disk to pick up any external state changes before evaluating whether the checkpoint is actionable.
			c.reconcileFocusedGoalFromDisk(false)
			c.runtime.setCheckpoint(&incoming)
			// This can be the hidden checkpoint dispatched by the network-error timer. Clear ordinary continuation bookkeeping
			// but retain the consecutive recovery count for a later failed retry.
			c.clearContinuationState(false)
			if !c.isActionableContinuationGoal(&incoming) {
				c.h.Abort()
				c.updateUI()
				return nil, nil
			}
			c.runtime.setCheckpoint(nil)
		} else {
			// A user-driven turn: clear any queued continuation and reset the autoContinue nudge state.
			c.runtime.setCheckpoint(nil)
			c.clearContinuationState(true)
			networkErrorRecoveryAfterSettleFor = nil
		}
		c.reconcileFocusedGoalFromDisk(false)
		if g := c.state(); g != nil && gstr(g, "status") == "active" {
			c.runningGoalID = new(gstr(g, "id"))
		} else {
			c.runningGoalID = nil
		}
		return nil, nil
	})

	c.pi.on("agent_end", func(c *core, data map[string]any) (any, error) {
		endedGoalID := c.runningGoalID
		c.runningGoalID = nil
		continuationAfterSettleFor = nil
		networkErrorRecoveryAfterSettleFor = nil
		messages := eventJSList(data["messages"])

		// Account for any tokens from aborted in-flight assistant messages so they are not silently lost (charged to the goal
		// they ran for).
		abortedTokens := 0.0
		for _, m := range messages {
			if isAbortedAssistantMessage(m) {
				abortedTokens += assistantTurnTokens(m)
			}
		}
		if g := c.state(); abortedTokens > 0 && endedGoalID != nil && g != nil && gstr(g, "id") == *endedGoalID {
			c.accountProgress(abortedTokens)
		}

		// Keep any prior recovery attempt while Pi finishes its own automatic retries.
		c.runtime.clearContinuationState(false)
		g := c.state()
		if g == nil || gstr(g, "status") != "active" || !gflag(g, "autoContinue") {
			return nil, nil
		}
		if endedGoalID != nil && gstr(g, "id") != *endedGoalID {
			return nil, nil
		}
		if !c.reconcileFocusedGoalFromDisk(false) {
			return nil, nil
		}
		// A genuine user abort pauses the goal. An assistant message with stopReason "aborted" WITHOUT a user abort signal is a
		// provider- or transport-side termination: it routes into the same bounded recovery as classified transient errors.
		if c.h.Signal().Err() != nil {
			c.pauseActiveGoal()
			return nil, nil
		}
		// Provider failures are not completed work: persist and refresh the display, never queue a continuation.
		if hasNetworkErrorAssistantMessage(messages) || hasAbortedAssistantMessage(messages) {
			c.persist()
			c.updateUI()
			networkErrorRecoveryAfterSettleFor = new(gstr(c.state(), "id"))
			return nil, nil
		}
		if hasErrorAssistantMessage(messages) {
			c.persist()
			c.updateUI()
			return nil, nil
		}
		c.runtime.clearNetworkErrorBackoff()
		c.persist()
		c.updateUI()
		// agent_end runs before pi finishes retries, compaction, terminating-tool settlement, and queued messages;
		// agent_settled is the first point where pi guarantees no automatic work remains.
		continuationAfterSettleFor = new(gstr(c.state(), "id"))
		return nil, nil
	})

	c.pi.on("agent_settled", func(c *core, _ map[string]any) (any, error) {
		c.auditMessages.flush(eventAuditCtx{c.h}, eventAuditSender{c.h})
		goalID := continuationAfterSettleFor
		continuationAfterSettleFor = nil
		networkErrorGoalID := networkErrorRecoveryAfterSettleFor
		networkErrorRecoveryAfterSettleFor = nil
		actionable := goalID != nil && *goalID != "" && c.isActionableContinuationGoal(goalID)
		c.scheduler.settled(actionable)
		if actionable {
			return nil, nil
		}
		if networkErrorGoalID == nil || !c.isActionableContinuationGoal(networkErrorGoalID) {
			return nil, nil
		}
		recovery := loadGoalSettings(c.h.Cwd()).networkRecovery
		policy := &networkErrorRecoveryPolicy{maxAttempts: recovery.maxAttempts, maxDelayMs: recovery.maxDelayMs}
		if plan := c.runtime.scheduleNetworkErrorRetry(hostRuntimeCtx{c.h}, c.state(), policy); plan != nil {
			cap := ", unbounded"
			if plan.maxAttempts > 0 {
				cap = "/" + jsNumber(plan.maxAttempts)
			}
			c.h.Notify("Provider network error. Retrying the goal in "+jsNumber(jsRound(plan.delayMs/1000))+"s (recovery "+jsNumber(plan.attempt)+cap+").", "warning")
			return nil, nil
		}
		// Only reachable under a configured bounded cap (maxAttempts > 0).
		c.h.Notify("Provider network errors persisted after all recovery attempts. The goal remains active; resume it when the provider is healthy.", "warning")
		return nil, nil
	})

	c.pi.on("session_shutdown", func(c *core, _ map[string]any) (any, error) {
		c.auditMessages.clear()
		c.scheduler.shutdown()
		continuationAfterSettleFor = nil
		networkErrorRecoveryAfterSettleFor = nil
		c.accountProgress(0)
		c.clearContinuationState(true)
		if c.terminalInputUnsubscribe != nil {
			c.terminalInputUnsubscribe()
		}
		c.terminalInputUnsubscribe = nil
		if c.state() != nil {
			c.persist()
		}
		c.clearGoalWidget() // Cancel UI work queued by final accounting before ctx is invalidated.
		key := c.eventSessionKey()
		liveRetention.clear(&key)
		return nil, nil
	})
}

// archiveDeferredCompletion archives a complete goal whose archival was deferred (turn_end): the agent has now seen the result.
func (c *core) archiveDeferredCompletion(completed *jsObject) {
	noReconcile, noCommit := false, false
	outcome, err := c.goalService.apply(c.st, goalMutationSpec{
		reconcile:     &noReconcile,
		archive:       true,
		commitFocused: &noCommit,
		mutate:        func(*jsObject) *jsObject { return completed },
		ledgerFn: func(written *jsObject) ([]*jsObject, error) {
			ev := ledgerEvent("goal_completed", "goalId", gstr(completed, "id"))
			ev.setOpt("archivePath", written.vals["archivedPath"])
			ev.set("at", nowIso())
			return []*jsObject{ev}, nil
		},
	})
	if err != nil {
		// The archive write throws on failure (e.g. unwritable archived directory); surface it as a typed outcome.
		outcome = goalMutationFail(err.Error())
	}
	id := gstr(completed, "id")
	if outcome.ok {
		c.goals.delete(id)
		c.assignFocused(nil)
		c.appendFocusEntry(nil, "completed")
		// §16.6: the dedicated goal_archived event (the completion transaction keeps goal_completed for compatibility).
		path := ""
		if outcome.goal != nil {
			path = gstr(outcome.goal, "archivedPath")
		}
		c.goalService.appendEvents(c.st, []*jsObject{ledgerEvent("goal_archived", "goalId", id, "archivePath", path, "at", nowIso())})
		if path != "" {
			c.h.Notify("Goal archived.\nFile: "+path, "info")
		} else {
			c.h.Notify("Goal archived.", "info")
		}
	} else {
		// §16.6 failure behavior: never claim success, keep the complete record recoverable at its active path.
		remaining := "(unknown)"
		if p, ok := completed.str("activePath"); ok && p != "" {
			remaining = p
		}
		c.h.Notify("Failed to archive completed goal: "+outcome.message+". The complete record remains at "+remaining+".", "warning")
		message := outcome.message
		if message == "" {
			message = "archive write failed"
		}
		c.goalService.appendEvents(c.st, []*jsObject{ledgerEvent("goal_archive_failed", "goalId", id, "message", message, "at", nowIso())})
	}
	c.updateUI()
}

// on registers an event handler that runs under the core's lock with the event's context bound as the core's host.
func (p *goalPi) on(event string, fn func(c *core, data map[string]any) (any, error)) {
	p.e.OnEvent(event, func(ctx sdk.Context, data map[string]any) (any, error) {
		var out any
		var err error
		p.c.withCtx(ctx, func() { out, err = fn(p.c, data) })
		return out, err
	})
}
