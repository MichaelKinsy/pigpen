package todo

// decodePhases defensively decodes a persisted JSON phases array.
// Returns (phases, true) only when every phase and task matches omp's schema.
// upstream: packages/coding-agent/src/tools/todo.ts:21-33
func decodePhases(v any) ([]TodoPhase, bool) {
	rawPhases, ok := v.([]any)
	if !ok {
		return nil, false
	}
	phases := make([]TodoPhase, 0, len(rawPhases))
	for _, rp := range rawPhases {
		pm, ok := rp.(map[string]any)
		if !ok {
			return nil, false
		}
		name, ok := pm["name"].(string)
		if !ok {
			return nil, false
		}
		rawTasks, ok := pm["tasks"].([]any)
		if !ok {
			return nil, false
		}
		tasks := make([]TodoItem, 0, len(rawTasks))
		for _, rt := range rawTasks {
			tm, ok := rt.(map[string]any)
			if !ok {
				return nil, false
			}
			content, ok := tm["content"].(string)
			if !ok {
				return nil, false
			}
			statusStr, ok := tm["status"].(string)
			if !ok || !IsValidStatus(statusStr) {
				return nil, false
			}
			item := TodoItem{
				Content: content,
				Status:  TodoStatus(statusStr),
			}
			if blocker, ok := tm["blocker"].(string); ok && blocker != "" {
				item.Blocker = blocker
			}
			tasks = append(tasks, item)
		}
		phases = append(phases, TodoPhase{Name: name, Tasks: tasks})
	}
	return phases, true
}

// canonicalTodoPhases extracts a durable todo snapshot from a single session entry,
// ignoring error results and read-only `view` calls.
// upstream: packages/coding-agent/src/tools/todo.ts:185-201
func canonicalTodoPhases(entry map[string]any) ([]TodoPhase, bool) {
	entryType, _ := entry["type"].(string)
	if entryType == "custom" {
		customType, _ := entry["customType"].(string)
		if customType != UserTodoEditCustomType {
			return nil, false
		}
		data, _ := entry["data"].(map[string]any)
		if data == nil {
			return nil, false
		}
		return decodePhases(data["phases"])
	}
	if entryType != "message" {
		return nil, false
	}
	msg, _ := entry["message"].(map[string]any)
	if msg == nil {
		return nil, false
	}
	role, _ := msg["role"].(string)
	toolName, _ := msg["toolName"].(string)
	isErr, _ := msg["isError"].(bool)
	if role != "toolResult" || toolName != "todo" || isErr {
		return nil, false
	}
	details, _ := msg["details"].(map[string]any)
	if details == nil {
		return nil, false
	}
	if op, _ := details["op"].(string); op == string(OpView) {
		return nil, false
	}
	return decodePhases(details["phases"])
}

// GetLatestTodoPhasesFromEntries scans session branch entries from newest to oldest
// and returns a defensive clone of the latest canonical todo snapshot.
// upstream: packages/coding-agent/src/tools/todo.ts:252-258
func GetLatestTodoPhasesFromEntries(entries []map[string]any) []TodoPhase {
	for i := len(entries) - 1; i >= 0; i-- {
		if phases, ok := canonicalTodoPhases(entries[i]); ok {
			return ClonePhases(phases)
		}
	}
	return nil
}
