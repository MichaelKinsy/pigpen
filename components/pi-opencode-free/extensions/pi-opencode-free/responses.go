// SPDX-License-Identifier: GPL-3.0-or-later
package piopencodefree

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func (c *client) runResponses(ctx context.Context, out *sdk.ModelEventStream, model, transcript map[string]any, opts sdk.ProviderStreamOptions) {
	r := newReply(out, model, opts)
	r.newID = c.newID
	r.start()
	request, err := buildResponsesRequest(model, transcript, opts)
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
	resp, cancel, note, err := c.send(ctx, base+"/responses", body, opts)
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
	parseResponsesSSE(ctx, resp.Body, r)
}

func buildResponsesRequest(model, transcript map[string]any, opts sdk.ProviderStreamOptions) (map[string]any, error) {
	systemText, tools := collectSystem(transcript)
	input := []any{}
	if systemText != "" {
		input = append(input, map[string]any{"role": "system", "content": systemText})
	}
	items, _ := transcript["messages"].([]any)
	messageIndex := 0
	for _, item := range items {
		message, _ := item.(map[string]any)
		if message == nil {
			continue
		}
		switch message["role"] {
		case "system":
		case "user":
			parts := inputParts(message["content"], modelSupportsImages(model))
			if len(parts) > 0 {
				input = append(input, map[string]any{"role": "user", "content": parts})
			}
		case "assistant":
			textIndex := 0
			blocks, _ := message["content"].([]any)
			for _, raw := range blocks {
				block, _ := raw.(map[string]any)
				if block == nil {
					continue
				}
				switch block["type"] {
				case "text":
					text, _ := block["text"].(string)
					id := fmt.Sprintf("msg_pi_%d", messageIndex)
					if textIndex > 0 {
						id = fmt.Sprintf("msg_pi_%d_%d", messageIndex, textIndex)
					}
					textIndex++
					input = append(input, map[string]any{
						"type": "message", "role": "assistant",
						"content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}},
						"status":  "completed", "id": id,
					})
				case "toolCall":
					id, _ := block["id"].(string)
					name, _ := block["name"].(string)
					args, _ := block["arguments"].(map[string]any)
					if args == nil {
						args = map[string]any{}
					}
					encoded, _ := json.Marshal(args)
					input = append(input, map[string]any{
						"type": "function_call", "call_id": id, "name": name, "arguments": string(encoded),
					})
				}
			}
		case "toolResult":
			callID, _ := message["toolCallId"].(string)
			input = append(input, map[string]any{
				"type": "function_call_output", "call_id": callID,
				"output": responsesToolOutput(message["content"], modelSupportsImages(model)),
			})
		}
		messageIndex++
	}
	request := map[string]any{
		"model":  modelID(model),
		"input":  input,
		"stream": true,
		"store":  false,
	}
	if len(tools) > 0 {
		converted := make([]map[string]any, 0, len(tools))
		for _, tool := range tools {
			parameters, _ := tool["parameters"].(map[string]any)
			if parameters == nil {
				parameters = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			converted = append(converted, map[string]any{
				"type": "function", "name": tool["name"], "description": tool["description"], "parameters": parameters,
			})
		}
		request["tools"] = converted
	}
	if choice, ok := opts.Values["toolChoice"]; ok && choice != nil {
		request["tool_choice"] = choice
	}
	if temp, ok := number(opts.Values["temperature"]); ok {
		request["temperature"] = temp
	}
	if maxTokens, ok := number(opts.Values["maxTokens"]); ok && maxTokens > 0 {
		request["max_output_tokens"] = max(16, int(maxTokens))
	}
	if reasoning, ok := model["reasoning"].(bool); ok && reasoning {
		level := reasoningLevel(opts.Values)
		if effort, ok := reasoningEffort(model, opts.Values); ok {
			request["reasoning"] = map[string]any{"effort": effort, "summary": "auto"}
			request["include"] = []any{"reasoning.encrypted_content"}
		} else if level == "" {
			// The upstream Responses engine disables reasoning when no level was
			// asked for, unless the model's map explicitly hides "off".
			levels, _ := model["thinkingLevelMap"].(map[string]any)
			if off, exists := levels["off"]; !exists || off != nil {
				effort := "none"
				if text, ok := off.(string); ok {
					effort = text
				}
				request["reasoning"] = map[string]any{"effort": effort}
			}
		}
	}
	if sampling, ok := opts.Values["samplingParams"].(map[string]any); ok {
		for key, value := range sampling {
			request[key] = value
		}
	}
	return request, nil
}

func inputParts(content any, supportsImages bool) []any {
	parts := []any{}
	if text, ok := content.(string); ok {
		return append(parts, map[string]any{"type": "input_text", "text": text})
	}
	blocks, _ := content.([]any)
	for _, item := range blocks {
		block, _ := item.(map[string]any)
		if block == nil {
			continue
		}
		switch block["type"] {
		case "text":
			text, _ := block["text"].(string)
			parts = append(parts, map[string]any{"type": "input_text", "text": text})
		case "image":
			if !supportsImages {
				continue
			}
			data, _ := block["data"].(string)
			mime, _ := block["mimeType"].(string)
			if data != "" {
				parts = append(parts, map[string]any{
					"type": "input_image", "detail": "auto", "image_url": "data:" + mime + ";base64," + data,
				})
			}
		}
	}
	return parts
}

func responsesToolOutput(content any, supportsImages bool) any {
	var text []string
	var images []any
	sawImage := false
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
			sawImage = true
			if !supportsImages {
				continue
			}
			data, _ := block["data"].(string)
			mime, _ := block["mimeType"].(string)
			if data != "" {
				images = append(images, map[string]any{
					"type": "input_image", "detail": "auto", "image_url": "data:" + mime + ";base64," + data,
				})
			}
		}
	}
	joined := strings.Join(text, "\n")
	if len(images) == 0 {
		switch {
		case joined != "":
			return joined
		case sawImage:
			return "(see attached image)"
		default:
			return "(no tool output)"
		}
	}
	parts := []any{}
	if joined != "" {
		parts = append(parts, map[string]any{"type": "input_text", "text": joined})
	}
	parts = append(parts, images...)
	return parts
}

type respEvent struct {
	Type        string          `json:"type"`
	OutputIndex int             `json:"output_index"`
	Response    *respResponse   `json:"response"`
	Item        json.RawMessage `json:"item"`
	Delta       string          `json:"delta"`
	Arguments   string          `json:"arguments"`
	Error       json.RawMessage `json:"error"`
	Code        string          `json:"code"`
	Message     string          `json:"message"`
}

type respResponse struct {
	ID                string          `json:"id"`
	Status            string          `json:"status"`
	Usage             *respUsage      `json:"usage"`
	Error             *respError      `json:"error"`
	IncompleteDetails *respIncomplete `json:"incomplete_details"`
	Output            []respItem      `json:"output"`
}

type respError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type respIncomplete struct {
	Reason string `json:"reason"`
}

type respUsage struct {
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	TotalTokens        int `json:"total_tokens"`
	InputTokensDetails *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
	OutputTokensDetails *struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
}

type respItem struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Content   []struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Refusal string `json:"refusal"`
	} `json:"content"`
	Summary []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"summary"`
}

type respState struct {
	kind      string
	block     int
	id        string
	name      string
	args      strings.Builder
	finalized bool
	textSeen  bool
	thinkSeen bool
}

func parseResponsesSSE(ctx context.Context, body io.Reader, r *reply) {
	states := map[int]*respState{}
	order := []int{}
	status := ""
	incomplete := ""
	terminal := false

	ensureTool := func(index int, id, name string) *respState {
		if state, ok := states[index]; ok {
			if state.kind != "function_call" {
				return nil
			}
			if state.id == "" && id != "" {
				state.id = id
				r.blocks[state.block]["id"] = id
			}
			if state.name == "" && name != "" {
				state.name = name
				r.blocks[state.block]["name"] = name
			}
			return state
		}
		if id == "" {
			id = r.newToolID()
		}
		block := r.startToolCall(id, name)
		state := &respState{kind: "function_call", block: block, id: id, name: name}
		states[index] = state
		order = append(order, index)
		return state
	}

	finishTools := func() error {
		for _, index := range order {
			state := states[index]
			if state == nil || state.finalized {
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
		var event respEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return fmt.Errorf("OpenCode Zen sent a line that is not JSON: %.80q", data)
		}
		if message := streamErrorMessage(event.Error); message != "" {
			return errors.New(message)
		}
		switch event.Type {
		case "response.output_item.added":
			item := decodeRespItem(event.Item)
			if item == nil {
				return nil
			}
			switch item.Type {
			case "function_call":
				state := ensureTool(event.OutputIndex, firstNonEmpty(item.CallID, item.ID), item.Name)
				if state != nil && item.Arguments != "" {
					state.args.WriteString(item.Arguments)
					r.toolCallDelta(state.block, item.Arguments)
				}
			case "reasoning":
				if state := states[event.OutputIndex]; state == nil {
					states[event.OutputIndex] = &respState{kind: "reasoning", block: -1}
				}
			case "message":
				if state := states[event.OutputIndex]; state == nil {
					states[event.OutputIndex] = &respState{kind: "message", block: -1}
				}
			}
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			state := states[event.OutputIndex]
			if state == nil {
				state = &respState{kind: "reasoning", block: -1}
				states[event.OutputIndex] = state
			}
			r.delta("thinking", event.Delta)
			state.thinkSeen = true
		case "response.reasoning_summary_part.done":
			if state := states[event.OutputIndex]; state != nil && state.kind == "reasoning" {
				r.delta("thinking", "\n\n")
			}
		case "response.output_text.delta", "response.refusal.delta":
			state := states[event.OutputIndex]
			if state == nil {
				state = &respState{kind: "message", block: -1}
				states[event.OutputIndex] = state
			}
			r.delta("text", event.Delta)
			state.textSeen = true
		case "response.function_call_arguments.delta":
			state := ensureTool(event.OutputIndex, "", "")
			if state != nil {
				state.args.WriteString(event.Delta)
				r.toolCallDelta(state.block, event.Delta)
			}
		case "response.function_call_arguments.done":
			state := ensureTool(event.OutputIndex, "", "")
			if state != nil {
				current := state.args.String()
				if after, ok := strings.CutPrefix(event.Arguments, current); ok && after != "" {
					r.toolCallDelta(state.block, after)
				}
				state.args.Reset()
				state.args.WriteString(event.Arguments)
			}
		case "response.output_item.done":
			item := decodeRespItem(event.Item)
			if item == nil {
				return nil
			}
			switch item.Type {
			case "function_call":
				state := ensureTool(event.OutputIndex, firstNonEmpty(item.CallID, item.ID), item.Name)
				if state != nil && !state.finalized {
					if item.Arguments != "" {
						current := state.args.String()
						if after, ok := strings.CutPrefix(item.Arguments, current); ok && after != "" {
							r.toolCallDelta(state.block, after)
						}
						state.args.Reset()
						state.args.WriteString(item.Arguments)
					}
					args, err := parseToolArguments(state.args.String())
					if err != nil {
						return err
					}
					r.finishToolCall(state.block, args)
					state.finalized = true
				}
			case "message":
				state := states[event.OutputIndex]
				var text strings.Builder
				for _, part := range item.Content {
					if part.Type == "output_text" {
						text.WriteString(part.Text)
					} else {
						text.WriteString(part.Refusal)
					}
				}
				if text.Len() > 0 && (state == nil || !state.textSeen) {
					r.delta("text", text.String())
					if state != nil {
						state.textSeen = true
					}
				}
			case "reasoning":
				state := states[event.OutputIndex]
				var text strings.Builder
				for _, part := range item.Summary {
					text.WriteString(part.Text)
				}
				for _, part := range item.Content {
					text.WriteString(part.Text)
				}
				if text.Len() > 0 && (state == nil || !state.thinkSeen) {
					r.delta("thinking", text.String())
					if state != nil {
						state.thinkSeen = true
					}
				}
			}
		case "response.completed", "response.incomplete", "response.done":
			if event.Type == "response.done" {
				return nil
			}
			if event.Response == nil {
				return errors.New("OpenCode Zen sent a terminal response without metadata")
			}
			if event.Response.Usage != nil {
				usage := event.Response.Usage
				cached := 0
				if usage.InputTokensDetails != nil {
					cached = usage.InputTokensDetails.CachedTokens
				}
				reasoning := 0
				if usage.OutputTokensDetails != nil {
					reasoning = usage.OutputTokensDetails.ReasoningTokens
				}
				setUsage(r.usage, max(0, usage.InputTokens-cached), usage.OutputTokens, cached, 0, reasoning, usage.TotalTokens)
			}
			status = event.Response.Status
			if event.Response.IncompleteDetails != nil {
				incomplete = event.Response.IncompleteDetails.Reason
			}
			terminal = true
		case "response.failed":
			message := "Unknown error (no error details in response)"
			if event.Response != nil && event.Response.Error != nil {
				message = firstNonEmpty(event.Response.Error.Message, event.Response.Error.Code)
			}
			return errors.New(message)
		case "error":
			return errors.New(firstNonEmpty(event.Message, event.Code, "OpenCode Zen returned an error"))
		}
		return nil
	})
	if err != nil && !errors.Is(err, io.EOF) {
		r.fail(err, ctx.Err() != nil)
		return
	}
	if !terminal {
		r.fail(errors.New("The OpenCode Responses stream ended before a terminal response event."), ctx.Err() != nil)
		return
	}
	if err := finishTools(); err != nil {
		r.fail(err, ctx.Err() != nil)
		return
	}
	reason := "stop"
	if status == "incomplete" && strings.Contains(strings.ToLower(incomplete), "max_output_tokens") {
		reason = "length"
	}
	r.done(reason)
}

func decodeRespItem(raw json.RawMessage) *respItem {
	var item respItem
	if len(raw) == 0 || json.Unmarshal(raw, &item) != nil {
		return nil
	}
	return &item
}

func streamErrorMessage(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value struct {
		Message string `json:"message"`
		Code    string `json:"code"`
		Type    string `json:"type"`
	}
	if json.Unmarshal(raw, &value) == nil {
		return firstNonEmpty(value.Message, value.Code, value.Type)
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
