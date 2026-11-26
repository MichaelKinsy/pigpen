package relay

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// upstream: packages/coding-agent/test/tools/browser-relay-server.test.ts,
//           packages/coding-agent/test/tools/browser-relay-probe.test.ts

var testExtHello = map[string]any{
	"t":                     "hello",
	"userAgent":             "test",
	"browserVersion":        "Chrome/151.0.0.0",
	"discardedTabsProtocol": 1,
	"tabs":                  []any{},
	"attachedTabIds":        []any{},
}

var testLegacyHello = map[string]any{
	"t":              "hello",
	"userAgent":      "test",
	"browserVersion": "Chrome/151.0.0.0",
	"tabs":           []any{},
	"attachedTabIds": []any{},
}

func startPublishedRelayServer(t *testing.T, opts RelayServerOptions) *RelayServer {
	t.Helper()
	srv, err := StartRelayServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	unpub, err := PublishRelayEndpoint(srv.Port, srv.AuthToken)
	if err != nil {
		srv.Stop()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = unpub()
		srv.Stop()
	})
	return srv
}

func dialExtAndHello(t *testing.T, srv *RelayServer, hello map[string]any, origin string) *WSConn {
	t.Helper()
	h := http.Header{}
	h.Set("Authorization", "Bearer "+srv.AuthToken)
	if origin != "" {
		h.Set("Origin", origin)
	}
	ws, err := DialWS(context.Background(), fmt.Sprintf("ws://127.0.0.1:%d/ext", srv.Port), &WSDialOptions{Headers: h})
	if err != nil {
		t.Fatalf("dial /ext failed: %v", err)
	}
	data, _ := json.Marshal(hello)
	if err := ws.WriteText(data); err != nil {
		_ = ws.Close()
		t.Fatalf("send hello failed: %v", err)
	}
	// Send a call and await callResult so we know the bridge processed hello synchronously.
	call, _ := json.Marshal(map[string]any{"t": "call", "id": 999, "call": map[string]any{"op": "collabHosts"}})
	if err := ws.WriteText(call); err != nil {
		_ = ws.Close()
		t.Fatalf("send call failed: %v", err)
	}
	for {
		raw, err := ws.ReadMessage()
		if err != nil {
			_ = ws.Close()
			t.Fatalf("wait hello ack failed: %v", err)
		}
		if strings.Contains(string(raw), `"callResult"`) {
			break
		}
	}
	return ws
}

func authGet(t *testing.T, url, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func rawHTTPGet(t *testing.T, port int, rawReq string) (*http.Response, string) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := io.WriteString(conn, rawReq); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(body)
}

func TestRelayServerDiscoveryAndOriginChecks(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", tmp)
	t.Setenv("HOME", tmp)

	srv := startPublishedRelayServer(t, RelayServerOptions{Port: 0})
	authHdr := http.Header{"Authorization": []string{"Bearer " + srv.AuthToken}}

	// Before extension connects: 503 with ompRelayCdpClients = 0.
	resp := authGet(t, fmt.Sprintf("http://127.0.0.1:%d/json/version", srv.Port), srv.AuthToken)
	var info503 map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&info503)
	_ = resp.Body.Close()
	if resp.StatusCode != 503 || *RelayCdpClientsOf(info503) != 0 {
		t.Fatalf("expected 503 with 0 clients, got %d %+v", resp.StatusCode, info503)
	}

	// Origin checks (upstream: server.ts:151-185) even WITH a valid bearer token -> 403.
	evilHdr := http.Header{
		"Origin":        []string{"https://evil.example"},
		"Authorization": []string{"Bearer " + srv.AuthToken},
	}
	if _, err := DialWS(context.Background(), fmt.Sprintf("ws://127.0.0.1:%d/cdp", srv.Port), &WSDialOptions{Headers: evilHdr}); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("expected /cdp with Origin to be 403, got %v", err)
	}
	if _, err := DialWS(context.Background(), fmt.Sprintf("ws://127.0.0.1:%d/ext", srv.Port), &WSDialOptions{Headers: evilHdr}); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("expected /ext with non-extension Origin to be 403, got %v", err)
	}
	for _, p := range []string{RelayRecheckPath, RelayRestartPath} {
		req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d%s", srv.Port, p), nil)
		req.Header.Set("Origin", "https://evil.example")
		req.Header.Set("Authorization", "Bearer "+srv.AuthToken)
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = r.Body.Close()
		if r.StatusCode != http.StatusForbidden {
			t.Fatalf("expected POST %s with Origin to be 403, got %d", p, r.StatusCode)
		}
	}

	// Connect a CDP client while still 503 -> ompRelayCdpClients = 1.
	cdpWS, err := DialWS(context.Background(), fmt.Sprintf("ws://127.0.0.1:%d/cdp", srv.Port), &WSDialOptions{Headers: authHdr})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && srv.Bridge.CDPClientCount() != 1 {
		time.Sleep(10 * time.Millisecond)
	}
	resp = authGet(t, fmt.Sprintf("http://127.0.0.1:%d/json/version", srv.Port), srv.AuthToken)
	info503 = nil
	_ = json.NewDecoder(resp.Body).Decode(&info503)
	_ = resp.Body.Close()
	if resp.StatusCode != 503 || *RelayCdpClientsOf(info503) != 1 {
		t.Fatalf("expected 503 with 1 client, got %d %+v", resp.StatusCode, info503)
	}

	// Connect extension -> 200 with 1 client.
	extWS := dialExtAndHello(t, srv, testExtHello, "chrome-extension://"+strings.Repeat("a", 32))
	defer extWS.Close()

	// Host header authority tests (upstream: browser-relay-server.test.ts:114-142).
	authLine := "Authorization: Bearer " + srv.AuthToken + "\r\n"
	cases := []struct {
		name string
		req  string
		want string
	}{
		{
			name: "advertises requested Host",
			req:  "GET /json/version HTTP/1.1\r\nHost: 100.100.92.97:12803\r\n" + authLine + "Connection: close\r\n\r\n",
			want: "ws://100.100.92.97:12803/cdp",
		},
		{
			name: "HTTP/1.0 no Host falls back to loopback",
			req:  "GET /json/version HTTP/1.0\r\n" + authLine + "\r\n",
			want: fmt.Sprintf("ws://127.0.0.1:%d/cdp", srv.Port),
		},
		{
			name: "empty Host falls back to loopback",
			req:  "GET /json/version HTTP/1.1\r\nHost: \r\n" + authLine + "Connection: close\r\n\r\n",
			want: fmt.Sprintf("ws://127.0.0.1:%d/cdp", srv.Port),
		},
		{
			name: "whitespace Host falls back to loopback",
			req:  "GET /json/version HTTP/1.1\r\nHost:    \r\n" + authLine + "Connection: close\r\n\r\n",
			want: fmt.Sprintf("ws://127.0.0.1:%d/cdp", srv.Port),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, body := rawHTTPGet(t, srv.Port, tc.req)
			if r.StatusCode != 200 {
				t.Fatalf("expected 200, got %d (%s)", r.StatusCode, body)
			}
			var parsed map[string]any
			if err := json.Unmarshal([]byte(body), &parsed); err != nil {
				t.Fatal(err)
			}
			gotWS, _ := parsed["webSocketDebuggerUrl"].(string)
			if gotWS != tc.want {
				t.Fatalf("webSocketDebuggerUrl = %q, want %q", gotWS, tc.want)
			}
			// webSocketDebuggerUrl must never carry the auth token.
			if strings.Contains(gotWS, srv.AuthToken) || strings.Contains(gotWS, "token=") {
				t.Fatalf("webSocketDebuggerUrl leaked token: %q", gotWS)
			}
		})
	}
	if isWSAuthority("bad/host@evil") || isWSAuthority("") || isWSAuthority("host with space") {
		t.Fatal("isWSAuthority accepted invalid authority")
	}
	if !isWSAuthority("100.100.92.97:12803") || !isWSAuthority("[::1]:9222") {
		t.Fatal("isWSAuthority rejected valid authority")
	}
	rBad, _ := rawHTTPGet(t, srv.Port, "GET /json/version HTTP/1.1\r\nHost: bad/host@evil\r\n"+authLine+"Connection: close\r\n\r\n")
	if rBad.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected net/http 400 on malformed Host, got %d", rBad.StatusCode)
	}

	_ = cdpWS.Close()
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) && srv.Bridge.CDPClientCount() != 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if srv.Bridge.CDPClientCount() != 0 {
		t.Fatalf("expected 0 CDP clients after close, got %d", srv.Bridge.CDPClientCount())
	}
}

func TestRelayServerBearerAuthAndEndpointPermissions(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", tmp)
	t.Setenv("HOME", tmp)

	srv1 := startPublishedRelayServer(t, RelayServerOptions{Port: 0})
	srv2 := startPublishedRelayServer(t, RelayServerOptions{Port: 0})
	ext1 := dialExtAndHello(t, srv1, testExtHello, "")
	defer ext1.Close()

	base1 := fmt.Sprintf("http://127.0.0.1:%d", srv1.Port)
	ws1 := fmt.Sprintf("ws://127.0.0.1:%d", srv1.Port)

	// Token length must be >= 128 bits (>= 32 hex chars).
	if len(srv1.AuthToken) < 32 {
		t.Fatalf("token too short: %d chars", len(srv1.AuthToken))
	}
	if srv1.AuthToken == srv2.AuthToken {
		t.Fatal("two relays must not share a token")
	}

	// 1. Every HTTP & WS endpoint returns 401 with no token, wrong token, or another relay's token.
	badTokens := []struct {
		name  string
		token string
	}{
		{"no token", ""},
		{"wrong token", strings.Repeat("0", 64)},
		{"other relay token", srv2.AuthToken},
	}
	for _, bt := range badTokens {
		t.Run(bt.name, func(t *testing.T) {
			for _, ep := range []struct{ method, path string }{
				{http.MethodGet, "/json/version"},
				{http.MethodGet, "/json"},
				{http.MethodGet, "/json/list"},
				{http.MethodPost, RelayRecheckPath},
				{http.MethodPost, RelayRestartPath},
			} {
				req, _ := http.NewRequest(ep.method, base1+ep.path, nil)
				if bt.token != "" {
					req.Header.Set("Authorization", "Bearer "+bt.token)
				}
				r, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				_ = r.Body.Close()
				if r.StatusCode != http.StatusUnauthorized {
					t.Fatalf("%s %s (%s): got %d, want 401", ep.method, ep.path, bt.name, r.StatusCode)
				}
			}
			for _, wsPath := range []string{"/cdp", "/ext"} {
				var opts *WSDialOptions
				if bt.token != "" {
					opts = &WSDialOptions{Headers: http.Header{"Authorization": []string{"Bearer " + bt.token}}}
				}
				if _, err := DialWS(context.Background(), ws1+wsPath, opts); err == nil || !strings.Contains(err.Error(), "401") {
					t.Fatalf("WS %s (%s): expected 401, got %v", wsPath, bt.name, err)
				}
			}
		})
	}

	// 2. sync's recheckRelay succeeds with the published token.
	var recheckHit atomic.Bool
	srvRecheck := startPublishedRelayServer(t, RelayServerOptions{
		Port: 0,
		InstalledStamp: func(string) (*RelayExtensionStamp, error) {
			recheckHit.Store(true)
			return nil, nil
		},
	})
	recheckExt := dialExtAndHello(t, srvRecheck, map[string]any{
		"t": "hello", "instanceId": "recheck-inst", "userAgent": "t", "browserVersion": "Chrome/151",
		"discardedTabsProtocol": 1, "reloadProtocol": 1, "ompVersion": RelayVersion,
		"tabs": []any{}, "attachedTabIds": []any{},
	}, "chrome-extension://"+strings.Repeat("a", 32))
	defer recheckExt.Close()
	recheckHit.Store(false)
	recheckRelay(fmt.Sprintf("http://127.0.0.1:%d", srvRecheck.Port))
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && !recheckHit.Load() {
		time.Sleep(10 * time.Millisecond)
	}
	if !recheckHit.Load() {
		t.Fatal("recheckRelay did not authenticate and trigger bridge.RecheckExtensions()")
	}
	if runtime.GOOS != "windows" {
		epFile := filepath.Join(EndpointsDir(), fmt.Sprintf("%d-%d", os.Getpid(), srv1.Port))
		if err := os.Chmod(epFile, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := RelayTokenForURL(base1); err == nil || !strings.Contains(err.Error(), "unsafe permissions") {
			t.Fatalf("expected unsafe permissions error for 0644 endpoint file, got %v", err)
		}
		for _, u := range DiscoverRelayEndpoints() {
			if u == base1 {
				t.Fatalf("DiscoverRelayEndpoints trusted 0644 endpoint file %s", epFile)
			}
		}
		_ = os.Chmod(epFile, 0o600)
		if tok, err := RelayTokenForURL(base1); err != nil || tok != srv1.AuthToken {
			t.Fatalf("expected restored 0600 file to succeed, got tok=%q err=%v", tok, err)
		}

		// Directory with 0755 is also rejected.
		if err := os.Chmod(EndpointsDir(), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := RelayTokenForURL(base1); err == nil || !strings.Contains(err.Error(), "unsafe permissions") {
			t.Fatalf("expected unsafe permissions error for 0755 dir, got %v", err)
		}
		_ = os.Chmod(EndpointsDir(), 0o700)
	}
}

func TestWaitForRelayExtensionOutcomes(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", tmp)
	t.Setenv("HOME", tmp)

	origVer := RelayVersion
	RelayVersion = "19.1.1"
	defer func() { RelayVersion = origVer }()

	// 1. Unreachable returns immediately.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	unusedPort := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	start := time.Now()
	out, err := WaitForRelayExtension(context.Background(), fmt.Sprintf("http://127.0.0.1:%d", unusedPort), nil)
	if err != nil || out != RelayOutcomeUnreachable {
		t.Fatalf("expected unreachable, got %q err=%v", out, err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("unreachable took too long: %v", time.Since(start))
	}

	// 2. Restarted waits for relay to come up and say hello.
	waitDone := make(chan RelayWaitOutcome, 1)
	go func() {
		o, _ := WaitForRelayExtension(context.Background(), fmt.Sprintf("http://127.0.0.1:%d", unusedPort), &WaitOptions{Restarted: true})
		waitDone <- o
	}()
	time.Sleep(250 * time.Millisecond)
	srv := startPublishedRelayServer(t, RelayServerOptions{Port: unusedPort})
	ext := dialExtAndHello(t, srv, testExtHello, "")
	defer ext.Close()
	if got := <-waitDone; got != RelayOutcomeReady {
		t.Fatalf("expected ready after restart, got %q", got)
	}

	// 3. Fake /json/version outcomes (upstream: browser-relay-probe.test.ts:64-130).
	cases := []struct {
		name    string
		payload map[string]any
		want    RelayWaitOutcome
	}{
		{
			name:    "no discarded-tab metadata",
			payload: map[string]any{"Browser": "Chrome/151"},
			want:    RelayOutcomeOutdatedRelay,
		},
		{
			name: "stale relay before blaming extension",
			payload: map[string]any{
				"ompRelayVersion":                   "18.5.1",
				"ompRelayDiscardedTabsProtocol":     "1",
				"ompExtensionDiscardedTabsProtocol": "0",
			},
			want: RelayOutcomeOutdatedRelay,
		},
		{
			name: "compatible older relay without client count",
			payload: map[string]any{
				"ompRelayVersion":                   "18.5.1",
				"ompRelayDiscardedTabsProtocol":     "1",
				"ompExtensionDiscardedTabsProtocol": "1",
			},
			want: RelayOutcomeReady,
		},
		{
			name: "idle older relay",
			payload: map[string]any{
				"ompRelayVersion":                   "18.5.1",
				"ompRelayDiscardedTabsProtocol":     "1",
				"ompExtensionDiscardedTabsProtocol": "1",
				"ompRelayCdpClients":                0,
			},
			want: RelayOutcomeIdleOlderRelay,
		},
		{
			name: "busy older relay",
			payload: map[string]any{
				"ompRelayVersion":                   "18.5.1",
				"ompRelayDiscardedTabsProtocol":     "1",
				"ompExtensionDiscardedTabsProtocol": "1",
				"ompRelayCdpClients":                1,
			},
			want: RelayOutcomeReady,
		},
		{
			name: "newer relay",
			payload: map[string]any{
				"ompRelayVersion":                   "999.0.0",
				"ompRelayDiscardedTabsProtocol":     "1",
				"ompExtensionDiscardedTabsProtocol": "1",
				"ompRelayCdpClients":                0,
			},
			want: RelayOutcomeReady,
		},
		{
			name: "same version relay",
			payload: map[string]any{
				"ompRelayVersion":                   "19.1.1",
				"ompRelayDiscardedTabsProtocol":     "1",
				"ompExtensionDiscardedTabsProtocol": "1",
				"ompRelayCdpClients":                0,
			},
			want: RelayOutcomeReady,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, 200, tc.payload)
			}))
			defer fake.Close()
			got, err := WaitForRelayExtension(context.Background(), fake.URL, nil)
			if err != nil || got != tc.want {
				t.Fatalf("got %q (err=%v), want %q", got, err, tc.want)
			}
		})
	}

	// 4. Legacy extension without discardedTabsProtocol -> outdated-extension.
	srv2 := startPublishedRelayServer(t, RelayServerOptions{Port: 0})
	legExt := dialExtAndHello(t, srv2, testLegacyHello, "")
	defer legExt.Close()
	got, err := WaitForRelayExtension(context.Background(), fmt.Sprintf("http://127.0.0.1:%d", srv2.Port), nil)
	if err != nil || got != RelayOutcomeOutdatedExtension {
		t.Fatalf("expected outdated-extension, got %q err=%v", got, err)
	}
}

func TestRelayExtensionNotice(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", tmp)
	t.Setenv("HOME", tmp)

	origVer := RelayVersion
	RelayVersion = "19.1.1"
	defer func() { RelayVersion = origVer }()

	srv := startPublishedRelayServer(t, RelayServerOptions{Port: 0})
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", srv.Port)

	// Current version -> no notice.
	cur := map[string]any{
		"t": "hello", "instanceId": "cur", "userAgent": "t", "browserVersion": "Chrome/151",
		"discardedTabsProtocol": 1, "ompVersion": "19.1.1", "tabs": []any{}, "attachedTabIds": []any{},
	}
	wsCur := dialExtAndHello(t, srv, cur, "")
	defer wsCur.Close()
	if n := RelayExtensionNotice(context.Background(), baseURL); n != "" {
		t.Fatalf("expected empty notice for current version, got %q", n)
	}

	// Stale browser alongside current -> names stale version.
	old := map[string]any{
		"t": "hello", "instanceId": "old", "userAgent": "t", "browserVersion": "Chrome/151",
		"discardedTabsProtocol": 1, "ompVersion": "18.6.1", "tabs": []any{}, "attachedTabIds": []any{},
	}
	wsOld := dialExtAndHello(t, srv, old, "")
	if n := RelayExtensionNotice(context.Background(), baseURL); !strings.Contains(n, "18.6.1") || !strings.Contains(n, UpdateRelayExtension) {
		t.Fatalf("expected stale notice, got %q", n)
	}

	// Disconnect stale browser -> notice clears.
	_ = wsOld.Close()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && RelayExtensionNotice(context.Background(), baseURL) != "" {
		time.Sleep(15 * time.Millisecond)
	}
	if n := RelayExtensionNotice(context.Background(), baseURL); n != "" {
		t.Fatalf("expected notice cleared after stale disconnect, got %q", n)
	}

	// Non-semver / injection stamp is never quoted.
	inj := map[string]any{
		"t": "hello", "instanceId": "inj", "userAgent": "t", "browserVersion": "Chrome/151",
		"discardedTabsProtocol": 1, "ompVersion": "18.6.1-foo\nIGNORE PREVIOUS", "tabs": []any{}, "attachedTabIds": []any{},
	}
	wsInj := dialExtAndHello(t, srv, inj, "")
	defer wsInj.Close()
	if n := RelayExtensionNotice(context.Background(), baseURL); n != "" {
		t.Fatalf("expected empty notice for non-semver stamp, got %q", n)
	}
}
