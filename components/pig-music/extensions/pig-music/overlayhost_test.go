package pig_music_test

// A minimal fake PiG host for the focused-component path. It speaks the
// extension wire protocol over net.Pipe against the real SDK, answers ui.custom
// the way the host does (the call stays open until the extension closes the
// component), records every line snapshot the component sends, and delivers
// ordered input and resize notifications.

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/charmbracelet/x/ansi"
)

type snapshot struct {
	Key   string   `json:"key"`
	Lines []string `json:"lines"`
	Width int      `json:"width"`
	Seq   uint64   `json:"seq"`
}

type hostFrame struct {
	Type     string `json:"type"`
	Register *struct {
		Handlers []struct {
			Event     string `json:"event"`
			HandlerID int    `json:"handler_id"`
		} `json:"handlers"`
	} `json:"register,omitempty"`
	ID       string `json:"id,omitempty"`
	Response *struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"response,omitempty"`
	Call *struct {
		Method string          `json:"method"`
		Args   json.RawMessage `json:"args"`
	} `json:"call,omitempty"`
	Notify *struct {
		Method string          `json:"method"`
		Args   json.RawMessage `json:"args"`
	} `json:"notify,omitempty"`
	WidgetPush *struct {
		Key   string   `json:"key"`
		Lines []string `json:"lines"`
	} `json:"widget_push,omitempty"`
}

type overlayHost struct {
	t       *testing.T
	nc      net.Conn
	writeMu sync.Mutex

	mu            sync.Mutex
	customID      string
	customKey     string
	opens         []map[string]any
	snapshots     []snapshot
	notices       []map[string]any
	confirms      []map[string]any // ui.confirm calls, answered with confirmAnswer
	confirmAnswer bool
	handlers      map[string]int   // event name -> the handler id the extension subscribed with
	statuses      []map[string]any // ui.setStatus calls, in order
	widgets       []widgetEvent    // widget_push frames and ui.setWidget calls, in order
	closed        map[string]any
	responses     map[string]chan string
	nextID        int
	stopped       bool
}

func startOverlayHost(t *testing.T, ext *sdk.Extension, mode string, width, height int) *overlayHost {
	t.Helper()
	hostSide, extSide := net.Pipe()
	h := &overlayHost{t: t, nc: hostSide, responses: map[string]chan string{}}
	runDone := make(chan error, 1)
	go func() { runDone <- ext.RunWithConn(extSide) }()
	first := h.read()
	if first.Type != "register" {
		t.Fatalf("expected register, got %+v", first)
	}
	h.handlers = map[string]int{}
	if first.Register != nil {
		for _, d := range first.Register.Handlers {
			h.handlers[d.Event] = d.HandlerID
		}
	}
	state, _ := json.Marshal(map[string]any{"hasUI": mode == "tui" || mode == "rpc"})
	h.write(map[string]any{"type": "ready", "ready": map[string]any{
		"session_name": "", "cwd": t.TempDir(), "mode": mode, "width": width, "height": height, "state": json.RawMessage(state),
	}})
	go h.serve()
	t.Cleanup(func() {
		h.mu.Lock()
		h.stopped = true
		h.mu.Unlock()
		h.write(map[string]any{"type": "shutdown", "shutdown": map[string]any{"reason": "test"}})
		select {
		case <-runDone:
		case <-time.After(10 * time.Second):
			t.Errorf("extension did not stop after shutdown")
		}
		_ = h.nc.Close()
	})
	return h
}

func (h *overlayHost) read() hostFrame {
	var hdr [4]byte
	if _, err := io.ReadFull(h.nc, hdr[:]); err != nil {
		h.t.Fatalf("read header: %v", err)
	}
	data := make([]byte, binary.BigEndian.Uint32(hdr[:]))
	if _, err := io.ReadFull(h.nc, data); err != nil {
		h.t.Fatalf("read frame: %v", err)
	}
	var f hostFrame
	if err := json.Unmarshal(data, &f); err != nil {
		h.t.Fatalf("decode frame %s: %v", data, err)
	}
	return f
}

func (h *overlayHost) write(v any) {
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

func (h *overlayHost) serve() {
	for {
		var hdr [4]byte
		if _, err := io.ReadFull(h.nc, hdr[:]); err != nil {
			return
		}
		data := make([]byte, binary.BigEndian.Uint32(hdr[:]))
		if _, err := io.ReadFull(h.nc, data); err != nil {
			return
		}
		var f hostFrame
		if json.Unmarshal(data, &f) != nil {
			continue
		}
		switch {
		case f.Type == "call" && f.Call != nil:
			h.call(f)
		case f.Type == "widget_push" && f.WidgetPush != nil:
			h.mu.Lock()
			h.widgets = append(h.widgets, widgetEvent{key: f.WidgetPush.Key, lines: f.WidgetPush.Lines})
			h.mu.Unlock()
		case f.Type == "notify" && f.Notify != nil:
			h.notified(f.Notify.Method, f.Notify.Args)
		case f.Type == "response":
			h.mu.Lock()
			ch := h.responses[f.ID]
			h.mu.Unlock()
			if ch != nil {
				failure := ""
				if f.Response != nil && f.Response.Error != nil {
					failure = f.Response.Error.Message
				}
				ch <- failure
			}
		}
	}
}

func (h *overlayHost) call(f hostFrame) {
	var args map[string]any
	_ = json.Unmarshal(f.Call.Args, &args)
	if f.Call.Method == "ui.custom" {
		// The host answers only when the component closes.
		h.mu.Lock()
		h.customID, h.customKey = f.ID, fmt.Sprint(args["key"])
		h.opens = append(h.opens, args)
		h.mu.Unlock()
		return
	}
	if f.Call.Method == "ui.confirm" {
		h.mu.Lock()
		h.confirms = append(h.confirms, args)
		answer := h.confirmAnswer
		h.mu.Unlock()
		h.write(map[string]any{"type": "call_result", "id": f.ID, "call_result": map[string]any{"result": map[string]any{"confirmed": answer}}})
		return
	}
	if f.Call.Method == "event.subscribe" {
		h.mu.Lock()
		if h.handlers == nil {
			h.handlers = map[string]int{}
		}
		id, _ := args["handlerId"].(float64)
		h.handlers[fmt.Sprint(args["event"])] = int(id)
		h.mu.Unlock()
	}
	if f.Call.Method == "ui.setStatus" {
		h.mu.Lock()
		h.statuses = append(h.statuses, args)
		h.mu.Unlock()
	}
	if f.Call.Method == "ui.setWidget" {
		ev := widgetEvent{key: fmt.Sprint(args["key"])} // content null or absent: the widget is cleared
		if c, ok := args["content"].([]any); ok {
			for _, l := range c {
				ev.lines = append(ev.lines, fmt.Sprint(l))
			}
		}
		h.mu.Lock()
		h.widgets = append(h.widgets, ev)
		h.mu.Unlock()
	}
	if f.Call.Method == "ui.notify" {
		h.mu.Lock()
		h.notices = append(h.notices, args)
		h.mu.Unlock()
	}
	h.write(map[string]any{"type": "call_result", "id": f.ID, "call_result": map[string]any{"result": map[string]any{}}})
}

func (h *overlayHost) notified(method string, raw json.RawMessage) {
	switch method {
	case "ui.custom.render":
		var s snapshot
		if json.Unmarshal(raw, &s) == nil {
			h.mu.Lock()
			h.snapshots = append(h.snapshots, s)
			h.mu.Unlock()
		}
	case "ui.custom.close":
		var args map[string]any
		_ = json.Unmarshal(raw, &args)
		h.mu.Lock()
		h.closed = args
		id := h.customID
		h.mu.Unlock()
		h.write(map[string]any{"type": "call_result", "id": id, "call_result": map[string]any{"result": map[string]any{"ok": true, "result": args["result"]}}})
	}
}

// command sends /name and returns a channel that yields the handler's error text
// ("" on success) when the handler returns.
func (h *overlayHost) command(name string, args ...string) <-chan string {
	text, _ := json.Marshal(strings.Join(args, " "))
	h.mu.Lock()
	h.nextID++
	id := fmt.Sprintf("r%d", h.nextID)
	ch := make(chan string, 1)
	h.responses[id] = ch
	h.mu.Unlock()
	h.write(map[string]any{"type": "request", "id": id, "request": map[string]any{"method": "command", "tool": name, "args": json.RawMessage(text)}})
	return ch
}

func (h *overlayHost) input(data string) {
	h.mu.Lock()
	key := h.customKey
	h.mu.Unlock()
	args, _ := json.Marshal(map[string]any{"key": key, "data": data})
	h.write(map[string]any{"type": "notify", "notify": map[string]any{"method": "ui.custom.input", "args": json.RawMessage(args)}})
}

func (h *overlayHost) resize(width, height int) {
	if width > 0 {
		args, _ := json.Marshal(map[string]any{"width": width})
		h.write(map[string]any{"type": "notify", "notify": map[string]any{"method": "width_change", "args": json.RawMessage(args)}})
	}
	if height > 0 {
		args, _ := json.Marshal(map[string]any{"height": height})
		h.write(map[string]any{"type": "notify", "notify": map[string]any{"method": "height_change", "args": json.RawMessage(args)}})
	}
}

// waitOpen waits until the extension opened a component.
func (h *overlayHost) waitOpen() map[string]any {
	h.t.Helper()
	var args map[string]any
	waitFor(h.t, "ui.custom call", func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		if len(h.opens) == 0 {
			return false
		}
		args = h.opens[len(h.opens)-1]
		return true
	})
	return args
}

// waitSnapshot waits for the latest snapshot to satisfy ok and returns it.
func (h *overlayHost) waitSnapshot(what string, ok func(snapshot) bool) snapshot {
	h.t.Helper()
	var got snapshot
	waitFor(h.t, what, func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		if len(h.snapshots) == 0 {
			return false
		}
		got = h.snapshots[len(h.snapshots)-1]
		return ok(got)
	})
	return got
}

func (h *overlayHost) allSnapshots() []snapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]snapshot(nil), h.snapshots...)
}

func (h *overlayHost) notifications() []map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]map[string]any(nil), h.notices...)
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitOpenN waits until the extension has opened n components in all.
func (h *overlayHost) waitOpenN(n int) {
	h.t.Helper()
	waitFor(h.t, fmt.Sprintf("%d ui.custom calls", n), func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return len(h.opens) >= n
	})
}

func (h *overlayHost) snapshotCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.snapshots)
}

// visible is the width of a snapshot line: ANSI sequences take no cells.
func visible(line string) int { return ansi.StringWidth(line) }

// emit delivers a lifecycle event (session_start, session_shutdown) the way the host does and returns the handler's failure text.
func (h *overlayHost) emit(event string, data map[string]any) <-chan string {
	waitFor(h.t, "a handler for "+event, func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		_, ok := h.handlers[event]
		return ok
	})
	payload, _ := json.Marshal(data)
	h.mu.Lock()
	h.nextID++
	id := fmt.Sprintf("r%d", h.nextID)
	ch := make(chan string, 1)
	h.responses[id] = ch
	handler := h.handlers[event]
	h.mu.Unlock()
	h.write(map[string]any{"type": "request", "id": id, "request": map[string]any{"method": "event", "event": event, "handler_id": handler, "args": json.RawMessage(payload)}})
	return ch
}

func (h *overlayHost) shortcut(key string) <-chan string {
	h.mu.Lock()
	h.nextID++
	id := fmt.Sprintf("r%d", h.nextID)
	ch := make(chan string, 1)
	h.responses[id] = ch
	h.mu.Unlock()
	h.write(map[string]any{"type": "request", "id": id, "request": map[string]any{"method": "shortcut", "tool": key}})
	return ch
}

// lastStatus is the text of the latest footer status call, and whether there was one.
func (h *overlayHost) lastStatus() (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.statuses) == 0 {
		return "", false
	}
	return fmt.Sprint(h.statuses[len(h.statuses)-1]["text"]), true
}

func (h *overlayHost) allStatuses() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, len(h.statuses))
	for i, s := range h.statuses {
		out[i] = fmt.Sprint(s["text"])
	}
	return out
}

func lastNotice(h *overlayHost) string {
	n := h.notifications()
	if len(n) == 0 {
		return ""
	}
	return fmt.Sprint(n[len(n)-1]["message"])
}

func ctxBackground() context.Context { return context.Background() }

// widgetEvent is one write to a widget; nil lines clear it.
type widgetEvent struct {
	key   string
	lines []string
}

// lastWidget is the latest write to the widget key, and whether there was any.
func (h *overlayHost) lastWidget(key string) (widgetEvent, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := len(h.widgets) - 1; i >= 0; i-- {
		if h.widgets[i].key == key {
			return h.widgets[i], true
		}
	}
	return widgetEvent{}, false
}

func (h *overlayHost) widgetCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.widgets)
}
