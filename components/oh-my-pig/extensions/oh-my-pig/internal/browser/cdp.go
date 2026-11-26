package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/relay"
)

const (
	wsGUID              = relay.WSGUID
	wsMaxMessageBytes   = 64 << 20 // 64 MiB default for dialWebSocket (tests assert 100 MiB rejected)
	cdpMaxHTTPBodyBytes = 8 << 20  // 8 MiB cap on /json and /json/version responses
)

type wsConn = relay.WSConn

func dialWebSocket(ctx context.Context, rawURL string) (*wsConn, error) {
	return relay.DialWS(ctx, rawURL, &relay.WSDialOptions{MaxMsgSize: wsMaxMessageBytes})
}

func dialWebSocketWithLimit(ctx context.Context, rawURL string, maxBytes int64) (*wsConn, error) {
	return relay.DialWS(ctx, rawURL, &relay.WSDialOptions{MaxMsgSize: maxBytes})
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
	return dialCDPWithHeaders(ctx, wsURL, nil, relay.DefaultMaxWSMessageBytes)
}

func dialCDPWithHeaders(ctx context.Context, wsURL string, headers http.Header, maxBytes int64) (*CDPClient, error) {
	ws, err := relay.DialWS(ctx, wsURL, &relay.WSDialOptions{Headers: headers, MaxMsgSize: maxBytes})
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
	return fetchCDPWebSocketURLWithHeaders(ctx, cdpURL, nil)
}

func fetchCDPWebSocketURLWithHeaders(ctx context.Context, cdpURL string, headers http.Header) (string, error) {
	endpoint := strings.TrimRight(cdpURL, "/") + "/json/version"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	for k, vals := range headers {
		for _, v := range vals {
			req.Header.Add(k, v)
		}
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
	return listCDPTargetsWithHeaders(ctx, cdpURL, nil)
}

func listCDPTargetsWithHeaders(ctx context.Context, cdpURL string, headers http.Header) ([]TargetInfo, string, error) {
	base := strings.TrimRight(cdpURL, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/json", nil)
	if err != nil {
		return nil, "", err
	}
	for k, vals := range headers {
		for _, v := range vals {
			req.Header.Add(k, v)
		}
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
