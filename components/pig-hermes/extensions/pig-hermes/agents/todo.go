package agents

import (
	"encoding/json"
	"fmt"
	"strings"
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/events"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/todo"
)

const (
	// MaxTodoItems caps the number of todo items per session (matches upstream todo_tool.py).
	MaxTodoItems = 256
	// MaxTodoContentChars caps per-item content length (matches upstream todo_tool.py).
	MaxTodoContentChars = 4000
	todoTruncationMark  = "… [truncated]"

	// TodoInjectionHeader is the stable marker prepended when re-injecting todos after compaction.
	TodoInjectionHeader = "[Your active task list was preserved across context compression]"
)

// TodoLegacyAliases lists pre-rename tool names that map to todo_list.
// Legacy alias "todo" is preserved for transcript/history compatibility; only "todo_list" is registered.
var TodoLegacyAliases = []string{"todo"}

// IsTodoToolName reports whether name is todo_list or a documented legacy alias ("todo").
func IsTodoToolName(name string) bool {
	if name == "todo_list" {
		return true
	}
	for _, alias := range TodoLegacyAliases {
		if name == alias {
			return true
		}
	}
	return false
}

func registerTodoTool(ext *sdk.Extension) {
	ext.RegisterTool(sdk.ToolDefinition{
		Name:          "todo_list",
		Label:         "Todo List",
		Description:   "Read or update the session task checklist. Omit 'todos' to read the current list. Pass 'todos' to replace (or set merge=true to merge updates by id). Legacy alias: 'todo'.",
		PromptSnippet: "Manage structured multi-step task checklists with todo_list (read, replace, or merge by id).",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"todos": map[string]any{
					"type":        "array",
					"description": "Task items to write. Omit to read the current list without modifying it.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"id": map[string]any{
								"type":        "string",
								"description": "Unique identifier for the task item.",
							},
							"content": map[string]any{
								"type":        "string",
								"description": "Short description of the task.",
							},
							"status": map[string]any{
								"type":        "string",
								"enum":        []string{"pending", "in_progress", "completed", "cancelled"},
								"description": "Task status: pending, in_progress, completed, or cancelled.",
							},
						},
						"required": []string{"id", "content", "status"},
					},
				},
				"merge": map[string]any{
					"type":        "boolean",
					"description": "When true, merge provided todos into the existing list by id instead of replacing the entire list.",
				},
			},
		},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			sessionID := resolveSessionID(ctx)
			res, mutated, items := executeTodo(todo.Default(), sessionID, params)
			if mutated {
				_ = ctx.AppendEntry("hermes_todo", map[string]any{
					"session_id": sessionID,
					"todos":      items,
				})
				_ = ctx.Events().Emit(events.ChannelTodoUpdated, map[string]any{
					"sessionId": sessionID,
					"todos":     items,
					"count":     len(items),
				})
			}
			return res, nil
		},
	})

	ext.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		restoreTodosFromSession(ctx, todo.Default())
		return nil, nil
	})

	ext.OnEvent(sdk.EventSessionCompact, func(ctx sdk.Context, _ map[string]any) (any, error) {
		sessionID := resolveSessionID(ctx)
		msg := BuildCompactionTodoInjection(todo.Default(), sessionID)
		if msg == "" {
			return nil, nil
		}
		trigger := false
		_ = ctx.SendMessage("hermes_todo_preserved", msg, false, sdk.SendMessageOptions{
			TriggerTurn: &trigger,
			DeliverAs:   "nextTurn",
		})
		return nil, nil
	})
}

// restoreTodosFromSession scans session entries (best-effort, safe on zero-value context)
// for the latest "hermes_todo" custom entry and restores its items into store.
func restoreTodosFromSession(ctx sdk.Context, store *todo.Store) {
	if store == nil {
		return
	}
	entries := safeSessionEntries(ctx)
	if len(entries) == 0 {
		return
	}
	sessionID := resolveSessionID(ctx)
	restoreTodosFromEntries(entries, sessionID, store)
}

func safeSessionEntries(ctx sdk.Context) (entries []json.RawMessage) {
	defer func() {
		if recover() != nil {
			entries = nil
		}
	}()
	raw, err := ctx.GetEntries()
	if err != nil {
		return nil
	}
	return raw
}

func restoreTodosFromEntries(entries []json.RawMessage, sessionID string, store *todo.Store) bool {
	if store == nil || len(entries) == 0 {
		return false
	}
	type todoEntryData struct {
		SessionID string      `json:"session_id"`
		Todos     []todo.Item `json:"todos"`
	}
	type customEntry struct {
		Type       string          `json:"type"`
		CustomType string          `json:"customType"`
		Data       json.RawMessage `json:"data"`
	}
	var (
		found   bool
		latest  []todo.Item
		entryID string
	)
	for _, raw := range entries {
		var ce customEntry
		if err := json.Unmarshal(raw, &ce); err != nil {
			continue
		}
		if ce.Type != "custom" || ce.CustomType != "hermes_todo" || len(ce.Data) == 0 {
			continue
		}
		var data todoEntryData
		if err := json.Unmarshal(ce.Data, &data); err != nil {
			continue
		}
		found = true
		latest = data.Todos
		if strings.TrimSpace(data.SessionID) != "" {
			entryID = strings.TrimSpace(data.SessionID)
		}
	}
	if !found {
		return false
	}
	targetSession := strings.TrimSpace(sessionID)
	if targetSession == "" || targetSession == "default" {
		if entryID != "" {
			targetSession = entryID
		}
	}
	if targetSession == "" {
		targetSession = "default"
	}
	sanitized := make([]todo.Item, 0, len(latest))
	for _, it := range latest {
		sanitized = append(sanitized, sanitizeTodoItem(it))
	}
	if len(sanitized) > MaxTodoItems {
		sanitized = sanitized[:MaxTodoItems]
	}
	_, err := store.Replace(targetSession, sanitized)
	return err == nil
}

// BuildCompactionTodoInjection returns the post-compaction reminder message if the
// session has at least one active (pending or in_progress) todo item, or "" otherwise.
func BuildCompactionTodoInjection(store *todo.Store, sessionID string) string {
	if store == nil {
		return ""
	}
	items := store.Get(sessionID)
	if len(items) == 0 {
		return ""
	}
	hasActive := false
	for _, it := range items {
		if it.Status == "pending" || it.Status == "in_progress" {
			hasActive = true
			break
		}
	}
	if !hasActive {
		return ""
	}
	return TodoInjectionHeader + "\n" + todo.FormatSummary(items)
}

func executeTodo(store *todo.Store, sessionID string, params map[string]any) (sdk.ToolResult, bool, []todo.Item) {
	if store == nil {
		store = todo.Default()
	}

	rawTodos, hasTodos := params["todos"]
	if !hasTodos || rawTodos == nil {
		items := store.Get(sessionID)
		return jsonResult(map[string]any{
			"todos":   items,
			"count":   len(items),
			"summary": todo.FormatSummary(items),
		}, false), false, items
	}

	incoming, err := parseTodoItems(rawTodos)
	if err != nil {
		return errorResult(err.Error()), false, nil
	}

	merge, _ := paramBool(params, "merge")
	target := incoming
	if merge {
		target = mergeTodoItems(store.Get(sessionID), incoming)
	}

	if len(target) > MaxTodoItems {
		return errorResult(fmt.Sprintf("todo list exceeds maximum of %d items (got %d)", MaxTodoItems, len(target))), false, nil
	}

	updated, err := store.Replace(sessionID, target)
	if err != nil {
		return errorResult(err.Error()), false, nil
	}

	return jsonResult(map[string]any{
		"todos":   updated,
		"count":   len(updated),
		"summary": todo.FormatSummary(updated),
	}, false), true, updated
}

func parseTodoItems(raw any) ([]todo.Item, error) {
	switch v := raw.(type) {
	case []todo.Item:
		out := make([]todo.Item, len(v))
		for i, it := range v {
			out[i] = sanitizeTodoItem(it)
		}
		return out, nil
	case []any:
		out := make([]todo.Item, 0, len(v))
		for i, elem := range v {
			m, ok := elem.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("todo item at index %d must be an object", i)
			}
			it := todo.Item{
				ID:      paramString(m, "id"),
				Content: paramString(m, "content"),
				Status:  paramString(m, "status"),
			}
			out = append(out, sanitizeTodoItem(it))
		}
		return out, nil
	default:
		return nil, fmt.Errorf("todos must be an array of {id, content, status} objects")
	}
}

func sanitizeTodoItem(it todo.Item) todo.Item {
	content := strings.TrimSpace(it.Content)
	if len([]rune(content)) > MaxTodoContentChars {
		runes := []rune(content)
		markRunes := []rune(todoTruncationMark)
		keep := MaxTodoContentChars - len(markRunes)
		if keep < 0 {
			keep = 0
		}
		content = string(runes[:keep]) + todoTruncationMark
	}
	return todo.Item{
		ID:      strings.TrimSpace(it.ID),
		Content: content,
		Status:  strings.TrimSpace(it.Status),
	}
}

func mergeTodoItems(existing, incoming []todo.Item) []todo.Item {
	out := make([]todo.Item, len(existing))
	copy(out, existing)
	indexByID := make(map[string]int, len(out))
	for i, it := range out {
		indexByID[it.ID] = i
	}
	for _, it := range incoming {
		if idx, ok := indexByID[it.ID]; ok && it.ID != "" {
			if it.Content != "" {
				out[idx].Content = it.Content
			}
			if it.Status != "" {
				out[idx].Status = it.Status
			}
		} else {
			indexByID[it.ID] = len(out)
			out = append(out, it)
		}
	}
	return out
}
