package ollamanative

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// stream sends one chat turn to Ollama's /api/chat and streams the answer back
// as PiG model events. It returns at once; the work runs on its own goroutine,
// and every failure becomes an error event on the stream.
func (c *client) stream(model, transcript map[string]any, opts sdk.ProviderStreamOptions) (*sdk.ModelEventStream, error) {
	if err := refuseOptions(opts); err != nil {
		return nil, err
	}
	out := sdk.CreateAssistantMessageEventStream()
	ctx := opts.Signal
	if ctx == nil {
		ctx = context.Background()
	}
	go c.run(ctx, out, model, chatRequest(model, transcript, opts), opts)
	return out, nil
}

// apiKey is the bearer token to send, or "" for none: the resolved API key
// unless it is the placeholder PiG wants for every provider, else the
// provider-scoped OLLAMA_API_KEY.
func apiKey(opts sdk.ProviderStreamOptions) string {
	if key, _ := opts.Values["apiKey"].(string); key != "" && key != localKey {
		return key
	}
	return envValue(opts.Values, "OLLAMA_API_KEY")
}

// streamBase is the provider-scoped OLLAMA_HOST when the caller gave one, else
// the process's.
func (c *client) streamBase(opts sdk.ProviderStreamOptions) string {
	if host := envValue(opts.Values, "OLLAMA_HOST"); host != "" {
		return normalizeHost(host)
	}
	return c.baseURL()
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

func (c *client) run(ctx context.Context, out *sdk.ModelEventStream, modelInfo map[string]any, request any, opts sdk.ProviderStreamOptions) {
	model, _ := modelInfo["id"].(string)
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

	// onPayload may replace the request; nil keeps it (Pi: undefined).
	if opts.OnPayload != nil {
		replaced, err := opts.OnPayload(request, modelInfo)
		if err != nil {
			fail(err)
			return
		}
		if replaced != nil {
			request = replaced
		}
	}
	body, err := json.Marshal(request)
	if err != nil {
		fail(fmt.Errorf("the request for Ollama cannot be encoded: %w", err))
		return
	}

	base := c.streamBase(opts)
	resp, cancel, note, err := c.send(ctx, base, body, opts)
	if err != nil {
		fail(err)
		return
	}
	defer cancel()
	defer resp.Body.Close()
	if opts.OnResponse != nil {
		headers := make(map[string]string, len(resp.Header))
		for name, values := range resp.Header {
			headers[strings.ToLower(name)] = strings.Join(values, ", ")
		}
		if err := opts.OnResponse(map[string]any{"status": resp.StatusCode, "headers": headers}, modelInfo); err != nil {
			fail(err)
			return
		}
	}
	if resp.StatusCode != http.StatusOK {
		text := apiError(resp)
		if resp.StatusCode == http.StatusNotFound && strings.Contains(strings.ToLower(text), "not found") {
			fail(fmt.Errorf("Ollama has no model %q (%s). Install it with: ollama pull %s", model, text, model))
			return
		}
		fail(fmt.Errorf("Ollama at %s answered %d: %s%s", base, resp.StatusCode, text, note))
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
			if opts.OnProviderStreamEvent != nil {
				var event map[string]any
				if err := json.Unmarshal(line, &event); err != nil {
					fail(fmt.Errorf("Ollama sent a line that is not a JSON object: %.80q", line))
					return
				}
				if err := opts.OnProviderStreamEvent(event, modelInfo); err != nil {
					fail(err)
					return
				}
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

// send posts the request and returns the first response that is final: a
// success, an error that retrying cannot fix, or the last one once the retries
// asked for with maxRetries are used up. Retried are connection failures,
// timeouts and the statuses Pi's provider SDKs retry (408, 409, 429, 5xx),
// waiting as the server asks (Retry-After) or 0.5 s doubling to 8 s, and
// failing at once, with the requested delay in the message, when that wait is
// more than maxRetryDelayMs (default 60 s, 0 for no cap). timeoutMs bounds the
// wait for the response headers of each attempt, as the SDKs' request timeout
// does; the body then streams for as long as it takes.
func (c *client) send(ctx context.Context, base string, body []byte, opts sdk.ProviderStreamOptions) (resp *http.Response, cancel context.CancelFunc, note string, err error) {
	retries := 0
	if n, ok := number(opts.Values["maxRetries"]); ok && n > 0 {
		retries = int(n)
	}
	maxDelay := 60 * time.Second
	if n, ok := number(opts.Values["maxRetryDelayMs"]); ok && n >= 0 {
		maxDelay = time.Duration(n) * time.Millisecond
	}
	for attempt := 0; ; attempt++ {
		resp, cancel, err = c.doChat(ctx, base, body, opts)
		var wait time.Duration
		switch {
		case err != nil:
			if ctx.Err() != nil || attempt >= retries {
				return nil, nil, "", err
			}
			wait = backoff(attempt)
		case retryable(resp.StatusCode) && attempt < retries:
			wait = retryAfter(resp.Header, time.Now())
			if wait < 0 {
				wait = backoff(attempt)
			}
			if maxDelay > 0 && wait > maxDelay {
				// Fail now and let the caller's own retry logic decide, as Pi does.
				note = fmt.Sprintf(" (Ollama asked to retry in %ds, more than maxRetryDelayMs %d)", int(wait.Seconds()), maxDelay.Milliseconds())
				return resp, cancel, note, nil
			}
			resp.Body.Close()
			cancel()
		default:
			return resp, cancel, note, nil
		}
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return nil, nil, "", ctx.Err()
		}
	}
}

func retryable(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusConflict || status == http.StatusTooManyRequests || status >= 500
}

func backoff(attempt int) time.Duration {
	return 500 * time.Millisecond << min(attempt, 4) // 0.5 s doubling, 8 s at most
}

// retryAfter reads Retry-After (seconds or an HTTP date) or the millisecond
// form retry-after-ms; -1 means the server did not say.
func retryAfter(h http.Header, now time.Time) time.Duration {
	if ms, err := strconv.ParseFloat(h.Get("Retry-After-Ms"), 64); err == nil && ms >= 0 {
		return time.Duration(ms * float64(time.Millisecond))
	}
	v := strings.TrimSpace(h.Get("Retry-After"))
	if secs, err := strconv.ParseFloat(v, 64); err == nil && secs >= 0 {
		return time.Duration(secs * float64(time.Second))
	}
	if t, err := http.ParseTime(v); err == nil {
		return max(t.Sub(now), 0)
	}
	return -1
}

func (c *client) doChat(ctx context.Context, base string, body []byte, opts sdk.ProviderStreamOptions) (*http.Response, context.CancelFunc, error) {
	ctx, cancel := context.WithCancel(ctx)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/chat", bytes.NewReader(body))
	if err != nil {
		cancel()
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if key := apiKey(opts); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	applyHeaders(req.Header, opts.Values)

	var timedOut atomic.Bool
	var timer *time.Timer
	timeout, hasTimeout := number(opts.Values["timeoutMs"])
	if hasTimeout && timeout > 0 {
		timer = time.AfterFunc(time.Duration(timeout)*time.Millisecond, func() { timedOut.Store(true); cancel() })
	}
	resp, err := c.http.Do(req)
	if timer != nil {
		timer.Stop()
	}
	if err != nil {
		cancel()
		switch {
		case timedOut.Load():
			return nil, nil, fmt.Errorf("request to Ollama at %s timed out after %d ms", base, int(timeout))
		case isDialFailure(err):
			return nil, nil, unreachable(base, err)
		case ctx.Err() == nil:
			return nil, nil, fmt.Errorf("request to Ollama at %s failed: %w", base, rootCause(err))
		}
		return nil, nil, err
	}
	return resp, cancel, nil
}
