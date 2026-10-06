package tintinweb_tasks

// Turn-based auto-clearing of completed tasks. upstream: auto-clear.ts.
//
// on_task_complete: each completed task gets its own countdown and is deleted individually.
// on_list_complete: the countdown starts when ALL tasks are completed, and clears them as a batch.
// Both countdowns are measured in turns and tick only at turn_start, so they stop the moment the agent does;
// startNewBatch covers a run that ends right after its last completion, using the run boundary armed by
// onRunEnded: work added within the run that completed the list belongs to it, work added after it ended does not.
type autoClearManager struct {
	getStore func() *taskStore
	getMode  func() string
	delay    int // how many turns completed tasks linger before auto-clearing

	completedAtTurn    map[string]int
	completedOrder     []string
	allCompletedAtTurn *int
	runEnded           bool
}

func newAutoClearManager(getStore func() *taskStore, getMode func() string, clearDelayTurns int) *autoClearManager {
	return &autoClearManager{getStore: getStore, getMode: getMode, delay: clearDelayTurns, completedAtTurn: map[string]int{}}
}

// trackCompletion records a task completion; call it AFTER the cascade logic.
func (m *autoClearManager) trackCompletion(taskID string, currentTurn int) {
	switch m.getMode() {
	case "on_task_complete":
		if _, ok := m.completedAtTurn[taskID]; !ok {
			m.completedOrder = append(m.completedOrder, taskID)
		}
		m.completedAtTurn[taskID] = currentTurn
	case "on_list_complete":
		m.checkAllCompleted(currentTurn)
	}
}

func allCompleted(tasks []*task) bool {
	if len(tasks) == 0 {
		return false
	}
	for _, t := range tasks {
		if t.Status != statusCompleted {
			return false
		}
	}
	return true
}

// checkAllCompleted starts or resets the batch countdown.
func (m *autoClearManager) checkAllCompleted(currentTurn int) {
	if allCompleted(m.getStore().list(nil)) {
		if m.allCompletedAtTurn == nil {
			m.allCompletedAtTurn = &currentTurn
		}
	} else {
		m.allCompletedAtTurn = nil
	}
}

// resetBatchCountdown resets the countdown (a new task, or a task going non-completed).
func (m *autoClearManager) resetBatchCountdown() { m.allCompletedAtTurn = nil }

// onRunEnded notes that no retry, compaction or queued continuation is coming: the list as it stands is this
// run's final one. It is also true of a list carried into a resumed or forked session.
func (m *autoClearManager) onRunEnded() { m.runEnded = true }

// startNewBatch is called as a task is about to be created. If a run has ended since the list was last added
// to and nothing is left to do on it, the list belongs to the batch before this one: retire it. A list with
// unfinished work, and one the agent is still building inside the same run, are left alone.
func (m *autoClearManager) startNewBatch() {
	m.allCompletedAtTurn = nil
	afterFinishedRun := m.runEnded
	m.runEnded = false
	if !afterFinishedRun || m.getMode() == "never" {
		return
	}
	if allCompleted(m.getStore().list(nil)) {
		m.getStore().clearCompleted()
		m.completedAtTurn, m.completedOrder = map[string]int{}, nil
	}
}

// reset drops all tracking state (a new session).
func (m *autoClearManager) reset() {
	m.completedAtTurn, m.completedOrder = map[string]int{}, nil
	m.allCompletedAtTurn = nil
	m.runEnded = false
}

// onTurnStart deletes the tasks whose linger period expired and reports whether any were cleared.
func (m *autoClearManager) onTurnStart(currentTurn int) bool {
	cleared := false
	switch mode := m.getMode(); {
	case mode == "on_task_complete":
		kept := m.completedOrder[:0:0]
		for _, id := range m.completedOrder {
			turn := m.completedAtTurn[id]
			t := m.getStore().get(id)
			switch {
			case t == nil || t.Status != statusCompleted: // deleted or reverted: drop the stale entry
				delete(m.completedAtTurn, id)
			case currentTurn-turn >= m.delay:
				m.getStore().delete(id)
				delete(m.completedAtTurn, id)
				cleared = true
			default:
				kept = append(kept, id)
			}
		}
		m.completedOrder = kept
	case mode == "on_list_complete" && m.allCompletedAtTurn != nil:
		if currentTurn-*m.allCompletedAtTurn >= m.delay {
			m.getStore().clearCompleted()
			m.allCompletedAtTurn = nil
			cleared = true
		}
	}
	return cleared
}
