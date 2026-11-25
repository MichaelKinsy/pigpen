package todo

import "encoding/json"

// TodoPhasesFingerprint returns omp's canonical JSON fingerprint for phases
// (phase name + each task's content and status, in order).
// upstream: packages/coding-agent/src/tools/todo.ts:169-176
func TodoPhasesFingerprint(phases []TodoPhase) string {
	type fpTask struct {
		Content string     `json:"content"`
		Status  TodoStatus `json:"status"`
	}
	type fpPhase struct {
		Name  string   `json:"name"`
		Tasks []fpTask `json:"tasks"`
	}
	out := make([]fpPhase, len(phases))
	for i, p := range phases {
		tasks := make([]fpTask, len(p.Tasks))
		for j, t := range p.Tasks {
			tasks[j] = fpTask{Content: t.Content, Status: t.Status}
		}
		out[i] = fpPhase{Name: p.Name, Tasks: tasks}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "[]"
	}
	return string(b)
}

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
			if !ok {
				return nil, false
			}
			// Snapshots saved before `blocked` was removed read back as `pending`.
			// upstream: packages/coding-agent/src/tools/todo.ts:179-189
			if statusStr == "blocked" {
				statusStr = string(StatusPending)
			}
			if !IsValidStatus(statusStr) {
				return nil, false
			}
			var notes []string
			if rawNotes, ok := tm["notes"].([]any); ok {
				for _, rn := range rawNotes {
					if s, ok := rn.(string); ok {
						notes = append(notes, s)
					}
				}
			}
			tasks = append(tasks, TodoItem{
				Content: content,
				Status:  TodoStatus(statusStr),
				Notes:   notes,
			})
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

// GetLatestTodoSnapshotIdentity identifies the latest durable canonical todo
// snapshot on the active branch.
// upstream: packages/coding-agent/src/tools/todo.ts:208-221
func GetLatestTodoSnapshotIdentity(entries []map[string]any) (TodoSnapshotIdentity, bool) {
	for i := len(entries) - 1; i >= 0; i-- {
		phases, ok := canonicalTodoPhases(entries[i])
		if !ok {
			continue
		}
		sourceID, _ := entries[i]["id"].(string)
		if sourceID == "" {
			return TodoSnapshotIdentity{}, false
		}
		return TodoSnapshotIdentity{
			SourceEntryID: sourceID,
			Fingerprint:   TodoPhasesFingerprint(phases),
		}, true
	}
	return TodoSnapshotIdentity{}, false
}

// GetTodoHudVisibility returns the persisted HUD visibility ("dismissed" or
// "revealed") only when it targets the current canonical snapshot exactly.
// upstream: packages/coding-agent/src/tools/todo.ts:224-243
func GetTodoHudVisibility(entries []map[string]any, phases []TodoPhase) (TodoHudVisibility, bool) {
	snapshot, ok := GetLatestTodoSnapshotIdentity(entries)
	if !ok || snapshot.Fingerprint != TodoPhasesFingerprint(phases) {
		return "", false
	}
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		if t, _ := entry["type"].(string); t != "custom" {
			continue
		}
		if ct, _ := entry["customType"].(string); ct != TodoHudStateCustomType {
			continue
		}
		data, _ := entry["data"].(map[string]any)
		if data == nil {
			continue
		}
		srcID, _ := data["sourceEntryId"].(string)
		fp, _ := data["fingerprint"].(string)
		vis, _ := data["visibility"].(string)
		if srcID == snapshot.SourceEntryID && fp == snapshot.Fingerprint &&
			(vis == string(HudVisibilityDismissed) || vis == string(HudVisibilityRevealed)) {
			return TodoHudVisibility(vis), true
		}
	}
	return "", false
}

// CreateTodoHudStateData builds persisted HUD metadata only when phases match
// the latest durable canonical snapshot on entries.
// upstream: packages/coding-agent/src/tools/todo.ts:246-254
func CreateTodoHudStateData(entries []map[string]any, phases []TodoPhase, visibility TodoHudVisibility) (TodoHudStateEntryData, bool) {
	snapshot, ok := GetLatestTodoSnapshotIdentity(entries)
	if !ok || snapshot.Fingerprint != TodoPhasesFingerprint(phases) {
		return TodoHudStateEntryData{}, false
	}
	return TodoHudStateEntryData{
		SourceEntryID: snapshot.SourceEntryID,
		Fingerprint:   snapshot.Fingerprint,
		Visibility:    visibility,
	}, true
}
