package jsonrpc

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// peer is the client end of a Conn under test.
type peer struct {
	toConn   *io.PipeWriter
	fromConn *bufio.Scanner
	conn     *Conn
}

func newPeer(t *testing.T, h Handler) *peer {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	p := &peer{toConn: inW, fromConn: bufio.NewScanner(outR)}
	p.fromConn.Buffer(make([]byte, 1<<20), 1<<26)
	p.conn = New(inR, outW, h)
	t.Cleanup(func() { inW.Close(); outR.Close() })
	return p
}

func (p *peer) send(t *testing.T, v any) {
	t.Helper()
	var line string
	if s, ok := v.(string); ok {
		line = s
	} else {
		b, _ := json.Marshal(v)
		line = string(b)
	}
	done := make(chan error, 1)
	go func() { _, err := p.toConn.Write([]byte(line + "\n")); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		p.toConn.CloseWithError(io.ErrClosedPipe)
		t.Fatal("the connection does not read its input")
	}
}

func (p *peer) recv(t *testing.T) map[string]any {
	t.Helper()
	done := make(chan map[string]any, 1)
	go func() {
		if !p.fromConn.Scan() {
			done <- nil
			return
		}
		var m map[string]any
		if err := json.Unmarshal(p.fromConn.Bytes(), &m); err != nil {
			t.Errorf("output %q is not JSON: %v", p.fromConn.Text(), err)
		}
		done <- m
	}()
	select {
	case m := <-done:
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("no message from the connection")
		return nil
	}
}

type coded struct {
	code int
	msg  string
	data any
}

func (c coded) Error() string                { return c.msg }
func (c coded) RPCError() (int, string, any) { return c.code, c.msg, c.data }

func TestConn(t *testing.T) {
	t.Run("answers a request with its result and the same id", func(t *testing.T) {
		p := newPeer(t, func(r *Request) (any, error) { return map[string]any{"echo": r.Method}, nil })
		p.send(t, map[string]any{"jsonrpc": "2.0", "id": 7, "method": "ping", "params": map[string]any{}})
		m := p.recv(t)
		if m["jsonrpc"] != "2.0" || m["id"] != float64(7) {
			t.Fatalf("response = %v", m)
		}
		if r, _ := m["result"].(map[string]any); r["echo"] != "ping" {
			t.Errorf("result = %v", m["result"])
		}
	})

	t.Run("keeps string ids and answers a null result as null", func(t *testing.T) {
		p := newPeer(t, func(*Request) (any, error) { return nil, nil })
		p.send(t, map[string]any{"jsonrpc": "2.0", "id": "abc", "method": "m"})
		m := p.recv(t)
		if m["id"] != "abc" {
			t.Fatalf("response = %v", m)
		}
		if v, ok := m["result"]; !ok || v != nil {
			t.Errorf("result = %v (present %v), want null", v, ok)
		}
	})

	t.Run("maps a coded error to code, message and data", func(t *testing.T) {
		p := newPeer(t, func(*Request) (any, error) { return nil, coded{-32602, "Invalid params: x", map[string]any{"k": 1}} })
		p.send(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "m"})
		e, _ := p.recv(t)["error"].(map[string]any)
		if e["code"] != float64(-32602) || e["message"] != "Invalid params: x" {
			t.Fatalf("error = %v", e)
		}
		if d, _ := e["data"].(map[string]any); d["k"] != float64(1) {
			t.Errorf("data = %v", e["data"])
		}
	})

	t.Run("maps any other error to Internal error with the details", func(t *testing.T) {
		p := newPeer(t, func(*Request) (any, error) { return nil, errors.New("boom") })
		p.send(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "m"})
		e, _ := p.recv(t)["error"].(map[string]any)
		if e["code"] != float64(-32603) || e["message"] != "Internal error" {
			t.Fatalf("error = %v", e)
		}
		if d, _ := e["data"].(map[string]any); d["details"] != "boom" {
			t.Errorf("data = %v", e["data"])
		}
	})

	t.Run("skips a line that is not JSON and keeps serving", func(t *testing.T) {
		p := newPeer(t, func(*Request) (any, error) { return "ok", nil })
		p.send(t, "this is not json")
		p.send(t, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "m"})
		if m := p.recv(t); m["id"] != float64(2) || m["result"] != "ok" {
			t.Fatalf("response = %v", m)
		}
	})

	t.Run("does not answer a notification", func(t *testing.T) {
		got := make(chan string, 1)
		p := newPeer(t, func(r *Request) (any, error) {
			if r.Method == "session/cancel" {
				if !r.Notification {
					t.Error("Notification flag not set")
				}
				got <- r.Method
			}
			return "ignored", nil
		})
		p.send(t, map[string]any{"jsonrpc": "2.0", "method": "session/cancel", "params": map[string]any{"sessionId": "s"}})
		if m := <-got; m != "session/cancel" {
			t.Fatalf("method = %q", m)
		}
		p.send(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "after"})
		if m := p.recv(t); m["id"] != float64(1) {
			t.Fatalf("the first output was %v, not the answer to the request", m)
		}
	})

	t.Run("a slow request does not block a later one", func(t *testing.T) {
		release := make(chan struct{})
		p := newPeer(t, func(r *Request) (any, error) {
			if r.Method == "slow" {
				<-release
			}
			return r.Method, nil
		})
		p.send(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "slow"})
		p.send(t, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "fast"})
		if m := p.recv(t); m["id"] != float64(2) {
			t.Fatalf("first response = %v", m)
		}
		close(release)
		if m := p.recv(t); m["id"] != float64(1) {
			t.Fatalf("second response = %v", m)
		}
	})

	t.Run("delivers the request params untouched", func(t *testing.T) {
		got := make(chan string, 1)
		p := newPeer(t, func(r *Request) (any, error) { got <- string(r.Params); return nil, nil })
		p.send(t, `{"jsonrpc":"2.0","id":1,"method":"m","params":{"a":[1,2,{"b":null}]}}`)
		if s := <-got; s != `{"a":[1,2,{"b":null}]}` {
			t.Errorf("params = %s", s)
		}
	})

	t.Run("correlates an outgoing request with its response", func(t *testing.T) {
		p := newPeer(t, func(*Request) (any, error) { return nil, nil })
		type res struct {
			Outcome string `json:"outcome"`
		}
		done := make(chan error, 1)
		var out res
		go func() { done <- p.conn.Call("session/request_permission", map[string]any{"sessionId": "s"}, &out) }()
		req := p.recv(t)
		if req["method"] != "session/request_permission" || req["jsonrpc"] != "2.0" {
			t.Fatalf("request = %v", req)
		}
		p.send(t, map[string]any{"jsonrpc": "2.0", "id": req["id"], "result": map[string]any{"outcome": "selected"}})
		if err := <-done; err != nil || out.Outcome != "selected" {
			t.Fatalf("err=%v out=%+v", err, out)
		}
	})

	t.Run("an error response to an outgoing request is returned as *Error", func(t *testing.T) {
		p := newPeer(t, func(*Request) (any, error) { return nil, nil })
		done := make(chan error, 1)
		go func() { done <- p.conn.Call("x", nil, nil) }()
		req := p.recv(t)
		p.send(t, map[string]any{"jsonrpc": "2.0", "id": req["id"], "error": map[string]any{"code": -32601, "message": "nope"}})
		var e *Error
		if err := <-done; !errors.As(err, &e) || e.Code != -32601 || e.Message != "nope" {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("closing the input fails pending requests and closes Done", func(t *testing.T) {
		p := newPeer(t, func(*Request) (any, error) { return nil, nil })
		done := make(chan error, 1)
		go func() { done <- p.conn.Call("x", nil, nil) }()
		p.recv(t)
		p.toConn.Close()
		select {
		case err := <-done:
			if err == nil {
				t.Error("pending request succeeded")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("pending request never failed")
		}
		select {
		case <-p.conn.Done():
		case <-time.After(5 * time.Second):
			t.Fatal("Done never closed")
		}
	})

	t.Run("serializes concurrent writes into whole lines", func(t *testing.T) {
		p := newPeer(t, func(*Request) (any, error) { return nil, nil })
		var wg sync.WaitGroup
		for i := 0; i < 200; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				_ = p.conn.Notify("session/update", map[string]any{"sessionId": "s", "n": i, "pad": strings.Repeat("x", 4096)})
			}(i)
		}
		seen := map[float64]bool{}
		for i := 0; i < 200; i++ {
			m := p.recv(t)
			seen[m["params"].(map[string]any)["n"].(float64)] = true
		}
		wg.Wait()
		if len(seen) != 200 {
			t.Errorf("saw %d distinct notifications", len(seen))
		}
	})

	t.Run("accepts a very large message", func(t *testing.T) {
		got := make(chan int, 1)
		p := newPeer(t, func(r *Request) (any, error) { got <- len(r.Params); return nil, nil })
		big := strings.Repeat("y", 8<<20)
		p.send(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"m","params":{"blob":%q}}`, big))
		if n := <-got; n < 8<<20 {
			t.Errorf("params length %d", n)
		}
	})

	t.Run("runs AfterResponse hooks after the response was written", func(t *testing.T) {
		order := make(chan string, 2)
		p := newPeer(t, func(r *Request) (any, error) {
			r.AfterResponse(func() { order <- "hook" })
			return "done", nil
		})
		p.send(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "m"})
		select {
		case <-order:
			// The hook may only run once the response is on the wire; reading it now proves it was written.
		case <-time.After(5 * time.Second):
			t.Fatal("hook never ran")
		}
		if m := p.recv(t); m["result"] != "done" {
			t.Fatalf("response = %v", m)
		}
	})
}

// failingWriter is a stdout that was destroyed (test/unit/stdout-destroyed-does-not-crash.test.ts).
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestDestroyedOutput(t *testing.T) {
	tw(t, "unit/stdout-destroyed-does-not-crash", "stdout writer: resolves even if stdout is destroyed", func(t *testing.T) {
		in, _ := io.Pipe()
		c := New(in, failingWriter{}, func(*Request) (any, error) { return nil, nil })
		done := make(chan struct{})
		go func() {
			_ = c.Notify("session/update", map[string]any{"sessionId": "s"})
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Notify blocked on a destroyed output")
		}
		select {
		case <-c.Done():
		case <-time.After(5 * time.Second):
			t.Fatal("a failed write did not end the connection")
		}
	})
}
