package vibe

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

type fakeTool struct {
	Name          string          `json:"name"`
	Label         string          `json:"label"`
	Description   string          `json:"description"`
	Parameters    json.RawMessage `json:"parameters"`
	DefaultActive *bool           `json:"default_active,omitempty"`
}

type fakeWireFrame struct {
	Type     string `json:"type"`
	ID       string `json:"id,omitempty"`
	Register *struct {
		Handlers []struct {
			Event     string `json:"event"`
			HandlerID int    `json:"handler_id"`
		} `json:"handlers"`
		Tools    []fakeTool `json:"tools"`
		Commands []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
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
}

type fakeHostOpts struct {
	SessionID      string
	SessionFile    string
	NoSessionFile  bool
	Cwd            string
	Mode           string
	HasUI          bool
	IsIdle         bool
	InitialTools   []string
	InitialActive  []string
	InitialEntries []json.RawMessage
	InitialBranch  []sdk.BranchEntry
	FailNextAppend bool
	ActiveModel    string
}

type fakeHost struct {
	t           *testing.T
	nc          net.Conn
	runDone     chan error
	writeMu     sync.Mutex
	mu          sync.Mutex
	sessionID   string
	sessionFile string
	cwd         string
	mode        string
	hasUI       bool
	isIdle      bool
	activeModel string

	tools            map[string]fakeTool
	toolOrder        []string
	activeTools      []string
	commands         map[string]string
	handlers         map[string][]int
	pending          map[string]chan fakeWireFrame
	nextID           int
	notifications    []map[string]any
	statuses         map[string]string
	sentMessages     []map[string]any
	sentUserMessages []map[string]any
	appendedEntries  []map[string]any
	entries          []json.RawMessage
	branch           []sdk.BranchEntry
	abortedCount     int
	failNextAppend   bool
	toolUpdates      []map[string]any
}

func startFakeHost(t *testing.T, ext *sdk.Extension, opts fakeHostOpts) *fakeHost {
	t.Helper()
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("USERPROFILE", fakeHome)
	t.Setenv("PIG_HOME", filepath.Join(fakeHome, ".pig"))
	t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(fakeHome, ".pig", "agent"))

	cwd := opts.Cwd
	if cwd == "" {
		cwd = t.TempDir()
	}
	sid := opts.SessionID
	if sid == "" {
		sid = "vibe-test-sess"
	}
	sf := opts.SessionFile
	if sf == "" && !opts.NoSessionFile {
		sf = filepath.Join(t.TempDir(), sid+".jsonl")
	}
	mode := opts.Mode
	if mode == "" {
		mode = "tui"
	}

	hostSide, extSide := net.Pipe()
	h := &fakeHost{
		t:              t,
		nc:             hostSide,
		runDone:        make(chan error, 1),
		sessionID:      sid,
		sessionFile:    sf,
		cwd:            cwd,
		mode:           mode,
		hasUI:          opts.HasUI,
		isIdle:         opts.IsIdle,
		activeModel:    opts.ActiveModel,
		tools:          map[string]fakeTool{},
		commands:       map[string]string{},
		handlers:       map[string][]int{},
		pending:        map[string]chan fakeWireFrame{},
		statuses:       map[string]string{},
		entries:        append([]json.RawMessage(nil), opts.InitialEntries...),
		branch:         append([]sdk.BranchEntry(nil), opts.InitialBranch...),
		failNextAppend: opts.FailNextAppend,
	}
	for _, name := range opts.InitialTools {
		h.tools[name] = fakeTool{Name: name, Label: name, Description: name + " tool"}
		h.toolOrder = append(h.toolOrder, name)
	}
	h.activeTools = append([]string(nil), opts.InitialActive...)

	go func() { h.runDone <- ext.RunWithConn(extSide) }()

	first := h.readFrame()
	for first.Type == "call" && first.Call != nil {
		h.answerCall(first)
		first = h.readFrame()
	}
	if first.Type != "register" || first.Register == nil {
		t.Fatalf("expected register frame, got %+v", first)
	}
	for _, hh := range first.Register.Handlers {
		h.handlers[hh.Event] = append(h.handlers[hh.Event], hh.HandlerID)
	}
	for _, tl := range first.Register.Tools {
		if _, ok := h.tools[tl.Name]; !ok {
			h.toolOrder = append(h.toolOrder, tl.Name)
		}
		h.tools[tl.Name] = tl
		if tl.DefaultActive == nil || *tl.DefaultActive {
			if !slices.Contains(h.activeTools, tl.Name) {
				h.activeTools = append(h.activeTools, tl.Name)
			}
		}
	}
	for _, cmd := range first.Register.Commands {
		h.commands[cmd.Name] = cmd.Description
	}
	stateObj := map[string]any{
		"hasUI":       opts.HasUI,
		"sessionId":   sid,
		"sessionFile": sf,
		"session":     map[string]any{"sessionId": sid, "sessionFile": sf},
		"model":       map[string]any{"provider": "anthropic", "id": "claude-sonnet-4-5", "contextWindow": 200000},
	}
	stateRaw, _ := json.Marshal(stateObj)
	h.writeFrame(map[string]any{
		"type": "ready",
		"ready": map[string]any{
			"session_name": "",
			"cwd":          cwd,
			"mode":         mode,
			"width":        80,
			"state":        json.RawMessage(stateRaw),
		},
	})
	go h.serve()
	t.Cleanup(func() {
		_ = hostSide.Close()
		select {
		case <-h.runDone:
		case <-time.After(2 * time.Second):
		}
	})
	return h
}

func (h *fakeHost) readFrame() fakeWireFrame {
	h.t.Helper()
	var n uint32
	if err := binary.Read(h.nc, binary.BigEndian, &n); err != nil {
		h.t.Fatalf("read frame len: %v", err)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(h.nc, buf); err != nil {
		h.t.Fatalf("read frame body: %v", err)
	}
	var f fakeWireFrame
	if err := json.Unmarshal(buf, &f); err != nil {
		h.t.Fatalf("unmarshal frame: %v", err)
	}
	return f
}

func (h *fakeHost) writeFrame(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	_ = binary.Write(h.nc, binary.BigEndian, uint32(len(data)))
	_, _ = h.nc.Write(data)
}

func (h *fakeHost) serve() {
	for {
		var n uint32
		if err := binary.Read(h.nc, binary.BigEndian, &n); err != nil {
			return
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(h.nc, buf); err != nil {
			return
		}
		var f fakeWireFrame
		if err := json.Unmarshal(buf, &f); err != nil {
			return
		}
		switch f.Type {
		case "call":
			if f.Call != nil {
				go h.answerCall(f)
			}
		case "response":
			h.mu.Lock()
			ch := h.pending[f.ID]
			delete(h.pending, f.ID)
			h.mu.Unlock()
			if ch != nil {
				ch <- f
			}
		}
	}
}

func (h *fakeHost) answerCall(f fakeWireFrame) {
	var args map[string]any
	if len(f.Call.Args) > 0 {
		_ = json.Unmarshal(f.Call.Args, &args)
	}
	var result any = map[string]any{}
	var errMsg string

	switch f.Call.Method {
	case "registerTool":
		var tl fakeTool
		if err := json.Unmarshal(f.Call.Args, &tl); err == nil && tl.Name != "" {
			h.mu.Lock()
			if _, ok := h.tools[tl.Name]; !ok {
				h.toolOrder = append(h.toolOrder, tl.Name)
			}
			h.tools[tl.Name] = tl
			h.mu.Unlock()
		}
	case "getAllTools":
		h.mu.Lock()
		list := make([]map[string]any, 0, len(h.toolOrder))
		for _, name := range h.toolOrder {
			tl := h.tools[name]
			list = append(list, map[string]any{
				"name":        tl.Name,
				"description": tl.Description,
				"parameters":  tl.Parameters,
				"source":      "extension",
			})
		}
		h.mu.Unlock()
		result = map[string]any{"tools": list}
	case "getActiveTools":
		h.mu.Lock()
		cp := append([]string(nil), h.activeTools...)
		h.mu.Unlock()
		if cp == nil {
			cp = []string{}
		}
		result = map[string]any{"tools": cp}
	case "setActiveTools":
		if raw, ok := args["tools"].([]any); ok {
			next := make([]string, 0, len(raw))
			for _, it := range raw {
				if s, ok := it.(string); ok {
					next = append(next, s)
				}
			}
			h.mu.Lock()
			h.activeTools = next
			h.mu.Unlock()
		}
	case "sessionRead":
		m, _ := args["method"].(string)
		h.mu.Lock()
		switch m {
		case "getSessionId":
			result = h.sessionID
		case "getSessionFile":
			if h.sessionFile != "" {
				result = h.sessionFile
			} else {
				result = nil
			}
		case "getCwd":
			result = h.cwd
		case "getEntries":
			list := make([]any, 0, len(h.entries))
			for _, raw := range h.entries {
				var v any
				_ = json.Unmarshal(raw, &v)
				list = append(list, v)
			}
			result = list
		case "getBranch":
			result = h.branch
		default:
			result = nil
		}
		h.mu.Unlock()
	case "watchSessionLog", "getSessionLog":
		h.mu.Lock()
		var rawEntries []map[string]any
		leafID := ""
		for _, raw := range h.entries {
			var m map[string]any
			if json.Unmarshal(raw, &m) == nil && m != nil {
				if id, _ := m["id"].(string); id != "" {
					leafID = id
				}
				rawEntries = append(rawEntries, m)
			}
		}
		h.mu.Unlock()
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
		result = map[string]any{
			"entries":    slice,
			"entryCount": len(rawEntries),
			"hasMore":    false,
			"leafId":     leafID,
		}
	case "getSessionId":
		h.mu.Lock()
		result = map[string]any{"id": h.sessionID}
		h.mu.Unlock()
	case "getSessionFile":
		h.mu.Lock()
		if h.sessionFile != "" {
			result = map[string]any{"file": h.sessionFile}
		} else {
			result = map[string]any{"file": nil}
		}
		h.mu.Unlock()
	case "appendEntry":
		h.mu.Lock()
		if h.failNextAppend {
			h.failNextAppend = false
			errMsg = "journal atomic publish failed"
		} else {
			h.appendedEntries = append(h.appendedEntries, args)
			entryObj := map[string]any{
				"id":         "e-" + time.Now().Format("150405.000000"),
				"type":       "custom",
				"customType": args["customType"],
				"data":       args["data"],
				"timestamp":  time.Now().UTC().Format(time.RFC3339),
			}
			raw, _ := json.Marshal(entryObj)
			h.entries = append(h.entries, raw)
		}
		h.mu.Unlock()
	case "isIdle":
		h.mu.Lock()
		result = map[string]any{"idle": h.isIdle}
		h.mu.Unlock()
	case "abort":
		h.mu.Lock()
		h.abortedCount++
		h.isIdle = true
		h.mu.Unlock()
	case "sendMessage":
		h.mu.Lock()
		h.sentMessages = append(h.sentMessages, args)
		h.mu.Unlock()
	case "sendUserMessage":
		h.mu.Lock()
		h.sentUserMessages = append(h.sentUserMessages, args)
		h.mu.Unlock()
	case "ui.notify":
		h.mu.Lock()
		h.notifications = append(h.notifications, args)
		h.mu.Unlock()
	case "ui.setStatus":
		h.mu.Lock()
		key, _ := args["key"].(string)
		val, _ := args["text"].(string)
		if val == "" {
			delete(h.statuses, key)
		} else {
			h.statuses[key] = val
		}
		h.mu.Unlock()
	case "toolUpdate":
		h.mu.Lock()
		h.toolUpdates = append(h.toolUpdates, args)
		h.mu.Unlock()
	case "configHome":
		result = map[string]any{"path": filepath.Join(h.cwd, ".pig")}
	case "isProjectTrusted":
		result = map[string]any{"trusted": false}
	case "getSettings":
		result = map[string]any{"settings": map[string]any{}}
	}

	cr := map[string]any{}
	if errMsg != "" {
		cr["error"] = map[string]any{"message": errMsg}
	} else {
		rawRes, _ := json.Marshal(result)
		cr["result"] = json.RawMessage(rawRes)
	}
	h.writeFrame(map[string]any{
		"type":        "call_result",
		"id":          f.ID,
		"call_result": cr,
	})
}

func (h *fakeHost) request(req map[string]any) fakeWireFrame {
	h.t.Helper()
	h.mu.Lock()
	h.nextID++
	id := "req-" + jsonNumber(h.nextID)
	ch := make(chan fakeWireFrame, 1)
	h.pending[id] = ch
	h.mu.Unlock()

	h.writeFrame(map[string]any{
		"type":    "request",
		"id":      id,
		"request": req,
	})

	select {
	case resp := <-ch:
		return resp
	case <-time.After(10 * time.Second):
		h.t.Fatalf("timed out waiting for response to %v", req["method"])
		return fakeWireFrame{}
	}
}

func jsonNumber(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func (h *fakeHost) baseCtx() map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	ctx := map[string]any{
		"cwd":         h.cwd,
		"has_ui":      h.hasUI,
		"mode":        h.mode,
		"session_id":  h.sessionID,
		"sessionFile": h.sessionFile,
	}
	if h.activeModel != "" {
		ctx["model"] = h.activeModel
	}
	return ctx
}

func (h *fakeHost) emitEvent(event string, data map[string]any) []json.RawMessage {
	h.t.Helper()
	h.mu.Lock()
	ids := append([]int(nil), h.handlers[event]...)
	h.mu.Unlock()
	if data == nil {
		data = map[string]any{}
	}
	if event == sdk.EventBeforeAgentStart {
		if _, ok := data["systemPromptOptions"]; !ok {
			h.mu.Lock()
			sel := append([]string(nil), h.activeTools...)
			cwd := h.cwd
			h.mu.Unlock()
			data["systemPromptOptions"] = map[string]any{
				"cwd":           cwd,
				"selectedTools": sel,
				"hiddenTools":   []string{},
			}
		}
	}
	raw, _ := json.Marshal(data)
	var out []json.RawMessage
	for _, hid := range ids {
		resp := h.request(map[string]any{
			"method":     "event",
			"handler_id": hid,
			"event":      event,
			"args":       json.RawMessage(raw),
		})
		if resp.Response != nil && resp.Response.Error != nil {
			h.t.Fatalf("event %s handler %d error: %s", event, hid, resp.Response.Error.Message)
		}
		if resp.Response != nil {
			out = append(out, resp.Response.Result)
		}
	}
	return out
}

func (h *fakeHost) callCommand(name, args string) error {
	h.t.Helper()
	resp := h.request(map[string]any{
		"method": "command",
		"tool":   name,
		"args":   args,
	})
	if resp.Response != nil && resp.Response.Error != nil {
		return &cmdError{msg: resp.Response.Error.Message}
	}
	return nil
}

type cmdError struct{ msg string }

func (e *cmdError) Error() string { return e.msg }

func (h *fakeHost) callTool(name, callID string, params map[string]any) (json.RawMessage, error) {
	h.t.Helper()
	if params == nil {
		params = map[string]any{}
	}
	// Fire EventToolCall veto hooks (approval engine).
	vetos := h.emitEvent(sdk.EventToolCall, map[string]any{
		"toolName":   name,
		"toolCallId": callID,
		"input":      params,
	})
	for _, v := range vetos {
		var m map[string]any
		if json.Unmarshal(v, &m) == nil && m != nil {
			if inner, ok := m["_pigToolCallResult"].(map[string]any); ok {
				m = inner
			}
			if block, _ := m["block"].(bool); block {
				reason, _ := m["reason"].(string)
				if reason == "" {
					reason = "Tool call blocked"
				}
				return nil, &cmdError{msg: reason}
			}
		}
	}
	rawArgs, _ := json.Marshal(params)
	resp := h.request(map[string]any{
		"method":       "tool_call",
		"tool":         name,
		"tool_call_id": callID,
		"args":         json.RawMessage(rawArgs),
	})
	if resp.Response != nil && resp.Response.Error != nil {
		return nil, &cmdError{msg: resp.Response.Error.Message}
	}
	if resp.Response == nil {
		return nil, nil
	}
	return resp.Response.Result, nil
}
func (h *fakeHost) getActiveTools() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.activeTools...)
}

func (h *fakeHost) getRegisteredToolNames() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	cp := append([]string(nil), h.toolOrder...)
	slices.Sort(cp)
	return cp
}
