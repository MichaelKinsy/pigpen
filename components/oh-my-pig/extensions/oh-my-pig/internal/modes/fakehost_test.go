package modes

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

type hostCall struct {
	Method         string
	Args           map[string]any
	CommandContext bool
}

type hostOptions struct {
	Mode                 string // "tui" (default), "rpc", "json", "print"
	HasUI                *bool
	Cwd                  string
	SessionID            string
	OmitSessionIDInReady bool
	SessionFile          string
	SessionName          string
	Model                string
	ModelProvider        string
	ModelReasoning       *bool
	PerMessageEffort     bool
	ContextWindow        int
	ThinkingLevel        string
	ActiveTools          []string
	AllTools             []map[string]any
	Branch               []map[string]any
	Entries              []map[string]any
	SystemPrompt         string
	Idle                 *bool
	ReplacementSessID    string
	OnCall               func(method string, args map[string]any) (result map[string]any, errMessage string)
}

type registeredToolMeta struct {
	Name          string          `json:"name"`
	Description   string          `json:"description"`
	Parameters    json.RawMessage `json:"parameters"`
	Exposure      string          `json:"exposure"`
	DefaultActive *bool           `json:"default_active"`
}

type fakeHost struct {
	t        *testing.T
	nc       net.Conn
	opts     hostOptions
	runDone  chan error
	writeMu  sync.Mutex
	mu       sync.Mutex
	calls    []hostCall
	handlers map[string][]int
	commands map[string]bool
	tools    map[string]registeredToolMeta
	pending  map[string]chan json.RawMessage
	failures map[string]string
	nextID   int

	// Mutable host state tracked across calls
	activeTools   []string
	thinkingLevel string
	sessionID     string
	sessionName   string
	model         string
	modelProvider string
	clipboard     string
	statuses      map[string]string
}

type wireFrame struct {
	Type     string `json:"type"`
	ID       string `json:"id,omitempty"`
	Register *struct {
		Handlers []struct {
			Event     string `json:"event"`
			HandlerID int    `json:"handler_id"`
		} `json:"handlers"`
		Commands []struct {
			Name string `json:"name"`
		} `json:"commands"`
		Tools []registeredToolMeta `json:"tools"`
	} `json:"register,omitempty"`
	Response *struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"response,omitempty"`
	Call *struct {
		Method         string          `json:"method"`
		Args           json.RawMessage `json:"args"`
		CommandContext bool            `json:"command_context"`
	} `json:"call,omitempty"`
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
	if opts.SessionID == "" && !opts.OmitSessionIDInReady {
		opts.SessionID = "sess-test-1"
	}
	if opts.Model == "" {
		opts.Model = "claude-sonnet-4-5"
	}
	if opts.ModelProvider == "" {
		opts.ModelProvider = "anthropic"
	}
	if opts.ContextWindow == 0 {
		opts.ContextWindow = 200000
	}
	if opts.ThinkingLevel == "" {
		opts.ThinkingLevel = "high"
	}
	reasoning := true
	if opts.ModelReasoning != nil {
		reasoning = *opts.ModelReasoning
	}
	if opts.ActiveTools == nil {
		opts.ActiveTools = []string{"read", "write", "edit", "bash", "grep", "glob", "ask", "task", "eval", "todo"}
	}

	hostSide, extSide := net.Pipe()
	h := &fakeHost{
		t:             t,
		nc:            hostSide,
		opts:          opts,
		runDone:       make(chan error, 1),
		handlers:      map[string][]int{},
		commands:      map[string]bool{},
		tools:         map[string]registeredToolMeta{},
		pending:       map[string]chan json.RawMessage{},
		failures:      map[string]string{},
		activeTools:   append([]string(nil), opts.ActiveTools...),
		thinkingLevel: opts.ThinkingLevel,
		sessionID:     opts.SessionID,
		sessionName:   opts.SessionName,
		model:         opts.Model,
		modelProvider: opts.ModelProvider,
		statuses:      map[string]string{},
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
	for _, hnd := range first.Register.Handlers {
		h.handlers[hnd.Event] = append(h.handlers[hnd.Event], hnd.HandlerID)
	}
	for _, cmd := range first.Register.Commands {
		h.commands[cmd.Name] = true
	}
	for _, tl := range first.Register.Tools {
		if tl.Exposure == "" {
			tl.Exposure = string(sdk.ToolExposureDirect)
		}
		h.tools[tl.Name] = tl
	}

	stateObj := map[string]any{
		"hasUI": hasUI,
		"model": map[string]any{
			"id":            opts.Model,
			"provider":      opts.ModelProvider,
			"contextWindow": opts.ContextWindow,
			"reasoning":     reasoning,
			"cost":          map[string]any{"input": 3.0, "output": 15.0, "cacheRead": 0.3, "cacheWrite": 3.75},
		},
		"sessionFile": opts.SessionFile,
		"sessionName": opts.SessionName,
	}
	if !opts.OmitSessionIDInReady {
		stateObj["sessionId"] = opts.SessionID
	}
	stateRaw, _ := json.Marshal(stateObj)
	h.write(map[string]any{"type": "ready", "ready": map[string]any{
		"session_name": opts.SessionName,
		"cwd":          opts.Cwd,
		"mode":         opts.Mode,
		"width":        100,
		"state":        json.RawMessage(stateRaw),
	}})
	go h.serve()
	t.Cleanup(h.stop)
	return h
}

func (h *fakeHost) callCommand(name, args string) (json.RawMessage, string) {
	h.t.Helper()
	return h.roundTrip(map[string]any{
		"method": "command",
		"tool":   name,
		"args":   args,
	})
}

func (h *fakeHost) callTool(name string, params map[string]any) (json.RawMessage, string) {
	h.t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		h.t.Fatalf("marshal tool args: %v", err)
	}
	return h.roundTrip(map[string]any{
		"method":       "tool_call",
		"tool":         name,
		"tool_call_id": "call-1",
		"args":         json.RawMessage(raw),
	})
}

func (h *fakeHost) fireEvent(event string, data map[string]any) json.RawMessage {
	h.t.Helper()
	ids := h.handlers[event]
	if len(ids) == 0 {
		h.t.Fatalf("no handler registered for event %q", event)
	}
	raw, err := json.Marshal(data)
	if err != nil {
		h.t.Fatalf("marshal event args: %v", err)
	}
	res, errMsg := h.roundTrip(map[string]any{
		"method":     "event",
		"event":      event,
		"handler_id": ids[len(ids)-1],
		"args":       json.RawMessage(raw),
	})
	if errMsg != "" {
		h.t.Fatalf("event %q failed: %s", event, errMsg)
	}
	return res
}

func (h *fakeHost) sendRunSignal(run uint64, active, aborted bool) {
	raw, _ := json.Marshal(map[string]any{
		"run":     run,
		"active":  active,
		"aborted": aborted,
	})
	h.write(map[string]any{
		"type": "notify",
		"notify": map[string]any{
			"method": "run_signal",
			"args":   json.RawMessage(raw),
		},
	})
}

func (h *fakeHost) fireEventWithTimeout(event string, data map[string]any, timeout time.Duration) (json.RawMessage, bool) {
	h.t.Helper()
	ids := h.handlers[event]
	if len(ids) == 0 {
		h.t.Fatalf("no handler registered for event %q", event)
	}
	raw, err := json.Marshal(data)
	if err != nil {
		h.t.Fatalf("marshal event args: %v", err)
	}
	h.mu.Lock()
	h.nextID++
	id := fmt.Sprintf("req-%d", h.nextID)
	ch := make(chan json.RawMessage, 1)
	h.pending[id] = ch
	h.mu.Unlock()
	h.write(map[string]any{"type": "request", "id": id, "request": map[string]any{
		"method":     "event",
		"event":      event,
		"handler_id": ids[len(ids)-1],
		"args":       json.RawMessage(raw),
	}})
	select {
	case res := <-ch:
		h.mu.Lock()
		errMsg := h.failures[id]
		delete(h.failures, id)
		h.mu.Unlock()
		if errMsg != "" {
			h.t.Fatalf("event %q failed: %s", event, errMsg)
		}
		return res, true
	case <-time.After(timeout):
		return nil, false
	}
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

func (h *fakeHost) notifications() []string {
	var out []string
	for _, c := range h.callsTo("ui.notify") {
		if msg, ok := c.Args["message"].(string); ok {
			out = append(out, msg)
		}
	}
	return out
}

func (h *fakeHost) lastNotify() string {
	ns := h.notifications()
	if len(ns) == 0 {
		return ""
	}
	return ns[len(ns)-1]
}

func (h *fakeHost) roundTrip(request map[string]any) (json.RawMessage, string) {
	h.mu.Lock()
	h.nextID++
	id := fmt.Sprintf("req-%d", h.nextID)
	ch := make(chan json.RawMessage, 1)
	h.pending[id] = ch
	h.mu.Unlock()
	h.write(map[string]any{"type": "request", "id": id, "request": request})
	select {
	case res := <-ch:
		h.mu.Lock()
		errMsg := h.failures[id]
		delete(h.failures, id)
		h.mu.Unlock()
		return res, errMsg
	case <-time.After(5 * time.Second):
		h.t.Fatalf("timed out waiting for response to %s (%v)", id, request["method"])
		return nil, ""
	}
}

func (h *fakeHost) serve() {
	for {
		f, ok := h.readMaybe()
		if !ok {
			return
		}
		switch f.Type {
		case "call":
			// Handle calls in a goroutine so nested requests (e.g. `with_session`
			// inside `newSession`) can round-trip without deadlocking the reader.
			go h.answer(f)
		case "response":
			h.mu.Lock()
			ch := h.pending[f.ID]
			delete(h.pending, f.ID)
			var res json.RawMessage
			if f.Response != nil {
				res = f.Response.Result
				if f.Response.Error != nil {
					h.failures[f.ID] = f.Response.Error.Message
				}
			}
			h.mu.Unlock()
			if ch != nil {
				ch <- res
			}
		}
	}
}

func isCommandOnlyMethod(method string) bool {
	switch method {
	case "waitForIdle", "newSession", "fork", "navigateTree", "switchSession", "reload":
		return true
	}
	return false
}

func (h *fakeHost) answer(f wireFrame) {
	var args map[string]any
	if len(f.Call.Args) > 0 {
		_ = json.Unmarshal(f.Call.Args, &args)
	}
	h.mu.Lock()
	h.calls = append(h.calls, hostCall{Method: f.Call.Method, Args: args, CommandContext: f.Call.CommandContext})
	h.mu.Unlock()

	// Enforce PiG's real command-only call restriction
	// (coding/extension/host/subprocess/command_only_calls.go:8-21).
	if isCommandOnlyMethod(f.Call.Method) && !f.Call.CommandContext {
		h.sendCallResult(f.ID, nil, fmt.Sprintf("ctx.%s is not a function: only a command context has it", f.Call.Method))
		return
	}

	if h.opts.OnCall != nil {
		if res, errMsg := h.opts.OnCall(f.Call.Method, args); res != nil || errMsg != "" {
			h.sendCallResult(f.ID, res, errMsg)
			return
		}
	}

	var res any = map[string]any{}
	var errMsg string

	switch f.Call.Method {
	case "getSessionID":
		h.mu.Lock()
		res = map[string]any{"sessionId": h.sessionID}
		h.mu.Unlock()
	case "getSessionFile":
		res = map[string]any{"sessionFile": h.opts.SessionFile}
	case "getSessionName":
		h.mu.Lock()
		res = map[string]any{"name": h.sessionName}
		h.mu.Unlock()
	case "setSessionName":
		h.mu.Lock()
		h.sessionName, _ = args["name"].(string)
		h.mu.Unlock()
	case "getActiveTools":
		h.mu.Lock()
		res = map[string]any{"tools": append([]string(nil), h.activeTools...)}
		h.mu.Unlock()
	case "setActiveTools":
		h.mu.Lock()
		if raw, ok := args["tools"].([]any); ok {
			var next []string
			for _, v := range raw {
				if s, ok := v.(string); ok {
					next = append(next, s)
				}
			}
			h.activeTools = next
		}
		h.mu.Unlock()
	case "getAllTools":
		if len(h.opts.AllTools) > 0 {
			res = map[string]any{"tools": h.opts.AllTools}
		} else {
			var tools []map[string]any
			seen := map[string]bool{}
			for _, name := range []string{"read", "write", "edit", "bash", "grep", "glob", "ask", "task", "eval", "todo", "propose", "goal"} {
				entry := map[string]any{
					"name":        name,
					"description": "tool " + name,
					"parameters":  map[string]any{"type": "object"},
					"exposure":    string(sdk.ToolExposureDirect),
				}
				if reg, ok := h.tools[name]; ok {
					entry["description"] = reg.Description
					if len(reg.Parameters) > 0 {
						entry["parameters"] = reg.Parameters
					}
					if reg.Exposure != "" {
						entry["exposure"] = reg.Exposure
					}
				}
				tools = append(tools, entry)
				seen[name] = true
			}
			for name, reg := range h.tools {
				if seen[name] {
					continue
				}
				entry := map[string]any{
					"name":        name,
					"description": reg.Description,
					"parameters":  reg.Parameters,
					"exposure":    reg.Exposure,
				}
				tools = append(tools, entry)
			}
			res = map[string]any{"tools": tools}
		}
	case "executeTool":
		// Enforce PiG's host rule: a `direct` tool is callable only while in activeTools;
		// a `codemode` (or `deferred`) tool is always callable via ExecuteTool even when inactive.
		name, _ := args["name"].(string)
		h.mu.Lock()
		meta, registered := h.tools[name]
		isActive := containsString(h.activeTools, name)
		h.mu.Unlock()
		if !registered {
			res = map[string]any{
				"toolCall": map[string]any{"name": name},
				"result":   map[string]any{"content": []any{map[string]any{"type": "text", "text": fmt.Sprintf("Unknown tool: %s", name)}}, "is_error": true},
				"isError":  true,
			}
			break
		}
		if meta.Exposure == string(sdk.ToolExposureDirect) && !isActive {
			res = map[string]any{
				"toolCall": map[string]any{"name": name},
				"result":   map[string]any{"content": []any{map[string]any{"type": "text", "text": fmt.Sprintf("Tool %q is not active (direct exposure requires active status)", name)}}, "is_error": true},
				"isError":  true,
			}
			break
		}
		rawArgs, _ := json.Marshal(args["args"])
		toolRes, toolErr := h.roundTrip(map[string]any{
			"method":       "tool_call",
			"tool":         name,
			"tool_call_id": "nested-1",
			"args":         json.RawMessage(rawArgs),
		})
		if toolErr != "" {
			res = map[string]any{
				"toolCall": map[string]any{"name": name},
				"result":   map[string]any{"content": []any{map[string]any{"type": "text", "text": toolErr}}, "is_error": true},
				"isError":  true,
			}
			break
		}
		var decoded struct {
			Content any  `json:"content"`
			Details any  `json:"details"`
			IsError bool `json:"isError"`
		}
		_ = json.Unmarshal(toolRes, &decoded)
		text := ""
		if s, ok := decoded.Content.(string); ok {
			text = s
		}
		res = map[string]any{
			"toolCall": map[string]any{"name": name},
			"result": map[string]any{
				"content":  []any{map[string]any{"type": "text", "text": text}},
				"details":  decoded.Details,
				"is_error": decoded.IsError,
			},
			"isError": decoded.IsError,
		}
	case "getThinkingLevel":
		h.mu.Lock()
		res = map[string]any{"level": h.thinkingLevel}
		h.mu.Unlock()
	case "setThinkingLevel":
		h.mu.Lock()
		h.thinkingLevel, _ = args["level"].(string)
		h.mu.Unlock()
	case "getModelInfo":
		reasoning := true
		if h.opts.ModelReasoning != nil {
			reasoning = *h.opts.ModelReasoning
		}
		h.mu.Lock()
		res = map[string]any{
			"id":                  h.model,
			"provider":            h.modelProvider,
			"contextWindow":       h.opts.ContextWindow,
			"reasoning":           reasoning,
			"inputCostPer1M":      3.0,
			"outputCostPer1M":     15.0,
			"cacheReadCostPer1M":  0.3,
			"cacheWriteCostPer1M": 3.75,
		}
		h.mu.Unlock()
	case "getModel":
		reasoning := true
		if h.opts.ModelReasoning != nil {
			reasoning = *h.opts.ModelReasoning
		}
		m := map[string]any{
			"id":        args["modelId"],
			"provider":  args["provider"],
			"reasoning": reasoning,
			"thinkingLevelMap": map[string]any{
				"off": "off", "minimal": "minimal", "low": "low", "medium": "medium", "high": "high", "xhigh": "xhigh", "max": "max",
			},
		}
		if h.opts.PerMessageEffort {
			m["compat"] = map[string]any{"supportsPerMessageEffort": true}
		}
		res = m
	case "resolveModel":
		spec, _ := args["spec"].(string)
		if spec == "unknown/bad" {
			res = map[string]any{"error": "Model not found: unknown/bad"}
		} else {
			res = map[string]any{"model": map[string]any{"id": spec, "provider": "openai"}}
		}
	case "setModel":
		res = map[string]any{"success": true}
	case "getModelAuth":
		res = map[string]any{"apiKey": "test-key"}
	case "getSystemPrompt":
		res = map[string]any{"prompt": h.opts.SystemPrompt}
	case "getContextUsage":
		tokens := 12000
		pct := float64(tokens) / float64(h.opts.ContextWindow) * 100
		res = map[string]any{"tokens": tokens, "contextWindow": h.opts.ContextWindow, "percent": pct}
	case "isIdle":
		idle := true
		if h.opts.Idle != nil {
			idle = *h.opts.Idle
		}
		res = map[string]any{"idle": idle}
	case "session.read", "session.getBranch":
		res = h.opts.Branch
	case "getBranch":
		res = map[string]any{"entries": h.opts.Branch}
	case "getEntries", "session.getEntries":
		res = h.opts.Entries
	case "watchSessionLog", "getSessionLog":
		var rawEntries []map[string]any
		leafID := ""
		for i, e := range h.opts.Branch {
			if id, _ := e["id"].(string); id != "" {
				if i > 0 {
					if _, hasParent := e["parentId"]; !hasParent {
						if prevID, _ := h.opts.Branch[i-1]["id"].(string); prevID != "" {
							e["parentId"] = prevID
						}
					}
				}
				leafID = id
			}
			rawEntries = append(rawEntries, e)
		}
		// Entries outside Branch represent sibling/abandoned branches in the same
		// JSONL file (their IDs are not ancestors of leafID).
		rawEntries = append(rawEntries, h.opts.Entries...)
		cursor := 0
		if c, ok := args["cursor"].(float64); ok {
			cursor = int(c)
		}
		var slice []map[string]any
		if cursor < len(rawEntries) {
			slice = rawEntries[cursor:]
		} else {
			slice = []map[string]any{}
		}
		res = map[string]any{
			"entries":    slice,
			"entryCount": len(rawEntries),
			"hasMore":    false,
			"leafId":     leafID,
		}
	case "clipboard.copy", "copyToClipboard":
		h.mu.Lock()
		h.clipboard, _ = args["text"].(string)
		h.mu.Unlock()
	case "ui.setStatus":
		h.mu.Lock()
		k, _ := args["key"].(string)
		if txt, ok := args["text"].(string); ok {
			h.statuses[k] = txt
		} else {
			delete(h.statuses, k)
		}
		h.mu.Unlock()
	case "newSession":
		if handle, ok := args["withSession"].(string); ok && handle != "" {
			nextSID := h.opts.ReplacementSessID
			if nextSID == "" {
				nextSID = h.opts.SessionID + "-next"
			}
			h.mu.Lock()
			h.sessionID = nextSID
			h.sessionName = ""
			h.mu.Unlock()
			hasUI := true
			wsArgs, _ := json.Marshal(map[string]any{
				"handle": handle,
				"ready": map[string]any{
					"cwd":  h.opts.Cwd,
					"mode": h.opts.Mode,
					"state": map[string]any{
						"hasUI": hasUI,
						"model": map[string]any{"id": h.model, "provider": h.modelProvider},
					},
				},
			})
			_, wsErr := h.roundTrip(map[string]any{
				"method": "with_session",
				"args":   json.RawMessage(wsArgs),
			})
			if wsErr != "" {
				errMsg = wsErr
				break
			}
		}
		res = map[string]any{"cancelled": false}
	case "navigateTree", "fork":
		res = map[string]any{"cancelled": false}
	case "compact":
		res = map[string]any{"summary": "compacted"}
	case "ui.confirm":
		res = map[string]any{"confirmed": true}
	case "ui.select":
		res = map[string]any{"ok": false}
	case "ui.input", "ui.editor":
		res = map[string]any{"ok": false}
	}
	h.sendCallResult(f.ID, res, errMsg)
}

func (h *fakeHost) sendCallResult(id string, res any, errMsg string) {
	payload := map[string]any{}
	if errMsg != "" {
		payload["error"] = map[string]any{"message": errMsg}
	} else {
		raw, _ := json.Marshal(res)
		payload["result"] = json.RawMessage(raw)
	}
	h.write(map[string]any{"type": "call_result", "id": id, "call_result": payload})
}

func (h *fakeHost) read() wireFrame {
	f, ok := h.readMaybe()
	if !ok {
		h.t.Fatalf("unexpected EOF from extension")
	}
	return f
}

func (h *fakeHost) readMaybe() (wireFrame, bool) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(h.nc, lenBuf[:]); err != nil {
		return wireFrame{}, false
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	buf := make([]byte, n)
	if _, err := io.ReadFull(h.nc, buf); err != nil {
		return wireFrame{}, false
	}
	var f wireFrame
	if err := json.Unmarshal(buf, &f); err != nil {
		return wireFrame{}, false
	}
	return f, true
}

func (h *fakeHost) write(v any) {
	raw, err := json.Marshal(v)
	if err != nil {
		h.t.Fatalf("marshal wire frame: %v", err)
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(raw)))
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	_, _ = h.nc.Write(hdr[:])
	_, _ = h.nc.Write(raw)
}

func (h *fakeHost) stop() {
	h.write(map[string]any{"type": "shutdown"})
	_ = h.nc.Close()
	select {
	case <-h.runDone:
	case <-time.After(2 * time.Second):
	}
}
