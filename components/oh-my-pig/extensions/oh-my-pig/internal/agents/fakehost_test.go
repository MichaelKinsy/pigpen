package agents

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// fakeChildScript controls how one fake child session behaves when prompted.
type fakeChildScript func(h *fakeHost, child *fakeChildState, promptText string) error

type fakeChildState struct {
	mu           sync.Mutex
	id           string
	createArgs   map[string]any
	messages     []map[string]any
	steers       []string
	aborted      bool
	disposed     bool
	bound        bool
	abortCh      chan struct{}
	abortOnce    sync.Once
	eventSeq     uint64
	sessionFile  string
	activeTools  []string
	systemPrompt string
}

func (c *fakeChildState) triggerAbort() {
	c.mu.Lock()
	c.aborted = true
	c.mu.Unlock()
	c.abortOnce.Do(func() { close(c.abortCh) })
}

type fakeHostOptions struct {
	Cwd           string
	SessionID     string
	SessionFile   string
	NoSessionFile bool
	Mode          string
	HasUI         bool
	IsIdle        bool
	ActiveModel   map[string]any
	OnChild       fakeChildScript
	Models        []map[string]any
}

type fakeHost struct {
	t           *testing.T
	nc          net.Conn
	opts        fakeHostOptions
	runDone     chan error
	writeMu     sync.Mutex
	mu          sync.Mutex
	calls       []fakeHostCall
	sentMsgs    []map[string]any
	toolUpdates []map[string]any
	toolDescs   map[string]string
	toolParams  map[string]map[string]any
	children    map[string]*fakeChildState
	handlers    map[string][]int
	tools       map[string]bool
	pending     map[string]chan json.RawMessage
	failures    map[string]string
	nextID      int
	stopped     bool
}
type fakeHostCall struct {
	Method string
	Args   map[string]any
}

type fakeWireFrame struct {
	Type     string `json:"type"`
	ID       string `json:"id,omitempty"`
	Register *struct {
		Handlers []struct {
			Event     string `json:"event"`
			HandlerID int    `json:"handler_id"`
		} `json:"handlers"`
		Tools []struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			Parameters  map[string]any `json:"parameters"`
		} `json:"tools"`
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

func startFakeHost(t *testing.T, ext *sdk.Extension, opts fakeHostOptions) *fakeHost {
	t.Helper()
	// Isolate HOME/USERPROFILE/PIG_HOME/PIG_CODING_AGENT_DIR so tests never read
	// the developer's real ~/.pig or ~/.omp agent/config files.
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("USERPROFILE", fakeHome)
	t.Setenv("PIG_HOME", filepath.Join(fakeHome, ".pig"))
	t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(fakeHome, ".pig", "agent"))
	if opts.Cwd == "" {
		opts.Cwd = t.TempDir()
	}
	if opts.SessionID == "" {
		opts.SessionID = "test-session-1"
	}
	if opts.SessionFile == "" && !opts.NoSessionFile {
		opts.SessionFile = filepath.Join(opts.Cwd, "root-session.jsonl")
	}
	mode := opts.Mode
	if mode == "" {
		mode = "print"
	}
	activeModel := opts.ActiveModel
	if activeModel == nil {
		activeModel = map[string]any{"provider": "anthropic", "id": "claude-sonnet-4-5", "contextWindow": 200000}
	}
	hostSide, extSide := net.Pipe()
	h := &fakeHost{
		t:          t,
		nc:         hostSide,
		opts:       opts,
		runDone:    make(chan error, 1),
		toolDescs:  map[string]string{},
		toolParams: map[string]map[string]any{},
		children:   map[string]*fakeChildState{},
		handlers:   map[string][]int{},
		tools:      map[string]bool{},
		pending:    map[string]chan json.RawMessage{},
		failures:   map[string]string{},
	}
	go func() { h.runDone <- ext.RunWithConn(extSide) }()

	first := h.read()
	for first.Type == "call" && first.Call != nil {
		h.answerCall(first)
		first = h.read()
	}
	if first.Type != "register" || first.Register == nil {
		t.Fatalf("expected register frame, got %+v", first)
	}
	for _, hnd := range first.Register.Handlers {
		h.handlers[hnd.Event] = append(h.handlers[hnd.Event], hnd.HandlerID)
	}
	for _, tl := range first.Register.Tools {
		h.tools[tl.Name] = true
		h.toolDescs[tl.Name] = tl.Description
		h.toolParams[tl.Name] = tl.Parameters
	}
	sessObj := map[string]any{
		"sessionId": opts.SessionID,
	}
	if !opts.NoSessionFile && opts.SessionFile != "" {
		sessObj["sessionFile"] = opts.SessionFile
	}
	stateObj := map[string]any{
		"hasUI":       opts.HasUI,
		"sessionId":   opts.SessionID,
		"sessionFile": opts.SessionFile,
		"session":     sessObj,
		"model":       activeModel,
	}
	stateRaw, _ := json.Marshal(stateObj)
	h.write(map[string]any{
		"type": "ready",
		"ready": map[string]any{
			"session_name": "",
			"cwd":          opts.Cwd,
			"mode":         mode,
			"width":        80,
			"state":        json.RawMessage(stateRaw),
		},
	})
	go h.serve()
	t.Cleanup(h.stop)
	return h
}

func (h *fakeHost) read() fakeWireFrame {
	var n uint32
	if err := binary.Read(h.nc, binary.BigEndian, &n); err != nil {
		if err == io.EOF {
			return fakeWireFrame{}
		}
		h.t.Fatalf("read frame length: %v", err)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(h.nc, buf); err != nil {
		h.t.Fatalf("read frame body: %v", err)
	}
	var f fakeWireFrame
	if err := json.Unmarshal(buf, &f); err != nil {
		h.t.Fatalf("decode frame: %v", err)
	}
	return f
}

func (h *fakeHost) write(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		h.t.Fatalf("encode frame: %v", err)
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
				// Run each call concurrently so childSession.prompt never blocks childSession.abort/queue.
				go h.answerCall(f)
			}
		case "notify":
			if f.Notify != nil && f.Notify.Method == "tool_update" {
				var u map[string]any
				_ = json.Unmarshal(f.Notify.Args, &u)
				h.mu.Lock()
				h.toolUpdates = append(h.toolUpdates, u)
				h.mu.Unlock()
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
				if f.Response != nil {
					ch <- f.Response.Result
				} else {
					ch <- nil
				}
			}
		}
	}
}

func (h *fakeHost) emitChildEvent(child *fakeChildState, event map[string]any) uint64 {
	child.mu.Lock()
	child.eventSeq++
	seq := child.eventSeq
	child.mu.Unlock()
	rawEvent, _ := json.Marshal(event)
	args, _ := json.Marshal(map[string]any{
		"id":    child.id,
		"seq":   seq,
		"event": json.RawMessage(rawEvent),
	})
	h.write(map[string]any{
		"type": "notify",
		"notify": map[string]any{
			"method": "childSession.event",
			"args":   json.RawMessage(args),
		},
	})
	return seq
}

func (h *fakeHost) answerCall(f fakeWireFrame) {
	var args map[string]any
	if len(f.Call.Args) > 0 {
		_ = json.Unmarshal(f.Call.Args, &args)
	}
	h.mu.Lock()
	h.calls = append(h.calls, fakeHostCall{Method: f.Call.Method, Args: args})
	h.mu.Unlock()

	var result any = map[string]any{}
	var errMsg string

	switch f.Call.Method {
	case "registerTool":
		if name, ok := args["name"].(string); ok {
			desc, _ := args["description"].(string)
			params, _ := args["parameters"].(map[string]any)
			h.mu.Lock()
			h.tools[name] = true
			h.toolDescs[name] = desc
			h.toolParams[name] = params
			h.mu.Unlock()
		}
	case "sendMessage":
		h.mu.Lock()
		h.sentMsgs = append(h.sentMsgs, args)
		h.mu.Unlock()
	case "isIdle":
		result = map[string]any{"idle": h.opts.IsIdle}
	case "getActiveTools":
		h.mu.Lock()
		names := make([]string, 0, len(h.tools))
		for k := range h.tools {
			names = append(names, k)
		}
		h.mu.Unlock()
		result = map[string]any{"tools": names}
	case "sessionRead":
		m, _ := args["method"].(string)
		switch m {
		case "getSessionId":
			result = h.opts.SessionID
		case "getSessionFile":
			if h.opts.NoSessionFile {
				result = nil
			} else {
				result = h.opts.SessionFile
			}
		case "getCwd":
			result = h.opts.Cwd
		default:
			result = nil
		}
	case "getModel":
		prov, _ := args["provider"].(string)
		mid, _ := args["modelId"].(string)
		models := h.opts.Models
		if models == nil {
			models = []map[string]any{
				{"provider": "anthropic", "id": "claude-sonnet-4-5", "contextWindow": 200000},
				{"provider": "openai", "id": "gpt-5-mini", "contextWindow": 128000},
			}
		}
		result = nil
		for _, m := range models {
			if m["provider"] == prov && m["id"] == mid {
				result = m
				break
			}
		}
	case "getModelRegistryState":
		models := h.opts.Models
		if models == nil {
			models = []map[string]any{
				{"provider": "anthropic", "id": "claude-sonnet-4-5", "contextWindow": 200000},
				{"provider": "openai", "id": "gpt-5-mini", "contextWindow": 128000},
			}
		}
		result = map[string]any{
			"models": models,
			"providers": map[string]any{
				"anthropic": map[string]any{"name": "anthropic", "configured": true},
				"openai":    map[string]any{"name": "openai", "configured": true},
			},
		}
	case "childSession.create":
		id, _ := args["id"].(string)
		modelMap, _ := args["model"].(map[string]any)
		if modelMap == nil {
			modelMap = map[string]any{"provider": "anthropic", "id": "claude-sonnet-4-5", "contextWindow": 200000}
		}
		think, _ := args["thinkingLevel"].(string)
		child := &fakeChildState{
			id:         id,
			createArgs: args,
			abortCh:    make(chan struct{}),
		}
		h.mu.Lock()
		h.children[id] = child
		h.mu.Unlock()
		result = map[string]any{
			"sessionId":     "sess-" + id,
			"sessionFile":   "",
			"model":         modelMap,
			"thinkingLevel": think,
			"activeTools":   []string{},
			"systemPrompt":  "",
		}
	case "childSession.bindExtensions":
		id, _ := args["id"].(string)
		h.mu.Lock()
		child := h.children[id]
		h.mu.Unlock()
		if child == nil {
			errMsg = "Unknown or disposed child session"
		} else {
			child.mu.Lock()
			child.bound = true
			child.mu.Unlock()
		}
	case "childSession.prompt":
		id, _ := args["id"].(string)
		promptText, _ := args["text"].(string)
		h.mu.Lock()
		child := h.children[id]
		h.mu.Unlock()
		if child == nil {
			errMsg = "Unknown or disposed child session"
		} else {
			child.mu.Lock()
			child.messages = append(child.messages, map[string]any{"role": "user", "content": promptText})
			child.mu.Unlock()
			if h.opts.OnChild != nil {
				if err := h.opts.OnChild(h, child, promptText); err != nil {
					errMsg = err.Error()
				}
			} else {
				h.finishChildWithText(child, "default reply: "+promptText)
			}
			child.mu.Lock()
			evCount := child.eventSeq
			child.mu.Unlock()
			result = map[string]any{"events": evCount}
		}
	case "childSession.queue":
		id, _ := args["id"].(string)
		txt, _ := args["text"].(string)
		h.mu.Lock()
		child := h.children[id]
		h.mu.Unlock()
		if child == nil {
			errMsg = "Unknown or disposed child session"
		} else {
			child.mu.Lock()
			child.steers = append(child.steers, txt)
			child.mu.Unlock()
		}
	case "childSession.abort":
		id, _ := args["id"].(string)
		h.mu.Lock()
		child := h.children[id]
		h.mu.Unlock()
		if child != nil {
			child.triggerAbort()
		}
	case "childSession.dispose":
		id, _ := args["id"].(string)
		h.mu.Lock()
		child := h.children[id]
		h.mu.Unlock()
		if child != nil {
			child.triggerAbort()
			child.mu.Lock()
			child.disposed = true
			child.mu.Unlock()
		}
	case "childSession.messages":
		id, _ := args["id"].(string)
		h.mu.Lock()
		child := h.children[id]
		h.mu.Unlock()
		if child == nil {
			errMsg = "Unknown or disposed child session"
		} else {
			child.mu.Lock()
			result = append([]map[string]any(nil), child.messages...)
			child.mu.Unlock()
		}
	}

	reply := map[string]any{"type": "call_result", "id": f.ID}
	if errMsg != "" {
		reply["call_result"] = map[string]any{"error": map[string]any{"message": errMsg}}
	} else {
		raw, _ := json.Marshal(result)
		reply["call_result"] = map[string]any{"result": json.RawMessage(raw)}
	}
	h.write(reply)
}

func (h *fakeHost) finishChildWithText(child *fakeChildState, text string) {
	msg := map[string]any{
		"role":       "assistant",
		"content":    []any{map[string]any{"type": "text", "text": text}},
		"stopReason": "stop",
		"provider":   "anthropic",
		"model":      "claude-sonnet-4-5",
		"usage": map[string]any{
			"input":       100,
			"output":      25,
			"totalTokens": 125,
			"cost":        map[string]any{"total": 0.01},
		},
	}
	child.mu.Lock()
	child.messages = append(child.messages, msg)
	child.mu.Unlock()
	h.emitChildEvent(child, map[string]any{"type": "agent_start"})
	h.emitChildEvent(child, map[string]any{"type": "message_end", "message": msg})
	h.emitChildEvent(child, map[string]any{"type": "agent_end", "messages": []any{msg}})
}

func (h *fakeHost) finishChildWithError(child *fakeChildState, errText string) {
	msg := map[string]any{
		"role":         "assistant",
		"content":      []any{},
		"stopReason":   "error",
		"errorMessage": errText,
		"provider":     "anthropic",
		"model":        "claude-sonnet-4-5",
	}
	child.mu.Lock()
	child.messages = append(child.messages, msg)
	child.mu.Unlock()
	h.emitChildEvent(child, map[string]any{"type": "agent_start"})
	h.emitChildEvent(child, map[string]any{"type": "message_end", "message": msg})
	h.emitChildEvent(child, map[string]any{"type": "agent_end", "messages": []any{msg}})
}

func (h *fakeHost) finishChildAborted(child *fakeChildState) {
	msg := map[string]any{
		"role":       "assistant",
		"content":    []any{map[string]any{"type": "text", "text": "working on partial step"}},
		"stopReason": "aborted",
	}
	child.mu.Lock()
	child.messages = append(child.messages, msg)
	child.mu.Unlock()
	h.emitChildEvent(child, map[string]any{"type": "message_end", "message": msg})
	h.emitChildEvent(child, map[string]any{"type": "agent_end", "messages": []any{msg}})
}

// callTool invokes a registered extension tool over the wire.
func (h *fakeHost) callTool(name string, params map[string]any) (map[string]any, string) {
	h.t.Helper()
	return h.callToolAsync(name, params, nil)
}

func (h *fakeHost) callToolAsync(name string, params map[string]any, cancelAfter <-chan struct{}) (map[string]any, string) {
	h.t.Helper()
	rawArgs, _ := json.Marshal(params)
	h.mu.Lock()
	h.nextID++
	reqID := fmt.Sprintf("req-%d", h.nextID)
	ch := make(chan json.RawMessage, 1)
	h.pending[reqID] = ch
	h.mu.Unlock()

	h.write(map[string]any{
		"type": "request",
		"id":   reqID,
		"request": map[string]any{
			"method":       "tool_call",
			"tool":         name,
			"tool_call_id": "tc-" + reqID,
			"args":         json.RawMessage(rawArgs),
		},
	})

	if cancelAfter != nil {
		go func() {
			<-cancelAfter
			h.write(map[string]any{
				"type":   "cancel",
				"cancel": map[string]any{"request_id": reqID},
			})
		}()
	}

	select {
	case raw := <-ch:
		h.mu.Lock()
		errMsg := h.failures[reqID]
		delete(h.failures, reqID)
		h.mu.Unlock()
		if errMsg != "" {
			return nil, errMsg
		}
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		return out, ""
	case <-time.After(10 * time.Second):
		h.t.Fatalf("timed out waiting for tool %s response", name)
		return nil, "timeout"
	}
}

func (h *fakeHost) callChildCustomTool(childID, toolName string, params map[string]any) (map[string]any, string) {
	h.t.Helper()
	rawArgs, _ := json.Marshal(params)
	h.mu.Lock()
	h.nextID++
	reqID := fmt.Sprintf("creq-%d", h.nextID)
	ch := make(chan json.RawMessage, 1)
	h.pending[reqID] = ch
	h.mu.Unlock()

	h.write(map[string]any{
		"type": "request",
		"id":   reqID,
		"request": map[string]any{
			"method":        "tool_call",
			"tool":          toolName,
			"tool_call_id":  "ctc-" + reqID,
			"child_session": childID,
			"args":          json.RawMessage(rawArgs),
		},
	})

	select {
	case raw := <-ch:
		h.mu.Lock()
		errMsg := h.failures[reqID]
		delete(h.failures, reqID)
		h.mu.Unlock()
		if errMsg != "" {
			return nil, errMsg
		}
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		return out, ""
	case <-time.After(5 * time.Second):
		h.t.Fatalf("timed out waiting for child custom tool %s", toolName)
		return nil, "timeout"
	}
}

func (h *fakeHost) fireEvent(event string, data map[string]any) {
	h.t.Helper()
	ids := h.handlers[event]
	for _, id := range ids {
		rawArgs, _ := json.Marshal(data)
		h.mu.Lock()
		h.nextID++
		reqID := fmt.Sprintf("ev-%d", h.nextID)
		ch := make(chan json.RawMessage, 1)
		h.pending[reqID] = ch
		h.mu.Unlock()
		h.write(map[string]any{
			"type": "request",
			"id":   reqID,
			"request": map[string]any{
				"method":     "event",
				"event":      event,
				"handler_id": id,
				"args":       json.RawMessage(rawArgs),
			},
		})
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			h.t.Fatalf("timed out waiting for event %s", event)
		}
	}
}

func (h *fakeHost) getChildren() []*fakeChildState {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]*fakeChildState, 0, len(h.children))
	for _, c := range h.children {
		out = append(out, c)
	}
	return out
}

func (h *fakeHost) stop() {
	h.mu.Lock()
	if h.stopped {
		h.mu.Unlock()
		return
	}
	h.stopped = true
	h.mu.Unlock()

	h.write(map[string]any{
		"type":     "shutdown",
		"shutdown": map[string]any{"reason": "test done"},
	})
	_ = h.nc.Close()
	select {
	case <-h.runDone:
	case <-time.After(3 * time.Second):
	}
}

// Keep context imported for helper signatures.
var _ context.Context
