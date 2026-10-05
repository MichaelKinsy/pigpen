package herdr_test

// Test harness: a fake herdr binary and a fake PiG host.
//
// The fake host speaks PiG's documented extension wire protocol (`pig docs show
// extension-api`) over an in-memory pipe and drives the real SDK through the
// public `Extension.RunWithConn`. So the tests cross the same boundary a real
// host does, and use no PiG-internal package.

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// ── Fake herdr ───────────────────────────────────────────────────────────────
//
// The test binary doubles as the fake `$HERDR_BIN_PATH`: when HERDR_FAKE_LOG is
// set, it records one line of tab-separated arguments and exits. That works on
// every platform, with no shell script.

func TestMain(m *testing.M) {
	if log := os.Getenv("HERDR_FAKE_LOG"); log != "" {
		os.Exit(runFakeHerdr(log))
	}
	os.Exit(m.Run())
}

func runFakeHerdr(log string) int {
	args := os.Args[1:]
	// Only reports are slow, so a release that does not wait for them lands first.
	if ms, _ := strconv.Atoi(os.Getenv("HERDR_FAKE_DELAY_MS")); ms > 0 && len(args) > 1 && args[1] == "report-agent" {
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
	// A hung report never returns and never logs; only the call timeout ends it.
	if os.Getenv("HERDR_FAKE_HANG") == "1" && len(args) > 1 && args[1] == "report-agent" {
		time.Sleep(time.Hour)
	}
	f, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		_, _ = f.WriteString(strings.Join(args, "\t") + "\t\n")
		_ = f.Close()
	}
	// A resume command is refused the way real herdr refuses it. The refused call
	// is logged above, so tests see the attempt and the retry.
	if slices.Contains(args, "--") {
		switch os.Getenv("HERDR_FAKE_RESUME") {
		case "old": // herdr < 0.9.2: the CLI does not know the `--` separator
			fmt.Fprintln(os.Stderr, "unknown option: --")
			return 2
		case "invalid": // herdr >= 0.9.2 rejecting the command; the report is not applied
			fmt.Println(`{"error":{"code":"invalid_resume_argv","message":"resume_argv must not contain apostrophes"},"id":"cli:request"}`)
			return 1
		case "message-option": // an old CLI naming another option it does not know
			fmt.Fprintln(os.Stderr, "unknown option: --message=Apply the plan?")
			return 2
		case "broken": // a failure that says nothing about resume support
			fmt.Fprintln(os.Stderr, "error: failed to connect to herdr socket")
			return 1
		}
	}
	code, _ := strconv.Atoi(os.Getenv("HERDR_FAKE_EXIT"))
	if msg := os.Getenv("HERDR_FAKE_STDERR"); msg != "" && code != 0 {
		fmt.Fprintln(os.Stderr, msg)
	}
	return code
}

type call struct{ args []string }

type fakeHerdr struct {
	t   *testing.T
	log string
}

// installFakeHerdr points the process environment at a fresh fake herdr pane.
func installFakeHerdr(t *testing.T) *fakeHerdr {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	h := &fakeHerdr{t: t, log: filepath.Join(t.TempDir(), "calls.log")}
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_PANE_ID", "p_7")
	t.Setenv("HERDR_BIN_PATH", exe)
	t.Setenv("HERDR_SOCKET_PATH", filepath.Join(t.TempDir(), "herdr.sock"))
	t.Setenv("HERDR_FAKE_LOG", h.log)
	t.Setenv("HERDR_FAKE_DELAY_MS", "")
	t.Setenv("HERDR_FAKE_EXIT", "")
	t.Setenv("HERDR_FAKE_STDERR", "")
	t.Setenv("HERDR_FAKE_HANG", "")
	t.Setenv("HERDR_FAKE_RESUME", "")
	return h
}

func (h *fakeHerdr) slow(delay time.Duration) {
	h.t.Setenv("HERDR_FAKE_DELAY_MS", strconv.Itoa(int(delay/time.Millisecond)))
}

func (h *fakeHerdr) hang() { h.t.Setenv("HERDR_FAKE_HANG", "1") }

// refuseResume makes herdr refuse any report that carries a resume command: "old"
// (herdr before 0.9.2), "invalid" (invalid_resume_argv) or "broken" (an unrelated failure).
func (h *fakeHerdr) refuseResume(kind string) { h.t.Setenv("HERDR_FAKE_RESUME", kind) }

// failWithMessage makes every call exit with code after printing msg, as herdr
// prints why it refused a request.
func (h *fakeHerdr) failWithMessage(code int, msg string) {
	h.failWith(code)
	h.t.Setenv("HERDR_FAKE_STDERR", msg)
}

func (h *fakeHerdr) failWith(code int) { h.t.Setenv("HERDR_FAKE_EXIT", strconv.Itoa(code)) }

func (h *fakeHerdr) calls() []call {
	data, err := os.ReadFile(h.log)
	if err != nil {
		return nil
	}
	var out []call
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		out = append(out, call{args: strings.Split(strings.TrimSuffix(line, "\t"), "\t")})
	}
	return out
}

func (h *fakeHerdr) waitForCalls(count int) []call {
	h.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		now := h.calls()
		if len(now) >= count {
			return now
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("expected %d herdr calls, saw %d: %v", count, len(now), now)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// settle gives a call that must not happen time to show up in the log.
func settle() { time.Sleep(200 * time.Millisecond) }

// flag reads `--name value` or `--name=value`.
func flag(c call, name string) (string, bool) {
	for i, arg := range c.args {
		if arg == name && i+1 < len(c.args) {
			return c.args[i+1], true
		}
		if strings.HasPrefix(arg, name+"=") {
			return strings.TrimPrefix(arg, name+"="), true
		}
	}
	return "", false
}

// resume returns the command after `--`, and whether the call has one.
func (c call) resume() ([]string, bool) {
	i := slices.Index(c.args, "--")
	if i < 0 {
		return nil, false
	}
	return c.args[i+1:], true
}

func (c call) verb() string {
	if len(c.args) < 2 {
		return ""
	}
	return c.args[1]
}

func states(all []call) []string {
	out := make([]string, 0, len(all))
	for _, c := range all {
		s, _ := flag(c, "--state")
		out = append(out, s)
	}
	return out
}

func seqs(t *testing.T, all []call) []int64 {
	t.Helper()
	out := make([]int64, 0, len(all))
	for _, c := range all {
		s, _ := flag(c, "--seq")
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			t.Fatalf("seq %q is not an integer: %v", s, err)
		}
		out = append(out, n)
	}
	return out
}

func verbs(all []call) []string {
	out := make([]string, 0, len(all))
	for _, c := range all {
		out = append(out, c.verb())
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
	sessionFile    string
	sessionID      string
	idle           bool
	sessionFailure bool
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
}

// startHost runs the extension against a fake host in the given mode.
func startHost(t *testing.T, ext *sdk.Extension, mode string) *fakeHost {
	t.Helper()
	hostSide, extSide := net.Pipe()
	h := &fakeHost{
		t:        t,
		nc:       hostSide,
		runDone:  make(chan error, 1),
		pending:  map[string]chan *wireResponse{},
		handlers: map[string]int{},
		state: hostState{
			sessionFile: "/home/u/.pig/sessions/s1.jsonl",
			sessionID:   "s1",
			idle:        true,
		},
	}
	go func() { h.runDone <- ext.RunWithConn(extSide) }()

	first := h.readFrame()
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
	case "getSessionFile", "getSessionID":
		if st.sessionFailure {
			h.write(map[string]any{"type": "call_result", "id": env.ID, "call_result": map[string]any{"error": map[string]any{"message": "no session"}}})
			return
		}
		if env.Call.Method == "getSessionFile" {
			result["sessionFile"] = st.sessionFile
		} else {
			result["sessionId"] = st.sessionID
		}
	case "isIdle":
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
		return
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
			h.t.Errorf("%s handler failed: %s", event, resp.Error.Message)
		}
	case <-time.After(10 * time.Second):
		h.t.Fatalf("no response to %s within 10s", event)
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
