package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/relay"
)

// fakeRelayExtension speaks the Chrome extension side of the native-host framing
// protocol over pipes (`link` -> `linked` -> `hello` -> scripted CDP replies) so
// tests exercise the entire chain:
//
//	fake extension -> native host stdio framing -> leader/follower election ->
//	relay HTTP/WS server -> RelayBridge -> browser.RelayBackend / Manager
type fakeRelayExtension struct {
	t          *testing.T
	instanceID string
	extID      string
	relayPort  int

	mu            sync.Mutex
	tabs          map[int]*fakeExtTab
	nextTabID     int
	attached      map[int]bool
	receivedRPCs  []relay.RelayToExtMessage
	hostInW       *io.PipeWriter
	hostDone      chan error
	linkedCh      chan struct{}
	unlinkedCh    chan string
	chunkBuf      map[int][]string
	clickCount    int
	lastMouseDown map[string]any
}

type fakeExtTab struct {
	snap  relay.TabSnapshot
	text  string
	title string
}

// 1x1 valid PNG (base64).
const tinyPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

func startFakeRelayExtension(t *testing.T, instanceID, extID string, relayPort int, initialTabs []fakeExtTab) *fakeRelayExtension {
	t.Helper()
	fe := &fakeRelayExtension{
		t:          t,
		instanceID: instanceID,
		extID:      extID,
		relayPort:  relayPort,
		tabs:       make(map[int]*fakeExtTab),
		nextTabID:  100,
		attached:   make(map[int]bool),
		linkedCh:   make(chan struct{}, 16),
		unlinkedCh: make(chan string, 16),
		chunkBuf:   make(map[int][]string),
	}
	for _, tab := range initialTabs {
		tc := tab
		fe.tabs[tc.snap.TabID] = &tc
		if tc.snap.TabID >= fe.nextTabID {
			fe.nextTabID = tc.snap.TabID + 1
		}
	}
	fe.connectHost()
	return fe
}

func (fe *fakeRelayExtension) connectHost() {
	inR, inW := io.Pipe()
	fe.mu.Lock()
	fe.hostInW = inW
	fe.hostDone = make(chan error, 1)
	fe.mu.Unlock()

	var fr relay.NativeFrameReader
	go func() {
		err := relay.ServeNativeHost(context.Background(), relay.NativeHostServeOptions{
			Input: inR,
			Write: func(frame []byte) error {
				msgs, err := fr.Push(frame)
				if err != nil {
					return err
				}
				for _, raw := range msgs {
					var m relay.FromNativeHost
					if err := json.Unmarshal(raw, &m); err != nil {
						continue
					}
					fe.handleFromHost(m)
				}
				return nil
			},
			RelayPort: fe.relayPort,
			Log:       func(string) {},
		})
		fe.hostDone <- err
	}()

	extID := fe.extID
	fe.sendToHost(relay.ToNativeHost{T: "link", ExtensionID: &extID})
}

func (fe *fakeRelayExtension) disconnectHost() {
	fe.mu.Lock()
	w := fe.hostInW
	done := fe.hostDone
	fe.mu.Unlock()
	if w != nil {
		_ = w.Close()
	}
	if done != nil {
		<-done
	}
}

func (fe *fakeRelayExtension) waitLinked() {
	fe.t.Helper()
	select {
	case <-fe.linkedCh:
	case <-time.After(5 * time.Second):
		fe.t.Fatalf("fakeRelayExtension %q timed out waiting for linked", fe.instanceID)
	}
}

func (fe *fakeRelayExtension) sendToHost(msg any) {
	frame, err := relay.EncodeNativeFrame(msg)
	if err != nil {
		fe.t.Errorf("encode native frame: %v", err)
		return
	}
	fe.mu.Lock()
	w := fe.hostInW
	fe.mu.Unlock()
	if w != nil {
		_, _ = w.Write(frame)
	}
}

func (fe *fakeRelayExtension) sendRelayMsg(payload any) {
	b, _ := json.Marshal(payload)
	line := string(b)
	fe.sendToHost(relay.ToNativeHost{T: "relay", Line: &line})
}

func (fe *fakeRelayExtension) sendHello() {
	fe.mu.Lock()
	snaps := make([]relay.TabSnapshot, 0, len(fe.tabs))
	for _, tab := range fe.tabs {
		snaps = append(snaps, tab.snap)
	}
	attached := make([]int, 0, len(fe.attached))
	for tid := range fe.attached {
		attached = append(attached, tid)
	}
	fe.mu.Unlock()

	ver := relay.RelayVersion
	fe.sendRelayMsg(map[string]any{
		"t":                     "hello",
		"instanceId":            fe.instanceID,
		"browserSession":        "sess-1",
		"userAgent":             "FakeChrome/150",
		"browserVersion":        "Chrome/150.0.0.0",
		"discardedTabsProtocol": relay.DiscardedTabsProtocolVersion,
		"downloadsProtocol":     relay.DownloadsProtocolVersion,
		"overlayProtocol":       relay.OverlayProtocolVersion,
		"reloadProtocol":        relay.ExtensionReloadProtocolVersion,
		"ompVersion":            ver,
		"tabs":                  snaps,
		"attachedTabIds":        attached,
	})
}

func (fe *fakeRelayExtension) handleFromHost(m relay.FromNativeHost) {
	switch m.T {
	case "linked":
		fe.sendHello()
		select {
		case fe.linkedCh <- struct{}{}:
		default:
		}
	case "unlinked":
		select {
		case fe.unlinkedCh <- m.Reason:
		default:
		}
	case "relay":
		fe.handleRelayLine(m.Line)
	case "chunk":
		if m.Relay {
			fe.mu.Lock()
			fe.chunkBuf[m.ID] = append(fe.chunkBuf[m.ID], m.Data)
			isLast := m.Last != nil && *m.Last
			var full string
			if isLast {
				full = strings.Join(fe.chunkBuf[m.ID], "")
				delete(fe.chunkBuf, m.ID)
			}
			fe.mu.Unlock()
			if isLast {
				fe.handleRelayLine(full)
			}
		}
	}
}

func (fe *fakeRelayExtension) handleRelayLine(line string) {
	var msg relay.RelayToExtMessage
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		return
	}
	fe.mu.Lock()
	fe.receivedRPCs = append(fe.receivedRPCs, msg)
	fe.mu.Unlock()
	if msg.T != "rpc" {
		return
	}
	go fe.handleRPC(msg)
}

func (fe *fakeRelayExtension) rpcOK(id int, res any) {
	if res == nil {
		res = map[string]any{}
	}
	fe.sendRelayMsg(map[string]any{"t": "rpcResult", "id": id, "ok": true, "result": res})
}

func (fe *fakeRelayExtension) rpcErr(id int, errMsg string) {
	fe.sendRelayMsg(map[string]any{"t": "rpcResult", "id": id, "ok": false, "error": errMsg})
}

func (fe *fakeRelayExtension) handleRPC(msg relay.RelayToExtMessage) {
	switch msg.Op {
	case "createTab":
		fe.mu.Lock()
		tid := fe.nextTabID
		fe.nextTabID++
		u := msg.URL
		if u == "" {
			u = "about:blank"
		}
		tab := &fakeExtTab{
			snap: relay.TabSnapshot{
				TabID:    tid,
				URL:      u,
				Title:    "New Tab",
				Active:   true,
				WindowID: 1,
				GroupID:  -1,
			},
			text:  "Initial page body",
			title: "New Tab",
		}
		fe.tabs[tid] = tab
		snap := tab.snap
		fe.mu.Unlock()
		fe.rpcOK(msg.ID, map[string]any{"tab": snap})

	case "removeTab":
		fe.mu.Lock()
		delete(fe.tabs, msg.TabID)
		delete(fe.attached, msg.TabID)
		fe.mu.Unlock()
		fe.sendRelayMsg(map[string]any{"t": "tabRemoved", "tabId": msg.TabID})
		fe.rpcOK(msg.ID, map[string]any{})

	case "attach":
		fe.mu.Lock()
		fe.attached[msg.TabID] = true
		fe.mu.Unlock()
		fe.rpcOK(msg.ID, map[string]any{})

	case "detach":
		fe.mu.Lock()
		delete(fe.attached, msg.TabID)
		fe.mu.Unlock()
		fe.sendRelayMsg(map[string]any{"t": "detached", "tabId": msg.TabID, "reason": "canceled_by_relay", "relayInitiated": true})
		fe.rpcOK(msg.ID, map[string]any{})

	case "group":
		grouped := map[string]int{}
		for _, tid := range msg.TabIDs {
			grouped[fmt.Sprintf("%d", tid)] = 99
		}
		fe.rpcOK(msg.ID, map[string]any{"grouped": grouped})

	case "ungroup", "overlay", "activateTab":
		fe.rpcOK(msg.ID, map[string]any{})

	case "findDownload":
		fe.mu.Lock()
		path := "/tmp/dl-" + msg.GUID + ".csv"
		fe.mu.Unlock()
		fe.rpcOK(msg.ID, map[string]any{"filename": path, "state": "complete"})

	case "send":
		fe.handleCDPSend(msg)
	}
}

func (fe *fakeRelayExtension) handleCDPSend(msg relay.RelayToExtMessage) {
	fe.mu.Lock()
	tab := fe.tabs[msg.TabID]
	fe.mu.Unlock()
	if tab == nil {
		fe.rpcErr(msg.ID, "tab not found")
		return
	}

	switch msg.Method {
	case "Page.enable", "Runtime.enable", "Runtime.disable", "Network.enable",
		"DOM.enable", "Inspector.enable", "Accessibility.enable",
		"Page.setLifecycleEventsEnabled", "Emulation.setFocusEmulationEnabled",
		"Emulation.setDeviceMetricsOverride", "Page.stopScreencast", "Page.bringToFront":
		if msg.Method == "Runtime.enable" {
			fe.sendRelayMsg(map[string]any{
				"t":      "cdpEvent",
				"tabId":  msg.TabID,
				"method": "Runtime.executionContextCreated",
				"params": map[string]any{
					"context": map[string]any{
						"id":      1,
						"name":    "",
						"auxData": map[string]any{"isDefault": true, "frameId": "main-frame"},
					},
				},
			})
		}
		fe.rpcOK(msg.ID, map[string]any{})

	case "Page.addScriptToEvaluateOnNewDocument":
		fe.rpcOK(msg.ID, map[string]any{"identifier": "script-1"})

	case "Page.createIsolatedWorld":
		fe.sendRelayMsg(map[string]any{
			"t":      "cdpEvent",
			"tabId":  msg.TabID,
			"method": "Runtime.executionContextCreated",
			"params": map[string]any{
				"context": map[string]any{
					"id":      2,
					"name":    kitWorldName,
					"auxData": map[string]any{"isDefault": false, "frameId": "main-frame"},
				},
			},
		})
		fe.rpcOK(msg.ID, map[string]any{"executionContextId": 2})

	case "Page.getFrameTree":
		fe.mu.Lock()
		u := tab.snap.URL
		fe.mu.Unlock()
		fe.rpcOK(msg.ID, map[string]any{
			"frameTree": map[string]any{
				"frame": map[string]any{
					"id":  "main-frame",
					"url": u,
				},
			},
		})

	case "Page.navigate":
		u, _ := msg.Params["url"].(string)
		fe.mu.Lock()
		tab.snap.URL = u
		tab.snap.Title = "Visited " + u
		tab.title = "Visited " + u
		tab.text = "Hello from " + u
		fe.mu.Unlock()
		fe.rpcOK(msg.ID, map[string]any{"frameId": "main-frame"})
		fe.sendRelayMsg(map[string]any{
			"t":      "cdpEvent",
			"tabId":  msg.TabID,
			"method": "Page.frameNavigated",
			"params": map[string]any{"frame": map[string]any{"id": "main-frame", "url": u}},
		})
		fe.sendRelayMsg(map[string]any{
			"t":      "cdpEvent",
			"tabId":  msg.TabID,
			"method": "Page.loadEventFired",
			"params": map[string]any{"timestamp": 1.0},
		})
		fe.sendRelayMsg(map[string]any{
			"t":      "cdpEvent",
			"tabId":  msg.TabID,
			"method": "Page.lifecycleEvent",
			"params": map[string]any{"frameId": "main-frame", "name": "load"},
		})

	case "Page.getLayoutMetrics":
		fe.rpcOK(msg.ID, map[string]any{
			"visualViewport": map[string]any{"pageX": 0, "pageY": 0, "clientWidth": 1365, "clientHeight": 768},
			"contentSize":    map[string]any{"width": 1365, "height": 768},
		})

	case "Page.startScreencast":
		fe.rpcOK(msg.ID, map[string]any{})
		fe.sendRelayMsg(map[string]any{
			"t":      "cdpEvent",
			"tabId":  msg.TabID,
			"method": "Page.screencastFrame",
			"params": map[string]any{"data": tinyPNG, "sessionId": 1},
		})

	case "Page.screencastFrameAck":
		fe.rpcOK(msg.ID, map[string]any{})

	case "Page.captureScreenshot":
		fe.rpcOK(msg.ID, map[string]any{"data": tinyPNG})

	case "Input.dispatchMouseEvent":
		fe.mu.Lock()
		if tpe, _ := msg.Params["type"].(string); tpe == "mousePressed" {
			fe.clickCount++
			fe.lastMouseDown = msg.Params
		}
		fe.mu.Unlock()
		fe.rpcOK(msg.ID, map[string]any{})

	case "Runtime.evaluate":
		expr, _ := msg.Params["expression"].(string)
		fe.mu.Lock()
		u := tab.snap.URL
		title := tab.title
		text := tab.text
		clicks := fe.clickCount
		fe.mu.Unlock()

		val := fe.evalFakeJS(expr, u, title, text, clicks)
		fe.rpcOK(msg.ID, map[string]any{
			"result": map[string]any{"type": "object", "value": val},
		})

	default:
		fe.rpcOK(msg.ID, map[string]any{})
	}
}

func (fe *fakeRelayExtension) evalFakeJS(expr, u, title, text string, clicks int) any {
	switch {
	case expr == "navigator.userAgent":
		return "FakeChrome/150"
	case expr == "document.readyState":
		return "complete"
	case strings.Contains(expr, "__ompBrowserKit"):
		// Installation of pageKitJS or kit helper call.
		if strings.Contains(expr, "globalThis.__ompBrowserKit =") {
			return nil
		}
		if strings.Contains(expr, "checkClickActionable") {
			return map[string]any{
				"ok": true, "x": 120.0, "y": 80.0,
				"rect": map[string]any{"x": 70.0, "y": 65.0, "width": 100.0, "height": 30.0},
			}
		}
		if strings.Contains(expr, "queryAll(") && strings.Contains(expr, ").length") {
			return 1.0
		}
		if strings.Contains(expr, "innerText") || strings.Contains(expr, "textContent") || strings.Contains(expr, ".text(") {
			return text
		}
	case strings.Contains(expr, "document.title") && strings.Contains(expr, "location.href"):
		return map[string]any{
			"url": u, "title": title,
			"width": 1365, "height": 768, "dpr": 1.0,
		}
	case strings.Contains(expr, "window.clicks"):
		return clicks
	case expr == "location.href":
		return u
	case expr == "document.title":
		return title
	}
	return text
}

func (fe *fakeRelayExtension) closeTabFromChrome(tabID int) {
	fe.mu.Lock()
	delete(fe.tabs, tabID)
	delete(fe.attached, tabID)
	fe.mu.Unlock()
	fe.sendRelayMsg(map[string]any{"t": "tabRemoved", "tabId": tabID})
}

func TestRelayEndToEndChainWithFakeExtension(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", tmp)
	t.Setenv("PIG_HOME", tmp)
	t.Setenv("HOME", tmp)

	// Register RelayBackend cleanly for the test.
	prevRelay, hadRelay := LookupBackend(string(KindRelay))
	cb := NewChromiumBackend()
	RegisterBackend(string(KindRelay), NewRelayBackend(cb))
	defer RegisterBackend(string(KindRelay), prevRelay)
	_ = hadRelay

	// Start fake extension #1 (leader) with dynamic port -> publishes endpoint file.
	fe1 := startFakeRelayExtension(t, "chrome-1", strings.Repeat("a", 32), 0, []fakeExtTab{
		{
			snap: relay.TabSnapshot{
				TabID:    10,
				URL:      "https://inbox.example/",
				Title:    "Inbox",
				Active:   true,
				WindowID: 1,
				GroupID:  -1,
			},
			text:  "Welcome to Inbox",
			title: "Inbox",
		},
	})
	fe1.waitLinked()
	defer fe1.disconnectHost()

	// 1. Endpoint discovery via ResolveBrowserKind (`app: "relay"` / `browser.relay` / `PI_BROWSER_RELAY`).
	endpoints := relay.DiscoverRelayEndpoints()
	if len(endpoints) == 0 {
		t.Fatal("expected discovered relay endpoint")
	}
	relayURL := endpoints[0]

	sRelay := DefaultSettings()
	sRelay.Relay = true
	if rk, err := ResolveBrowserKind(ResolveParams{}, ResolveSession{Settings: sRelay}, envLookup(map[string]string{})); err != nil || rk.Kind != KindRelay || rk.CdpURL != relayURL {
		t.Fatalf("browser.relay resolution failed: %+v err=%v", rk, err)
	}
	if rk, err := ResolveBrowserKind(ResolveParams{}, ResolveSession{Settings: DefaultSettings()}, envLookup(map[string]string{"PI_BROWSER_RELAY": "1"})); err != nil || rk.Kind != KindRelay || rk.CdpURL != relayURL {
		t.Fatalf("PI_BROWSER_RELAY=1 resolution failed: %+v err=%v", rk, err)
	}
	if rk, err := ResolveBrowserKind(ResolveParams{App: "relay", HasApp: true}, ResolveSession{Settings: DefaultSettings()}, envLookup(map[string]string{})); err != nil || rk.Kind != KindRelay || rk.CdpURL != relayURL {
		t.Fatalf("app:relay resolution failed: %+v err=%v", rk, err)
	}

	// 2. Auth & Origin checks:
	//    - No token -> 401
	//    - Valid token + Origin header -> 403
	wsBase := "ws" + strings.TrimPrefix(relayURL, "http") + "/cdp"
	if _, err := relay.DialWS(context.Background(), wsBase, nil); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected 401 without bearer token on /cdp, got %v", err)
	}
	tok, err := relay.RelayTokenForURL(relayURL)
	if err != nil {
		t.Fatalf("RelayTokenForURL(%s): %v", relayURL, err)
	}
	if _, err := relay.DialWS(context.Background(), wsBase, &relay.WSDialOptions{
		Headers: http.Header{
			"Authorization": []string{"Bearer " + tok},
			"Origin":        []string{"https://attacker.example"},
		},
	}); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("expected Origin rejection on /cdp, got %v", err)
	}

	// browser.relayUrl pointing at a port with no readable endpoint file fails clearly.
	sMissing := DefaultSettings()
	sMissing.Relay = true
	sMissing.RelayURL = "http://127.0.0.1:65432"
	if _, err := NewRelayBackend(cb).Open(context.Background(), OpenOptions{Name: "x", Endpoint: sMissing.RelayURL}); err == nil || !strings.Contains(err.Error(), "no readable endpoint file") {
		t.Fatalf("expected missing endpoint token error, got %v", err)
	}
	mgr := NewManager()
	mgr.env = envLookup(map[string]string{})
	ctx := context.Background()
	req := InvokeRequest{
		Cwd:            tmp,
		OwnerSessionID: "sess-1",
		Settings:       &sRelay,
	}

	// Existing rejections for insecure and custom downloads directory must hold.
	if _, err := mgr.Invoke(ctx, req, json.RawMessage(`{"action":"open","name":"bad1","app":"relay","insecure":true}`)); err == nil || !strings.Contains(err.Error(), "insecure is not supported") {
		t.Fatalf("expected insecure rejection, got %v", err)
	}
	if _, err := mgr.Invoke(ctx, req, json.RawMessage(`{"action":"open","name":"bad2","app":"relay","downloads":"/tmp/dl"}`)); err == nil || !strings.Contains(err.Error(), "cannot choose where Chrome saves downloads") {
		t.Fatalf("expected downloads rejection, got %v", err)
	}

	// targets lists the fake extension's tab.
	tRes, err := mgr.Invoke(ctx, req, json.RawMessage(`{"action":"targets","app":"relay"}`))
	if err != nil || !strings.Contains(tRes.Text, "https://inbox.example/") {
		t.Fatalf("targets failed: res=%+v err=%v", tRes, err)
	}

	// open creates a tab and navigates via goto.
	openRes, err := mgr.Invoke(ctx, req, json.RawMessage(`{"action":"open","name":"work","app":"relay","url":"https://docs.example/start"}`))
	if err != nil {
		t.Fatalf("open failed: %v", err)
	}
	if openRes.Details.Browser != KindRelay || openRes.Details.URL != "https://docs.example/start" {
		t.Fatalf("unexpected open details: %+v", openRes.Details)
	}

	// goto
	if _, err := mgr.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"work","chain":[{"method":"goto","args":["https://docs.example/page2"]}]}`)); err != nil {
		t.Fatalf("goto failed: %v", err)
	}

	// text
	txtRes, err := mgr.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"work","chain":[{"method":"text"}]}`))
	if err != nil || !strings.Contains(fmt.Sprint(txtRes.Details.Value), "Hello from https://docs.example/page2") {
		t.Fatalf("text failed: res=%+v err=%v", txtRes, err)
	}

	// click (selector click exercises waitForSelectorPresent + elementActionableCenter + Input.dispatchMouseEvent + overlay input)
	if _, err := mgr.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"work","chain":[{"method":"click","args":["#submit"]}]}`)); err != nil {
		t.Fatalf("click failed: %v", err)
	}
	fe1.mu.Lock()
	clicks := fe1.clickCount
	fe1.mu.Unlock()
	if clicks < 1 {
		t.Fatalf("expected fake extension to observe mousePressed from click, got %d", clicks)
	}
	// screenshot (hides overlay -> Page.captureScreenshot -> shows overlay)
	ssRes, err := mgr.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"work","chain":[{"method":"screenshot"}]}`))
	if err != nil || len(ssRes.Images) != 1 {
		t.Fatalf("screenshot failed: res=%+v err=%v", ssRes, err)
	}
	if _, err := base64.StdEncoding.DecodeString(ssRes.Images[0].Data); err != nil {
		t.Fatalf("invalid screenshot base64: %v", err)
	}

	// 4. Two clients on one adopted tab: both attach and read without conflicting.
	req2 := InvokeRequest{Cwd: tmp, OwnerSessionID: "sess-2", Settings: &sRelay}
	if _, err := mgr.Invoke(ctx, req, json.RawMessage(`{"action":"open","name":"shared","app":"relay","target":"Inbox"}`)); err != nil {
		t.Fatalf("client 1 adopt failed: %v", err)
	}
	if _, err := mgr.Invoke(ctx, req2, json.RawMessage(`{"action":"open","name":"shared","app":"relay","target":"Inbox"}`)); err != nil {
		t.Fatalf("client 2 adopt failed: %v", err)
	}
	r1, err1 := mgr.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"shared","chain":[{"method":"text"}]}`))
	r2, err2 := mgr.Invoke(ctx, req2, json.RawMessage(`{"action":"call","name":"shared","chain":[{"method":"text"}]}`))
	if err1 != nil || err2 != nil || !strings.Contains(fmt.Sprint(r1.Details.Value), "Welcome to Inbox") || !strings.Contains(fmt.Sprint(r2.Details.Value), "Welcome to Inbox") {
		t.Fatalf("two clients on one tab failed: (%+v,%v) (%+v,%v)", r1, err1, r2, err2)
	}

	// 5. Tab closed underneath in Chrome -> Alive() turns false and next call fails cleanly.
	fe1.closeTabFromChrome(10)
	time.Sleep(50 * time.Millisecond)
	if _, err := mgr.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"shared","chain":[{"method":"text"}]}`)); err == nil {
		t.Fatal("expected error after tab closed underneath")
	}

	// Close owned tab cleanly.
	if _, err := mgr.Invoke(ctx, req, json.RawMessage(`{"action":"close","name":"work"}`)); err != nil {
		t.Fatalf("close work failed: %v", err)
	}
	mgr.ReleaseTabsForOwner(ctx, "sess-1", true)
	mgr.ReleaseTabsForOwner(ctx, "sess-2", true)
}

func TestRelayFollowerAndReconnectEndToEnd(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", tmp)
	t.Setenv("PIG_HOME", tmp)
	t.Setenv("HOME", tmp)

	prevRelay, _ := LookupBackend(string(KindRelay))
	RegisterBackend(string(KindRelay), NewRelayBackend(NewChromiumBackend()))
	defer RegisterBackend(string(KindRelay), prevRelay)

	port, err := FindFreeTCPPort()
	if err != nil {
		t.Fatal(err)
	}
	relayURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	// Start leader browser 1 and follower browser 2 on the same relay port.
	fe1 := startFakeRelayExtension(t, "chrome-a", strings.Repeat("a", 32), port, []fakeExtTab{
		{
			snap:  relay.TabSnapshot{TabID: 1, URL: "https://browser-a.example/", Title: "Browser A", Active: true, WindowID: 1, GroupID: -1},
			text:  "From A",
			title: "Browser A",
		},
	})
	fe1.waitLinked()

	fe2 := startFakeRelayExtension(t, "edge-b", strings.Repeat("b", 32), port, []fakeExtTab{
		{
			snap:  relay.TabSnapshot{TabID: 2, URL: "https://browser-b.example/", Title: "Browser B", Active: true, WindowID: 1, GroupID: -1},
			text:  "From B",
			title: "Browser B",
		},
	})
	fe2.waitLinked()
	defer fe2.disconnectHost()

	// Both browsers' tabs are visible via the leader relay.
	s := DefaultSettings()
	s.Relay = true
	s.RelayURL = relayURL
	mgr := NewManager()
	mgr.env = envLookup(map[string]string{})
	req := InvokeRequest{Cwd: tmp, OwnerSessionID: "multi", Settings: &s}
	ctx := context.Background()
	// Poll briefly for follower's hello to reach leader.
	deadline := time.Now().Add(2 * time.Second)
	var listText string
	for time.Now().Before(deadline) {
		res, err := mgr.Invoke(ctx, req, json.RawMessage(`{"action":"targets","app":"relay"}`))
		if err == nil && strings.Contains(res.Text, "browser-a.example") && strings.Contains(res.Text, "browser-b.example") {
			listText = res.Text
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !strings.Contains(listText, "browser-b.example") {
		t.Fatalf("expected both browsers in targets, got %q", listText)
	}

	// Disconnect browser 1 (leader): browser 2 receives unlinked, takes over as leader, and serves browser-b.example.
	fe1.disconnectHost()
	select {
	case <-fe2.unlinkedCh:
	case <-time.After(5 * time.Second):
		t.Fatal("follower did not receive unlinked after leader left")
	}
	fe2.waitLinked()

	// Reconnect browser 1 as a follower now!
	fe1.connectHost()
	fe1.waitLinked()
	defer fe1.disconnectHost()

	deadline = time.Now().Add(2 * time.Second)
	listText = ""
	for time.Now().Before(deadline) {
		res, err := mgr.Invoke(ctx, req, json.RawMessage(`{"action":"targets","app":"relay"}`))
		if err == nil && strings.Contains(res.Text, "browser-a.example") && strings.Contains(res.Text, "browser-b.example") {
			listText = res.Text
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !strings.Contains(listText, "browser-a.example") || !strings.Contains(listText, "browser-b.example") {
		t.Fatalf("expected both browsers after reconnect & handover, got %q", listText)
	}
}

func TestRelayTabIsolationAndOwnership(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", tmp)
	t.Setenv("PIG_HOME", tmp)
	t.Setenv("HOME", tmp)

	prevRelay, _ := LookupBackend(string(KindRelay))
	RegisterBackend(string(KindRelay), NewRelayBackend(NewChromiumBackend()))
	defer RegisterBackend(string(KindRelay), prevRelay)

	fe := startFakeRelayExtension(t, "iso-chrome", strings.Repeat("d", 32), 0, []fakeExtTab{
		{
			snap:  relay.TabSnapshot{TabID: 10, URL: "https://human.example/", Title: "Human sentinel", Active: true, WindowID: 1, GroupID: -1},
			text:  "Human sentinel body",
			title: "Human sentinel",
		},
	})
	fe.waitLinked()
	defer fe.disconnectHost()

	s := DefaultSettings()
	s.Relay = true
	mgr := NewManager()
	mgr.env = envLookup(map[string]string{})
	ctx := context.Background()

	t.Run(`upstream: browser-relay-tab-isolation.test.ts:431 "opens default relay tabs in the background and closes them, leaving borrowed pages open"`, func(t *testing.T) {
		req := InvokeRequest{Cwd: tmp, OwnerSessionID: "iso-s1", Settings: &s}
		if _, err := mgr.Invoke(ctx, req, json.RawMessage(`{"action":"open","name":"owned","app":"relay","url":"https://owned.example/"}`)); err != nil {
			t.Fatalf("open owned failed: %v", err)
		}
		// Verify createTab requested active=false (background).
		fe.mu.Lock()
		var sawBgCreate bool
		for _, rpc := range fe.receivedRPCs {
			if rpc.T == "rpc" && rpc.Op == "createTab" && rpc.Active != nil && !*rpc.Active {
				sawBgCreate = true
			}
		}
		fe.mu.Unlock()
		if !sawBgCreate {
			t.Fatal("expected default relay tab opened with active:false (background)")
		}
		// Borrow human tab -> close releases without removing tab 10.
		if _, err := mgr.Invoke(ctx, req, json.RawMessage(`{"action":"open","name":"borrowed","app":"relay","target":"Human sentinel"}`)); err != nil {
			t.Fatalf("open borrowed failed: %v", err)
		}
		if _, err := mgr.Invoke(ctx, req, json.RawMessage(`{"action":"close","name":"borrowed"}`)); err != nil {
			t.Fatalf("close borrowed failed: %v", err)
		}
		fe.mu.Lock()
		_, humanAlive := fe.tabs[10]
		fe.mu.Unlock()
		if !humanAlive {
			t.Fatal("closing borrowed tab must not close human tab 10")
		}
		// Closing owned tab removes it from Chrome.
		if _, err := mgr.Invoke(ctx, req, json.RawMessage(`{"action":"close","name":"owned"}`)); err != nil {
			t.Fatalf("close owned failed: %v", err)
		}
		fe.mu.Lock()
		tabCount := len(fe.tabs)
		fe.mu.Unlock()
		if tabCount != 1 {
			t.Fatalf("expected owned tab removed (leaving only human tab 10), got %d tabs", tabCount)
		}
	})

	t.Run(`upstream: browser-relay-tab-isolation.test.ts:573 "leaves a persist tab open in the user's browser when its session ends and closes the others"`, func(t *testing.T) {
		req := InvokeRequest{Cwd: tmp, OwnerSessionID: "iso-persist", Settings: &s}
		if _, err := mgr.Invoke(ctx, req, json.RawMessage(`{"action":"open","name":"kept","app":"relay","url":"https://kept.example/","persist":true}`)); err != nil {
			t.Fatalf("open persist tab failed: %v", err)
		}
		if _, err := mgr.Invoke(ctx, req, json.RawMessage(`{"action":"open","name":"plain","app":"relay","url":"https://plain.example/"}`)); err != nil {
			t.Fatalf("open plain tab failed: %v", err)
		}
		if n := mgr.ReleaseTabsForOwner(ctx, "iso-persist", true); n != 2 {
			t.Fatalf("expected 2 tabs released, got %d", n)
		}
		fe.mu.Lock()
		var hasKept, hasPlain bool
		for _, tab := range fe.tabs {
			if tab.snap.URL == "https://kept.example/" {
				hasKept = true
			}
			if tab.snap.URL == "https://plain.example/" {
				hasPlain = true
			}
		}
		fe.mu.Unlock()
		if !hasKept || hasPlain {
			t.Fatalf("expected persist tab kept (%v) and plain tab closed (hasPlain=%v)", hasKept, hasPlain)
		}
	})

	t.Run(`upstream: browser-relay-tab-isolation.test.ts:885 "reports each relay download's own file when a later download of the same URL finishes first"`, func(t *testing.T) {
		req := InvokeRequest{Cwd: tmp, OwnerSessionID: "iso-dl", Settings: &s}
		if _, err := mgr.Invoke(ctx, req, json.RawMessage(`{"action":"open","name":"dl","app":"relay","url":"https://dl.example/"}`)); err != nil {
			t.Fatalf("open dl tab failed: %v", err)
		}
		fe.mu.Lock()
		var dlTabID int
		for tid, tab := range fe.tabs {
			if tab.snap.URL == "https://dl.example/" {
				dlTabID = tid
			}
		}
		fe.mu.Unlock()
		fe.sendRelayMsg(map[string]any{"t": "cdpEvent", "tabId": dlTabID, "method": "Page.downloadWillBegin", "params": map[string]any{"guid": "g-first", "url": "https://dl.example/held", "suggestedFilename": "held.csv"}})
		fe.sendRelayMsg(map[string]any{"t": "cdpEvent", "tabId": dlTabID, "method": "Page.downloadWillBegin", "params": map[string]any{"guid": "g-second", "url": "https://dl.example/held", "suggestedFilename": "held.csv"}})
		fe.sendRelayMsg(map[string]any{"t": "cdpEvent", "tabId": dlTabID, "method": "Page.downloadProgress", "params": map[string]any{"guid": "g-second", "state": "completed", "receivedBytes": 8}})
		r1, err := mgr.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"dl","chain":[{"method":"waitForDownload","args":[{"timeout":3000}]}]}`))
		if err != nil {
			t.Fatalf("first waitForDownload failed: %v", err)
		}
		fe.sendRelayMsg(map[string]any{"t": "cdpEvent", "tabId": dlTabID, "method": "Page.downloadProgress", "params": map[string]any{"guid": "g-first", "state": "completed", "receivedBytes": 8}})
		r2, err := mgr.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"dl","chain":[{"method":"waitForDownload","args":[{"timeout":3000}]}]}`))
		if err != nil {
			t.Fatalf("second waitForDownload failed: %v", err)
		}
		j1, _ := json.Marshal(r1.Details.Value)
		j2, _ := json.Marshal(r2.Details.Value)
		if !strings.Contains(string(j1), "g-second") || !strings.Contains(string(j2), "g-first") {
			t.Fatalf("expected g-second then g-first, got %s and %s", j1, j2)
		}
		mgr.ReleaseTabsForOwner(ctx, "iso-dl", true)
	})
}

// TestRelayDebuggerDetachModelError documents and verifies the port's exact
// model-facing behavior when Chrome detaches the debugger under an open relay
// tab (`canceled_by_user` when the user dismisses the debugging bar, and
// `replaced_with_devtools` when the user opens DevTools). Unlike omp's
// tab-worker + tab-supervisor (which auto-readopts the tab on non-cancel
// detaches), the port ends the session and reports that the tab is no longer
// open so the model must reopen it with browser.open().
func TestRelayDebuggerDetachModelError(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", tmp)
	t.Setenv("PIG_HOME", tmp)
	t.Setenv("HOME", tmp)

	prevRelay, _ := LookupBackend(string(KindRelay))
	RegisterBackend(string(KindRelay), NewRelayBackend(NewChromiumBackend()))
	defer RegisterBackend(string(KindRelay), prevRelay)

	fe := startFakeRelayExtension(t, "detach-chrome", strings.Repeat("e", 32), 0, []fakeExtTab{
		{
			snap:  relay.TabSnapshot{TabID: 10, URL: "https://user.example/", Title: "User Page", Active: true, WindowID: 1, GroupID: -1},
			text:  "User Page body",
			title: "User Page",
		},
	})
	fe.waitLinked()
	defer fe.disconnectHost()

	s := DefaultSettings()
	s.Relay = true
	mgr := NewManager()
	mgr.env = envLookup(map[string]string{})
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		reason string
	}{
		{name: "canceled_by_user", reason: "canceled_by_user"},
		{name: "replaced_with_devtools", reason: "replaced_with_devtools"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := InvokeRequest{Cwd: tmp, OwnerSessionID: "detach-" + tc.name, Settings: &s}
			tabName := "work-" + tc.name
			if _, err := mgr.Invoke(ctx, req, json.RawMessage(fmt.Sprintf(`{"action":"open","name":%q,"app":"relay","url":"https://work.example/%s"}`, tabName, tc.name))); err != nil {
				t.Fatalf("open failed: %v", err)
			}
			// Locate the newly created tab ID on the fake extension.
			fe.mu.Lock()
			var tabID int
			wantURL := "https://work.example/" + tc.name
			for tid, tab := range fe.tabs {
				if tab.snap.URL == wantURL {
					tabID = tid
					break
				}
			}
			fe.mu.Unlock()
			if tabID == 0 {
				t.Fatalf("could not find created tab for %s", wantURL)
			}

			// Extension reports Chrome detached the debugger underneath the open session.
			fe.sendRelayMsg(map[string]any{
				"t":      "detached",
				"tabId":  tabID,
				"reason": tc.reason,
			})

			// Wait for Target.detachedFromTarget to propagate to relayTab so Alive() turns false.
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				if live := mgr.getLiveTab(ctx, tabKey{owner: req.OwnerSessionID, name: tabName}); live == nil {
					break
				}
				time.Sleep(15 * time.Millisecond)
			}

			// Next call sees the exact model-facing error asking the model to open the tab again
			// (no automatic re-adoption).
			_, err := mgr.Invoke(ctx, req, json.RawMessage(fmt.Sprintf(`{"action":"call","name":%q,"chain":[{"method":"text"}]}`, tabName)))
			if err == nil {
				t.Fatalf("expected error after %s detach, got nil", tc.reason)
			}
			wantErr := fmt.Sprintf("No open browser tab named %q. Open one first with browser.open({ name: %q, url: ... }).", tabName, tabName)
			if err.Error() != wantErr {
				t.Fatalf("after %s detach: got error %q, want %q", tc.reason, err.Error(), wantErr)
			}
		})
	}
}
