package pi_goal_x

// ported from goal-task-count.ts

// taskSubtreeCounts mirrors TaskSubtreeCounts.
type taskCounts struct {
	total, complete, skipped, pending int
	// pendingTasks is filled only when collectPending is requested.
	pendingTasks []*jsObject
}

// taskCountOpts mirrors the options object of countTaskSubtree.
type taskCountOpts struct {
	doneIncludesSkipped bool
	collectPending      bool
}

// countTaskSubtree walks tasks (and subtasks) once. Pass at most one taskCountOpts.
func countTaskSubtree(tasks []any, opts ...taskCountOpts) taskCounts {
	var o taskCountOpts
	if len(opts) > 0 {
		o = opts[0]
	}
	var total, rawComplete, rawSkipped int
	var pendingTasks []*jsObject
	var walk func([]any)
	walk = func(list []any) {
		for _, x := range list {
			t, _ := x.(*jsObject)
			if t == nil {
				continue
			}
			total++
			switch gstr(t, "status") {
			case "complete":
				rawComplete++
			case "skipped":
				rawSkipped++
			default:
				if o.collectPending {
					pendingTasks = append(pendingTasks, t)
				}
			}
			if subs, ok := t.vals["subtasks"].([]any); ok && len(subs) > 0 {
				walk(subs)
			}
		}
	}
	walk(tasks)
	done := rawComplete
	if o.doneIncludesSkipped {
		done = rawComplete + rawSkipped
	}
	c := taskCounts{total: total, complete: done, skipped: rawSkipped, pending: total - done}
	if o.collectPending {
		c.pendingTasks = pendingTasks
		if c.pendingTasks == nil {
			c.pendingTasks = []*jsObject{}
		}
	}
	return c
}

// countAllTasks is the total node count only; nil tasks count 0.
func countAllTasks(tasks []any) int { return countTaskSubtree(tasks).total }
