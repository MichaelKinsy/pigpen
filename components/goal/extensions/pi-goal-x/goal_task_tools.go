package pi_goal_x

import (
	"fmt"
)

// ported from goal-task-tools.ts. The task-tool support: flat parent-linked `set_goal_tasks` input -> recursive task tree (with the
// validation the recursive path enforced) and id-stable merging that preserves progress for matching ids. Tasks are []any of
// *jsObject, like deriveTasksFromObjective.

// maxGoalTasks is MAX_TASKS.
const maxGoalTasks = 50

// flatTaskParent is the trimmed parent_id of a flat item, "" when it has none.
func flatTaskParent(item any) string {
	p, _ := toolItemString(item, "parent_id")
	return jsTrim(p)
}

// flatTaskID is the trimmed id of a flat item ("" when it is not a string).
func flatTaskID(item any) string {
	id, _ := toolItemString(item, "id")
	return jsTrim(id)
}

// convertFlatTasks converts a flat parent-linked task list into the recursive task tree, validating non-empty unique ids and
// titles, existing parents, acyclic relationships, at most 50 tasks, the subtask depth within maxSubtaskDepth, and
// lightweight_subtasks only on tasks that have children. errMsg is "" on success.
func convertFlatTasks(flat []any, maxSubtaskDepth int) (tasks []any, errMsg string) {
	if len(flat) > maxGoalTasks {
		return nil, fmt.Sprintf("Task list cannot exceed %d tasks.", maxGoalTasks)
	}
	ids := map[string]bool{}
	for _, item := range flat {
		id := flatTaskID(item)
		if id == "" {
			return nil, "All tasks must have a non-empty id."
		}
		if ids[id] {
			return nil, fmt.Sprintf("Duplicate task id: \"%s\".", id)
		}
		ids[id] = true
		title, _ := toolItemString(item, "title")
		if jsTrim(title) == "" {
			return nil, fmt.Sprintf("Task \"%s\" must have a non-empty title.", id)
		}
	}

	// Parent must exist and relationships must be acyclic.
	byID := map[string]any{}
	for _, item := range flat {
		byID[flatTaskID(item)] = item
	}
	for _, item := range flat {
		parentID := flatTaskParent(item)
		if parentID != "" {
			if _, ok := byID[parentID]; !ok {
				return nil, fmt.Sprintf("Task \"%s\" references missing parent \"%s\".", flatTaskID(item), parentID)
			}
			// Walk up; if we return to the node itself, there is a cycle.
			cursor := byID[parentID]
			seen := map[string]bool{flatTaskID(item): true}
			for cursor != nil {
				if seen[flatTaskID(cursor)] {
					return nil, fmt.Sprintf("Cyclic parent relationship involving task \"%s\".", flatTaskID(cursor))
				}
				seen[flatTaskID(cursor)] = true
				if up := flatTaskParent(cursor); up != "" {
					cursor = byID[up]
				} else {
					cursor = nil
				}
			}
		}
	}

	// Build the tree.
	childrenOf := map[string][]any{}
	var roots []any
	for _, item := range flat {
		if parentID := flatTaskParent(item); parentID != "" {
			childrenOf[parentID] = append(childrenOf[parentID], item)
		} else {
			roots = append(roots, item)
		}
	}
	var buildNode func(item any) *jsObject
	buildNode = func(item any) *jsObject {
		node := newObject()
		node.set("id", flatTaskID(item))
		title, _ := toolItemString(item, "title")
		node.set("title", jsTrim(title))
		node.set("status", "pending")
		if vc, _ := toolItemString(item, "verification_contract"); jsTrim(vc) != "" {
			node.set("verificationContract", jsTrim(vc))
		} else {
			node.set("verificationContract", undef{})
		}
		if lw, _ := toolItemField(item, "lightweight_subtasks"); lw == true {
			node.set("lightweightSubtasks", true)
		} else {
			node.set("lightweightSubtasks", undef{})
		}
		if children := childrenOf[gstr(node, "id")]; len(children) > 0 {
			subs := make([]any, len(children))
			for i, child := range children {
				subs[i] = buildNode(child)
			}
			node.set("subtasks", subs)
		}
		return node
	}
	tasks = make([]any, len(roots))
	for i, r := range roots {
		tasks[i] = buildNode(r)
	}

	// Lightweight placement: lightweight_subtasks must be on a task with children.
	for _, item := range flat {
		if lw, _ := toolItemField(item, "lightweight_subtasks"); lw == true {
			if len(childrenOf[flatTaskID(item)]) == 0 {
				return nil, fmt.Sprintf("Task \"%s\" sets lightweight_subtasks but has no subtasks.", flatTaskID(item))
			}
		}
	}

	if v := findSubtaskDepthViolation(tasks, maxSubtaskDepth); v != "" {
		return nil, v
	}
	return tasks, ""
}

// mergeTasksWithExisting merges converted tasks into an existing tree. Matching ids preserve runtime progress ONLY (status, evidence,
// completion/skip timestamps, skip reason); incoming structural fields are authoritative and omission clears them.
func mergeTasksWithExisting(existing, incoming []any) []any {
	existingByID := map[string]*jsObject{}
	var index func(tasks []any)
	index = func(tasks []any) {
		for _, t := range tasks {
			o, ok := t.(*jsObject)
			if !ok {
				continue
			}
			existingByID[gstr(o, "id")] = o
			if subs, ok := o.vals["subtasks"].([]any); ok {
				index(subs)
			}
		}
	}
	index(existing)

	prop := func(o *jsObject, k string) any {
		if o != nil && o.has(k) {
			return o.vals[k]
		}
		return undef{}
	}
	var mergeTask func(input *jsObject) *jsObject
	mergeTask = func(input *jsObject) *jsObject {
		prior := existingByID[gstr(input, "id")]
		base := newObject()
		base.set("id", gstr(input, "id"))
		base.set("title", gstr(input, "title"))
		// Structural fields are authoritative; undefined (omitted) clears.
		base.set("verificationContract", prop(input, "verificationContract"))
		base.set("lightweightSubtasks", prop(input, "lightweightSubtasks"))
		if prior != nil {
			base.set("status", prop(prior, "status"))
			base.set("evidence", prop(prior, "evidence"))
			base.set("completedAt", prop(prior, "completedAt"))
			base.set("skippedAt", prop(prior, "skippedAt"))
			base.set("skipReason", prop(prior, "skipReason"))
		} else {
			base.set("status", "pending")
		}
		if subs, ok := input.vals["subtasks"].([]any); ok && len(subs) > 0 {
			out := make([]any, len(subs))
			for i, child := range subs {
				out[i] = mergeTask(child.(*jsObject))
			}
			base.set("subtasks", out)
		}
		return base
	}
	out := make([]any, len(incoming))
	for i, t := range incoming {
		out[i] = mergeTask(t.(*jsObject))
	}
	return out
}

// countTasks counts every node in a task tree (roots + all descendants).
func countTasks(tasks []any) int {
	total := 0
	for _, t := range tasks {
		total++
		if o, ok := t.(*jsObject); ok {
			if subs, ok := o.vals["subtasks"].([]any); ok {
				total += countTasks(subs)
			}
		}
	}
	return total
}
