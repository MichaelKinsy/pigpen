package browser

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	wsGUID              = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	wsMaxMessageBytes   = 64 << 20 // 64 MiB total message cap
	wsReadChunkBytes    = 64 << 10 // 64 KiB incremental read chunks
	wsWriteTimeout      = 15 * time.Second
	cdpMaxHTTPBodyBytes = 8 << 20 // 8 MiB cap on /json and /json/version responses
)

// wsConn is a minimal RFC 6455 WebSocket client built on Go's standard library
// (no third-party modules). It validates RSV bits, server non-masking, control
// frame FIN/length, fragmentation sequence, caps total message size to 64 MiB
// with bounded incremental reads, sends close frames before shutting down, and
// applies write deadlines so a wedged peer cannot block callers forever.
type wsConn struct {
	conn   net.Conn
	br     *bufio.Reader
	wmu    sync.Mutex
	closed atomic.Bool
}

func dialWebSocket(ctx context.Context, rawURL string) (*wsConn, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid websocket URL %q: %w", rawURL, err)
	}
	host := u.Host
	if u.Port() == "" {
		if u.Scheme == "wss" {
			host = net.JoinHostPort(u.Hostname(), "443")
		} else {
			host = net.JoinHostPort(u.Hostname(), "80")
		}
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	var conn net.Conn
	if u.Scheme == "wss" {
		tlsDialer := &tls.Dialer{NetDialer: dialer}
		conn, err = tlsDialer.DialContext(ctx, "tcp", host)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", host)
	}
	if err != nil {
		return nil, fmt.Errorf("WebSocket connection to '%s' failed: %w", rawURL, err)
	}

	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}

	var keyBytes [16]byte
	if _, err := io.ReadFull(rand.Reader, keyBytes[:]); err != nil {
		_ = conn.Close()
		return nil, err
	}
	secKey := base64.StdEncoding.EncodeToString(keyBytes[:])
	reqPath := u.RequestURI()
	if reqPath == "" {
		reqPath = "/"
	}

	var req strings.Builder
	fmt.Fprintf(&req, "GET %s HTTP/1.1\r\n", reqPath)
	fmt.Fprintf(&req, "Host: %s\r\n", u.Host)
	req.WriteString("Upgrade: websocket\r\n")
	req.WriteString("Connection: Upgrade\r\n")
	fmt.Fprintf(&req, "Sec-WebSocket-Key: %s\r\n", secKey)
	req.WriteString("Sec-WebSocket-Version: 13\r\n\r\n")

	if _, err := io.WriteString(conn, req.String()); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("WebSocket handshake write failed: %w", err)
	}

	br := bufio.NewReaderSize(conn, 64*1024)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodGet})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("WebSocket handshake read failed: %w", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		_ = conn.Close()
		return nil, fmt.Errorf("WebSocket connection to '%s' failed: Unexpected server response: %d", rawURL, resp.StatusCode)
	}

	sum := sha1.Sum([]byte(secKey + wsGUID))
	wantAccept := base64.StdEncoding.EncodeToString(sum[:])
	if got := strings.TrimSpace(resp.Header.Get("Sec-WebSocket-Accept")); got != wantAccept {
		_ = conn.Close()
		return nil, fmt.Errorf("WebSocket connection to '%s' failed: invalid Sec-WebSocket-Accept", rawURL)
	}

	_ = conn.SetDeadline(time.Time{})
	return &wsConn{conn: conn, br: br}, nil
}

func (w *wsConn) WriteText(payload []byte) error {
	return w.writeFrame(0x1, payload, false)
}

func (w *wsConn) writeFrame(opcode byte, payload []byte, allowClosing bool) error {
	w.wmu.Lock()
	defer w.wmu.Unlock()
	if !allowClosing && w.closed.Load() {
		return errors.New("websocket closed")
	}
	var hdr [14]byte
	hdr[0] = 0x80 | (opcode & 0x0f) // FIN=1
	n := len(payload)
	idx := 2
	switch {
	case n < 126:
		hdr[1] = 0x80 | byte(n)
	case n <= 65535:
		hdr[1] = 0x80 | 126
		binary.BigEndian.PutUint16(hdr[2:4], uint16(n))
		idx = 4
	default:
		hdr[1] = 0x80 | 127
		binary.BigEndian.PutUint64(hdr[2:10], uint64(n))
		idx = 10
	}
	var mask [4]byte
	if _, err := io.ReadFull(rand.Reader, mask[:]); err != nil {
		return err
	}
	copy(hdr[idx:idx+4], mask[:])
	idx += 4

	buf := make([]byte, idx+n)
	copy(buf[:idx], hdr[:idx])
	for i := range n {
		buf[idx+i] = payload[i] ^ mask[i&3]
	}
	_ = w.conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
	_, err := w.conn.Write(buf)
	_ = w.conn.SetWriteDeadline(time.Time{})
	return err
}

func (w *wsConn) ReadMessage() ([]byte, error) {
	var msg []byte
	inFragment := false
	for {
		var h [2]byte
		if _, err := io.ReadFull(w.br, h[:]); err != nil {
			return nil, err
		}
		fin := (h[0] & 0x80) != 0
		rsv := h[0] & 0x70
		opcode := h[0] & 0x0f
		masked := (h[1] & 0x80) != 0
		length := uint64(h[1] & 0x7f)

		// RFC 6455 §5.2: RSV1-3 must be 0 when no extension is negotiated.
		if rsv != 0 {
			return nil, fmt.Errorf("websocket protocol error: non-zero RSV bits 0x%x", rsv)
		}
		// RFC 6455 §5.1: server-to-client frames must NOT be masked.
		if masked {
			return nil, errors.New("websocket protocol error: masked server frame")
		}
		switch length {
		case 126:
			var ext [2]byte
			if _, err := io.ReadFull(w.br, ext[:]); err != nil {
				return nil, err
			}
			length = uint64(binary.BigEndian.Uint16(ext[:]))
		case 127:
			var ext [8]byte
			if _, err := io.ReadFull(w.br, ext[:]); err != nil {
				return nil, err
			}
			length = binary.BigEndian.Uint64(ext[:])
		}

		isControl := opcode >= 0x8
		if isControl {
			// RFC 6455 §5.5: control frames MUST NOT be fragmented and MUST have length <= 125.
			if !fin {
				return nil, fmt.Errorf("websocket protocol error: fragmented control frame (opcode 0x%x)", opcode)
			}
			if length > 125 {
				return nil, fmt.Errorf("websocket protocol error: control frame too large (%d bytes)", length)
			}
		} else {
			if opcode == 0x0 && !inFragment {
				return nil, errors.New("websocket protocol error: unexpected continuation frame")
			}
			if (opcode == 0x1 || opcode == 0x2) && inFragment {
				return nil, errors.New("websocket protocol error: new data frame while fragmented message in progress")
			}
			if length > uint64(wsMaxMessageBytes) || uint64(len(msg))+length > uint64(wsMaxMessageBytes) {
				return nil, fmt.Errorf("websocket message exceeds %d byte limit", wsMaxMessageBytes)
			}
		}

		// Read payload in bounded chunks so a lying frame header cannot cause an
		// eager huge allocation before bytes actually arrive on the wire.
		var payload []byte
		if length > 0 {
			rem := length
			var chunk [wsReadChunkBytes]byte
			for rem > 0 {
				want := min(rem, uint64(len(chunk)))
				if _, err := io.ReadFull(w.br, chunk[:want]); err != nil {
					return nil, err
				}
				payload = append(payload, chunk[:want]...)
				rem -= want
			}
		}

		switch opcode {
		case 0x8: // close
			_ = w.writeFrame(0x8, nil, true)
			return nil, io.EOF
		case 0x9: // ping
			_ = w.writeFrame(0xA, payload, false)
			continue
		case 0xA: // pong
			continue
		case 0x0, 0x1, 0x2:
			msg = append(msg, payload...)
			if fin {
				return msg, nil
			}
			inFragment = true
		default:
			return nil, fmt.Errorf("unsupported websocket opcode %d", opcode)
		}
	}
}

func (w *wsConn) Close() error {
	if w.closed.Swap(true) {
		return nil
	}
	// Send the RFC 6455 close frame before closing the underlying socket.
	_ = w.writeFrame(0x8, nil, true)
	return w.conn.Close()
}

// CDPEvent is an asynchronous event emitted by Chrome DevTools Protocol.
type CDPEvent struct {
	SessionID string
	Method    string
	Params    json.RawMessage
}

type cdpEnvelope struct {
	ID        int64           `json:"id,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Method    string          `json:"method,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *cdpProtoError  `json:"error,omitempty"`
}

type cdpProtoError struct {
	Code    int64  `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data,omitempty"`
}

func (e *cdpProtoError) Error() string {
	if e.Data != "" {
		return fmt.Sprintf("CDP error %d: %s (%s)", e.Code, e.Message, e.Data)
	}
	return fmt.Sprintf("CDP error %d: %s", e.Code, e.Message)
}

type cdpReply struct {
	result json.RawMessage
	err    error
}

// CDPClient multiplexes request/response RPCs and events over a single browser
// or page WebSocket connection, supporting flattened CDP sessionIds.
type CDPClient struct {
	ws        *wsConn
	nextID    atomic.Int64
	mu        sync.Mutex
	pending   map[int64]chan cdpReply
	reqMeta   map[int64]cdpReqMeta
	listeners map[int]func(CDPEvent)
	tracers   map[string]*cdpTraceRing // sessionID -> per-tab ring buffer
	nextSub   int
	closed    bool
	closeErr  error
	done      chan struct{}
}

type cdpReqMeta struct {
	sessionID string
	method    string
}

// DialCDP connects to a Chrome DevTools Protocol WebSocket endpoint.
func DialCDP(ctx context.Context, wsURL string) (*CDPClient, error) {
	ws, err := dialWebSocket(ctx, wsURL)
	if err != nil {
		return nil, err
	}
	c := &CDPClient{
		ws:        ws,
		pending:   make(map[int64]chan cdpReply),
		reqMeta:   make(map[int64]cdpReqMeta),
		listeners: make(map[int]func(CDPEvent)),
		tracers:   make(map[string]*cdpTraceRing),
		done:      make(chan struct{}),
	}
	go c.readLoop()
	return c, nil
}

// AttachTrace registers a per-session trace ring on c. Returns an detach func.
func (c *CDPClient) AttachTrace(sessionID string, ring *cdpTraceRing) func() {
	c.mu.Lock()
	c.tracers[sessionID] = ring
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		if c.tracers[sessionID] == ring {
			delete(c.tracers, sessionID)
		}
		c.mu.Unlock()
	}
}

func (c *CDPClient) recordTrace(sessionID string, e cdpTraceEntry) {
	c.mu.Lock()
	ring := c.tracers[sessionID]
	c.mu.Unlock()
	if ring != nil {
		ring.add(e)
	}
}

// Subscribe registers an event callback. Call the returned function to unsubscribe.
func (c *CDPClient) Subscribe(fn func(CDPEvent)) func() {
	c.mu.Lock()
	c.nextSub++
	id := c.nextSub
	c.listeners[id] = fn
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		delete(c.listeners, id)
		c.mu.Unlock()
	}
}

// Send sends a CDP command (optionally scoped to sessionID) and unmarshals the
// result into out if non-nil.
func (c *CDPClient) Send(ctx context.Context, sessionID, method string, params any, out any) error {
	wait, err := c.SendAsync(ctx, sessionID, method, params, out)
	if err != nil {
		return err
	}
	return wait()
}

// SendAsync synchronously writes a CDP command frame to the WebSocket (preserving
// strict wire ordering with subsequent Send calls) and returns a wait closure
// that blocks for its reply.
func (c *CDPClient) SendAsync(ctx context.Context, sessionID, method string, params any, out any) (func() error, error) {
	id := c.nextID.Add(1)
	req := struct {
		ID        int64  `json:"id"`
		SessionID string `json:"sessionId,omitempty"`
		Method    string `json:"method"`
		Params    any    `json:"params,omitempty"`
	}{
		ID:        id,
		SessionID: sessionID,
		Method:    method,
		Params:    params,
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	ch := make(chan cdpReply, 1)
	c.mu.Lock()
	if c.closed {
		err := c.closeErr
		c.mu.Unlock()
		if err == nil {
			err = errors.New("CDP connection closed")
		}
		return nil, err
	}
	c.pending[id] = ch
	c.reqMeta[id] = cdpReqMeta{sessionID: sessionID, method: method}
	ring := c.tracers[sessionID]
	c.mu.Unlock()

	if ring != nil {
		var paramBytes []byte
		if params != nil {
			paramBytes, _ = json.Marshal(params)
		}
		ring.add(cdpTraceEntry{
			TS:        time.Now(),
			Dir:       "->",
			Method:    method,
			ID:        id,
			SessionID: sessionID,
			FrameID:   extractTraceFrameID(paramBytes),
			Detail:    traceSnippet(paramBytes),
		})
	}

	if err := c.ws.WriteText(payload); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		delete(c.reqMeta, id)
		c.mu.Unlock()
		return nil, err
	}

	wait := func() error {
		select {
		case rep := <-ch:
			if rep.err != nil {
				return rep.err
			}
			if out != nil && len(rep.result) > 0 {
				if err := json.Unmarshal(rep.result, out); err != nil {
					return fmt.Errorf("CDP %s decode failed: %w", method, err)
				}
			}
			return nil
		case <-ctx.Done():
			c.mu.Lock()
			delete(c.pending, id)
			delete(c.reqMeta, id)
			c.mu.Unlock()
			if ring != nil {
				ring.add(cdpTraceEntry{
					TS:        time.Now(),
					Dir:       "!!",
					Method:    method,
					ID:        id,
					SessionID: sessionID,
					Detail:    "ctx=" + ctx.Err().Error(),
				})
			}
			return ctx.Err()
		case <-c.done:
			c.mu.Lock()
			err := c.closeErr
			c.mu.Unlock()
			if err == nil {
				err = errors.New("CDP connection closed")
			}
			return err
		}
	}
	return wait, nil
}

// Alive reports whether the WebSocket connection is still open.
func (c *CDPClient) Alive() bool {
	select {
	case <-c.done:
		return false
	default:
		return true
	}
}

// Close shuts down the CDP connection and wakes any pending callers.
func (c *CDPClient) Close() error {
	c.failAll(errors.New("CDP connection closed"))
	return c.ws.Close()
}

func (c *CDPClient) readLoop() {
	defer close(c.done)
	for {
		raw, err := c.ws.ReadMessage()
		if err != nil {
			c.failAll(err)
			return
		}
		var env cdpEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			continue
		}
		if env.ID != 0 {
			c.mu.Lock()
			ch, ok := c.pending[env.ID]
			meta := c.reqMeta[env.ID]
			if ok {
				delete(c.pending, env.ID)
				delete(c.reqMeta, env.ID)
			}
			ring := c.tracers[meta.sessionID]
			c.mu.Unlock()
			if ring != nil {
				detail := traceSnippet(env.Result)
				if env.Error != nil {
					detail = "ERR: " + env.Error.Error()
				}
				ring.add(cdpTraceEntry{
					TS:        time.Now(),
					Dir:       "<-",
					Method:    meta.method,
					ID:        env.ID,
					SessionID: meta.sessionID,
					Detail:    detail,
				})
			}
			if ok {
				var rerr error
				if env.Error != nil {
					rerr = env.Error
				}
				ch <- cdpReply{result: env.Result, err: rerr}
			}
			continue
		}
		if env.Method != "" {
			ev := CDPEvent{
				SessionID: env.SessionID,
				Method:    env.Method,
				Params:    env.Params,
			}
			c.recordTrace(env.SessionID, cdpTraceEntry{
				TS:        time.Now(),
				Dir:       "ev",
				Method:    env.Method,
				SessionID: env.SessionID,
				FrameID:   extractTraceFrameID(env.Params),
				Detail:    traceSnippet(env.Params),
			})
			c.mu.Lock()
			subs := make([]func(CDPEvent), 0, len(c.listeners))
			for _, fn := range c.listeners {
				subs = append(subs, fn)
			}
			c.mu.Unlock()
			for _, fn := range subs {
				func(listener func(CDPEvent)) {
					defer func() { _ = recover() }()
					listener(ev)
				}(fn)
			}
		}
	}
}

func (c *CDPClient) failAll(err error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.closeErr = err
	for id, ch := range c.pending {
		ch <- cdpReply{err: err}
		delete(c.pending, id)
	}
	c.mu.Unlock()
}

// cdpHTTPClient uses a direct dialer with Proxy: nil so loopback CDP probes
// never route through HTTP_PROXY/HTTPS_PROXY.
// upstream: rawHttpGet in attach.ts:53
var cdpHTTPClient = &http.Client{
	Transport: &http.Transport{
		Proxy: nil,
		DialContext: (&net.Dialer{
			Timeout: 3 * time.Second,
		}).DialContext,
	},
	Timeout: 5 * time.Second,
}

type cdpVersionResponse struct {
	Browser              string `json:"Browser"`
	ProtocolVersion      string `json:"Protocol-Version"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

// FetchCDPWebSocketURL queries `${cdpURL}/json/version` and returns its
// browser-level webSocketDebuggerUrl (bounded read).
func FetchCDPWebSocketURL(ctx context.Context, cdpURL string) (string, error) {
	endpoint := strings.TrimRight(cdpURL, "/") + "/json/version"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	resp, err := cdpHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("CDP endpoint %s returned HTTP %d", endpoint, resp.StatusCode)
	}
	var ver cdpVersionResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, cdpMaxHTTPBodyBytes)).Decode(&ver); err != nil {
		return "", fmt.Errorf("CDP /json/version decode failed: %w", err)
	}
	if ver.WebSocketDebuggerURL == "" {
		return "", fmt.Errorf("CDP endpoint %s returned no webSocketDebuggerUrl", endpoint)
	}
	return ver.WebSocketDebuggerURL, nil
}

// WaitForCDP polls `${cdpURL}/json/version` until it answers with a WebSocket URL
// or ctx expires.
// upstream: waitForCdp in attach.ts:166
func WaitForCDP(ctx context.Context, cdpURL string) (string, error) {
	var lastErr error
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		wsURL, err := FetchCDPWebSocketURL(ctx, cdpURL)
		if err == nil {
			return wsURL, nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return "", fmt.Errorf("Timed out waiting for CDP endpoint %s: %v", cdpURL, lastErr)
			}
			return "", fmt.Errorf("Timed out waiting for CDP endpoint %s", cdpURL)
		case <-ticker.C:
		}
	}
}

type cdpRawTarget struct {
	ID                   string `json:"id"`
	Type                 string `json:"type"`
	Title                string `json:"title"`
	URL                  string `json:"url"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

// ListCDPTargets fetches `${cdpURL}/json` and returns page targets formatted
// as omp's browser.targets().
// upstream: listCdpTargets + describeCdpTargets in attach.ts:615-655
func ListCDPTargets(ctx context.Context, cdpURL string) ([]TargetInfo, string, error) {
	base := strings.TrimRight(cdpURL, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/json", nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := cdpHTTPClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("failed to list CDP targets at %s: %w", base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("CDP /json at %s returned HTTP %d", base, resp.StatusCode)
	}
	var raw []cdpRawTarget
	if err := json.NewDecoder(io.LimitReader(resp.Body, cdpMaxHTTPBodyBytes)).Decode(&raw); err != nil {
		return nil, "", err
	}
	targets := make([]TargetInfo, 0, len(raw))
	for _, entry := range raw {
		if entry.Type != "page" {
			continue
		}
		targets = append(targets, TargetInfo{
			Target: entry.URL,
			Title:  entry.Title,
			URL:    entry.URL,
		})
	}
	return targets, DescribeCDPTargets(targets, "Pages at "+base), nil
}

// DescribeCDPTargets formats a human-readable listing matching omp's describeCdpTargets.
// upstream: describeCdpTargets in attach.ts:636
func DescribeCDPTargets(targets []TargetInfo, source string) string {
	if len(targets) == 0 {
		return fmt.Sprintf("%s: none.", source)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s (adopt one with target set to a URL or title substring):", source)
	for _, t := range targets {
		fmt.Fprintf(&b, "\n- %q %s", t.Title, t.URL)
	}
	return b.String()
}
