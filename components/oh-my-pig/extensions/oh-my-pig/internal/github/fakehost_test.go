package github

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

type ghRegisteredTool struct {
	Name          string           `json:"name"`
	Label         string           `json:"label"`
	Description   string           `json:"description"`
	Exposure      sdk.ToolExposure `json:"exposure"`
	DefaultActive *bool            `json:"default_active"`
	Parameters    map[string]any   `json:"parameters"`
}

type ghFakeHost struct {
	t           *testing.T
	nc          net.Conn
	cwd         string
	sessionID   string
	runDone     chan error
	writeMu     sync.Mutex
	mu          sync.Mutex
	pending     map[string]chan json.RawMessage
	failures    map[string]string
	tools       map[string]ghRegisteredTool
	allTools    []sdk.ToolInfo
	activeTools []string
	handlers    map[string][]int
	nextID      int
	stopped     bool
}

type ghWireFrame struct {
	Type     string `json:"type"`
	ID       string `json:"id,omitempty"`
	Register *struct {
		Tools    []ghRegisteredTool `json:"tools"`
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
}

func startGhFakeHost(t *testing.T, ext *sdk.Extension, cwd, sessionID string, activeTools []string) *ghFakeHost {
	t.Helper()
	hostSide, extSide := net.Pipe()
	h := &ghFakeHost{
		t:           t,
		nc:          hostSide,
		cwd:         cwd,
		sessionID:   sessionID,
		runDone:     make(chan error, 1),
		pending:     map[string]chan json.RawMessage{},
		failures:    map[string]string{},
		tools:       map[string]ghRegisteredTool{},
		activeTools: append([]string(nil), activeTools...),
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
		h.tools[tl.Name] = tl
		exp := tl.Exposure
		if exp == "" {
			exp = sdk.ToolExposureDirect
		}
		paramBytes, _ := json.Marshal(tl.Parameters)
		h.allTools = append(h.allTools, sdk.ToolInfo{
			Name:        tl.Name,
			Description: tl.Description,
			Parameters:  json.RawMessage(paramBytes),
			Exposure:    exp,
		})
	}
	for _, hd := range first.Register.Handlers {
		h.handlers[hd.Event] = append(h.handlers[hd.Event], hd.HandlerID)
	}
	state, _ := json.Marshal(map[string]any{
		"hasUI": true,
		"session": map[string]any{
			"sessionId": sessionID,
		},
	})
	h.write(map[string]any{"type": "ready", "ready": map[string]any{
		"session_name": "",
		"cwd":          cwd,
		"mode":         "tui",
		"width":        80,
		"state":        json.RawMessage(state),
	}})
	go h.serve()
	t.Cleanup(h.stop)
	return h
}

// isCallableTool enforces PiG's host rule (coding/session_loadout.go:122):
// direct tools are callable only while active; codemode/deferred are always callable.
func (h *ghFakeHost) isCallableTool(name string) bool {
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
	return exp == sdk.ToolExposureDirect && slices.Contains(h.activeTools, name)
}

func (h *ghFakeHost) getActiveTools() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.activeTools...)
}

func (h *ghFakeHost) emitEvent(eventName string, data map[string]any) {
	h.t.Helper()
	payload, _ := json.Marshal(data)
	for _, hid := range h.handlers[eventName] {
		_, errMsg := h.roundTrip(map[string]any{
			"method":     "event",
			"event":      eventName,
			"handler_id": hid,
			"args":       json.RawMessage(payload),
		})
		if errMsg != "" {
			h.t.Fatalf("event %s failed: %s", eventName, errMsg)
		}
	}
}

func (h *ghFakeHost) callToolWithID(name, callID string, params map[string]any) (json.RawMessage, string) {
	h.t.Helper()
	argv, _ := json.Marshal(params)
	return h.roundTrip(map[string]any{
		"method":       "tool_call",
		"tool":         name,
		"tool_call_id": callID,
		"args":         json.RawMessage(argv),
	})
}

func (h *ghFakeHost) read() ghWireFrame {
	var hdr [4]byte
	if _, err := io.ReadFull(h.nc, hdr[:]); err != nil {
		h.t.Fatalf("read header: %v", err)
	}
	data := make([]byte, binary.BigEndian.Uint32(hdr[:]))
	if _, err := io.ReadFull(h.nc, data); err != nil {
		h.t.Fatalf("read frame: %v", err)
	}
	var f ghWireFrame
	if err := json.Unmarshal(data, &f); err != nil {
		h.t.Fatalf("decode frame: %v", err)
	}
	return f
}

func (h *ghFakeHost) write(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		h.t.Errorf("marshal: %v", err)
		return
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(data)))
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	_, _ = h.nc.Write(hdr[:])
	_, _ = h.nc.Write(data)
}

func (h *ghFakeHost) serve() {
	for {
		var hdr [4]byte
		if _, err := io.ReadFull(h.nc, hdr[:]); err != nil {
			return
		}
		data := make([]byte, binary.BigEndian.Uint32(hdr[:]))
		if _, err := io.ReadFull(h.nc, data); err != nil {
			return
		}
		var f ghWireFrame
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

func (h *ghFakeHost) answer(f ghWireFrame) {
	args := map[string]any{}
	_ = json.Unmarshal(f.Call.Args, &args)
	go func() {
		var result any
		switch f.Call.Method {
		case "getSessionId", "getSessionID":
			result = map[string]any{"sessionId": h.sessionID}
		case "getAllTools":
			h.mu.Lock()
			tools := append([]sdk.ToolInfo(nil), h.allTools...)
			h.mu.Unlock()
			result = map[string]any{"tools": tools}
		case "getActiveTools":
			h.mu.Lock()
			active := append([]string(nil), h.activeTools...)
			h.mu.Unlock()
			result = map[string]any{"tools": active}
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
			rawRes, errMsg := h.callToolWithID(name, callerID+"/1", toolArgs)
			if errMsg != "" {
				result = sdk.AgentToolCallOutcome{
					ToolCall: sdk.ToolCall{Type: "toolCall", ID: callerID + "/1", Name: name, Arguments: toolArgs},
					Result:   sdk.AgentToolResult{Content: []sdk.ToolResultContent{{Type: "text", Text: errMsg}}, IsError: true},
					IsError:  true,
				}
				break
			}
			var tr struct {
				Content string `json:"content"`
				Details any    `json:"details"`
				IsError bool   `json:"isError"`
			}
			_ = json.Unmarshal(rawRes, &tr)
			result = sdk.AgentToolCallOutcome{
				ToolCall: sdk.ToolCall{Type: "toolCall", ID: callerID + "/1", Name: name, Arguments: toolArgs},
				Result: sdk.AgentToolResult{
					Content: []sdk.ToolResultContent{{Type: "text", Text: tr.Content}},
					Details: tr.Details,
					IsError: tr.IsError,
				},
				IsError: tr.IsError,
			}
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

func (h *ghFakeHost) roundTrip(request map[string]any) (json.RawMessage, string) {
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

func (h *ghFakeHost) stop() {
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
	case <-time.After(10 * time.Second):
		h.t.Errorf("extension did not stop after shutdown")
	}
	_ = h.nc.Close()
}
