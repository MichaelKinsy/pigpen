package pi_goal_x

// ported from goal-task-index.ts

// goalTaskRow is one entry of goalTaskIndex.ordered; parentId "" is a root task.
type goalTaskRow struct {
	task     *jsObject
	depth    int
	parentId string
}

// goalTaskIndex is TaskIndex: an immutable snapshot with derived lookups.
type goalTaskIndex struct {
	tasks    []any
	byId     map[string]*jsObject
	ordered  []goalTaskRow
	pending  []*jsObject
	complete int
	skipped  int
}

type taskIndexEntry struct {
	index *goalTaskIndex
	chars int
}

var (
	taskIndexCache  []taskIndexEntry
	taskIndexChars  int
	taskIndexFields = []string{"id", "title", "status", "completedAt", "skippedAt", "evidence", "skipReason", "verificationContract", "lightweightSubtasks"}
)

func taskFieldValue(t *jsObject, k string) any {
	v, ok := t.vals[k]
	if !ok {
		return undef{}
	}
	return v
}

// sameTasks compares the indexed fields without serializing long titles/contracts.
func sameTasks(left, right []any) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		a, _ := left[i].(*jsObject)
		b, _ := right[i].(*jsObject)
		if a == nil || b == nil {
			if a != b {
				return false
			}
			continue
		}
		for _, f := range taskIndexFields {
			if taskFieldValue(a, f) != taskFieldValue(b, f) {
				return false
			}
		}
		as, aok := a.vals["subtasks"].([]any)
		bs, bok := b.vals["subtasks"].([]any)
		if aok || bok {
			if !aok || !bok || !sameTasks(as, bs) {
				return false
			}
		}
	}
	return true
}

// taskIndex is content validated: equal inputs reuse the cached snapshot; mutable input never aliases it.
func taskIndex(tasks []any) *goalTaskIndex {
	for i := len(taskIndexCache) - 1; i >= 0; i-- {
		if sameTasks(tasks, taskIndexCache[i].index.tasks) {
			return taskIndexCache[i].index
		}
	}
	chars := 0
	var copyList func([]any) []any
	copyList = func(list []any) []any {
		out := make([]any, len(list))
		for i, x := range list {
			t, _ := x.(*jsObject)
			if t == nil {
				out[i] = x
				continue
			}
			for _, f := range taskIndexFields {
				if s, ok := t.vals[f].(string); ok {
					chars += utf16Len(s)
				}
			}
			c := t.clone()
			if subs, ok := t.vals["subtasks"].([]any); ok {
				c.set("subtasks", copyList(subs))
			}
			out[i] = c
		}
		return out
	}
	snapshot := copyList(tasks)
	res := &goalTaskIndex{tasks: snapshot, byId: map[string]*jsObject{}}
	var walk func([]any, int, string)
	walk = func(list []any, depth int, parent string) {
		for _, x := range list {
			t, _ := x.(*jsObject)
			if t == nil {
				continue
			}
			res.byId[gstr(t, "id")] = t
			res.ordered = append(res.ordered, goalTaskRow{t, depth, parent})
			switch gstr(t, "status") {
			case "pending":
				res.pending = append(res.pending, t)
			case "complete":
				res.complete++
			default:
				res.skipped++
			}
			if subs, ok := t.vals["subtasks"].([]any); ok {
				walk(subs, depth+1, gstr(t, "id"))
			}
		}
	}
	walk(snapshot, 0, "")
	// Bound retained text as well as entry count; oversized plans stay fully supported.
	if chars <= 2_000_000 {
		for len(taskIndexCache) >= 32 || taskIndexChars+chars > 2_000_000 {
			taskIndexChars -= taskIndexCache[0].chars
			taskIndexCache = taskIndexCache[1:]
		}
		taskIndexCache = append(taskIndexCache, taskIndexEntry{res, chars})
		taskIndexChars += chars
	}
	return res
}
