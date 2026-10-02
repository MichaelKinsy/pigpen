package ws_test

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/host"
	"github.com/MichaelKinsy/pigpen/ahp/internal/testkit"
	"github.com/MichaelKinsy/pigpen/ahp/internal/twin"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
	"github.com/MichaelKinsy/pigpen/ahp/internal/ws"
)

func serve(t *testing.T, opts ws.Options) (*host.Host, *ws.Server) {
	t.Helper()
	h := testkit.NewHost(host.Options{})
	if opts.Addr == "" {
		opts.Addr = "127.0.0.1:0"
	}
	s, err := ws.Serve(h, opts)
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return h, s
}

func url(s *ws.Server, query string) string {
	return "ws://127.0.0.1:" + itoa(s.Port()) + query
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func initialize(t *testing.T, c *ws.Client, id string) string {
	t.Helper()
	req, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{
		"channel": wire.RootChannel, "clientId": id, "protocolVersions": testkit.SupportedVersions(),
	}})
	if err := c.WriteText(req); err != nil {
		t.Fatal(err)
	}
	msg, err := c.ReadMessage(2 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
		} `json:"result"`
	}
	if err := json.Unmarshal(msg, &res); err != nil {
		t.Fatalf("%s: %v", msg, err)
	}
	return res.Result.ProtocolVersion
}

// Twins of upstream test/handshake.test.ts, describe "connection token".
func TestConnectionToken(t *testing.T) {
	// VS Code names a manually configured connection token `tkn`; generic AHP examples use
	// `token`. A direct listener configured with a token accepts either spelling.
	twin.Run(t, "handshake", "accepts the token as `token`", func(t *testing.T) {
		_, s := serve(t, ws.Options{Token: "secret"})
		c, _, err := ws.Dial(url(s, "?token=secret"), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if v := initialize(t, c, "token-client"); v != "0.9.0" {
			t.Fatalf("protocolVersion = %q", v)
		}
	})
	twin.Run(t, "handshake", "accepts the token as `tkn`", func(t *testing.T) {
		_, s := serve(t, ws.Options{Token: "secret"})
		c, _, err := ws.Dial(url(s, "?tkn=secret"), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if v := initialize(t, c, "tkn-client"); v != "0.9.0" {
			t.Fatalf("protocolVersion = %q", v)
		}
	})
	twin.Run(t, "handshake", "rejects a wrong token", func(t *testing.T) {
		_, s := serve(t, ws.Options{Token: "secret"})
		_, resp, err := ws.Dial(url(s, "?tkn=wrong"), nil)
		if err == nil {
			t.Fatal("a wrong token must not upgrade")
		}
		if resp == nil || resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("response = %+v", resp)
		}
	})
	twin.Run(t, "handshake", "ignores a VS Code tkn when the listener requires no token", func(t *testing.T) {
		_, s := serve(t, ws.Options{})
		c, _, err := ws.Dial(url(s, "?tkn=legacy-tunnel-value"), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if v := initialize(t, c, "legacy-client"); v != "0.9.0" {
			t.Fatalf("protocolVersion = %q", v)
		}
	})
}

// Additional Go cases: the transport itself and the hardening the port adds (Origin check,
// message limits, keep-alive, slow peers). pi-ahp relies on the `ws` package for these.
func TestNoTokenAndMissingTokenAreDifferent(t *testing.T) {
	_, s := serve(t, ws.Options{Token: "secret"})
	if _, resp, err := ws.Dial(url(s, ""), nil); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a missing token must be refused with 401: err=%v resp=%+v", err, resp)
	}
	if _, resp, err := ws.Dial(url(s, "?token="), nil); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("an empty token must be refused: err=%v resp=%+v", err, resp)
	}
}

func TestPlainHTTPGetsUpgradeRequired(t *testing.T) {
	_, s := serve(t, ws.Options{})
	resp, err := http.Get("http://127.0.0.1:" + itoa(s.Port()) + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 426 || strings.TrimSpace(string(body)) != "Upgrade required" {
		t.Fatalf("status %d body %q", resp.StatusCode, body)
	}
}

func TestBrowserOriginsAreRefusedUnlessAllowed(t *testing.T) {
	_, s := serve(t, ws.Options{})
	hdr := http.Header{"Origin": {"https://evil.example"}}
	if _, resp, err := ws.Dial(url(s, ""), hdr); err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a foreign Origin must be refused with 403: err=%v resp=%+v", err, resp)
	}
	_, s2 := serve(t, ws.Options{AllowedOrigins: []string{"https://console.example"}})
	c, _, err := ws.Dial(url(s2, ""), http.Header{"Origin": {"https://console.example"}})
	if err != nil {
		t.Fatalf("an allowed Origin must connect: %v", err)
	}
	c.Close()
}

func TestAcceptKeyMatchesRFC6455(t *testing.T) {
	if got := ws.AcceptKey("dGhlIHNhbXBsZSBub25jZQ=="); got != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Fatalf("AcceptKey = %q", got)
	}
}

// rawConn upgrades by hand so a test can send frames a well-behaved client never would.
func rawConn(t *testing.T, s *ws.Server) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn, err := net.Dial("tcp", "127.0.0.1:"+itoa(s.Port()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	io.WriteString(conn, "GET / HTTP/1.1\r\nHost: x\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n")
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil || resp.StatusCode != 101 {
		t.Fatalf("upgrade: %v %+v", err, resp)
	}
	return conn, br
}

func maskedFrame(fin bool, opcode byte, payload []byte) []byte {
	b0 := opcode
	if fin {
		b0 |= 0x80
	}
	key := []byte{1, 2, 3, 4}
	out := []byte{b0}
	switch n := len(payload); {
	case n < 126:
		out = append(out, 0x80|byte(n))
	case n < 65536:
		out = append(out, 0x80|126, byte(n>>8), byte(n))
	default:
		var l [8]byte
		binary.BigEndian.PutUint64(l[:], uint64(n))
		out = append(out, 0x80|127)
		out = append(out, l[:]...)
	}
	out = append(out, key...)
	for i, c := range payload {
		out = append(out, c^key[i%4])
	}
	return out
}

func readFrame(t *testing.T, br *bufio.Reader) (opcode byte, payload []byte) {
	t.Helper()
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(br, hdr); err != nil {
		t.Fatalf("read frame: %v", err)
	}
	n := int(hdr[1] & 0x7f)
	switch n {
	case 126:
		ext := make([]byte, 2)
		io.ReadFull(br, ext)
		n = int(binary.BigEndian.Uint16(ext))
	case 127:
		ext := make([]byte, 8)
		io.ReadFull(br, ext)
		n = int(binary.BigEndian.Uint64(ext))
	}
	payload = make([]byte, n)
	io.ReadFull(br, payload)
	return hdr[0] & 0x0f, payload
}

func TestFragmentedMessagesAreReassembledAndPingsAnswered(t *testing.T) {
	_, s := serve(t, ws.Options{})
	conn, br := rawConn(t, s)
	req := `{"jsonrpc":"2.0","id":7,"method":"ping","params":{"channel":"ahp-root://"}}`
	conn.Write(maskedFrame(false, 1, []byte(req[:20])))
	conn.Write(maskedFrame(true, 9, []byte("hi"))) // a ping between fragments is legal
	conn.Write(maskedFrame(true, 0, []byte(req[20:])))
	gotPong, gotReply := false, false
	for i := 0; i < 2; i++ {
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		op, payload := readFrame(t, br)
		switch op {
		case 10:
			gotPong = string(payload) == "hi"
		case 1:
			gotReply = strings.Contains(string(payload), `"id":7`)
		}
	}
	if !gotPong || !gotReply {
		t.Fatalf("pong=%v reply=%v", gotPong, gotReply)
	}
}

func TestProtocolViolationsCloseTheConnection(t *testing.T) {
	_, s := serve(t, ws.Options{MaxMessageBytes: 1024})
	cases := map[string]struct {
		frame []byte
		code  uint16
	}{
		"unmasked client frame": {[]byte{0x81, 0x02, 'h', 'i'}, 1002},
		"oversized message":     {maskedFrame(true, 1, make([]byte, 2048)), 1009},
		"reserved opcode":       {maskedFrame(true, 3, []byte("x")), 1002},
		"invalid utf-8":         {maskedFrame(true, 1, []byte{0xff, 0xfe}), 1007},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			conn, br := rawConn(t, s)
			conn.Write(tc.frame)
			conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			op, payload := readFrame(t, br)
			if op != 8 || len(payload) < 2 || binary.BigEndian.Uint16(payload) != tc.code {
				t.Fatalf("opcode %d payload %v, want close %d", op, payload, tc.code)
			}
		})
	}
}

func TestClosingReleasesTheHostConnection(t *testing.T) {
	h, s := serve(t, ws.Options{})
	c, _, err := ws.Dial(url(s, ""), nil)
	if err != nil {
		t.Fatal(err)
	}
	initialize(t, c, "closer")
	req, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "subscribe", "params": map[string]any{"channel": wire.RootChannel}})
	c.WriteText(req)
	if _, err := c.ReadMessage(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	if h.SubscriberCount(wire.RootChannel) != 1 {
		t.Fatalf("subscribers = %d", h.SubscriberCount(wire.RootChannel))
	}
	c.Close()
	testkit.Eventually(t, "the host to drop the closed connection's subscriptions", func() bool { return h.SubscriberCount(wire.RootChannel) == 0 })
}

func TestSlowPeerIsClosedNotBuffered(t *testing.T) {
	h, s := serve(t, ws.Options{MaxQueuedFrames: 8})
	conn, _ := rawConn(t, s) // never reads
	if tc, ok := conn.(*net.TCPConn); ok {
		tc.SetReadBuffer(1024) // keep the kernel from absorbing the backlog
	}
	frame := maskedFrame(true, 1, []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"channel":"ahp-root://","clientId":"slow","protocolVersions":["0.9.0"],"initialSubscriptions":["ahp-root://"]}}`))
	conn.Write(frame)
	testkit.Eventually(t, "the slow client to subscribe", func() bool { return h.SubscriberCount(wire.RootChannel) == 1 })
	// Large frames: a few thousand tiny ones can vanish into the kernel's socket buffers without
	// ever blocking the writer, which would make this test depend on the machine.
	blob := strings.Repeat("x", 32<<10)
	for i := 0; i < 400 && h.SubscriberCount(wire.RootChannel) == 1; i++ {
		h.DispatchServerAction(wire.RootChannel, ahptypes.StateAction{Value: &ahptypes.RootConfigChangedAction{
			Type: ahptypes.ActionTypeRootConfigChanged, Config: map[string]json.RawMessage{"blob": json.RawMessage(`"` + blob + `"`)},
		}})
	}
	testkit.Eventually(t, "the host to drop the peer that stopped reading", func() bool { return h.SubscriberCount(wire.RootChannel) == 0 })
}

func TestServerCloseEndsConnections(t *testing.T) {
	_, s := serve(t, ws.Options{})
	c, _, err := ws.Dial(url(s, ""), nil)
	if err != nil {
		t.Fatal(err)
	}
	initialize(t, c, "closing")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReadMessage(2 * time.Second); err == nil {
		t.Fatal("the peer must see the connection end")
	}
}
