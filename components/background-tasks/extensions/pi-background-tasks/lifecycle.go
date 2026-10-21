package pi_background_tasks

// Terminal-state transitions of a task. upstream: extensions/lifecycle.ts.

// lifecycleHooks is what the transitions call; the runtime wires its state into it and tests inject recorders.
type lifecycleHooks struct {
	rememberSnapshot func(*task) *snapshot
	persistSnapshots func()
	sendTaskEvent    func(eventType string, t *task) bool
	refreshUI        func()
	clearTaskTimers  func(*task)
}

// finalizeTaskLifecycle closes a task and sends its exit wake. The first close wins (the closed flag makes it idempotent);
// the status derives from the override, the stop reason and the exit code; exitNotified flips to true only after the sender
// reports success.
func finalizeTaskLifecycle(t *task, exitCode *int, hooks lifecycleHooks, statusOverride, terminationReason string) *task {
	if !closeTaskLifecycle(t, exitCode, hooks, statusOverride, terminationReason) {
		return t
	}
	sendExitWakeLifecycle(t, hooks)
	hooks.refreshUI()
	return t
}

// closeTaskLifecycle is the terminal transition without the exit wake. It reports false when the task was already closed.
func closeTaskLifecycle(t *task, exitCode *int, hooks lifecycleHooks, statusOverride, terminationReason string) bool {
	if t.closed {
		return false
	}
	t.closed = true
	t.UpdatedAt = clockNow()
	t.ExitCode = exitCode
	hooks.clearTaskTimers(t)

	switch {
	case statusOverride != "":
		t.Status = statusOverride
	case t.stopReason == stopTimeout:
		t.Status = statusTimedOut
	case t.stopReason != "":
		t.Status = statusStopped
	case exitCode != nil && *exitCode == 0:
		t.Status = statusCompleted
	default:
		t.Status = statusFailed
	}
	t.TerminationReason = resolveTerminationReason(t, exitCode, terminationReason)
	hooks.rememberSnapshot(t)
	hooks.persistSnapshots()
	return true
}

func sendExitWakeLifecycle(t *task, hooks lifecycleHooks) {
	if !hooks.sendTaskEvent(eventExit, t) {
		return
	}
	t.ExitNotified = true
	hooks.rememberSnapshot(t)
	hooks.persistSnapshots()
}

// resolveTerminationReason: an explicit reason wins, then one the caller pre-stamped (requestStop stamps
// "extension-stop" before the signal), then a derivation from the stop reason and exit code; a null exit code with no stop
// is external (a signal we did not send).
func resolveTerminationReason(t *task, exitCode *int, explicit string) string {
	switch {
	case explicit != "":
		return explicit
	case t.TerminationReason != "":
		return t.TerminationReason
	case t.stopReason == stopTimeout:
		return termTimeout
	case t.stopReason == stopUser:
		return termExtensionStop
	case t.stopReason == stopShutdown:
		return termSessionShutdown
	case exitCode == nil:
		return termExternal
	}
	return termSelfExit
}

// replayMissedExitsLifecycle sends the exit wake of every task whose terminal transition never produced one.
func replayMissedExitsLifecycle(tasks []*task, hooks lifecycleHooks) {
	for _, t := range selectMissedExits(tasks) {
		sendExitWakeLifecycle(t, hooks)
	}
}
