package ast

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"slices"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

type registeredTool struct {
	Name          string           `json:"name"`
	Label         string           `json:"label"`
	Description   string           `json:"description"`
	Exposure      sdk.ToolExposure `json:"exposure"`
	DefaultActive *bool            `json:"default_active"`
	Parameters    map[string]any   `json:"parameters"`
}

type registeredHandler struct {
	ID    int    `json:"handler_id"`
	Event string `json:"event"`
}

type fakeHost struct {
	t           *testing.T
	nc          net.Conn
	cwd         string
	sessionID   string
	activeTools []string
	extraTools  []sdk.ToolInfo
	runDone     chan error
	writeMu     sync.Mutex
	mu          sync.Mutex
	pending     map[string]chan json.RawMessage
	failures    map[string]string
	tools       map[string]registeredTool
	toolOrder   []string
	handlers    map[string][]int
	sentMsgs    []sentCustomMessage
	nextID      int
	stopped     bool
}

type sentCustomMessage struct {
	CustomType  string
	Content     string
	Display     bool
	DeliverAs   string
	TriggerTurn *bool
	Details     map[string]any
}

type wireFrame struct {
	Type     string `json:"type"`
	ID       string `json:"id,omitempty"`
	Register *struct {
		Tools    []registeredTool    `json:"tools"`
		Handlers []registeredHandler `json:"handlers"`
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
}

func startHost(t *testing.T, ext *sdk.Extension, cwd string, activeTools []string) *fakeHost {
	return startHostWithSession(t, ext, cwd, "test-ast-session-"+t.Name(), activeTools)
}

func startHostWithSession(t *testing.T, ext *sdk.Extension, cwd, sessionID string, activeTools []string, extraTools ...sdk.ToolInfo) *fakeHost {
	t.Helper()
	if cwd == "" {
		cwd = t.TempDir()
	}
	hostSide, extSide := net.Pipe()
	h := &fakeHost{
		t:           t,
		nc:          hostSide,
		cwd:         cwd,
		sessionID:   sessionID,
		activeTools: append([]string(nil), activeTools...),
		extraTools:  append([]sdk.ToolInfo(nil), extraTools...),
		runDone:     make(chan error, 1),
		pending:     map[string]chan json.RawMessage{},
		failures:    map[string]string{},
		tools:       map[string]registeredTool{},
		handlers:    map[string][]int{},
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
		if _, exists := h.tools[tl.Name]; !exists {
			h.toolOrder = append(h.toolOrder, tl.Name)
		}
		h.tools[tl.Name] = tl
	}
	for _, hd := range first.Register.Handlers {
		h.handlers[hd.Event] = append(h.handlers[hd.Event], hd.ID)
	}
	state, _ := json.Marshal(map[string]any{"hasUI": true, "sessionId": h.sessionID})
	h.write(map[string]any{"type": "ready", "ready": map[string]any{
		"session_name": "",
		"cwd":          h.cwd,
		"mode":         "tui",
		"width":        80,
		"state":        json.RawMessage(state),
	}})
	go h.serve()
	t.Cleanup(h.stop)
	return h
}

func (h *fakeHost) allToolInfos() []sdk.ToolInfo {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]sdk.ToolInfo, 0, len(h.extraTools)+len(h.toolOrder))
	out = append(out, h.extraTools...)
	for _, name := range h.toolOrder {
		tl := h.tools[name]
		rawParams, _ := json.Marshal(tl.Parameters)
		out = append(out, sdk.ToolInfo{
			Name:        tl.Name,
			Description: tl.Description,
			Parameters:  rawParams,
			Exposure:    tl.Exposure,
			Source:      "extension",
		})
	}
	return out
}

// isCallableTool enforces PiG's real host rule (coding/session_loadout.go:122):
// a `direct` tool is callable via ExecuteTool ONLY while active; a `codemode`
// or `deferred` tool is always callable even when inactive (mounted in bash).
func (h *fakeHost) isCallableTool(name string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	tl, ok := h.tools[name]
	if !ok {
		return false
	}
	exp := tl.Exposure
	if exp == "" {
		exp = sdk.ToolExposureDirect
	}
	if exp == sdk.ToolExposureCodemode || exp == sdk.ToolExposureDeferred {
		return true
	}
	if exp == sdk.ToolExposureDirect && slices.Contains(h.activeTools, name) {
		return true
	}
	return false
}

type toolCallWireResult struct {
	Content string         `json:"content"`
	Details map[string]any `json:"details"`
	IsError bool           `json:"is_error"`
}

func (h *fakeHost) callTool(name string, params map[string]any) (toolCallWireResult, string) {
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
		return toolCallWireResult{}, errMsg
	}
	var res toolCallWireResult
	if err := json.Unmarshal(raw, &res); err != nil {
		h.t.Fatalf("unmarshal tool result %s: %v", string(raw), err)
	}
	return res, ""
}

func (h *fakeHost) emitEvent(eventName string, data map[string]any) {
	h.t.Helper()
	hids := h.handlers[eventName]
	if len(hids) == 0 {
		h.t.Fatalf("event %q has no registered handler", eventName)
	}
	payload, _ := json.Marshal(data)
	for _, hid := range hids {
		_, errMsg := h.roundTrip(map[string]any{
			"method":     "event",
			"event":      eventName,
			"handler_id": hid,
			"args":       json.RawMessage(payload),
		})
		if errMsg != "" {
			h.t.Fatalf("event %s (handler %d) failed: %s", eventName, hid, errMsg)
		}
	}
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
	case <-time.After(10 * time.Second):
		h.t.Fatalf("no response to %v within 10s", request["method"])
		return nil, ""
	}
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
	go func() {
		var result any
		switch f.Call.Method {
		case "getSessionId", "getSessionID":
			result = map[string]any{"sessionId": h.sessionID}
		case "sessionRead":
			method, _ := args["method"].(string)
			if method == "getSessionId" {
				result = h.sessionID
			} else {
				result = map[string]any{}
			}
		case "getAllTools":
			result = map[string]any{"tools": h.allToolInfos()}
		case "getActiveTools":
			h.mu.Lock()
			result = map[string]any{"tools": append([]string(nil), h.activeTools...)}
			h.mu.Unlock()
		case "setActiveTools":
			if raw, ok := args["tools"].([]any); ok {
				var names []string
				for _, item := range raw {
					if s, ok := item.(string); ok {
						names = append(names, s)
					}
				}
				h.mu.Lock()
				h.activeTools = names
				h.mu.Unlock()
			}
			result = map[string]any{}
		case "executeTool":
			callerID, _ := args["callerId"].(string)
			name, _ := args["name"].(string)
			toolArgs, _ := args["args"].(map[string]any)
			if !h.isCallableTool(name) {
				result = sdk.AgentToolCallOutcome{
					ToolCall: sdk.ToolCall{Type: "toolCall", ID: callerID + "/1", Name: name, Arguments: toolArgs},
					Result:   sdk.AgentToolResult{Content: []sdk.ToolResultContent{{Type: "text", Text: fmt.Sprintf("Tool %q not found", name)}}, IsError: true},
					IsError:  true,
				}
				break
			}
			res, errMsg := h.callTool(name, toolArgs)
			if errMsg != "" {
				result = sdk.AgentToolCallOutcome{
					ToolCall: sdk.ToolCall{Type: "toolCall", ID: callerID + "/1", Name: name, Arguments: toolArgs},
					Result:   sdk.AgentToolResult{Content: []sdk.ToolResultContent{{Type: "text", Text: errMsg}}, IsError: true},
					IsError:  true,
				}
				break
			}
			result = sdk.AgentToolCallOutcome{
				ToolCall: sdk.ToolCall{Type: "toolCall", ID: callerID + "/1", Name: name, Arguments: toolArgs},
				Result: sdk.AgentToolResult{
					Content: []sdk.ToolResultContent{{Type: "text", Text: res.Content}},
					Details: res.Details,
					IsError: res.IsError,
				},
				IsError: res.IsError,
			}
		case "sendMessage":
			msg, _ := args["message"].(map[string]any)
			opts, _ := args["options"].(map[string]any)
			rec := sentCustomMessage{}
			if msg != nil {
				rec.CustomType, _ = msg["customType"].(string)
				rec.Content, _ = msg["content"].(string)
				rec.Display, _ = msg["display"].(bool)
				rec.Details, _ = msg["details"].(map[string]any)
			}
			if opts != nil {
				rec.DeliverAs, _ = opts["deliverAs"].(string)
				if tt, ok := opts["triggerTurn"].(bool); ok {
					v := tt
					rec.TriggerTurn = &v
				}
			}
			h.mu.Lock()
			h.sentMsgs = append(h.sentMsgs, rec)
			h.mu.Unlock()
			result = map[string]any{}
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

func (h *fakeHost) stop() {
	h.mu.Lock()
	if h.stopped {
		h.mu.Unlock()
		return
	}
	h.stopped = true
	h.mu.Unlock()
	h.write(map[string]any{"type": "shutdown", "shutdown": map[string]any{"reason": "test"}})
	select {
	case <-h.runDone:
	case <-time.After(5 * time.Second):
		h.t.Errorf("extension did not stop after shutdown")
	}
	_ = h.nc.Close()
}
