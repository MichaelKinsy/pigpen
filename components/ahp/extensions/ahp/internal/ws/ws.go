// Package ws is a minimal RFC 6455 WebSocket transport for the AHP host: a server (HTTP upgrade
// with token and Origin checks, text frames, fragmentation, ping/pong, close, size limits, a
// bounded outbound queue) and a small client used by tests. It replaces pi-ahp's
// src/transport/websocket.ts, which uses the `ws` package, so the extension needs no third-party
// module. AHP sends each JSON-RPC message as one text frame.
package ws

import (
	"bufio"
	"context"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/MichaelKinsy/pigpen/ahp/internal/host"
)

const guid = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

const (
	opContinuation = 0x0
	opText         = 0x1
	opBinary       = 0x2
	opClose        = 0x8
	opPing         = 0x9
	opPong         = 0xA
)

// Close codes (RFC 6455 section 7.4).
const (
	closeNormal        = 1000
	closeGoingAway     = 1001
	closeProtocolError = 1002
	closeInvalidData   = 1007
	closeTooBig        = 1009
)

// Options configure a Server.
type Options struct {
	// Addr is the listen address, "127.0.0.1:0" for an ephemeral loopback port.
	Addr string
	// Token, when non-empty, must be presented as ?token= or ?tkn= (VS Code's spelling).
	Token string
	// AllowedOrigins lists browser Origins that may connect. A request with an Origin header that is
	// not listed is refused; a request without one (native clients) is allowed.
	AllowedOrigins []string
	// MaxMessageBytes bounds one message (0: 16 MiB).
	MaxMessageBytes int
	// MaxQueuedFrames bounds the outbound queue of one peer (0: 4096); a peer that falls behind is closed.
	MaxQueuedFrames int
	// PingInterval is the keep-alive interval (0: 30 s); a peer that says nothing for two intervals is closed.
	PingInterval time.Duration
	Log          func(string)
}

func (o *Options) defaults() {
	if o.MaxMessageBytes <= 0 {
		o.MaxMessageBytes = 16 << 20
	}
	if o.MaxQueuedFrames <= 0 {
		o.MaxQueuedFrames = 4096
	}
	if o.PingInterval <= 0 {
		o.PingInterval = 30 * time.Second
	}
}

// AcceptKey computes Sec-WebSocket-Accept for a client key.
func AcceptKey(key string) string {
	sum := sha1.Sum([]byte(key + guid))
	return base64.StdEncoding.EncodeToString(sum[:])
}

// Server accepts WebSocket connections and hands each to the host.
type Server struct {
	h    *host.Host
	opts Options
	ln   net.Listener
	srv  *http.Server

	mu     sync.Mutex
	conns  map[*serverConn]struct{}
	closed bool
	wg     sync.WaitGroup
}

// Serve starts listening and wires each connection into the host.
func Serve(h *host.Host, opts Options) (*Server, error) {
	opts.defaults()
	ln, err := net.Listen("tcp", opts.Addr)
	if err != nil {
		return nil, err
	}
	s := &Server{h: h, opts: opts, ln: ln, conns: map[*serverConn]struct{}{}}
	s.srv = &http.Server{Handler: http.HandlerFunc(s.handle), ReadHeaderTimeout: 10 * time.Second}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		_ = s.srv.Serve(ln)
	}()
	return s, nil
}

// Addr is the bound address.
func (s *Server) Addr() net.Addr { return s.ln.Addr() }

// Port is the bound port.
func (s *Server) Port() int { return s.ln.Addr().(*net.TCPAddr).Port }

// Close stops listening and closes every connection.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	conns := make([]*serverConn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.srv.Shutdown(ctx) // hijacked connections are not tracked by http.Server
	_ = s.ln.Close()
	for _, c := range conns {
		c.shutdown(closeGoingAway, "server closing")
	}
	s.wg.Wait()
	return nil
}

func (s *Server) logf(format string, args ...any) {
	if s.opts.Log != nil {
		s.opts.Log(fmt.Sprintf(format, args...))
	}
}

func headerHasToken(h http.Header, name, token string) bool {
	for _, v := range h.Values(name) {
		for _, part := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

func (s *Server) originAllowed(origin string) bool {
	for _, allowed := range s.opts.AllowedOrigins {
		if strings.EqualFold(allowed, origin) {
			return true
		}
	}
	return false
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	if !headerHasToken(r.Header, "Upgrade", "websocket") {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusUpgradeRequired)
		_, _ = io.WriteString(w, "Upgrade required")
		return
	}
	if s.opts.Token != "" {
		q := r.URL.Query()
		// VS Code calls a manually configured connection token `tkn`; generic AHP examples use
		// `token`, so a protected direct listener accepts both.
		presented, ok := "", false
		if vals, has := q["token"]; has {
			presented, ok = vals[0], true
		} else if vals, has := q["tkn"]; has {
			presented, ok = vals[0], true
		}
		if !ok || subtle.ConstantTimeCompare([]byte(presented), []byte(s.opts.Token)) != 1 {
			// 403, as VS Code's agent host (the AHP reference host) answers a missing or wrong tkn; pi-ahp writes 401.
			// A 401 would also owe a WWW-Authenticate challenge, which a query token has no scheme for.
			s.logf("rejecting upgrade: bad token")
			w.WriteHeader(http.StatusForbidden)
			return
		}
	}
	if origin := r.Header.Get("Origin"); origin != "" && !s.originAllowed(origin) {
		s.logf("rejecting upgrade: origin %q is not allowed", origin)
		w.WriteHeader(http.StatusForbidden)
		return
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if r.Method != http.MethodGet || !headerHasToken(r.Header, "Connection", "upgrade") ||
		r.Header.Get("Sec-WebSocket-Version") != "13" || !validKey(key) {
		w.Header().Set("Sec-WebSocket-Version", "13")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	conn, brw, err := hj.Hijack()
	if err != nil {
		return
	}
	if _, err := fmt.Fprintf(brw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", AcceptKey(key)); err != nil || brw.Flush() != nil {
		conn.Close()
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		conn.Close()
		return
	}
	sc := newServerConn(s, conn, brw.Reader)
	s.conns[sc] = struct{}{}
	s.wg.Add(1)
	s.mu.Unlock()
	sc.hostConn = s.h.Accept(sc)
	go sc.run()
}

func validKey(key string) bool {
	raw, err := base64.StdEncoding.DecodeString(key)
	return err == nil && len(raw) == 16
}

// ── server connection ───────────────────────────────────────────────────

type outFrame struct {
	opcode  byte
	payload []byte
	close   bool // the connection ends after this frame
}

type serverConn struct {
	s        *Server
	conn     net.Conn
	br       *bufio.Reader
	hostConn *host.Conn

	out      chan outFrame // data frames
	ctrl     chan outFrame // control frames, sent ahead of data
	done     chan struct{}
	stop     chan struct{} // the reader is finished: flush control frames, then end the writer
	doneOnce sync.Once
	closing  sync.Once
}

func newServerConn(s *Server, conn net.Conn, br *bufio.Reader) *serverConn {
	return &serverConn{
		s: s, conn: conn, br: br,
		out:  make(chan outFrame, s.opts.MaxQueuedFrames),
		ctrl: make(chan outFrame, 16),
		done: make(chan struct{}),
		stop: make(chan struct{}),
	}
}

// Send implements host.Transport. It never blocks: a peer whose queue is full is closed, which is
// safe because AHP clients recover through reconnect.
func (c *serverConn) Send(frame []byte) error {
	select {
	case <-c.done:
		return errors.New("connection closed")
	default:
	}
	select {
	case c.out <- outFrame{opcode: opText, payload: frame}:
		return nil
	default:
		c.s.logf("closing a peer that stopped reading (queue of %d frames full)", cap(c.out))
		go c.shutdown(closeGoingAway, "too slow")
		return errors.New("outbound queue full")
	}
}

// Close implements host.Transport.
func (c *serverConn) Close() error {
	go c.shutdown(closeNormal, "")
	return nil
}

func closePayload(code uint16, reason string) []byte {
	p := make([]byte, 2, 2+len(reason))
	binary.BigEndian.PutUint16(p, code)
	if len(reason) > 123 {
		reason = reason[:123]
	}
	return append(p, reason...)
}

// shutdown sends a close frame, gives the peer a moment to answer, then drops the connection.
func (c *serverConn) shutdown(code uint16, reason string) {
	c.closing.Do(func() {
		select {
		case c.ctrl <- outFrame{opcode: opClose, payload: closePayload(code, reason), close: true}:
		case <-c.done:
			return
		}
		select {
		case <-c.done:
		case <-time.After(time.Second):
			c.finish()
		}
	})
}

func (c *serverConn) finish() {
	c.doneOnce.Do(func() {
		close(c.done)
		_ = c.conn.Close()
	})
}

func (c *serverConn) run() {
	defer c.s.wg.Done()
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		c.writeLoop()
	}()
	c.readLoop()
	close(c.stop)
	select {
	case <-writerDone:
	case <-time.After(2 * time.Second):
	}
	c.finish()
	<-writerDone
	c.s.mu.Lock()
	delete(c.s.conns, c)
	c.s.mu.Unlock()
	c.hostConn.Closed()
}

func (c *serverConn) writeLoop() {
	ping := time.NewTicker(c.s.opts.PingInterval)
	defer ping.Stop()
	write := func(f outFrame) bool {
		_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := writeFrame(c.conn, f.opcode, f.payload, false); err != nil {
			c.finish()
			return false
		}
		if f.close {
			// Closing handshake initiated by us: leave the peer time to echo; the reader ends on EOF.
			return true
		}
		return true
	}
	for {
		// Control frames first.
		select {
		case f := <-c.ctrl:
			if !write(f) {
				return
			}
			continue
		default:
		}
		select {
		case f := <-c.ctrl:
			if !write(f) {
				return
			}
		case f := <-c.out:
			if !write(f) {
				return
			}
		case <-ping.C:
			if !write(outFrame{opcode: opPing}) {
				return
			}
		case <-c.stop:
			// Flush what the reader queued (the echo of a close, a protocol-error close), then end.
			for {
				select {
				case f := <-c.ctrl:
					if !write(f) {
						return
					}
				default:
					return
				}
			}
		case <-c.done:
			return
		}
	}
}

type frameHeader struct {
	fin     bool
	rsv     byte
	opcode  byte
	masked  bool
	length  uint64
	maskKey [4]byte
}

func readHeader(r io.Reader) (frameHeader, error) {
	var h frameHeader
	var b [2]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return h, err
	}
	h.fin = b[0]&0x80 != 0
	h.rsv = (b[0] >> 4) & 0x7
	h.opcode = b[0] & 0x0f
	h.masked = b[1]&0x80 != 0
	h.length = uint64(b[1] & 0x7f)
	switch h.length {
	case 126:
		var e [2]byte
		if _, err := io.ReadFull(r, e[:]); err != nil {
			return h, err
		}
		h.length = uint64(binary.BigEndian.Uint16(e[:]))
	case 127:
		var e [8]byte
		if _, err := io.ReadFull(r, e[:]); err != nil {
			return h, err
		}
		h.length = binary.BigEndian.Uint64(e[:])
		if h.length>>63 != 0 {
			return h, errors.New("frame length has the high bit set")
		}
	}
	if h.masked {
		if _, err := io.ReadFull(r, h.maskKey[:]); err != nil {
			return h, err
		}
	}
	return h, nil
}

func writeFrame(w io.Writer, opcode byte, payload []byte, mask bool) error {
	hdr := []byte{0x80 | opcode}
	maskBit := byte(0)
	if mask {
		maskBit = 0x80
	}
	switch n := len(payload); {
	case n < 126:
		hdr = append(hdr, maskBit|byte(n))
	case n <= 0xffff:
		hdr = append(hdr, maskBit|126, byte(n>>8), byte(n))
	default:
		var l [8]byte
		binary.BigEndian.PutUint64(l[:], uint64(n))
		hdr = append(hdr, maskBit|127)
		hdr = append(hdr, l[:]...)
	}
	body := payload
	if mask {
		key := [4]byte{byte(time.Now().UnixNano()), 0x37, 0xfa, 0x21}
		hdr = append(hdr, key[:]...)
		body = make([]byte, len(payload))
		for i, b := range payload {
			body[i] = b ^ key[i%4]
		}
	}
	_, err := w.Write(append(hdr, body...))
	return err
}

// readMessage reads one full data message (reassembling fragments) and handles control frames
// through onControl. requireMask is true on the server side.
func readMessage(r *bufio.Reader, limit int, requireMask bool, onControl func(op byte, payload []byte) error) (opcode byte, msg []byte, code uint16, err error) {
	var buf []byte
	var first byte
	inMessage := false
	for {
		h, err := readHeader(r)
		if err != nil {
			return 0, nil, 0, err
		}
		if h.rsv != 0 {
			return 0, nil, closeProtocolError, errors.New("reserved bits set")
		}
		if requireMask && !h.masked {
			return 0, nil, closeProtocolError, errors.New("client frames must be masked")
		}
		isControl := h.opcode&0x8 != 0
		if isControl && (!h.fin || h.length > 125) {
			return 0, nil, closeProtocolError, errors.New("invalid control frame")
		}
		switch h.opcode {
		case opContinuation:
			if !inMessage {
				return 0, nil, closeProtocolError, errors.New("unexpected continuation frame")
			}
		case opText, opBinary:
			if inMessage {
				return 0, nil, closeProtocolError, errors.New("data frame inside a fragmented message")
			}
		case opClose, opPing, opPong:
		default:
			return 0, nil, closeProtocolError, fmt.Errorf("reserved opcode %d", h.opcode)
		}
		if !isControl && h.length > uint64(limit-len(buf)) {
			return 0, nil, closeTooBig, errors.New("message too big")
		}
		payload := make([]byte, h.length)
		if _, err := io.ReadFull(r, payload); err != nil {
			return 0, nil, 0, err
		}
		if h.masked {
			for i := range payload {
				payload[i] ^= h.maskKey[i%4]
			}
		}
		if isControl {
			if err := onControl(h.opcode, payload); err != nil {
				return h.opcode, nil, 0, err
			}
			continue
		}
		if !inMessage {
			first = h.opcode
			inMessage = true
		}
		buf = append(buf, payload...)
		if h.fin {
			if first == opText && !utf8.Valid(buf) {
				return 0, nil, closeInvalidData, errors.New("invalid UTF-8 in a text message")
			}
			return first, buf, 0, nil
		}
	}
}

var errPeerClosed = errors.New("peer closed the connection")

func (c *serverConn) readLoop() {
	idle := 2 * c.s.opts.PingInterval
	for {
		_ = c.conn.SetReadDeadline(time.Now().Add(idle))
		_, msg, code, err := readMessage(c.br, c.s.opts.MaxMessageBytes, true, func(op byte, payload []byte) error {
			_ = c.conn.SetReadDeadline(time.Now().Add(idle))
			switch op {
			case opPing:
				select {
				case c.ctrl <- outFrame{opcode: opPong, payload: payload}:
				default:
				}
			case opClose:
				echo := closePayload(closeNormal, "")
				if len(payload) >= 2 {
					echo = payload[:2]
				}
				c.closing.Do(func() {}) // the peer initiated: do not start our own handshake
				select {
				case c.ctrl <- outFrame{opcode: opClose, payload: echo, close: true}:
				default:
				}
				return errPeerClosed
			}
			return nil
		})
		if err != nil {
			if code != 0 {
				c.closing.Do(func() {})
				select {
				case c.ctrl <- outFrame{opcode: opClose, payload: closePayload(code, ""), close: true}:
				default:
				}
			}
			return
		}
		c.hostConn.Receive(msg)
	}
}

// ── client (tests and interop) ──────────────────────────────────────────

// Client is a WebSocket client connection.
type Client struct {
	conn net.Conn
	br   *bufio.Reader
	wmu  sync.Mutex
}

// Dial connects to a ws:// URL. On a refused upgrade it returns the HTTP response.
func Dial(rawurl string, header http.Header) (*Client, *http.Response, error) {
	u, err := url.Parse(rawurl)
	if err != nil {
		return nil, nil, err
	}
	if u.Scheme != "ws" {
		return nil, nil, fmt.Errorf("ws: unsupported scheme %q", u.Scheme)
	}
	conn, err := net.DialTimeout("tcp", u.Host, 5*time.Second)
	if err != nil {
		return nil, nil, err
	}
	rawKey := make([]byte, 16)
	for i := range rawKey {
		rawKey[i] = byte(time.Now().UnixNano()>>uint(i)) ^ byte(i*31+7)
	}
	key := base64.StdEncoding.EncodeToString(rawKey)
	path := u.RequestURI()
	var sb strings.Builder
	fmt.Fprintf(&sb, "GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n", path, u.Host, key)
	for k, vs := range header {
		for _, v := range vs {
			fmt.Fprintf(&sb, "%s: %s\r\n", k, v)
		}
	}
	sb.WriteString("\r\n")
	if _, err := io.WriteString(conn, sb.String()); err != nil {
		conn.Close()
		return nil, nil, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodGet})
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols || resp.Header.Get("Sec-WebSocket-Accept") != AcceptKey(key) {
		conn.Close()
		return nil, resp, fmt.Errorf("ws: upgrade refused: %s", resp.Status)
	}
	return &Client{conn: conn, br: br}, resp, nil
}

// WriteText sends one text message.
func (c *Client) WriteText(msg []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return writeFrame(c.conn, opText, msg, true)
}

// ReadMessage returns the next data message (text or binary), answering pings.
func (c *Client) ReadMessage(timeout time.Duration) ([]byte, error) {
	_ = c.conn.SetReadDeadline(time.Now().Add(timeout))
	_, msg, _, err := readMessage(c.br, 64<<20, false, func(op byte, payload []byte) error {
		switch op {
		case opPing:
			c.wmu.Lock()
			defer c.wmu.Unlock()
			return writeFrame(c.conn, opPong, payload, true)
		case opClose:
			c.wmu.Lock()
			_ = writeFrame(c.conn, opClose, payload, true)
			c.wmu.Unlock()
			return io.EOF
		}
		return nil
	})
	return msg, err
}

// Close sends a close frame and closes the connection.
func (c *Client) Close() error {
	c.wmu.Lock()
	_ = writeFrame(c.conn, opClose, closePayload(closeNormal, ""), true)
	c.wmu.Unlock()
	return c.conn.Close()
}
