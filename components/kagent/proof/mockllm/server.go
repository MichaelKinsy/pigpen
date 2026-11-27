package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type config struct {
	ChunkDelay time.Duration
	TTFT       time.Duration
	ChunkChars int
	Models     []string
}

// marshal encodes v as JSON without HTML escaping.
func marshal(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
	return bytes.TrimRight(buf.Bytes(), "\n")
}

func envInt(k string, d int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return d
}

func loadConfig() config {
	models := strings.Split(envOr("MOCK_MODELS", "mock-model"), ",")
	return config{
		ChunkDelay: time.Duration(envInt("MOCK_CHUNK_DELAY_MS", 5)) * time.Millisecond,
		TTFT:       time.Duration(envInt("MOCK_TTFT_MS", 50)) * time.Millisecond,
		ChunkChars: envInt("MOCK_CHUNK_CHARS", 8),
		Models:     models,
	}
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

var (
	logMu sync.Mutex
	stats = struct {
		sync.Mutex
		m map[string]int
	}{m: map[string]int{}}
	reqSeq atomic.Int64
)

func logJSON(v map[string]any) {
	v["ts"] = time.Now().UTC().Format(time.RFC3339Nano)
	b := marshal(v)
	logMu.Lock()
	os.Stdout.Write(append(b, '\n'))
	logMu.Unlock()
}

// authKinds reports which auth headers are present, never their values.
func authKinds(h http.Header) []string {
	var out []string
	if a := h.Get("Authorization"); a != "" {
		scheme, _, _ := strings.Cut(a, " ")
		out = append(out, "authorization:"+strings.ToLower(scheme))
	}
	for _, k := range []string{"X-Api-Key", "Api-Key", "X-Goog-Api-Key"} {
		if h.Get(k) != "" {
			out = append(out, strings.ToLower(k))
		}
	}
	return out
}

func headerNames(h http.Header) []string {
	out := make([]string, 0, len(h))
	for k := range h {
		out = append(out, strings.ToLower(k))
	}
	sort.Strings(out)
	return out
}

type server struct{ cfg config }

// sleep waits d or until the client goes away.
func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

type sseWriter struct {
	w     http.ResponseWriter
	f     http.Flusher
	ctx   context.Context
	delay time.Duration
	named bool
	seq   int
	first bool
}

func newSSE(w http.ResponseWriter, r *http.Request, cfg config, named bool) *sseWriter {
	f, _ := w.(http.Flusher)
	return &sseWriter{w: w, f: f, ctx: r.Context(), delay: cfg.ChunkDelay, named: named}
}

func (s *sseWriter) start(ttft time.Duration) bool {
	if !sleep(s.ctx, ttft) {
		return false
	}
	s.w.Header().Set("Content-Type", "text/event-stream")
	s.w.Header().Set("Cache-Control", "no-cache")
	s.w.WriteHeader(200)
	return true
}

// send writes one event; pause adds the per-chunk delay after it.
func (s *sseWriter) send(name string, v any, pause bool) bool {
	b := marshal(v)
	if s.named {
		fmt.Fprintf(s.w, "event: %s\n", name)
	}
	fmt.Fprintf(s.w, "data: %s\n\n", b)
	if s.f != nil {
		s.f.Flush()
	}
	if pause {
		return sleep(s.ctx, s.delay)
	}
	return s.ctx.Err() == nil
}

func (s *sseWriter) raw(line string) {
	fmt.Fprintf(s.w, "data: %s\n\n", line)
	if s.f != nil {
		s.f.Flush()
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	b := marshal(v)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(b)
}

func errJSON(w http.ResponseWriter, api string, code int, msg string) {
	if api == "anthropic" {
		writeJSON(w, code, map[string]any{"type": "error", "error": map[string]any{"type": "invalid_request_error", "message": msg}})
		return
	}
	writeJSON(w, code, map[string]any{"error": map[string]any{"message": msg, "type": "invalid_request_error"}})
}

// handleModel serves one model API endpoint using the given parser and renderer.
func (s *server) handleModel(api string, parse func(map[string]any) *conv, render func(*server, http.ResponseWriter, *http.Request, *conv, reply)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		entry := map[string]any{"path": r.URL.Path, "method": r.Method, "api": api, "seq": reqSeq.Add(1),
			"auth": authKinds(r.Header), "headers": headerNames(r.Header), "ua": r.Header.Get("User-Agent"),
			"remote": r.RemoteAddr}
		defer func() { entry["ms"] = time.Since(start).Milliseconds(); logJSON(entry) }()
		if r.Method != http.MethodPost {
			entry["status"] = 405
			errJSON(w, api, 405, "method not allowed")
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, 256<<20))
		entry["body_bytes"] = len(raw)
		var body map[string]any
		if err != nil || json.Unmarshal(bytes.TrimSpace(raw), &body) != nil {
			entry["status"] = 400
			errJSON(w, api, 400, "invalid JSON body")
			return
		}
		c := parse(body)
		if c.shouldHold() { // a model call that never answers: the turn is still running when the harness is made to crash
			entry["status"] = "held"
			<-r.Context().Done()
			return
		}
		rep := plan(c)
		if dir := os.Getenv("MOCK_DUMP_DIR"); dir != "" { // debugging aid: request bodies hold prompts, never credentials
			os.WriteFile(fmt.Sprintf("%s/req-%04d-%s.json", dir, entry["seq"], api), raw, 0o600)
		}
		in, out := usageFor(c, rep)
		entry["status"] = 200
		entry["model"] = c.Model
		entry["stream"] = c.Stream
		entry["messages"] = len(c.Msgs)
		entry["messages_hash"] = c.hash()
		entry["tools"] = c.toolNames()
		entry["step"] = rep.Step
		entry["usage"] = map[string]int{"in": in, "out": out}
		if res, cut := c.loggedResults(); res != "" {
			entry["results"] = res
			if cut {
				entry["results_truncated"] = true
			}
		}
		if rep.Call != nil {
			entry["call"] = rep.Call.Name
		} else {
			entry["text_len"] = len(rep.Text)
			if strings.HasPrefix(rep.Text, "BASKET-DONE") || strings.HasPrefix(rep.Text, "APPROVE-DONE") {
				entry["text"] = rep.Text
			}
		}
		stats.Lock()
		stats.m[api+":"+strings.SplitN(rep.Step, ":", 2)[0]]++
		stats.m[api+":total"]++
		stats.Unlock()
		render(s, w, r, c, rep)
	}
}

func (s *server) handleModels(w http.ResponseWriter, r *http.Request) {
	entry := map[string]any{"path": r.URL.Path, "method": r.Method, "api": "models", "auth": authKinds(r.Header), "headers": headerNames(r.Header), "status": 200}
	defer logJSON(entry)
	data := []map[string]any{}
	for _, m := range s.cfg.Models {
		data = append(data, map[string]any{"id": m, "object": "model", "created": 1700000000, "owned_by": "kagent-mock",
			"type": "model", "display_name": m, "created_at": "2023-11-14T22:13:20Z"})
	}
	first, last := "", ""
	if len(s.cfg.Models) > 0 {
		first, last = s.cfg.Models[0], s.cfg.Models[len(s.cfg.Models)-1]
	}
	writeJSON(w, 200, map[string]any{"object": "list", "data": data, "has_more": false, "first_id": first, "last_id": last})
}

func (s *server) mux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", s.handleModel("chat", parseChat, renderChat))
	mux.HandleFunc("/v1/responses", s.handleModel("responses", parseResponses, renderResponses))
	mux.HandleFunc("/v1/messages", s.handleModel("anthropic", parseAnthropic, renderAnthropic))
	mux.HandleFunc("/v1/messages/count_tokens", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 256<<20))
		var body map[string]any
		json.Unmarshal(raw, &body)
		c := parseAnthropic(body)
		logJSON(map[string]any{"path": r.URL.Path, "method": r.Method, "api": "anthropic", "auth": authKinds(r.Header), "body_bytes": len(raw), "status": 200})
		writeJSON(w, 200, map[string]any{"input_tokens": (c.InputBytes + 3) / 4})
	})
	mux.HandleFunc("/v1/models", s.handleModels)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok\n")) })
	mux.HandleFunc("/_mock/stats", func(w http.ResponseWriter, r *http.Request) {
		stats.Lock()
		defer stats.Unlock()
		writeJSON(w, 200, stats.m)
	})
	return mux
}

// ---------------- OpenAI Chat Completions ----------------

func renderChat(s *server, w http.ResponseWriter, r *http.Request, c *conv, rep reply) {
	in, out := usageFor(c, rep)
	usage := map[string]any{"prompt_tokens": in, "completion_tokens": out, "total_tokens": in + out,
		"prompt_tokens_details": map[string]any{"cached_tokens": 0}, "completion_tokens_details": map[string]any{"reasoning_tokens": 0}}
	id := "chatcmpl-" + shaHex(c.hash() + rep.Step)[:20]
	finish := "stop"
	var tcID string
	if rep.Call != nil {
		finish = "tool_calls"
		tcID = callID("call_", rep, c)
	}
	base := func(delta map[string]any, fin any) map[string]any {
		return map[string]any{"id": id, "object": "chat.completion.chunk", "created": 1700000000, "model": c.Model,
			"system_fingerprint": "fp_mock", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": fin}}}
	}
	if !c.Stream {
		sleep(r.Context(), s.cfg.TTFT)
		msg := map[string]any{"role": "assistant", "content": nil, "refusal": nil}
		if rep.Call != nil {
			msg["tool_calls"] = []any{map[string]any{"id": tcID, "type": "function", "function": map[string]any{"name": rep.Call.Name, "arguments": rep.Call.Args}}}
		} else {
			msg["content"] = rep.Text
		}
		writeJSON(w, 200, map[string]any{"id": id, "object": "chat.completion", "created": 1700000000, "model": c.Model,
			"system_fingerprint": "fp_mock", "choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": finish}}, "usage": usage})
		return
	}
	sw := newSSE(w, r, s.cfg, false)
	if !sw.start(s.cfg.TTFT) {
		return
	}
	if rep.Call != nil {
		sw.send("", base(map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{map[string]any{
			"index": 0, "id": tcID, "type": "function", "function": map[string]any{"name": rep.Call.Name, "arguments": ""}}}}, nil), true)
		for _, p := range chunks(rep.Call.Args, s.cfg.ChunkChars) {
			if !sw.send("", base(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "function": map[string]any{"arguments": p}}}}, nil), true) {
				return
			}
		}
	} else {
		sw.send("", base(map[string]any{"role": "assistant", "content": ""}, nil), true)
		for _, p := range chunks(rep.Text, s.cfg.ChunkChars) {
			if !sw.send("", base(map[string]any{"content": p}, nil), true) {
				return
			}
		}
	}
	sw.send("", base(map[string]any{}, finish), false)
	if c.IncludeUsage {
		sw.send("", map[string]any{"id": id, "object": "chat.completion.chunk", "created": 1700000000, "model": c.Model,
			"system_fingerprint": "fp_mock", "choices": []any{}, "usage": usage}, false)
	}
	sw.raw("[DONE]")
}

// ---------------- OpenAI Responses ----------------

func renderResponses(s *server, w http.ResponseWriter, r *http.Request, c *conv, rep reply) {
	in, out := usageFor(c, rep)
	usage := map[string]any{"input_tokens": in, "output_tokens": out, "total_tokens": in + out,
		"input_tokens_details": map[string]any{"cached_tokens": 0}, "output_tokens_details": map[string]any{"reasoning_tokens": 0}}
	rid := "resp_" + shaHex(c.hash() + rep.Step)[:20]
	resp := func(status string, output []any, withUsage bool) map[string]any {
		m := map[string]any{"id": rid, "object": "response", "created_at": 1700000000, "status": status, "model": c.Model,
			"output": output, "error": nil, "incomplete_details": nil, "parallel_tool_calls": true, "tool_choice": "auto",
			"tools": []any{}, "store": false}
		if withUsage {
			m["usage"] = usage
		}
		return m
	}
	var itemDone map[string]any
	var itemID string
	if rep.Call != nil {
		itemID = callID("fc_", rep, c)
		itemDone = map[string]any{"id": itemID, "type": "function_call", "status": "completed", "call_id": callID("call_", rep, c),
			"name": rep.Call.Name, "arguments": rep.Call.Args}
		if rep.Call.Namespace != "" {
			itemDone["namespace"] = rep.Call.Namespace
		}
	} else {
		itemID = "msg_" + shaHex(c.hash() + rep.Step)[:20]
		itemDone = map[string]any{"id": itemID, "type": "message", "status": "completed", "role": "assistant",
			"content": []any{map[string]any{"type": "output_text", "text": rep.Text, "annotations": []any{}}}}
	}
	if !c.Stream {
		sleep(r.Context(), s.cfg.TTFT)
		writeJSON(w, 200, resp("completed", []any{itemDone}, true))
		return
	}
	sw := newSSE(w, r, s.cfg, true)
	if !sw.start(s.cfg.TTFT) {
		return
	}
	ev := func(typ string, m map[string]any, pause bool) bool {
		m["type"] = typ
		m["sequence_number"] = sw.seq
		sw.seq++
		return sw.send(typ, m, pause)
	}
	ev("response.created", map[string]any{"response": resp("in_progress", []any{}, false)}, false)
	ev("response.in_progress", map[string]any{"response": resp("in_progress", []any{}, false)}, false)
	if rep.Call != nil {
		added := map[string]any{"id": itemID, "type": "function_call", "status": "in_progress", "call_id": itemDone["call_id"],
			"name": rep.Call.Name, "arguments": ""}
		if rep.Call.Namespace != "" {
			added["namespace"] = rep.Call.Namespace
		}
		ev("response.output_item.added", map[string]any{"output_index": 0, "item": added}, true)
		for _, p := range chunks(rep.Call.Args, s.cfg.ChunkChars) {
			if !ev("response.function_call_arguments.delta", map[string]any{"item_id": itemID, "output_index": 0, "delta": p}, true) {
				return
			}
		}
		ev("response.function_call_arguments.done", map[string]any{"item_id": itemID, "output_index": 0, "name": rep.Call.Name, "arguments": rep.Call.Args}, false)
	} else {
		ev("response.output_item.added", map[string]any{"output_index": 0, "item": map[string]any{"id": itemID, "type": "message",
			"status": "in_progress", "role": "assistant", "content": []any{}}}, false)
		part := map[string]any{"type": "output_text", "text": "", "annotations": []any{}}
		ev("response.content_part.added", map[string]any{"item_id": itemID, "output_index": 0, "content_index": 0, "part": part}, true)
		for _, p := range chunks(rep.Text, s.cfg.ChunkChars) {
			if !ev("response.output_text.delta", map[string]any{"item_id": itemID, "output_index": 0, "content_index": 0, "delta": p}, true) {
				return
			}
		}
		ev("response.output_text.done", map[string]any{"item_id": itemID, "output_index": 0, "content_index": 0, "text": rep.Text}, false)
		ev("response.content_part.done", map[string]any{"item_id": itemID, "output_index": 0, "content_index": 0,
			"part": map[string]any{"type": "output_text", "text": rep.Text, "annotations": []any{}}}, false)
	}
	ev("response.output_item.done", map[string]any{"output_index": 0, "item": itemDone}, false)
	ev("response.completed", map[string]any{"response": resp("completed", []any{itemDone}, true)}, false)
}

// ---------------- Anthropic Messages ----------------

func renderAnthropic(s *server, w http.ResponseWriter, r *http.Request, c *conv, rep reply) {
	in, out := usageFor(c, rep)
	mid := "msg_" + shaHex(c.hash() + rep.Step)[:20]
	stop := "end_turn"
	var block map[string]any
	if rep.Call != nil {
		stop = "tool_use"
		var input any
		json.Unmarshal([]byte(rep.Call.Args), &input)
		block = map[string]any{"type": "tool_use", "id": callID("toolu_", rep, c), "name": rep.Call.Name, "input": input}
	} else {
		block = map[string]any{"type": "text", "text": rep.Text}
	}
	usageFull := func(o int) map[string]any {
		return map[string]any{"input_tokens": in, "output_tokens": o, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 0}
	}
	if !c.Stream {
		sleep(r.Context(), s.cfg.TTFT)
		writeJSON(w, 200, map[string]any{"id": mid, "type": "message", "role": "assistant", "model": c.Model,
			"content": []any{block}, "stop_reason": stop, "stop_sequence": nil, "usage": usageFull(out)})
		return
	}
	sw := newSSE(w, r, s.cfg, true)
	if !sw.start(s.cfg.TTFT) {
		return
	}
	sw.send("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": mid, "type": "message", "role": "assistant",
		"model": c.Model, "content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": usageFull(1)}}, false)
	startBlock := map[string]any{}
	for k, v := range block {
		startBlock[k] = v
	}
	if rep.Call != nil {
		startBlock["input"] = map[string]any{}
	} else {
		startBlock["text"] = ""
	}
	sw.send("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": startBlock}, false)
	sw.send("ping", map[string]any{"type": "ping"}, true)
	if rep.Call != nil {
		for _, p := range chunks(rep.Call.Args, s.cfg.ChunkChars) {
			if !sw.send("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": p}}, true) {
				return
			}
		}
	} else {
		for _, p := range chunks(rep.Text, s.cfg.ChunkChars) {
			if !sw.send("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": p}}, true) {
				return
			}
		}
	}
	sw.send("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0}, false)
	sw.send("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop, "stop_sequence": nil},
		"usage": map[string]any{"input_tokens": in, "output_tokens": out, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 0}}, false)
	sw.send("message_stop", map[string]any{"type": "message_stop"}, false)
}
