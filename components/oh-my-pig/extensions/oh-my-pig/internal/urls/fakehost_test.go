package urls

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

type fakeHostCall struct {
	Method string
	Args   map[string]any
}

type fakeHostOpts struct {
	SessionID   string
	SessionFile string
	Cwd         string
	ActiveTools []string
	Skills      []sdk.SystemPromptSkill
	OnCall      func(method string, args map[string]any) (any, string)
}

type fakeHost struct {
	t        *testing.T
	nc       net.Conn
	opts     fakeHostOpts
	runDone  chan error
	writeMu  sync.Mutex
	mu       sync.Mutex
	calls    []fakeHostCall
	handlers map[string][]int
	pending  map[string]chan json.RawMessage
	nextID   int
}

type wireMsg struct {
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

func startFakeHost(t *testing.T, ext *sdk.Extension, opts fakeHostOpts) *fakeHost {
	t.Helper()
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
	}
	go func() { h.runDone <- ext.RunWithConn(extSide) }()
	first := h.read()
	for first.Type == "call" && first.Call != nil {
		h.answer(first)
		first = h.read()
	}
	if first.Type != "register" || first.Register == nil {
		t.Fatalf("expected register frame, got %+v", first)
	}
	for _, hnd := range first.Register.Handlers {
		h.handlers[hnd.Event] = append(h.handlers[hnd.Event], hnd.HandlerID)
	}
	stateObj := map[string]any{
		"hasUI": true,
		"session": map[string]any{
			"sessionId":   opts.SessionID,
			"sessionFile": opts.SessionFile,
		},
		"activeTools": opts.ActiveTools,
	}
	state, _ := json.Marshal(stateObj)
	h.write(map[string]any{
		"type": "ready",
		"ready": map[string]any{
			"session_name": "",
			"cwd":          opts.Cwd,
			"mode":         "tui",
			"width":        80,
			"state":        json.RawMessage(state),
		},
	})
	go h.serve()
	t.Cleanup(h.stop)
	return h
}

func (h *fakeHost) fireEvent(event string, data map[string]any) {
	h.t.Helper()
	ids := h.handlers[event]
	if len(ids) == 0 {
		h.t.Fatalf("no handler registered for event %q", event)
	}
	args, err := json.Marshal(data)
	if err != nil {
		h.t.Fatal(err)
	}
	for _, hid := range ids {
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
				"method":     "event",
				"handler_id": hid,
				"event":      event,
				"args":       json.RawMessage(args),
			},
		})
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			h.t.Fatalf("timed out waiting for event %q", event)
		}
	}
}

func (h *fakeHost) recordedCalls() []fakeHostCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]fakeHostCall(nil), h.calls...)
}

func (h *fakeHost) stop() {
	_ = h.nc.Close()
	select {
	case <-h.runDone:
	case <-time.After(2 * time.Second):
	}
}

func (h *fakeHost) serve() {
	for {
		msg, err := h.tryRead()
		if err != nil {
			return
		}
		switch msg.Type {
		case "call":
			if msg.Call != nil {
				h.answer(msg)
			}
		case "response":
			h.mu.Lock()
			ch := h.pending[msg.ID]
			delete(h.pending, msg.ID)
			h.mu.Unlock()
			if ch != nil {
				var res json.RawMessage
				if msg.Response != nil {
					res = msg.Response.Result
				}
				ch <- res
			}
		}
	}
}

func (h *fakeHost) answer(msg wireMsg) {
	var args map[string]any
	if len(msg.Call.Args) > 0 {
		_ = json.Unmarshal(msg.Call.Args, &args)
	}
	h.mu.Lock()
	h.calls = append(h.calls, fakeHostCall{Method: msg.Call.Method, Args: args})
	h.mu.Unlock()

	var result any
	var errMsg string
	if h.opts.OnCall != nil {
		result, errMsg = h.opts.OnCall(msg.Call.Method, args)
	}
	if result == nil && errMsg == "" {
		switch msg.Call.Method {
		case "getSessionID":
			result = map[string]any{"sessionId": h.opts.SessionID}
		case "getSessionFile":
			result = map[string]any{"sessionFile": h.opts.SessionFile}
		case "getActiveTools":
			result = map[string]any{"tools": h.opts.ActiveTools}
		case "getSystemPromptOptions":
			result = sdk.SystemPromptOptions{
				Cwd:    h.opts.Cwd,
				Skills: h.opts.Skills,
			}
		default:
			result = map[string]any{}
		}
	}
	resp := map[string]any{"type": "call_result", "id": msg.ID}
	if errMsg != "" {
		resp["call_result"] = map[string]any{"error": map[string]any{"message": errMsg}}
	} else {
		raw, _ := json.Marshal(result)
		resp["call_result"] = map[string]any{"result": json.RawMessage(raw)}
	}
	h.write(resp)
}

func (h *fakeHost) read() wireMsg {
	msg, err := h.tryRead()
	if err != nil {
		h.t.Fatalf("read frame: %v", err)
	}
	return msg
}

func (h *fakeHost) tryRead() (wireMsg, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(h.nc, hdr[:]); err != nil {
		return wireMsg{}, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	buf := make([]byte, n)
	if _, err := io.ReadFull(h.nc, buf); err != nil {
		return wireMsg{}, err
	}
	var msg wireMsg
	if err := json.Unmarshal(buf, &msg); err != nil {
		return wireMsg{}, err
	}
	return msg, nil
}

func (h *fakeHost) write(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		h.t.Fatal(err)
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(data)))
	h.writeMu.Lock()
	_, werr := h.nc.Write(append(hdr[:], data...))
	h.writeMu.Unlock()
	if werr != nil && !errors.Is(werr, net.ErrClosed) && !errors.Is(werr, io.ErrClosedPipe) {
		h.t.Fatalf("write frame: %v", werr)
	}
}
