package browser

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
)

type testFakeHost struct {
	t         *testing.T
	nc        net.Conn
	sessionID string
	cwd       string
	runDone   chan error
	writeMu   sync.Mutex
	mu        sync.Mutex
	handlers  map[string][]int
	pending   map[string]chan json.RawMessage
	failures  map[string]string
	nextID    int
}

type testWireFrame struct {
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

func startTestFakeHost(t *testing.T, ext *sdk.Extension, sessionID string) *testFakeHost {
	t.Helper()
	hostSide, extSide := net.Pipe()
	h := &testFakeHost{
		t:         t,
		nc:        hostSide,
		sessionID: sessionID,
		cwd:       t.TempDir(),
		runDone:   make(chan error, 1),
		handlers:  map[string][]int{},
		pending:   map[string]chan json.RawMessage{},
		failures:  map[string]string{},
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
	state, _ := json.Marshal(map[string]any{"hasUI": false})
	h.write(map[string]any{"type": "ready", "ready": map[string]any{
		"cwd":   h.cwd,
		"mode":  "print",
		"width": 80,
		"state": json.RawMessage(state),
	}})
	go h.serve()
	t.Cleanup(h.stop)
	return h
}

func (h *testFakeHost) fire(event string, data map[string]any) {
	h.t.Helper()
	ids := h.handlers[event]
	if len(ids) == 0 {
		h.t.Fatalf("no handler registered for event %q", event)
	}
	args, _ := json.Marshal(data)
	for _, hid := range ids {
		h.mu.Lock()
		h.nextID++
		id := fmt.Sprintf("r%d", h.nextID)
		ch := make(chan json.RawMessage, 1)
		h.pending[id] = ch
		h.mu.Unlock()
		h.write(map[string]any{"type": "request", "id": id, "request": map[string]any{
			"method":     "event",
			"event":      event,
			"handler_id": hid,
			"args":       json.RawMessage(args),
		}})
		select {
		case <-ch:
			h.mu.Lock()
			fail := h.failures[id]
			h.mu.Unlock()
			if fail != "" {
				h.t.Fatalf("event %s handler %d failed: %s", event, hid, fail)
			}
		case <-time.After(5 * time.Second):
			h.t.Fatalf("event %s handler %d timed out", event, hid)
		}
	}
}

func (h *testFakeHost) read() testWireFrame {
	var hdr [4]byte
	if _, err := io.ReadFull(h.nc, hdr[:]); err != nil {
		h.t.Fatalf("read header: %v", err)
	}
	data := make([]byte, binary.BigEndian.Uint32(hdr[:]))
	if _, err := io.ReadFull(h.nc, data); err != nil {
		h.t.Fatalf("read frame: %v", err)
	}
	var f testWireFrame
	if err := json.Unmarshal(data, &f); err != nil {
		h.t.Fatalf("decode frame: %v", err)
	}
	return f
}

func (h *testFakeHost) write(v any) {
	data, _ := json.Marshal(v)
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(data)))
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	if _, err := h.nc.Write(hdr[:]); err != nil {
		return
	}
	_, _ = h.nc.Write(data)
}

func (h *testFakeHost) serve() {
	for {
		var hdr [4]byte
		if _, err := io.ReadFull(h.nc, hdr[:]); err != nil {
			return
		}
		data := make([]byte, binary.BigEndian.Uint32(hdr[:]))
		if _, err := io.ReadFull(h.nc, data); err != nil {
			return
		}
		var f testWireFrame
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

func (h *testFakeHost) answer(f testWireFrame) {
	go func() {
		var result map[string]any
		if f.Call.Method == "getSessionID" {
			result = map[string]any{"sessionId": h.sessionID}
		} else {
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

func (h *testFakeHost) stop() {
	h.write(map[string]any{"type": "shutdown", "shutdown": map[string]any{"reason": "test"}})
	select {
	case <-h.runDone:
	case <-time.After(5 * time.Second):
	}
	_ = h.nc.Close()
}

// TestSessionReloadClearsTombstoneAndReopensFresh verifies that after
// session_shutdown(reason="reload") / lifecycle.End(id):
//  1. tabs opened before reload are closed,
//  2. a late open arriving after End and before session_start is still refused,
//  3. session_start for the same session ID on a fresh extension instance clears
//     the tombstone so subsequent opens succeed with a fresh lease.
func TestSessionReloadClearsTombstoneAndReopensFresh(t *testing.T) {
	orig := RegisteredBackends()
	t.Cleanup(func() {
		for k := range RegisteredBackends() {
			RegisterBackend(k, nil)
		}
		for k, v := range orig {
			RegisterBackend(k, v)
		}
		lifecycle.ResetForTests()
		defaultManager.ReleaseAllTabs(context.Background(), true)
	})

	var openSeq atomic.Int32
	fb := &countingBackend{onOpen: func(OpenOptions) { openSeq.Add(1) }}
	RegisterBackend(string(KindHeadless), fb)

	const sid = "reload-sess-1"
	sOff := DefaultSettings()
	sOff.Tern, sOff.WebKit, sOff.Cmux, sOff.Relay = false, false, false, false

	// Instance 1: session starts and opens a tab.
	ext1 := sdk.New("browser-inst1")
	Register(ext1)
	RegisterBackend(string(KindHeadless), fb)
	host1 := startTestFakeHost(t, ext1, sid)
	host1.fire("session_start", map[string]any{"type": "session_start"})

	req := InvokeRequest{OwnerSessionID: sid, Cwd: t.TempDir(), Settings: &sOff}
	if _, err := defaultManager.Invoke(context.Background(), req, json.RawMessage(`{"action":"open","name":"main","url":"http://before.test","app":"chromium"}`)); err != nil {
		t.Fatalf("open before reload failed: %v", err)
	}
	if openSeq.Load() != 1 || len(defaultManager.ListTabsForOwner(sid)) != 1 {
		t.Fatalf("expected 1 open tab before reload, opens=%d tabs=%d", openSeq.Load(), len(defaultManager.ListTabsForOwner(sid)))
	}

	// Reload fires session_shutdown(reason="reload") -> lifecycle.End(sid).
	closedBefore := fb.closed.Load()
	host1.fire("session_shutdown", map[string]any{"type": "session_shutdown", "reason": "reload"})
	if fb.closed.Load()-closedBefore != 1 {
		t.Fatalf("expected pre-reload tab closed on shutdown, closed delta=%d", fb.closed.Load()-closedBefore)
	}
	if len(defaultManager.ListTabsForOwner(sid)) != 0 {
		t.Fatalf("expected 0 tabs after shutdown")
	}

	// Late open arriving AFTER End and BEFORE the new session_start is still rejected.
	if _, err := defaultManager.Invoke(context.Background(), req, json.RawMessage(`{"action":"open","name":"late","app":"chromium"}`)); err == nil || !strings.Contains(err.Error(), "ended") {
		t.Fatalf("expected late open before new session_start to be rejected, got %v", err)
	}
	if openSeq.Load() != 1 {
		t.Fatalf("late open should not have called backend.Open, opens=%d", openSeq.Load())
	}

	// Fresh extension instance receives session_start for the SAME session ID.
	ext2 := sdk.New("browser-inst2")
	Register(ext2)
	RegisterBackend(string(KindHeadless), fb)
	host2 := startTestFakeHost(t, ext2, sid)
	host2.fire("session_start", map[string]any{"type": "session_start"})

	// Opening after reload's session_start succeeds with a fresh lease.
	res, err := defaultManager.Invoke(context.Background(), req, json.RawMessage(`{"action":"open","name":"main","url":"http://after.test","app":"chromium"}`))
	if err != nil || !strings.Contains(res.Text, "Opened") {
		t.Fatalf("expected fresh open after reload session_start, got res=%+v err=%v", res, err)
	}
	if openSeq.Load() != 2 || len(defaultManager.ListTabsForOwner(sid)) != 1 {
		t.Fatalf("expected fresh lease (opens=2, tabs=1), got opens=%d tabs=%d", openSeq.Load(), len(defaultManager.ListTabsForOwner(sid)))
	}
}
