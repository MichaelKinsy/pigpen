package toolcmd

import (
	"context"
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

type fakeHostOptions struct {
	cwd         string
	sessionID   string
	allTools    []sdk.ToolInfo
	activeTools []string
	// executeTool handles host `executeTool` calls in tests (invoked ONLY when
	// PiG's real callableEntriesLocked exposure rules permit calling `name`).
	executeTool func(ctx context.Context, callerID, name string, args map[string]any) sdk.AgentToolCallOutcome
}

type fakeHost struct {
	t          *testing.T
	nc         net.Conn
	opts       fakeHostOptions
	runDone    chan error
	writeMu    sync.Mutex
	mu         sync.Mutex
	calls      []hostCall
	handlers   map[string][]int
	pending    map[string]chan json.RawMessage
	execCancel map[string]context.CancelFunc
	nextID     int
	stopped    bool

	allTools             []sdk.ToolInfo
	activeTools          []string
	preparesLoadoutTools map[string]bool
	hiddenDeclarations   []string
}

type wireFrame struct {
	Type     string `json:"type"`
	ID       string `json:"id,omitempty"`
	Register *struct {
		Tools    []map[string]any `json:"tools"`
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

func startFakeHost(t *testing.T, ext *sdk.Extension, opts fakeHostOptions) *fakeHost {
	t.Helper()
	if opts.cwd == "" {
		opts.cwd = t.TempDir()
	}
	if opts.sessionID == "" {
		opts.sessionID = "sess-" + t.Name()
	}
	hostSide, extSide := net.Pipe()
	h := &fakeHost{
		t:                    t,
		nc:                   hostSide,
		opts:                 opts,
		runDone:              make(chan error, 1),
		handlers:             map[string][]int{},
		pending:              map[string]chan json.RawMessage{},
		execCancel:           map[string]context.CancelFunc{},
		allTools:             append([]sdk.ToolInfo(nil), opts.allTools...),
		activeTools:          append([]string(nil), opts.activeTools...),
		preparesLoadoutTools: map[string]bool{},
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
	for _, td := range first.Register.Tools {
		name, _ := td["name"].(string)
		if prep, _ := td["prepares_loadout"].(bool); prep && name != "" {
			h.preparesLoadoutTools[name] = true
		}
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

func (h *fakeHost) isCallableTool(name string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, info := range h.allTools {
		if info.Name != name {
			continue
		}
		exp := info.Exposure
		if exp == "" {
			exp = sdk.ToolExposureDirect
		}
		// Mirror PiG coding/session_loadout.go:122 (callableEntriesLocked):
		// codemode/deferred are always callable; direct is callable only while active;
		// model-only and hidden are never callable via ExecuteTool.
		if exp == sdk.ToolExposureCodemode || exp == sdk.ToolExposureDeferred {
			return true
		}
		if exp == sdk.ToolExposureDirect && slices.Contains(h.activeTools, name) {
			return true
		}
		return false
	}
	return false
}

func (h *fakeHost) getActiveTools() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.activeTools...)
}

// getHiddenDeclarations returns the HiddenDeclarations produced by active
// tools' PrepareLoadout hooks on the last setActiveTools (matching PiG's
// Session.HiddenDeclarationNames).
func (h *fakeHost) getHiddenDeclarations() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.hiddenDeclarations...)
}

// getProjectedProviderTools returns the active tools minus hiddenDeclarations,
// matching PiG's hideDeclarations transcript projection before provider
// conversion (coding/session_loadout.go:288-327).
func (h *fakeHost) getProjectedProviderTools() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	hidden := make(map[string]bool, len(h.hiddenDeclarations))
	for _, name := range h.hiddenDeclarations {
		hidden[name] = true
	}
	out := make([]string, 0, len(h.activeTools))
	for _, name := range h.activeTools {
		if !hidden[name] {
			out = append(out, name)
		}
	}
	return out
}

func (h *fakeHost) setTools(all []sdk.ToolInfo, active []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.allTools = append([]sdk.ToolInfo(nil), all...)
	h.activeTools = append([]string(nil), active...)
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

func (h *fakeHost) emitEvent(eventName string, data map[string]any) (json.RawMessage, string) {
	h.t.Helper()
	ids := h.handlers[eventName]
	if len(ids) == 0 {
		h.t.Fatalf("no handler registered for event %q", eventName)
	}
	if data == nil {
		data = map[string]any{}
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

func (h *fakeHost) callTool(name string, params map[string]any) (json.RawMessage, string) {
	return h.callToolWithID(name, "call-bash-1", params)
}

func (h *fakeHost) callToolWithID(name, callID string, params map[string]any) (json.RawMessage, string) {
	h.t.Helper()
	argv, _ := json.Marshal(params)
	return h.roundTrip(map[string]any{
		"method":       "tool_call",
		"tool":         name,
		"tool_call_id": callID,
		"args":         json.RawMessage(argv),
	})
}

// callModelTool simulates a top-level model tool call through PiG's
// agent/tool_execution.go:366-369: it REFUSES any tool not currently in
// h.activeTools with "Tool <name> not found", regardless of whether the
// extension registered a handler for it.
func (h *fakeHost) callModelTool(name, callID string, params map[string]any) (json.RawMessage, string) {
	h.t.Helper()
	h.mu.Lock()
	isActive := slices.Contains(h.activeTools, name)
	h.mu.Unlock()
	if !isActive {
		return nil, "Tool " + name + " not found"
	}
	return h.callToolWithID(name, callID, params)
}

// runPrepareLoadout mirrors PiG's Session.applyToolLoadout + runLoadoutHooks
// (coding/session_loadout.go:134-276): builds ToolLoadout from the active tools,
// invokes `tool_prepare_loadout` on every active tool that registered
// PrepareLoadout, and stores the merged HiddenDeclarations.
func (h *fakeHost) runPrepareLoadout(active []string) {
	h.mu.Lock()
	allTools := append([]sdk.ToolInfo(nil), h.allTools...)
	var hookTools []string
	for _, name := range active {
		if h.preparesLoadoutTools[name] {
			hookTools = append(hookTools, name)
		}
	}
	h.mu.Unlock()

	if len(hookTools) == 0 {
		h.mu.Lock()
		h.hiddenDeclarations = nil
		h.mu.Unlock()
		return
	}

	byName := make(map[string]sdk.ToolInfo, len(allTools))
	exposures := make(map[string]string, len(allTools))
	registered := make([]sdk.AgentTool, 0, len(allTools))
	for _, info := range allTools {
		byName[info.Name] = info
		exp := info.Exposure
		if exp == "" {
			exp = sdk.ToolExposureDirect
		}
		exposures[info.Name] = string(exp)
		registered = append(registered, sdk.AgentTool{
			Name:        info.Name,
			Label:       info.Name,
			Description: info.Description,
			Parameters:  json.RawMessage(info.Parameters),
		})
	}
	activeSet := make(map[string]bool, len(active))
	declared := make([]sdk.AgentTool, 0, len(active))
	for _, name := range active {
		activeSet[name] = true
		if info, ok := byName[name]; ok {
			declared = append(declared, sdk.AgentTool{
				Name:        info.Name,
				Label:       info.Name,
				Description: info.Description,
				Parameters:  json.RawMessage(info.Parameters),
			})
		}
	}
	var callable []sdk.AgentTool
	for _, info := range allTools {
		exp := info.Exposure
		if exp == "" {
			exp = sdk.ToolExposureDirect
		}
		if exp == sdk.ToolExposureCodemode || exp == sdk.ToolExposureDeferred || (exp == sdk.ToolExposureDirect && activeSet[info.Name]) {
			callable = append(callable, sdk.AgentTool{
				Name:        info.Name,
				Label:       info.Name,
				Description: info.Description,
				Parameters:  json.RawMessage(info.Parameters),
			})
		}
	}
	payload, _ := json.Marshal(map[string]any{
		"declared":   declared,
		"callable":   callable,
		"registered": registered,
		"exposures":  exposures,
	})

	seenHidden := map[string]bool{}
	var hidden []string
	for _, toolName := range hookTools {
		res, errMsg := h.roundTrip(map[string]any{
			"method": "tool_prepare_loadout",
			"tool":   toolName,
			"args":   json.RawMessage(payload),
		})
		if errMsg != "" || len(res) == 0 || string(res) == "null" {
			continue
		}
		var changes sdk.ToolLoadoutChanges
		if json.Unmarshal(res, &changes) == nil {
			for _, name := range changes.HiddenDeclarations {
				if !seenHidden[name] {
					seenHidden[name] = true
					hidden = append(hidden, name)
				}
			}
		}
	}
	h.mu.Lock()
	h.hiddenDeclarations = hidden
	h.mu.Unlock()
}

func (h *fakeHost) read() wireFrame {
	var hdr [4]byte
	if _, err := io.ReadFull(h.nc, hdr[:]); err != nil {
		h.t.Fatalf("read header: %v", err)
	}
	data := make([]byte, binary.BigEndian.Uint32(hdr[:]))
	if _, err := io.ReadFull(h.nc, data); err != nil {
		h.t.Fatalf("read body: %v", err)
	}
	var f wireFrame
	if err := json.Unmarshal(data, &f); err != nil {
		h.t.Fatalf("unmarshal frame: %v", err)
	}
	return f
}

func (h *fakeHost) write(msg any) {
	data, err := json.Marshal(msg)
	if err != nil {
		h.t.Fatalf("marshal frame: %v", err)
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(data)))
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	_, _ = h.nc.Write(hdr[:])
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
			return
		}
		switch f.Type {
		case "response":
			h.mu.Lock()
			ch := h.pending[f.ID]
			delete(h.pending, f.ID)
			h.mu.Unlock()
			if ch != nil {
				ch <- data
			}
		case "call":
			if f.Call != nil {
				go h.answer(f)
			}
		}
	}
}

func (h *fakeHost) answer(f wireFrame) {
	var args map[string]any
	if len(f.Call.Args) > 0 {
		_ = json.Unmarshal(f.Call.Args, &args)
	}
	h.mu.Lock()
	h.calls = append(h.calls, hostCall{Method: f.Call.Method, Args: args})
	h.mu.Unlock()

	var result any
	switch f.Call.Method {
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
		var names []string
		if raw, ok := args["tools"].([]any); ok {
			for _, item := range raw {
				if s, ok := item.(string); ok {
					names = append(names, s)
				}
			}
		}
		// Mirror PiG coding/session_loadout.go:178-203 (planToolLoadout):
		// tools that stay active keep their current order, newly active tools
		// are appended in requested order (deduplicated), and unregistered or
		// hidden-exposure names are dropped.
		h.mu.Lock()
		registered := make(map[string]sdk.ToolInfo, len(h.allTools))
		for _, info := range h.allTools {
			registered[info.Name] = info
		}
		requested := make(map[string]bool, len(names))
		for _, n := range names {
			requested[n] = true
		}
		seen := make(map[string]bool, len(names))
		next := make([]string, 0, len(names))
		admit := func(n string) bool {
			info, ok := registered[n]
			if !ok {
				return false
			}
			return info.Exposure != sdk.ToolExposureHidden
		}
		for _, existing := range h.activeTools {
			if requested[existing] && !seen[existing] && admit(existing) {
				seen[existing] = true
				next = append(next, existing)
			}
		}
		for _, n := range names {
			if !seen[n] && admit(n) {
				seen[n] = true
				next = append(next, n)
			}
		}
		h.activeTools = next
		h.mu.Unlock()
		h.runPrepareLoadout(next)
		result = map[string]any{}
	case "getSessionID":
		result = map[string]any{"sessionId": h.opts.sessionID}
	case "executeTool.cancel":
		execID, _ := args["executeId"].(string)
		h.mu.Lock()
		cancelFn := h.execCancel[execID]
		h.mu.Unlock()
		if cancelFn != nil {
			cancelFn()
		}
		result = map[string]any{}
	case "executeTool":
		callerID, _ := args["callerId"].(string)
		name, _ := args["name"].(string)
		execID, _ := args["executeId"].(string)
		toolArgs, _ := args["args"].(map[string]any)
		if !h.isCallableTool(name) {
			result = sdk.AgentToolCallOutcome{
				ToolCall: sdk.ToolCall{Type: "toolCall", ID: callerID + "/1", Name: name, Arguments: toolArgs},
				Result:   sdk.AgentToolResult{Content: []sdk.ToolResultContent{{Type: "text", Text: fmt.Sprintf("Tool %q not found", name)}}, IsError: true},
				IsError:  true,
			}
			break
		}
		execCtx, cancel := context.WithCancel(context.Background())
		if execID != "" {
			h.mu.Lock()
			h.execCancel[execID] = cancel
			h.mu.Unlock()
			defer func() {
				h.mu.Lock()
				delete(h.execCancel, execID)
				h.mu.Unlock()
				cancel()
			}()
		} else {
			defer cancel()
		}
		if h.opts.executeTool != nil {
			result = h.opts.executeTool(execCtx, callerID, name, toolArgs)
		} else {
			result = sdk.AgentToolCallOutcome{
				ToolCall: sdk.ToolCall{Type: "toolCall", ID: callerID + "/1", Name: name, Arguments: toolArgs},
				Result:   sdk.AgentToolResult{Content: []sdk.ToolResultContent{{Type: "text", Text: "not found"}}, IsError: true},
				IsError:  true,
			}
		}
	default:
		result = map[string]any{}
	}
	raw, _ := json.Marshal(result)
	h.write(map[string]any{
		"type":        "call_result",
		"id":          f.ID,
		"call_result": map[string]any{"result": json.RawMessage(raw)},
	})
}

func (h *fakeHost) roundTrip(req map[string]any) (json.RawMessage, string) {
	h.mu.Lock()
	h.nextID++
	id := fmt.Sprintf("req-%d", h.nextID)
	ch := make(chan json.RawMessage, 1)
	h.pending[id] = ch
	h.mu.Unlock()

	h.write(map[string]any{"type": "request", "id": id, "request": req})
	select {
	case raw := <-ch:
		var f wireFrame
		_ = json.Unmarshal(raw, &f)
		if f.Response != nil && f.Response.Error != nil {
			return nil, f.Response.Error.Message
		}
		if f.Response != nil {
			return f.Response.Result, ""
		}
		return nil, ""
	case <-time.After(10 * time.Second):
		h.t.Fatalf("timed out waiting for %v", req["method"])
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
	_ = h.nc.Close()
	select {
	case <-h.runDone:
	case <-time.After(3 * time.Second):
	}
}
