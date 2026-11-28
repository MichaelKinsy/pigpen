package pause

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
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

type pauseFakeHost struct {
	t         *testing.T
	nc        net.Conn
	sessionID string
	hasUI     bool
	runDone   chan error
	writeMu   sync.Mutex
	mu        sync.Mutex
	pending   map[string]chan json.RawMessage
	failures  map[string]string
	handlers  map[string][]int
	notifies  []string
	nextID    int
	onSelect  func(title string, options []string) (any, bool)
}

type pauseWireFrame struct {
	Type     string `json:"type"`
	ID       string `json:"id,omitempty"`
	Register *struct {
		Handlers []struct {
			Event     string `json:"event"`
			HandlerID int    `json:"handler_id"`
		} `json:"handlers"`
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

func startPauseHost(t *testing.T, ext *sdk.Extension, sessionID string, hasUI bool) *pauseFakeHost {
	t.Helper()
	hostSide, extSide := net.Pipe()
	h := &pauseFakeHost{
		t:         t,
		nc:        hostSide,
		sessionID: sessionID,
		hasUI:     hasUI,
		runDone:   make(chan error, 1),
		pending:   map[string]chan json.RawMessage{},
		failures:  map[string]string{},
		handlers:  map[string][]int{},
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
	for _, hd := range first.Register.Handlers {
		h.handlers[hd.Event] = append(h.handlers[hd.Event], hd.HandlerID)
	}
	mode := "print"
	if hasUI {
		mode = "tui"
	}
	state, _ := json.Marshal(map[string]any{
		"hasUI":     hasUI,
		"sessionId": sessionID,
	})
	h.write(map[string]any{"type": "ready", "ready": map[string]any{
		"session_name": sessionID,
		"cwd":          t.TempDir(),
		"mode":         mode,
		"width":        100,
		"height":       40,
		"state":        json.RawMessage(state),
	}})
	go h.serve()
	t.Cleanup(h.stop)
	return h
}

func (h *pauseFakeHost) read() pauseWireFrame {
	var hdr [4]byte
	if _, err := io.ReadFull(h.nc, hdr[:]); err != nil {
		h.t.Fatalf("read header: %v", err)
	}
	data := make([]byte, binary.BigEndian.Uint32(hdr[:]))
	if _, err := io.ReadFull(h.nc, data); err != nil {
		h.t.Fatalf("read frame: %v", err)
	}
	var f pauseWireFrame
	if err := json.Unmarshal(data, &f); err != nil {
		h.t.Fatalf("decode frame: %v", err)
	}
	return f
}

func (h *pauseFakeHost) write(v any) {
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

func (h *pauseFakeHost) serve() {
	for {
		var hdr [4]byte
		if _, err := io.ReadFull(h.nc, hdr[:]); err != nil {
			return
		}
		data := make([]byte, binary.BigEndian.Uint32(hdr[:]))
		if _, err := io.ReadFull(h.nc, data); err != nil {
			return
		}
		var f pauseWireFrame
		if json.Unmarshal(data, &f) != nil {
			continue
		}
		switch f.Type {
		case "call":
			if f.Call != nil {
				go h.answer(f)
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

func (h *pauseFakeHost) answer(f pauseWireFrame) {
	var args map[string]any
	if len(f.Call.Args) > 0 {
		_ = json.Unmarshal(f.Call.Args, &args)
	}
	var result any = map[string]any{}
	switch f.Call.Method {
	case "ui.notify":
		msg, _ := args["message"].(string)
		h.mu.Lock()
		h.notifies = append(h.notifies, msg)
		h.mu.Unlock()
	case "ui.select":
		title, _ := args["title"].(string)
		var opts []string
		if rawOpts, ok := args["options"].([]any); ok {
			for _, o := range rawOpts {
				if s, ok := o.(string); ok {
					opts = append(opts, s)
				}
			}
		}
		h.mu.Lock()
		cb := h.onSelect
		h.mu.Unlock()
		if cb != nil {
			val, ok := cb(title, opts)
			if ok {
				result = map[string]any{"value": val}
			} else {
				result = map[string]any{}
			}
		}
	case "sessionRead":
		method, _ := args["method"].(string)
		if method == "getSessionId" {
			result = h.sessionID
		}
	}
	raw, _ := json.Marshal(result)
	h.write(map[string]any{
		"type":        "call_result",
		"id":          f.ID,
		"call_result": map[string]any{"result": json.RawMessage(raw)},
	})
}

func (h *pauseFakeHost) startRequest(req map[string]any) (string, chan json.RawMessage) {
	h.mu.Lock()
	h.nextID++
	id := fmt.Sprintf("req-%d", h.nextID)
	ch := make(chan json.RawMessage, 1)
	h.pending[id] = ch
	h.mu.Unlock()
	h.write(map[string]any{"type": "request", "id": id, "request": req})
	return id, ch
}

func (h *pauseFakeHost) roundTrip(req map[string]any) (json.RawMessage, string) {
	h.t.Helper()
	id, ch := h.startRequest(req)
	select {
	case res := <-ch:
		h.mu.Lock()
		errMsg := h.failures[id]
		h.mu.Unlock()
		return res, errMsg
	case <-time.After(10 * time.Second):
		h.t.Fatalf("timed out waiting for %v", req["method"])
		return nil, ""
	}
}

func (h *pauseFakeHost) cancelRequest(id string) {
	h.write(map[string]any{
		"type":   "cancel",
		"id":     id,
		"cancel": map[string]any{"id": id, "reason": "user cancelled"},
	})
}

func (h *pauseFakeHost) callCommand(name, args string) (json.RawMessage, string) {
	h.t.Helper()
	return h.roundTrip(map[string]any{
		"method": "command",
		"tool":   name,
		"args":   args,
	})
}

func (h *pauseFakeHost) startEventAsync(event string, data map[string]any) (string, chan json.RawMessage) {
	h.t.Helper()
	ids := h.handlers[event]
	if len(ids) == 0 {
		h.t.Fatalf("no handler registered for %q", event)
	}
	if data == nil {
		data = map[string]any{}
	}
	raw, _ := json.Marshal(data)
	return h.startRequest(map[string]any{
		"method":     "event",
		"event":      event,
		"handler_id": ids[0],
		"args":       json.RawMessage(raw),
	})
}

func (h *pauseFakeHost) emitEvent(event string, data map[string]any) (json.RawMessage, string) {
	h.t.Helper()
	id, ch := h.startEventAsync(event, data)
	select {
	case res := <-ch:
		h.mu.Lock()
		errMsg := h.failures[id]
		h.mu.Unlock()
		return res, errMsg
	case <-time.After(10 * time.Second):
		h.t.Fatalf("timed out waiting for event %s", event)
		return nil, ""
	}
}

func (h *pauseFakeHost) lastNotify() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.notifies) == 0 {
		return ""
	}
	return h.notifies[len(h.notifies)-1]
}

func (h *pauseFakeHost) stop() {
	h.write(map[string]any{"type": "shutdown"})
	_ = h.nc.Close()
	select {
	case <-h.runDone:
	case <-time.After(5 * time.Second):
	}
}

// --- Gate unit tests ---

// upstream: packages/agent/test/pause-gate.test.ts: "holds the next model call while paused and releases it on resume"
func TestGatePauseResumeIdempotencyAndMetadata(t *testing.T) {
	g := NewGate()

	if g.IsPaused() {
		t.Fatal("new gate must start unpaused")
	}
	if _, ok := g.PausedSince(); ok {
		t.Fatal("unpaused gate must report PausedSince ok=false")
	}
	if d, ok := g.Resume(); ok || d != 0 {
		t.Fatalf("Resume on unpaused gate = (%v, %v), want (0, false)", d, ok)
	}

	if !g.Pause() {
		t.Fatal("first Pause() must return true")
	}
	if g.Pause() {
		t.Fatal("second Pause() while paused must return false")
	}
	if !g.IsPaused() {
		t.Fatal("gate must report IsPaused() == true")
	}
	since, ok := g.PausedSince()
	if !ok || since.IsZero() {
		t.Fatalf("expected non-zero PausedSince, got (%v, %v)", since, ok)
	}
	st := g.Status()
	if !st.Paused || !st.PausedSince.Equal(since) {
		t.Fatalf("unexpected Status: %+v", st)
	}

	time.Sleep(15 * time.Millisecond)
	elapsed, resumed := g.Resume()
	if !resumed || elapsed < 10*time.Millisecond {
		t.Fatalf("expected Resume() >= 10ms, got (%v, %v)", elapsed, resumed)
	}
	// Repeated Resume must be a safe no-op (never double-close panic).
	if d2, ok2 := g.Resume(); ok2 || d2 != 0 {
		t.Fatalf("second Resume() = (%v, %v), want (0, false)", d2, ok2)
	}
	if g.IsPaused() || g.Status().Paused {
		t.Fatal("gate must be unpaused after Resume")
	}
}

// upstream: packages/agent/test/pause-gate.test.ts: "lets an external abort unwind a parked run without releasing the gate"
func TestGateConcurrentWaitersAndSingleWaiterCancel(t *testing.T) {
	g := NewGate()
	if err := g.WaitUntilResumed(context.Background()); err != nil {
		t.Fatalf("WaitUntilResumed when unpaused returned %v", err)
	}

	g.Pause()

	const numWaiters = 8
	var released atomic.Int32
	var wg sync.WaitGroup
	errs := make(chan error, numWaiters)

	for range numWaiters {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := g.WaitUntilResumed(context.Background())
			released.Add(1)
			errs <- err
		}()
	}

	// Cancel one dedicated waiter; it must unwind immediately while leaving the gate engaged.
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancelledDone := make(chan error, 1)
	go func() {
		cancelledDone <- g.WaitUntilResumed(cancelCtx)
	}()

	time.Sleep(15 * time.Millisecond)
	cancel()
	select {
	case err := <-cancelledDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled for cancelled waiter, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled waiter did not unwind promptly")
	}
	if !g.IsPaused() {
		t.Fatal("cancelling one waiter must NOT release the gate")
	}
	if released.Load() != 0 {
		t.Fatalf("expected 0 other waiters released while paused, got %d", released.Load())
	}

	// Now resume and confirm all remaining waiters wake atomically.
	g.Resume()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("waiters did not wake after Resume")
	}
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("waiter returned unexpected error: %v", err)
		}
	}
	if released.Load() != numWaiters {
		t.Fatalf("expected all %d waiters released, got %d", numWaiters, released.Load())
	}
}

// upstream: packages/agent/test/pause-gate.test.ts: "reports pause state transitions to onChange subscribers"
func TestGateOnChangeListenersCalledOutsideMutex(t *testing.T) {
	g := NewGate()

	var mu sync.Mutex
	var transitions []bool
	unsub := g.OnChange(func(paused bool) {
		// Re-entering g.IsPaused() inside listener verifies mutex is NOT held.
		if g.IsPaused() != paused {
			t.Errorf("IsPaused()=%v disagreed with callback arg %v", g.IsPaused(), paused)
		}
		mu.Lock()
		transitions = append(transitions, paused)
		mu.Unlock()
	})
	// Panicking listener must not break other listeners or the gate.
	unsubPanic := g.OnChange(func(bool) {
		panic("boom listener")
	})
	defer unsubPanic()

	g.Pause()
	g.Pause() // duplicate does not fire
	g.Resume()
	g.Resume() // duplicate does not fire
	unsub()
	unsub() // idempotent unsubscribe
	g.Pause()
	g.Resume()

	mu.Lock()
	defer mu.Unlock()
	if len(transitions) != 2 || !transitions[0] || transitions[1] {
		t.Fatalf("unexpected transitions: %v, want [true false]", transitions)
	}
}

// Concurrent stress test verifying -race safety under interleaved Pause/Resume/WaitUntilResumed.
func TestGateConcurrentStressRaceFree(t *testing.T) {
	g := NewGate()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	var wg sync.WaitGroup
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				_ = g.WaitUntilResumed(ctx)
			}
		}()
	}
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				g.Pause()
				_ = g.Status()
				g.Resume()
			}
		}()
	}
	wg.Wait()
	g.Resume()
}

// --- Slash command & EventToolCall integration tests ---

func TestPauseCommandHeadlessToggleStatusResume(t *testing.T) {
	g := NewGate()
	prevWork := WorkStatus
	t.Cleanup(func() {
		WorkStatus = prevWork
		g.Resume()
	})
	WorkStatus = func(_ sdk.Context) WorkSummary {
		return WorkSummary{UnsettledTurns: 1, UnfinishedJobs: 2, ActiveAgents: 1}
	}

	ext := sdk.New("test-pause-cmd")
	registerWithGate(ext, g)
	sid := "sess-pause-cmd"
	t.Cleanup(func() { lifecycle.End(sid) })
	h := startPauseHost(t, ext, sid, false)
	_, _ = h.emitEvent(sdk.EventSessionStart, nil)

	// status while running
	if _, errMsg := h.callCommand("pause", "status"); errMsg != "" {
		t.Fatalf("/pause status failed: %s", errMsg)
	}
	if got := h.lastNotify(); !strings.Contains(got, "Running") || !strings.Contains(got, "3 running jobs/agents") {
		t.Fatalf("unexpected running status notify: %q", got)
	}

	// resume while not paused
	if _, errMsg := h.callCommand("pause", "resume"); errMsg != "" {
		t.Fatalf("/pause resume failed: %s", errMsg)
	}
	if got := h.lastNotify(); got != "Not paused." {
		t.Fatalf("expected 'Not paused.', got %q", got)
	}

	// toggle ON with /pause
	if _, errMsg := h.callCommand("pause", ""); errMsg != "" {
		t.Fatalf("/pause failed: %s", errMsg)
	}
	if !g.IsPaused() {
		t.Fatal("expected gate paused after /pause")
	}
	if got := h.lastNotify(); !strings.Contains(got, "Paused (3 running jobs/agents") {
		t.Fatalf("unexpected pause notify: %q", got)
	}

	// status while paused
	if _, errMsg := h.callCommand("pause", "status"); errMsg != "" {
		t.Fatalf("/pause status failed: %s", errMsg)
	}
	if got := h.lastNotify(); !strings.Contains(got, "Paused for") {
		t.Fatalf("unexpected paused status notify: %q", got)
	}

	// toggle OFF with /pause (no args)
	if _, errMsg := h.callCommand("pause", ""); errMsg != "" {
		t.Fatalf("second /pause failed: %s", errMsg)
	}
	if g.IsPaused() {
		t.Fatal("expected gate resumed after second /pause")
	}
	if got := h.lastNotify(); !strings.Contains(got, "Resumed after") {
		t.Fatalf("unexpected resume notify: %q", got)
	}

	// toggle ON then resume via `/pause off`
	_, _ = h.callCommand("pause", "")
	if !g.IsPaused() {
		t.Fatal("expected gate paused")
	}
	_, _ = h.callCommand("pause", "off")
	if g.IsPaused() {
		t.Fatal("expected gate resumed after /pause off")
	}
}

func TestPauseCommandInteractiveSelectResumesOnDismiss(t *testing.T) {
	g := NewGate()
	t.Cleanup(func() { g.Resume() })

	ext := sdk.New("test-pause-tui")
	registerWithGate(ext, g)
	sid := "sess-pause-tui"
	t.Cleanup(func() { lifecycle.End(sid) })
	h := startPauseHost(t, ext, sid, true)
	_, _ = h.emitEvent(sdk.EventSessionStart, nil)

	enteredDialog := make(chan struct{}, 1)
	releaseDialog := make(chan struct{})
	h.mu.Lock()
	h.onSelect = func(title string, options []string) (any, bool) {
		if !g.IsPaused() {
			t.Errorf("gate must be paused while select dialog is open")
		}
		select {
		case enteredDialog <- struct{}{}:
		default:
		}
		<-releaseDialog
		return "Resume", true
	}
	h.mu.Unlock()

	cmdDone := make(chan string, 1)
	go func() {
		_, errMsg := h.callCommand("pause", "")
		cmdDone <- errMsg
	}()

	select {
	case <-enteredDialog:
	case <-time.After(3 * time.Second):
		close(releaseDialog)
		t.Fatal("timed out waiting for interactive Resume dialog")
	}
	if !g.IsPaused() {
		close(releaseDialog)
		t.Fatal("expected gate to be paused while dialog is shown")
	}

	close(releaseDialog)
	select {
	case errMsg := <-cmdDone:
		if errMsg != "" {
			t.Fatalf("/pause interactive failed: %s", errMsg)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for /pause command to return")
	}
	if g.IsPaused() {
		t.Fatal("expected gate to resume when dialog dismissed")
	}
	if got := h.lastNotify(); !strings.Contains(got, "Resumed after") {
		t.Fatalf("expected resumed notification, got %q", got)
	}
}

// upstream: packages/agent/test/pause-gate.test.ts: "holds tool execution at the tool boundary when paused mid-turn"
func TestWaitBeforeToolCallParksWhilePausedAndWakesOnResume(t *testing.T) {
	g := NewGate()
	t.Cleanup(func() { g.Resume() })

	// Register must NOT install its own EventToolCall handler (M5: zero IPC overhead).
	ext := sdk.New("test-pause-toolcall")
	registerWithGate(ext, g)
	sid := "sess-pause-tool"
	t.Cleanup(func() { lifecycle.End(sid) })
	h := startPauseHost(t, ext, sid, false)
	_, _ = h.emitEvent(sdk.EventSessionStart, nil)
	if len(h.handlers[sdk.EventToolCall]) > 0 {
		t.Fatalf("expected pause.Register NOT to register EventToolCall")
	}

	// Fast path when unpaused: returns nil immediately.
	if err := waitBeforeToolCallWithGate(sdk.Context{}, g); err != nil {
		t.Fatalf("unpaused WaitBeforeToolCall failed: %v", err)
	}

	// Engage pause: WaitBeforeToolCall parks until Resume.
	g.Pause()
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- waitBeforeToolCallWithGate(sdk.Context{}, g)
	}()
	select {
	case err := <-doneCh:
		t.Fatalf("WaitBeforeToolCall returned while paused: %v", err)
	case <-time.After(60 * time.Millisecond):
	}

	// Resume wakes the parked caller with nil.
	g.Resume()
	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("expected nil after resume, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("parked WaitBeforeToolCall did not wake after Resume")
	}
}

func TestHeadlessOwnerSessionShutdownReleasesPause(t *testing.T) {
	g := NewGate()
	t.Cleanup(func() { g.Resume() })

	extA := sdk.New("test-pause-owner-a")
	registerWithGate(extA, g)
	hA := startPauseHost(t, extA, "sess-owner-a", false)
	t.Cleanup(func() { lifecycle.End("sess-owner-a") })
	_, _ = hA.emitEvent(sdk.EventSessionStart, nil)

	extB := sdk.New("test-pause-owner-b")
	registerWithGate(extB, g)
	hB := startPauseHost(t, extB, "sess-owner-b", false)
	t.Cleanup(func() { lifecycle.End("sess-owner-b") })
	_, _ = hB.emitEvent(sdk.EventSessionStart, nil)

	// Session A engages /pause headlessly while session B is also active.
	if _, errMsg := hA.callCommand("pause", ""); errMsg != "" {
		t.Fatalf("/pause failed: %s", errMsg)
	}
	if !g.IsPaused() {
		t.Fatal("expected gate paused")
	}

	// Session A shuts down -> gate must resume even though session B is still alive.
	_, _ = hA.emitEvent(sdk.EventSessionShutdown, nil)
	if g.IsPaused() {
		t.Fatal("expected headless owner session shutdown to release pause")
	}
}

func TestOnChangeNotificationsAreOrdered(t *testing.T) {
	g := NewGate()
	var mu sync.Mutex
	var seq []bool
	unsub := g.OnChange(func(paused bool) {
		mu.Lock()
		seq = append(seq, paused)
		mu.Unlock()
	})
	defer unsub()

	for range 20 {
		g.Pause()
		g.Resume()
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seq) != 40 {
		t.Fatalf("expected 40 notifications, got %d", len(seq))
	}
	for i, v := range seq {
		want := i%2 == 0
		if v != want {
			t.Fatalf("notification[%d] = %v, want %v (out of order)", i, v, want)
		}
	}
}

func TestSessionShutdownReleasesLeakedPause(t *testing.T) {
	g := NewGate()
	t.Cleanup(func() { g.Resume() })

	ext := sdk.New("test-pause-shutdown")
	registerWithGate(ext, g)
	sid := "sess-pause-shutdown"
	h := startPauseHost(t, ext, sid, false)
	_, _ = h.emitEvent(sdk.EventSessionStart, nil)

	g.Pause()
	if !g.IsPaused() {
		t.Fatal("expected gate paused")
	}
	_, _ = h.emitEvent(sdk.EventSessionShutdown, nil)
	if g.IsPaused() {
		t.Fatal("expected last session shutdown to release leaked pause")
	}
}
