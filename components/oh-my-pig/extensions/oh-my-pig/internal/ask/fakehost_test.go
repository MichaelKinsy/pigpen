package ask

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

type registeredTool struct {
	Name          string         `json:"name"`
	Label         string         `json:"label"`
	Description   string         `json:"description"`
	PromptSnippet string         `json:"prompt_snippet"`
	ExecutionMode string         `json:"execution_mode"`
	Parameters    map[string]any `json:"parameters"`
}

type hostOptions struct {
	Mode   string // "tui" (default), "rpc", "json", "print"
	HasUI  *bool  // default: true for tui and rpc, false otherwise
	Cwd    string
	OnCall func(method string, args map[string]any) (result map[string]any, errMessage string)
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
	handlers map[string][]int
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
	state, _ := json.Marshal(map[string]any{"hasUI": hasUI})
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

func (h *fakeHost) callTool(name string, params map[string]any) (json.RawMessage, string) {
	h.t.Helper()
	if _, ok := h.tools[name]; !ok {
		h.t.Fatalf("tool %q is not registered", name)
	}
	argv, _ := json.Marshal(params)
	return h.roundTrip(map[string]any{
		"method":       "tool_call",
		"tool":         name,
		"tool_call_id": "call-1",
		"args":         json.RawMessage(argv),
	})
}

func (h *fakeHost) prepareArgs(name string, params map[string]any) (map[string]any, string) {
	h.t.Helper()
	argv, _ := json.Marshal(params)
	raw, errMsg := h.roundTrip(map[string]any{
		"method": "tool_prepare_arguments",
		"tool":   name,
		"args":   json.RawMessage(argv),
	})
	if errMsg != "" {
		return nil, errMsg
	}
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out, ""
}

func (h *fakeHost) emitEvent(event string, payload map[string]any) {
	h.t.Helper()
	ids := h.handlers[event]
	if payload == nil {
		payload = map[string]any{}
	}
	for _, id := range ids {
		args, _ := json.Marshal(payload)
		_, _ = h.roundTrip(map[string]any{
			"method":     "event",
			"event":      event,
			"handler_id": id,
			"args":       json.RawMessage(args),
		})
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
		var result map[string]any
		errMessage := ""
		if h.opts.OnCall != nil {
			result, errMessage = h.opts.OnCall(f.Call.Method, args)
		}
		if errMessage != "" {
			h.write(map[string]any{
				"type": "call_result",
				"id":   f.ID,
				"call_result": map[string]any{
					"error": map[string]any{"message": errMessage},
				},
			})
			return
		}
		if result == nil {
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
	h.write(map[string]any{"type": "shutdown", "shutdown": map[string]any{"reason": "test"}})
	select {
	case <-h.runDone:
	case <-time.After(10 * time.Second):
		h.t.Errorf("extension did not stop after shutdown")
	}
	_ = h.nc.Close()
}
