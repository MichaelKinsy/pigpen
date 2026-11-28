package debug

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
	PromptSnippet string           `json:"prompt_snippet"`
	ExecutionMode string           `json:"execution_mode"`
	Exposure      sdk.ToolExposure `json:"exposure,omitempty"`
	DefaultActive *bool            `json:"default_active,omitempty"`
	Parameters    map[string]any   `json:"parameters"`
}

type registeredHandler struct {
	Event     string `json:"event"`
	HandlerID int    `json:"handler_id"`
}

type fakeHost struct {
	t           *testing.T
	nc          net.Conn
	cwd         string
	sessionID   string
	trusted     bool
	runDone     chan error
	writeMu     sync.Mutex
	mu          sync.Mutex
	pending     map[string]chan json.RawMessage
	failures    map[string]string
	tools       map[string]registeredTool
	toolOrder   []string
	activeTools []string
	handlers    map[string][]int
	nextID      int
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

func startHost(t *testing.T, ext *sdk.Extension, cwd string, sessionID string) *fakeHost {
	return startHostWithTrust(t, ext, cwd, sessionID, true)
}

func startHostWithTrust(t *testing.T, ext *sdk.Extension, cwd string, sessionID string, trusted bool) *fakeHost {
	t.Helper()
	if cwd == "" {
		cwd = t.TempDir()
	}
	hostSide, extSide := net.Pipe()
	h := &fakeHost{
		t:         t,
		nc:        hostSide,
		cwd:       cwd,
		sessionID: sessionID,
		trusted:   trusted,
		runDone:   make(chan error, 1),
		pending:   map[string]chan json.RawMessage{},
		failures:  map[string]string{},
		tools:     map[string]registeredTool{},
		handlers:  map[string][]int{},
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
		h.toolOrder = append(h.toolOrder, tl.Name)
		// Mirror PiG host activation rules (tool_definition.go:76):
		// DefaultActive nil -> true for direct/model-only, false for codemode/deferred/hidden.
		exp := tl.Exposure
		if exp == "" {
			exp = sdk.ToolExposureDirect
		}
		active := exp == sdk.ToolExposureDirect || exp == sdk.ToolExposureModelOnly
		if tl.DefaultActive != nil {
			active = *tl.DefaultActive
		}
		if active {
			h.activeTools = append(h.activeTools, tl.Name)
		}
	}
	for _, hd := range first.Register.Handlers {
		h.handlers[hd.Event] = append(h.handlers[hd.Event], hd.HandlerID)
	}
	state, _ := json.Marshal(map[string]any{"hasUI": false})
	h.write(map[string]any{"type": "ready", "ready": map[string]any{
		"session_name": "",
		"cwd":          cwd,
		"mode":         "print",
		"width":        80,
		"state":        json.RawMessage(state),
	}})
	go h.serve()
	t.Cleanup(h.stop)
	return h
}

// isCallableTool enforces PiG's real host rule (coding/session_loadout.go:122):
// direct tools are callable via ExecuteTool ONLY while active; codemode and
// deferred tools are always callable; model-only and hidden are never callable.
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

func (h *fakeHost) getActiveTools() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.activeTools...)
}

func (h *fakeHost) setActiveTools(names []string) {
	h.mu.Lock()
	h.activeTools = append([]string(nil), names...)
	h.mu.Unlock()
}

func (h *fakeHost) read() wireFrame {
	var lenBuf [4]byte
	if _, err := io.ReadFull(h.nc, lenBuf[:]); err != nil {
		h.t.Fatalf("read frame len: %v", err)
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	payload := make([]byte, n)
	if _, err := io.ReadFull(h.nc, payload); err != nil {
		h.t.Fatalf("read frame payload: %v", err)
	}
	var f wireFrame
	if err := json.Unmarshal(payload, &f); err != nil {
		h.t.Fatalf("decode frame: %v", err)
	}
	return f
}

func (h *fakeHost) write(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		h.t.Errorf("encode frame: %v", err)
		return
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(b)))
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	if _, err := h.nc.Write(hdr[:]); err != nil {
		return
	}
	_, _ = h.nc.Write(b)
}

func (h *fakeHost) serve() {
	for {
		var lenBuf [4]byte
		if _, err := io.ReadFull(h.nc, lenBuf[:]); err != nil {
			return
		}
		n := binary.BigEndian.Uint32(lenBuf[:])
		payload := make([]byte, n)
		if _, err := io.ReadFull(h.nc, payload); err != nil {
			return
		}
		var f wireFrame
		if err := json.Unmarshal(payload, &f); err != nil {
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
			if f.Response != nil && f.Response.Error != nil {
				h.failures[f.ID] = f.Response.Error.Message
			}
			h.mu.Unlock()
			if ch != nil {
				var raw json.RawMessage
				if f.Response != nil {
					raw = f.Response.Result
				}
				ch <- raw
			}
		}
	}
}

func (h *fakeHost) answer(f wireFrame) {
	go func() {
		var args map[string]any
		if len(f.Call.Args) > 0 {
			_ = json.Unmarshal(f.Call.Args, &args)
		}
		var res any = map[string]any{}
		switch f.Call.Method {
		case "getSessionID":
			res = map[string]any{"sessionId": h.sessionID}
		case "isProjectTrusted":
			h.mu.Lock()
			trusted := h.trusted
			h.mu.Unlock()
			res = map[string]any{"trusted": trusted}
		case "getAllTools":
			h.mu.Lock()
			infos := make([]sdk.ToolInfo, 0, len(h.toolOrder))
			for _, name := range h.toolOrder {
				tl := h.tools[name]
				paramsRaw, _ := json.Marshal(tl.Parameters)
				exp := tl.Exposure
				if exp == "" {
					exp = sdk.ToolExposureDirect
				}
				infos = append(infos, sdk.ToolInfo{
					Name:        tl.Name,
					Description: tl.Description,
					Parameters:  paramsRaw,
					Exposure:    exp,
				})
			}
			h.mu.Unlock()
			res = map[string]any{"tools": infos}
		case "getActiveTools":
			res = map[string]any{"tools": h.getActiveTools()}
		case "setActiveTools":
			var names []string
			if raw, ok := args["tools"].([]any); ok {
				for _, item := range raw {
					if s, ok := item.(string); ok {
						names = append(names, s)
					}
				}
			}
			h.setActiveTools(names)
			res = map[string]any{}
		case "executeTool":
			callerID, _ := args["callerId"].(string)
			name, _ := args["name"].(string)
			toolArgs, _ := args["args"].(map[string]any)
			if !h.isCallableTool(name) {
				res = sdk.AgentToolCallOutcome{
					ToolCall: sdk.ToolCall{Type: "toolCall", ID: callerID + "/1", Name: name, Arguments: toolArgs},
					Result: sdk.AgentToolResult{
						Content: []sdk.ToolResultContent{{Type: "text", Text: fmt.Sprintf("Tool %q not found", name)}},
						IsError: true,
					},
					IsError: true,
				}
				break
			}
			toolRes, errMsg := h.invokeToolWithID(name, callerID+"/1", toolArgs)
			if errMsg != "" {
				res = sdk.AgentToolCallOutcome{
					ToolCall: sdk.ToolCall{Type: "toolCall", ID: callerID + "/1", Name: name, Arguments: toolArgs},
					Result: sdk.AgentToolResult{
						Content: []sdk.ToolResultContent{{Type: "text", Text: errMsg}},
						IsError: true,
					},
					IsError: true,
				}
				break
			}
			contentText, _ := toolRes["content"].(string)
			res = sdk.AgentToolCallOutcome{
				ToolCall: sdk.ToolCall{Type: "toolCall", ID: callerID + "/1", Name: name, Arguments: toolArgs},
				Result: sdk.AgentToolResult{
					Content: []sdk.ToolResultContent{{Type: "text", Text: contentText}},
					Details: toolRes["details"],
				},
				IsError: false,
			}
		}
		h.write(map[string]any{
			"type": "call_result",
			"id":   f.ID,
			"call_result": map[string]any{
				"result": res,
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

	h.write(map[string]any{
		"type":    "request",
		"id":      id,
		"request": request,
	})

	select {
	case raw := <-ch:
		h.mu.Lock()
		errMsg := h.failures[id]
		delete(h.failures, id)
		h.mu.Unlock()
		return raw, errMsg
	case <-time.After(15 * time.Second):
		h.t.Fatalf("timed out waiting for %v response", request["method"])
		return nil, ""
	}
}

func (h *fakeHost) invokeTool(name string, params map[string]any) (result map[string]any, errMsg string) {
	return h.invokeToolWithID(name, "call-1", params)
}

func (h *fakeHost) invokeToolWithID(name, callID string, params map[string]any) (result map[string]any, errMsg string) {
	h.t.Helper()
	if _, ok := h.tools[name]; !ok {
		h.t.Fatalf("tool %q is not registered", name)
	}
	argv, _ := json.Marshal(params)
	raw, errMsg := h.roundTrip(map[string]any{
		"method":       "tool_call",
		"tool":         name,
		"tool_call_id": callID,
		"args":         json.RawMessage(argv),
	})
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &result)
	}
	return result, errMsg
}

func (h *fakeHost) emitEvent(name string, data map[string]any) {
	h.t.Helper()
	argv, _ := json.Marshal(data)
	for _, hid := range h.handlers[name] {
		_, errMsg := h.roundTrip(map[string]any{
			"method":     "event",
			"event":      name,
			"handler_id": hid,
			"args":       json.RawMessage(argv),
		})
		if errMsg != "" {
			h.t.Fatalf("event %s handler %d failed: %s", name, hid, errMsg)
		}
	}
}

func (h *fakeHost) stop() {
	h.write(map[string]any{"type": "shutdown", "shutdown": map[string]any{"reason": "test"}})
	select {
	case <-h.runDone:
	case <-time.After(5 * time.Second):
	}
	_ = h.nc.Close()
}
