package frontend

import (
	"bytes"
	"encoding/json"
	"maps"
	"strings"
	"sync"
	"testing"
	"time"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/stencil-hq/tern-sdk/go/tern"
)

// terminal is PiG's terminal output as a fake Tern reads it.
type terminal struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (t *terminal) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.buf.Write(p)
}

// take returns and clears what was written.
func (t *terminal) take() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := t.buf.String()
	t.buf.Reset()
	return out
}

func (t *terminal) peek() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.buf.String()
}

type message struct {
	verb string
	body map[string]any
}

// MarshalJSON writes the body, so a test can search frames as JSON.
func (m message) MarshalJSON() ([]byte, error) { return json.Marshal(m.body) }

// parse splits terminal output into TSP messages, joining chunks.
func parse(t *testing.T, out string) []message {
	t.Helper()
	var got []message
	chunks := map[string][]byte{}
	for _, part := range strings.Split(out, "\x1b_tsp;")[1:] {
		before, _, ok := strings.Cut(part, "\x1b\\")
		if !ok {
			t.Fatalf("unterminated message %q", part)
		}
		m, ok := tern.SplitMessage([]byte(before))
		if !ok {
			t.Fatalf("message %q", part)
		}
		body := m.Body
		var chunk, more string
		for _, p := range m.Params {
			switch p.Key {
			case "c":
				chunk = p.Value
			case "m":
				more = p.Value
			}
		}
		if chunk != "" {
			chunks[chunk] = append(chunks[chunk], body...)
			if more == "1" {
				continue
			}
			body = chunks[chunk]
			delete(chunks, chunk)
		}
		var decoded map[string]any
		if err := json.Unmarshal(body, &decoded); err != nil {
			t.Fatalf("message %q: %v", body, err)
		}
		got = append(got, message{m.Verb, decoded})
	}
	return got
}

// harness drives a session as PiG does, with a fake Tern answering on
// HandleInput: PiG's terminal is out, and a test plays Tern's side.
type harness struct {
	t       *testing.T
	session *session
	out     *terminal

	mu        sync.Mutex
	fallbacks []string
	edits     []fe.Edit
	undos     int
	sends     []string
	actions   []fe.Action
	timeout   func()
	syncs     int
	resized   int
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, out: &terminal{}}
	env := fe.Env{
		Out:     h.out,
		Getenv:  func(key string) string { return map[string]string{"TERM_PROGRAM": "tern"}[key] },
		Version: "test",
		Fallback: func(reason string) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.fallbacks = append(h.fallbacks, reason)
		},
		Edit: func(edit fe.Edit) { h.record(func() { h.edits = append(h.edits, edit) }) },
		Undo: func() { h.record(func() { h.undos++ }) },
		Send: func(text string) { h.record(func() { h.sends = append(h.sends, text) }) },
		Act:  func(action fe.Action) { h.record(func() { h.actions = append(h.actions, action) }) },
	}
	h.session = newSession(env)
	h.session.afterFunc = func(_ time.Duration, fn func()) *time.Timer {
		h.mu.Lock()
		h.timeout = fn
		h.mu.Unlock()
		timer := time.NewTimer(time.Hour)
		timer.Stop()
		return timer
	}
	// No keepalive ticks unless a test asks for them, so the output holds
	// only what the test caused.
	h.session.keepalive = time.Hour
	h.session.unhandled = func(in tern.Input) {
		if r, ok := in.(*tern.Resize); ok {
			h.record(func() { h.resized = r.Cols })
		}
	}
	h.session.start()
	h.eventually("the hello", func() bool { return strings.Contains(h.out.peek(), "\x1b_tsp;q;") })
	t.Cleanup(func() { _ = h.session.Close() })
	return h
}

func (h *harness) record(fn func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	fn()
}

// eventually waits for cond, which the SDK's goroutines make true.
func (h *harness) eventually(what string, cond func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			h.t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// reply answers the hello and waits for the surface to open.
func (h *harness) reply(fields string) {
	h.t.Helper()
	if !h.session.HandleInput("\x1b_tsp;r;{\"r\":\"hello\",\"v\":1" + fields + "}\x1b\\") {
		h.t.Fatal("hello reply not taken")
	}
	h.eventually("the surface", func() bool { return strings.Contains(h.out.peek(), "\x1b_tsp;o;") })
}

// ack acknowledges every frame written so far and waits until the SDK took
// the ack.
func (h *harness) ack() {
	h.t.Helper()
	h.event(`{"ev":"ack","sf":"pig","s":` + itoa(h.session.out.frames.Load()) + `}`)
	h.synced()
}

// synced waits until the SDK handled everything HandleInput passed it: it
// reads input in order, so once the handlers' goroutine sees a resize sent
// last, the frames that acks released are written and the handlers of
// earlier events ran.
func (h *harness) synced() {
	h.t.Helper()
	h.syncs++
	cols := 1000 + h.syncs
	h.event(`{"ev":"resize","cols":` + itoa(int64(cols)) + `}`)
	h.eventually("the SDK's input loop", func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.resized == cols
	})
}

func (h *harness) event(body string) bool {
	return h.session.HandleInput("\x1b_tsp;e;" + body + "\x1b\\")
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// messages returns the TSP messages written since the last call.
func (h *harness) messages() []message {
	h.t.Helper()
	return parse(h.t, h.out.take())
}

// frames returns the frames written since the last call.
func (h *harness) frames() []message {
	h.t.Helper()
	var frames []message
	for _, m := range h.messages() {
		if m.verb == "f" {
			frames = append(frames, m)
		}
	}
	return frames
}

// ready returns a harness whose hello is answered (with ample credits),
// whose input loop runs, and whose regions went out with an empty first
// frame, so the next Apply goes out at once and holds only its own ops.
func ready(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.reply(`,"credits":64,"cols":137,"cell":{"w":8,"h":16}`)
	h.session.InputReady()
	h.apply()
	if got := h.frames(); len(got) != 1 {
		t.Fatalf("first frame = %#v", got)
	}
	return h
}

func (h *harness) apply(ops ...fe.Op) {
	h.t.Helper()
	if err := h.session.Apply(fe.Frame{Ops: ops}); err != nil {
		h.t.Fatal(err)
	}
}

// applyOps applies one frame and returns the ops of the frames it wrote.
func (h *harness) applyOps(ops ...fe.Op) []any {
	h.t.Helper()
	h.apply(ops...)
	var all []any
	for _, f := range h.frames() {
		all = append(all, f.body["ops"].([]any)...)
	}
	return all
}

// jsonOps normalizes expected ops to their wire form.
func jsonOps(t *testing.T, ops ...any) []any {
	t.Helper()
	data, err := json.Marshal(ops)
	if err != nil {
		t.Fatal(err)
	}
	var out []any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// opsOn are the ops that name id (as the node they add or change).
func opsOn(ops []any, id string) []any {
	var out []any
	for _, op := range ops {
		if o := op.([]any); len(o) > 1 && o[1] == id {
			out = append(out, op)
		}
	}
	return out
}

// node finds the node with id in the subtrees ops add.
func node(ops []any, id string) map[string]any {
	var find func(n map[string]any) map[string]any
	find = func(n map[string]any) map[string]any {
		if n["id"] == id {
			return n
		}
		children, _ := n["c"].([]any)
		for _, c := range children {
			if found := find(c.(map[string]any)); found != nil {
				return found
			}
		}
		return nil
	}
	for _, op := range ops {
		if o := op.([]any); o[0] == "add" {
			if found := find(o[4].(map[string]any)); found != nil {
				return found
			}
		}
	}
	return nil
}

func props(n map[string]any) map[string]any {
	p, _ := n["p"].(map[string]any)
	return p
}

// shown is the document Tern shows after ops: each node's props by id.
type shown map[string]map[string]any

// shownBy applies ops, as Tern does, to an empty document.
func shownBy(ops []any) shown {
	d := shown{}
	d.apply(ops)
	return d
}

func (d shown) apply(ops []any) {
	for _, op := range ops {
		o := op.([]any)
		switch o[0] {
		case "add":
			d.add(o[4].(map[string]any))
		case "set":
			p := d.node(o[1].(string))
			for k, v := range o[2].(map[string]any) {
				if v == nil {
					delete(p, k)
				} else {
					p[k] = v
				}
			}
		case "text":
			p := d.node(o[1].(string))
			if o[2] == "append" {
				old, _ := p["text"].(string)
				p["text"] = old + o[3].(string)
			} else {
				p["text"] = o[3]
			}
		case "del":
			id := o[1].(string)
			for k := range d {
				if k == id || strings.HasPrefix(k, id+".") {
					delete(d, k)
				}
			}
		}
	}
}

func (d shown) add(n map[string]any) {
	p := map[string]any{}
	maps.Copy(p, props(n))
	d[n["id"].(string)] = p
	children, _ := n["c"].([]any)
	for _, c := range children {
		d.add(c.(map[string]any))
	}
}

func (d shown) node(id string) map[string]any {
	if d[id] == nil {
		d[id] = map[string]any{}
	}
	return d[id]
}
