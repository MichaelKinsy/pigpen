package rpiv_todo

import "slices"

// isTaskDetails is the defensive discriminator for persisted details: entries from older or corrupt
// sessions are skipped silently. upstream: state/replay.ts:9-13.
func isTaskDetails(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	_, tasksOK := m["tasks"].([]any)
	_, nextOK := number(m["nextId"])
	return tasksOK && nextOK
}

// taskFromJSON decodes one persisted task; fields of the wrong type are left unset.
func taskFromJSON(m map[string]any) task {
	t := task{Status: statusPending}
	if id, ok := number(m["id"]); ok {
		t.ID = int(id)
	}
	t.Subject, _ = m["subject"].(string)
	if s, ok := m["status"].(string); ok {
		t.Status = s
	}
	if s, ok := m["description"].(string); ok {
		t.Description = strPtr(s)
	}
	if s, ok := m["activeForm"].(string); ok {
		t.ActiveForm = strPtr(s)
	}
	if s, ok := m["owner"].(string); ok {
		t.Owner = strPtr(s)
	}
	if deps, ok := numberList(m["blockedBy"]); ok && len(deps) > 0 {
		t.BlockedBy = slices.Clone(deps)
	}
	if md, ok := m["metadata"].(map[string]any); ok {
		t.Metadata = map[string]any{}
		for k, v := range md {
			t.Metadata[k] = v
		}
	}
	return t
}

// replayFromBranch walks the branch in order; the last toolResult of the `todo` tool whose details match
// the snapshot shape wins. With none it returns an empty state. It is pure of the store.
// upstream: state/replay.ts:24-38.
func replayFromBranch(entries []map[string]any) taskState {
	result := freshState()
	for _, entry := range entries {
		if entry["type"] != "message" {
			continue
		}
		msg, _ := entry["message"].(map[string]any)
		if msg == nil || msg["role"] != "toolResult" || msg["toolName"] != toolName {
			continue
		}
		if !isTaskDetails(msg["details"]) {
			continue
		}
		d := msg["details"].(map[string]any)
		raw := d["tasks"].([]any)
		tasks := make([]task, 0, len(raw))
		for _, r := range raw {
			if m, ok := r.(map[string]any); ok {
				tasks = append(tasks, taskFromJSON(m))
			}
		}
		next, _ := number(d["nextId"])
		result = taskState{Tasks: tasks, NextID: int(next)}
	}
	return result
}
