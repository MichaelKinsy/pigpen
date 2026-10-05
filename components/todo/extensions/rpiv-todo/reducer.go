package rpiv_todo

import (
	"fmt"
	"maps"
	"slices"
)

func errorResult(state taskState, message string) applyResult {
	return applyResult{State: state, Op: op{Kind: "error", Message: message}}
}

func sameNumberList(a, b []float64) bool { return slices.Equal(a, b) }

// sameRecord compares metadata by JSON equality, the original's notion of "changed" (it round-trips
// through persistence). Both nil and both empty are the same; Go's DeepEqual distinguishes them, so the
// values are compared through their canonical form.
func sameRecord(a, b map[string]any) bool {
	if a == nil && b == nil {
		return true
	}
	return jsonString(a) == jsonString(b)
}

func samePtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// taskChanged is whether an update changed anything, so a no-effect update reads "No change" instead of
// "Updated #N". blockedBy is order-sensitive. upstream: state/state-reducer.ts:55-65.
func taskChanged(before, after task) bool {
	return before.Subject != after.Subject || before.Status != after.Status ||
		!samePtr(before.Description, after.Description) || !samePtr(before.ActiveForm, after.ActiveForm) ||
		!samePtr(before.Owner, after.Owner) || !sameNumberList(before.BlockedBy, after.BlockedBy) ||
		!sameRecord(before.Metadata, after.Metadata)
}

func findByNumber(tasks []task, id float64) int {
	return slices.IndexFunc(tasks, func(t task) bool { return float64(t.ID) == id })
}

// present is whether the argument was given: JSON null reads as absent, as the schema rejects it for
// every optional field and the original tests `!== undefined`.
func present(p params, key string) bool { return p[key] != nil }

// truthyString is `if (params.x)` for a string: given and not empty.
func truthyString(p params, key string) (string, bool) {
	s, ok := text(p, key)
	return s, ok && s != ""
}

func strPtr(s string) *string { return &s }

// applyTaskMutation is the pure reducer: (state, action, params) -> (state, op). Validation is in line:
// structural guards, then state-aware checks (transition legality, dangling or deleted blockedBy,
// self-block, cycles). upstream: state/state-reducer.ts:76-229.
func applyTaskMutation(state taskState, action string, p params) applyResult {
	switch action {
	case "create":
		return applyCreate(state, p)
	case "update":
		return applyUpdate(state, p)
	case "list":
		o := op{Kind: "list", IncludeDeleted: p["includeDeleted"] == true}
		if present(p, "status") {
			o.StatusFilter, _ = text(p, "status")
		}
		return applyResult{State: state, Op: o}
	case "get":
		id, ok := number(p["id"])
		if !ok {
			return errorResult(state, "id required for get")
		}
		idx := findByNumber(state.Tasks, id)
		if idx == -1 {
			return errorResult(state, fmt.Sprintf("#%s not found", jsNumber(id)))
		}
		return applyResult{State: state, Op: op{Kind: "get", Task: state.Tasks[idx]}}
	case "delete":
		id, ok := number(p["id"])
		if !ok {
			return errorResult(state, "id required for delete")
		}
		idx := findByNumber(state.Tasks, id)
		if idx == -1 {
			return errorResult(state, fmt.Sprintf("#%s not found", jsNumber(id)))
		}
		current := state.Tasks[idx]
		if current.Status == statusDeleted {
			return errorResult(state, fmt.Sprintf("#%d is already deleted", current.ID))
		}
		updated := current
		updated.Status = statusDeleted
		tasks := slices.Clone(state.Tasks)
		tasks[idx] = updated
		return applyResult{State: taskState{Tasks: tasks, NextID: state.NextID}, Op: op{Kind: "delete", ID: updated.ID, Subject: updated.Subject}}
	case "clear":
		return applyResult{State: taskState{Tasks: []task{}, NextID: 1}, Op: op{Kind: "clear", Count: len(state.Tasks)}}
	}
	return errorResult(state, fmt.Sprintf("unknown action %q", action))
}

func applyCreate(state taskState, p params) applyResult {
	subject, _ := text(p, "subject")
	if jsTrim(subject) == "" {
		return errorResult(state, "subject required for create")
	}
	deps, _ := numberList(p["blockedBy"])
	for _, dep := range deps {
		idx := findByNumber(state.Tasks, dep)
		if idx == -1 {
			return errorResult(state, fmt.Sprintf("blockedBy: #%s not found", jsNumber(dep)))
		}
		if state.Tasks[idx].Status == statusDeleted {
			return errorResult(state, fmt.Sprintf("blockedBy: #%s is deleted", jsNumber(dep)))
		}
	}
	created := task{ID: state.NextID, Subject: subject, Status: statusPending}
	if s, ok := truthyString(p, "description"); ok {
		created.Description = strPtr(s)
	}
	if s, ok := truthyString(p, "activeForm"); ok {
		created.ActiveForm = strPtr(s)
	}
	if len(deps) > 0 {
		created.BlockedBy = slices.Clone(deps)
	}
	if s, ok := truthyString(p, "owner"); ok {
		created.Owner = strPtr(s)
	}
	if m, ok := p["metadata"].(map[string]any); ok {
		created.Metadata = maps.Clone(m)
		if created.Metadata == nil {
			created.Metadata = map[string]any{}
		}
	}
	tasks := append(slices.Clone(state.Tasks), created)
	return applyResult{State: taskState{Tasks: tasks, NextID: state.NextID + 1}, Op: op{Kind: "create", TaskID: created.ID}}
}

func applyUpdate(state taskState, p params) applyResult {
	id, ok := number(p["id"])
	if !ok {
		return errorResult(state, "id required for update")
	}
	idx := findByNumber(state.Tasks, id)
	if idx == -1 {
		return errorResult(state, fmt.Sprintf("#%s not found", jsNumber(id)))
	}
	current := state.Tasks[idx]
	add, _ := numberList(p["addBlockedBy"])
	remove, _ := numberList(p["removeBlockedBy"])

	hasMutation := present(p, "subject") || present(p, "description") || present(p, "activeForm") ||
		present(p, "status") || present(p, "owner") || present(p, "metadata") || len(add) > 0 || len(remove) > 0
	if !hasMutation {
		return errorResult(state, "update requires at least one mutable field: subject, description, activeForm, status, owner, metadata, addBlockedBy, or removeBlockedBy")
	}

	newStatus := current.Status
	if present(p, "status") {
		to, _ := text(p, "status")
		if !isTransitionValid(current.Status, to) {
			return errorResult(state, fmt.Sprintf("illegal transition %s → %s", current.Status, to))
		}
		newStatus = to
	}

	newBlockedBy := slices.Clone(current.BlockedBy)
	if len(remove) > 0 {
		newBlockedBy = slices.DeleteFunc(newBlockedBy, func(dep float64) bool { return slices.Contains(remove, dep) })
	}
	if len(add) > 0 {
		for _, dep := range add {
			if dep == float64(current.ID) {
				return errorResult(state, fmt.Sprintf("cannot block #%d on itself", current.ID))
			}
			depIdx := findByNumber(state.Tasks, dep)
			if depIdx == -1 {
				return errorResult(state, fmt.Sprintf("addBlockedBy: #%s not found", jsNumber(dep)))
			}
			if state.Tasks[depIdx].Status == statusDeleted {
				return errorResult(state, fmt.Sprintf("addBlockedBy: #%s is deleted", jsNumber(dep)))
			}
			if !slices.Contains(newBlockedBy, dep) {
				newBlockedBy = append(newBlockedBy, dep)
			}
		}
		if detectCycle(state.Tasks, current.ID, newBlockedBy) {
			return errorResult(state, "addBlockedBy would create a cycle in the blockedBy graph")
		}
	}

	newMetadata := current.Metadata
	if m, ok := p["metadata"].(map[string]any); ok {
		merged := maps.Clone(current.Metadata)
		if merged == nil {
			merged = map[string]any{}
		}
		for k, v := range m {
			if v == nil {
				delete(merged, k)
			} else {
				merged[k] = v
			}
		}
		if len(merged) > 0 {
			newMetadata = merged
		} else {
			newMetadata = nil
		}
	}

	updated := current
	updated.Status = newStatus
	if s, ok := text(p, "subject"); ok {
		updated.Subject = s
	}
	if s, ok := text(p, "description"); ok {
		updated.Description = strPtr(s)
	}
	if s, ok := text(p, "activeForm"); ok {
		updated.ActiveForm = strPtr(s)
	}
	if s, ok := text(p, "owner"); ok {
		updated.Owner = strPtr(s)
	}
	if len(newBlockedBy) > 0 {
		updated.BlockedBy = newBlockedBy
	} else {
		updated.BlockedBy = nil
	}
	updated.Metadata = newMetadata

	tasks := slices.Clone(state.Tasks)
	tasks[idx] = updated
	return applyResult{
		State: taskState{Tasks: tasks, NextID: state.NextID},
		Op:    op{Kind: "update", ID: updated.ID, FromStatus: current.Status, ToStatus: newStatus, Changed: taskChanged(current, updated)},
	}
}

var validTransitions = map[string][]string{
	statusPending:    {statusInProgress, statusCompleted, statusDeleted},
	statusInProgress: {statusPending, statusCompleted, statusDeleted},
	statusCompleted:  {statusDeleted},
	statusDeleted:    {},
}

// isTransitionValid reports whether a status change is allowed: same to same is idempotent, completed is
// one-way to deleted and deleted is terminal. upstream: state/invariants.ts:8-19.
func isTransitionValid(from, to string) bool {
	if from == to {
		return true
	}
	return slices.Contains(validTransitions[from], to)
}

// detectCycle reports whether merging newBlockedBy into taskID's blockedBy set creates a cycle in the
// dependency graph. upstream: state/task-graph.ts:11-40.
func detectCycle(tasks []task, taskID int, newBlockedBy []float64) bool {
	edges := map[float64][]float64{}
	order := make([]float64, 0, len(tasks))
	for _, t := range tasks {
		id := float64(t.ID)
		order = append(order, id)
		if t.ID == taskID {
			merged := slices.Clone(t.BlockedBy)
			for _, d := range newBlockedBy {
				if !slices.Contains(merged, d) {
					merged = append(merged, d)
				}
			}
			edges[id] = merged
		} else {
			edges[id] = slices.Clone(t.BlockedBy)
		}
	}
	visiting := map[float64]bool{}
	visited := map[float64]bool{}
	var from func(node float64) bool
	from = func(node float64) bool {
		if visiting[node] {
			return true
		}
		if visited[node] {
			return false
		}
		visiting[node] = true
		for _, next := range edges[node] {
			if from(next) {
				return true
			}
		}
		delete(visiting, node)
		visited[node] = true
		return false
	}
	for _, node := range order {
		if from(node) {
			return true
		}
	}
	return false
}

// deriveBlocks inverts blockedBy: for each task, which tasks list it. upstream: state/task-graph.ts:48-57.
func deriveBlocks(tasks []task) map[int][]int {
	blocks := map[int][]int{}
	for _, t := range tasks {
		for _, dep := range t.BlockedBy {
			blocks[int(dep)] = append(blocks[int(dep)], t.ID)
		}
	}
	return blocks
}
