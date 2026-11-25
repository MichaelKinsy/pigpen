package ternbrowser

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/ternwire"
)

// fakeAnswer mirrors packages/coding-agent/test/tools/tern-fake-daemon.ts:12.
type fakeAnswer struct {
	OK    any
	HasOK bool
	Error *struct {
		Kind    string `json:"kind"`
		Message string `json:"message"`
	}
}

func okAns(v any) *fakeAnswer { return &fakeAnswer{OK: v, HasOK: true} }

func errAns(kind, msg string) *fakeAnswer {
	return &fakeAnswer{
		Error: &struct {
			Kind    string `json:"kind"`
			Message string `json:"message"`
		}{Kind: kind, Message: msg},
	}
}

type fakeRequest struct {
	ID int64
	Op map[string]any
}

type fakeDaemonOpts struct {
	HangUp  bool
	Welcome map[string]any
}

// fakeDaemon is a faithful Go port of packages/coding-agent/test/tools/tern-fake-daemon.ts.
// Every response uses the real Tern daemon's exact JSON wire shape:
//   - eval: { ok: { value: ... } }
//   - kit eval: { ok: { value: { value: ... } } }
//   - events: { ok: { events: [{ seq, type, ... }], next, dropped } }
//   - dom: { ok: <DomReply> }
type fakeDaemon struct {
	socketPath string
	ln         net.Listener
	dir        string

	mu       sync.Mutex
	hellos   [][]byte
	requests []fakeRequest
	owners   map[int64]net.Conn
	conns    map[net.Conn]struct{}
}

func startFakeDaemon(t *testing.T, handler func(op map[string]any, id int64) *fakeAnswer, opts fakeDaemonOpts) *fakeDaemon {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Unix socket fake daemon test is Unix-only")
	}
	dir, err := os.MkdirTemp("/tmp", "tbfd-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	sock := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		_ = os.RemoveAll(dir)
		t.Fatalf("listen unix: %v", err)
	}
	d := &fakeDaemon{
		socketPath: sock,
		ln:         ln,
		dir:        dir,
		owners:     make(map[int64]net.Conn),
		conns:      make(map[net.Conn]struct{}),
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			d.mu.Lock()
			d.conns[conn] = struct{}{}
			d.mu.Unlock()
			go d.serve(conn, handler, opts)
		}
	}()
	t.Cleanup(d.Close)
	return d
}

func (d *fakeDaemon) send(conn net.Conn, msg any) {
	f, err := ternwire.EncodeFrame(msg)
	if err != nil {
		return
	}
	_, _ = conn.Write(f)
}

func (d *fakeDaemon) Answer(id int64, ans *fakeAnswer) {
	if ans == nil {
		return
	}
	d.mu.Lock()
	conn := d.owners[id]
	d.mu.Unlock()
	if conn == nil {
		return
	}
	var body any
	if ans.Error != nil {
		body = map[string]any{"error": map[string]any{"kind": ans.Error.Kind, "message": ans.Error.Message}}
	} else {
		body = map[string]any{"ok": ans.OK}
	}
	d.send(conn, map[string]any{"id": id, "browser": body})
}

func (d *fakeDaemon) serve(conn net.Conn, handler func(op map[string]any, id int64) *fakeAnswer, opts fakeDaemonOpts) {
	defer func() {
		d.mu.Lock()
		delete(d.conns, conn)
		d.mu.Unlock()
		_ = conn.Close()
	}()
	for {
		var hdr [4]byte
		if _, err := io.ReadFull(conn, hdr[:]); err != nil {
			return
		}
		n := binary.LittleEndian.Uint32(hdr[:])
		payload := make([]byte, n)
		if _, err := io.ReadFull(conn, payload); err != nil {
			return
		}
		if opts.HangUp {
			return
		}
		var msg struct {
			Hello   json.RawMessage `json:"hello"`
			ID      *int64          `json:"id"`
			Browser map[string]any  `json:"browser"`
		}
		if err := json.Unmarshal(payload, &msg); err != nil {
			return
		}
		if len(msg.Hello) > 0 {
			d.mu.Lock()
			d.hellos = append(d.hellos, payload)
			d.mu.Unlock()
			welcome := opts.Welcome
			if welcome == nil {
				welcome = map[string]any{"version": 99}
			}
			d.send(conn, map[string]any{"welcome": welcome})
			d.send(conn, map[string]any{"id": 0, "output": map[string]any{"pane": 7}})
		} else if msg.Browser != nil && msg.ID != nil {
			id := *msg.ID
			d.mu.Lock()
			d.requests = append(d.requests, fakeRequest{ID: id, Op: msg.Browser})
			d.owners[id] = conn
			d.mu.Unlock()
			if handler != nil {
				d.Answer(id, handler(msg.Browser, id))
			}
		}
	}
}

func (d *fakeDaemon) Requests() []fakeRequest {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]fakeRequest, len(d.requests))
	copy(out, d.requests)
	return out
}

func (d *fakeDaemon) Ops() []string {
	reqs := d.Requests()
	out := make([]string, 0, len(reqs))
	for _, r := range reqs {
		if s, ok := r.Op["op"].(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func (d *fakeDaemon) Close() {
	_ = d.ln.Close()
	d.mu.Lock()
	for c := range d.conns {
		_ = c.Close()
	}
	d.mu.Unlock()
	_ = os.RemoveAll(d.dir)
}

func jsonArgs(vals ...any) []json.RawMessage {
	out := make([]json.RawMessage, len(vals))
	for i, v := range vals {
		b, _ := json.Marshal(v)
		out[i] = b
	}
	return out
}

type fakePageSpec struct {
	eventBatches  [][]map[string]any
	kit           map[string]func(args []any) any
	blankLoadPoll int
}

// startPage mirrors startPage() in packages/coding-agent/test/tools/browser-tern-tab.test.ts:38.
func startPage(t *testing.T, page *fakePageSpec) *fakeDaemon {
	t.Helper()
	var mu sync.Mutex
	seq := 0
	polls := 0
	return startFakeDaemon(t, func(op map[string]any, _ int64) *fakeAnswer {
		mu.Lock()
		defer mu.Unlock()
		switch op["op"] {
		case "open":
			return okAns(map[string]any{"block": 7, "url": "about:blank"})
		case "events":
			var batch []map[string]any
			if len(page.eventBatches) > 0 {
				batch = page.eventBatches[0]
				page.eventBatches = page.eventBatches[1:]
			}
			if polls == page.blankLoadPoll {
				blank := []map[string]any{
					{"type": "committed", "url": "about:blank"},
					{"type": "loaded", "url": "about:blank"},
				}
				batch = append(blank, batch...)
			}
			polls++
			stamped := make([]map[string]any, 0, len(batch))
			for _, ev := range batch {
				seq++
				cp := map[string]any{"seq": seq}
				for k, v := range ev {
					cp[k] = v
				}
				stamped = append(stamped, cp)
			}
			return okAns(map[string]any{"events": stamped, "next": seq, "dropped": 0})
		case "state":
			return okAns(map[string]any{
				"url":     "https://example.test/",
				"title":   "Example",
				"loading": true,
				"back":    false,
				"forward": false,
				"width":   800,
				"height":  600,
			})
		case "eval":
			fn, _ := op["function"].(string)
			args, _ := op["args"].([]any)
			if strings.Contains(fn, "__ompTernKit") && len(args) > 0 {
				method, _ := args[0].(string)
				stub := page.kit[method]
				if stub == nil {
					return errAns("js", "Error: kit method "+method+" not stubbed")
				}
				var margs []any
				if len(args) > 1 {
					margs, _ = args[1].([]any)
				}
				return okAns(map[string]any{"value": map[string]any{"value": stub(margs)}})
			}
			return okAns(map[string]any{})
		case "dialog":
			return okAns(map[string]any{})
		default:
			return okAns(map[string]any{})
		}
	}, fakeDaemonOpts{})
}

func captureMsg(msg map[string]any) map[string]any {
	full := map[string]any{"omp": "tern", "ts": 1}
	for k, v := range msg {
		full[k] = v
	}
	b, _ := json.Marshal(full)
	return map[string]any{
		"type":  "message",
		"world": "page",
		"main":  true,
		"url":   "https://example.test/",
		"body":  string(b),
	}
}
