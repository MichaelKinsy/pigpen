package tern

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
	Mode        string
	HasUI       *bool
	Cwd         string
	SessionID   string
	SessionName string
	SessionFile string
	OnCall      func(method string, args map[string]any) (result any, errMessage string)
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
	commands map[string]bool
	handlers map[string][]int
	nextID   int
}

type wireFrame struct {
	Type     string `json:"type"`
	ID       string `json:"id,omitempty"`
	Register *struct {
		Commands []struct {
			Name string `json:"name"`
		} `json:"commands"`
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
		commands: map[string]bool{},
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
	for _, cmd := range first.Register.Commands {
		h.commands[cmd.Name] = true
	}
	for _, hd := range first.Register.Handlers {
		h.handlers[hd.Event] = append(h.handlers[hd.Event], hd.HandlerID)
	}
	stateObj := map[string]any{"hasUI": hasUI}
	if opts.SessionID != "" || opts.SessionName != "" || opts.SessionFile != "" {
		stateObj["session"] = map[string]any{
			"sessionId":   opts.SessionID,
			"sessionName": opts.SessionName,
			"sessionFile": opts.SessionFile,
		}
	}
	state, _ := json.Marshal(stateObj)
	h.write(map[string]any{"type": "ready", "ready": map[string]any{
		"cwd":   opts.Cwd,
		"mode":  opts.Mode,
		"width": 80,
		"state": json.RawMessage(state),
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

func (h *fakeHost) resetCalls() {
	h.mu.Lock()
	h.calls = nil
	h.mu.Unlock()
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
	data["type"] = eventName
	raw, _ := json.Marshal(data)
	var lastRes json.RawMessage
	var lastErr string
	for _, hid := range ids {
		lastRes, lastErr = h.roundTrip(map[string]any{
			"method":     "event",
			"event":      eventName,
			"handler_id": hid,
			"args":       json.RawMessage(raw),
		})
	}
	return lastRes, lastErr
}

// runWithSession simulates the PiG host invoking a replacement session's
// `withSession` callback before answering the `fork` call.
func (h *fakeHost) runWithSession(handle string) (json.RawMessage, string) {
	h.t.Helper()
	args, _ := json.Marshal(map[string]any{
		"handle": handle,
		"ready": map[string]any{
			"cwd":  h.opts.Cwd,
			"mode": h.opts.Mode,
			"state": map[string]any{
				"hasUI": true,
			},
		},
	})
	return h.roundTrip(map[string]any{
		"method": "with_session",
		"args":   json.RawMessage(args),
	})
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

func mapEnv(m map[string]string) EnvLookup {
	return func(key string) (string, bool) {
		v, ok := m[key]
		return v, ok
	}
}
