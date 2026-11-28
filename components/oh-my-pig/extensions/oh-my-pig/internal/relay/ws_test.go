package relay

import (
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWebSocketServerClientRoundTripAndLargePayload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, attempted, err := UpgradeWS(w, r, DefaultMaxWSMessageBytes)
		if !attempted {
			w.WriteHeader(http.StatusUpgradeRequired)
			return
		}
		if err != nil {
			return
		}
		defer ws.Close()
		// Send a ping before echoing messages.
		_ = ws.WritePing([]byte("hi"))
		for {
			msg, err := ws.ReadMessage()
			if err != nil {
				return
			}
			if err := ws.WriteText(msg); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	// Non-upgrade HTTP request gets 426.
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUpgradeRequired {
		t.Fatalf("expected 426, got %d", resp.StatusCode)
	}

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	client, err := DialWS(context.Background(), wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	// Small, medium (>125), and large (>65535) payloads exercise all 3 length encodings.
	for _, size := range []int{5, 1024, 128 * 1024} {
		want := strings.Repeat("a", size)
		if err := client.WriteText([]byte(want)); err != nil {
			t.Fatalf("write size %d: %v", size, err)
		}
		got, err := client.ReadMessage()
		if err != nil {
			t.Fatalf("read size %d: %v", size, err)
		}
		if string(got) != want {
			t.Fatalf("size %d mismatch: got len %d", size, len(got))
		}
	}
}

func TestWebSocketServerRejectsUnmaskedClientFrameAndHandlesFragmentation(t *testing.T) {
	errCh := make(chan error, 1)
	msgCh := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, _, err := UpgradeWS(w, r, 1024)
		if err != nil || ws == nil {
			return
		}
		defer ws.Close()
		msg, err := ws.ReadMessage()
		if err != nil {
			errCh <- err
			return
		}
		msgCh <- string(msg)
	}))
	defer srv.Close()

	// 1. Send unmasked frame from client -> server must reject per RFC 6455 §5.1.
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	c1, err := DialWS(context.Background(), wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Bypass client masking by writing raw unmasked frame directly to underlying conn.
	_, _ = c1.conn.Write([]byte{0x81, 0x02, 'h', 'i'})
	select {
	case err := <-errCh:
		if err == nil || !strings.Contains(err.Error(), "unmasked") {
			t.Fatalf("expected unmasked error, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for unmasked rejection")
	}
	_ = c1.Close()

	// 2. Fragmented masked message with interleaved ping from client.
	c2, err := DialWS(context.Background(), wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	writeMasked := func(fin bool, opcode byte, payload []byte) {
		var hdr [6]byte
		if fin {
			hdr[0] = 0x80 | opcode
		} else {
			hdr[0] = opcode
		}
		hdr[1] = 0x80 | byte(len(payload))
		mask := [4]byte{1, 2, 3, 4}
		copy(hdr[2:6], mask[:])
		out := make([]byte, 6+len(payload))
		copy(out[:6], hdr[:])
		for i, b := range payload {
			out[6+i] = b ^ mask[i&3]
		}
		_, _ = c2.conn.Write(out)
	}
	writeMasked(false, 0x1, []byte("hello "))
	writeMasked(true, 0x9, []byte("ping"))
	writeMasked(true, 0x0, []byte("world"))

	select {
	case got := <-msgCh:
		if got != "hello world" {
			t.Fatalf("expected 'hello world', got %q", got)
		}
	case err := <-errCh:
		t.Fatalf("unexpected error: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for fragmented message")
	}

	// 3. Oversized frame (>1024 byte limit) rejected without reading body.
	c3, err := DialWS(context.Background(), wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c3.Close()
	var hdr [14]byte
	hdr[0] = 0x81
	hdr[1] = 0x80 | 127
	binary.BigEndian.PutUint64(hdr[2:10], 10*1024*1024)
	_, _ = c3.conn.Write(hdr[:])
	select {
	case err := <-errCh:
		if err == nil || !strings.Contains(err.Error(), "limit") {
			t.Fatalf("expected limit error, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for oversized rejection")
	}
	_ = io.EOF
}
