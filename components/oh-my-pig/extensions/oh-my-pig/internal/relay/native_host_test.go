package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// upstream: packages/coding-agent/test/tools/browser-relay-native-host.test.ts

type nativeHostHarness struct {
	t        *testing.T
	pw       *io.PipeWriter
	mu       sync.Mutex
	received []FromNativeHost
	notify   chan struct{}
	done     chan error
}

func startNativeHostHarness(t *testing.T, relayPort int) *nativeHostHarness {
	t.Helper()
	pr, pw := io.Pipe()
	h := &nativeHostHarness{
		t:      t,
		pw:     pw,
		notify: make(chan struct{}, 64),
		done:   make(chan error, 1),
	}
	var reader NativeFrameReader
	go func() {
		err := ServeNativeHost(context.Background(), NativeHostServeOptions{
			Input: pr,
			Write: func(frame []byte) error {
				if len(frame)-4 > NativeFrameLimit {
					t.Errorf("frame body %d exceeds NativeFrameLimit %d", len(frame)-4, NativeFrameLimit)
				}
				msgs, err := reader.Push(frame)
				if err != nil {
					t.Errorf("frame decode failed: %v", err)
					return err
				}
				h.mu.Lock()
				for _, raw := range msgs {
					var m FromNativeHost
					if err := json.Unmarshal(raw, &m); err != nil {
						t.Errorf("unmarshal FromNativeHost: %v", err)
						continue
					}
					h.received = append(h.received, m)
				}
				h.mu.Unlock()
				select {
				case h.notify <- struct{}{}:
				default:
				}
				return nil
			},
			RelayPort: relayPort,
			Log:       func(string) {},
		})
		h.done <- err
	}()
	return h
}

func (h *nativeHostHarness) send(msg any) {
	frame, err := EncodeNativeFrame(msg)
	if err != nil {
		h.t.Fatal(err)
	}
	// Split across two writes so NativeFrameReader must reassemble.
	_, _ = h.pw.Write(frame[:3])
	_, _ = h.pw.Write(frame[3:])
}

func (h *nativeHostHarness) disconnect() {
	_ = h.pw.Close()
}

func (h *nativeHostHarness) receivedLen() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.received)
}

func (h *nativeHostHarness) waitFor(from int, pred func(FromNativeHost) bool) FromNativeHost {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		h.mu.Lock()
		for i := from; i < len(h.received); i++ {
			if pred(h.received[i]) {
				m := h.received[i]
				h.mu.Unlock()
				return m
			}
		}
		h.mu.Unlock()
		if time.Now().After(deadline) {
			h.t.Fatal("timed out waiting for native host message")
		}
		select {
		case <-h.notify:
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func (h *nativeHostHarness) linkAndHello(instanceID string, from int) {
	h.t.Helper()
	h.waitFor(from, func(m FromNativeHost) bool { return m.T == "linked" })
	hello, _ := json.Marshal(map[string]any{
		"t":                     "hello",
		"instanceId":            instanceID,
		"userAgent":             "test",
		"browserVersion":        "Chrome/151.0.0.0",
		"tabs":                  []any{},
		"attachedTabIds":        []any{},
		"discardedTabsProtocol": 1,
	})
	s := string(hello)
	h.send(ToNativeHost{T: "relay", Line: &s})
}

func freeLocalPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return p
}

func relayInstanceID(t *testing.T, port int) string {
	t.Helper()
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	tok, err := RelayTokenForURL(base)
	if err != nil {
		t.Fatalf("RelayTokenForURL(%s): %v", base, err)
	}
	req, _ := http.NewRequest(http.MethodGet, base+"/json/version", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	s, _ := body["ompRelayInstance"].(string)
	return s
}

func TestNativeHostOpenRefusedAndInvalidFirstMessage(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", tmp)
	t.Setenv("HOME", tmp)

	// `open` branch is out of scope and must return a clear exit refusal.
	h := startNativeHostHarness(t, 0)
	cwd := tmp
	extID := strings.Repeat("a", 32)
	h.send(ToNativeHost{T: "open", Cwd: &cwd, ExtensionID: &extID})
	exitMsg := h.waitFor(0, func(m FromNativeHost) bool { return m.T == "exit" })
	if exitMsg.Code != nil || !strings.Contains(exitMsg.Stderr, "side-panel chat") {
		t.Fatalf("unexpected open refusal: %+v", exitMsg)
	}
	<-h.done

	// Non-open/non-link first message is refused.
	h2 := startNativeHostHarness(t, 0)
	line := "{}"
	h2.send(ToNativeHost{T: "relay", Line: &line})
	exit2 := h2.waitFor(0, func(m FromNativeHost) bool { return m.T == "exit" })
	if exit2.Code != nil || !strings.Contains(exit2.Stderr, "first message must be") {
		t.Fatalf("unexpected first-message refusal: %+v", exit2)
	}
	<-h2.done
}

func TestNativeHostPublishesAndRetiresEndpoint(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", tmp)
	t.Setenv("HOME", tmp)

	before := DiscoverRelayEndpoints()
	h := startNativeHostHarness(t, 0)
	extID := strings.Repeat("a", 32)
	h.send(ToNativeHost{T: "link", ExtensionID: &extID})
	h.linkAndHello("dynamic-browser", 0)

	var published string
	for _, ep := range DiscoverRelayEndpoints() {
		if !slices.Contains(before, ep) {
			published = ep
			break
		}
	}
	if published == "" {
		t.Fatal("relay did not publish an endpoint")
	}
	out, err := WaitForRelayExtension(context.Background(), published, nil)
	if err != nil || out != RelayOutcomeReady {
		t.Fatalf("expected ready at %s, got %q err=%v", published, out, err)
	}
	h.disconnect()
	<-h.done
	if slices.Contains(DiscoverRelayEndpoints(), published) {
		t.Fatalf("expected endpoint %s retired after disconnect", published)
	}
}

func TestNativeHostLeaderFollowerAndHandover(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", tmp)
	t.Setenv("HOME", tmp)

	port := freeLocalPort(t)
	cdpURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	first := startNativeHostHarness(t, port)
	extA := strings.Repeat("a", 32)
	first.send(ToNativeHost{T: "link", ExtensionID: &extA})
	first.linkAndHello("first", 0)
	if out, err := WaitForRelayExtension(context.Background(), cdpURL, nil); err != nil || out != RelayOutcomeReady {
		t.Fatalf("leader not ready: %q %v", out, err)
	}
	leaderInst := relayInstanceID(t, port)

	second := startNativeHostHarness(t, port)
	extB := strings.Repeat("b", 32)
	second.send(ToNativeHost{T: "link", ExtensionID: &extB})
	second.linkAndHello("second", 0)

	// Follower call reaches leader's bridge and comes back on follower's port.
	callLine := `{"t":"call","id":7,"call":{"op":"collabHosts"}}`
	second.send(ToNativeHost{T: "relay", Line: &callLine})
	second.waitFor(0, func(m FromNativeHost) bool {
		return m.T == "relay" && strings.Contains(m.Line, `"t":"callResult"`) && strings.Contains(m.Line, `"id":7`)
	})

	// Leader leaves -> follower gets unlinked, promotes to leader, gets linked.
	mark := second.receivedLen()
	first.disconnect()
	<-first.done
	second.waitFor(mark, func(m FromNativeHost) bool { return m.T == "unlinked" })
	second.linkAndHello("second", mark)
	if out, err := WaitForRelayExtension(context.Background(), cdpURL, nil); err != nil || out != RelayOutcomeReady {
		t.Fatalf("promoted leader not ready: %q %v", out, err)
	}
	if newInst := relayInstanceID(t, port); newInst == leaderInst {
		t.Fatalf("expected new relay instance after handover, still %q", newInst)
	}

	second.disconnect()
	<-second.done
	if out, err := WaitForRelayExtension(context.Background(), cdpURL, nil); err != nil || out != RelayOutcomeUnreachable {
		t.Fatalf("expected unreachable after second disconnect, got %q %v", out, err)
	}
}

func TestNativeHostRestartHandshake(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", tmp)
	t.Setenv("HOME", tmp)

	origVer := RelayVersion
	RelayVersion = "19.1.1"
	defer func() { RelayVersion = origVer }()

	port := freeLocalPort(t)
	cdpURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	h := startNativeHostHarness(t, port)
	extID := strings.Repeat("a", 32)
	h.send(ToNativeHost{T: "link", ExtensionID: &extID})
	h.linkAndHello("browser", 0)
	if out, _ := WaitForRelayExtension(context.Background(), cdpURL, nil); out != RelayOutcomeReady {
		t.Fatalf("expected ready, got %q", out)
	}

	// Same version restart is refused (409 -> false).
	if ok, err := RequestRelayRestart(context.Background(), cdpURL, false); err != nil || ok {
		t.Fatalf("expected same-version restart refused, got ok=%v err=%v", ok, err)
	}

	// Newer version restart is accepted (202) and host exits.
	tok, err := RelayTokenForURL(cdpURL)
	if err != nil {
		t.Fatalf("RelayTokenForURL(%s): %v", cdpURL, err)
	}
	req, _ := http.NewRequest(http.MethodPost, cdpURL+RelayRestartPath+"?version=999.0.0", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.StatusCode)
	}
	<-h.done
	if out, _ := WaitForRelayExtension(context.Background(), cdpURL, nil); out != RelayOutcomeUnreachable {
		t.Fatalf("expected unreachable after restart exit, got %q", out)
	}
}

func TestRunNativeHostOverPipes(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", tmp)
	t.Setenv("HOME", tmp)

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	var stderr strings.Builder
	exitCh := make(chan int, 1)
	go func() {
		exitCh <- RunNativeHost(context.Background(), HostIO{
			Stdin:  inR,
			Stdout: outW,
			Stderr: &stderr,
			Getenv: func(string) string { return "" },
		})
		_ = outW.Close()
	}()

	extID := strings.Repeat("c", 32)
	frame, _ := EncodeNativeFrame(ToNativeHost{T: "link", ExtensionID: &extID})
	_, _ = inW.Write(frame)

	buf := make([]byte, 4096)
	var fr NativeFrameReader
	n, err := outR.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	msgs, err := fr.Push(buf[:n])
	if err != nil || len(msgs) == 0 {
		t.Fatalf("expected linked frame, got %d err=%v", len(msgs), err)
	}
	var linked FromNativeHost
	_ = json.Unmarshal(msgs[0], &linked)
	if linked.T != "linked" {
		t.Fatalf("expected linked, got %+v", linked)
	}
	_ = inW.Close()
	if code := <-exitCh; code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%q)", code, stderr.String())
	}
}
