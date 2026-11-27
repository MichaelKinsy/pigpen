package memoryskills

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/config"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/events"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/memory"
)

const replaceRemovedError = "'replace' is not available; use 'patch'. old_text is the exact span to change and " +
	"content is its new text; the rest of the entry is kept. To rewrite a whole entry, " +
	"set old_text to the whole entry. Nothing was written."

const memoryToolDescription = "Save durable facts to persistent memory that survive across sessions. Memory is " +
	"injected into every future turn, so keep entries compact and high-signal.\n\n" +
	"HOW: make ALL your changes to one target in ONE call via an 'operations' array (each " +
	"item: {action, content?, old_text?}). The batch applies atomically and the char limit is " +
	"checked only on the FINAL result — so a single call can remove/patch stale entries " +
	"to free room AND add new ones, even when an add alone would overflow. The response " +
	"reports current/limit chars and confirms completion; one batch call finishes the " +
	"update, so don't repeat it.\n\n" +
	"WHEN: only for facts that apply to EVERY session regardless of task: who the user " +
	"is, stable environment facts, standing conventions with no task home. Anything " +
	"learned while doing a task (procedures, pitfalls, and the user's preferences and " +
	"corrections for that kind of work) belongs in the task's skill via skill_manage, " +
	"where it loads only when relevant; memory is injected into every turn and must " +
	"stay small.\n\n" +
	"IF FULL: an add is rejected with the current entries shown. Reissue as ONE batch that " +
	"removes or shortens enough stale entries and adds the new one together.\n\n" +
	"TARGETS: 'user' = who the user is (name, role, preferences, style). 'memory' = your " +
	"notes (environment, conventions, tool quirks, lessons).\n\n" +
	"SKIP: trivial/obvious info, easily re-discovered facts, raw data dumps, task progress, " +
	"completed-work logs, temporary TODO state (use session_search for those). Reusable " +
	"procedures belong in a skill, not memory."

func memoryToolSchema() sdk.Schema {
	return sdk.Schema{
		"type": "object",
		"properties": map[string]any{
			"target": map[string]any{
				"type":        "string",
				"enum":        []string{"memory", "user"},
				"description": "Which memory store: 'memory' for personal notes, 'user' for user profile.",
			},
			"operations": map[string]any{
				"type": "array",
				"description": "Changes applied atomically in one call against the final char budget; " +
					"a single change is a one-item list.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"action": map[string]any{
							"type": "string",
							"enum": []string{"add", "patch", "remove"},
						},
						"content": map[string]any{
							"type": "string",
							"description": "Required for 'add' and 'patch'. For 'add', the new entry. For 'patch', " +
								"the text that replaces old_text; the rest of the entry is kept.",
						},
						"old_text": map[string]any{
							"type": "string",
							"description": "REQUIRED for 'patch' and 'remove': text from one entry. 'patch' replaces " +
								"exactly this span (it must occur once in the entry); 'remove' deletes the " +
								"whole entry. Omit for 'add'.",
						},
					},
					"required":             []string{"action"},
					"additionalProperties": false,
				},
			},
			// Legacy flat single-op compatibility fields.
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"add", "patch", "remove"},
				"description": "Legacy single-operation action (prefer operations[]).",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "Legacy single-operation content (prefer operations[]).",
			},
			"old_text": map[string]any{
				"type":        "string",
				"description": "Legacy single-operation old_text (prefer operations[]).",
			},
			"new_text": map[string]any{
				"type":        "string",
				"description": "Legacy alias for content (prefer operations[]).",
			},
		},
	}
}

func registerMemoryTool(ext *sdk.Extension, cfg *config.Config, memStore *memory.Store) {
	if isToolDisabled(cfg, "memory") {
		return
	}
	ext.RegisterTool(sdk.ToolDefinition{
		Name:        "memory",
		Label:       "Memory",
		Description: memoryToolDescription,
		Parameters:  memoryToolSchema(),
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			return handleMemoryTool(ctx, memStore, params), nil
		},
	})
}

func handleMemoryTool(ctx sdk.Context, memStore *memory.Store, params map[string]any) sdk.ToolResult {
	if memStore == nil {
		return jsonToolError("Memory is not available. It may be disabled in config or this environment.")
	}

	targetStr := stringParam(params, "target")
	if targetStr == "" {
		targetStr = string(memory.TargetMemory)
	}
	target := memory.Target(strings.ToLower(strings.TrimSpace(targetStr)))
	if target != memory.TargetMemory && target != memory.TargetUser {
		return jsonToolError(fmt.Sprintf("Invalid memory target '%s'. Use 'memory' or 'user'.", targetStr))
	}

	ops, errMsg := parseMemoryOperations(params, target)
	if errMsg != "" {
		return jsonToolError(errMsg)
	}

	goCtx := contextFromSDK(ctx)
	res, err := memStore.Apply(goCtx, target, ops)
	if err != nil {
		return jsonToolError(err.Error())
	}

	sessionID := safeSessionID(ctx)
	lastAction := ops[len(ops)-1].Action
	if len(ops) > 1 {
		lastAction = "batch"
	}
	payload := events.MemoryWritePayload{
		SessionID:   sessionID,
		Target:      string(target),
		Action:      lastAction,
		EntrySHA256: res.SHA256,
		CharsUsed:   res.CharsUsed,
		CharsCap:    res.CharsLimit,
	}
	// Best-effort event emission; unit tests without a connected host still succeed.
	safeEmit(ctx, events.ChannelMemoryWrite, payload)
	return jsonToolOK(res)
}

func safeSessionID(ctx sdk.Context) (sid string) {
	defer func() { _ = recover() }()
	if id, err := ctx.GetSessionID(); err == nil {
		return id
	}
	return ""
}

func safeEmit(ctx sdk.Context, channel string, payload any) {
	defer func() { _ = recover() }()
	_ = ctx.Events().Emit(channel, payload)
}

func parseMemoryOperations(params map[string]any, topTarget memory.Target) ([]memory.Operation, string) {
	if rawOps, hasOps := params["operations"]; hasOps && rawOps != nil {
		slice, ok := rawOps.([]any)
		if !ok {
			return nil, "operations must be a list of {action, content?, old_text?} objects."
		}
		if len(slice) == 0 {
			return nil, "Missing required parameter: 'operations', a list of {action: add/patch/remove, content?, old_text?} changes; a single change is a one-item list."
		}
		ops := make([]memory.Operation, 0, len(slice))
		for i, item := range slice {
			m, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Sprintf("Operation %d must be an object with {action, content?, old_text?}.", i+1)
			}
			if nestedTarget := strings.TrimSpace(stringParam(m, "target")); nestedTarget != "" && memory.Target(strings.ToLower(nestedTarget)) != topTarget {
				return nil, fmt.Sprintf("Operation %d names a different target than the one at the top level. One call writes one store: retry with separate calls for different stores. Nothing was written or staged.", i+1)
			}
			action := strings.ToLower(strings.TrimSpace(stringParam(m, "action")))
			if action == "replace" {
				return nil, replaceRemovedError
			}
			if action == "" {
				return nil, fmt.Sprintf("Operation %d is missing required field 'action'.", i+1)
			}
			content := stringParam(m, "content")
			if content == "" {
				content = stringParam(m, "new_text")
			}
			oldText := stringParam(m, "old_text")
			ops = append(ops, memory.Operation{
				Action:  action,
				Content: content,
				OldText: oldText,
			})
		}
		return ops, ""
	}

	// Legacy flat single-op compatibility.
	action := strings.ToLower(strings.TrimSpace(stringParam(params, "action")))
	if action == "" {
		return nil, "Missing required parameter: 'operations', a list of {action: add/patch/remove, content?, old_text?} changes; a single change is a one-item list."
	}
	if action == "replace" {
		return nil, replaceRemovedError
	}
	if action != "add" && action != "patch" && action != "remove" {
		return nil, fmt.Sprintf("Unknown action '%s'. Use: add, patch, remove", action)
	}
	content := stringParam(params, "content")
	if content == "" {
		content = stringParam(params, "new_text")
	}
	oldText := stringParam(params, "old_text")
	return []memory.Operation{{
		Action:  action,
		Content: content,
		OldText: oldText,
	}}, ""
}

func contextFromSDK(ctx sdk.Context) context.Context {
	done := ctx.Done()
	if done == nil {
		return context.Background()
	}
	c, cancel := context.WithCancel(context.Background())
	go func() {
		select {
		case <-done:
			cancel()
		case <-c.Done():
		}
	}()
	return c
}

func stringParam(params map[string]any, key string) string {
	if params == nil {
		return ""
	}
	v, ok := params[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

func boolParam(params map[string]any, key string) bool {
	if params == nil {
		return false
	}
	v, ok := params[key]
	if !ok || v == nil {
		return false
	}
	b, ok := v.(bool)
	return ok && b
}

func jsonToolOK(v any) sdk.ToolResult {
	b, err := json.Marshal(v)
	if err != nil {
		return jsonToolError(err.Error())
	}
	return sdk.ToolResult{Content: string(b)}
}

func jsonToolError(msg string) sdk.ToolResult {
	b, _ := json.Marshal(map[string]any{
		"success": false,
		"error":   msg,
	})
	return sdk.ToolResult{
		Content: string(b),
		IsError: true,
	}
}

func isToolDisabled(cfg *config.Config, name string) bool {
	if cfg == nil {
		return false
	}
	for _, d := range cfg.DisabledTools {
		if strings.EqualFold(strings.TrimSpace(d), name) {
			return true
		}
	}
	return false
}
