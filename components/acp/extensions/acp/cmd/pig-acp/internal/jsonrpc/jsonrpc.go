// Package jsonrpc is newline-delimited JSON-RPC 2.0 over a pair of streams, the transport
// ACP uses on stdio (ndJsonStream and Connection of @agentclientprotocol/sdk).
package jsonrpc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// Error is a JSON-RPC error object.
type Error struct {
	Code    int
	Message string
	Data    any
}

func (e *Error) Error() string { return e.Message }

// Coded is implemented by errors that carry a JSON-RPC code (acp.RequestError does).
type Coded interface {
	error
	RPCError() (code int, message string, data any)
}

// Request is an incoming request or notification.
type Request struct {
	Method string
	Params json.RawMessage
	// Notification is true when the message has no id.
	Notification bool
	after        []func()
}

// AfterResponse runs fn once the response to this request has been queued for writing, so
// anything fn sends follows the response on the wire.
func (r *Request) AfterResponse(fn func()) { r.after = append(r.after, fn) }

// Handler serves one incoming message. For a notification the result is dropped.
type Handler func(req *Request) (any, error)

// maxLine bounds one message (an embedded image can be many megabytes).
const maxLine = 64 << 20

// Conn is one JSON-RPC connection. Outgoing messages go through an unbounded FIFO queue and a
// single writer goroutine, so a slow or stalled peer never blocks the agent, lines never
// interleave, and the order in which messages were queued is the order on the wire.
type Conn struct {
	h Handler

	mu      sync.Mutex
	queue   [][]byte
	wake    chan struct{}
	failed  bool
	writing bool
	active  int
	nextID  int64
	pending map[string]chan message

	done     chan struct{}
	doneOnce sync.Once
}

type message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *wireError      `json:"error,omitempty"`
}

type wireError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// ErrClosed is returned when the connection can no longer carry messages.
var ErrClosed = errors.New("jsonrpc: connection closed")

// New starts reading from r and writing to w; every incoming message runs h on its own goroutine.
func New(r io.Reader, w io.Writer, h Handler) *Conn {
	c := &Conn{h: h, wake: make(chan struct{}, 1), pending: map[string]chan message{}, done: make(chan struct{})}
	go c.writeLoop(w)
	go c.readLoop(r)
	return c
}

func (c *Conn) closeDone() {
	c.doneOnce.Do(func() {
		close(c.done)
		c.mu.Lock()
		for id, ch := range c.pending {
			close(ch)
			delete(c.pending, id)
		}
		c.mu.Unlock()
	})
}

func (c *Conn) writeLoop(w io.Writer) {
	for {
		c.mu.Lock()
		if len(c.queue) == 0 {
			c.mu.Unlock()
			<-c.wake
			continue
		}
		line := c.queue[0]
		c.queue = c.queue[1:]
		c.writing = true
		c.mu.Unlock()
		_, err := w.Write(line)
		c.mu.Lock()
		c.writing = false
		c.mu.Unlock()
		if err != nil {
			c.mu.Lock()
			c.failed = true
			c.queue = nil
			c.mu.Unlock()
			c.closeDone()
			return
		}
	}
}

func (c *Conn) enqueue(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.mu.Lock()
	if c.failed {
		c.mu.Unlock()
		return ErrClosed
	}
	c.queue = append(c.queue, append(b, '\n'))
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
	return nil
}

func (c *Conn) readLoop(r io.Reader) {
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := readLine(br)
		if len(bytes.TrimSpace(line)) > 0 {
			var m message
			if json.Unmarshal(line, &m) == nil {
				c.dispatch(m)
			}
		}
		if err != nil {
			c.closeDone()
			return
		}
	}
}

func readLine(br *bufio.Reader) ([]byte, error) {
	var out []byte
	for {
		part, err := br.ReadSlice('\n')
		out = append(out, part...)
		if len(out) > maxLine {
			return nil, io.ErrShortBuffer
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		return out, err
	}
}

func (c *Conn) dispatch(m message) {
	if m.Method == "" {
		if len(m.ID) == 0 {
			return
		}
		c.mu.Lock()
		ch, ok := c.pending[string(m.ID)]
		delete(c.pending, string(m.ID))
		c.mu.Unlock()
		if ok {
			ch <- m
		}
		return
	}
	notification := len(m.ID) == 0
	c.mu.Lock()
	c.active++
	c.mu.Unlock()
	go c.serve(m, notification)
}

func (c *Conn) serve(m message, notification bool) {
	defer func() {
		c.mu.Lock()
		c.active--
		c.mu.Unlock()
	}()
	req := &Request{Method: m.Method, Params: m.Params, Notification: notification}
	res, err := c.call(req)
	if notification {
		return
	}
	resp := map[string]any{"jsonrpc": "2.0", "id": m.ID}
	if err != nil {
		resp["error"] = toWireError(err)
	} else {
		resp["result"] = res
	}
	_ = c.enqueue(resp)
	for _, fn := range req.after {
		fn()
	}
}

func (c *Conn) call(req *Request) (res any, err error) {
	defer func() {
		if p := recover(); p != nil {
			res, err = nil, fmt.Errorf("panic: %v", p)
		}
	}()
	return c.h(req)
}

func toWireError(err error) *wireError {
	var coded Coded
	if errors.As(err, &coded) {
		code, msg, data := coded.RPCError()
		return &wireError{Code: code, Message: msg, Data: data}
	}
	return &wireError{Code: -32603, Message: "Internal error", Data: map[string]any{"details": err.Error()}}
}

// Call sends a request and decodes its result into result (may be nil).
func (c *Conn) Call(method string, params, result any) error {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	key := fmt.Sprint(id)
	ch := make(chan message, 1)
	select {
	case <-c.done:
		c.mu.Unlock()
		return ErrClosed
	default:
	}
	c.pending[key] = ch
	c.mu.Unlock()
	if err := c.enqueue(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		c.mu.Lock()
		delete(c.pending, key)
		c.mu.Unlock()
		return err
	}
	m, ok := <-ch
	if !ok {
		return ErrClosed
	}
	if m.Error != nil {
		return &Error{Code: m.Error.Code, Message: m.Error.Message, Data: m.Error.Data}
	}
	if result != nil && len(m.Result) > 0 {
		return json.Unmarshal(m.Result, result)
	}
	return nil
}

// Notify sends a notification.
func (c *Conn) Notify(method string, params any) error {
	return c.enqueue(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

// Done is closed when the input ends or a write fails for good.
func (c *Conn) Done() <-chan struct{} { return c.done }

// Drain waits until every request being served has been answered and every queued message has been
// written, or until timeout. It lets a client that closes its output right after sending a request
// (a one-shot script) still receive the answer.
func (c *Conn) Drain(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		idle := c.active == 0 && len(c.queue) == 0 && !c.writing || c.failed
		c.mu.Unlock()
		if idle {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}
