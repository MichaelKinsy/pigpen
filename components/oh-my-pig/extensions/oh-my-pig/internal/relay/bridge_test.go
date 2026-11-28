package relay

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// upstream: packages/coding-agent/test/tools/browser-relay-bridge.test.ts,
//           packages/coding-agent/test/tools/browser-relay-self-update.test.ts

type fakeExtSock struct {
	mu       sync.Mutex
	messages []RelayToExtMessage
	acked    map[int]bool
	closed   bool
	notify   chan struct{}
}

func newFakeExtSock() *fakeExtSock {
	return &fakeExtSock{
		acked:  make(map[int]bool),
		notify: make(chan struct{}, 128),
	}
}

func (f *fakeExtSock) Send(text string) {
	var m RelayToExtMessage
	_ = json.Unmarshal([]byte(text), &m)
	f.mu.Lock()
	f.messages = append(f.messages, m)
	f.mu.Unlock()
	select {
	case f.notify <- struct{}{}:
	default:
	}
}

func (f *fakeExtSock) Close() {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
}

func (f *fakeExtSock) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func (f *fakeExtSock) rpcs(op string) []RelayToExtMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []RelayToExtMessage
	for _, m := range f.messages {
		if m.T == "rpc" && m.Op == op {
			out = append(out, m)
		}
	}
	return out
}

func (f *fakeExtSock) pending(op string) []RelayToExtMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []RelayToExtMessage
	for _, m := range f.messages {
		if m.T == "rpc" && m.Op == op && !f.acked[m.ID] {
			out = append(out, m)
		}
	}
	return out
}

func (f *fakeExtSock) waitPending(t *testing.T, op string) []RelayToExtMessage {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if p := f.pending(op); len(p) > 0 {
			return p
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for pending %q rpc", op)
		}
		select {
		case <-f.notify:
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (f *fakeExtSock) reloads() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, m := range f.messages {
		if m.T == "reload" {
			n++
		}
	}
	return n
}

func (f *fakeExtSock) markAcked(id int) {
	f.mu.Lock()
	f.acked[id] = true
	f.mu.Unlock()
}

type fakeCDPSock struct {
	mu       sync.Mutex
	messages []map[string]any
	notify   chan struct{}
}

func newFakeCDPSock() *fakeCDPSock {
	return &fakeCDPSock{notify: make(chan struct{}, 128)}
}

func (f *fakeCDPSock) Send(text string) {
	var m map[string]any
	_ = json.Unmarshal([]byte(text), &m)
	f.mu.Lock()
	f.messages = append(f.messages, m)
	f.mu.Unlock()
	select {
	case f.notify <- struct{}{}:
	default:
	}
}

func (f *fakeCDPSock) Close() {}

func (f *fakeCDPSock) waitReply(t *testing.T, id int) map[string]any {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		f.mu.Lock()
		for _, m := range f.messages {
			if v, ok := m["id"].(float64); ok && int(v) == id {
				f.mu.Unlock()
				return m
			}
		}
		f.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for CDP reply id=%d", id)
		}
		select {
		case <-f.notify:
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (f *fakeCDPSock) waitEvent(t *testing.T, method string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		f.mu.Lock()
		for _, m := range f.messages {
			if m["method"] == method {
				f.mu.Unlock()
				return m
			}
		}
		f.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for CDP event %q", method)
		}
		select {
		case <-f.notify:
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (f *fakeCDPSock) snapshot() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]map[string]any, len(f.messages))
	copy(out, f.messages)
	return out
}

func mkTab(tabID int, opts ...func(*TabSnapshot)) TabSnapshot {
	snap := TabSnapshot{
		TabID:    tabID,
		URL:      "https://example.com/",
		Title:    "Example",
		WindowID: 1,
		GroupID:  -1,
	}
	for _, fn := range opts {
		fn(&snap)
	}
	return snap
}

func connectExt(bridge *RelayBridge, sock *fakeExtSock, tabs []TabSnapshot, attached []int) {
	bridge.ExtConnected(sock, "")
	hello, _ := json.Marshal(map[string]any{
		"t":                     "hello",
		"userAgent":             "test",
		"browserVersion":        "Chrome/151.0.0.0",
		"discardedTabsProtocol": 1,
		"tabs":                  tabs,
		"attachedTabIds":        attached,
	})
	bridge.ExtMessage(sock, string(hello))
}

func connectExtInstance(bridge *RelayBridge, sock *fakeExtSock, instanceID, browserSession string, tabs []TabSnapshot, extra map[string]any) {
	extID, _ := extra["extensionId"].(string)
	bridge.ExtConnected(sock, extID)
	m := map[string]any{
		"t":                     "hello",
		"instanceId":            instanceID,
		"browserSession":        browserSession,
		"userAgent":             "test",
		"browserVersion":        "Chrome/151.0.0.0",
		"discardedTabsProtocol": 1,
		"tabs":                  tabs,
		"attachedTabIds":        []int{},
	}
	for k, v := range extra {
		if k != "extensionId" {
			m[k] = v
		}
	}
	raw, _ := json.Marshal(m)
	bridge.ExtMessage(sock, string(raw))
}

func ackOp(bridge *RelayBridge, sock *fakeExtSock, op string, result any) int {
	if result == nil {
		result = map[string]any{}
	}
	pending := sock.pending(op)
	for _, rpc := range pending {
		sock.markAcked(rpc.ID)
		raw, _ := json.Marshal(map[string]any{"t": "rpcResult", "id": rpc.ID, "ok": true, "result": result})
		bridge.ExtMessage(sock, string(raw))
	}
	return len(pending)
}

func nackOp(bridge *RelayBridge, sock *fakeExtSock, op string, errMsg string) int {
	pending := sock.pending(op)
	for _, rpc := range pending {
		sock.markAcked(rpc.ID)
		raw, _ := json.Marshal(map[string]any{"t": "rpcResult", "id": rpc.ID, "ok": false, "error": errMsg})
		bridge.ExtMessage(sock, string(raw))
	}
	return len(pending)
}

var bridgeMsgSeq = 100

func nextID() int {
	bridgeMsgSeq++
	return bridgeMsgSeq
}

func attachPageTab(t *testing.T, bridge *RelayBridge, ext *fakeExtSock, cdp *fakeCDPSock, connID, tabID int, instanceID string) string {
	t.Helper()
	if instanceID == "" {
		instanceID = "anon"
	}
	id := nextID()
	targetID := fmt.Sprintf("PAGE%s.%d", InstanceCode(instanceID, ""), tabID)
	cmd, _ := json.Marshal(map[string]any{
		"id":     id,
		"method": "Target.attachToTarget",
		"params": map[string]any{"targetId": targetID, "flatten": true},
	})
	bridge.CDPMessage(connID, string(cmd))
	// Ack attach if one was issued (may already be attached).
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if len(ext.pending("attach")) > 0 {
			ackOp(bridge, ext, "attach", map[string]any{})
		}
		for _, m := range cdp.snapshot() {
			if v, ok := m["id"].(float64); ok && int(v) == id {
				if res, ok := m["result"].(map[string]any); ok {
					if sid, ok := res["sessionId"].(string); ok {
						return sid
					}
				}
				t.Fatalf("attachToTarget failed: %+v", m)
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("attachPageTab timed out for tab %d", tabID)
	return ""
}

func TestRelayBridgeDiscoveryGroupingAndClaims(t *testing.T) {
	bridge := NewRelayBridge(BridgeOptions{Group: &DefaultGroupConfig})
	ext := newFakeExtSock()
	connectExt(bridge, ext, []TabSnapshot{
		mkTab(1),
		mkTab(2, func(s *TabSnapshot) { s.URL = "about:blank" }),
		mkTab(3, func(s *TabSnapshot) { s.URL = "chrome://extensions/" }),
		mkTab(4, func(s *TabSnapshot) { s.Pinned = true }),
		mkTab(5, func(s *TabSnapshot) { s.GroupID = 77 }),
	}, nil)

	// No grouping on hello or unclaiming discovery.
	if len(ext.rpcs("group")) != 0 {
		t.Fatalf("unexpected group RPC on hello")
	}

	cdp := newFakeCDPSock()
	connID := bridge.CDPConnected(cdp, "")
	id := nextID()
	bridge.CDPMessage(connID, fmt.Sprintf(`{"id":%d,"method":"Target.getTargets"}`, id))
	rep := cdp.waitReply(t, id)
	infos := rep["result"].(map[string]any)["targetInfos"].([]any)
	// Tab 3 (chrome://) is ineligible -> 4 targets.
	if len(infos) != 4 {
		t.Fatalf("expected 4 eligible targets, got %d: %+v", len(infos), infos)
	}

	// Browser.close must be refused/no-op (returns {} without closing browser).
	closeID := nextID()
	bridge.CDPMessage(connID, fmt.Sprintf(`{"id":%d,"method":"Browser.close"}`, closeID))
	closeRep := cdp.waitReply(t, closeID)
	if _, hasErr := closeRep["error"]; hasErr {
		t.Fatalf("Browser.close should ack {} safely, got %+v", closeRep)
	}

	// Claiming tab 1 via /cdp?claim= groups only tab 1.
	worker := newFakeCDPSock()
	workerConn := bridge.CDPConnected(worker, fmt.Sprintf("PAGE%s.1", InstanceCode("anon", "")))
	grp := ext.waitPending(t, "group")
	if len(grp) != 1 || len(grp[0].TabIDs) != 1 || grp[0].TabIDs[0] != 1 || grp[0].Title != "omp" {
		t.Fatalf("unexpected group RPC: %+v", grp)
	}
	ackOp(bridge, ext, "group", map[string]any{"grouped": map[string]any{"1": 42}})
	time.Sleep(20 * time.Millisecond)

	// Claiming pinned tab 4 or user-grouped tab 5 must not issue group RPCs.
	_ = bridge.CDPConnected(newFakeCDPSock(), fmt.Sprintf("PAGE%s.4", InstanceCode("anon", "")))
	_ = bridge.CDPConnected(newFakeCDPSock(), fmt.Sprintf("PAGE%s.5", InstanceCode("anon", "")))
	time.Sleep(20 * time.Millisecond)
	if len(ext.rpcs("group")) != 1 {
		t.Fatalf("expected no grouping for pinned/user-grouped tabs, got %d", len(ext.rpcs("group")))
	}

	// User pulls tab 1 out of omp group -> opt-out, never regrouped.
	upd1, _ := json.Marshal(map[string]any{"t": "tabUpdated", "tab": mkTab(1, func(s *TabSnapshot) { s.GroupID = 42 })})
	bridge.ExtMessage(ext, string(upd1))
	upd2, _ := json.Marshal(map[string]any{"t": "tabUpdated", "tab": mkTab(1, func(s *TabSnapshot) { s.GroupID = -1 })})
	bridge.ExtMessage(ext, string(upd2))
	upd3, _ := json.Marshal(map[string]any{"t": "tabUpdated", "tab": mkTab(1, func(s *TabSnapshot) { s.GroupID = -1; s.URL = "https://example.com/other" })})
	bridge.ExtMessage(ext, string(upd3))
	if len(ext.rpcs("group")) != 1 {
		t.Fatalf("expected user opt-out to prevent regrouping")
	}

	// Claim tab 2 -> group, then disconnect claiming client -> ungroup.
	w2 := bridge.CDPConnected(newFakeCDPSock(), fmt.Sprintf("PAGE%s.2", InstanceCode("anon", "")))
	ext.waitPending(t, "group")
	ackOp(bridge, ext, "group", map[string]any{"grouped": map[string]any{"2": 42}})
	time.Sleep(20 * time.Millisecond)
	bridge.CDPClosed(w2)
	ung := ext.waitPending(t, "ungroup")
	if len(ung) != 1 || ung[0].TabIDs[0] != 2 {
		t.Fatalf("expected ungroup for tab 2, got %+v", ung)
	}
	bridge.CDPClosed(workerConn)
}

func TestRelayBridgeCreateTargetAndRefusal(t *testing.T) {
	bridge := NewRelayBridge(BridgeOptions{Group: &DefaultGroupConfig})
	ext := newFakeExtSock()
	connectExt(bridge, ext, nil, nil)

	cdp := newFakeCDPSock()
	connID := bridge.CDPConnected(cdp, "")
	// Arm auto-attach on browser connection.
	aaID := nextID()
	bridge.CDPMessage(connID, fmt.Sprintf(`{"id":%d,"method":"Target.setAutoAttach","params":{}}`, aaID))
	cdp.waitReply(t, aaID)

	// Background tab sets active:false; attach refusal closes tab and returns error.
	cID := nextID()
	bridge.CDPMessage(connID, fmt.Sprintf(`{"id":%d,"method":"Target.createTarget","params":{"url":"https://example.com/","background":true}}`, cID))
	creates := ext.waitPending(t, "createTab")
	if creates[0].Active == nil || *creates[0].Active != false {
		t.Fatalf("expected active:false for background createTarget, got %+v", creates[0].Active)
	}
	ackOp(bridge, ext, "createTab", map[string]any{"tab": mkTab(9)})
	ext.waitPending(t, "attach")
	nackOp(bridge, ext, "attach", "Host access is restricted by policy.")
	rm := ext.waitPending(t, "removeTab")
	if rm[0].TabID != 9 {
		t.Fatalf("expected removeTab 9, got %+v", rm[0])
	}
	ackOp(bridge, ext, "removeTab", map[string]any{})
	rep := cdp.waitReply(t, cID)
	errObj, ok := rep["error"].(map[string]any)
	if !ok || !strings.Contains(errObj["message"].(string), "Host access is restricted by policy.") {
		t.Fatalf("expected policy refusal error, got %+v", rep)
	}
}

func TestRelayBridgeRuntimeVirtualizationAndTwoClients(t *testing.T) {
	bridge := NewRelayBridge(BridgeOptions{})
	ext := newFakeExtSock()
	connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)

	c1 := newFakeCDPSock()
	conn1 := bridge.CDPConnected(c1, "")
	s1 := attachPageTab(t, bridge, ext, c1, conn1, 1, "anon")

	// First Runtime.enable triggers root Runtime.disable -> Runtime.enable.
	en1 := nextID()
	bridge.CDPMessage(conn1, fmt.Sprintf(`{"id":%d,"sessionId":%q,"method":"Runtime.enable"}`, en1, s1))
	p1 := ext.waitPending(t, "send")
	if p1[0].Method != "Runtime.disable" {
		t.Fatalf("expected Runtime.disable first, got %s", p1[0].Method)
	}
	ackOp(bridge, ext, "send", map[string]any{})
	p2 := ext.waitPending(t, "send")
	if p2[0].Method != "Runtime.enable" {
		t.Fatalf("expected Runtime.enable second, got %s", p2[0].Method)
	}
	ctxParams := map[string]any{
		"context": map[string]any{
			"id":       17,
			"origin":   "https://example.com",
			"name":     "",
			"uniqueId": "ctx-17",
			"auxData":  map[string]any{"isDefault": true, "frameId": "f1"},
		},
	}
	evRaw, _ := json.Marshal(map[string]any{"t": "cdpEvent", "tabId": 1, "method": "Runtime.executionContextCreated", "params": ctxParams})
	bridge.ExtMessage(ext, string(evRaw))
	ackOp(bridge, ext, "send", map[string]any{})
	c1.waitReply(t, en1)

	// Second client attaches to the same tab: reuses attachment and replays context 17 without another root Runtime cycle.
	c2 := newFakeCDPSock()
	conn2 := bridge.CDPConnected(c2, "")
	s2 := attachPageTab(t, bridge, ext, c2, conn2, 1, "anon")
	sendCountBefore := len(ext.rpcs("send"))
	en2 := nextID()
	bridge.CDPMessage(conn2, fmt.Sprintf(`{"id":%d,"sessionId":%q,"method":"Runtime.enable"}`, en2, s2))
	c2.waitReply(t, en2)
	if len(ext.rpcs("send")) != sendCountBefore {
		t.Fatalf("expected no extra root Runtime RPC for second client")
	}
	c2.waitEvent(t, "Runtime.executionContextCreated")

	// Client 2 disables Runtime; client 1 still receives new contexts, client 2 does not.
	dis2 := nextID()
	bridge.CDPMessage(conn2, fmt.Sprintf(`{"id":%d,"sessionId":%q,"method":"Runtime.disable"}`, dis2, s2))
	c2.waitReply(t, dis2)

	ev2, _ := json.Marshal(map[string]any{"t": "cdpEvent", "tabId": 1, "method": "Runtime.executionContextCreated", "params": map[string]any{"context": map[string]any{"id": 18}}})
	bridge.ExtMessage(ext, string(ev2))

	// Disconnecting client 1 keeps attachment alive because client 2 holds a session.
	bridge.CDPClosed(conn1)
	time.Sleep(20 * time.Millisecond)
	if len(ext.rpcs("detach")) != 0 {
		t.Fatalf("expected attachment kept while second client holds session")
	}
	// Disconnecting client 2 detaches Chrome debugger.
	bridge.CDPClosed(conn2)
	ext.waitPending(t, "detach")
}

func TestRelayBridgeTabWideSwitchesAndFocusResetBeforeDetach(t *testing.T) {
	bridge := NewRelayBridge(BridgeOptions{})
	ext := newFakeExtSock()
	connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)

	c1 := newFakeCDPSock()
	conn1 := bridge.CDPConnected(c1, "")
	s1 := attachPageTab(t, bridge, ext, c1, conn1, 1, "anon")
	s2 := attachPageTab(t, bridge, ext, c1, conn1, 1, "anon")

	sendSwitch := func(sid, method string, enabled bool) {
		id := nextID()
		bridge.CDPMessage(conn1, fmt.Sprintf(`{"id":%d,"sessionId":%q,"method":%q,"params":{"enabled":%t}}`, id, sid, method, enabled))
		ext.waitPending(t, "send")
		ackOp(bridge, ext, "send", map[string]any{})
		c1.waitReply(t, id)
	}
	sendSwitch(s1, "Page.setInterceptFileChooserDialog", true)
	sendSwitch(s2, "Page.setInterceptFileChooserDialog", true)
	sendSwitch(s2, "Page.setInterceptFileChooserDialog", false)
	// s1 still wants it, so the 3rd send must still pass enabled:true.
	sends := ext.rpcs("send")
	if len(sends) != 3 || sends[2].Params["enabled"] != true {
		t.Fatalf("expected chooser kept enabled while s1 holds it: %+v", sends)
	}

	// Enable focus emulation on s1, then close conn1: focus reset must go out BEFORE detach.
	sendSwitch(s1, "Emulation.setFocusEmulationEnabled", true)
	bridge.CDPClosed(conn1)
	pendingFocus := ext.waitPending(t, "send")
	if len(ext.rpcs("detach")) != 0 {
		t.Fatalf("detach must wait for focus reset transition")
	}
	if pendingFocus[0].Method != "Emulation.setFocusEmulationEnabled" && pendingFocus[0].Method != "Page.setInterceptFileChooserDialog" {
		t.Fatalf("unexpected pending switch reset: %+v", pendingFocus)
	}
	// Ack all switch resets -> detach arrives.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ackOp(bridge, ext, "send", map[string]any{})
		if len(ext.pending("detach")) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(ext.pending("detach")) != 1 {
		t.Fatalf("expected detach after switch resets settled")
	}
}

func TestRelayBridgeOverlayDownloadPathHiddenFramesAndMultiInstance(t *testing.T) {
	bridge := NewRelayBridge(BridgeOptions{OverlayHideWait: 80 * time.Millisecond})
	chrome := newFakeExtSock()
	edge := newFakeExtSock()
	connectExtInstance(bridge, chrome, "chrome", "run-1", []TabSnapshot{mkTab(1, func(s *TabSnapshot) { s.Active = true })}, map[string]any{
		"overlayProtocol":   1,
		"downloadsProtocol": 1,
		"panelProtocol":     1,
	})
	connectExtInstance(bridge, edge, "edge", "run-1", []TabSnapshot{mkTab(1)}, map[string]any{
		"overlayProtocol": 1,
	})

	cdp := newFakeCDPSock()
	connID := bridge.CDPConnected(cdp, fmt.Sprintf("PAGE%s.1", InstanceCode("chrome", "run-1")))
	// Claim triggers overlay mount.
	ov := chrome.waitPending(t, "overlay")
	if ov[0].Action != OverlayMount {
		t.Fatalf("expected overlay mount, got %+v", ov[0])
	}
	ackOp(bridge, chrome, "overlay", map[string]any{})

	// Attach page target using run-1 code.
	attID := nextID()
	bridge.CDPMessage(connID, fmt.Sprintf(`{"id":%d,"method":"Target.attachToTarget","params":{"targetId":"PAGE%s.1","flatten":true}}`, attID, InstanceCode("chrome", "run-1")))
	chrome.waitPending(t, "attach")
	// Verify clearForeignFrames is true on claimed tab.
	if att := chrome.rpcs("attach")[0]; att.ClearForeignFrames == nil || !*att.ClearForeignFrames {
		t.Fatalf("expected clearForeignFrames=true on claimed tab")
	}
	ackOp(bridge, chrome, "attach", map[string]any{})
	attRep := cdp.waitReply(t, attID)
	sid := attRep["result"].(map[string]any)["sessionId"].(string)

	// Screenshot hides overlay, sends Page.captureScreenshot, then shows overlay.
	capID := nextID()
	bridge.CDPMessage(connID, fmt.Sprintf(`{"id":%d,"sessionId":%q,"method":"Page.captureScreenshot","params":{"format":"png"}}`, capID, sid))
	ovHide := chrome.waitPending(t, "overlay")
	if ovHide[0].Action != OverlayHide {
		t.Fatalf("expected overlay hide before screenshot, got %+v", ovHide[0])
	}
	ackOp(bridge, chrome, "overlay", map[string]any{})
	capSend := chrome.waitPending(t, "send")
	if capSend[0].Method != "Page.captureScreenshot" {
		t.Fatalf("expected Page.captureScreenshot, got %+v", capSend[0])
	}
	ackOp(bridge, chrome, "send", map[string]any{"data": "iVBORw0KGgo="})
	cdp.waitReply(t, capID)
	ovShow := chrome.waitPending(t, "overlay")
	if ovShow[0].Action != OverlayShow {
		t.Fatalf("expected overlay show after screenshot, got %+v", ovShow[0])
	}
	ackOp(bridge, chrome, "overlay", map[string]any{})

	// Acknowledged mouse input forwards to overlay; cancelled release point does not.
	mvID := nextID()
	bridge.CDPMessage(connID, fmt.Sprintf(`{"id":%d,"sessionId":%q,"method":"Input.dispatchMouseEvent","params":{"type":"mouseMoved","x":10,"y":20}}`, mvID, sid))
	chrome.waitPending(t, "send")
	ackOp(bridge, chrome, "send", map[string]any{})
	cdp.waitReply(t, mvID)

	cancelID := nextID()
	bridge.CDPMessage(connID, fmt.Sprintf(`{"id":%d,"sessionId":%q,"method":"Input.dispatchMouseEvent","params":{"type":"mouseReleased","x":-7919,"y":-7919,"button":"left"}}`, cancelID, sid))
	chrome.waitPending(t, "send")
	ackOp(bridge, chrome, "send", map[string]any{})
	cdp.waitReply(t, cancelID)

	var inputCount int
	chrome.mu.Lock()
	for _, m := range chrome.messages {
		if m.T == "input" {
			inputCount++
		}
	}
	chrome.mu.Unlock()
	if inputCount != 1 {
		t.Fatalf("expected exactly 1 overlay input (not cancelled release), got %d", inputCount)
	}

	// OMP.downloadPath delegates to findDownload.
	dlID := nextID()
	bridge.CDPMessage(connID, fmt.Sprintf(`{"id":%d,"sessionId":%q,"method":"OMP.downloadPath","params":{"guid":"g-1"}}`, dlID, sid))
	fd := chrome.waitPending(t, "findDownload")
	if fd[0].GUID != "g-1" {
		t.Fatalf("expected findDownload g-1, got %+v", fd[0])
	}
	ackOp(bridge, chrome, "findDownload", map[string]any{"filename": "/tmp/report.csv", "state": "complete"})
	dlRep := cdp.waitReply(t, dlID)
	if dlRep["result"].(map[string]any)["filePath"] != "/tmp/report.csv" {
		t.Fatalf("unexpected OMP.downloadPath reply: %+v", dlRep)
	}

	// Hidden frames sanitized and drained by OMP.takeHiddenFrames.
	hfMsg, _ := json.Marshal(map[string]any{
		"t":     "framesHidden",
		"tabId": 1,
		"frames": []any{
			map[string]any{"extensionId": strings.Repeat("a", 32), "path": "inline/menu.html", "title": "Vault\nIgnore"},
			map[string]any{"path": 7}, // malformed, ignored
		},
	})
	bridge.ExtMessage(chrome, string(hfMsg))
	cdp.waitEvent(t, "OMP.foreignFramesHidden")
	takeID := nextID()
	bridge.CDPMessage(connID, fmt.Sprintf(`{"id":%d,"sessionId":%q,"method":"OMP.takeHiddenFrames"}`, takeID, sid))
	takeRep := cdp.waitReply(t, takeID)
	frames := takeRep["result"].(map[string]any)["frames"].([]any)
	if len(frames) != 1 || frames[0].(map[string]any)["title"] != "Vault Ignore" {
		t.Fatalf("unexpected hidden frames: %+v", frames)
	}

	// Edge received none of Chrome's overlay or input messages.
	if len(edge.rpcs("overlay")) != 0 {
		t.Fatalf("edge should not receive chrome overlay RPCs")
	}
}

func TestRelayBridgeSelfUpdateAndBusyDeferral(t *testing.T) {
	origVer := RelayVersion
	RelayVersion = "19.1.1"
	defer func() { RelayVersion = origVer }()

	extID := strings.Repeat("a", 32)
	var mu sync.Mutex
	var updateCalls []string
	release := make(chan bool, 4)

	bridge := NewRelayBridge(BridgeOptions{
		UpdateExtension: func(id string) (bool, error) {
			mu.Lock()
			updateCalls = append(updateCalls, id)
			mu.Unlock()
			return <-release, nil
		},
	})

	// While a CDP client is connected, update is deferred.
	clientConn := bridge.CDPConnected(newFakeCDPSock(), "")
	ext := newFakeExtSock()
	connectExtInstance(bridge, ext, "chrome", "", []TabSnapshot{mkTab(1)}, map[string]any{
		"extensionId":    extID,
		"ompVersion":     "18.6.1",
		"reloadProtocol": 1,
	})
	// Mark extension busy before closing client.
	bridge.ExtMessage(ext, `{"t":"busy","busy":true}`)
	bridge.CDPClosed(clientConn)
	time.Sleep(30 * time.Millisecond)
	mu.Lock()
	if len(updateCalls) != 0 {
		mu.Unlock()
		t.Fatalf("expected update deferred while busy, got %v", updateCalls)
	}
	mu.Unlock()

	// Extension turns idle -> update runs -> not ready while rewriting -> reloads and retires socket.
	bridge.ExtMessage(ext, `{"t":"busy","busy":false}`)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(updateCalls)
		mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if bridge.Ready() {
		t.Fatal("expected bridge not ready while updating extension files")
	}
	release <- true
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && ext.reloads() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if ext.reloads() != 1 || !ext.isClosed() {
		t.Fatalf("expected 1 reload and closed socket, got reloads=%d closed=%v", ext.reloads(), ext.isClosed())
	}

	// Same old version coming back must NOT reload a second time.
	again := newFakeExtSock()
	connectExtInstance(bridge, again, "chrome", "", []TabSnapshot{mkTab(1)}, map[string]any{
		"extensionId":    extID,
		"ompVersion":     "18.6.1",
		"reloadProtocol": 1,
	})
	time.Sleep(30 * time.Millisecond)
	if again.reloads() != 0 {
		t.Fatalf("expected no reload loop for same version")
	}
}
