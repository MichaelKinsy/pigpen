// SPDX-License-Identifier: GPL-3.0-or-later
package piopencodefree

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

type chatRequest struct {
	Model       string           `json:"model"`
	Messages    []map[string]any `json:"messages"`
	Tools       []map[string]any `json:"tools,omitempty"`
	ToolChoice  any              `json:"tool_choice,omitempty"`
	Stream      bool             `json:"stream"`
	Temperature *float64         `json:"temperature,omitempty"`
	MaxTokens   int              `json:"max_tokens,omitempty"`
	Reasoning   string           `json:"reasoning_effort,omitempty"`
	StreamOpts  map[string]any   `json:"stream_options,omitempty"`
}

func (c *client) runChat(ctx context.Context, out *sdk.ModelEventStream, model, transcript map[string]any, opts sdk.ProviderStreamOptions) {
	r := newReply(out, model, opts)
	r.newID = c.newID
	r.start()
	request, err := buildChatRequest(model, transcript, opts)
	if err != nil {
		r.fail(err, ctx.Err() != nil)
		return
	}
	payload, err := invokePayload(opts, request, model)
	if err != nil {
		r.fail(err, ctx.Err() != nil)
		return
	}
	body, err := json.Marshal(payload)
	if err != nil {
		r.fail(fmt.Errorf("the request for OpenCode Zen cannot be encoded: %w", err), false)
		return
	}
	base := strings.TrimRight(c.baseURL, "/")
	if modelBase, _ := model["baseUrl"].(string); modelBase != "" {
		base = strings.TrimRight(modelBase, "/")
	}
	resp, cancel, note, err := c.send(ctx, base+"/chat/completions", body, opts)
	if err != nil {
		r.fail(err, ctx.Err() != nil)
		return
	}
	defer cancel()
	defer func() { _ = resp.Body.Close() }()
	if err := observeResponse(opts, resp, model); err != nil {
		r.fail(err, ctx.Err() != nil)
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		r.fail(httpError(resp, note), false)
		return
	}
	parseChatSSE(ctx, resp.Body, r)
}

func buildChatRequest(model, transcript map[string]any, opts sdk.ProviderStreamOptions) (map[string]any, error) {
	systemText, tools := collectSystem(transcript)
	messages := make([]map[string]any, 0)
	if systemText != "" {
		messages = append(messages, map[string]any{"role": "system", "content": systemText})
	}
	items, _ := transcript["messages"].([]any)
	supportsImages := modelSupportsImages(model)
	i := 0
	for i < len(items) {
		message, _ := items[i].(map[string]any)
		i++
		if message == nil {
			continue
		}
		switch message["role"] {
		case "system":
			// Already collapsed into the leading system message.
		case "user":
			if text, ok := message["content"].(string); ok {
				messages = append(messages, map[string]any{"role": "user", "content": text})
				continue
			}
			text, images := messageTextAndImages(message["content"], supportsImages)
			if text == "" && len(images) == 0 {
				continue
			}
			if len(images) == 0 {
				messages = append(messages, map[string]any{"role": "user", "content": text})
			} else {
				parts := make([]map[string]any, 0, len(images)+1)
				if text != "" {
					parts = append(parts, map[string]any{"type": "text", "text": text})
				}
				parts = append(parts, images...)
				messages = append(messages, map[string]any{"role": "user", "content": parts})
			}
		case "assistant":
			reasoning, _ := model["reasoning"].(bool)
			converted := convertAssistantMessage(message, reasoning)
			if converted != nil {
				messages = append(messages, converted)
			}
		case "toolResult":
			var imageParts []map[string]any
			for {
				text, images := toolResultTextAndImages(message["content"], supportsImages)
				if text == "" {
					if len(images) > 0 || toolResultHasImage(message["content"]) {
						text = "(see attached image)"
					} else {
						text = "(no tool output)"
					}
				}
				messages = append(messages, map[string]any{
					"role": "tool", "tool_call_id": message["toolCallId"], "content": text,
				})
				imageParts = append(imageParts, images...)
				if i >= len(items) {
					break
				}
				next, _ := items[i].(map[string]any)
				if next == nil || next["role"] != "toolResult" {
					break
				}
				message = next
				i++
			}
			if len(imageParts) > 0 {
				parts := append([]map[string]any{{"type": "text", "text": "Attached image(s) from tool result:"}}, imageParts...)
				messages = append(messages, map[string]any{"role": "user", "content": parts})
			}
		}
	}

	request := chatRequest{Model: modelID(model), Messages: messages, Stream: true}
	if len(tools) > 0 {
		request.Tools = chatTools(tools)
	}
	if choice, ok := opts.Values["toolChoice"]; ok && choice != nil {
		request.ToolChoice = choice
	}
	if temp, ok := number(opts.Values["temperature"]); ok {
		request.Temperature = &temp
	}
	if maxTokens, ok := number(opts.Values["maxTokens"]); ok && maxTokens > 0 {
		request.MaxTokens = int(maxTokens)
	}
	if reasoning, ok := model["reasoning"].(bool); ok && reasoning {
		if effort, ok := reasoningEffort(model, opts.Values); ok {
			request.Reasoning = effort
		}
	}
	request.StreamOpts = map[string]any{"include_usage": true}

	encoded, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		return nil, err
	}
	// Sampling parameters are applied last and may override named fields.
	if sampling, ok := opts.Values["samplingParams"].(map[string]any); ok {
		for key, value := range sampling {
			payload[key] = value
		}
	}
	return payload, nil
}

func modelID(model map[string]any) string {
	id, _ := model["id"].(string)
	return strings.TrimPrefix(id, "opencode/")
}

func modelSupportsImages(model map[string]any) bool {
	switch input := model["input"].(type) {
	case []any:
		for _, item := range input {
			if item == "image" {
				return true
			}
		}
	case []string:
		for _, item := range input {
			if item == "image" {
				return true
			}
		}
	}
	return false
}

func convertAssistantMessage(message map[string]any, reasoning bool) map[string]any {
	var textParts, thinking []string
	var calls []map[string]any
	blocks, _ := message["content"].([]any)
	for _, item := range blocks {
		block, _ := item.(map[string]any)
		if block == nil {
			continue
		}
		switch block["type"] {
		case "text":
			if text, _ := block["text"].(string); text != "" {
				textParts = append(textParts, text)
			}
		case "thinking":
			if text, _ := block["thinking"].(string); text != "" {
				thinking = append(thinking, text)
			}
		case "toolCall":
			id, _ := block["id"].(string)
			name, _ := block["name"].(string)
			args, _ := block["arguments"].(map[string]any)
			if args == nil {
				args = map[string]any{}
			}
			encoded, _ := json.Marshal(args)
			calls = append(calls, map[string]any{
				"id": id, "type": "function",
				"function": map[string]any{"name": name, "arguments": string(encoded)},
			})
		}
	}
	content := strings.Join(textParts, "")
	if content == "" && len(calls) == 0 {
		return nil
	}
	converted := map[string]any{"role": "assistant", "content": content}
	if len(calls) > 0 {
		converted["tool_calls"] = calls
	}
	// Zen's free tier wants assistant messages to carry reasoning_content even
	// when it is empty (requiresReasoningContentOnAssistantMessages); the
	// original applies that to reasoning requests only.
	if reasoning && len(thinking) == 0 {
		converted["reasoning_content"] = ""
	} else if len(thinking) > 0 {
		converted["reasoning_content"] = strings.Join(thinking, "\n")
	}
	return converted
}

func toolResultTextAndImages(content any, supportsImages bool) (string, []map[string]any) {
	var text []string
	var images []map[string]any
	blocks, _ := content.([]any)
	for _, item := range blocks {
		block, _ := item.(map[string]any)
		if block == nil {
			continue
		}
		switch block["type"] {
		case "text":
			if value, _ := block["text"].(string); value != "" {
				text = append(text, value)
			}
		case "image":
			if supportsImages {
				data, _ := block["data"].(string)
				mime, _ := block["mimeType"].(string)
				if data != "" {
					images = append(images, map[string]any{
						"type":      "image_url",
						"image_url": map[string]any{"url": "data:" + mime + ";base64," + data},
					})
				}
			}
		}
	}
	return strings.Join(text, "\n"), images
}

func toolResultHasImage(content any) bool {
	blocks, _ := content.([]any)
	for _, item := range blocks {
		block, _ := item.(map[string]any)
		if block != nil && block["type"] == "image" {
			return true
		}
	}
	return false
}

func messageTextAndImages(content any, supportsImages bool) (string, []map[string]any) {
	if text, ok := content.(string); ok {
		return text, nil
	}
	var text []string
	var images []map[string]any
	blocks, _ := content.([]any)
	for _, item := range blocks {
		block, _ := item.(map[string]any)
		if block == nil {
			continue
		}
		switch block["type"] {
		case "text":
			if value, _ := block["text"].(string); value != "" {
				text = append(text, value)
			}
		case "image":
			if supportsImages {
				data, _ := block["data"].(string)
				mime, _ := block["mimeType"].(string)
				if data != "" {
					images = append(images, map[string]any{
						"type":      "image_url",
						"image_url": map[string]any{"url": "data:" + mime + ";base64," + data},
					})
				}
			}
		}
	}
	return strings.Join(text, "\n"), images
}

func chatTools(tools []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		parameters, _ := tool["parameters"].(map[string]any)
		if parameters == nil {
			parameters = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": tool["name"], "description": tool["description"], "parameters": parameters,
			},
		})
	}
	return out
}

// collectSystem collapses system messages into the current system prompt and
// the tools currently on offer, in declaration order.
func collectSystem(transcript map[string]any) (string, []map[string]any) {
	contentParts := []string{}
	sectionOrder := []string{}
	sections := map[string]string{}
	toolOrder := []string{}
	tools := map[string]map[string]any{}
	items, _ := transcript["messages"].([]any)
	for _, item := range items {
		message, _ := item.(map[string]any)
		if message == nil || message["role"] != "system" {
			continue
		}
		if text := contentText(message["content"]); text != "" {
			contentParts = append(contentParts, text)
		}
		switch raw := message["sections"].(type) {
		case map[string]string:
			names := make([]string, 0, len(raw))
			for name := range raw {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				sectionOrder = appendUnique(sectionOrder, name)
				sections[name] = raw[name]
			}
		case map[string]any:
			names := make([]string, 0, len(raw))
			for name := range raw {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				if value, ok := raw[name].(string); ok {
					sectionOrder = appendUnique(sectionOrder, name)
					sections[name] = value
				} else {
					delete(sections, name)
				}
			}
		case []any:
			for _, entry := range raw {
				section, _ := entry.(map[string]any)
				if section == nil {
					continue
				}
				name, _ := section["name"].(string)
				if name == "" {
					continue
				}
				if value, ok := section["value"].(string); ok {
					sectionOrder = appendUnique(sectionOrder, name)
					sections[name] = value
				} else {
					delete(sections, name)
				}
			}
		}
		for _, removed := range mapsList(message["toolsRemoved"]) {
			name, _ := removed["name"].(string)
			if name == "" {
				continue
			}
			delete(tools, name)
			toolOrder = removeName(toolOrder, name)
		}
		for _, added := range mapsList(message["toolsAdded"]) {
			name, _ := added["name"].(string)
			if name == "" {
				continue
			}
			if _, exists := tools[name]; !exists {
				toolOrder = append(toolOrder, name)
			}
			tools[name] = added
		}
	}
	parts := append([]string{}, contentParts...)
	for _, name := range sectionOrder {
		if text := sections[name]; text != "" {
			parts = append(parts, text)
		}
	}
	out := make([]map[string]any, 0, len(toolOrder))
	for _, name := range toolOrder {
		if tool := tools[name]; tool != nil {
			out = append(out, tool)
		}
	}
	return strings.Join(parts, "\n\n"), out
}

func contentText(content any) string {
	if text, ok := content.(string); ok {
		return text
	}
	var parts []string
	blocks, _ := content.([]any)
	for _, item := range blocks {
		block, _ := item.(map[string]any)
		if block == nil {
			continue
		}
		if block["type"] == "text" {
			if text, _ := block["text"].(string); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func mapsList(value any) []map[string]any {
	items, _ := value.([]any)
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func appendUnique(names []string, name string) []string {
	for _, existing := range names {
		if existing == name {
			return names
		}
	}
	return append(names, name)
}

func removeName(names []string, name string) []string {
	out := names[:0]
	for _, existing := range names {
		if existing != name {
			out = append(out, existing)
		}
	}
	return out
}

func reasoningLevel(values map[string]any) string {
	level := ""
	for _, key := range []string{"reasoningEffort", "reasoning", "effort"} {
		if value, _ := values[key].(string); value != "" {
			level = value
			break
		}
	}
	if enabled, exists := values["thinkingEnabled"].(bool); exists {
		if !enabled {
			level = "off"
		} else if level == "" {
			level = "high"
		}
	}
	return level
}

func reasoningEffort(model map[string]any, values map[string]any) (string, bool) {
	level := reasoningLevel(values)
	if level == "" {
		return "", false
	}
	levels, _ := model["thinkingLevelMap"].(map[string]any)
	if levels != nil {
		if mapped, exists := levels[level]; exists {
			if mapped == nil {
				// A null entry hides an unsupported level; "off" is special,
				// a non-off level falls back to its own name as upstream does.
				if level == "off" {
					return "", false
				}
				return level, true
			}
			if text, ok := mapped.(string); ok {
				return text, true
			}
		}
	}
	if level == "off" {
		return "", false
	}
	return level, true
}

type chatChunk struct {
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
	Choices []struct {
		Delta struct {
			Content          string  `json:"content"`
			ReasoningContent *string `json:"reasoning_content"`
			Reasoning        string  `json:"reasoning"`
			ToolCalls        []struct {
				Index    *int   `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		TotalTokens         int `json:"total_tokens"`
		PromptTokensDetails *struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
		CompletionTokensDetails *struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		} `json:"completion_tokens_details"`
	} `json:"usage"`
}

type chatToolState struct {
	block     int
	id        string
	name      string
	args      strings.Builder
	finalized bool
}

func parseChatSSE(ctx context.Context, body io.Reader, r *reply) {
	states := map[int]*chatToolState{}
	order := []int{}
	finish := ""
	sawContent := false
	gotDone := false

	ensureTool := func(index int, id, name string) *chatToolState {
		if state, ok := states[index]; ok {
			if id != "" && state.id == "" {
				state.id = id
				r.blocks[state.block]["id"] = id
			}
			if name != "" && state.name == "" {
				state.name = name
				r.blocks[state.block]["name"] = name
			}
			return state
		}
		toolID := id
		if toolID == "" {
			toolID = r.newToolID()
		}
		block := r.startToolCall(toolID, name)
		state := &chatToolState{block: block, id: toolID, name: name}
		states[index] = state
		order = append(order, index)
		return state
	}

	finishTools := func() error {
		for _, index := range order {
			state := states[index]
			if state.finalized {
				continue
			}
			args, err := parseToolArguments(state.args.String())
			if err != nil {
				return err
			}
			r.finishToolCall(state.block, args)
			state.finalized = true
		}
		return nil
	}

	err := sseData(body, func(data string) error {
		if err := r.observe(data); err != nil {
			return err
		}
		var chunk chatChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return fmt.Errorf("OpenCode Zen sent a line that is not JSON: %.80q", data)
		}
		if chunk.Error != nil {
			message := chunk.Error.Message
			if message == "" {
				message = chunk.Error.Type
			}
			if message == "" {
				message = chunk.Error.Code
			}
			return errors.New(message)
		}
		if chunk.Usage != nil {
			cached := 0
			if chunk.Usage.PromptTokensDetails != nil {
				cached = chunk.Usage.PromptTokensDetails.CachedTokens
			}
			reasoning := 0
			if chunk.Usage.CompletionTokensDetails != nil {
				reasoning = chunk.Usage.CompletionTokensDetails.ReasoningTokens
			}
			setUsage(r.usage, max(0, chunk.Usage.PromptTokens-cached), chunk.Usage.CompletionTokens, cached, 0, reasoning, chunk.Usage.TotalTokens)
		}
		for _, choice := range chunk.Choices {
			delta := choice.Delta
			if delta.ReasoningContent != nil {
				r.delta("thinking", *delta.ReasoningContent)
				if *delta.ReasoningContent != "" {
					sawContent = true
				}
			} else if delta.Reasoning != "" {
				r.delta("thinking", delta.Reasoning)
				sawContent = true
			}
			if delta.Content != "" {
				r.delta("text", delta.Content)
				sawContent = true
			}
			for i, call := range delta.ToolCalls {
				index := i
				if call.Index != nil {
					index = *call.Index
				}
				state := ensureTool(index, call.ID, call.Function.Name)
				if call.Function.Arguments != "" {
					state.args.WriteString(call.Function.Arguments)
					r.toolCallDelta(state.block, call.Function.Arguments)
					sawContent = true
				}
			}
			if choice.FinishReason != nil && *choice.FinishReason != "" {
				finish = *choice.FinishReason
				if err := finishTools(); err != nil {
					return err
				}
				r.closeOpen()
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, io.EOF) {
		r.fail(err, ctx.Err() != nil)
		return
	}
	if errors.Is(err, io.EOF) {
		gotDone = true
	}
	if !gotDone && finish == "" && !sawContent {
		r.fail(errors.New("The response stream from OpenCode Zen ended before it finished (no final message)."), ctx.Err() != nil)
		return
	}
	if err := finishTools(); err != nil {
		r.fail(err, ctx.Err() != nil)
		return
	}
	reason := stopReasonFromFinish(finish)
	r.done(reason)
}

func parseToolArguments(raw string) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]any{}, nil
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return nil, fmt.Errorf("OpenCode Zen sent tool-call arguments that are not a JSON object: %.120q", raw)
	}
	if args == nil {
		args = map[string]any{}
	}
	return args, nil
}

func stopReasonFromFinish(finish string) string {
	switch finish {
	case "length":
		return "length"
	case "tool_calls", "function_call":
		return "toolUse"
	default:
		return "stop"
	}
}
