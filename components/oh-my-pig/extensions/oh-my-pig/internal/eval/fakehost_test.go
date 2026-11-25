package eval

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

type registeredTool struct {
	Name          string         `json:"name"`
	Label         string         `json:"label"`
	Description   string         `json:"description"`
	ExecutionMode string         `json:"execution_mode"`
	Parameters    map[string]any `json:"parameters"`
}

type hostOptions struct {
	Cwd            string
	SessionID      string
	EmptySessionID bool
	Settings       map[string]any
	OnExecTool     func(name string, args map[string]any) sdk.AgentToolCallOutcome
}

type fakeHost struct {
	t        *testing.T
	nc       net.Conn
	opts     hostOptions
	runDone  chan error
	writeMu  sync.Mutex
	mu       sync.Mutex
	pending  map[string]chan json.RawMessage
	failures map[string]string
	tools    map[string]registeredTool
	handlers map[string][]int
	updates  []json.RawMessage
	nextID   int
}

type wireFrame struct {
	Type     string `json:"type"`
	ID       string `json:"id,omitempty"`
	Register *struct {
		Tools    []registeredTool `json:"tools"`
		Handlers []struct {
			Event     string `json:"event"`
			HandlerID int    `json:"handler_id"`
		} `json:"handlers"`
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
	if opts.Cwd == "" {
		opts.Cwd = t.TempDir()
	}
	if opts.SessionID == "" && !opts.EmptySessionID {
		opts.SessionID = "test-session-" + newRandomID()
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
	stateMap := map[string]any{"hasUI": false}
	if opts.Settings != nil {
		stateMap["settings"] = opts.Settings
	}
	state, _ := json.Marshal(stateMap)
	h.write(map[string]any{"type": "ready", "ready": map[string]any{
		"session_name": "",
		"cwd":          opts.Cwd,
		"mode":         "print",
		"width":        80,
		"state":        json.RawMessage(state),
	}})
	go h.serve()
	t.Cleanup(h.stop)
	return h
}

func (h *fakeHost) callTool(name string, params map[string]any) (json.RawMessage, string) {
	h.t.Helper()
	if _, ok := h.tools[name]; !ok {
		h.t.Fatalf("tool %q is not registered", name)
	}
	argv, _ := json.Marshal(params)
	return h.roundTrip(map[string]any{
		"method":       "tool_call",
		"tool":         name,
		"tool_call_id": "call-" + newRandomID(),
		"args":         json.RawMessage(argv),
	})
}

func (h *fakeHost) startToolCallAsync(name string, params map[string]any) (reqID string, ch chan json.RawMessage) {
	h.t.Helper()
	argv, _ := json.Marshal(params)
	h.mu.Lock()
	h.nextID++
	reqID = fmt.Sprintf("req-%d", h.nextID)
	ch = make(chan json.RawMessage, 1)
	h.pending[reqID] = ch
	h.mu.Unlock()
	h.write(map[string]any{
		"type": "request",
		"id":   reqID,
		"request": map[string]any{
			"method":       "tool_call",
			"tool":         name,
			"tool_call_id": "call-" + reqID,
			"args":         json.RawMessage(argv),
		},
	})
	return reqID, ch
}

func (h *fakeHost) cancelRequest(reqID string) {
	h.write(map[string]any{
		"type":   "cancel",
		"id":     reqID,
		"cancel": map[string]any{"id": reqID, "reason": "cancelled by test"},
	})
}

func (h *fakeHost) emitEvent(event string, data map[string]any) (json.RawMessage, string) {
	h.t.Helper()
	if data == nil {
		data = map[string]any{}
	}
	raw, _ := json.Marshal(data)
	var lastRes json.RawMessage
	var lastErr string
	for _, hid := range h.handlers[event] {
		lastRes, lastErr = h.roundTrip(map[string]any{
			"method":     "event",
			"event":      event,
			"handler_id": hid,
			"args":       json.RawMessage(raw),
		})
	}
	return lastRes, lastErr
}

func (h *fakeHost) prepareLoadout(tool string, callable []string) (sdk.ToolLoadoutChanges, string) {
	h.t.Helper()
	return h.prepareLoadoutWithDeclared(tool, callable, callable)
}

func (h *fakeHost) prepareLoadoutWithDeclared(tool string, declared, callable []string) (sdk.ToolLoadoutChanges, string) {
	h.t.Helper()
	declaredTools := make([]map[string]any, 0, len(declared))
	for _, name := range declared {
		declaredTools = append(declaredTools, map[string]any{"name": name})
	}
	callableTools := make([]map[string]any, 0, len(callable))
	for _, name := range callable {
		callableTools = append(callableTools, map[string]any{"name": name})
	}
	args, _ := json.Marshal(map[string]any{
		"declared":   declaredTools,
		"callable":   callableTools,
		"registered": callableTools,
	})
	raw, errMsg := h.roundTrip(map[string]any{
		"method": "tool_prepare_loadout",
		"tool":   tool,
		"args":   json.RawMessage(args),
	})
	var changes sdk.ToolLoadoutChanges
	if errMsg == "" && len(raw) > 0 {
		_ = json.Unmarshal(raw, &changes)
	}
	return changes, errMsg
}

func (h *fakeHost) roundTrip(req map[string]any) (json.RawMessage, string) {
	h.t.Helper()
	h.mu.Lock()
	h.nextID++
	id := fmt.Sprintf("req-%d", h.nextID)
	ch := make(chan json.RawMessage, 1)
	h.pending[id] = ch
	h.mu.Unlock()
	h.write(map[string]any{"type": "request", "id": id, "request": req})
	select {
	case res := <-ch:
		h.mu.Lock()
		errMsg := h.failures[id]
		h.mu.Unlock()
		return res, errMsg
	case <-time.After(30 * time.Second):
		h.t.Fatalf("timed out waiting for %v", req["method"])
		return nil, ""
	}
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
		if json.Unmarshal(data, &f) != nil {
			continue
		}
		switch f.Type {
		case "call":
			if f.Call != nil {
				go h.answer(f)
			}
		case "notify":
			if f.Notify != nil && f.Notify.Method == "tool_update" {
				h.mu.Lock()
				h.updates = append(h.updates, append(json.RawMessage(nil), f.Notify.Args...))
				h.mu.Unlock()
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
	var args map[string]any
	if len(f.Call.Args) > 0 {
		_ = json.Unmarshal(f.Call.Args, &args)
	}
	var result any
	var errMsg string
	switch f.Call.Method {
	case "registerTool":
		var tl registeredTool
		if err := json.Unmarshal(f.Call.Args, &tl); err == nil && tl.Name != "" {
			h.mu.Lock()
			h.tools[tl.Name] = tl
			h.mu.Unlock()
		}
		result = map[string]any{}
	case "tool_update":
		h.mu.Lock()
		h.updates = append(h.updates, append(json.RawMessage(nil), f.Call.Args...))
		h.mu.Unlock()
		result = map[string]any{}
	case "sessionRead":
		method, _ := args["method"].(string)
		switch method {
		case "getSessionId":
			result = h.opts.SessionID
		case "getSessionFile":
			result = nil
		case "getCwd":
			result = h.opts.Cwd
		default:
			result = nil
		}
	case "executeTool":
		name, _ := args["name"].(string)
		rawArgs, _ := args["args"].(map[string]any)
		if h.opts.OnExecTool != nil {
			result = h.opts.OnExecTool(name, rawArgs)
		} else {
			result = sdk.AgentToolCallOutcome{
				ToolCall: sdk.ToolCall{Type: "toolCall", ID: "nested-1", Name: name, Arguments: rawArgs},
				Result: sdk.AgentToolResult{
					Content: []sdk.ToolResultContent{{Type: "text", Text: "unknown tool: " + name}},
					IsError: true,
				},
				IsError: true,
			}
		}
	case "executeTool.cancel":
		result = map[string]any{}
	default:
		result = map[string]any{}
	}

	resp := map[string]any{"type": "call_result", "id": f.ID}
	if errMsg != "" {
		resp["call_result"] = map[string]any{"error": map[string]any{"message": errMsg}}
	} else {
		raw, _ := json.Marshal(result)
		resp["call_result"] = map[string]any{"result": json.RawMessage(raw)}
	}
	h.write(resp)
}

func (h *fakeHost) getUpdates() []json.RawMessage {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]json.RawMessage, len(h.updates))
	copy(out, h.updates)
	return out
}

func (h *fakeHost) stop() {
	h.write(map[string]any{"type": "shutdown"})
	_ = h.nc.Close()
	select {
	case <-h.runDone:
	case <-time.After(5 * time.Second):
	}
}
