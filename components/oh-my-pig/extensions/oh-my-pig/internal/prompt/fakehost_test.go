package prompt

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
	Method string
	Args   map[string]any
}

type hostOptions struct {
	Mode          string // "tui" (default), "rpc", "json", "print"
	HasUI         *bool
	Cwd           string
	Model         string
	ModelProvider string
	OnCall        func(method string, args map[string]any) (result map[string]any, errMessage string)
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
	pending  map[string]chan json.RawMessage
	failures map[string]string
	nextID   int
}

type wireFrame struct {
	Type     string `json:"type"`
	ID       string `json:"id,omitempty"`
	Register *struct {
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
	hostSide, extSide := net.Pipe()
	h := &fakeHost{
		t:        t,
		nc:       hostSide,
		opts:     opts,
		runDone:  make(chan error, 1),
		handlers: map[string][]int{},
		pending:  map[string]chan json.RawMessage{},
		failures: map[string]string{},
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
	stateObj := map[string]any{"hasUI": hasUI}
	if opts.Model != "" {
		stateObj["model"] = map[string]any{"id": opts.Model, "provider": opts.ModelProvider}
	}
	state, _ := json.Marshal(stateObj)
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

func (h *fakeHost) fire(event string, data map[string]any) json.RawMessage {
	h.t.Helper()
	ids := h.handlers[event]
	if len(ids) == 0 {
		h.t.Fatalf("no handler registered for event %q", event)
	}
	args, err := json.Marshal(data)
	if err != nil {
		h.t.Fatalf("marshal event args: %v", err)
	}
	res, errMsg := h.roundTrip(map[string]any{
		"method":     "event",
		"event":      event,
		"handler_id": ids[len(ids)-1],
		"args":       json.RawMessage(args),
	})
	if errMsg != "" {
		h.t.Fatalf("event %q failed: %s", event, errMsg)
	}
	return res
}

func (h *fakeHost) fireBeforeAgentStart(data map[string]any) (string, bool) {
	h.t.Helper()
	raw := h.fire(sdk.EventBeforeAgentStart, data)
	var envelope struct {
		Result *struct {
			SystemPrompt *string `json:"systemPrompt"`
		} `json:"_pigPromptResult"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		h.t.Fatalf("decode before_agent_start reply %s: %v", raw, err)
	}
	if envelope.Result == nil || envelope.Result.SystemPrompt == nil {
		return "", false
	}
	return *envelope.Result.SystemPrompt, true
}

func (h *fakeHost) fireContext(messages []any) ([]any, bool) {
	h.t.Helper()
	raw := h.fire(sdk.EventContext, map[string]any{
		"type":     "context",
		"messages": messages,
	})
	var reply struct {
		Messages  []any `json:"messages"`
		Unchanged bool  `json:"_pigContextUnchanged"`
	}
	if err := json.Unmarshal(raw, &reply); err != nil {
		h.t.Fatalf("decode context reply %s: %v", raw, err)
	}
	if reply.Unchanged {
		return messages, false
	}
	return reply.Messages, true
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
		h.t.Fatalf("timed out waiting for response to %s", id)
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
			h.answer(f)
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

func (h *fakeHost) answer(f wireFrame) {
	if f.Call == nil {
		return
	}
	var args map[string]any
	if len(f.Call.Args) > 0 {
		_ = json.Unmarshal(f.Call.Args, &args)
	}
	h.mu.Lock()
	h.calls = append(h.calls, hostCall{Method: f.Call.Method, Args: args})
	h.mu.Unlock()

	var result map[string]any
	var errMsg string
	if h.opts.OnCall != nil {
		result, errMsg = h.opts.OnCall(f.Call.Method, args)
	}
	if errMsg != "" {
		h.write(map[string]any{
			"type":        "call_result",
			"id":          f.ID,
			"call_result": map[string]any{"error": map[string]any{"message": errMsg}},
		})
		return
	}
	if result == nil {
		result = map[string]any{}
	}
	encoded, _ := json.Marshal(result)
	h.write(map[string]any{
		"type":        "call_result",
		"id":          f.ID,
		"call_result": map[string]any{"result": json.RawMessage(encoded)},
	})
}

func (h *fakeHost) read() wireFrame {
	f, ok := h.readMaybe()
	if !ok {
		h.t.Fatalf("unexpected EOF reading frame from extension")
	}
	return f
}

func (h *fakeHost) readMaybe() (wireFrame, bool) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(h.nc, lenBuf[:]); err != nil {
		return wireFrame{}, false
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	payload := make([]byte, n)
	if _, err := io.ReadFull(h.nc, payload); err != nil {
		return wireFrame{}, false
	}
	var f wireFrame
	if err := json.Unmarshal(payload, &f); err != nil {
		h.t.Fatalf("unmarshal frame: %v", err)
	}
	return f, true
}

func (h *fakeHost) write(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		h.t.Fatalf("marshal frame: %v", err)
	}
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(data)))
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	_, _ = h.nc.Write(lenBuf[:])
	_, _ = h.nc.Write(data)
}

func (h *fakeHost) stop() {
	h.write(map[string]any{"type": "shutdown"})
	_ = h.nc.Close()
	select {
	case <-h.runDone:
	case <-time.After(2 * time.Second):
	}
}
