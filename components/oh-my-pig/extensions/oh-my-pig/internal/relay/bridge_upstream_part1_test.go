package relay

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 1-to-1 port of upstream `packages/coding-agent/test/tools/browser-relay-bridge.test.ts`
// (part 1: lines 183–1994 — target discovery, tab grouping, child targets, Runtime sessions,
// command deadlines, attachment release, multiple extension instances).
// Every subtest is named `upstream: browser-relay-bridge.test.ts:<line> "<title>"`.

func TestUpstreamBridge_01_DiscoveryAndGrouping(t *testing.T) {
	anon := InstanceCode("anon", "")
	pageID := func(tabID int) string { return fmt.Sprintf("PAGE%s.%d", anon, tabID) }

	t.Run(`upstream: browser-relay-bridge.test.ts:184 "enumerates current eligible pages without attaching or claiming tabs"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{Group: &DefaultGroupConfig})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{
			mkTab(1),
			mkTab(2, func(s *TabSnapshot) { s.URL = "about:blank" }),
			mkTab(3, func(s *TabSnapshot) { s.URL = "chrome://extensions/" }),
		}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		id1 := sendCDP(bridge, conn, "", "Target.getTargets", nil)
		rep1 := cdp.waitReply(t, id1)
		infos1 := rep1["result"].(map[string]any)["targetInfos"].([]any)
		if len(infos1) != 2 {
			t.Fatalf("got %d targets, want 2: %+v", len(infos1), infos1)
		}
		sendExt(bridge, ext, map[string]any{"t": "tabRemoved", "tabId": 2})
		id2 := sendCDP(bridge, conn, "", "Target.getTargets", nil)
		rep2 := cdp.waitReply(t, id2)
		infos2 := rep2["result"].(map[string]any)["targetInfos"].([]any)
		if len(infos2) != 1 || infos2[0].(map[string]any)["targetId"] != pageID(1) {
			t.Fatalf("got %+v, want only %s", infos2, pageID(1))
		}
		if len(ext.rpcs("attach")) != 0 || len(ext.rpcs("group")) != 0 {
			t.Fatalf("discovery must neither attach nor group")
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:213 "groups nothing on hello or tab lifecycle events — only claimed tabs join the omp group"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{Group: &DefaultGroupConfig})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1), mkTab(2), mkTab(3, func(s *TabSnapshot) { s.URL = "about:blank" })}, nil)
		sendExt(bridge, ext, map[string]any{"t": "tabCreated", "tab": mkTab(9)})
		if len(ext.rpcs("group")) != 0 {
			t.Fatalf("expected 0 group RPCs, got %d", len(ext.rpcs("group")))
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:221 "never groups from command traffic: a discovery scan sending page commands to every tab is not driving"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{Group: &DefaultGroupConfig})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1), mkTab(2)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		for _, tid := range []int{1, 2} {
			sid := attachPageTab(t, bridge, ext, cdp, conn, tid, "anon")
			sendCDP(bridge, conn, sid, "Page.enable", nil)
			sendCDP(bridge, conn, sid, "Page.getFrameTree", nil)
		}
		time.Sleep(15 * time.Millisecond)
		if len(ext.rpcs("group")) != 0 {
			t.Fatalf("expected 0 group RPCs from command traffic, got %d", len(ext.rpcs("group")))
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:238 "groups exactly the tab a client claims"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{Group: &DefaultGroupConfig})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1), mkTab(2)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, pageID(1))
		_ = attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		grps := ext.waitPending(t, "group")
		if len(grps) != 1 || !reflect.DeepEqual(grps[0].TabIDs, []int{1}) || grps[0].Title != "omp" || grps[0].Color != "cyan" {
			t.Fatalf("unexpected group RPC: %+v", grps)
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:251 "never groups pinned tabs or tabs in a user group, even when claimed"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{Group: &DefaultGroupConfig})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{
			mkTab(3, func(s *TabSnapshot) { s.Pinned = true }),
			mkTab(4, func(s *TabSnapshot) { s.GroupID = 77 }),
		}, nil)
		c1 := newFakeCDPSock()
		_ = attachPageTab(t, bridge, ext, c1, bridge.CDPConnected(c1, pageID(3)), 3, "anon")
		c2 := newFakeCDPSock()
		_ = attachPageTab(t, bridge, ext, c2, bridge.CDPConnected(c2, pageID(4)), 4, "anon")
		time.Sleep(15 * time.Millisecond)
		if len(ext.rpcs("group")) != 0 {
			t.Fatalf("expected 0 group RPCs for pinned/user-grouped tabs")
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:260 "does not issue group RPCs when grouping is disabled"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{Group: nil})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		c := newFakeCDPSock()
		_ = attachPageTab(t, bridge, ext, c, bridge.CDPConnected(c, pageID(1)), 1, "anon")
		time.Sleep(15 * time.Millisecond)
		if len(ext.rpcs("group")) != 0 {
			t.Fatalf("expected 0 group RPCs when grouping disabled")
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:269 "closes a tab Target.createTarget made for a client that disconnected before it opened"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, nil, nil)
		conn := bridge.CDPConnected(newFakeCDPSock(), "")
		sendCDP(bridge, conn, "", "Target.createTarget", map[string]any{"url": "https://example.com/"})
		p := ext.waitPending(t, "createTab")
		bridge.CDPClosed(conn)
		ackOne(bridge, ext, p[0].ID, true, map[string]any{"tab": mkTab(9)}, "")
		rm := ext.waitPending(t, "removeTab")
		if len(rm) != 1 || rm[0].TabID != 9 {
			t.Fatalf("expected removeTab 9, got %+v", rm)
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:284 "auto-claims a tab created through Target.createTarget"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{Group: &DefaultGroupConfig})
		ext := newFakeExtSock()
		connectExt(bridge, ext, nil, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		cID := sendCDP(bridge, conn, "", "Target.createTarget", map[string]any{"url": "https://example.com/"})
		p := ext.waitPending(t, "createTab")
		ackOne(bridge, ext, p[0].ID, true, map[string]any{"tab": mkTab(9)}, "")
		cdp.waitReply(t, cID)
		grps := ext.waitPending(t, "group")
		if len(grps) != 1 || !reflect.DeepEqual(grps[0].TabIDs, []int{9}) {
			t.Fatalf("expected group [9], got %+v", grps)
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:301 "fails Target.createTarget with Chrome's refusal and closes the tab when the new tab cannot be attached"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, nil, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		cdp.waitReply(t, sendCDP(bridge, conn, "", "Target.setAutoAttach", map[string]any{}))
		cID := sendCDP(bridge, conn, "", "Target.createTarget", map[string]any{"url": "about:blank"})
		ackOne(bridge, ext, ext.waitPending(t, "createTab")[0].ID, true, map[string]any{"tab": mkTab(9, func(s *TabSnapshot) { s.URL = "about:blank" })}, "")
		ackOne(bridge, ext, ext.waitPending(t, "attach")[0].ID, false, nil, "Host access is restricted by policy.")
		rm := ext.waitPending(t, "removeTab")
		if rm[0].TabID != 9 {
			t.Fatalf("expected removeTab 9, got %+v", rm)
		}
		ackOne(bridge, ext, rm[0].ID, true, map[string]any{}, "")
		rep := cdp.waitReply(t, cID)
		if !strings.Contains(fmt.Sprint(rep["error"]), "Host access is restricted by policy.") {
			t.Fatalf("expected policy refusal error, got %+v", rep)
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:328 "creates a Target.createTarget background tab without selecting it"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, nil, nil)
		conn := bridge.CDPConnected(newFakeCDPSock(), "")
		sendCDP(bridge, conn, "", "Target.createTarget", map[string]any{"url": "https://example.com/", "background": true})
		p := ext.waitPending(t, "createTab")
		if p[0].URL != "https://example.com/" || p[0].Active == nil || *p[0].Active {
			t.Fatalf("expected active:false, got %+v", p[0])
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:345 "leaves createTab selection to Chrome for Target.createTarget $params"`, func(t *testing.T) {
		for _, params := range []map[string]any{
			{"url": "https://example.com/"},
			{"url": "https://example.com/", "background": false},
		} {
			bridge := NewRelayBridge(BridgeOptions{})
			ext := newFakeExtSock()
			connectExt(bridge, ext, nil, nil)
			conn := bridge.CDPConnected(newFakeCDPSock(), "")
			sendCDP(bridge, conn, "", "Target.createTarget", params)
			p := ext.waitPending(t, "createTab")
			if p[0].URL != "https://example.com/" || p[0].Active != nil {
				t.Fatalf("params=%v: expected active omitted, got %+v", params, p[0])
			}
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:359 "never re-groups a tab the user pulled out of the omp group"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{Group: &DefaultGroupConfig})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		c := newFakeCDPSock()
		_ = attachPageTab(t, bridge, ext, c, bridge.CDPConnected(c, pageID(1)), 1, "anon")
		ackOne(bridge, ext, ext.waitPending(t, "group")[0].ID, true, map[string]any{"grouped": map[string]any{"1": 42}}, "")
		time.Sleep(15 * time.Millisecond)
		sendExt(bridge, ext, map[string]any{"t": "tabUpdated", "tab": mkTab(1, func(s *TabSnapshot) { s.GroupID = 42 })})
		sendExt(bridge, ext, map[string]any{"t": "tabUpdated", "tab": mkTab(1, func(s *TabSnapshot) { s.GroupID = -1 })})
		sendExt(bridge, ext, map[string]any{"t": "tabUpdated", "tab": mkTab(1, func(s *TabSnapshot) { s.GroupID = -1; s.URL = "https://example.com/other" })})
		time.Sleep(15 * time.Millisecond)
		if len(ext.rpcs("group")) != 1 {
			t.Fatalf("expected 1 group RPC after user opt-out, got %d", len(ext.rpcs("group")))
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:379 "ungroups when the claiming client disconnects, even while another connection still holds sessions"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{Group: &DefaultGroupConfig})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		reg := newFakeCDPSock()
		_ = attachPageTab(t, bridge, ext, reg, bridge.CDPConnected(reg, ""), 1, "anon")
		w := newFakeCDPSock()
		wConn := bridge.CDPConnected(w, pageID(1))
		_ = attachPageTab(t, bridge, ext, w, wConn, 1, "anon")
		ackOne(bridge, ext, ext.waitPending(t, "group")[0].ID, true, map[string]any{"grouped": map[string]any{"1": 42}}, "")
		time.Sleep(15 * time.Millisecond)
		bridge.CDPClosed(wConn)
		ung := ext.waitPending(t, "ungroup")
		if len(ung) != 1 || !reflect.DeepEqual(ung[0].TabIDs, []int{1}) {
			t.Fatalf("expected ungroup [1], got %+v", ung)
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:398 "ungroups a tab whose claiming client closed while its group RPC was in flight"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{Group: &DefaultGroupConfig})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		wConn := bridge.CDPConnected(newFakeCDPSock(), pageID(1))
		p := ext.waitPending(t, "group")
		bridge.CDPClosed(wConn)
		if len(ext.rpcs("ungroup")) != 0 {
			t.Fatalf("ungroup sent before group settled")
		}
		ackOne(bridge, ext, p[0].ID, true, map[string]any{"grouped": map[string]any{"1": 42}}, "")
		ung := ext.waitPending(t, "ungroup")
		if len(ung) != 1 || !reflect.DeepEqual(ung[0].TabIDs, []int{1}) {
			t.Fatalf("expected ungroup [1], got %+v", ung)
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:412 "never overlaps group RPCs: a tab claimed mid-flight waits for the pending group"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{Group: &DefaultGroupConfig})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1), mkTab(2)}, nil)
		c1 := newFakeCDPSock()
		_ = attachPageTab(t, bridge, ext, c1, bridge.CDPConnected(c1, pageID(1)), 1, "anon")
		p1 := ext.waitPending(t, "group")
		c2 := newFakeCDPSock()
		_ = attachPageTab(t, bridge, ext, c2, bridge.CDPConnected(c2, pageID(2)), 2, "anon")
		time.Sleep(15 * time.Millisecond)
		if len(ext.rpcs("group")) != 1 {
			t.Fatalf("expected second group queued, got %d", len(ext.rpcs("group")))
		}
		ackOne(bridge, ext, p1[0].ID, true, map[string]any{"grouped": map[string]any{"1": 42}}, "")
		p2 := ext.waitPending(t, "group")
		if !reflect.DeepEqual(p2[0].TabIDs, []int{2}) {
			t.Fatalf("expected second group [2], got %+v", p2[0].TabIDs)
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:430 "regroups claimed tabs after an extension reconnect instead of treating the dissolve as user opt-out"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{Group: &DefaultGroupConfig})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		c := newFakeCDPSock()
		_ = attachPageTab(t, bridge, ext, c, bridge.CDPConnected(c, pageID(1)), 1, "anon")
		ackOne(bridge, ext, ext.waitPending(t, "group")[0].ID, true, map[string]any{"grouped": map[string]any{"1": 42}}, "")
		time.Sleep(15 * time.Millisecond)
		bridge.ExtClosed(ext)
		ext2 := newFakeExtSock()
		connectExt(bridge, ext2, []TabSnapshot{mkTab(1, func(s *TabSnapshot) { s.GroupID = -1 })}, nil)
		grps := ext2.waitPending(t, "group")
		if len(grps) != 1 || !reflect.DeepEqual(grps[0].TabIDs, []int{1}) {
			t.Fatalf("expected regroup [1] on reconnect, got %+v", grps)
		}
	})
}

func TestUpstreamBridge_02_RuntimeDeadlinesDetachAndMultiInstance(t *testing.T) {
	anon := InstanceCode("anon", "")
	pageID := func(tabID int, inst string) string {
		if inst == "" {
			inst = "anon"
		}
		return fmt.Sprintf("PAGE%s.%d", InstanceCode(inst, ""), tabID)
	}

	t.Run(`upstream: browser-relay-bridge.test.ts:980 "virtualizes Runtime enable state for each pseudo-session"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		c1 := newFakeCDPSock()
		conn1 := bridge.CDPConnected(c1, "")
		s1 := attachPageTab(t, bridge, ext, c1, conn1, 1, "anon")
		en1 := sendCDP(bridge, conn1, s1, "Runtime.enable", nil)
		pDis := ext.waitPending(t, "send")
		if pDis[0].Method != "Runtime.disable" {
			t.Fatalf("want Runtime.disable first, got %s", pDis[0].Method)
		}
		ackOne(bridge, ext, pDis[0].ID, true, map[string]any{}, "")
		pEn := ext.waitPending(t, "send")
		if pEn[0].Method != "Runtime.enable" {
			t.Fatalf("want Runtime.enable second, got %s", pEn[0].Method)
		}
		ctx17 := map[string]any{"context": map[string]any{"id": 17, "uniqueId": "context-17"}}
		sendExt(bridge, ext, map[string]any{"t": "cdpEvent", "tabId": 1, "method": "Runtime.executionContextCreated", "params": ctx17})
		ackOne(bridge, ext, pEn[0].ID, true, map[string]any{}, "")
		c1.waitReply(t, en1)

		c2 := newFakeCDPSock()
		conn2 := bridge.CDPConnected(c2, "")
		s2 := attachPageTab(t, bridge, ext, c2, conn2, 1, "anon")
		sendsBefore := len(ext.rpcs("send"))
		en2 := sendCDP(bridge, conn2, s2, "Runtime.enable", nil)
		c2.waitReply(t, en2)
		if len(ext.rpcs("send")) != sendsBefore {
			t.Fatalf("second Runtime.enable must replay without root RPC")
		}
		if len(c2.messagesOf("Runtime.executionContextCreated")) != 1 {
			t.Fatalf("second session did not get replayed context 17")
		}
		cdpDis := sendCDP(bridge, conn2, s2, "Runtime.disable", nil)
		c2.waitReply(t, cdpDis)
		ctx18 := map[string]any{"context": map[string]any{"id": 18, "uniqueId": "context-18"}}
		sendExt(bridge, ext, map[string]any{"t": "cdpEvent", "tabId": 1, "method": "Runtime.executionContextCreated", "params": ctx18})
		if len(c1.messagesOf("Runtime.executionContextCreated")) != 2 {
			t.Fatalf("first session should receive both contexts")
		}
		if len(c2.messagesOf("Runtime.executionContextCreated")) != 1 {
			t.Fatalf("disabled second session must not receive context 18")
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:1192 "fails a command Chrome answers at once after 20 s, naming the command and the tab"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{RPCTimeout: 30 * time.Millisecond, PageBoundRPCTimeout: 150 * time.Millisecond})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		sid := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		id1 := sendCDP(bridge, conn, sid, "Input.dispatchMouseEvent", map[string]any{"type": "mouseMoved", "x": 1, "y": 1})
		id2 := sendCDP(bridge, conn, sid, "Runtime.evaluate", map[string]any{"expression": "1", "awaitPromise": false})
		r1 := cdp.waitReply(t, id1)
		r2 := cdp.waitReply(t, id2)
		if !strings.Contains(fmt.Sprint(r1["error"]), "Input.dispatchMouseEvent on tab 1: Chrome sent no answer within 30ms") {
			t.Fatalf("unexpected r1: %+v", r1)
		}
		if !strings.Contains(fmt.Sprint(r2["error"]), "Runtime.evaluate on tab 1: Chrome sent no answer within 30ms") {
			t.Fatalf("unexpected r2: %+v", r2)
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:1389 "reuses an existing debugger attachment for a second connection"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{Group: &DefaultGroupConfig})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		reg := newFakeCDPSock()
		_ = attachPageTab(t, bridge, ext, reg, bridge.CDPConnected(reg, ""), 1, "anon")
		worker := newFakeCDPSock()
		wConn := bridge.CDPConnected(worker, "")
		attID := sendCDP(bridge, wConn, "", "Target.attachToTarget", map[string]any{"targetId": "PAGE" + anon + ".1"})
		worker.waitReply(t, attID)
		if len(ext.rpcs("attach")) != 1 || worker.sessionFor(attID) == "" {
			t.Fatalf("expected 1 attach reused, got %d attaches, sid=%q", len(ext.rpcs("attach")), worker.sessionFor(attID))
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:1411 "keeps the attachment while another connection still holds a session on the tab"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{Group: &DefaultGroupConfig})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		reg := newFakeCDPSock()
		_ = attachPageTab(t, bridge, ext, reg, bridge.CDPConnected(reg, ""), 1, "anon")
		w := newFakeCDPSock()
		wConn := bridge.CDPConnected(w, "")
		sid := attachPageTab(t, bridge, ext, w, wConn, 1, "anon")
		w.waitReply(t, sendCDP(bridge, wConn, "", "Target.detachFromTarget", map[string]any{"sessionId": sid}))
		time.Sleep(15 * time.Millisecond)
		if len(ext.rpcs("detach")) != 0 {
			t.Fatalf("expected 0 detaches while registry holds session")
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:1671 "fails a pipelined duplicate Runtime.enable when the root enable fails"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		sid := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		e1 := sendCDP(bridge, conn, sid, "Runtime.enable", nil)
		p := ext.waitPending(t, "send")
		e2 := sendCDP(bridge, conn, sid, "Runtime.enable", nil)
		ackOne(bridge, ext, p[0].ID, false, nil, "root failed")
		r1 := cdp.waitReply(t, e1)
		r2 := cdp.waitReply(t, e2)
		if r1["error"] == nil || r2["error"] == nil {
			t.Fatalf("both enables must fail: r1=%+v r2=%+v", r1, r2)
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:1748 "scopes the hello GC to the reconnecting instance"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		chrome := newFakeExtSock()
		edge := newFakeExtSock()
		connectExtInstance(bridge, chrome, "chrome", "", []TabSnapshot{mkTab(1, func(s *TabSnapshot) { s.Title = "Chrome tab" })}, nil)
		connectExtInstance(bridge, edge, "edge", "", []TabSnapshot{mkTab(1, func(s *TabSnapshot) { s.Title = "Edge tab" })}, nil)
		bridge.ExtClosed(edge)
		connectExtInstance(bridge, newFakeExtSock(), "edge", "", []TabSnapshot{}, nil)
		targets := bridge.ListTargets()
		if len(targets) != 1 || targets[0]["title"] != "Chrome tab" {
			t.Fatalf("expected only Chrome tab, got %+v", targets)
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:1763 "replaces the socket, resets runtime state, and keeps the registry when the same instance reconnects"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		first := newFakeExtSock()
		connectExtInstance(bridge, first, "chrome", "", []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		sid := attachPageTab(t, bridge, first, cdp, conn, 1, "chrome")
		en := sendCDP(bridge, conn, sid, "Runtime.enable", nil)
		ackOne(bridge, first, first.waitPending(t, "send")[0].ID, true, map[string]any{}, "")
		ackOne(bridge, first, first.waitPending(t, "send")[0].ID, true, map[string]any{}, "")
		cdp.waitReply(t, en)

		second := newFakeExtSock()
		connectExtInstance(bridge, second, "chrome", "", []TabSnapshot{mkTab(1)}, nil)
		if !first.isClosed() || len(bridge.ListTargets()) != 1 {
			t.Fatalf("expected old socket closed and 1 target")
		}
		sid2 := attachPageTab(t, bridge, second, cdp, conn, 1, "chrome")
		en2 := sendCDP(bridge, conn, sid2, "Runtime.enable", nil)
		p := second.waitPending(t, "send")
		if p[0].Method != "Runtime.disable" {
			t.Fatalf("expected fresh Runtime.disable after socket replacement, got %s", p[0].Method)
		}
		ackOne(bridge, second, p[0].ID, true, map[string]any{}, "")
		ackOne(bridge, second, second.waitPending(t, "send")[0].ID, true, map[string]any{}, "")
		cdp.waitReply(t, en2)
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:1799 "routes rpcs to the browser instance that owns the tab"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		chrome := newFakeExtSock()
		edge := newFakeExtSock()
		connectExtInstance(bridge, chrome, "chrome", "", []TabSnapshot{mkTab(1)}, nil)
		connectExtInstance(bridge, edge, "edge", "", []TabSnapshot{mkTab(1)}, nil)
		cCdp, eCdp := newFakeCDPSock(), newFakeCDPSock()
		cConn, eConn := bridge.CDPConnected(cCdp, ""), bridge.CDPConnected(eCdp, "")
		cSid := attachPageTab(t, bridge, chrome, cCdp, cConn, 1, "chrome")
		eSid := attachPageTab(t, bridge, edge, eCdp, eConn, 1, "edge")
		sendCDP(bridge, cConn, cSid, "Runtime.enable", nil)
		sendCDP(bridge, eConn, eSid, "Runtime.enable", nil)
		pC := chrome.waitPending(t, "send")
		pE := edge.waitPending(t, "send")
		if pC[0].TabID != 1 || pE[0].TabID != 1 {
			t.Fatalf("wrong tab routing")
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:1826 "opens new tabs in the browser showing the first selected tab, not the one that reconnected last"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		chrome := newFakeExtSock()
		connectExtInstance(bridge, chrome, "chrome", "", []TabSnapshot{mkTab(1, func(s *TabSnapshot) { s.Active = true })}, nil)
		connectExtInstance(bridge, newFakeExtSock(), "edge", "", []TabSnapshot{mkTab(1, func(s *TabSnapshot) { s.Active = true })}, nil)
		edge := newFakeExtSock()
		connectExtInstance(bridge, edge, "edge", "", []TabSnapshot{mkTab(1, func(s *TabSnapshot) { s.Active = true })}, nil)
		conn := bridge.CDPConnected(newFakeCDPSock(), "")
		sendCDP(bridge, conn, "", "Target.createTarget", map[string]any{})
		chrome.waitPending(t, "createTab")
		if len(edge.rpcs("createTab")) != 0 {
			t.Fatalf("expected chrome (first selected) to receive createTab, not reconnected edge")
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:1840 "opens new tabs in the browser of a tab the client already drives"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		chrome := newFakeExtSock()
		edge := newFakeExtSock()
		connectExtInstance(bridge, edge, "edge", "", []TabSnapshot{mkTab(1)}, nil)
		connectExtInstance(bridge, chrome, "chrome", "", []TabSnapshot{mkTab(1, func(s *TabSnapshot) { s.Active = true })}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, pageID(1, "edge"))
		_ = attachPageTab(t, bridge, edge, cdp, conn, 1, "edge")
		sendCDP(bridge, conn, "", "Target.createTarget", map[string]any{})
		edge.waitPending(t, "createTab")
		if len(chrome.rpcs("createTab")) != 0 {
			t.Fatalf("expected edge (affine claimed tab) to receive createTab")
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:1855 "opens a new tab in the browser of nearTargetId after the user closed that tab"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		chrome := newFakeExtSock()
		edge := newFakeExtSock()
		connectExtInstance(bridge, edge, "edge", "", []TabSnapshot{mkTab(1)}, nil)
		connectExtInstance(bridge, chrome, "chrome", "", []TabSnapshot{mkTab(1, func(s *TabSnapshot) { s.Active = true })}, nil)
		sendExt(bridge, edge, map[string]any{"t": "tabRemoved", "tabId": 1})
		conn := bridge.CDPConnected(newFakeCDPSock(), "")
		sendCDP(bridge, conn, "", "Target.createTarget", map[string]any{"url": "about:blank", "nearTargetId": pageID(1, "edge")})
		edge.waitPending(t, "createTab")
		if len(chrome.rpcs("createTab")) != 0 {
			t.Fatalf("expected edge to receive createTab via nearTargetId")
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:1875 "waits for the browser of nearTargetId to reconnect instead of opening the tab in another browser"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{AffineReconnectWait: 300 * time.Millisecond})
		edge := newFakeExtSock()
		chrome := newFakeExtSock()
		connectExtInstance(bridge, edge, "edge", "", []TabSnapshot{mkTab(1)}, nil)
		connectExtInstance(bridge, chrome, "chrome", "", []TabSnapshot{mkTab(1, func(s *TabSnapshot) { s.Active = true })}, nil)
		bridge.ExtClosed(edge)
		conn := bridge.CDPConnected(newFakeCDPSock(), "")
		sendCDP(bridge, conn, "", "Target.createTarget", map[string]any{"url": "about:blank", "nearTargetId": pageID(1, "edge")})
		time.Sleep(25 * time.Millisecond)
		if len(chrome.rpcs("createTab")) != 0 {
			t.Fatalf("must wait for offline affine browser")
		}
		back := newFakeExtSock()
		connectExtInstance(bridge, back, "edge", "", []TabSnapshot{mkTab(1)}, nil)
		back.waitPending(t, "createTab")
		if len(chrome.rpcs("createTab")) != 0 {
			t.Fatalf("must not fall back to chrome when edge reconnected")
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:1900 "opens the tab in another browser once the browser of nearTargetId stays offline past its reconnect window"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{AffineReconnectWait: 40 * time.Millisecond})
		edge := newFakeExtSock()
		chrome := newFakeExtSock()
		connectExtInstance(bridge, edge, "edge", "", []TabSnapshot{mkTab(1)}, nil)
		connectExtInstance(bridge, chrome, "chrome", "", []TabSnapshot{mkTab(1, func(s *TabSnapshot) { s.Active = true })}, nil)
		bridge.ExtClosed(edge)
		conn := bridge.CDPConnected(newFakeCDPSock(), "")
		sendCDP(bridge, conn, "", "Target.createTarget", map[string]any{"url": "about:blank", "nearTargetId": pageID(1, "edge")})
		chrome.waitPending(t, "createTab")
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:1927 "retires a browser's targets once it restarted, so an old target id never names a tab that reuses its id"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		connectExtInstance(bridge, newFakeExtSock(), "chrome", "run-1", []TabSnapshot{mkTab(7)}, nil)
		oldTarget := bridge.ListTargets()[0]["id"]
		connectExtInstance(bridge, newFakeExtSock(), "chrome", "run-1", []TabSnapshot{mkTab(7)}, nil)
		if bridge.ListTargets()[0]["id"] != oldTarget {
			t.Fatalf("same browserSession must keep targetId")
		}
		restarted := newFakeExtSock()
		connectExtInstance(bridge, restarted, "chrome", "run-2", []TabSnapshot{mkTab(7, func(s *TabSnapshot) { s.URL = "https://mail.example/" })}, nil)
		if bridge.ListTargets()[0]["id"] == oldTarget {
			t.Fatalf("new browserSession must mint new targetId")
		}
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		cID := sendCDP(bridge, conn, "", "Target.closeTarget", map[string]any{"targetId": oldTarget})
		if cdp.waitReply(t, cID)["error"] == nil || len(restarted.rpcs("removeTab")) != 0 {
			t.Fatalf("retired targetId must fail closeTarget without touching restarted browser")
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:1952 "closes a tab Chrome creates after the createTab RPC timed out"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{RPCTimeout: 30 * time.Millisecond, LateRPCKeep: 200 * time.Millisecond})
		ext := newFakeExtSock()
		connectExt(bridge, ext, nil, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		cID := sendCDP(bridge, conn, "", "Target.createTarget", map[string]any{})
		p := ext.waitPending(t, "createTab")
		if cdp.waitReply(t, cID)["error"] == nil {
			t.Fatal("expected timeout error")
		}
		ackOne(bridge, ext, p[0].ID, true, map[string]any{"tab": mkTab(9)}, "")
		rm := ext.waitPending(t, "removeTab")
		if rm[0].TabID != 9 {
			t.Fatalf("expected late removeTab 9, got %+v", rm)
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:1975 "forgets a timed-out createTab once its late window passes, so a wedged extension cannot grow the relay"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{RPCTimeout: 25 * time.Millisecond, LateRPCKeep: 40 * time.Millisecond})
		ext := newFakeExtSock()
		connectExt(bridge, ext, nil, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		cID := sendCDP(bridge, conn, "", "Target.createTarget", map[string]any{})
		p := ext.waitPending(t, "createTab")
		cdp.waitReply(t, cID)
		time.Sleep(70 * time.Millisecond)
		ackOne(bridge, ext, p[0].ID, true, map[string]any{"tab": mkTab(9)}, "")
		time.Sleep(20 * time.Millisecond)
		if len(ext.rpcs("removeTab")) != 0 {
			t.Fatalf("late window passed; must not close tab")
		}
	})
}

// Suppress unused imports if refactored.
var _ = sync.Mutex{}
var _ atomic.Int32
