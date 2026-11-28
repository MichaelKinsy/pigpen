package checkpoint

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

type hostCall struct {
	Method string
	Args   map[string]any
}

type registeredTool struct {
	Name          string           `json:"name"`
	Label         string           `json:"label"`
	Description   string           `json:"description"`
	Exposure      sdk.ToolExposure `json:"exposure"`
	DefaultActive *bool            `json:"default_active"`
	Parameters    map[string]any   `json:"parameters"`
}

type hostOptions struct {
	cwd         string
	sessionID   string
	leafID      string
	activeTools []string
	// entries holds ALL entries in the session file (including abandoned branches).
	// Each entry has "id" and optional "parentId" (nil for root).
	entries []map[string]any
}

type fakeHost struct {
	t        *testing.T
	nc       net.Conn
	opts     hostOptions
	runDone  chan error
	writeMu  sync.Mutex
	mu       sync.Mutex
	calls    []hostCall
	pending  map[string]chan json.RawMessage
	failures map[string]string
	tools    map[string]registeredTool
	allTools []sdk.ToolInfo
	handlers map[string][]int
	nextID   int
	nextSeq  int
	stopped  bool
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
}

func startHost(t *testing.T, ext *sdk.Extension, opts hostOptions) *fakeHost {
	t.Helper()
	if opts.cwd == "" {
		opts.cwd = t.TempDir()
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
			"sessionId": opts.sessionID,
		},
	})
	h.write(map[string]any{"type": "ready", "ready": map[string]any{
		"session_name": "",
		"cwd":          opts.cwd,
		"mode":         "tui",
		"width":        80,
		"state":        json.RawMessage(state),
	}})
	go h.serve()
	t.Cleanup(h.stop)
	return h
}

// isCallableTool enforces PiG's real host rule (coding/session_loadout.go:122):
// direct tools are callable only while active; codemode/deferred are always callable.
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
	return exp == sdk.ToolExposureDirect && slices.Contains(h.opts.activeTools, name)
}

func (h *fakeHost) getActiveTools() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.opts.activeTools...)
}

// appendEntry appends an entry onto the host's session log chained to current leafID,
// and notifies the extension's replicated session log mirror just like the real host.
func (h *fakeHost) appendEntry(entry map[string]any) string {
	h.mu.Lock()
	h.nextSeq++
	id, _ := entry["id"].(string)
	if id == "" {
		id = fmt.Sprintf("e-%d", h.nextSeq)
		entry["id"] = id
	}
	if _, hasParent := entry["parentId"]; !hasParent {
		if h.opts.leafID != "" {
			entry["parentId"] = h.opts.leafID
		} else {
			entry["parentId"] = nil
		}
	}
	h.opts.entries = append(h.opts.entries, entry)
	h.opts.leafID = id
	h.mu.Unlock()
	h.pushSessionLogUpdate(entry, id)
	return id
}

// setLeaf moves the active leaf pointer (simulating /tree navigation) and
// notifies the extension's replicated session mirror via state_update.
func (h *fakeHost) setLeaf(leafID string) {
	h.mu.Lock()
	h.opts.leafID = leafID
	sid := h.opts.sessionID
	entries := append([]map[string]any(nil), h.opts.entries...)
	h.mu.Unlock()
	raw := make([]json.RawMessage, 0, len(entries))
	for _, e := range entries {
		b, _ := json.Marshal(e)
		raw = append(raw, b)
	}
	payload, _ := json.Marshal(map[string]any{
		"state": map[string]any{
			"session": map[string]any{
				"sessionId":       sid,
				"leafId":          leafID,
				"entriesAppended": raw,
				"entryCount":      len(raw),
			},
		},
	})
	h.write(map[string]any{
		"type": "notify",
		"notify": map[string]any{
			"method": "state_update",
			"args":   json.RawMessage(payload),
		},
	})
}

func (h *fakeHost) pushSessionLogUpdate(entry map[string]any, leafID string) {
	b, _ := json.Marshal(entry)
	h.mu.Lock()
	sid := h.opts.sessionID
	count := len(h.opts.entries)
	h.mu.Unlock()
	payload, _ := json.Marshal(map[string]any{
		"state": map[string]any{
			"session": map[string]any{
				"sessionId":       sid,
				"leafId":          leafID,
				"entriesAppended": []json.RawMessage{b},
				"entryCount":      count,
			},
		},
	})
	h.write(map[string]any{
		"type": "notify",
		"notify": map[string]any{
			"method": "state_update",
			"args":   json.RawMessage(payload),
		},
	})
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

func (h *fakeHost) callToolWithID(name, callID string, params map[string]any) (json.RawMessage, string) {
	h.t.Helper()
	if _, ok := h.tools[name]; !ok {
		h.t.Fatalf("tool %q is not registered", name)
	}
	argv, _ := json.Marshal(params)
	return h.roundTrip(map[string]any{
		"method":       "tool_call",
		"tool":         name,
		"tool_call_id": callID,
		"args":         json.RawMessage(argv),
	})
}

func (h *fakeHost) callTool(name string, params map[string]any) (json.RawMessage, string) {
	return h.callToolWithID(name, "call-1", params)
}

func (h *fakeHost) emitEvent(eventName string, data map[string]any) (json.RawMessage, string) {
	h.t.Helper()
	ids := h.handlers[eventName]
	if len(ids) == 0 {
		h.t.Fatalf("event %q has no registered handler", eventName)
	}
	payload, _ := json.Marshal(data)
	var last json.RawMessage
	for _, hid := range ids {
		res, errMsg := h.roundTrip(map[string]any{
			"method":     "event",
			"event":      eventName,
			"handler_id": hid,
			"args":       json.RawMessage(payload),
		})
		if errMsg != "" {
			return nil, errMsg
		}
		last = res
	}
	return last, ""
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
	h.mu.Unlock()
	go func() {
		var result any
		switch f.Call.Method {
		case "getSessionId", "getSessionID":
			result = map[string]any{"sessionId": h.opts.sessionID}
		case "getLeafId", "getLeafID":
			h.mu.Lock()
			leaf := h.opts.leafID
			h.mu.Unlock()
			if leaf != "" {
				result = map[string]any{"leafId": leaf}
			} else {
				result = map[string]any{"leafId": nil}
			}
		case "watchSessionLog":
			h.mu.Lock()
			entries := append([]map[string]any(nil), h.opts.entries...)
			leaf := h.opts.leafID
			h.mu.Unlock()
			if leaf == "" && len(entries) > 0 {
				if id, ok := entries[len(entries)-1]["id"].(string); ok && id != "" {
					leaf = id
				}
			}
			cursor := 0
			if c, ok := args["cursor"].(float64); ok {
				cursor = int(c)
			}
			var slice []map[string]any
			if cursor < len(entries) {
				slice = entries[cursor:]
			} else {
				slice = []map[string]any{}
			}
			result = map[string]any{
				"entries":    slice,
				"entryCount": len(entries),
				"hasMore":    false,
				"leafId":     leaf,
			}
		case "getAllTools":
			h.mu.Lock()
			tools := append([]sdk.ToolInfo(nil), h.allTools...)
			h.mu.Unlock()
			result = map[string]any{"tools": tools}
		case "getActiveTools":
			h.mu.Lock()
			tools := append([]string(nil), h.opts.activeTools...)
			h.mu.Unlock()
			result = map[string]any{"tools": tools}
		case "setActiveTools":
			if raw, ok := args["tools"].([]any); ok {
				var names []string
				for _, item := range raw {
					if s, ok := item.(string); ok {
						names = append(names, s)
					}
				}
				h.mu.Lock()
				h.opts.activeTools = names
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
	case <-time.After(10 * time.Second):
		h.t.Errorf("extension did not stop after shutdown")
	}
	_ = h.nc.Close()
}
