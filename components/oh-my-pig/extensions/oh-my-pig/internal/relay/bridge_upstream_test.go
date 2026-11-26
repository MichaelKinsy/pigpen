package relay

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Full upstream test port for:
//   - packages/coding-agent/test/tools/browser-relay-bridge.test.ts (117 cases)
//   - packages/coding-agent/test/tools/browser-relay-self-update.test.ts (25 cases)
//
// Each subtest is named with its upstream file, line, and title.

// ---- helper extensions for bridge parity tests ------------------------------

func (f *fakeCDPSock) messagesOf(method string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []map[string]any
	for _, m := range f.messages {
		if m["method"] == method {
			out = append(out, m)
		}
	}
	return out
}

func (f *fakeCDPSock) attachedTargetIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, m := range f.messages {
		if m["method"] != "Target.attachedToTarget" {
			continue
		}
		if p, ok := m["params"].(map[string]any); ok {
			if info, ok := p["targetInfo"].(map[string]any); ok {
				if id, ok := info["targetId"].(string); ok {
					out = append(out, id)
				}
			}
		}
	}
	return out
}

func (f *fakeCDPSock) destroyedTargetIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, m := range f.messages {
		if m["method"] != "Target.targetDestroyed" {
			continue
		}
		if p, ok := m["params"].(map[string]any); ok {
			if id, ok := p["targetId"].(string); ok {
				out = append(out, id)
			}
		}
	}
	return out
}

func (f *fakeCDPSock) attachedSessionIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, m := range f.messages {
		if m["method"] != "Target.attachedToTarget" {
			continue
		}
		if p, ok := m["params"].(map[string]any); ok {
			if sid, ok := p["sessionId"].(string); ok {
				out = append(out, sid)
			}
		}
	}
	return out
}

func (f *fakeCDPSock) replyFor(id int) (map[string]any, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.messages {
		if v, ok := m["id"].(float64); ok && int(v) == id {
			return m, true
		}
	}
	return nil, false
}

func (f *fakeCDPSock) sessionFor(id int) string {
	m, ok := f.replyFor(id)
	if !ok {
		return ""
	}
	res, _ := m["result"].(map[string]any)
	sid, _ := res["sessionId"].(string)
	return sid
}

func waitCond(t *testing.T, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", desc)
}

func sendCDP(bridge *RelayBridge, connID int, sessionID, method string, params map[string]any) int {
	id := nextID()
	env := map[string]any{"id": id, "method": method}
	if sessionID != "" {
		env["sessionId"] = sessionID
	}
	if params != nil {
		env["params"] = params
	}
	b, _ := json.Marshal(env)
	bridge.CDPMessage(connID, string(b))
	return id
}

func sendExt(bridge *RelayBridge, sock *fakeExtSock, payload any) {
	b, _ := json.Marshal(payload)
	bridge.ExtMessage(sock, string(b))
}

func ackOne(bridge *RelayBridge, sock *fakeExtSock, id int, ok bool, result any, errMsg string) {
	sock.markAcked(id)
	msg := map[string]any{"t": "rpcResult", "id": id, "ok": ok}
	if ok {
		if result == nil {
			result = map[string]any{}
		}
		msg["result"] = result
	} else {
		msg["error"] = errMsg
	}
	sendExt(bridge, sock, msg)
}

func switchSendValues(ext *fakeExtSock, method string) []any {
	var out []any
	for _, rpc := range ext.rpcs("send") {
		if rpc.Method == method {
			out = append(out, rpc.Params["enabled"])
		}
	}
	return out
}

func armPageAutoAttach(t *testing.T, bridge *RelayBridge, ext *fakeExtSock, cdp *fakeCDPSock, connID int, sessionID string, ok bool) {
	t.Helper()
	id := sendCDP(bridge, connID, sessionID, "Target.setAutoAttach", map[string]any{
		"autoAttach": true, "waitForDebuggerOnStart": false, "flatten": true,
	})
	p := ext.waitPending(t, "send")
	if ok {
		ackOne(bridge, ext, p[0].ID, true, map[string]any{}, "")
	} else {
		ackOne(bridge, ext, p[0].ID, false, nil, "Target.setAutoAttach failed")
	}
	cdp.waitReply(t, id)
}

func childTrafficOf(cdp *fakeCDPSock, childID string) [][2]string {
	var out [][2]string
	for _, m := range cdp.snapshot() {
		method, _ := m["method"].(string)
		if method == "" {
			continue
		}
		sid, _ := m["sessionId"].(string)
		p, _ := m["params"].(map[string]any)
		psid, _ := p["sessionId"].(string)
		if sid == childID || psid == childID {
			out = append(out, [2]string{method, sid})
		}
	}
	return out
}

// ============================================================================
// upstream: browser-relay-bridge.test.ts — Child targets (lines 449-977)
// ============================================================================

func TestUpstreamBridge_ChildTargets(t *testing.T) {
	attachEv := map[string]any{
		"t": "cdpEvent", "tabId": 1, "method": "Target.attachedToTarget",
		"params": map[string]any{"sessionId": "CHILD1", "targetInfo": map[string]any{"targetId": "FRAME1", "type": "iframe"}},
	}
	nestedEv := map[string]any{
		"t": "cdpEvent", "tabId": 1, "sessionId": "CHILD1", "method": "Target.attachedToTarget",
		"params": map[string]any{"sessionId": "CHILD2", "targetInfo": map[string]any{"targetId": "FRAME2", "type": "iframe"}},
	}
	navEv := map[string]any{"t": "cdpEvent", "tabId": 1, "sessionId": "CHILD1", "method": "Page.frameNavigated", "params": map[string]any{}}
	detachEv := map[string]any{"t": "cdpEvent", "tabId": 1, "method": "Target.detachedFromTarget", "params": map[string]any{"sessionId": "CHILD1"}}

	t.Run(`upstream: browser-relay-bridge.test.ts:496 "owns an explicitly attached child without arming auto-attach"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		page := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		otherCDP := newFakeCDPSock()
		other := bridge.CDPConnected(otherCDP, "")
		otherPage := attachPageTab(t, bridge, ext, otherCDP, other, 1, "anon")

		attID := sendCDP(bridge, conn, page, "Target.attachToTarget", map[string]any{"targetId": "FRAME1", "flatten": true})
		p := ext.waitPending(t, "send")
		sendExt(bridge, ext, attachEv)
		ackOne(bridge, ext, p[0].ID, true, map[string]any{"sessionId": "CHILD1"}, "")
		cdp.waitReply(t, attID)
		if got := cdp.sessionFor(attID); got != "CHILD1" {
			t.Fatalf("got session %q, want CHILD1", got)
		}

		evID := sendCDP(bridge, conn, "CHILD1", "Runtime.evaluate", map[string]any{"expression": "1"})
		evOther := sendCDP(bridge, other, "CHILD1", "Runtime.evaluate", map[string]any{"expression": "1"})
		p = ext.waitPending(t, "send")
		ackOne(bridge, ext, p[0].ID, true, map[string]any{"result": map[string]any{"type": "number", "value": 1}}, "")
		cdp.waitReply(t, evID)
		otherRep := otherCDP.waitReply(t, evOther)
		if !strings.Contains(fmt.Sprint(otherRep["error"]), "Unknown session id CHILD1") {
			t.Fatalf("expected Unknown session id CHILD1 for other conn, got %+v", otherRep)
		}

		// Arm auto-attach on CHILD1, receive nested CHILD2, then arm + disarm page auto-attach:
		// AUTOMATIC child is revoked, explicit CHILD1 and descendant CHILD2 survive.
		armPageAutoAttach(t, bridge, ext, cdp, conn, "CHILD1", true)
		sendExt(bridge, ext, nestedEv)
		armPageAutoAttach(t, bridge, ext, cdp, conn, page, true)
		armPageAutoAttach(t, bridge, ext, otherCDP, other, otherPage, true)
		sendExt(bridge, ext, map[string]any{
			"t": "cdpEvent", "tabId": 1, "method": "Target.attachedToTarget",
			"params": map[string]any{"sessionId": "AUTOMATIC", "targetInfo": map[string]any{"targetId": "FRAME2", "type": "iframe"}},
		})
		disID := sendCDP(bridge, conn, page, "Target.setAutoAttach", map[string]any{"autoAttach": false, "waitForDebuggerOnStart": false, "flatten": true})
		cdp.waitReply(t, disID)

		// Explicit CHILD1 and descendant CHILD2 still work.
		afterID := sendCDP(bridge, conn, "CHILD1", "Runtime.evaluate", map[string]any{"expression": "1"})
		p = ext.waitPending(t, "send")
		ackOne(bridge, ext, p[0].ID, true, map[string]any{"result": map[string]any{"value": 1}}, "")
		if rep := cdp.waitReply(t, afterID); rep["error"] != nil {
			t.Fatalf("explicit CHILD1 failed after opt-out: %+v", rep)
		}
		descID := sendCDP(bridge, conn, "CHILD2", "Runtime.evaluate", map[string]any{"expression": "1"})
		p = ext.waitPending(t, "send")
		ackOne(bridge, ext, p[0].ID, true, map[string]any{"result": map[string]any{"value": 1}}, "")
		if rep := cdp.waitReply(t, descID); rep["error"] != nil {
			t.Fatalf("explicit descendant CHILD2 failed after opt-out: %+v", rep)
		}
		sendExt(bridge, ext, navEv)
		sendExt(bridge, ext, detachEv)
		wantOther := [][2]string{
			{"Target.attachedToTarget", otherPage},
			{"Page.frameNavigated", "CHILD1"},
			{"Target.detachedFromTarget", otherPage},
		}
		if got := childTrafficOf(otherCDP, "CHILD1"); !reflect.DeepEqual(got, wantOther) {
			t.Fatalf("otherCDP traffic = %v, want %v", got, wantOther)
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:578 "announces a real child target only to the page session that armed auto-attach"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		armed := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		armPageAutoAttach(t, bridge, ext, cdp, conn, armed, true)
		_ = attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		otherCDP := newFakeCDPSock()
		_ = attachPageTab(t, bridge, ext, otherCDP, bridge.CDPConnected(otherCDP, ""), 1, "anon")

		sendExt(bridge, ext, attachEv)
		sendExt(bridge, ext, navEv)
		sendExt(bridge, ext, detachEv)
		want := [][2]string{{"Target.attachedToTarget", armed}, {"Page.frameNavigated", "CHILD1"}, {"Target.detachedFromTarget", armed}}
		if got := childTrafficOf(cdp, "CHILD1"); !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		if got := childTrafficOf(otherCDP, "CHILD1"); len(got) != 0 {
			t.Fatalf("otherCDP got unexpected child traffic: %v", got)
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:596 "keeps a child on the first armed session of its connection, even after that session is released"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		first := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		armPageAutoAttach(t, bridge, ext, cdp, conn, first, true)
		second := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		armPageAutoAttach(t, bridge, ext, cdp, conn, second, true)

		sendExt(bridge, ext, attachEv)
		detID := sendCDP(bridge, conn, "", "Target.detachFromTarget", map[string]any{"sessionId": first})
		cdp.waitReply(t, detID)
		sendExt(bridge, ext, navEv)
		sendExt(bridge, ext, detachEv)
		want := [][2]string{{"Target.attachedToTarget", first}, {"Page.frameNavigated", "CHILD1"}, {"Target.detachedFromTarget", first}}
		if got := childTrafficOf(cdp, "CHILD1"); !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:618 "revokes automatic children when the last local page is released while another connection remains armed"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		page := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		armPageAutoAttach(t, bridge, ext, cdp, conn, page, true)
		otherCDP := newFakeCDPSock()
		other := bridge.CDPConnected(otherCDP, "")
		otherPage := attachPageTab(t, bridge, ext, otherCDP, other, 1, "anon")
		armPageAutoAttach(t, bridge, ext, otherCDP, other, otherPage, true)

		sendExt(bridge, ext, attachEv)
		detID := sendCDP(bridge, conn, "", "Target.detachFromTarget", map[string]any{"sessionId": page})
		cdp.waitReply(t, detID)
		cmdID := sendCDP(bridge, conn, "CHILD1", "Runtime.evaluate", map[string]any{"expression": "1"})
		rep := cdp.waitReply(t, cmdID)
		if !strings.Contains(fmt.Sprint(rep["error"]), "Unknown session id CHILD1") {
			t.Fatalf("expected Unknown session id CHILD1, got %+v", rep)
		}
		sendExt(bridge, ext, navEv)
		wantOther := [][2]string{{"Target.attachedToTarget", otherPage}, {"Page.frameNavigated", "CHILD1"}}
		if got := childTrafficOf(otherCDP, "CHILD1"); !reflect.DeepEqual(got, wantOther) {
			t.Fatalf("otherCDP got %v, want %v", got, wantOther)
		}
	})

	for _, leave := range []string{"disconnect", "release"} {
		t.Run(fmt.Sprintf(`upstream: browser-relay-bridge.test.ts:662 "isolates opt-out and disarms Chrome when the final armed holder leaves by %s"`, leave), func(t *testing.T) {
			bridge := NewRelayBridge(BridgeOptions{})
			ext := newFakeExtSock()
			connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
			firstCDP := newFakeCDPSock()
			first := bridge.CDPConnected(firstCDP, "")
			firstSession := attachPageTab(t, bridge, ext, firstCDP, first, 1, "anon")
			armPageAutoAttach(t, bridge, ext, firstCDP, first, firstSession, true)
			secondCDP := newFakeCDPSock()
			second := bridge.CDPConnected(secondCDP, "")
			secondSession := attachPageTab(t, bridge, ext, secondCDP, second, 1, "anon")
			armPageAutoAttach(t, bridge, ext, secondCDP, second, secondSession, true)
			if leave == "disconnect" {
				s2b := attachPageTab(t, bridge, ext, secondCDP, second, 1, "anon")
				armPageAutoAttach(t, bridge, ext, secondCDP, second, s2b, true)
			}
			sendExt(bridge, ext, attachEv)
			disID := sendCDP(bridge, first, firstSession, "Target.setAutoAttach", map[string]any{"autoAttach": false, "waitForDebuggerOnStart": false, "flatten": true})
			firstCDP.waitReply(t, disID)
			if len(ext.pending("send")) != 0 {
				t.Fatalf("expected Chrome still armed while second holder remains")
			}
			if leave == "disconnect" {
				bridge.CDPClosed(second)
			} else {
				dID := sendCDP(bridge, second, "", "Target.detachFromTarget", map[string]any{"sessionId": secondSession})
				secondCDP.waitReply(t, dID)
			}
			p := ext.waitPending(t, "send")
			if p[0].Method != "Target.setAutoAttach" || p[0].Params["autoAttach"] != false {
				t.Fatalf("expected Target.setAutoAttach(false), got %+v", p[0])
			}
			ackOne(bridge, ext, p[0].ID, true, map[string]any{}, "")
		})
	}

	t.Run(`upstream: browser-relay-bridge.test.ts:748 "disarms Chrome after another holder's in-flight enable fails"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		firstCDP := newFakeCDPSock()
		first := bridge.CDPConnected(firstCDP, "")
		session := attachPageTab(t, bridge, ext, firstCDP, first, 1, "anon")
		armPageAutoAttach(t, bridge, ext, firstCDP, first, session, true)
		secondCDP := newFakeCDPSock()
		second := bridge.CDPConnected(secondCDP, "")
		other := attachPageTab(t, bridge, ext, secondCDP, second, 1, "anon")

		sendCDP(bridge, second, other, "Target.setAutoAttach", map[string]any{"autoAttach": true, "waitForDebuggerOnStart": false, "flatten": true})
		p1 := ext.waitPending(t, "send")
		sendCDP(bridge, first, session, "Target.setAutoAttach", map[string]any{"autoAttach": false, "waitForDebuggerOnStart": false, "flatten": true})
		ackOne(bridge, ext, p1[0].ID, false, nil, "failed")
		p2 := ext.waitPending(t, "send")
		if p2[0].Method != "Target.setAutoAttach" || p2[0].Params["autoAttach"] != false {
			t.Fatalf("expected disarm after failed enable, got %+v", p2[0])
		}
		ackOne(bridge, ext, p2[0].ID, true, map[string]any{}, "")
	})

	for _, leave := range []string{"disconnect", "release"} {
		t.Run(fmt.Sprintf(`upstream: browser-relay-bridge.test.ts:786 "retries a failed disable after its holder leaves by %s"`, leave), func(t *testing.T) {
			bridge := NewRelayBridge(BridgeOptions{})
			ext := newFakeExtSock()
			connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
			cdp := newFakeCDPSock()
			conn := bridge.CDPConnected(cdp, "")
			session := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
			armPageAutoAttach(t, bridge, ext, cdp, conn, session, true)
			reg := newFakeCDPSock()
			_ = attachPageTab(t, bridge, ext, reg, bridge.CDPConnected(reg, ""), 1, "anon")

			sendCDP(bridge, conn, session, "Target.setAutoAttach", map[string]any{"autoAttach": false, "waitForDebuggerOnStart": false, "flatten": true})
			p1 := ext.waitPending(t, "send")
			if leave == "disconnect" {
				bridge.CDPClosed(conn)
			} else {
				dID := sendCDP(bridge, conn, "", "Target.detachFromTarget", map[string]any{"sessionId": session})
				cdp.waitReply(t, dID)
			}
			ackOne(bridge, ext, p1[0].ID, false, nil, "failed")
			p2 := ext.waitPending(t, "send")
			if p2[0].Method != "Target.setAutoAttach" || p2[0].Params["autoAttach"] != false {
				t.Fatalf("expected retry disarm, got %+v", p2[0])
			}
			ackOne(bridge, ext, p2[0].ID, true, map[string]any{}, "")
		})
	}

	t.Run(`upstream: browser-relay-bridge.test.ts:824 "rejects a queued auto-attach after its page session is released"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		first := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		queued := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")

		sendCDP(bridge, conn, first, "Target.setAutoAttach", map[string]any{"autoAttach": true, "waitForDebuggerOnStart": false, "flatten": true})
		p1 := ext.waitPending(t, "send")
		queuedID := sendCDP(bridge, conn, queued, "Target.setAutoAttach", map[string]any{"autoAttach": true, "waitForDebuggerOnStart": false, "flatten": true})
		detID := sendCDP(bridge, conn, "", "Target.detachFromTarget", map[string]any{"sessionId": queued})
		cdp.waitReply(t, detID)
		ackOne(bridge, ext, p1[0].ID, true, map[string]any{}, "")
		rep := cdp.waitReply(t, queuedID)
		if !strings.Contains(fmt.Sprint(rep["error"]), "Unknown session id "+queued) {
			t.Fatalf("expected Unknown session id %s, got %+v", queued, rep)
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:867 "does not report children to a session whose Target.setAutoAttach failed"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		otherCDP := newFakeCDPSock()
		other := bridge.CDPConnected(otherCDP, "")
		otherSession := attachPageTab(t, bridge, ext, otherCDP, other, 1, "anon")
		armPageAutoAttach(t, bridge, ext, otherCDP, other, otherSession, true)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		failed := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		armPageAutoAttach(t, bridge, ext, cdp, conn, failed, false)

		sendExt(bridge, ext, attachEv)
		sendExt(bridge, ext, navEv)
		sendExt(bridge, ext, detachEv)
		if len(childTrafficOf(cdp, "CHILD1")) != 0 {
			t.Fatalf("failed session should receive no child traffic")
		}
		if len(childTrafficOf(otherCDP, "CHILD1")) != 3 {
			t.Fatalf("other session missing child traffic")
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:900 "tells a connection that arms auto-attach late about the children Chrome already reported"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		firstCDP := newFakeCDPSock()
		first := bridge.CDPConnected(firstCDP, "")
		armPageAutoAttach(t, bridge, ext, firstCDP, first, attachPageTab(t, bridge, ext, firstCDP, first, 1, "anon"), true)
		sendExt(bridge, ext, attachEv)
		armPageAutoAttach(t, bridge, ext, firstCDP, first, "CHILD1", true)
		sendExt(bridge, ext, nestedEv)

		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		late := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		armPageAutoAttach(t, bridge, ext, cdp, conn, late, true)
		armPageAutoAttach(t, bridge, ext, cdp, conn, "CHILD1", true)
		sendExt(bridge, ext, navEv)
		sendExt(bridge, ext, detachEv)

		if len(childTrafficOf(cdp, "CHILD1")) < 3 {
			t.Fatalf("late connection did not receive replayed CHILD1 lifecycle: %v", childTrafficOf(cdp, "CHILD1"))
		}
		if len(childTrafficOf(cdp, "CHILD2")) < 1 {
			t.Fatalf("late connection did not receive replayed nested CHILD2")
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:930 "does not tell a later connection about children that ended with the debugger attachment"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		firstCDP := newFakeCDPSock()
		first := bridge.CDPConnected(firstCDP, "")
		firstSession := attachPageTab(t, bridge, ext, firstCDP, first, 1, "anon")
		armPageAutoAttach(t, bridge, ext, firstCDP, first, firstSession, true)
		sendExt(bridge, ext, attachEv)
		detID := sendCDP(bridge, first, "", "Target.detachFromTarget", map[string]any{"sessionId": firstSession})
		p := ext.waitPending(t, "detach")
		ackOne(bridge, ext, p[0].ID, true, map[string]any{}, "")
		firstCDP.waitReply(t, detID)

		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		late := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		armPageAutoAttach(t, bridge, ext, cdp, conn, late, true)
		if len(childTrafficOf(cdp, "CHILD1")) != 0 {
			t.Fatalf("stale child announced to late connection")
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:955 "detaches children from connections still holding the tab when the extension lost its attachment"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		session := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		armPageAutoAttach(t, bridge, ext, cdp, conn, session, true)
		sendExt(bridge, ext, attachEv)
		armPageAutoAttach(t, bridge, ext, cdp, conn, "CHILD1", true)
		sendExt(bridge, ext, nestedEv)
		before := len(cdp.snapshot())

		connectExt(bridge, newFakeExtSock(), []TabSnapshot{mkTab(1)}, nil)
		var detaches [][2]string
		for _, m := range cdp.snapshot()[before:] {
			if m["method"] == "Target.detachedFromTarget" {
				sid, _ := m["sessionId"].(string)
				p, _ := m["params"].(map[string]any)
				csid, _ := p["sessionId"].(string)
				detaches = append(detaches, [2]string{sid, csid})
			}
		}
		want := [][2]string{{"CHILD1", "CHILD2"}, {session, "CHILD1"}}
		if !reflect.DeepEqual(detaches, want) {
			t.Fatalf("detaches = %v, want %v", detaches, want)
		}
	})
}

// ============================================================================
// upstream: browser-relay-bridge.test.ts — Runtime, deadlines, attachment release (lines 979-1727)
// ============================================================================

func TestUpstreamBridge_RuntimeDeadlinesAndAttachmentRelease(t *testing.T) {
	anon := InstanceCode("anon", "")

	t.Run(`upstream: browser-relay-bridge.test.ts:1052 "keeps a pipelined Runtime.disable authoritative while root enable completes"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		sid := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		enID := sendCDP(bridge, conn, sid, "Runtime.enable", nil)
		p1 := ext.waitPending(t, "send")
		disID := sendCDP(bridge, conn, sid, "Runtime.disable", nil)
		cdp.waitReply(t, disID)
		ackOne(bridge, ext, p1[0].ID, true, map[string]any{}, "")
		p2 := ext.waitPending(t, "send")
		sendExt(bridge, ext, map[string]any{"t": "cdpEvent", "tabId": 1, "method": "Runtime.executionContextCreated", "params": map[string]any{"context": map[string]any{"id": 19}}})
		ackOne(bridge, ext, p2[0].ID, true, map[string]any{}, "")
		cdp.waitReply(t, enID)
		if len(cdp.messagesOf("Runtime.executionContextCreated")) != 0 {
			t.Fatalf("disabled session should not receive executionContextCreated")
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:1082 "refreshes Runtime contexts after the extension reconnects"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext1 := newFakeExtSock()
		connectExt(bridge, ext1, []TabSnapshot{mkTab(1)}, nil)
		c1 := newFakeCDPSock()
		conn1 := bridge.CDPConnected(c1, "")
		s1 := attachPageTab(t, bridge, ext1, c1, conn1, 1, "anon")
		en1 := sendCDP(bridge, conn1, s1, "Runtime.enable", nil)
		p := ext1.waitPending(t, "send")
		ackOne(bridge, ext1, p[0].ID, true, map[string]any{}, "")
		p = ext1.waitPending(t, "send")
		sendExt(bridge, ext1, map[string]any{"t": "cdpEvent", "tabId": 1, "method": "Runtime.executionContextCreated", "params": map[string]any{"context": map[string]any{"id": 17}}})
		ackOne(bridge, ext1, p[0].ID, true, map[string]any{}, "")
		c1.waitReply(t, en1)

		bridge.ExtClosed(ext1)
		ext2 := newFakeExtSock()
		connectExt(bridge, ext2, []TabSnapshot{mkTab(1)}, []int{1})
		c2 := newFakeCDPSock()
		conn2 := bridge.CDPConnected(c2, "")
		s2 := attachPageTab(t, bridge, ext2, c2, conn2, 1, "anon")
		en2 := sendCDP(bridge, conn2, s2, "Runtime.enable", nil)
		p = ext2.waitPending(t, "send")
		if p[0].Method != "Runtime.disable" {
			t.Fatalf("expected Runtime.disable after reconnect, got %s", p[0].Method)
		}
		ackOne(bridge, ext2, p[0].ID, true, map[string]any{}, "")
		p = ext2.waitPending(t, "send")
		sendExt(bridge, ext2, map[string]any{"t": "cdpEvent", "tabId": 1, "method": "Runtime.executionContextCreated", "params": map[string]any{"context": map[string]any{"id": 18}}})
		ackOne(bridge, ext2, p[0].ID, true, map[string]any{}, "")
		c2.waitReply(t, en2)
		ctxs := c2.messagesOf("Runtime.executionContextCreated")
		if len(ctxs) != 1 || ctxs[0]["params"].(map[string]any)["context"].(map[string]any)["id"] != float64(18) {
			t.Fatalf("expected fresh context 18, got %+v", ctxs)
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:1181 "page-bound commands use longer deadline than immediate commands"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{
			RPCTimeout:          40 * time.Millisecond,
			PageBoundRPCTimeout: 140 * time.Millisecond,
		})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		sid := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")

		immID := sendCDP(bridge, conn, sid, "Input.dispatchMouseEvent", map[string]any{"type": "mouseMoved", "x": 1, "y": 1})
		pbID := sendCDP(bridge, conn, sid, "Page.navigate", map[string]any{"url": "https://example.com/slow"})
		immRep := cdp.waitReply(t, immID)
		if !strings.Contains(fmt.Sprint(immRep["error"]), "Input.dispatchMouseEvent on tab 1: Chrome sent no answer within 40ms") {
			t.Fatalf("unexpected immediate timeout error: %+v", immRep)
		}
		if _, early := cdp.replyFor(pbID); early {
			t.Fatalf("page-bound command timed out too early")
		}
		pbRep := cdp.waitReply(t, pbID)
		if !strings.Contains(fmt.Sprint(pbRep["error"]), "Page.navigate on tab 1: Chrome sent no answer within 140ms") {
			t.Fatalf("unexpected page-bound timeout error: %+v", pbRep)
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:1204 "keeps a tab Chrome detached on its own adoptable, but bans it after the user cancels the debugging bar"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		dID := sendCDP(bridge, conn, "", "Target.setDiscoverTargets", map[string]any{})
		cdp.waitReply(t, dID)
		sid := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")

		sendExt(bridge, ext, map[string]any{"t": "detached", "tabId": 1, "reason": "target_closed"})
		if len(bridge.ListTargets()) != 1 {
			t.Fatalf("non-user detach should keep tab listed")
		}
		sid2 := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		if sid2 == sid {
			t.Fatalf("expected new session after re-adopt")
		}

		sendExt(bridge, ext, map[string]any{"t": "detached", "tabId": 1, "reason": "canceled_by_user"})
		if len(bridge.ListTargets()) != 0 {
			t.Fatalf("canceled_by_user should hide tab")
		}
		refID := sendCDP(bridge, conn, "", "Target.attachToTarget", map[string]any{"targetId": "PAGE" + anon + ".1"})
		if rep := cdp.waitReply(t, refID); rep["error"] == nil {
			t.Fatalf("expected attach refusal after canceled_by_user, got %+v", rep)
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:1253 "tells the driving session why Chrome detached before failing its command in flight"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{ChromeDetachReport: 500 * time.Millisecond})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		sid := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		clickID := sendCDP(bridge, conn, sid, "Input.dispatchMouseEvent", map[string]any{"type": "mousePressed"})
		p := ext.waitPending(t, "send")
		ackOne(bridge, ext, p[0].ID, false, nil, "Detached while handling command.")
		time.Sleep(25 * time.Millisecond)
		if _, premature := cdp.replyFor(clickID); premature {
			t.Fatalf("command failure must wait for OMP.debuggerDetached")
		}
		sendExt(bridge, ext, map[string]any{"t": "detached", "tabId": 1, "reason": "target_closed"})
		cdp.waitReply(t, clickID)
		msgs := cdp.snapshot()
		toldIdx, failIdx := -1, -1
		for i, m := range msgs {
			if m["method"] == "OMP.debuggerDetached" && m["sessionId"] == sid && toldIdx < 0 {
				toldIdx = i
			}
			if v, ok := m["id"].(float64); ok && int(v) == clickID {
				failIdx = i
			}
		}
		if toldIdx < 0 || failIdx <= toldIdx {
			t.Fatalf("expected OMP.debuggerDetached (%d) before error reply (%d)", toldIdx, failIdx)
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:1283 "neither reports nor re-announces the detach of a tab the relay is closing"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		cdp.waitReply(t, sendCDP(bridge, conn, "", "Target.setDiscoverTargets", map[string]any{}))
		sid := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		closeID := sendCDP(bridge, conn, sid, "Target.closeTarget", map[string]any{"targetId": "PAGE" + anon + ".1"})
		p := ext.waitPending(t, "removeTab")
		sendExt(bridge, ext, map[string]any{"t": "detached", "tabId": 1, "reason": "target_closed"})
		if len(cdp.messagesOf("OMP.debuggerDetached")) != 0 {
			t.Fatalf("closing tab must not emit OMP.debuggerDetached")
		}
		ackOne(bridge, ext, p[0].ID, true, map[string]any{}, "")
		sendExt(bridge, ext, map[string]any{"t": "tabRemoved", "tabId": 1})
		cdp.waitReply(t, closeID)
		if len(bridge.ListTargets()) != 0 {
			t.Fatalf("expected 0 targets after close")
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:1321 "detaches cleanly on explicit last-session release and permits reattachment"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{Group: &DefaultGroupConfig})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		sid := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		cdp.waitReply(t, sendCDP(bridge, conn, "", "Target.detachFromTarget", map[string]any{"sessionId": sid}))
		p := ext.waitPending(t, "detach")
		sendExt(bridge, ext, map[string]any{"t": "detached", "tabId": 1, "reason": "target_closed", "relayInitiated": true})
		ackOne(bridge, ext, p[0].ID, true, map[string]any{}, "")
		_ = attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		if len(cdp.destroyedTargetIDs()) != 0 {
			t.Fatalf("relay-initiated detach must not destroy target")
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:1355 "serializes immediate reattachment behind the detach RPC and its echo"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		sid := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		cdp.waitReply(t, sendCDP(bridge, conn, "", "Target.detachFromTarget", map[string]any{"sessionId": sid}))
		pDet := ext.waitPending(t, "detach")

		reID := sendCDP(bridge, conn, "", "Target.attachToTarget", map[string]any{"targetId": "PAGE" + anon + ".1"})
		time.Sleep(25 * time.Millisecond)
		if len(ext.rpcs("attach")) != 1 {
			t.Fatalf("expected reattach serialized behind detach, got %d attaches", len(ext.rpcs("attach")))
		}
		sendExt(bridge, ext, map[string]any{"t": "detached", "tabId": 1, "reason": "target_closed", "relayInitiated": true})
		ackOne(bridge, ext, pDet[0].ID, true, map[string]any{}, "")
		pAtt := ext.waitPending(t, "attach")
		ackOne(bridge, ext, pAtt[0].ID, true, map[string]any{}, "")
		if cdp.waitReply(t, reID)["error"] != nil {
			t.Fatalf("reattach failed")
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:1430 "detaches once the tab session released alongside the page session leaves no holder"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		aaID := sendCDP(bridge, conn, "", "Target.setAutoAttach", nil)
		p := ext.waitPending(t, "attach")
		ackOne(bridge, ext, p[0].ID, true, map[string]any{}, "")
		cdp.waitReply(t, aaID)
		pageSid := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		var tabSid string
		for _, s := range cdp.attachedSessionIDs() {
			if s != pageSid {
				tabSid = s
				break
			}
		}
		cdp.waitReply(t, sendCDP(bridge, conn, "", "Target.detachFromTarget", map[string]any{"sessionId": pageSid}))
		if len(ext.rpcs("detach")) != 0 {
			t.Fatalf("tab session still holds attachment")
		}
		cdp.waitReply(t, sendCDP(bridge, conn, "", "Target.detachFromTarget", map[string]any{"sessionId": tabSid}))
		ext.waitPending(t, "detach")
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:1458 "retracts held sessions when reconnect reattachment fails"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		sid := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		rep := newFakeExtSock()
		connectExt(bridge, rep, []TabSnapshot{mkTab(1)}, nil)
		p := rep.waitPending(t, "attach")
		ackOne(bridge, rep, p[0].ID, false, nil, "debugger unavailable")
		ev := cdp.waitEvent(t, "Target.detachedFromTarget")
		if ev["params"].(map[string]any)["sessionId"] != sid {
			t.Fatalf("expected detachedFromTarget for %s, got %+v", sid, ev)
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:1516 "does not ban a tab when its in-flight attach is interrupted by extension replacement"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		sendCDP(bridge, conn, "", "Target.attachToTarget", map[string]any{"targetId": "PAGE" + anon + ".1"})
		ext.waitPending(t, "attach")
		rep := newFakeExtSock()
		connectExt(bridge, rep, []TabSnapshot{mkTab(1)}, nil)
		time.Sleep(20 * time.Millisecond)
		sid := attachPageTab(t, bridge, rep, cdp, conn, 1, "anon")
		if sid == "" {
			t.Fatal("expected retry attach to succeed after replacement")
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:1544 "clears an in-flight detach immediately when the extension socket is replaced"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		sid := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		cdp.waitReply(t, sendCDP(bridge, conn, "", "Target.detachFromTarget", map[string]any{"sessionId": sid}))
		ext.waitPending(t, "detach")

		rep := newFakeExtSock()
		connectExt(bridge, rep, []TabSnapshot{mkTab(1)}, nil)
		repSid := attachPageTab(t, bridge, rep, cdp, conn, 1, "anon")

		// Delayed relayInitiated detach from old socket must not retract replacement session.
		sendExt(bridge, rep, map[string]any{"t": "detached", "tabId": 1, "reason": "target_closed", "relayInitiated": true})
		for _, m := range cdp.messagesOf("Target.detachedFromTarget") {
			if m["params"].(map[string]any)["sessionId"] == repSid {
				t.Fatalf("delayed relayInitiated detach retracted new session")
			}
		}
		// Genuine user cancellation retracts replacement session.
		sendExt(bridge, rep, map[string]any{"t": "detached", "tabId": 1, "reason": "canceled_by_user"})
		waitCond(t, "user detach", func() bool {
			for _, m := range cdp.messagesOf("Target.detachedFromTarget") {
				if m["params"].(map[string]any)["sessionId"] == repSid {
					return true
				}
			}
			return false
		})
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:1608 "still fans root Runtime events out to a session that never enabled the domain"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		sid := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		sendExt(bridge, ext, map[string]any{"t": "cdpEvent", "tabId": 1, "method": "Runtime.executionContextCreated", "params": map[string]any{"context": map[string]any{"id": 42}}})
		if len(cdp.messagesOf("Runtime.executionContextCreated")) != 1 {
			t.Fatalf("default session must receive root Runtime events")
		}
		cdp.waitReply(t, sendCDP(bridge, conn, sid, "Runtime.disable", nil))
		sendExt(bridge, ext, map[string]any{"t": "cdpEvent", "tabId": 1, "method": "Runtime.executionContextCreated", "params": map[string]any{"context": map[string]any{"id": 43}}})
		if len(cdp.messagesOf("Runtime.executionContextCreated")) != 1 {
			t.Fatalf("explicitly disabled session must not receive later Runtime events")
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:1644 "holds and fails pipelined duplicate Runtime.enable with root cycle"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		sid := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")

		en1 := sendCDP(bridge, conn, sid, "Runtime.enable", nil)
		p := ext.waitPending(t, "send")
		en2 := sendCDP(bridge, conn, sid, "Runtime.enable", nil)
		time.Sleep(15 * time.Millisecond)
		if _, ok := cdp.replyFor(en1); ok {
			t.Fatal("en1 acked early")
		}
		if _, ok := cdp.replyFor(en2); ok {
			t.Fatal("en2 acked early")
		}
		ackOne(bridge, ext, p[0].ID, false, nil, "root disable failed")
		if cdp.waitReply(t, en1)["error"] == nil || cdp.waitReply(t, en2)["error"] == nil {
			t.Fatal("both pipelined enables must fail when root cycle fails")
		}
	})
}

// ============================================================================
// upstream: browser-relay-bridge.test.ts — Multi-instance, affine reconnect,
// page-session target lifecycle, auto-attach gating, front app, switches,
// active tab metadata (lines 1728-3150)
// ============================================================================

type fakeFrontApp struct {
	fn func(time.Duration) (int, error)
}

func (f fakeFrontApp) KeepInFront(d time.Duration) (int, error) { return f.fn(d) }

func TestUpstreamBridge_MultiInstancePageLifecycleAutoAttachAndFrontApp(t *testing.T) {
	t.Run(`upstream: browser-relay-bridge.test.ts:1729 "multiple instances, browserSession retirement, affine reconnect, late createTab"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{
			AffineReconnectWait: 80 * time.Millisecond,
			RPCTimeout:          40 * time.Millisecond,
			LateRPCKeep:         120 * time.Millisecond,
		})
		chrome := newFakeExtSock()
		edge := newFakeExtSock()
		connectExtInstance(bridge, chrome, "chrome", "run-1", []TabSnapshot{mkTab(1, func(s *TabSnapshot) { s.Active = true; s.Title = "Chrome tab" })}, nil)
		connectExtInstance(bridge, edge, "edge", "run-1", []TabSnapshot{mkTab(1, func(s *TabSnapshot) { s.Title = "Edge tab" })}, nil)
		if len(bridge.ListTargets()) != 2 {
			t.Fatalf("expected 2 targets across chrome+edge")
		}

		// Offline instance hides its tabs; reconnect restores; browserSession change retires old target ID.
		oldTarget := fmt.Sprintf("PAGE%s.1", InstanceCode("edge", "run-1"))
		bridge.ExtClosed(edge)
		if len(bridge.ListTargets()) != 1 {
			t.Fatalf("expected offline edge hidden")
		}
		// Target.createTarget with nearTargetId waits for edge to reconnect.
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		sendCDP(bridge, conn, "", "Target.createTarget", map[string]any{"url": "about:blank", "nearTargetId": oldTarget})
		time.Sleep(15 * time.Millisecond)
		if len(chrome.rpcs("createTab")) != 0 {
			t.Fatalf("should wait for affine browser reconnect")
		}
		edge2 := newFakeExtSock()
		connectExtInstance(bridge, edge2, "edge", "run-1", []TabSnapshot{mkTab(1)}, nil)
		pCreate := edge2.waitPending(t, "createTab")
		// Let createTab time out, then ack late -> bridge must close the orphaned tab.
		time.Sleep(60 * time.Millisecond)
		ackOne(bridge, edge2, pCreate[0].ID, true, map[string]any{"tab": mkTab(9)}, "")
		rm := edge2.waitPending(t, "removeTab")
		if rm[0].TabID != 9 {
			t.Fatalf("expected late createTab to close tab 9, got %+v", rm[0])
		}
		ackOne(bridge, edge2, rm[0].ID, true, map[string]any{}, "")

		// Browser restart (new browserSession) retires oldTarget so closeTarget fails cleanly.
		edge3 := newFakeExtSock()
		connectExtInstance(bridge, edge3, "edge", "run-2", []TabSnapshot{mkTab(1)}, nil)
		closeOld := sendCDP(bridge, conn, "", "Target.closeTarget", map[string]any{"targetId": oldTarget})
		if rep := cdp.waitReply(t, closeOld); rep["error"] == nil {
			t.Fatalf("expected error closing retired target, got %+v", rep)
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:2119 "page-session target lifecycle routes to page's browser and rejects unknown targets"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		chrome := newFakeExtSock()
		edge := newFakeExtSock()
		connectExtInstance(bridge, chrome, "chrome", "", []TabSnapshot{mkTab(1)}, nil)
		connectExtInstance(bridge, edge, "edge", "", []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		sid := attachPageTab(t, bridge, chrome, cdp, conn, 1, "chrome")
		createdID := fmt.Sprintf("PAGE%s.9", InstanceCode("chrome", ""))

		cID := sendCDP(bridge, conn, sid, "Target.createTarget", map[string]any{"url": "https://example.com/", "newWindow": true})
		p := chrome.waitPending(t, "createTab")
		if len(edge.rpcs("createTab")) != 0 {
			t.Fatal("page-session createTarget routed to wrong browser")
		}
		ackOne(bridge, chrome, p[0].ID, true, map[string]any{"tab": mkTab(9)}, "")
		if rep := cdp.waitReply(t, cID); rep["result"].(map[string]any)["targetId"] != createdID {
			t.Fatalf("unexpected createTarget reply: %+v", rep)
		}

		aID := sendCDP(bridge, conn, sid, "Target.activateTarget", map[string]any{"targetId": createdID})
		p = chrome.waitPending(t, "activateTab")
		ackOne(bridge, chrome, p[0].ID, true, map[string]any{}, "")
		cdp.waitReply(t, aID)

		badID := sendCDP(bridge, conn, sid, "Target.activateTarget", map[string]any{"targetId": "UNKNOWN"})
		if rep := cdp.waitReply(t, badID); rep["error"] == nil {
			t.Fatalf("expected error for unknown target on page session")
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:2219 "auto-attach gating: legacy hide, discarded retraction, revival, foreign-frame claim"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{Group: &DefaultGroupConfig})
		legacy := newFakeExtSock()
		bridge.ExtConnected(legacy, "")
		sendExt(bridge, legacy, map[string]any{"t": "hello", "userAgent": "t", "browserVersion": "Chrome/150", "tabs": []TabSnapshot{mkTab(1)}, "attachedTabIds": []int{}})
		ext := newFakeExtSock()
		connectExtInstance(bridge, ext, "sup", "", []TabSnapshot{
			mkTab(1, func(s *TabSnapshot) { s.Discarded = true }),
			mkTab(2),
		}, nil)
		if len(bridge.ListTargets()) != 2 {
			t.Fatalf("expected only supported instance's 2 tabs listed, got %d", len(bridge.ListTargets()))
		}
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		cdp.waitReply(t, sendCDP(bridge, conn, "", "Target.setDiscoverTargets", map[string]any{"discover": true}))
		aaID := sendCDP(bridge, conn, "", "Target.setAutoAttach", map[string]any{"autoAttach": true})
		p := ext.waitPending(t, "attach")
		if len(p) != 1 || p[0].TabID != 2 {
			t.Fatalf("expected only non-discarded tab 2 attached, got %+v", p)
		}
		ackOne(bridge, ext, p[0].ID, true, map[string]any{}, "")
		cdp.waitReply(t, aaID)

		// Revival of tab 1 attaches it.
		sendExt(bridge, ext, map[string]any{"t": "tabUpdated", "tab": mkTab(1, func(s *TabSnapshot) { s.Discarded = false })})
		pRev := ext.waitPending(t, "attach")
		ackOne(bridge, ext, pRev[0].ID, true, map[string]any{}, "")
		supCode := InstanceCode("sup", "")
		waitCond(t, "tab 1 attached", func() bool {
			for _, id := range cdp.attachedTargetIDs() {
				if id == "TAB"+supCode+".1" {
					return true
				}
			}
			return false
		})

		// Foreign-frame refusal cleared when worker claims tab.
		sendExt(bridge, ext, map[string]any{"t": "detached", "tabId": 1, "reason": "target_closed"})
		pRetry := ext.waitPending(t, "attach")
		ackOne(bridge, ext, pRetry[0].ID, false, nil, "Cannot access a chrome-extension:// URL of different extension")
		waitCond(t, "tab 1 refused", func() bool { return len(bridge.ListTargets()) == 1 })

		bridge.CDPConnected(newFakeCDPSock(), "PAGE"+supCode+".1")
		pClaim := ext.waitPending(t, "attach")
		if pClaim[0].ClearForeignFrames == nil || !*pClaim[0].ClearForeignFrames {
			t.Fatalf("expected clearForeignFrames=true after claim")
		}
		ackOne(bridge, ext, pClaim[0].ID, true, map[string]any{}, "")
		waitCond(t, "tab 1 restored", func() bool { return len(bridge.ListTargets()) == 2 })
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:2617 "front app preservation hook on Page.windowOpen"`, func(t *testing.T) {
		var mu sync.Mutex
		var holds []int
		var reads atomic.Int32
		bridge := NewRelayBridge(BridgeOptions{})
		bridge.KeepFrontAppWith(func() (FrontApp, error) {
			app := int(reads.Add(1))
			return fakeFrontApp{fn: func(d time.Duration) (int, error) {
				mu.Lock()
				holds = append(holds, app)
				mu.Unlock()
				return 1, nil
			}}, nil
		})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1), mkTab(2)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		s1 := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")

		// mouseMoved does not read front app.
		idMv := sendCDP(bridge, conn, s1, "Input.dispatchMouseEvent", map[string]any{"type": "mouseMoved", "x": 5, "y": 5})
		ackOne(bridge, ext, ext.waitPending(t, "send")[0].ID, true, map[string]any{}, "")
		cdp.waitReply(t, idMv)
		if reads.Load() != 0 {
			t.Fatalf("mouseMoved should not read front app")
		}

		// mousePressed reads front app; Page.windowOpen holds it once.
		idPress := sendCDP(bridge, conn, s1, "Input.dispatchMouseEvent", map[string]any{"type": "mousePressed", "x": 5, "y": 5})
		ackOne(bridge, ext, ext.waitPending(t, "send")[0].ID, true, map[string]any{}, "")
		cdp.waitReply(t, idPress)
		sendExt(bridge, ext, map[string]any{"t": "cdpEvent", "tabId": 1, "method": "Page.windowOpen", "params": map[string]any{"url": "https://example.com/"}})
		waitCond(t, "front app hold", func() bool {
			mu.Lock()
			defer mu.Unlock()
			return len(holds) == 1
		})
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:2843 "keeps only the newly activated tab active in its window"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{
			mkTab(1, func(s *TabSnapshot) { s.Title = "First"; s.Active = true; s.WindowID = 1 }),
			mkTab(2, func(s *TabSnapshot) { s.Title = "Second"; s.WindowID = 1 }),
			mkTab(3, func(s *TabSnapshot) { s.Title = "Other"; s.Active = true; s.WindowID = 2 }),
		}, nil)
		sendExt(bridge, ext, map[string]any{"t": "tabUpdated", "tab": mkTab(2, func(s *TabSnapshot) { s.Title = "Second"; s.Active = true; s.WindowID = 1 })})
		got := map[string]string{}
		for _, target := range bridge.ListTargets() {
			got[target["title"]] = target["active"]
		}
		if got["First"] != "false" || got["Second"] != "true" || got["Other"] != "true" {
			t.Fatalf("unexpected active flags: %+v", got)
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:3120 "switches off what a vanished client left on when relay restarts under live extension"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1), mkTab(2)}, []int{1})
		waitCond(t, "2 switch resets on tab 1", func() bool { return len(ext.rpcs("send")) == 2 })
		if got := switchSendValues(ext, "Page.setInterceptFileChooserDialog"); !reflect.DeepEqual(got, []any{false}) {
			t.Fatalf("expected chooser reset false, got %v", got)
		}
		if got := switchSendValues(ext, "Emulation.setFocusEmulationEnabled"); !reflect.DeepEqual(got, []any{false}) {
			t.Fatalf("expected focus reset false, got %v", got)
		}
	})
}

// ============================================================================
// upstream: browser-relay-self-update.test.ts (lines 115-491)
// ============================================================================

func TestUpstreamSelfUpdate_InstalledFolderAndGuards(t *testing.T) {
	origVer := RelayVersion
	RelayVersion = "19.1.1"
	defer func() { RelayVersion = origVer }()

	extID := strings.Repeat("a", 32)

	t.Run(`upstream: browser-relay-self-update.test.ts:436 "reloads an extension running another build of this version once per build"`, func(t *testing.T) {
		rebuilt := "rebuilt"
		stale := "stale"
		var writes atomic.Int32
		bridge := NewRelayBridge(BridgeOptions{
			InstalledStamp: func(string) (*RelayExtensionStamp, error) {
				return &RelayExtensionStamp{OmpVersion: RelayVersion, OmpBuild: &rebuilt}, nil
			},
			UpdateExtension: func(string) (bool, error) {
				writes.Add(1)
				return true, nil
			},
		})
		sock := newFakeExtSock()
		connectExtInstance(bridge, sock, "chrome", "", []TabSnapshot{mkTab(1)}, map[string]any{
			"extensionId": extID, "ompVersion": RelayVersion, "ompBuild": stale, "reloadProtocol": 1,
		})
		waitCond(t, "reload sent", func() bool { return sock.reloads() == 1 && sock.isClosed() })

		// Same stale build reconnecting must not reload again.
		back := newFakeExtSock()
		connectExtInstance(bridge, back, "chrome", "", []TabSnapshot{mkTab(1)}, map[string]any{
			"extensionId": extID, "ompVersion": RelayVersion, "ompBuild": stale, "reloadProtocol": 1,
		})
		time.Sleep(30 * time.Millisecond)
		if back.reloads() != 0 || writes.Load() != 0 {
			t.Fatalf("expected no second reload and no writes, reloads=%d writes=%d", back.reloads(), writes.Load())
		}
	})

	t.Run(`upstream: browser-relay-self-update.test.ts:454 "leaves matching build alone until recheck finds folder changed"`, func(t *testing.T) {
		cur := "current"
		rebuilt := "rebuilt"
		var mu sync.Mutex
		stamp := &RelayExtensionStamp{OmpVersion: RelayVersion, OmpBuild: &cur}
		bridge := NewRelayBridge(BridgeOptions{
			InstalledStamp: func(string) (*RelayExtensionStamp, error) {
				mu.Lock()
				defer mu.Unlock()
				return stamp, nil
			},
		})
		sock := newFakeExtSock()
		connectExtInstance(bridge, sock, "chrome", "", []TabSnapshot{mkTab(1)}, map[string]any{
			"extensionId": extID, "ompVersion": RelayVersion, "ompBuild": cur, "reloadProtocol": 1,
		})
		time.Sleep(30 * time.Millisecond)
		if sock.reloads() != 0 {
			t.Fatal("should not reload matching build")
		}
		mu.Lock()
		stamp = &RelayExtensionStamp{OmpVersion: RelayVersion, OmpBuild: &rebuilt}
		mu.Unlock()
		bridge.RecheckExtensions()
		waitCond(t, "reload after recheck", func() bool { return sock.reloads() == 1 })
	})

	t.Run(`upstream: browser-relay-self-update.test.ts:280 "releases an attach the last client left mid-way, then updates"`, func(t *testing.T) {
		var calls atomic.Int32
		bridge := NewRelayBridge(BridgeOptions{
			UpdateExtension: func(string) (bool, error) {
				calls.Add(1)
				return true, nil
			},
		})
		conn := bridge.CDPConnected(newFakeCDPSock(), "")
		sock := newFakeExtSock()
		connectExtInstance(bridge, sock, "chrome", "", []TabSnapshot{mkTab(1)}, map[string]any{
			"extensionId": extID, "ompVersion": "18.6.1", "reloadProtocol": 1,
		})
		sendCDP(bridge, conn, "", "Target.attachToTarget", map[string]any{"targetId": fmt.Sprintf("PAGE%s.1", InstanceCode("chrome", "")), "flatten": true})
		pAtt := sock.waitPending(t, "attach")
		bridge.CDPClosed(conn)
		time.Sleep(25 * time.Millisecond)
		if calls.Load() != 0 {
			t.Fatal("must not update while attach in flight")
		}
		ackOne(bridge, sock, pAtt[0].ID, true, map[string]any{}, "")
		pDet := sock.waitPending(t, "detach")
		if calls.Load() != 0 {
			t.Fatal("must not update while detach in flight")
		}
		ackOne(bridge, sock, pDet[0].ID, true, map[string]any{}, "")
		waitCond(t, "update + reload after detach", func() bool { return calls.Load() == 1 && sock.reloads() == 1 })
	})

	t.Run(`upstream: browser-relay-self-update.test.ts:162-187 "ignores extensions without reloadProtocol, current/newer, unstamped, or without extensionId"`, func(t *testing.T) {
		var calls atomic.Int32
		bridge := NewRelayBridge(BridgeOptions{
			UpdateExtension: func(string) (bool, error) {
				calls.Add(1)
				return true, nil
			},
		})
		connectExtInstance(bridge, newFakeExtSock(), "c1", "", []TabSnapshot{mkTab(1)}, map[string]any{"extensionId": extID, "ompVersion": "18.6.1", "reloadProtocol": 0})
		connectExtInstance(bridge, newFakeExtSock(), "c2", "", []TabSnapshot{mkTab(1)}, map[string]any{"extensionId": extID, "ompVersion": RelayVersion, "reloadProtocol": 1})
		connectExtInstance(bridge, newFakeExtSock(), "c3", "", []TabSnapshot{mkTab(1)}, map[string]any{"extensionId": extID, "ompVersion": "999.0.0", "reloadProtocol": 1})
		connectExtInstance(bridge, newFakeExtSock(), "c4", "", []TabSnapshot{mkTab(1)}, map[string]any{"extensionId": extID, "ompVersion": "", "reloadProtocol": 1})
		connectExtInstance(bridge, newFakeExtSock(), "c5", "", []TabSnapshot{mkTab(1)}, map[string]any{"extensionId": "", "ompVersion": "18.6.1", "reloadProtocol": 1})
		time.Sleep(30 * time.Millisecond)
		if calls.Load() != 0 {
			t.Fatalf("expected 0 update calls, got %d", calls.Load())
		}
	})
}
