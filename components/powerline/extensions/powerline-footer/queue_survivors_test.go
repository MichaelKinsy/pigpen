package powerline_footer

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Tests that kill the surviving mutants of the queue slice. They use queue_store.go, queue_state.go, queue_editor.go,
// extension.go and select_overlay.go (the picker is shared with the keys and cdquote slices; the queue slice is the earliest to
// have the file), and the test helpers of the queue tests (queueRig, delayRig, withStore).

// The summary reads the inbox once and answers from its snapshot while the file is unchanged (store.ts getDisplayItems).
func TestSummarySnapshotAnswersWithoutReadingTheInbox(t *testing.T) {
	withStore(t, func(store tstore, dir string) {
		context := currentQueueContext(dir, "")
		store.add(createQueueItemInput{Text: "visible", Source: queueSource{Cwd: dir}, Target: queueTarget{Kind: targetGlobal}, Intent: intentSteer})
		eq(t, store.summarize(context, false).LeadingText, "visible", "first summary reads the inbox")
		read := queueReadFile
		reads := 0
		queueReadFile = func(path string) ([]byte, error) { reads++; return read(path) }
		defer func() { queueReadFile = read }()
		eq(t, store.summarize(context, false).LeadingText, "visible", "second summary")
		eq(t, store.summarize(context, false).QueueCount, 1, "third summary")
		eq(t, reads, 0, "an unchanged inbox is answered from the snapshot")
	})
}

// Only an item still "delivering" goes back to queued after 60 s (index.ts 1620-1625).
func TestSixtySecondTimerLeavesAnItemThatIsNoLongerDelivering(t *testing.T) {
	for _, status := range []queueStatus{statusFailed, statusBlocked, statusSent} {
		r := newQueueRig(t)
		r.host.idle = true
		item := r.item("stuck", intentFollowUp, queueTarget{Kind: targetGlobal})
		r.deliver(item)
		if _, err := r.store.update(item.ID, queueUpdates{Status: status, SetError: true, Error: "elsewhere"}); err != nil {
			t.Fatal(err)
		}
		r.clock.tick(61 * time.Second)
		got := r.get(item.ID)
		eq(t, got.Status, status, "the status "+string(status)+" is kept")
		eq(t, got.Error, "elsewhere", "and so is its error")
	}
}

// A new compaction retires the delivery that was waiting for the last one (index.ts 2111-2121 cancelPostCompactionDelivery).
func TestSessionBeforeCompactCancelsThePostCompactionTimer(t *testing.T) {
	r := newQueueRig(t)
	r.emit("session_start", map[string]any{"reason": "new"})
	r.item("after", intentPostCompact, queueTarget{Kind: targetCurrentSession})
	r.emit("session_before_compact", nil)
	r.emit("session_compact", map[string]any{"willRetry": false})
	eq(t, r.clock.active(), 1, "the delivery after the compaction is scheduled")
	polls := 0
	r.host.idleHook = func() (bool, error) { polls++; return false, nil }
	r.emit("session_before_compact", nil)
	eq(t, r.clock.active(), 0, "the next compaction cancels it")
	r.clock.tick(time.Second)
	eq(t, polls, 0, "and nothing asks for idleness any more")
}

// Enter on an empty editor during a compaction sends the waiting delayed prompt now, which queues it for after the compaction
// (index.ts 3370-3376).
func TestEmptySubmitDuringCompactionSendsTheWaitingPrompt(t *testing.T) {
	r := newDelayRig(t, 4000)
	r.submit("later")
	r.emit("session_before_compact", nil)
	r.ed.text = "  "
	r.q.mu.Lock()
	verdict := r.q.interceptEditorInput(r.ed, editorInput{Submit: true})
	r.q.mu.Unlock()
	eq(t, verdict, editorHandled, "handled")
	if r.q.pendingSend != nil {
		t.Error("the waiting prompt was not sent")
	}
	items, _ := r.store.queuedDeliveryItems(r.q.context(), intentPostCompact)
	if len(items) != 1 || items[0].Text != "later" {
		t.Fatalf("queued = %+v, want the waiting prompt", items)
	}
	eq(t, r.ed.history, []string{"later"}, "kept in the editor history")
}

// miniHost is just enough of the PiG host for an extension to run: it answers host calls and records the request each call
// belongs to, which the shared fake host does not show.
type miniHost struct {
	t       *testing.T
	nc      net.Conn
	wmu     sync.Mutex
	mu      sync.Mutex
	pending map[string]chan struct{}
	parents map[string]string // method -> parent request id of its last call
	handler int
}

func (h *miniHost) readFrame() (map[string]json.RawMessage, bool) {
	var hdr [4]byte
	if _, err := io.ReadFull(h.nc, hdr[:]); err != nil {
		return nil, false
	}
	data := make([]byte, binary.BigEndian.Uint32(hdr[:]))
	if _, err := io.ReadFull(h.nc, data); err != nil {
		return nil, false
	}
	var f map[string]json.RawMessage
	_ = json.Unmarshal(data, &f)
	return f, true
}

func (h *miniHost) write(v any) {
	data, _ := json.Marshal(v)
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(data)))
	h.wmu.Lock()
	defer h.wmu.Unlock()
	_, _ = h.nc.Write(append(hdr[:], data...))
}

func (h *miniHost) answer(f map[string]json.RawMessage) {
	var id string
	_ = json.Unmarshal(f["id"], &id)
	var call struct {
		Method          string `json:"method"`
		ParentRequestID string `json:"parent_request_id"`
	}
	_ = json.Unmarshal(f["call"], &call)
	h.mu.Lock()
	h.parents[call.Method] = call.ParentRequestID
	h.mu.Unlock()
	h.write(map[string]any{"type": "call_result", "id": id, "call_result": map[string]any{"result": map[string]any{}}})
}

// fire sends an event request to the extension's handler and returns a channel closed once the handler has answered.
func (h *miniHost) fire(id string) chan struct{} {
	done := make(chan struct{})
	h.mu.Lock()
	h.pending[id] = done
	h.mu.Unlock()
	h.write(map[string]any{"type": "request", "id": id, "request": map[string]any{
		"method": "event", "event": sdk.EventAgentEnd, "handler_id": h.handler, "args": map[string]any{"type": sdk.EventAgentEnd}}})
	return done
}

func (h *miniHost) serve() {
	for {
		f, ok := h.readFrame()
		if !ok {
			return
		}
		var typ string
		_ = json.Unmarshal(f["type"], &typ)
		switch typ {
		case "call":
			h.answer(f)
		case "response":
			var id string
			_ = json.Unmarshal(f["id"], &id)
			h.mu.Lock()
			done := h.pending[id]
			delete(h.pending, id)
			h.mu.Unlock()
			if done != nil {
				close(done)
			}
		}
	}
}

// The sender is bound to the context that asked for it (queue_state.go queueHostAPI.Sender); a later handler's context, which
// the host switches to meanwhile, must not take over a send the earlier context started (extension.go sdkHost.Sender).
func TestSenderStaysBoundToTheContextThatCreatedIt(t *testing.T) {
	ctxs := make(chan sdk.Context, 2)
	release := make(chan struct{})
	var once sync.Once
	e := sdk.New("sender-ctx")
	e.OnEvent(sdk.EventAgentEnd, func(ctx sdk.Context, _ map[string]any) (any, error) {
		first := false
		once.Do(func() { first = true })
		ctxs <- ctx
		if !first {
			<-release // the second handler stays active
		}
		return nil, nil
	})

	hostSide, extSide := net.Pipe()
	h := &miniHost{t: t, nc: hostSide, pending: map[string]chan struct{}{}, parents: map[string]string{}}
	runDone := make(chan error, 1)
	go func() { runDone <- e.RunWithConn(extSide) }()
	var register map[string]json.RawMessage
	for {
		f, ok := h.readFrame()
		if !ok {
			t.Fatal("connection closed before register")
		}
		var typ string
		_ = json.Unmarshal(f["type"], &typ)
		if typ == "call" {
			h.answer(f)
			continue
		}
		if typ == "register" {
			register = f
			break
		}
	}
	var reg struct {
		Handlers []struct {
			HandlerID int `json:"handler_id"`
		} `json:"handlers"`
	}
	_ = json.Unmarshal(register["register"], &reg)
	if len(reg.Handlers) != 1 {
		t.Fatalf("handlers = %+v", reg.Handlers)
	}
	h.handler = reg.Handlers[0].HandlerID
	h.write(map[string]any{"type": "ready", "ready": map[string]any{
		"session_name": "", "cwd": t.TempDir(), "mode": "tui", "width": 80, "state": map[string]any{"hasUI": true}}})
	go h.serve()
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		h.write(map[string]any{"type": "shutdown", "shutdown": map[string]any{"reason": "test"}})
		select {
		case <-runDone:
		case <-time.After(10 * time.Second):
			t.Error("extension did not stop")
		}
		_ = hostSide.Close()
	})

	wait := func(ch chan struct{}) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(10 * time.Second):
			t.Fatal("no response from the extension")
		}
	}
	wait(h.fire("r1"))
	first := <-ctxs
	h.fire("r2") // stays active until released
	second := <-ctxs

	host := &sdkHost{ctx: first}
	send := host.Sender()
	host.ctx = second
	if err := send("text", "followUp"); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	parent, called := h.parents["sendUserMessage"]
	h.mu.Unlock()
	if !called {
		t.Fatal("sendUserMessage was not called")
	}
	eq(t, parent, "", "the call belongs to the finished request of the first context, not to the handler that is running")
}

type plainOverlayTheme struct{}

func (plainOverlayTheme) Fg(_, text string) string { return text }
func (plainOverlayTheme) Bold(text string) string  { return text }

// pi-tui SelectList keeps the selection in the middle of the window (select-list.js render: selectedIndex - floor(maxVisible / 2)).
func TestSelectListWindowCentersTheSelection(t *testing.T) {
	var items []selectItem
	for _, v := range []string{"v0", "v1", "v2", "v3", "v4", "v5", "v6", "v7", "v8", "v9"} {
		items = append(items, selectItem{Value: v})
	}
	l := &selectList{items: items, selectedIndex: 4, maxVisible: 5, theme: plainOverlayTheme{}}
	eq(t, l.render(30), []string{"  v2", "  v3", "→ v4", "  v5", "  v6", "  (5/10)"}, "window around the selection")
}

// A description is shown on one line: line breaks become a space (select-list.js normalizeToSingleLine).
func TestSelectListDescriptionIsOneLine(t *testing.T) {
	l := &selectList{items: []selectItem{{Value: "x", Description: "first\r\n\nsecond line"}, {Value: "y"}}, selectedIndex: 1, maxVisible: 5, theme: plainOverlayTheme{}}
	eq(t, l.render(80)[0], "  x"+strings.Repeat(" ", 31)+"first second line", "one line")
}

// Each row of the overlay is padded to the inner width (truncateToWidth(text, innerWidth, "…", true)), also by a single cell.
func TestSelectOverlayRowsArePaddedToTheInnerWidth(t *testing.T) {
	o := newSelectOverlay(plainOverlayTheme{}, "abc", "xyz", []selectItem{{Value: "a"}}, 5)
	lines := o.Render(6)
	eq(t, lines[1], "│abc │", "a title one cell short of the inner width")
	eq(t, lines[len(lines)-2], "│xyz │", "a hint one cell short of the inner width")
}
