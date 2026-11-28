package prediction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// CompleteFunc executes the ephemeral prediction completion. Tests override it
// on the controller to avoid real provider/network calls.
type CompleteFunc func(goCtx context.Context, sdkCtx sdk.Context, modelObj, req, auth map[string]any) (json.RawMessage, error)

// defaultComplete invokes `sdkCtx.Complete(modelObj, req, auth)` in a goroutine
// so `goCtx` cancellation / deadline immediately unblocks the caller. Note that
// PiG's `sdk.Context.Complete` does not accept a cancellation signal across the
// subprocess wire, so cancellation stops waiting and discards any late reply
// locally while the host call drains.
func defaultComplete(goCtx context.Context, sdkCtx sdk.Context, modelObj, req, auth map[string]any) (json.RawMessage, error) {
	type result struct {
		raw json.RawMessage
		err error
	}
	ch := make(chan result, 1)
	go func() {
		raw, err := sdkCtx.Complete(modelObj, req, auth)
		ch <- result{raw: raw, err: err}
	}()
	select {
	case <-goCtx.Done():
		return nil, goCtx.Err()
	case r := <-ch:
		return r.raw, r.err
	}
}

// buildPredictionRequest prepares the model object, auth payload, and
// prompt-cache-friendly completion request reusing:
//   - the session's exact systemPrompt
//   - the session's active tool catalog (`GetAllTools` filtered to `GetActiveTools`
//     in active order, preserving raw JSON schema parameter ordering)
//   - the compaction-aware LLM conversation prefix from `BuildSessionContext`
//     (preserving real user/assistant/toolCall/toolResult messages verbatim,
//     converting compaction/branch/custom/bash summaries using Pi's exact format)
//   - the rendered `<composer-prediction>` user prompt appended at the end
//   - `maxTokens: PredictionMaxTokens` and `toolChoice: "none"` (only when tools
//     are non-empty so providers never reject `toolChoice: "none"` without tools).
func buildPredictionRequest(ctx sdk.Context, sessMessages []map[string]any, alternatives bool) (modelObj, req, auth map[string]any, ok bool) {
	modelInfo, err := ctx.GetModelInfo()
	if err != nil || modelInfo == nil || strings.TrimSpace(modelInfo.ID) == "" {
		return nil, nil, nil, false
	}
	modelObj = map[string]any{
		"id":       modelInfo.ID,
		"provider": modelInfo.Provider,
	}
	if regModel, err := ctx.ModelRegistry().Find(modelInfo.Provider, modelInfo.ID); err == nil && regModel != nil {
		modelObj = regModel
	}

	if rawAuth, err := ctx.GetModelAuth(modelInfo.Provider, modelInfo.ID); err == nil && len(rawAuth) > 0 {
		_ = json.Unmarshal(rawAuth, &auth)
	}

	messages := buildPredictionMessages(sessMessages, alternatives)
	req = map[string]any{
		"messages":  messages,
		"maxTokens": PredictionMaxTokens,
	}
	if sysPrompt, err := ctx.GetSystemPrompt(); err == nil && sysPrompt != "" {
		req["systemPrompt"] = sysPrompt
	}
	if tools := buildActiveToolSchemas(ctx); len(tools) > 0 {
		req["tools"] = tools
		req["toolChoice"] = "none"
	}
	return modelObj, req, auth, true
}

// loadSessionContextMessages fetches the compaction-aware session context via
// `ctx.SessionManager().BuildSessionContext()`. When the host does not serve
// `sessionRead/buildSessionContext` (e.g. minimal harnesses), it falls back to
// converting `ctx.GetBranch()` entries.
func loadSessionContextMessages(ctx sdk.Context) ([]map[string]any, error) {
	if sc, err := ctx.SessionManager().BuildSessionContext(); err == nil {
		return sc.Messages, nil
	}
	branch, err := ctx.GetBranch()
	if err != nil {
		return nil, err
	}
	return fallbackBranchMessages(branch), nil
}

// buildActiveToolSchemas returns the active tool definitions matching the
// session's active tool catalog in active-tool order, shaped for PiG's
// `decodeSubprocessStream` (`[]ai.ToolSchema`). Preserves `Parameters` as
// `json.RawMessage` so parameter declaration order stays identical to the main
// session for prompt-cache hits.
func buildActiveToolSchemas(ctx sdk.Context) []map[string]any {
	activeNames, err := ctx.GetActiveTools()
	if err != nil || len(activeNames) == 0 {
		return nil
	}
	allTools, err := ctx.GetAllTools()
	if err != nil || len(allTools) == 0 {
		return nil
	}
	byName := make(map[string]sdk.ToolInfo, len(allTools))
	for _, tool := range allTools {
		byName[tool.Name] = tool
	}
	out := make([]map[string]any, 0, len(activeNames))
	seen := make(map[string]struct{}, len(activeNames))
	for _, name := range activeNames {
		if _, dup := seen[name]; dup {
			continue
		}
		info, ok := byName[name]
		if !ok {
			continue
		}
		seen[name] = struct{}{}
		entry := map[string]any{
			"name":        info.Name,
			"description": info.Description,
		}
		if len(info.Parameters) > 0 {
			entry["parameters"] = info.Parameters
		} else {
			entry["parameters"] = map[string]any{"type": "object"}
		}
		if len(info.PromptGuidelines) > 0 {
			entry["promptGuidelines"] = info.PromptGuidelines
		}
		out = append(out, entry)
	}
	return out
}

// buildPredictionMessages converts session context messages (from
// `BuildSessionContext`) into LLM wire messages matching PiG's `ConvertToLLM`
// (preserving real `user`, `assistant`, `toolResult`, and `system` messages
// verbatim including real `toolCall`/`toolResult` blocks, and wrapping
// `compactionSummary`, `branchSummary`, `bashExecution`, and `custom` messages
// into user messages with Pi's exact prefixes), then appends the rendered
// `<composer-prediction>` user prompt.
func buildPredictionMessages(sessMessages []map[string]any, alternatives bool) []map[string]any {
	messages := make([]map[string]any, 0, len(sessMessages)+1)
	for _, m := range sessMessages {
		if m == nil {
			continue
		}
		role, _ := m["role"].(string)
		switch role {
		case "system", "user", "assistant", "toolResult", "tool":
			messages = append(messages, m)
		case "compactionSummary":
			summary, _ := m["summary"].(string)
			text := "The conversation history before this point was compacted into the following summary:\n\n<summary>\n" + summary + "\n</summary>"
			messages = append(messages, userTextMessage(text, m["timestamp"]))
		case "branchSummary":
			summary, _ := m["summary"].(string)
			text := "The following is a summary of a branch that this conversation came back from:\n\n<summary>\n" + summary + "</summary>"
			messages = append(messages, userTextMessage(text, m["timestamp"]))
		case "bashExecution":
			if excluded, _ := m["excludeFromContext"].(bool); excluded {
				continue
			}
			messages = append(messages, userTextMessage(bashExecutionToText(m), m["timestamp"]))
		case "custom":
			if excluded, _ := m["excludeFromContext"].(bool); excluded {
				continue
			}
			switch content := m["content"].(type) {
			case string:
				messages = append(messages, userTextMessage(content, m["timestamp"]))
			case []any:
				msg := map[string]any{"role": "user", "content": content}
				if ts := m["timestamp"]; ts != nil {
					msg["timestamp"] = ts
				}
				messages = append(messages, msg)
			}
		}
	}
	messages = append(messages, map[string]any{
		"role":    "user",
		"content": []map[string]any{{"type": "text", "text": RenderPrompt(alternatives)}},
	})
	return messages
}

func userTextMessage(text string, timestamp any) map[string]any {
	msg := map[string]any{
		"role":    "user",
		"content": []map[string]any{{"type": "text", "text": text}},
	}
	if timestamp != nil {
		msg["timestamp"] = timestamp
	}
	return msg
}

func bashExecutionToText(m map[string]any) string {
	cmd, _ := m["command"].(string)
	output, _ := m["output"].(string)
	cancelled, _ := m["cancelled"].(bool)
	truncated, _ := m["truncated"].(bool)
	fullOutputPath, _ := m["fullOutputPath"].(string)

	text := "Ran `" + cmd + "`\n"
	if output != "" {
		text += "```\n" + output + "\n```"
	} else {
		text += "(no output)"
	}
	if cancelled {
		text += "\n\n(command cancelled)"
	} else if exitCode, ok := m["exitCode"].(float64); ok && exitCode != 0 {
		text += fmt.Sprintf("\n\nCommand exited with code %d", int(exitCode))
	}
	if truncated && fullOutputPath != "" {
		text += fmt.Sprintf("\n\n[Output truncated. Full output: %s]", fullOutputPath)
	}
	return text
}

// fallbackBranchMessages converts `sdk.BranchEntry` records into session context
// messages honoring the newest compaction's `FirstKeptEntryID` when
// `SessionManager().BuildSessionContext()` is unavailable.
func fallbackBranchMessages(branch []sdk.BranchEntry) []map[string]any {
	compactionIdx := -1
	for i, e := range branch {
		if e.Type == "compaction" {
			compactionIdx = i
		}
	}
	var active []sdk.BranchEntry
	if compactionIdx >= 0 {
		comp := branch[compactionIdx]
		active = append(active, comp)
		foundFirstKept := false
		for _, e := range branch[:compactionIdx] {
			if e.ID != "" && e.ID == comp.FirstKeptEntryID {
				foundFirstKept = true
			}
			if !foundFirstKept {
				continue
			}
			if e.Type == "message" && e.Role == "system" {
				continue
			}
			active = append(active, e)
		}
		active = append(active, branch[compactionIdx+1:]...)
	} else {
		active = branch
	}

	out := make([]map[string]any, 0, len(active))
	for _, entry := range active {
		switch entry.Type {
		case "compaction":
			if strings.TrimSpace(entry.Content) != "" {
				out = append(out, map[string]any{
					"role":    "compactionSummary",
					"summary": entry.Content,
				})
			}
		case "branch_summary":
			if strings.TrimSpace(entry.Content) != "" {
				out = append(out, map[string]any{
					"role":    "branchSummary",
					"summary": entry.Content,
				})
			}
		case "message":
			switch entry.Role {
			case "user":
				if strings.TrimSpace(entry.Content) != "" {
					out = append(out, map[string]any{
						"role":    "user",
						"content": []map[string]any{{"type": "text", "text": entry.Content}},
					})
				}
			case "assistant":
				blocks := make([]map[string]any, 0, 1+len(entry.ToolCalls))
				if strings.TrimSpace(entry.Content) != "" {
					blocks = append(blocks, map[string]any{"type": "text", "text": entry.Content})
				}
				for _, tc := range entry.ToolCalls {
					var parsedArgs any
					if err := json.Unmarshal([]byte(tc.Args), &parsedArgs); err != nil || parsedArgs == nil {
						parsedArgs = map[string]any{}
					}
					blocks = append(blocks, map[string]any{
						"type":      "toolCall",
						"id":        tc.ID,
						"name":      tc.Name,
						"arguments": parsedArgs,
					})
				}
				if len(blocks) > 0 {
					out = append(out, map[string]any{
						"role":    "assistant",
						"content": blocks,
					})
				}
			case "toolResult":
				if entry.ToolCallID != "" {
					out = append(out, map[string]any{
						"role":       "toolResult",
						"toolCallId": entry.ToolCallID,
						"toolName":   entry.ToolName,
						"content":    []map[string]any{{"type": "text", "text": entry.Content}},
						"isError":    entry.IsError,
					})
				}
			}
		}
	}
	return out
}

// contextHasCompletedAssistant reports whether the active session context
// contains at least one assistant message.
func contextHasCompletedAssistant(messages []map[string]any) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		if role, _ := messages[i]["role"].(string); role == "assistant" {
			return true
		}
	}
	return false
}

// contextAnchor returns a stable fingerprint for the last context-bearing
// message so conversation advancement invalidates an in-flight prediction.
// Prefers the session leaf entry ID when available; falls back to message count
// and last context message JSON.
func contextAnchor(ctx sdk.Context, messages []map[string]any) string {
	if leafID, err := ctx.SessionManager().GetLeafID(); err == nil && leafID != nil && *leafID != "" {
		return "leaf:" + *leafID
	}
	if branch, err := ctx.GetBranch(); err == nil {
		for i := len(branch) - 1; i >= 0; i-- {
			e := branch[i]
			if e.Type == "message" || e.Type == "compaction" || e.Type == "branch_summary" {
				if e.ID != "" {
					return "branch:" + e.ID
				}
				return fmt.Sprintf("branch:%d:%s:%s", i, e.Type, e.Role)
			}
		}
	}
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if m == nil {
			continue
		}
		if excluded, _ := m["excludeFromContext"].(bool); excluded {
			continue
		}
		if role, _ := m["role"].(string); role == "custom" {
			if ct, _ := m["customType"].(string); ct == "advisor" {
				continue
			}
		}
		raw, _ := json.Marshal(m)
		return fmt.Sprintf("msg:%d:%s", i, string(raw))
	}
	return ""
}

// extractCompleteText parses an assistant message returned by `ctx.Complete`.
func extractCompleteText(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", errors.New("empty response from provider")
	}
	var resp struct {
		StopReason   string `json:"stopReason"`
		Error        string `json:"error"`
		ErrorMessage string `json:"errorMessage"`
		Content      any    `json:"content"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", err
	}
	if resp.Error != "" {
		return "", errors.New(resp.Error)
	}
	if resp.ErrorMessage != "" {
		return "", errors.New(resp.ErrorMessage)
	}
	if resp.StopReason == "error" && resp.Content == nil {
		return "", errors.New("provider returned error")
	}
	switch c := resp.Content.(type) {
	case string:
		return strings.TrimSpace(c), nil
	case []any:
		var parts []string
		for _, item := range c {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if block["type"] == "text" {
				if t, ok := block["text"].(string); ok {
					parts = append(parts, t)
				}
			}
		}
		return strings.TrimSpace(strings.Join(parts, "")), nil
	}
	return "", nil
}
