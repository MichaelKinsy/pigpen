package herdr_test

// Test harness: a fake herdr socket server and a fake PiG host.
//
// herdr-pig talks to herdr over its socket (newline-delimited JSON, one request
// per connection, answered by one line), so the fake herdr is a unix socket
// server that records every request line it receives, as the original's own
// test does. The fake host speaks PiG's documented extension wire protocol
// (`pig docs show extension-api`) over an in-memory pipe and drives the real SDK
// through the public `Extension.RunWithConn`. So the tests cross the same
// boundary a real host does, and use no PiG-internal package.

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// ── Fake herdr ───────────────────────────────────────────────────────────────

// herdrRequest is one request line a connection delivered.
type herdrRequest struct {
	Raw    string
	ID     string
	Method string
	Params map[string]any
}

// behavior is what the fake herdr does with one connection.
type behavior struct {
	// Silent reads the request and never answers: the client's attempt timeout ends it.
	Silent bool
	// Close reads the request and closes the connection without an answer (a half-close, "end" in Node).
	Close bool
	// Delay waits before answering.
	Delay time.Duration
}

type fakeHerdr struct {
	t    *testing.T
	ln   net.Listener
	path string

	mu    sync.Mutex
	reqs  []herdrRequest
	conns int
	// behave picks the behavior of the n-th connection (0-based). Nil answers at once.
	behave func(n int) behavior
}

// installFakeHerdr points the process environment at a fresh fake herdr pane
// (the original's own test uses the pane id w1:p1).
func installFakeHerdr(t *testing.T) *fakeHerdr {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake herdr listens on a unix socket; Windows uses a named pipe")
	}
	// A unix socket path is limited to about 104 bytes: keep it short.
	dir, err := os.MkdirTemp("", "hd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	h := &fakeHerdr{t: t, path: filepath.Join(dir, "s")}
	h.ln, err = net.Listen("unix", h.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.ln.Close() })
	go h.accept()
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_SOCKET_PATH", h.path)
	t.Setenv("HERDR_PANE_ID", "w1:p1")
	t.Setenv("PIGPEN_HERDR_UI_PROMPTS", "")
	return h
}

func (h *fakeHerdr) accept() {
	for {
		conn, err := h.ln.Accept()
		if err != nil {
			return
		}
		h.mu.Lock()
		n := h.conns
		h.conns++
		h.mu.Unlock()
		go h.serve(conn, n)
	}
}

func (h *fakeHerdr) serve(conn net.Conn, n int) {
	defer conn.Close()
	var b behavior
	h.mu.Lock()
	behave := h.behave
	h.mu.Unlock()
	if behave != nil {
		b = behave(n)
	}
	r := bufio.NewReader(conn)
	for {
		line, err := r.ReadString('\n')
		if line = strings.TrimSuffix(line, "\n"); line != "" {
			req := herdrRequest{Raw: line}
			var msg struct {
				ID     string         `json:"id"`
				Method string         `json:"method"`
				Params map[string]any `json:"params"`
			}
			if json.Unmarshal([]byte(line), &msg) != nil {
				h.t.Errorf("fake herdr: not JSON: %q", line)
			}
			req.ID, req.Method, req.Params = msg.ID, msg.Method, msg.Params
			h.mu.Lock()
			h.reqs = append(h.reqs, req)
			h.mu.Unlock()
			switch {
			case b.Close:
				return
			case b.Silent:
				continue
			}
			if b.Delay > 0 {
				time.Sleep(b.Delay)
			}
			_, _ = io.WriteString(conn, `{"result":{}}`+"\n")
		}
		if err != nil {
			return
		}
	}
}

// setBehave sets the behavior of the connections that follow.
func (h *fakeHerdr) setBehave(f func(n int) behavior) {
	h.mu.Lock()
	h.behave = f
	h.mu.Unlock()
}

func (h *fakeHerdr) requests() []herdrRequest {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]herdrRequest(nil), h.reqs...)
}

func (h *fakeHerdr) connections() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.conns
}

// waitFor waits until count requests arrived (the original's waitFor, with a longer bound for a loaded host) and
// fails when the number is not exactly count.
func (h *fakeHerdr) waitFor(count int) []herdrRequest {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for len(h.requests()) < count && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	got := h.requests()
	if len(got) != count {
		h.t.Fatalf("expected %d herdr requests, saw %d: %v", count, len(got), rawLines(got))
	}
	return got
}

func rawLines(all []herdrRequest) []string {
	out := make([]string, 0, len(all))
	for _, r := range all {
		out = append(out, r.Raw)
	}
	return out
}

// settle gives a request that must not happen time to show up.
func settle() { time.Sleep(200 * time.Millisecond) }

func states(all []herdrRequest) []string {
	out := make([]string, 0, len(all))
	for _, r := range all {
		s, _ := r.Params["state"].(string)
		if r.Method == "pane.release_agent" {
			s = "release"
		}
		out = append(out, s)
	}
	return out
}

func seqs(t *testing.T, all []herdrRequest) []float64 {
	t.Helper()
	out := make([]float64, 0, len(all))
	for _, r := range all {
		n, ok := r.Params["seq"].(float64)
		if !ok {
			t.Fatalf("seq is not a number in %s", r.Raw)
		}
		out = append(out, n)
	}
	return out
}

// ── Fake PiG host ────────────────────────────────────────────────────────────

type wireEnvelope struct {
	Type       string          `json:"type"`
	ID         string          `json:"id,omitempty"`
	Register   *wireRegister   `json:"register,omitempty"`
	Ready      map[string]any  `json:"ready,omitempty"`
	Request    map[string]any  `json:"request,omitempty"`
	Response   *wireResponse   `json:"response,omitempty"`
	Call       *wireCall       `json:"call,omitempty"`
	CallResult map[string]any  `json:"call_result,omitempty"`
	Shutdown   map[string]any  `json:"shutdown,omitempty"`
	Raw        json.RawMessage `json:"-"`
}

type wireRegister struct {
	Name     string `json:"name"`
	Handlers []struct {
		Event     string `json:"event"`
		HandlerID int    `json:"handler_id"`
	} `json:"handlers"`
	Tools    []json.RawMessage `json:"tools"`
	Commands []json.RawMessage `json:"commands"`
}

type wireResponse struct {
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type wireCall struct {
	Method string          `json:"method"`
	Args   json.RawMessage `json:"args"`
}

// hostState is what the fake host answers for the session getters.
type hostState struct {
	sessionFile string
	// noFile answers getSessionFile with no file (an in-memory session: Pi's undefined).
	noFile      bool
	fileFailure bool
	sessionID   string
	noID        bool
	idFailure   bool
	idle        bool
	idleFailure bool
}

type fakeHost struct {
	t        *testing.T
	nc       net.Conn
	register wireRegister
	runDone  chan error

	writeMu sync.Mutex

	mu       sync.Mutex
	state    hostState
	pending  map[string]chan *wireResponse
	nextID   int
	handlers map[string]int
	// listeners maps a pi.events channel to the handler id the extension subscribed.
	listeners map[string]string
}

// startHost runs the extension against a fake host in the given mode.
func startHost(t *testing.T, ext *sdk.Extension, mode string) *fakeHost {
	t.Helper()
	hostSide, extSide := net.Pipe()
	h := &fakeHost{
		t:         t,
		nc:        hostSide,
		runDone:   make(chan error, 1),
		pending:   map[string]chan *wireResponse{},
		handlers:  map[string]int{},
		listeners: map[string]string{},
		state: hostState{
			// The original's own test ctx().
			sessionFile: "/home/user/.pig/agent/sessions/one.jsonl",
			sessionID:   "session-one",
			idle:        true,
		},
	}
	go func() { h.runDone <- ext.RunWithConn(extSide) }()

	// pi.events listeners subscribed in the factory register before the extension does.
	first := h.readFrame()
	for first.Type == "call" && first.Call != nil && first.Call.Method == "events.on" {
		var args struct {
			Channel   string `json:"channel"`
			HandlerID string `json:"handlerId"`
		}
		_ = json.Unmarshal(first.Call.Args, &args)
		h.listeners[args.Channel] = args.HandlerID
		h.write(map[string]any{"type": "call_result", "id": first.ID, "call_result": map[string]any{"result": map[string]any{}}})
		first = h.readFrame()
	}
	if first.Type != "register" || first.Register == nil {
		t.Fatalf("expected register, got %+v", first)
	}
	h.register = *first.Register
	for _, handler := range h.register.Handlers {
		h.handlers[handler.Event] = handler.HandlerID
	}
	h.write(map[string]any{"type": "ready", "ready": map[string]any{"session_name": "", "cwd": t.TempDir(), "mode": mode, "width": 80}})
	go h.serve()
	t.Cleanup(h.stop)
	return h
}

func (h *fakeHost) readFrame() wireEnvelope {
	var hdr [4]byte
	if _, err := io.ReadFull(h.nc, hdr[:]); err != nil {
		h.t.Fatalf("read header: %v", err)
	}
	data := make([]byte, binary.BigEndian.Uint32(hdr[:]))
	if _, err := io.ReadFull(h.nc, data); err != nil {
		h.t.Fatalf("read frame: %v", err)
	}
	var env wireEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		h.t.Fatalf("decode frame %s: %v", data, err)
	}
	return env
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

// serve answers the extension's host calls and routes event responses.
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
		var env wireEnvelope
		if json.Unmarshal(data, &env) != nil {
			continue
		}
		switch env.Type {
		case "call":
			go h.answer(env)
		case "response":
			h.mu.Lock()
			ch := h.pending[env.ID]
			delete(h.pending, env.ID)
			h.mu.Unlock()
			if ch != nil {
				ch <- env.Response
			}
		}
	}
}

func (h *fakeHost) answer(env wireEnvelope) {
	h.mu.Lock()
	st := h.state
	h.mu.Unlock()
	result := map[string]any{}
	switch env.Call.Method {
	case "getSessionFile":
		if st.fileFailure {
			h.write(map[string]any{"type": "call_result", "id": env.ID, "call_result": map[string]any{"error": map[string]any{"message": "no session"}}})
			return
		}
		if !st.noFile {
			result["sessionFile"] = st.sessionFile
		}
	case "getSessionID":
		if st.idFailure {
			h.write(map[string]any{"type": "call_result", "id": env.ID, "call_result": map[string]any{"error": map[string]any{"message": "no session"}}})
			return
		}
		if !st.noID {
			result["sessionId"] = st.sessionID
		}
	case "isIdle":
		if st.idleFailure {
			h.write(map[string]any{"type": "call_result", "id": env.ID, "call_result": map[string]any{"error": map[string]any{"message": "no idle state"}}})
			return
		}
		result["idle"] = st.idle
	}
	h.write(map[string]any{"type": "call_result", "id": env.ID, "call_result": map[string]any{"result": result}})
}

func (h *fakeHost) setState(update func(*hostState)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	update(&h.state)
}

// fire delivers one event and waits for the extension's response, as PiG's host does.
// An event the extension did not register for is not delivered.
func (h *fakeHost) fire(event string, data map[string]any) {
	h.t.Helper()
	if msg, failed := h.fireResult(event, data); failed {
		h.t.Errorf("%s handler failed: %s", event, msg)
	}
}

// fireResult delivers one event like fire and returns the handler's error message instead of failing.
func (h *fakeHost) fireResult(event string, data map[string]any) (string, bool) {
	h.t.Helper()
	h.mu.Lock()
	id, ok := h.handlers[event]
	h.nextID++
	reqID := fmt.Sprintf("r%d", h.nextID)
	ch := make(chan *wireResponse, 1)
	if ok {
		h.pending[reqID] = ch
	}
	h.mu.Unlock()
	if !ok {
		return "", false
	}
	if data == nil {
		data = map[string]any{}
	}
	data["type"] = event
	args, _ := json.Marshal(data)
	h.write(map[string]any{"type": "request", "id": reqID, "request": map[string]any{"method": "event", "event": event, "handler_id": id, "args": json.RawMessage(args)}})
	select {
	case resp := <-ch:
		if resp != nil && resp.Error != nil {
			return resp.Error.Message, true
		}
	case <-time.After(10 * time.Second):
		h.t.Fatalf("no response to %s within 10s", event)
	}
	return "", false
}

// emit dispatches one pi.events payload to the extension's listener on channel
// and waits for it to finish, as PiG's host does. It fails when nothing listens.
func (h *fakeHost) emit(channel string, data any) {
	h.t.Helper()
	h.mu.Lock()
	handlerID, ok := h.listeners[channel]
	h.nextID++
	reqID := fmt.Sprintf("r%d", h.nextID)
	ch := make(chan *wireResponse, 1)
	h.pending[reqID] = ch
	h.mu.Unlock()
	if !ok {
		h.t.Fatalf("no pi.events listener on %q", channel)
	}
	payload, _ := json.Marshal(data)
	args, _ := json.Marshal(map[string]any{"handlerId": handlerID, "channel": channel, "json": json.RawMessage(payload)})
	h.write(map[string]any{"type": "request", "id": reqID, "request": map[string]any{"method": "events.dispatch", "args": json.RawMessage(args)}})
	select {
	case resp := <-ch:
		if resp != nil && resp.Error != nil {
			h.t.Errorf("%s listener failed: %s", channel, resp.Error.Message)
		}
	case <-time.After(10 * time.Second):
		h.t.Fatalf("no response to %s within 10s", channel)
	}
}

func (h *fakeHost) registered(event string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.handlers[event]
	return ok
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
