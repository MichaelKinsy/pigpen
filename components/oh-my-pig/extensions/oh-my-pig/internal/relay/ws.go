package relay

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
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
	"sync/atomic"
	"time"
)

const (
	// WSGUID is the RFC 6455 Sec-WebSocket-Accept magic GUID.
	WSGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

	// DefaultMaxWSMessageBytes matches omp's MAX_PAYLOAD_BYTES (256 MiB, server.ts:77):
	// screenshots travel base64-encoded through both websocket legs.
	DefaultMaxWSMessageBytes = 256 << 20

	wsReadChunkBytes = 64 << 10
	wsWriteTimeout   = 15 * time.Second
)

// WSConn is an RFC 6455 WebSocket connection built on Go's standard library.
// It supports both client mode (masked writes, unmasked reads) and server mode
// (unmasked writes, masked reads), validates RSV bits, masking rules, control
// frame FIN/length, fragmentation sequences, caps total message size with bounded
// incremental reads, responds to pings with pongs, sends close frames before
// shutting down, and applies write deadlines.
type WSConn struct {
	conn       net.Conn
	br         *bufio.Reader
	isServer   bool
	maxMsgSize uint64
	wmu        sync.Mutex
	closed     atomic.Bool
}

// WSDialOptions configures DialWS.
type WSDialOptions struct {
	Headers    http.Header
	MaxMsgSize int64
}

// DialWS opens an RFC 6455 client WebSocket connection to rawURL.
func DialWS(ctx context.Context, rawURL string, opts *WSDialOptions) (*WSConn, error) {
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
	req.WriteString("Sec-WebSocket-Version: 13\r\n")
	if opts != nil && opts.Headers != nil {
		for k, vals := range opts.Headers {
			for _, v := range vals {
				fmt.Fprintf(&req, "%s: %s\r\n", k, v)
			}
		}
	}
	req.WriteString("\r\n")

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

	wantAccept := wsAcceptKey(secKey)
	if got := strings.TrimSpace(resp.Header.Get("Sec-WebSocket-Accept")); got != wantAccept {
		_ = conn.Close()
		return nil, fmt.Errorf("WebSocket connection to '%s' failed: invalid Sec-WebSocket-Accept", rawURL)
	}

	_ = conn.SetDeadline(time.Time{})
	maxSize := uint64(DefaultMaxWSMessageBytes)
	if opts != nil && opts.MaxMsgSize > 0 {
		maxSize = uint64(opts.MaxMsgSize)
	}
	return &WSConn{conn: conn, br: br, isServer: false, maxMsgSize: maxSize}, nil
}

// UpgradeWS upgrades an incoming HTTP request to a server-side RFC 6455 WebSocket connection.
// Returns (nil, nil) if the request is not a valid WebSocket upgrade (caller writes 426).
func UpgradeWS(w http.ResponseWriter, r *http.Request, maxMsgSize int64) (*WSConn, bool, error) {
	if r.Method != http.MethodGet {
		return nil, false, nil
	}
	if !headerContainsToken(r.Header, "Connection", "upgrade") ||
		!headerContainsToken(r.Header, "Upgrade", "websocket") {
		return nil, false, nil
	}
	if strings.TrimSpace(r.Header.Get("Sec-WebSocket-Version")) != "13" {
		return nil, false, nil
	}
	secKey := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key"))
	if secKey == "" {
		return nil, false, nil
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, true, errors.New("http.ResponseWriter does not support Hijacker")
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		return nil, true, err
	}
	accept := wsAcceptKey(secKey)
	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
	if _, err := rw.WriteString(resp); err != nil {
		_ = conn.Close()
		return nil, true, err
	}
	if err := rw.Flush(); err != nil {
		_ = conn.Close()
		return nil, true, err
	}
	_ = conn.SetDeadline(time.Time{})
	if maxMsgSize <= 0 {
		maxMsgSize = DefaultMaxWSMessageBytes
	}
	return &WSConn{
		conn:       conn,
		br:         rw.Reader,
		isServer:   true,
		maxMsgSize: uint64(maxMsgSize),
	}, true, nil
}

func wsAcceptKey(secKey string) string {
	sum := sha1.Sum([]byte(secKey + WSGUID))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func headerContainsToken(h http.Header, key, token string) bool {
	for _, val := range h.Values(key) {
		for _, part := range strings.Split(val, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

// WriteText sends a text frame (opcode 0x1).
func (w *WSConn) WriteText(payload []byte) error {
	return w.writeFrame(0x1, payload, false)
}

// WritePing sends a protocol-level ping frame (opcode 0x9).
func (w *WSConn) WritePing(payload []byte) error {
	if len(payload) > 125 {
		payload = payload[:125]
	}
	return w.writeFrame(0x9, payload, false)
}

func (w *WSConn) writeFrame(opcode byte, payload []byte, allowClosing bool) error {
	w.wmu.Lock()
	defer w.wmu.Unlock()
	if !allowClosing && w.closed.Load() {
		return errors.New("websocket closed")
	}
	var hdr [14]byte
	hdr[0] = 0x80 | (opcode & 0x0f) // FIN=1
	n := len(payload)
	maskBit := byte(0)
	if !w.isServer {
		maskBit = 0x80
	}
	idx := 2
	switch {
	case n < 126:
		hdr[1] = maskBit | byte(n)
	case n <= 65535:
		hdr[1] = maskBit | 126
		binary.BigEndian.PutUint16(hdr[2:4], uint16(n))
		idx = 4
	default:
		hdr[1] = maskBit | 127
		binary.BigEndian.PutUint64(hdr[2:10], uint64(n))
		idx = 10
	}

	_ = w.conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
	defer func() { _ = w.conn.SetWriteDeadline(time.Time{}) }()

	if w.isServer {
		if _, err := w.conn.Write(hdr[:idx]); err != nil {
			return err
		}
		if n > 0 {
			_, err := w.conn.Write(payload)
			return err
		}
		return nil
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
	_, err := w.conn.Write(buf)
	return err
}

// ReadMessage reads the next complete text or binary WebSocket message, handling
// fragmentation, ping/pong, and close frames.
func (w *WSConn) ReadMessage() ([]byte, error) {
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
		// RFC 6455 §5.1: client->server frames MUST be masked; server->client MUST NOT be masked.
		if w.isServer && !masked {
			return nil, errors.New("websocket protocol error: unmasked client frame")
		}
		if !w.isServer && masked {
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
			// RFC 6455 §5.2: MSB must be 0.
			if length&(1<<63) != 0 {
				return nil, errors.New("websocket protocol error: invalid 64-bit length")
			}
		}

		var mask [4]byte
		if masked {
			if _, err := io.ReadFull(w.br, mask[:]); err != nil {
				return nil, err
			}
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
			if length > w.maxMsgSize || uint64(len(msg))+length > w.maxMsgSize {
				return nil, fmt.Errorf("websocket message exceeds %d byte limit", w.maxMsgSize)
			}
		}

		// Read payload in bounded chunks so a lying frame header cannot cause an
		// eager huge allocation before bytes actually arrive on the wire.
		var payload []byte
		if length > 0 {
			rem := length
			var chunk [wsReadChunkBytes]byte
			var pos uint64
			for rem > 0 {
				want := min(rem, uint64(len(chunk)))
				if _, err := io.ReadFull(w.br, chunk[:want]); err != nil {
					return nil, err
				}
				if masked {
					for i := uint64(0); i < want; i++ {
						chunk[i] ^= mask[(pos+i)&3]
					}
				}
				payload = append(payload, chunk[:want]...)
				pos += want
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

// Close sends an RFC 6455 close frame and closes the underlying TCP connection.
func (w *WSConn) Close() error {
	if w.closed.Swap(true) {
		return nil
	}
	_ = w.writeFrame(0x8, nil, true)
	return w.conn.Close()
}
