package ternwire

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"sync"
	"time"
)

const (
	connectTimeout        = 10 * time.Second
	defaultRequestTimeout = 30 * time.Second
	abandonedLimit        = 256
)

// Transport abstracts the byte stream a Client speaks frames over.
// upstream: packages/coding-agent/src/tools/browser/tern/wire.ts:257
type Transport interface {
	Product() string
	Peer() string
	WaitsForEvents() bool
	GreetingHint() string
	Connect(ctx context.Context) (net.Conn, error)
}

// RequestOptions configures a single request.
// upstream: packages/coding-agent/src/tools/browser/tern/wire.ts:210
type RequestOptions struct {
	// Timeout overrides the default 30s request timeout when > 0.
	Timeout time.Duration
	// OnLateAnswer is called in its own goroutine with the raw JSON "ok" result
	// if an answer arrives after the request timed out or was cancelled.
	OnLateAnswer func(raw json.RawMessage)
}

type pendingOp struct {
	channel      ReplyType
	label        string
	ch           chan opResult
	onLateAnswer func(raw json.RawMessage)
}

type opResult struct {
	raw json.RawMessage
	err error
}

type abandonedOp struct {
	id           int64
	label        string
	onLateAnswer func(raw json.RawMessage)
}

// Client is one connection to the Tern daemon (or another host speaking its
// JSON frame protocol).
// upstream: packages/coding-agent/src/tools/browser/tern/wire.ts:358
type Client struct {
	transport Transport
	conn      net.Conn

	writeMu sync.Mutex

	mu         sync.Mutex
	welcomed   bool
	ops        []string
	closed     bool
	closeErr   *Error
	nextID     int64
	pending    map[int64]*pendingOp
	abandoned  map[int64]abandonedOp
	abandonedQ []int64
	welcomeCh  chan error
	readerDone chan struct{}
}

// Dial connects to the Tern daemon's Unix socket, sends {"hello":{}}, and
// waits for {"welcome":{"ops":[...]}}.
// upstream: packages/coding-agent/src/tools/browser/tern/wire.ts:398
func Dial(ctx context.Context, socket string) (*Client, error) {
	return DialTransport(ctx, newUnixSocketTransport(socket))
}

// DialTransport connects using a custom Transport, greets, and waits for welcome.
func DialTransport(ctx context.Context, transport Transport) (*Client, error) {
	dialCtx := ctx
	var cancel context.CancelFunc
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		dialCtx, cancel = context.WithTimeout(ctx, connectTimeout)
		defer cancel()
	}

	conn, err := transport.Connect(dialCtx)
	if err != nil {
		return nil, err
	}

	c := &Client{
		transport:  transport,
		conn:       conn,
		nextID:     1,
		pending:    make(map[int64]*pendingOp),
		abandoned:  make(map[int64]abandonedOp),
		welcomeCh:  make(chan error, 1),
		readerDone: make(chan struct{}),
	}

	go c.readLoop()

	helloFrame, err := EncodeFrame(map[string]any{"hello": map[string]any{}})
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	deadline, ok := dialCtx.Deadline()
	if !ok {
		deadline = time.Now().Add(connectTimeout)
	}
	if err := c.writeFrame(helloFrame, deadline); err != nil {
		c.fail(c.droppedError(err.Error()))
		return nil, c.getCloseError()
	}

	select {
	case err := <-c.welcomeCh:
		if err != nil {
			return nil, err
		}
		return c, nil
	case <-dialCtx.Done():
		var te *Error
		if errors.Is(dialCtx.Err(), context.DeadlineExceeded) {
			te = &Error{
				Kind:    ErrConnect,
				Message: fmt.Sprintf("The %s did not answer the greeting in time", transport.Peer()),
			}
		} else {
			te = &Error{
				Kind:    ErrConnect,
				Message: fmt.Sprintf("Cannot reach the %s: %s", transport.Peer(), dialCtx.Err().Error()),
			}
		}
		c.fail(te)
		return nil, te
	}
}

// Ops returns the list of request kinds advertised in the daemon's welcome.
func (c *Client) Ops() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.ops))
	copy(out, c.ops)
	return out
}

// Supports reports whether the daemon's welcome listed op.
func (c *Client) Supports(op string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Contains(c.ops, op)
}

// Product returns the transport's display name ("Tern", "WebKit", etc.).
func (c *Client) Product() string {
	return c.transport.Product()
}

// WaitsForEvents reports whether the peer holds an events call with wait:true.
func (c *Client) WaitsForEvents() bool {
	return c.transport.WaitsForEvents()
}

// Connected reports whether the connection is welcomed and open.
func (c *Client) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.welcomed && !c.closed
}

// Request sends {"id":N, kind: request} and unmarshals the "ok" result into out (if non-nil).
// upstream: packages/coding-agent/src/tools/browser/tern/wire.ts:410
func (c *Client) Request(ctx context.Context, kind string, request any, out any) error {
	return c.RequestWithOptions(ctx, kind, request, out, RequestOptions{})
}

// RequestWithOptions sends {"id":N, kind: request} with custom timeout and late-answer handling.
// upstream: packages/coding-agent/src/tools/browser/tern/wire.ts:424
func (c *Client) RequestWithOptions(ctx context.Context, kind string, request any, out any, opts RequestOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	channel := ReplyType(kind)
	label := kind
	if kind == "browser" {
		if opName := extractOpName(request); opName != "" {
			label = "browser " + opName
		} else {
			label = "browser op"
		}
	}

	// Marshal before allocating an ID so non-JSON arguments never advance nextID or leave pending state.
	c.mu.Lock()
	if c.closed {
		err := c.closeErr
		c.mu.Unlock()
		if err != nil {
			return err
		}
		return &Error{Kind: ErrClosed, Message: fmt.Sprintf("%s connection closed", c.transport.Product())}
	}
	id := c.nextID
	envelope := map[string]any{
		"id": id,
		kind: request,
	}
	frame, err := EncodeFrame(envelope)
	if err != nil {
		c.mu.Unlock()
		return &Error{
			Kind:    ErrInvalid,
			Message: fmt.Sprintf("%s %s has arguments that are not JSON: %s", c.transport.Product(), label, err.Error()),
		}
	}
	c.nextID++
	ch := make(chan opResult, 1)
	c.pending[id] = &pendingOp{
		channel:      channel,
		label:        label,
		ch:           ch,
		onLateAnswer: opts.OnLateAnswer,
	}
	c.mu.Unlock()

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultRequestTimeout
	}
	deadline := time.Now().Add(timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}

	// B5/N5: enforce write deadline and fail the whole client on ANY write error
	// (including timeout after a partial frame write so subsequent frames cannot corrupt the stream).
	if err := c.writeFrame(frame, deadline); err != nil {
		c.takePending(id)
		var te *Error
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			te = &Error{
				Kind:    ErrTimeout,
				Message: fmt.Sprintf("%s %s timed out after %dms", c.transport.Product(), label, timeout.Milliseconds()),
			}
			c.fail(&Error{
				Kind:    ErrClosed,
				Message: fmt.Sprintf("%s connection failed: write timed out after %dms", c.transport.Product(), timeout.Milliseconds()),
			})
			return te
		}
		te = &Error{
			Kind:    ErrClosed,
			Message: fmt.Sprintf("%s connection failed: %s", c.transport.Product(), err.Error()),
		}
		c.fail(te)
		return te
	}
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()

	finish := func(res opResult) error {
		if res.err != nil {
			return res.err
		}
		if out != nil && len(res.raw) > 0 {
			if err := json.Unmarshal(res.raw, out); err != nil {
				return &Error{
					Kind:    ErrProtocol,
					Message: fmt.Sprintf("%s %s answer decode failed: %s", c.transport.Product(), label, err.Error()),
				}
			}
		}
		return nil
	}

	select {
	case res := <-ch:
		return finish(res)
	case <-timer.C:
		// S6: if reader already took the pending entry and delivered to ch,
		// either return or hand it to onLateAnswer so no open block id is ever lost.
		if !c.abandon(id) {
			res := <-ch
			if res.err == nil && opts.OnLateAnswer != nil {
				go opts.OnLateAnswer(res.raw)
			}
		}
		return &Error{
			Kind:    ErrTimeout,
			Message: fmt.Sprintf("%s %s timed out after %dms", c.transport.Product(), label, timeout.Milliseconds()),
		}
	case <-ctx.Done():
		if !c.abandon(id) {
			res := <-ch
			if res.err == nil && opts.OnLateAnswer != nil {
				go opts.OnLateAnswer(res.raw)
			}
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return &Error{
				Kind:    ErrTimeout,
				Message: fmt.Sprintf("%s %s timed out", c.transport.Product(), label),
			}
		}
		return ctx.Err()
	}
}

func extractOpName(req any) string {
	if m, ok := req.(map[string]any); ok {
		if s, ok := m["op"].(string); ok {
			return s
		}
		return ""
	}
	// Support typed request structs via lightweight JSON tag inspection when needed.
	b, err := json.Marshal(req)
	if err != nil {
		return ""
	}
	var hdr struct {
		Op string `json:"op"`
	}
	if json.Unmarshal(b, &hdr) == nil {
		return hdr.Op
	}
	return ""
}

// Fork sends a fork request for pane block in direction dir ("right" or "down",
// omitted when empty) and returns the new pane's block ID.
// upstream: packages/coding-agent/src/tools/browser/tern/wire.ts:415
func (c *Client) Fork(ctx context.Context, block int, dir string) (int, error) {
	req := map[string]any{"block": block}
	if dir != "" {
		req["dir"] = dir
	}
	var res struct {
		Block *int `json:"block"`
	}
	if err := c.Request(ctx, "fork", req, &res); err != nil {
		return 0, err
	}
	if res.Block == nil {
		return 0, &Error{
			Kind:    ErrProtocol,
			Message: fmt.Sprintf("%s answered fork without a \"block\"", c.transport.Product()),
		}
	}
	return *res.Block, nil
}

// Close closes the connection and rejects all pending requests.
// upstream: packages/coding-agent/src/tools/browser/tern/wire.ts:473
func (c *Client) Close() error {
	c.fail(&Error{
		Kind:    ErrClosed,
		Message: fmt.Sprintf("%s connection closed", c.transport.Product()),
	})
	return nil
}

func (c *Client) writeFrame(frame []byte, deadline time.Time) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.conn.SetWriteDeadline(deadline)
	_, err := c.conn.Write(frame)
	_ = c.conn.SetWriteDeadline(time.Time{})
	return err
}

func (c *Client) readLoop() {
	defer close(c.readerDone)
	reader := NewFrameReader(c.transport.Product())
	buf := make([]byte, 32*1024)
	for {
		n, err := c.conn.Read(buf)
		if n > 0 {
			payloads, perr := reader.Push(buf[:n])
			if perr != nil {
				var te *Error
				if !errors.As(perr, &te) {
					te = &Error{Kind: ErrProtocol, Message: perr.Error()}
				}
				c.fail(te)
				return
			}
			for _, payload := range payloads {
				if !c.handlePayload(payload) {
					return
				}
			}
		}
		if err != nil {
			reason := ""
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				reason = err.Error()
			}
			c.fail(c.droppedError(reason))
			return
		}
	}
}

func (c *Client) handlePayload(payload []byte) bool {
	reply, err := DecodeReply(payload, c.transport.Product())
	if err != nil {
		var te *Error
		if !errors.As(err, &te) {
			te = &Error{Kind: ErrProtocol, Message: err.Error()}
		}
		c.fail(te)
		return false
	}

	switch reply.Type {
	case ReplyWelcome:
		c.mu.Lock()
		c.ops = reply.Ops
		wasWelcomed := c.welcomed
		c.welcomed = true
		c.mu.Unlock()
		if !wasWelcomed {
			select {
			case c.welcomeCh <- nil:
			default:
			}
		}
	case ReplyBrowser, ReplyFork:
		pending := c.takePending(reply.ID)
		if pending == nil {
			c.handleLateAnswer(reply.ID, reply.Answer)
			return true
		}
		if pending.channel != reply.Type {
			pending.ch <- opResult{
				err: &Error{
					Kind:    ErrProtocol,
					Message: fmt.Sprintf("%s answered %s with a %s answer", c.transport.Product(), pending.label, reply.Type),
				},
			}
			return true
		}
		raw, err := answerOf(c.transport.Product(), pending.label, reply.Answer)
		pending.ch <- opResult{raw: raw, err: err}
	case ReplyOther:
		// Skip unknown message kinds.
	}
	return true
}

func (c *Client) takePending(id int64) *pendingOp {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.pending[id]
	if ok {
		delete(c.pending, id)
	}
	return p
}

// abandon removes id from pending and records its late-answer hook.
// Returns true if id was still pending and removed, false if the reader already took it.
func (c *Client) abandon(id int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.pending[id]
	if !ok {
		return false
	}
	delete(c.pending, id)
	if p.onLateAnswer != nil {
		c.abandoned[id] = abandonedOp{
			id:           id,
			label:        p.label,
			onLateAnswer: p.onLateAnswer,
		}
		c.abandonedQ = append(c.abandonedQ, id)
		for len(c.abandoned) > abandonedLimit && len(c.abandonedQ) > 0 {
			evict := c.abandonedQ[0]
			c.abandonedQ = c.abandonedQ[1:]
			delete(c.abandoned, evict)
		}
	}
	return true
}

func (c *Client) handleLateAnswer(id int64, answer json.RawMessage) {
	c.mu.Lock()
	ab, ok := c.abandoned[id]
	if ok {
		delete(c.abandoned, id)
	}
	c.mu.Unlock()
	if !ok {
		return
	}
	raw, err := answerOf(c.transport.Product(), ab.label, answer)
	if err != nil {
		return
	}
	// S6: invoke asynchronously so onLateAnswer can send follow-up ops (e.g. close)
	// without deadlocking the readLoop goroutine.
	go ab.onLateAnswer(raw)
}

func (c *Client) droppedError(reason string) *Error {
	c.mu.Lock()
	welcomed := c.welcomed
	c.mu.Unlock()
	if welcomed {
		if reason != "" {
			return &Error{Kind: ErrClosed, Message: fmt.Sprintf("Connection to the %s failed: %s", c.transport.Peer(), reason)}
		}
		return &Error{Kind: ErrClosed, Message: fmt.Sprintf("The %s closed the connection", c.transport.Peer())}
	}
	if reason != "" {
		return &Error{Kind: ErrConnect, Message: fmt.Sprintf("Cannot reach the %s: %s", c.transport.Peer(), reason)}
	}
	hint := c.transport.GreetingHint()
	if hint != "" {
		hint = ": " + hint
	}
	return &Error{Kind: ErrConnect, Message: fmt.Sprintf("The %s closed the connection before greeting%s", c.transport.Peer(), hint)}
}

func (c *Client) getCloseError() *Error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closeErr
}

func (c *Client) fail(err *Error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.closeErr = err
	pending := c.pending
	c.pending = make(map[int64]*pendingOp)
	c.abandoned = make(map[int64]abandonedOp)
	c.abandonedQ = nil
	welcomed := c.welcomed
	c.mu.Unlock()

	if !welcomed {
		select {
		case c.welcomeCh <- err:
		default:
		}
	}
	for _, p := range pending {
		p.ch <- opResult{err: err}
	}
	if c.conn != nil {
		_ = c.conn.Close()
	}
}

// answerOf extracts the "ok" payload or returns a protocol/daemon Error.
// upstream: packages/coding-agent/src/tools/browser/tern/wire.ts:321
func answerOf(product, label string, answer json.RawMessage) (json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(answer, &obj); err != nil || obj == nil {
		return nil, &Error{
			Kind:    ErrProtocol,
			Message: fmt.Sprintf("%s answered %s without \"ok\" or \"error\"", product, label),
		}
	}
	if okVal, hasOK := obj["ok"]; hasOK {
		return okVal, nil
	}
	if errVal, hasErr := obj["error"]; hasErr {
		var errObj struct {
			Kind    string `json:"kind"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(errVal, &errObj); err == nil {
			kindStr := errObj.Kind
			if kindStr == "" {
				kindStr = "failed"
			}
			msg := errObj.Message
			if msg == "" {
				msg = "failed"
			}
			kind, known := protocolErrorKinds[kindStr]
			if !known {
				kind = ErrFailed
			}
			return nil, &Error{
				Kind:    kind,
				Message: fmt.Sprintf("%s %s failed (%s): %s", product, label, kindStr, msg),
			}
		}
	}
	return nil, &Error{
		Kind:    ErrProtocol,
		Message: fmt.Sprintf("%s answered %s without \"ok\" or \"error\"", product, label),
	}
}
