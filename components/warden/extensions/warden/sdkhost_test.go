package warden

// Layer-1 test harness for a Go extension port: a fake PiG host that speaks the
// documented extension wire protocol (`pig docs show extension-api`) over an
// in-memory pipe and drives the real SDK through the public
// `Extension.RunWithConn`. The tests therefore cross the same boundary a real
// host does and use no PiG-internal package.
//
// DERIVED from the Skill's fakehost_test.go template, deliberately not named fakehost_test.go: the repository's
// template check requires an unchanged copy, and this one is fixed and extended (see port/PORT.md and the lane's
// friction notes): Host.Command sends `tool` and a string argument (the template's {name, args} gives
// "unknown command:"), Host.Notify sends a notification, and the session mirror and model stream are served.
//
// The host answers every host call through Host.OnCall. The default answers
// {} to anything, which is enough for notifications; return a result map (or
// set the error message) for calls the port depends on, such as "exec",
// "ui.select", "isIdle", "getSessionFile".

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

// HostCall is one call the extension made to the host.
type HostCall struct {
	Method string
	Args   map[string]any
}

// HostOptions configures StartHost.
type HostOptions struct {
	Mode  string // "tui" (default), "rpc", "json" or "print"
	HasUI *bool  // default: true for tui and rpc, false otherwise
	Cwd   string
	// OnCall answers a host call. Return (result, "") for success or
	// (nil, message) for a host error. Nil means "answer {} to everything".
	OnCall func(method string, args map[string]any) (result map[string]any, errMessage string)
}

// Host is the fake PiG host.
type Host struct {
	t        *testing.T
	nc       net.Conn
	opts     HostOptions
	runDone  chan error
	writeMu  sync.Mutex
	mu       sync.Mutex
	calls    []HostCall
	pending  map[string]chan json.RawMessage
	failures map[string]string
	handlers map[string]int
	tools    map[string]bool
	cmds     map[string]bool
	nextID   int
}

type frame struct {
	Type     string `json:"type"`
	ID       string `json:"id,omitempty"`
	Register *struct {
		Handlers []struct {
			Event     string `json:"event"`
			HandlerID int    `json:"handler_id"`
		} `json:"handlers"`
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
		Commands []struct {
			Name string `json:"name"`
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

// StartHost runs ext against a fake host and returns once the extension has registered.
func StartHost(t *testing.T, ext *sdk.Extension, opts HostOptions) *Host {
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
	h := &Host{
		t: t, nc: hostSide, opts: opts, runDone: make(chan error, 1),
		pending: map[string]chan json.RawMessage{}, failures: map[string]string{},
		handlers: map[string]int{}, tools: map[string]bool{}, cmds: map[string]bool{},
	}
	go func() { h.runDone <- ext.RunWithConn(extSide) }()
	first := h.read()
	if first.Type != "register" || first.Register == nil {
		t.Fatalf("expected register, got %+v", first)
	}
	for _, hd := range first.Register.Handlers {
		h.handlers[hd.Event] = hd.HandlerID
	}
	for _, tl := range first.Register.Tools {
		h.tools[tl.Name] = true
	}
	for _, c := range first.Register.Commands {
		h.cmds[c.Name] = true
	}
	state, _ := json.Marshal(map[string]any{"hasUI": hasUI})
	h.write(map[string]any{"type": "ready", "ready": map[string]any{
		"session_name": "", "cwd": opts.Cwd, "mode": opts.Mode, "width": 80, "state": json.RawMessage(state),
	}})
	go h.serve()
	t.Cleanup(h.stop)
	return h
}

// Registered reports whether the extension registered a handler for event.
func (h *Host) Registered(event string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.handlers[event]
	return ok
}

// Calls returns every host call so far, in arrival order.
func (h *Host) Calls() []HostCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]HostCall(nil), h.calls...)
}

// CallsTo returns the calls of one method.
func (h *Host) CallsTo(method string) []HostCall {
	var out []HostCall
	for _, c := range h.Calls() {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

func (h *Host) read() frame {
	var hdr [4]byte
	if _, err := io.ReadFull(h.nc, hdr[:]); err != nil {
		h.t.Fatalf("read header: %v", err)
	}
	data := make([]byte, binary.BigEndian.Uint32(hdr[:]))
	if _, err := io.ReadFull(h.nc, data); err != nil {
		h.t.Fatalf("read frame: %v", err)
	}
	var f frame
	if err := json.Unmarshal(data, &f); err != nil {
		h.t.Fatalf("decode frame %s: %v", data, err)
	}
	return f
}

func (h *Host) write(v any) {
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

// serve answers host calls and routes responses to Fire and Request.
func (h *Host) serve() {
	for {
		var hdr [4]byte
		if _, err := io.ReadFull(h.nc, hdr[:]); err != nil {
			return
		}
		data := make([]byte, binary.BigEndian.Uint32(hdr[:]))
		if _, err := io.ReadFull(h.nc, data); err != nil {
			return
		}
		var f frame
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

func (h *Host) answer(f frame) {
	args := map[string]any{}
	_ = json.Unmarshal(f.Call.Args, &args)
	// Record before answering so a test that reads Calls after the handler
	// returned always sees the call.
	h.mu.Lock()
	h.calls = append(h.calls, HostCall{Method: f.Call.Method, Args: args})
	h.mu.Unlock()
	// Answer off the read loop so a slow OnCall cannot block response routing.
	go func() {
		result, errMessage := map[string]any{}, ""
		if h.opts.OnCall != nil {
			result, errMessage = h.opts.OnCall(f.Call.Method, args)
		}
		if errMessage != "" {
			h.write(map[string]any{"type": "call_result", "id": f.ID, "call_result": map[string]any{"error": map[string]any{"message": errMessage}}})
			return
		}
		if result == nil {
			result = map[string]any{}
		}
		h.write(map[string]any{"type": "call_result", "id": f.ID, "call_result": map[string]any{"result": result}})
	}()
}

// Fire delivers one event the way PiG's host does (waiting for the handler)
// and returns the handler's result. An event the extension did not register
// for is not delivered and returns nil. A handler error fails the test.
func (h *Host) Fire(event string, data map[string]any) json.RawMessage {
	h.t.Helper()
	h.mu.Lock()
	id, ok := h.handlers[event]
	h.mu.Unlock()
	if !ok {
		return nil
	}
	if data == nil {
		data = map[string]any{}
	}
	data["type"] = event
	args, _ := json.Marshal(data)
	result, failure := h.roundTrip(map[string]any{"method": "event", "event": event, "handler_id": id, "args": json.RawMessage(args)})
	if failure != "" {
		h.t.Errorf("%s handler failed: %s", event, failure)
	}
	return result
}

// Command runs a registered slash command and returns its error text ("" on success).
func (h *Host) Command(name, args string) string {
	h.t.Helper()
	if !h.cmds[name] {
		h.t.Fatalf("command %q is not registered", name)
	}
	// PiG's host names the command in `tool` and sends the argument text as a JSON string; the Skill template
	// sent {name, args}, which the SDK answers with "unknown command: " (see pigpen-warden-FRICTION.md).
	argv, _ := json.Marshal(args)
	_, failure := h.roundTrip(map[string]any{"method": "command", "tool": name, "args": json.RawMessage(argv)})
	return failure
}

// Tool executes a registered tool and returns its raw result and error text.
func (h *Host) Tool(name string, params map[string]any) (json.RawMessage, string) {
	h.t.Helper()
	if !h.tools[name] {
		h.t.Fatalf("tool %q is not registered", name)
	}
	argv, _ := json.Marshal(params)
	return h.roundTrip(map[string]any{"method": "tool_call", "tool": name, "tool_call_id": "call-1", "args": json.RawMessage(argv)})
}

func (h *Host) roundTrip(request map[string]any) (json.RawMessage, string) {
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

func (h *Host) stop() {
	h.write(map[string]any{"type": "shutdown", "shutdown": map[string]any{"reason": "test"}})
	select {
	case <-h.runDone:
	case <-time.After(10 * time.Second):
		h.t.Errorf("extension did not stop after shutdown")
	}
	_ = h.nc.Close()
}

// Notify sends the extension a host notification (state_update, model_stream_event ...), the way PiG's host
// pushes session growth and model stream events. The Skill template has no such call.
func (h *Host) Notify(method string, args any) {
	raw, _ := json.Marshal(args)
	h.write(map[string]any{"type": "notify", "notify": map[string]any{"method": method, "args": json.RawMessage(raw)}})
}
