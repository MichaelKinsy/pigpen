package rpiv_todo

// Pure derivations of the task state. upstream: state/selectors.ts.

type tasksByStatus struct{ Pending, InProgress, Completed []task }

type todoCounts struct{ Total, Pending, InProgress, Completed int }

// overlayLayout is the overlay's row decision: the visible slice plus the overflow summary parts.
type overlayLayout struct {
	Visible         []task
	HiddenCompleted int
	TruncatedTail   int
}

func filterTasks(tasks []task, keep func(task) bool) []task {
	out := []task{}
	for _, t := range tasks {
		if keep(t) {
			out = append(out, t)
		}
	}
	return out
}

// selectVisibleTasks is the tasks without deleted tombstones.
func selectVisibleTasks(s taskState) []task {
	return filterTasks(s.Tasks, func(t task) bool { return t.Status != statusDeleted })
}

// selectTasksByStatus groups the visible tasks by status.
func selectTasksByStatus(s taskState) tasksByStatus {
	visible := selectVisibleTasks(s)
	by := func(status string) []task {
		return filterTasks(visible, func(t task) bool { return t.Status == status })
	}
	return tasksByStatus{Pending: by(statusPending), InProgress: by(statusInProgress), Completed: by(statusCompleted)}
}

// selectTodoCounts counts the visible tasks for the heading and the /todos header.
func selectTodoCounts(s taskState) todoCounts {
	g := selectTasksByStatus(s)
	return todoCounts{
		Total:      len(g.Pending) + len(g.InProgress) + len(g.Completed),
		Pending:    len(g.Pending),
		InProgress: len(g.InProgress),
		Completed:  len(g.Completed),
	}
}

// selectShowTaskIDs is whether any visible task has a blockedBy reference: the #id prefix needs an anchor.
func selectShowTaskIDs(s taskState) bool {
	for _, t := range selectVisibleTasks(s) {
		if len(t.BlockedBy) > 0 {
			return true
		}
	}
	return false
}

// selectTaskSubjectByID is the subject of a task for renderCall's accent label.
func selectTaskSubjectByID(s taskState, id float64) (string, bool) {
	if i := findByNumber(s.Tasks, id); i != -1 {
		return s.Tasks[i].Subject, true
	}
	return "", false
}

// selectOverlayLayout is the "drop completed first, then truncate the non-completed tail" rule. budget is
// the body slot count; on overflow one more slot is reserved for the summary row.
func selectOverlayLayout(s taskState, budget int) overlayLayout {
	all := selectVisibleTasks(s)
	if len(all) <= budget {
		return overlayLayout{Visible: all}
	}
	inner := budget - 1
	nonCompleted := filterTasks(all, func(t task) bool { return t.Status != statusCompleted })
	totalCompleted := len(all) - len(nonCompleted)
	if len(nonCompleted) <= inner {
		kept := map[int]bool{}
		for _, t := range nonCompleted {
			kept[t.ID] = true
		}
		for _, t := range all {
			if len(kept) >= inner {
				break
			}
			if t.Status == statusCompleted {
				kept[t.ID] = true
			}
		}
		visible := filterTasks(all, func(t task) bool { return kept[t.ID] })
		shown := len(filterTasks(visible, func(t task) bool { return t.Status == statusCompleted }))
		return overlayLayout{Visible: visible, HiddenCompleted: totalCompleted - shown}
	}
	if inner < 0 {
		inner = 0
	}
	return overlayLayout{Visible: nonCompleted[:inner], HiddenCompleted: totalCompleted, TruncatedTail: len(nonCompleted) - inner}
}

// selectHasActive is whether any visible task is pending or in progress (the heading icon).
func selectHasActive(s taskState) bool {
	for _, t := range selectVisibleTasks(s) {
		if t.Status == statusPending || t.Status == statusInProgress {
			return true
		}
	}
	return false
}
