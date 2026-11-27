package autoresearch

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
)

var fakeHostSeq atomic.Int64

type hostCall struct {
	Method string
	Args   map[string]any
}

type registeredTool struct {
	Name          string         `json:"name"`
	Label         string         `json:"label"`
	Description   string         `json:"description"`
	DefaultActive *bool          `json:"default_active"`
	Parameters    map[string]any `json:"parameters"`
}

type registeredHandler struct {
	Event     string `json:"event"`
	HandlerID int    `json:"handler_id"`
}

type hostOptions struct {
	Mode               string
	HasUI              *bool
	Cwd                string
	SessionID          string
	ActiveTools        []string
	Branch             []map[string]any
	HasPendingMessages bool
}

type fakeHost struct {
	t           *testing.T
	nc          net.Conn
	opts        hostOptions
	runDone     chan error
	writeMu     sync.Mutex
	mu          sync.Mutex
	calls       []hostCall
	toolUpdates []map[string]any
	pending     map[string]chan json.RawMessage
	failures    map[string]string
	tools       map[string]registeredTool
	handlers    map[string][]int
	commands    map[string]bool
	nextID      int
}

type wireFrame struct {
	Type     string `json:"type"`
	ID       string `json:"id,omitempty"`
	Register *struct {
		Tools    []registeredTool    `json:"tools"`
		Handlers []registeredHandler `json:"handlers"`
		Commands []struct {
			Name string `json:"name"`
		} `json:"commands"`
	} `json:"register,omitempty"`
	Response *struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"response,omitempty"`
	Call *struct {
		Method string          `json:"method"`
		Args   json.RawMessage `json:"args"`
	} `json:"call,omitempty"`
	Notify *struct {
		Method string          `json:"method"`
		Args   json.RawMessage `json:"args"`
	} `json:"notify,omitempty"`
}

func startHost(t *testing.T, ext *sdk.Extension, opts hostOptions) *fakeHost {
	t.Helper()
	if opts.Mode == "" {
		opts.Mode = "tui"
	}
	hasUI := opts.Mode == "tui" || opts.Mode == "rpc"
	if opts.HasUI != nil {
		hasUI = *opts.HasUI
	}
	if opts.Cwd == "" {
		opts.Cwd = t.TempDir()
	}
	if opts.SessionID == "" {
		opts.SessionID = fmt.Sprintf("ar-sess-%d", fakeHostSeq.Add(1))
	}
	t.Cleanup(func() { lifecycle.End(opts.SessionID) })
	if opts.ActiveTools == nil {
		opts.ActiveTools = []string{"read", "bash"}
	}
	hostSide, extSide := net.Pipe()
	h := &fakeHost{
		t:        t,
		nc:       hostSide,
		opts:     opts,
		runDone:  make(chan error, 1),
		pending:  map[string]chan json.RawMessage{},
		failures: map[string]string{},
		tools:    map[string]registeredTool{},
		handlers: map[string][]int{},
		commands: map[string]bool{},
	}
	go func() { h.runDone <- ext.RunWithConn(extSide) }()
	first := h.read()
	for first.Type == "call" && first.Call != nil {
		h.answer(first)
		first = h.read()
	}
	if first.Type != "register" || first.Register == nil {
		t.Fatalf("expected register, got %+v", first)
	}
	for _, tl := range first.Register.Tools {
		h.tools[tl.Name] = tl
	}
	for _, hd := range first.Register.Handlers {
		h.handlers[hd.Event] = append(h.handlers[hd.Event], hd.HandlerID)
	}
	for _, cmd := range first.Register.Commands {
		h.commands[cmd.Name] = true
	}
	state, _ := json.Marshal(map[string]any{"hasUI": hasUI})
	h.write(map[string]any{"type": "ready", "ready": map[string]any{
		"session_name": "",
		"cwd":          opts.Cwd,
		"mode":         opts.Mode,
		"width":        80,
		"state":        json.RawMessage(state),
	}})
	go h.serve()
	t.Cleanup(h.stop)
	return h
}

func (h *fakeHost) activeTools() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.opts.ActiveTools...)
}

func (h *fakeHost) callsTo(method string) []hostCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []hostCall
	for _, c := range h.calls {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

func (h *fakeHost) updatesSnapshot() []map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]map[string]any(nil), h.toolUpdates...)
}

func (h *fakeHost) toolSchemaProps(name string) map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	tl := h.tools[name]
	props, _ := tl.Parameters["properties"].(map[string]any)
	return props
}

func (h *fakeHost) startToolCallAsync(name string, params map[string]any) (reqID string, done <-chan struct {
	res    map[string]any
	errMsg string
}) {
	h.t.Helper()
	argv, _ := json.Marshal(params)
	h.mu.Lock()
	h.nextID++
	id := fmt.Sprintf("r%d", h.nextID)
	ch := make(chan json.RawMessage, 1)
	h.pending[id] = ch
	h.mu.Unlock()
	h.write(map[string]any{
		"type": "request",
		"id":   id,
		"request": map[string]any{
			"method":       "tool_call",
			"tool":         name,
			"tool_call_id": "call-async",
			"args":         json.RawMessage(argv),
		},
	})
	outCh := make(chan struct {
		res    map[string]any
		errMsg string
	}, 1)
	go func() {
		raw := <-ch
		h.mu.Lock()
		failure := h.failures[id]
		h.mu.Unlock()
		var m map[string]any
		if len(raw) > 0 && string(raw) != "null" {
			_ = json.Unmarshal(raw, &m)
		}
		outCh <- struct {
			res    map[string]any
			errMsg string
		}{res: m, errMsg: failure}
	}()
	return id, outCh
}

func (h *fakeHost) cancelRequest(reqID string) {
	h.write(map[string]any{
		"type": "cancel",
		"id":   reqID,
		"cancel": map[string]any{
			"request_id": reqID,
			"reason":     "user aborted",
		},
	})
}

func (h *fakeHost) callTool(name string, params map[string]any) (map[string]any, string) {
	h.t.Helper()
	if _, ok := h.tools[name]; !ok {
		h.t.Fatalf("tool %q is not registered", name)
	}
	argv, _ := json.Marshal(params)
	raw, errMsg := h.roundTrip(map[string]any{
		"method":       "tool_call",
		"tool":         name,
		"tool_call_id": "call-1",
		"args":         json.RawMessage(argv),
	})
	if errMsg != "" {
		return nil, errMsg
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		h.t.Fatalf("decode tool_call response %s: %v", string(raw), err)
	}
	return out, ""
}

func (h *fakeHost) emitEvent(event string, payload map[string]any) (map[string]any, string) {
	h.t.Helper()
	ids := h.handlers[event]
	if len(ids) == 0 {
		h.t.Fatalf("no handler registered for event %q", event)
	}
	if payload == nil {
		payload = map[string]any{}
	}
	var last map[string]any
	for _, id := range ids {
		args, _ := json.Marshal(payload)
		raw, errMsg := h.roundTrip(map[string]any{
			"method":     "event",
			"event":      event,
			"handler_id": id,
			"args":       json.RawMessage(args),
		})
		if errMsg != "" {
			return nil, errMsg
		}
		if len(raw) > 0 && string(raw) != "null" {
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err == nil {
				last = m
			}
		}
	}
	return last, ""
}

func (h *fakeHost) runCommand(name, args string) string {
	h.t.Helper()
	rawArgs, _ := json.Marshal(args)
	_, errMsg := h.roundTrip(map[string]any{
		"method": "command",
		"tool":   name,
		"args":   json.RawMessage(rawArgs),
	})
	return errMsg
}

func (h *fakeHost) fireBeforeAgentStart(payload map[string]any) (string, bool) {
	h.t.Helper()
	res, errMsg := h.emitEvent(sdk.EventBeforeAgentStart, payload)
	if errMsg != "" {
		h.t.Fatalf("before_agent_start error: %s", errMsg)
	}
	if res == nil {
		return "", false
	}
	inner, _ := res["_pigPromptResult"].(map[string]any)
	if inner == nil {
		return "", false
	}
	sp, ok := inner["systemPrompt"].(string)
	return sp, ok
}

func (h *fakeHost) read() wireFrame {
	var hdr [4]byte
	if _, err := io.ReadFull(h.nc, hdr[:]); err != nil {
		h.t.Fatalf("read header: %v", err)
	}
	data := make([]byte, binary.BigEndian.Uint32(hdr[:]))
	if _, err := io.ReadFull(h.nc, data); err != nil {
		h.t.Fatalf("read frame: %v", err)
	}
	var f wireFrame
	if err := json.Unmarshal(data, &f); err != nil {
		h.t.Fatalf("decode frame %s: %v", data, err)
	}
	return f
}

func (h *fakeHost) write(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		h.t.Errorf("marshal: %v", err)
		return
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(data)))
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	if _, err := h.nc.Write(hdr[:]); err != nil {
		return
	}
	_, _ = h.nc.Write(data)
}

func (h *fakeHost) serve() {
	for {
		var hdr [4]byte
		if _, err := io.ReadFull(h.nc, hdr[:]); err != nil {
			return
		}
		data := make([]byte, binary.BigEndian.Uint32(hdr[:]))
		if _, err := io.ReadFull(h.nc, data); err != nil {
			return
		}
		var f wireFrame
		if err := json.Unmarshal(data, &f); err != nil {
			continue
		}
		switch f.Type {
		case "notify":
			if f.Notify != nil && f.Notify.Method == "tool_update" {
				var m map[string]any
				if json.Unmarshal(f.Notify.Args, &m) == nil {
					h.mu.Lock()
					h.toolUpdates = append(h.toolUpdates, m)
					h.mu.Unlock()
				}
			}
		case "call":
			if f.Call != nil {
				h.answer(f)
			}
		case "response":
			h.mu.Lock()
			ch := h.pending[f.ID]
			delete(h.pending, f.ID)
			h.mu.Unlock()
			if ch == nil || f.Response == nil {
				continue
			}
			if f.Response.Error != nil {
				h.mu.Lock()
				h.failures[f.ID] = f.Response.Error.Message
				h.mu.Unlock()
			}
			ch <- f.Response.Result
		}
	}
}

func (h *fakeHost) answer(f wireFrame) {
	args := map[string]any{}
	_ = json.Unmarshal(f.Call.Args, &args)
	h.mu.Lock()
	h.calls = append(h.calls, hostCall{Method: f.Call.Method, Args: args})
	switch f.Call.Method {
	case "appendEntry":
		ct, _ := args["customType"].(string)
		if ct == "" {
			ct, _ = args["custom_type"].(string)
		}
		id := fmt.Sprintf("e-%d", len(h.opts.Branch)+1)
		h.opts.Branch = append(h.opts.Branch, map[string]any{
			"id":         id,
			"type":       "custom",
			"customType": ct,
			"data":       args["data"],
		})
	case "setActiveTools":
		if rawTools, ok := args["tools"].([]any); ok {
			next := make([]string, 0, len(rawTools))
			for _, item := range rawTools {
				if s, ok := item.(string); ok {
					next = append(next, s)
				}
			}
			h.opts.ActiveTools = next
		}
	case "registerTool":
		rawTool, err := json.Marshal(args)
		if err == nil {
			var tl registeredTool
			if json.Unmarshal(rawTool, &tl) == nil && tl.Name != "" {
				h.tools[tl.Name] = tl
			}
		}
	}
	active := append([]string(nil), h.opts.ActiveTools...)
	sessID := h.opts.SessionID
	branch := append([]map[string]any(nil), h.opts.Branch...)
	hasPending := h.opts.HasPendingMessages
	h.mu.Unlock()

	go func() {
		var result any
		switch f.Call.Method {
		case "sessionRead":
			sub, _ := args["method"].(string)
			switch sub {
			case "getSessionId":
				result = sessID
			case "getSessionFile":
				result = nil
			case "getBranch":
				result = branch
			case "getEntries":
				result = branch
			default:
				result = nil
			}
		case "getSessionID":
			result = map[string]any{"sessionId": sessID}
		case "getSessionFile":
			result = map[string]any{"sessionFile": nil}
		case "getActiveTools":
			result = map[string]any{"tools": active}
		case "hasPendingMessages":
			result = map[string]any{"pending": hasPending}
		default:
			result = map[string]any{}
		}
		h.write(map[string]any{
			"type": "call_result",
			"id":   f.ID,
			"call_result": map[string]any{
				"result": result,
			},
		})
	}()
}

func (h *fakeHost) roundTrip(request map[string]any) (json.RawMessage, string) {
	h.t.Helper()
	h.mu.Lock()
	h.nextID++
	id := fmt.Sprintf("r%d", h.nextID)
	ch := make(chan json.RawMessage, 1)
	h.pending[id] = ch
	h.mu.Unlock()
	h.write(map[string]any{"type": "request", "id": id, "request": request})
	select {
	case result := <-ch:
		h.mu.Lock()
		failure := h.failures[id]
		h.mu.Unlock()
		return result, failure
	case <-time.After(15 * time.Second):
		h.t.Fatalf("no response to %v within 15s", request["method"])
		return nil, ""
	}
}

func (h *fakeHost) stop() {
	h.write(map[string]any{"type": "shutdown", "shutdown": map[string]any{"reason": "test"}})
	select {
	case <-h.runDone:
	case <-time.After(10 * time.Second):
		h.t.Errorf("extension did not stop after shutdown")
	}
	_ = h.nc.Close()
}
