package pi_background_tasks

import (
	"slices"
	"time"
)

// Output reactions and wake delivery of a task. upstream: extensions/background-tasks.ts:418-596.

func (rt *bgRuntime) persistScheduledOutputDrop(t *task, pending wakePending, reason string, extra wakeDiagnostic) {
	recordScheduledOutputDrop(t, pending, reason, extra, rt.logWake, clockNow)
	// A dropped output wake is a diagnostic record, and a chatty task drops one per chunk; it rides the next persist
	// instead of forcing one.
	rt.persistSoon.request()
}

func (rt *bgRuntime) wakeBudgetLimits(cwd string) outputWakeBudgetLimits {
	return outputWakeBudgetLimits{
		maxBytes: max(0, jsToInt(settingNumber("outputWakeBudgetMaxBytes", defaultWakeBudgetMaxBytes, cwd))),
		maxWakes: max(0, jsToInt(settingNumber("outputWakeBudgetMaxWakes", defaultWakeBudgetMaxWakes, cwd))),
	}
}

func (rt *bgRuntime) announceWakeBudgetExhausted(t *task) bool {
	announced := sendOutputWakeBudgetExhaustedNotice(rt.noticeDeps(), t, rt.wakeBudgetLimits(rt.cwd()))
	if announced {
		rememberSnapshot(t)
		rt.persistNow()
	}
	return announced
}

// sendTaskEvent delivers an output or exit wake. An exit wake of a task whose output was released reads its tail first, so
// it returns false at once and the wake follows from the read; a cleared task gets none.
func (rt *bgRuntime) sendTaskEvent(eventType string, t *task, o sendTaskWakeOptions, output *outputTail) bool {
	if eventType == eventExit && (rt.tasks.get(t.ID) != t || rt.shuttingDown) {
		return false
	}
	alertMax := settingNumber("outputAlertMaxChars", defaultOutputAlertMaxChars, rt.cwd())
	if eventType == eventExit && t.NotifyOnExit && !t.ExitNotified && len(t.output) == 0 && output == nil {
		if rt.exitWakeDue[t] {
			return false
		}
		rt.exitWakeDue[t] = true
		// The read is started here, under the lock: its place among the task's other readers and in the admission queue is
		// the order of the calls, as in the original, whatever the goroutine below does first.
		read := rt.reader.start(t, alertMax, t.output)
		go func() {
			tail := read.wait()
			rt.loop.post(func() {
				rt.run(func() {
					defer rt.releaseExit(t)
					if rt.tasks.get(t.ID) != t || rt.shuttingDown || t.ExitNotified {
						return
					}
					hooks := rt.hooks
					hooks.sendTaskEvent = func(typ string, target *task) bool { return rt.sendTaskEvent(typ, target, o, &tail) }
					sendExitWakeLifecycle(t, hooks)
				})
			})
		}()
		return false
	}
	sent := sendTaskWake(sendTaskWakeDeps{
		isShuttingDown: func() bool { return rt.shuttingDown },
		logDiagnostic:  rt.logWake,
		messageType:    bgMessageType,
		outputTail: func(target *task) string {
			if output != nil {
				return tailText(output.text, alertMax, output.truncated)
			}
			return tailText(target.output, alertMax, false)
		},
		rememberSnapshot: rememberSnapshot,
		sendMessage:      rt.sendMessage,
	}, eventType, t, o)
	seq := t.WakeSequence
	if o.sequence != nil {
		seq = *o.sequence
	}
	publishBackgroundTaskActivity(eventType, t, activityOptions{eventAt: o.eventAt, matchedPattern: o.matchedPattern, newOutputTail: o.newOutputTail, sequence: &seq})
	rememberSnapshot(t)
	rt.persistNow()
	return sent
}

// scheduleOutputReaction arms the settle timer after a chunk: the output wake is decided when the output has been quiet for
// outputSettleMs.
func (rt *bgRuntime) scheduleOutputReaction(t *task) {
	ensureWakeState(t)
	if !t.NotifyOnOutput {
		return
	}
	if !canEmitOutputWake(t) {
		at := clockNow()
		if t.LastOutputAt != nil {
			at = *t.LastOutputAt
		}
		rt.logWake(wakeDiagnostic{EventAt: &at, EventType: eventOutput, Reason: "output-after-stop-suppressed", StopReason: t.stopReason, TaskID: t.ID, TaskStatus: t.Status, Timestamp: clockNow()})
		return
	}
	if t.outputTimer != nil {
		for _, p := range slices.Clone(t.PendingWakes) {
			if p.EventType == eventOutput {
				rt.persistScheduledOutputDrop(t, p, "output-wake-rescheduled", wakeDiagnostic{})
			}
		}
		t.outputTimer.Stop()
	}
	at := clockNow()
	if t.LastOutputAt != nil {
		at = *t.LastOutputAt
	}
	pending := scheduleTaskWake(t, eventOutput, at)
	settle := settingNumber("outputSettleMs", defaultOutputSettleMs, rt.cwd())
	t.outputTimer = rt.after(time.Duration(settle*float64(time.Millisecond)), func() { rt.onOutputSettled(t, pending) })
}

// onOutputSettled is the settle timer: it decides the wake from the new output.
func (rt *bgRuntime) onOutputSettled(t *task, pending wakePending) {
	t.outputTimer = nil
	if !canEmitOutputWake(t) {
		rt.sendTaskEvent(eventOutput, t, sendTaskWakeOptions{eventAt: &pending.EventAt, sequence: &pending.Sequence}, nil)
		rt.refreshUI()
		return
	}
	maxChars := settingNumber("outputBufferMaxChars", defaultOutputBufferMaxChars, rt.cwd())
	// The read waits for the log and the admission queue, so it runs without the lock; the decision continues on the loop.
	read := rt.reader.start(t, maxChars, t.output)
	go func() {
		output := read.wait().text
		rt.loop.post(func() { rt.run(func() { rt.decideOutputWake(t, pending, output) }) })
	}()
}

func (rt *bgRuntime) decideOutputWake(t *task, pending wakePending, output string) {
	unseen := jsSliceFrom(output, t.lastAnnouncedLen)
	if jsTrim(unseen) == "" {
		t.lastAnnouncedLen = jsLen(output)
		rt.persistScheduledOutputDrop(t, pending, "empty-output", wakeDiagnostic{})
		return
	}
	if !canEmitOutputWake(t) {
		rt.sendTaskEvent(eventOutput, t, sendTaskWakeOptions{eventAt: &pending.EventAt, sequence: &pending.Sequence}, nil)
		rt.refreshUI()
		return
	}
	patternMatched := true
	if t.matcher != nil {
		matched, err := t.matcher(unseen)
		if err == nil && !matched {
			matched, err = t.matcher(output)
		}
		if err != nil {
			logBackgroundDiagnostic("notify pattern over budget", map[string]any{"id": t.ID, "pattern": t.NotifyPattern, "error": err.Error()})
			sendOutputMatcherTimeoutNotice(rt.noticeDeps(), t, err.Error())
			rt.persistScheduledOutputDrop(t, pending, "notify-pattern-timeout", wakeDiagnostic{MatchedPattern: t.NotifyPattern})
			return
		}
		patternMatched = matched
	}
	if !patternMatched {
		rt.persistScheduledOutputDrop(t, pending, "notify-pattern-no-match", wakeDiagnostic{MatchedPattern: t.NotifyPattern})
		return
	}
	alertMax := settingNumber("outputAlertMaxChars", defaultOutputAlertMaxChars, rt.cwd())
	newTail := tailText(unseen, alertMax, false)
	var decisions []wakeDiagnostic
	limits := rt.wakeBudgetLimits(rt.cwd())
	emit := shouldEmitOutputWake(t, outputDecisionInput{
		wakeBudgetLimits: &limits, dedupeHashes: rt.dedupe, eventAt: pending.EventAt,
		logDiagnostic: func(d wakeDiagnostic) { decisions = append(decisions, d) }, newOutput: unseen, newOutputTail: newTail,
		patternMatched: patternMatched, sequence: &pending.Sequence,
	})
	if !emit {
		var last wakeDiagnostic
		reason := "output-after-stop-suppressed"
		if len(decisions) > 0 {
			last = decisions[len(decisions)-1]
			reason = last.Reason
		}
		t.lastAnnouncedLen = jsLen(output)
		rt.persistScheduledOutputDrop(t, pending, reason, last)
		if reason == "wake-budget-exhausted" {
			rt.announceWakeBudgetExhausted(t)
		}
		rt.refreshUI()
		return
	}
	t.lastAnnouncedLen = jsLen(output)
	rt.sendTaskEvent(eventOutput, t, sendTaskWakeOptions{eventAt: &pending.EventAt, matchedPattern: t.NotifyPattern, newOutputTail: newTail, sequence: &pending.Sequence}, nil)
	rt.refreshUI()
}
