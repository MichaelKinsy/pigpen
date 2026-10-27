// SPDX-License-Identifier: GPL-3.0-or-later
package piopencodefree

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// stream dispatches one model turn to Zen's Chat Completions or Responses
// endpoint, depending on the model's api. It returns at once; the work runs on
// its own goroutine and every failure becomes an error event on the stream.
func (c *client) stream(model, transcript map[string]any, opts sdk.ProviderStreamOptions) (*sdk.ModelEventStream, error) {
	out := sdk.CreateAssistantMessageEventStream()
	ctx := opts.Signal
	if ctx == nil {
		ctx = context.Background()
	}
	api, _ := model["api"].(string)
	go func() {
		switch api {
		case "", "openai-completions":
			c.runChat(ctx, out, model, transcript, opts)
		case "openai-responses":
			c.runResponses(ctx, out, model, transcript, opts)
		default:
			failStream(out, nil, fmt.Errorf("opencode-free does not support the model API %q", api), false)
		}
	}()
	return out, nil
}

// reply is the assistant message under construction. Each event carries a
// snapshot, so later changes never reach an event already delivered.
type reply struct {
	out       *sdk.ModelEventStream
	model     string
	api       string
	modelInfo map[string]any
	blocks    []map[string]any
	usage     map[string]any
	stop      string
	now       func() time.Time
	open      string
	onStream  func(any, map[string]any) error
	newID     func() string
}

func newReply(out *sdk.ModelEventStream, model map[string]any, opts ...sdk.ProviderStreamOptions) *reply {
	id, _ := model["id"].(string)
	api, _ := model["api"].(string)
	if api == "" {
		api = "openai-completions"
	}
	r := &reply{out: out, model: id, api: api, modelInfo: model, usage: zeroUsage(), stop: "stop", now: time.Now, newID: randomID}
	if len(opts) > 0 {
		r.onStream = opts[0].OnProviderStreamEvent
	}
	return r
}

func (r *reply) newToolID() string {
	if r.newID != nil {
		return r.newID()
	}
	return randomID()
}

func (r *reply) observe(data string) error {
	if r.onStream == nil {
		return nil
	}
	var event map[string]any
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		return err
	}
	return r.onStream(event, r.modelInfo)
}

func (r *reply) snapshot() map[string]any {
	blocks := make([]any, len(r.blocks))
	for i, block := range r.blocks {
		blocks[i] = cloneMap(block)
	}
	return map[string]any{
		"role": "assistant", "api": r.api, "provider": ProviderID, "model": r.model,
		"content": blocks, "stopReason": r.stop, "timestamp": r.now().UnixMilli(),
		"usage": cloneMap(r.usage),
	}
}

func zeroUsage() map[string]any {
	return map[string]any{
		"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0,
		"cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0},
	}
}

func (r *reply) start() {
	r.out.Push(map[string]any{"type": "start", "partial": r.snapshot()})
}

func (r *reply) closeOpen() {
	if r.open == "" {
		return
	}
	last := r.blocks[len(r.blocks)-1]
	key := "text"
	if r.open == "thinking" {
		key = "thinking"
	}
	r.out.Push(map[string]any{
		"type": r.open + "_end", "contentIndex": len(r.blocks) - 1,
		"content": last[key], "partial": r.snapshot(),
	})
	r.open = ""
}

func (r *reply) delta(kind, text string) {
	if text == "" {
		return
	}
	if r.open != kind {
		r.closeOpen()
		r.blocks = append(r.blocks, map[string]any{"type": kind, kind: ""})
		r.open = kind
		r.out.Push(map[string]any{
			"type": kind + "_start", "contentIndex": len(r.blocks) - 1,
			"partial": r.snapshot(),
		})
	}
	last := r.blocks[len(r.blocks)-1]
	last[kind] = last[kind].(string) + text
	r.out.Push(map[string]any{
		"type": kind + "_delta", "contentIndex": len(r.blocks) - 1,
		"delta": text, "partial": r.snapshot(),
	})
}

func (r *reply) startToolCall(id, name string) int {
	r.closeOpen()
	index := len(r.blocks)
	r.blocks = append(r.blocks, map[string]any{
		"type": "toolCall", "id": id, "name": name, "arguments": map[string]any{},
	})
	r.out.Push(map[string]any{"type": "toolcall_start", "contentIndex": index, "partial": r.snapshot()})
	return index
}

func (r *reply) toolCallDelta(index int, fragment string) {
	if fragment == "" {
		return
	}
	r.out.Push(map[string]any{
		"type": "toolcall_delta", "contentIndex": index,
		"delta": fragment, "partial": r.snapshot(),
	})
}

func (r *reply) finishToolCall(index int, args map[string]any) {
	if args == nil {
		args = map[string]any{}
	}
	block := r.blocks[index]
	block["arguments"] = cloneMap(args)
	r.out.Push(map[string]any{
		"type": "toolcall_end", "contentIndex": index,
		"toolCall": cloneMap(block), "partial": r.snapshot(),
	})
}

func (r *reply) hasToolCalls() bool {
	for _, block := range r.blocks {
		if block["type"] == "toolCall" {
			return true
		}
	}
	return false
}

func (r *reply) done(reason string) {
	if reason == "" {
		reason = "stop"
	}
	if reason == "stop" && r.hasToolCalls() {
		reason = "toolUse"
	}
	r.closeOpen()
	r.stop = reason
	r.out.Push(map[string]any{"type": "done", "reason": reason, "message": r.snapshot()})
}

func (r *reply) fail(err error, aborted bool) {
	reason, text := "error", err.Error()
	if aborted {
		reason, text = "aborted", "Request was aborted"
	}
	r.closeOpen()
	r.stop = reason
	msg := r.snapshot()
	msg["errorMessage"] = text
	r.out.Push(map[string]any{"type": "error", "reason": reason, "error": msg})
}

func failStream(out *sdk.ModelEventStream, model map[string]any, err error, aborted bool) {
	r := newReply(out, model)
	r.fail(err, aborted)
}

func setUsage(usage map[string]any, input, output, cacheRead, cacheWrite, reasoning, total int) {
	if total <= 0 {
		total = input + output + cacheRead + cacheWrite
	}
	usage["input"] = input
	usage["output"] = output
	usage["cacheRead"] = cacheRead
	usage["cacheWrite"] = cacheWrite
	usage["totalTokens"] = total
	if reasoning > 0 {
		usage["reasoning"] = reasoning
	}
}

// sseData calls fn for each data line of an SSE body. [DONE] is returned as
// io.EOF; blank lines and comments are skipped.
func sseData(r io.Reader, fn func(data string) error) error {
	reader := bufio.NewReaderSize(r, 64<<10)
	for {
		line, readErr := reader.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				return io.EOF
			}
			if data != "" {
				if err := fn(data); err != nil {
					return err
				}
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return io.EOF
			}
			return readErr
		}
	}
}

// send posts body and returns the first response that is final: a success, an
// error retrying cannot fix, or the last one once maxRetries is used up. It
// retries connection failures, timeouts and 408/409/429/5xx, honoring
// Retry-After and maxRetryDelayMs. timeoutMs bounds the wait for response
// headers only; the body then streams for as long as it takes.
func (c *client) send(ctx context.Context, endpoint string, body []byte, opts sdk.ProviderStreamOptions) (resp *http.Response, cancel context.CancelFunc, note string, err error) {
	retries := 0
	if n, ok := number(opts.Values["maxRetries"]); ok && n > 0 {
		retries = int(n)
	}
	maxDelay := 60 * time.Second
	if n, ok := number(opts.Values["maxRetryDelayMs"]); ok && n >= 0 {
		maxDelay = time.Duration(n) * time.Millisecond
	}
	for attempt := 0; ; attempt++ {
		resp, cancel, err = c.do(ctx, endpoint, body, opts)
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
				note = fmt.Sprintf(" (OpenCode asked to retry in %ds, more than maxRetryDelayMs %d)", int(wait.Seconds()), maxDelay.Milliseconds())
				return resp, cancel, note, nil
			}
			_ = resp.Body.Close()
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

func (c *client) do(ctx context.Context, endpoint string, body []byte, opts sdk.ProviderStreamOptions) (*http.Response, context.CancelFunc, error) {
	ctx, cancel := context.WithCancel(ctx)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		cancel()
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	for key, value := range providerHeaders() {
		req.Header.Set(key, value)
	}
	applyHeaders(req.Header, opts.Values)

	var timedOut atomic.Bool
	var timer *time.Timer
	timeout, hasTimeout := number(opts.Values["timeoutMs"])
	if hasTimeout && timeout > 0 {
		timer = time.AfterFunc(time.Duration(timeout)*time.Millisecond, func() { timedOut.Store(true); cancel() })
	}
	httpClient := c.http
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if timer != nil {
		timer.Stop()
	}
	if err != nil {
		cancel()
		switch {
		case timedOut.Load():
			return nil, nil, fmt.Errorf("request to OpenCode Zen timed out after %d ms", int(timeout))
		case isDialFailure(err):
			return nil, nil, fmt.Errorf("OpenCode Zen is not reachable at %s: %w", endpoint, err)
		case ctx.Err() == nil:
			return nil, nil, fmt.Errorf("request to OpenCode Zen failed: %w", err)
		}
		return nil, nil, err
	}
	return resp, cancel, nil
}

func retryable(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusConflict || status == http.StatusTooManyRequests || status >= 500
}

func backoff(attempt int) time.Duration {
	return 500 * time.Millisecond << min(attempt, 4)
}

func retryAfter(h http.Header, now time.Time) time.Duration {
	if ms, err := strconv.ParseFloat(h.Get("Retry-After-Ms"), 64); err == nil && ms >= 0 {
		return time.Duration(ms * float64(time.Millisecond))
	}
	value := strings.TrimSpace(h.Get("Retry-After"))
	if secs, err := strconv.ParseFloat(value, 64); err == nil && secs >= 0 {
		return time.Duration(secs * float64(time.Second))
	}
	if t, err := http.ParseTime(value); err == nil {
		return max(t.Sub(now), 0)
	}
	return -1
}

func isDialFailure(err error) bool {
	var netErr *net.OpError
	return errors.As(err, &netErr)
}

// applyHeaders merges options.headers over the defaults. A null value removes
// a default header of that name.
func applyHeaders(h http.Header, values map[string]any) {
	headers, _ := values["headers"].(map[string]any)
	for name, value := range headers {
		switch v := value.(type) {
		case nil:
			h.Del(name)
		case string:
			h.Set(name, v)
		}
	}
	if typed, ok := values["headers"].(map[string]*string); ok {
		for name, value := range typed {
			if value == nil {
				h.Del(name)
			} else {
				h.Set(name, *value)
			}
		}
	}
	if typed, ok := values["headers"].(map[string]string); ok {
		for name, value := range typed {
			h.Set(name, value)
		}
	}
}

// number reads a JSON number from an option map.
func number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		value, err := n.Float64()
		return value, err == nil
	}
	return 0, false
}

func httpError(resp *http.Response, note string) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	text := strings.TrimSpace(string(body))
	if text == "" {
		text = resp.Status
	}
	return fmt.Errorf("OpenCode Zen answered %d: %s%s", resp.StatusCode, text, note)
}

func observeResponse(opts sdk.ProviderStreamOptions, resp *http.Response, model map[string]any) error {
	if opts.OnResponse == nil {
		return nil
	}
	headers := make(map[string]string, len(resp.Header))
	for name, values := range resp.Header {
		headers[strings.ToLower(name)] = strings.Join(values, ", ")
	}
	return opts.OnResponse(map[string]any{"status": resp.StatusCode, "headers": headers}, model)
}

func invokePayload(opts sdk.ProviderStreamOptions, payload any, model map[string]any) (any, error) {
	if opts.OnPayload == nil {
		return payload, nil
	}
	replaced, err := opts.OnPayload(payload, model)
	if err != nil {
		return nil, err
	}
	if replaced != nil {
		return replaced, nil
	}
	return payload, nil
}
