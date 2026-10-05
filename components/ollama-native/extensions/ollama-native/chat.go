package ollamanative

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// stream sends one chat turn to Ollama's /api/chat and streams the answer back
// as PiG model events. It returns at once; the work runs on its own goroutine,
// and every failure becomes an error event on the stream.
func (c *client) stream(model, transcript map[string]any, opts sdk.ProviderStreamOptions) (*sdk.ModelEventStream, error) {
	out := sdk.CreateAssistantMessageEventStream()
	ctx := opts.Signal
	if ctx == nil {
		ctx = context.Background()
	}
	id, _ := model["id"].(string)
	go c.run(ctx, out, id, chatRequest(model, transcript, opts), apiKey(opts))
	return out, nil
}

func apiKey(opts sdk.ProviderStreamOptions) string {
	key, _ := opts.Values["apiKey"].(string)
	if key == localKey {
		return ""
	}
	return key
}

// reply is the assistant message under construction. Each event carries a
// snapshot of it, so later changes never reach an event already delivered.
type reply struct {
	model  string
	blocks []map[string]any
	usage  map[string]any
	stop   string
	now    func() time.Time
}

func (r *reply) snapshot() map[string]any {
	blocks := make([]any, len(r.blocks))
	for i, b := range r.blocks {
		cp := make(map[string]any, len(b))
		for k, v := range b {
			cp[k] = v
		}
		blocks[i] = cp
	}
	msg := map[string]any{
		"role": "assistant", "api": ProviderID, "provider": ProviderID, "model": r.model,
		"content": blocks, "stopReason": r.stop, "timestamp": r.now().UnixMilli(),
		"usage": r.usage,
	}
	return msg
}

func zeroUsage() map[string]any {
	return map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0,
		"cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}}
}

// chunk is one line of Ollama's streamed /api/chat answer.
type chunk struct {
	Error   string `json:"error"`
	Message struct {
		Content   string `json:"content"`
		Thinking  string `json:"thinking"`
		ToolCalls []struct {
			ID       string `json:"id"`
			Function struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls"`
	} `json:"message"`
	Done            bool   `json:"done"`
	DoneReason      string `json:"done_reason"`
	PromptEvalCount int    `json:"prompt_eval_count"`
	EvalCount       int    `json:"eval_count"`
}

func (c *client) run(ctx context.Context, out *sdk.ModelEventStream, model string, request map[string]any, key string) {
	r := &reply{model: model, usage: zeroUsage(), stop: "stop", now: time.Now}
	out.Push(map[string]any{"type": "start", "partial": r.snapshot()})

	fail := func(err error) {
		reason, text := "error", err.Error()
		if ctx.Err() != nil {
			reason, text = "aborted", "Request was aborted"
		}
		r.stop = reason
		msg := r.snapshot()
		msg["errorMessage"] = text
		out.Push(map[string]any{"type": "error", "reason": reason, "error": msg})
	}

	base := c.baseURL()
	resp, cancel, err := c.doChat(ctx, request, key)
	if err != nil {
		if isDialFailure(err) {
			err = unreachable(base, err)
		} else if ctx.Err() == nil {
			err = fmt.Errorf("request to Ollama at %s failed: %w", base, rootCause(err))
		}
		fail(err)
		return
	}
	defer cancel()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		text := apiError(resp)
		if resp.StatusCode == http.StatusNotFound && strings.Contains(strings.ToLower(text), "not found") {
			fail(fmt.Errorf("Ollama has no model %q (%s). Install it with: ollama pull %s", model, text, model))
			return
		}
		fail(fmt.Errorf("Ollama at %s answered %d: %s", base, resp.StatusCode, text))
		return
	}

	var open string // "text" or "thinking": the block deltas are going into, if any
	closeOpen := func() {
		if open == "" {
			return
		}
		last := r.blocks[len(r.blocks)-1]
		key := "text"
		if open == "thinking" {
			key = "thinking"
		}
		out.Push(map[string]any{"type": open + "_end", "contentIndex": len(r.blocks) - 1, "content": last[key], "partial": r.snapshot()})
		open = ""
	}
	delta := func(kind, text string) {
		if text == "" {
			return
		}
		if open != kind {
			closeOpen()
			r.blocks = append(r.blocks, map[string]any{"type": kind, kind: ""})
			open = kind
			out.Push(map[string]any{"type": kind + "_start", "contentIndex": len(r.blocks) - 1, "partial": r.snapshot()})
		}
		last := r.blocks[len(r.blocks)-1]
		last[kind] = last[kind].(string) + text
		out.Push(map[string]any{"type": kind + "_delta", "contentIndex": len(r.blocks) - 1, "delta": text, "partial": r.snapshot()})
	}

	toolCalls := 0
	reader := bufio.NewReaderSize(resp.Body, 64<<10)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			var ch chunk
			if err := json.Unmarshal(line, &ch); err != nil {
				fail(fmt.Errorf("Ollama sent a line that is not JSON: %.80q", line))
				return
			}
			if ch.Error != "" {
				fail(errors.New(ch.Error))
				return
			}
			delta("thinking", ch.Message.Thinking)
			delta("text", ch.Message.Content)
			for _, tc := range ch.Message.ToolCalls {
				closeOpen()
				id := tc.ID
				if id == "" {
					id = c.newID()
				}
				args := tc.Function.Arguments
				if args == nil {
					args = map[string]any{}
				}
				idx := len(r.blocks)
				call := map[string]any{"type": "toolCall", "id": id, "name": tc.Function.Name, "arguments": map[string]any{}}
				r.blocks = append(r.blocks, call)
				out.Push(map[string]any{"type": "toolcall_start", "contentIndex": idx, "partial": r.snapshot()})
				call["arguments"] = args
				raw, _ := json.Marshal(args)
				out.Push(map[string]any{"type": "toolcall_delta", "contentIndex": idx, "delta": string(raw), "partial": r.snapshot()})
				out.Push(map[string]any{"type": "toolcall_end", "contentIndex": idx, "toolCall": call, "partial": r.snapshot()})
				toolCalls++
			}
			if ch.Done {
				closeOpen()
				r.usage["input"], r.usage["output"] = ch.PromptEvalCount, ch.EvalCount
				r.usage["totalTokens"] = ch.PromptEvalCount + ch.EvalCount
				switch {
				case toolCalls > 0:
					r.stop = "toolUse"
				case ch.DoneReason == "length":
					r.stop = "length"
				default:
					r.stop = "stop"
				}
				out.Push(map[string]any{"type": "done", "reason": r.stop, "message": r.snapshot()})
				return
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				fail(errors.New("The response stream from Ollama ended before it finished (no final message)."))
			} else {
				fail(fmt.Errorf("reading Ollama's response failed: %w", rootCause(readErr)))
			}
			return
		}
	}
}

func (c *client) doChat(ctx context.Context, request map[string]any, key string) (*http.Response, context.CancelFunc, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL()+"/api/chat", strings.NewReader(string(body)))
	if err != nil {
		cancel()
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	return resp, cancel, nil
}
