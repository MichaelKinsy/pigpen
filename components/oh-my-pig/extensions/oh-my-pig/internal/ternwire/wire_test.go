package ternwire

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// FakeAnswer is { ok } or { error }, or nil to never answer.
// upstream: packages/coding-agent/test/tools/tern-fake-daemon.ts:12
type FakeAnswer struct {
	OK    any
	HasOK bool
	Error *struct {
		Kind    string `json:"kind"`
		Message string `json:"message"`
	}
}

func okAns(v any) *FakeAnswer { return &FakeAnswer{OK: v, HasOK: true} }

func errAns(kind, msg string) *FakeAnswer {
	return &FakeAnswer{
		Error: &struct {
			Kind    string `json:"kind"`
			Message string `json:"message"`
		}{Kind: kind, Message: msg},
	}
}

type fakeReq struct {
	ID int64
	Op map[string]any
}

type fakeDaemonOpts struct {
	HangUp  bool
	Welcome map[string]any
	Fork    func(req map[string]any, id int64) *FakeAnswer
}

// fakeDaemon mirrors packages/coding-agent/test/tools/tern-fake-daemon.ts.
type fakeDaemon struct {
	socketPath string
	ln         net.Listener
	dir        string

	mu       sync.Mutex
	hellos   [][]byte
	requests []fakeReq
	forks    []fakeReq
	owners   map[int64]net.Conn
	isFork   map[int64]bool
	conns    map[net.Conn]struct{}
}

func startFakeDaemon(t *testing.T, handler func(op map[string]any, id int64) *FakeAnswer, opts fakeDaemonOpts) *fakeDaemon {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Unix socket fake daemon test is Unix-only")
	}
	dir, err := os.MkdirTemp("/tmp", "tf-*")
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
		isFork:     make(map[int64]bool),
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
	f, err := EncodeFrame(msg)
	if err != nil {
		return
	}
	_, _ = conn.Write(f)
}

func (d *fakeDaemon) Answer(id int64, ans *FakeAnswer) {
	if ans == nil {
		return
	}
	d.mu.Lock()
	conn := d.owners[id]
	fork := d.isFork[id]
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
	ch := "browser"
	if fork {
		ch = "fork"
	}
	d.send(conn, map[string]any{"id": id, ch: body})
}

func (d *fakeDaemon) serve(conn net.Conn, handler func(op map[string]any, id int64) *FakeAnswer, opts fakeDaemonOpts) {
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
			Fork    map[string]any  `json:"fork"`
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
			d.requests = append(d.requests, fakeReq{ID: id, Op: msg.Browser})
			d.owners[id] = conn
			d.mu.Unlock()
			if handler != nil {
				d.Answer(id, handler(msg.Browser, id))
			}
		} else if msg.Fork != nil && msg.ID != nil {
			id := *msg.ID
			d.mu.Lock()
			d.forks = append(d.forks, fakeReq{ID: id, Op: msg.Fork})
			d.owners[id] = conn
			d.isFork[id] = true
			d.mu.Unlock()
			if opts.Fork != nil {
				d.Answer(id, opts.Fork(msg.Fork, id))
			}
		}
	}
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

func framePayload(p []byte) []byte {
	out := make([]byte, 4+len(p))
	binary.LittleEndian.PutUint32(out[:4], uint32(len(p)))
	copy(out[4:], p)
	return out
}

// upstream: browser-tern-wire.test.ts: "greets with a JSON hello and correlates answers by id"
func TestUpstreamWireHelloAndCorrelation(t *testing.T) {
	heldCh := make(chan int64, 1)
	d := startFakeDaemon(t, func(op map[string]any, id int64) *FakeAnswer {
		if op["op"] == "slow" {
			heldCh <- id
			return nil
		}
		return okAns(map[string]any{"echo": op["value"]})
	}, fakeDaemonOpts{})

	c, err := Dial(context.Background(), d.socketPath)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	slowDone := make(chan string, 1)
	go func() {
		var out string
		if err := c.Request(context.Background(), "browser", map[string]any{"op": "slow"}, &out); err != nil {
			t.Errorf("slow request: %v", err)
			return
		}
		slowDone <- out
	}()

	heldID := <-heldCh
	var fast map[string]any
	if err := c.Request(context.Background(), "browser", map[string]any{"op": "fast", "value": 7}, &fast); err != nil {
		t.Fatalf("fast request: %v", err)
	}
	if fast["echo"] != float64(7) {
		t.Fatalf("expected echo=7, got %v", fast)
	}
	d.Answer(heldID, okAns("late"))
	if got := <-slowDone; got != "late" {
		t.Fatalf("expected late, got %q", got)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.hellos) != 1 || string(d.hellos[0]) != `{"hello":{}}` {
		t.Fatalf("unexpected hello: %q", d.hellos)
	}
	if len(d.requests) != 2 || d.requests[0].ID != 1 || d.requests[1].ID != 2 {
		t.Fatalf("unexpected request ids: %+v", d.requests)
	}
}

// upstream: browser-tern-wire.test.ts: "skips members and message kinds it does not know and rejects frames that are not answers"
func TestUpstreamWireDecodeReply(t *testing.T) {
	r, err := DecodeReply([]byte(`{"welcome":{"version":99}}`), "Tern")
	if err != nil || r.Type != ReplyWelcome || len(r.Ops) != 0 {
		t.Fatalf("welcome version: %+v err=%v", r, err)
	}
	r, err = DecodeReply([]byte(`{"welcome":{"ops":["browser",7,"fork"]}}`), "Tern")
	if err != nil || r.Type != ReplyWelcome || len(r.Ops) != 2 || r.Ops[0] != "browser" || r.Ops[1] != "fork" {
		t.Fatalf("welcome ops: %+v err=%v", r, err)
	}
	r, err = DecodeReply([]byte(`{"id":3,"output":{}}`), "Tern")
	if err != nil || r.Type != ReplyOther {
		t.Fatalf("other: %+v err=%v", r, err)
	}
	r, err = DecodeReply([]byte(`{"id":3,"browser":{"ok":1},"extra":true}`), "Tern")
	if err != nil || r.Type != ReplyBrowser || r.ID != 3 {
		t.Fatalf("browser extra: %+v err=%v", r, err)
	}

	badPayloads := []struct {
		payload []byte
		wantMsg string
	}{
		{[]byte{0x0a, 0x04}, "not JSON"},
		{[]byte(`[1,2]`), "not a JSON object"},
		{[]byte(`{"browser":{"ok":1}}`), "without an id"},
		{[]byte(`{"id":null,"browser":{"ok":1}}`), "without an id"},
		{[]byte{0x7b, 0xff, 0x7d}, "not JSON"},
	}
	for _, tc := range badPayloads {
		_, err := DecodeReply(tc.payload, "Tern")
		var te *Error
		if !errors.As(err, &te) || te.Kind != ErrProtocol || !strings.Contains(te.Message, tc.wantMsg) {
			t.Fatalf("payload %x: expected protocol containing %q, got %v", tc.payload, tc.wantMsg, err)
		}
	}
}

// upstream: browser-tern-wire.test.ts: "turns an error envelope into a TernError carrying its kind"
func TestUpstreamWireErrorEnvelope(t *testing.T) {
	d := startFakeDaemon(t, func(map[string]any, int64) *FakeAnswer {
		return errAns("no_window", "no Tern window is open")
	}, fakeDaemonOpts{})
	c, err := Dial(context.Background(), d.socketPath)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	err = c.Request(context.Background(), "browser", map[string]any{"op": "open"}, nil)
	var te *Error
	if !errors.As(err, &te) || te.Kind != ErrNoWindow || !strings.Contains(te.Message, "no Tern window is open") || !IsUnavailable(err) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// upstream: browser-tern-wire.test.ts: "treats a Tern from before the JSON protocol, which hangs up on the hello, as unavailable"
func TestUpstreamWireHangUpBeforeGreeting(t *testing.T) {
	d := startFakeDaemon(t, nil, fakeDaemonOpts{HangUp: true})
	_, err := Dial(context.Background(), d.socketPath)
	var te *Error
	if !errors.As(err, &te) || te.Kind != ErrConnect || !strings.Contains(te.Message, "without omp's JSON protocol") || !IsUnavailable(err) {
		t.Fatalf("unexpected hangup error: %v", err)
	}
}

// upstream: browser-tern-wire.test.ts: "fails to connect to a missing socket with an unavailable error"
func TestUpstreamWireMissingSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only")
	}
	_, err := Dial(context.Background(), "/tmp/omp-tern-missing-daemon.sock")
	var te *Error
	if !errors.As(err, &te) || te.Kind != ErrConnect || !IsUnavailable(err) {
		t.Fatalf("unexpected missing socket error: %v", err)
	}
}

// upstream: browser-tern-wire.test.ts: "rejects an op whose fields are not JSON without leaving it pending"
func TestUpstreamWireInvalidJSONRequest(t *testing.T) {
	d := startFakeDaemon(t, func(map[string]any, int64) *FakeAnswer { return okAns("fine") }, fakeDaemonOpts{})
	c, err := Dial(context.Background(), d.socketPath)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	err = c.Request(context.Background(), "browser", map[string]any{"op": "eval", "bad": math.NaN()}, nil)
	var te *Error
	if !errors.As(err, &te) || te.Kind != ErrInvalid {
		t.Fatalf("expected invalid error, got %v", err)
	}
	d.mu.Lock()
	reqLen := len(d.requests)
	d.mu.Unlock()
	if reqLen != 0 {
		t.Fatalf("expected 0 daemon requests, got %d", reqLen)
	}
	var out string
	if err := c.Request(context.Background(), "browser", map[string]any{"op": "state"}, &out); err != nil || out != "fine" {
		t.Fatalf("connection unusable after invalid request: err=%v out=%q", err, out)
	}
}

// upstream: browser-tern-wire.test.ts: "hands a late answer of an abandoned op to its onLateAnswer hook"
// Also verifies S6: calling client.Request from inside onLateAnswer must not deadlock the reader goroutine.
func TestUpstreamWireLateAnswerAndNonBlockingClose(t *testing.T) {
	receivedCh := make(chan int64, 1)
	closedBlockCh := make(chan float64, 1)
	d := startFakeDaemon(t, func(op map[string]any, id int64) *FakeAnswer {
		if op["op"] == "open" {
			receivedCh <- id
			return nil
		}
		if op["op"] == "close" {
			if blk, ok := op["block"].(float64); ok {
				closedBlockCh <- blk
			}
			return okAns(map[string]any{})
		}
		return okAns(map[string]any{})
	}, fakeDaemonOpts{})

	c, err := Dial(context.Background(), d.socketPath)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	closeDoneCh := make(chan error, 1)
	reqCtx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- c.RequestWithOptions(reqCtx, "browser", map[string]any{"op": "open"}, nil, RequestOptions{
			OnLateAnswer: func(raw json.RawMessage) {
				var res struct {
					Block int `json:"block"`
				}
				if json.Unmarshal(raw, &res) == nil && res.Block != 0 {
					// Must succeed promptly without stalling readLoop (S6).
					closeCtx, done := context.WithTimeout(context.Background(), 2*time.Second)
					defer done()
					closeDoneCh <- c.Request(closeCtx, "browser", map[string]any{"op": "close", "block": res.Block}, nil)
				}
			},
		})
	}()

	id := <-receivedCh
	cancel()
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	d.Answer(id, okAns(map[string]any{"block": 12}))

	select {
	case blk := <-closedBlockCh:
		if blk != 12 {
			t.Fatalf("expected close block 12, got %v", blk)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for late close (readLoop stalled?)")
	}
	select {
	case err := <-closeDoneCh:
		if err != nil {
			t.Fatalf("late close failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for late close answer")
	}
}

// upstream: browser-tern-wire.test.ts: "assembles frames split across many chunks, and several frames in one chunk"
func TestUpstreamWireFrameReaderChunks(t *testing.T) {
	reader := NewFrameReader("Tern")
	big := make([]byte, 100_000)
	for i := range big {
		big[i] = byte(i % 251)
	}
	raw := framePayload(big)
	var payloads [][]byte
	for off := 0; off < len(raw); off += 777 {
		end := min(off+777, len(raw))
		ps, err := reader.Push(raw[off:end])
		if err != nil {
			t.Fatalf("Push: %v", err)
		}
		payloads = append(payloads, ps...)
	}
	if len(payloads) != 1 || !bytes.Equal(payloads[0], big) {
		t.Fatalf("big payload mismatch: len=%d", len(payloads))
	}
	both := append(framePayload([]byte{1, 2}), framePayload([]byte{3})...)
	ps, err := reader.Push(both)
	if err != nil || len(ps) != 2 || !bytes.Equal(ps[0], []byte{1, 2}) || !bytes.Equal(ps[1], []byte{3}) {
		t.Fatalf("multi-frame chunk mismatch: %+v err=%v", ps, err)
	}
}

// upstream: browser-tern-wire.test.ts: "reports fork unsupported by a Tern whose welcome lists no ops"
func TestUpstreamWireForkUnsupportedWhenUnlisted(t *testing.T) {
	d := startFakeDaemon(t, func(map[string]any, int64) *FakeAnswer { return okAns(map[string]any{}) }, fakeDaemonOpts{})
	c, err := Dial(context.Background(), d.socketPath)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	if c.Supports("fork") {
		t.Fatal("expected Supports(fork) == false")
	}
}

// upstream: browser-tern-wire.test.ts: "sends a fork to a Tern that lists it and resolves the new pane's block"
func TestUpstreamWireForkSuccess(t *testing.T) {
	d := startFakeDaemon(t, nil, fakeDaemonOpts{
		Welcome: map[string]any{"ops": []string{"browser", "fork"}},
		Fork:    func(map[string]any, int64) *FakeAnswer { return okAns(map[string]any{"block": 9}) },
	})
	c, err := Dial(context.Background(), d.socketPath)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	if !c.Supports("fork") {
		t.Fatal("expected Supports(fork) == true")
	}
	blk, err := c.Fork(context.Background(), 5, "")
	if err != nil || blk != 9 {
		t.Fatalf("Fork: blk=%d err=%v", blk, err)
	}
}

// upstream: browser-tern-wire.test.ts: "turns a fork error answer into a TernError carrying its kind"
func TestUpstreamWireForkError(t *testing.T) {
	d := startFakeDaemon(t, nil, fakeDaemonOpts{
		Welcome: map[string]any{"ops": []string{"browser", "fork"}},
		Fork: func(map[string]any, int64) *FakeAnswer {
			return errAns("not_agent", "block 5 reported no session file")
		},
	})
	c, err := Dial(context.Background(), d.socketPath)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	_, err = c.Fork(context.Background(), 5, "")
	var te *Error
	if !errors.As(err, &te) || te.Kind != ErrNotAgent {
		t.Fatalf("expected not_agent, got %v", err)
	}
}

// upstream: browser-tern-wire.test.ts: "rejects pending ops on close, on timeout and on abort"
func TestUpstreamWireCloseTimeoutAbort(t *testing.T) {
	d := startFakeDaemon(t, func(map[string]any, int64) *FakeAnswer { return nil }, fakeDaemonOpts{})
	c, err := Dial(context.Background(), d.socketPath)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	err = c.RequestWithOptions(context.Background(), "browser", map[string]any{"op": "state"}, nil, RequestOptions{
		Timeout: 30 * time.Millisecond,
	})
	var te *Error
	if !errors.As(err, &te) || te.Kind != ErrTimeout {
		t.Fatalf("expected timeout, got %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(15 * time.Millisecond)
		cancel()
	}()
	if err := c.Request(ctx, "browser", map[string]any{"op": "state"}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	pendingErr := make(chan error, 1)
	go func() {
		pendingErr <- c.Request(context.Background(), "browser", map[string]any{"op": "state"}, nil)
	}()
	time.Sleep(15 * time.Millisecond)
	_ = c.Close()
	err = <-pendingErr
	if !errors.As(err, &te) || te.Kind != ErrClosed || IsUnavailable(err) {
		t.Fatalf("expected closed (not unavailable), got %v", err)
	}
}

// upstream: browser-tern-kind.test.ts: "needs both the daemon socket and a numeric pane" & "lets PI_BROWSER_TERN override the setting in both directions"
func TestUpstreamResolveKindAndParseFlag(t *testing.T) {
	env := map[string]string{"TERN_PANE_SOCKET": "/tmp/tern.sock", "TERN_PANE": "42"}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }

	sock, pane, ok := ResolveKind(true, lookup)
	if !ok || sock != "/tmp/tern.sock" || pane != 42 {
		t.Fatalf("ResolveKind default: ok=%v sock=%q pane=%d", ok, sock, pane)
	}
	env["PI_BROWSER_TERN"] = "0"
	if _, _, ok := ResolveKind(true, lookup); ok {
		t.Fatal("PI_BROWSER_TERN=0 should disable")
	}
	env["PI_BROWSER_TERN"] = "n"
	if _, _, ok := ResolveKind(true, lookup); ok {
		t.Fatal("PI_BROWSER_TERN=n should disable per upstream parseFlag")
	}
	env["PI_BROWSER_TERN"] = "2"
	if _, _, ok := ResolveKind(true, lookup); ok {

		t.Fatal("PI_BROWSER_TERN=2 should disable per upstream parseFlag")
	}
	env["PI_BROWSER_TERN"] = "y"
	if _, _, ok := ResolveKind(false, lookup); !ok {
		t.Fatal("PI_BROWSER_TERN=y should enable per upstream parseFlag")
	}
	env["PI_BROWSER_TERN"] = "1"
	if _, _, ok := ResolveKind(false, lookup); !ok {
		t.Fatal("PI_BROWSER_TERN=1 should enable")
	}
	env["TERN_PANE"] = "pane-1"
	if _, _, ok := ResolveKind(true, lookup); ok {
		t.Fatal("non-numeric TERN_PANE should fail")
	}
}

// N5: a timed-out or failed write must fail the whole client so a half-written frame
// cannot leave a corrupted connection open for subsequent requests.
func TestWriteErrorFailsClientN5(t *testing.T) {
	cConn, sConn := net.Pipe()
	defer sConn.Close()

	// Complete hello/welcome, then stop reading from sConn so a write blocks and times out.
	go func() {
		var hdr [4]byte
		if _, err := io.ReadFull(sConn, hdr[:]); err != nil {
			return
		}
		n := binary.LittleEndian.Uint32(hdr[:])
		buf := make([]byte, n)
		if _, err := io.ReadFull(sConn, buf); err != nil {
			return
		}
		welcome, _ := EncodeFrame(map[string]any{"welcome": map[string]any{"ops": []string{"browser"}}})
		_, _ = sConn.Write(welcome)
		// Intentionally never read again so net.Pipe write blocks until write deadline.
	}()

	tr := &pipeTransport{conn: cConn}
	client, err := DialTransport(context.Background(), tr)
	if err != nil {
		t.Fatalf("DialTransport: %v", err)
	}
	defer client.Close()

	err = client.RequestWithOptions(context.Background(), "browser", map[string]any{"op": "state"}, nil, RequestOptions{
		Timeout: 40 * time.Millisecond,
	})
	var te *Error
	if !errors.As(err, &te) || te.Kind != ErrTimeout {
		t.Fatalf("expected timeout on blocked write, got %v", err)
	}
	if client.Connected() {
		t.Fatal("N5 violation: client stayed Connected() == true after write timeout")
	}
	// Subsequent request must immediately fail with ErrClosed.
	err2 := client.Request(context.Background(), "browser", map[string]any{"op": "state"}, nil)
	if !errors.As(err2, &te) || te.Kind != ErrClosed {
		t.Fatalf("N5 violation: expected ErrClosed after failed write, got %v", err2)
	}
}

type pipeTransport struct {
	conn net.Conn
}

func (p *pipeTransport) Product() string                             { return "Tern" }
func (p *pipeTransport) Peer() string                                { return "pipe" }
func (p *pipeTransport) WaitsForEvents() bool                        { return false }
func (p *pipeTransport) GreetingHint() string                        { return "" }
func (p *pipeTransport) Connect(_ context.Context) (net.Conn, error) { return p.conn, nil }

// Gated real-Tern daemon integration test (C5): set OMP_TERN_DAEMON_TEST=1 to run.
func TestRealTernHeadless(t *testing.T) {
	if os.Getenv("OMP_TERN_DAEMON_TEST") != "1" {
		t.Skip("set OMP_TERN_DAEMON_TEST=1 to run real isolated tern daemon test")
	}
	if runtime.GOOS == "windows" {
		t.Skip("headless tern test is Unix-only")
	}
	ternBin, err := exec.LookPath("tern")
	if err != nil {
		t.Skip("tern binary not found on PATH")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, `<!doctype html><html><head><title>TernWire Test</title></head><body><h1>Hello</h1></body></html>`)
	}))
	defer srv.Close()

	root, err := os.MkdirTemp("/tmp", "tw-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(root)

	homeDir := filepath.Join(root, "home")
	cfgDir := filepath.Join(root, "cfg")
	daemonSock := filepath.Join(root, "d.sock")
	for _, d := range []string{homeDir, cfgDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	cleanEnv := func() []string {
		strip := map[string]bool{
			"HOME": true, "TERN_ACP": true, "TERN_BLOB_DIR": true, "TERN_COMPLETE": true,
			"TERN_IDENTITY": true, "TERN_LENSES": true, "TERN_PANE": true,
			"TERN_PANE_SOCKET": true, "TERN_WINDOW_KEY": true, "TERN_WINDOW_SOCKET": true,
			"TERM_PROGRAM": true, "TERM_PROGRAM_VERSION": true,
			"TMUX": true, "TMUX_PANE": true, "STY": true, "ZELLIJ": true,
		}
		var env []string
		for _, kv := range os.Environ() {
			k, _, _ := strings.Cut(kv, "=")
			if !strip[k] {
				env = append(env, kv)
			}
		}
		env = append(env,
			"HOME="+homeDir,
			"TERN_CONFIG_DIR="+cfgDir,
			"TERN_DAEMON_SOCKET="+daemonSock,
		)
		return env
	}

	daemonCmd := exec.Command(ternBin, "daemon", "--socket", daemonSock)
	daemonCmd.Env = cleanEnv()
	if err := daemonCmd.Start(); err != nil {
		t.Fatalf("start tern daemon: %v", err)
	}
	defer func() {
		stop := exec.Command(ternBin, "daemon", "stop", "--socket", daemonSock)
		stop.Env = cleanEnv()
		_ = stop.Run()
		done := make(chan error, 1)
		go func() { done <- daemonCmd.Wait() }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = daemonCmd.Process.Kill()
			<-done
		}
	}()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(daemonSock); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := os.Stat(daemonSock); err != nil {
		t.Fatalf("isolated tern daemon did not create socket at %s", daemonSock)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	client, err := Dial(ctx, daemonSock)
	if err != nil {
		t.Fatalf("Dial real tern daemon: %v", err)
	}
	defer client.Close()

	if !client.Connected() {
		t.Fatal("expected client.Connected() == true")
	}
	t.Logf("hello/welcome succeeded; ops=%v", client.Ops())

	err = client.Request(ctx, "browser", map[string]any{"op": "open", "owner": 1, "url": srv.URL}, nil)
	var te *Error
	if errors.As(err, &te) && (te.Kind == ErrNoWindow || te.Kind == ErrUnsupported) {
		t.Logf("real tern daemon answered browser open with %s (IsUnavailable=%v): %s", te.Kind, IsUnavailable(err), te.Message)
		return
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
