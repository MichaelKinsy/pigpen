package relay

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 1-to-1 port of upstream `packages/coding-agent/test/tools/browser-relay-bridge.test.ts`
// (part 2: lines 2013–3821 — closeOnHello, last-hello fallback, page-session lifecycle,
// auto-attach gating, front app, file chooser & focus emulation switches, foreign frames,
// download paths, hidden frames, page overlay, busy extension).
// Every subtest is named `upstream: browser-relay-bridge.test.ts:<line> "<title>"`.

func TestUpstreamBridge_03_CloseQueueFallbackAutoAttach(t *testing.T) {
	anon := InstanceCode("anon", "")
	pageID := func(tabID int, inst string) string {
		if inst == "" {
			inst = "anon"
		}
		return fmt.Sprintf("PAGE%s.%d", InstanceCode(inst, ""), tabID)
	}

	t.Run(`upstream: browser-relay-bridge.test.ts:2013 "does not report a tab closed while its browser's extension is offline, and closes it once that browser reconnects"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		chrome := newFakeExtSock()
		connectExtInstance(bridge, chrome, "chrome", "", []TabSnapshot{mkTab(7), mkTab(8)}, nil)
		bridge.ExtClosed(chrome)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		cID := sendCDP(bridge, conn, "", "Target.closeTarget", map[string]any{"targetId": pageID(7, "chrome")})
		if cdp.waitReply(t, cID)["error"] == nil {
			t.Fatal("expected error while browser offline")
		}
		back := newFakeExtSock()
		connectExtInstance(bridge, back, "chrome", "", []TabSnapshot{mkTab(7), mkTab(8)}, nil)
		rm := back.waitPending(t, "removeTab")
		if len(rm) != 1 || rm[0].TabID != 7 {
			t.Fatalf("expected removeTab 7 on reconnect, got %+v", rm)
		}
		// Drops again before closing: next reconnect closes it too.
		bridge.ExtClosed(back)
		time.Sleep(15 * time.Millisecond)
		again := newFakeExtSock()
		connectExtInstance(bridge, again, "chrome", "", []TabSnapshot{mkTab(7), mkTab(8)}, nil)
		rm2 := again.waitPending(t, "removeTab")
		if len(rm2) != 1 || rm2[0].TabID != 7 {
			t.Fatalf("expected removeTab 7 on second reconnect, got %+v", rm2)
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:2030 "closes a tab whose close its browser dropped mid-call once that browser reconnects"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		chrome := newFakeExtSock()
		connectExtInstance(bridge, chrome, "chrome", "", []TabSnapshot{mkTab(7)}, nil)
		conn := bridge.CDPConnected(newFakeCDPSock(), "")
		sendCDP(bridge, conn, "", "Target.closeTarget", map[string]any{"targetId": pageID(7, "chrome")})
		chrome.waitPending(t, "removeTab")
		bridge.ExtClosed(chrome)
		time.Sleep(15 * time.Millisecond)
		back := newFakeExtSock()
		connectExtInstance(bridge, back, "chrome", "", []TabSnapshot{mkTab(7)}, nil)
		rm := back.waitPending(t, "removeTab")
		if len(rm) != 1 || rm[0].TabID != 7 {
			t.Fatalf("expected removeTab 7 on reconnect, got %+v", rm)
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:2052 "does not close a tab whose id now shows another page when its browser reconnects"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		chrome := newFakeExtSock()
		connectExtInstance(bridge, chrome, "chrome", "", []TabSnapshot{mkTab(7)}, nil)
		bridge.ExtClosed(chrome)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		cID := sendCDP(bridge, conn, "", "Target.closeTarget", map[string]any{"targetId": pageID(7, "chrome")})
		cdp.waitReply(t, cID)
		back := newFakeExtSock()
		connectExtInstance(bridge, back, "chrome", "", []TabSnapshot{mkTab(7, func(s *TabSnapshot) { s.URL = "https://mail.example/" })}, nil)
		time.Sleep(20 * time.Millisecond)
		if len(back.rpcs("removeTab")) != 0 {
			t.Fatalf("must not close tab 7 when URL changed")
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:2064 "reports a tab the relay no longer knows as closed"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		connectExtInstance(bridge, newFakeExtSock(), "chrome", "", []TabSnapshot{mkTab(7)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		cID := sendCDP(bridge, conn, "", "Target.closeTarget", map[string]any{"targetId": pageID(8, "chrome")})
		rep := cdp.waitReply(t, cID)
		if !strings.Contains(fmt.Sprint(rep["error"]), "No target with id") {
			t.Fatalf("expected No target with id error, got %+v", rep)
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:2072 "falls back to a live instance for browser-wide requests when the last hello goes offline"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		chrome := newFakeExtSock()
		edge := newFakeExtSock()
		connectExtInstance(bridge, chrome, "chrome", "", []TabSnapshot{mkTab(1, func(s *TabSnapshot) { s.Title = "Chrome tab" })}, map[string]any{"browserVersion": "Chrome/151.0.0.0"})
		connectExtInstance(bridge, edge, "edge", "", []TabSnapshot{mkTab(1, func(s *TabSnapshot) { s.Title = "Edge tab" })}, map[string]any{"browserVersion": "Edg/151.0.0.0"})
		bridge.ExtClosed(edge)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		cID := sendCDP(bridge, conn, "", "Target.createTarget", map[string]any{"url": "https://example.com/"})
		p := chrome.waitPending(t, "createTab")
		if len(edge.rpcs("createTab")) != 0 {
			t.Fatal("must not route to offline edge")
		}
		ackOne(bridge, chrome, p[0].ID, true, map[string]any{"tab": mkTab(9, func(s *TabSnapshot) { s.Title = "Created" })}, "")
		cdp.waitReply(t, cID)
		if bridge.VersionInfo("ws://relay")["Browser"] != "Chrome/151.0.0.0" {
			t.Fatalf("unexpected Browser: %v", bridge.VersionInfo("ws://relay")["Browser"])
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:2101 "hides tabs of an instance whose socket went offline"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		chrome := newFakeExtSock()
		edge := newFakeExtSock()
		connectExtInstance(bridge, chrome, "chrome", "", []TabSnapshot{mkTab(1, func(s *TabSnapshot) { s.Title = "Chrome tab" })}, nil)
		connectExtInstance(bridge, edge, "edge", "", []TabSnapshot{mkTab(1, func(s *TabSnapshot) { s.Title = "Edge tab" })}, nil)
		bridge.ExtClosed(edge)
		if len(bridge.ListTargets()) != 1 || bridge.ListTargets()[0]["title"] != "Chrome tab" {
			t.Fatalf("expected only Chrome tab while edge offline")
		}
		connectExtInstance(bridge, newFakeExtSock(), "edge", "", []TabSnapshot{mkTab(1, func(s *TabSnapshot) { s.Title = "Edge tab" })}, nil)
		if len(bridge.ListTargets()) != 2 {
			t.Fatalf("expected both tabs after edge reconnected")
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:2161 "fails a page session's activate or close of a target the relay does not know"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		sid := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		cases := [][2]any{
			{"Target.activateTarget", map[string]any{"targetId": "2E08A730A7A82AC514A041D202746564"}},
			{"Target.activateTarget", map[string]any{"targetId": pageID(9, "anon")}},
			{"Target.activateTarget", map[string]any{}},
			{"Target.closeTarget", map[string]any{"targetId": "2E08A730A7A82AC514A041D202746564"}},
		}
		for _, tc := range cases {
			id := sendCDP(bridge, conn, sid, tc[0].(string), tc[1].(map[string]any))
			rep := cdp.waitReply(t, id)
			if !strings.Contains(fmt.Sprint(rep["error"]), "No target with id") {
				t.Fatalf("case %v: expected No target with id, got %+v", tc, rep)
			}
		}
		if len(ext.rpcs("send")) != 0 || len(ext.rpcs("activateTab")) != 0 || len(ext.rpcs("removeTab")) != 0 {
			t.Fatal("unknown target must not forward to extension")
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:2240 "eagerly attaches live tabs and retracts discarded tabs"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1, func(s *TabSnapshot) { s.Discarded = true }), mkTab(2)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		cdp.waitReply(t, sendCDP(bridge, conn, "", "Target.setDiscoverTargets", map[string]any{"discover": true}))
		aaID := sendCDP(bridge, conn, "", "Target.setAutoAttach", map[string]any{"autoAttach": true, "flatten": true})
		p := ext.waitPending(t, "attach")
		if len(p) != 1 || p[0].TabID != 2 {
			t.Fatalf("expected attach only for live tab 2, got %+v", p)
		}
		ackOne(bridge, ext, p[0].ID, true, map[string]any{}, "")
		cdp.waitReply(t, aaID)
		if !containsString(cdp.attachedTargetIDs(), "TAB"+anon+".2") || containsString(cdp.attachedTargetIDs(), "TAB"+anon+".1") {
			t.Fatalf("unexpected attached: %v", cdp.attachedTargetIDs())
		}
		if !containsString(cdp.destroyedTargetIDs(), "PAGE"+anon+".1") {
			t.Fatalf("expected discarded tab 1 retracted")
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:2271 "announces revived tabs and retracts discarded tabs"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1, func(s *TabSnapshot) { s.Discarded = true }), mkTab(2)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		cdp.waitReply(t, sendCDP(bridge, conn, "", "Target.setDiscoverTargets", map[string]any{"discover": true}))
		aaID := sendCDP(bridge, conn, "", "Target.setAutoAttach", map[string]any{"autoAttach": true})
		ackOne(bridge, ext, ext.waitPending(t, "attach")[0].ID, true, map[string]any{}, "")
		cdp.waitReply(t, aaID)

		sendExt(bridge, ext, map[string]any{"t": "tabUpdated", "tab": mkTab(1, func(s *TabSnapshot) { s.Discarded = false })})
		ackOne(bridge, ext, ext.waitPending(t, "attach")[0].ID, true, map[string]any{}, "")
		waitCond(t, "tab 1 revived", func() bool { return containsString(cdp.attachedTargetIDs(), "TAB"+anon+".1") })

		sendExt(bridge, ext, map[string]any{"t": "tabUpdated", "tab": mkTab(2, func(s *TabSnapshot) { s.Discarded = true })})
		waitCond(t, "tab 2 retracted", func() bool { return containsString(cdp.destroyedTargetIDs(), "PAGE"+anon+".2") })
		pDet := ext.waitPending(t, "detach")
		if pDet[0].TabID != 2 {
			t.Fatalf("expected detach for tab 2, got %+v", pDet)
		}
		ackOne(bridge, ext, pDet[0].ID, true, map[string]any{}, "")
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:2303 "announces and attaches a tab again once its URL changes after Chrome refused its reattach"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1), mkTab(2)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		cdp.waitReply(t, sendCDP(bridge, conn, "", "Target.setDiscoverTargets", map[string]any{"discover": true}))
		aaID := sendCDP(bridge, conn, "", "Target.setAutoAttach", map[string]any{"autoAttach": true})
		waitCond(t, "2 attaches", func() bool { return len(ext.pending("attach")) == 2 })
		ackOp(bridge, ext, "attach", map[string]any{})
		cdp.waitReply(t, aaID)

		sendExt(bridge, ext, map[string]any{"t": "detached", "tabId": 1, "reason": "target_closed"})
		ackOne(bridge, ext, ext.waitPending(t, "attach")[0].ID, false, nil, "Cannot access a chrome-extension:// URL of different extension")
		waitCond(t, "tab 1 hidden", func() bool { return len(bridge.ListTargets()) == 1 })

		sendExt(bridge, ext, map[string]any{"t": "tabUpdated", "tab": mkTab(1, func(s *TabSnapshot) { s.URL = "https://example.com/next" })})
		ackOne(bridge, ext, ext.waitPending(t, "attach")[0].ID, true, map[string]any{}, "")
		waitCond(t, "tab 1 reattached twice", func() bool {
			n := 0
			for _, id := range cdp.attachedTargetIDs() {
				if id == "TAB"+anon+".1" {
					n++
				}
			}
			return n == 2
		})
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:2343 "attaches and groups a tab Chrome refused over another extension's frame once a worker claims it"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{Group: &DefaultGroupConfig})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1), mkTab(2)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		aaID := sendCDP(bridge, conn, "", "Target.setAutoAttach", map[string]any{"autoAttach": true})
		waitCond(t, "2 attaches", func() bool { return len(ext.pending("attach")) == 2 })
		ackOp(bridge, ext, "attach", map[string]any{})
		cdp.waitReply(t, aaID)

		sendExt(bridge, ext, map[string]any{"t": "detached", "tabId": 1, "reason": "target_closed"})
		pRef := ext.waitPending(t, "attach")
		if pRef[0].ClearForeignFrames == nil || *pRef[0].ClearForeignFrames {
			t.Fatalf("unclaimed tab must have clearForeignFrames=false")
		}
		ackOne(bridge, ext, pRef[0].ID, false, nil, "Cannot access a chrome-extension:// URL of different extension")
		waitCond(t, "tab 1 hidden", func() bool { return len(bridge.ListTargets()) == 1 })

		bridge.CDPConnected(newFakeCDPSock(), pageID(1, "anon"))
		pClaim := ext.waitPending(t, "attach")
		if pClaim[0].ClearForeignFrames == nil || !*pClaim[0].ClearForeignFrames {
			t.Fatalf("claimed tab must have clearForeignFrames=true")
		}
		grps := ext.waitPending(t, "group")
		if !reflect.DeepEqual(grps[0].TabIDs, []int{1}) {
			t.Fatalf("expected group [1], got %+v", grps)
		}
		ackOne(bridge, ext, pClaim[0].ID, true, map[string]any{}, "")
		waitCond(t, "tab 1 visible", func() bool { return len(bridge.ListTargets()) == 2 })
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:2373 "does not reattach an initially discovered tab discarded during attach"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		cdp.waitReply(t, sendCDP(bridge, conn, "", "Target.setDiscoverTargets", map[string]any{"discover": true}))
		aaID := sendCDP(bridge, conn, "", "Target.setAutoAttach", map[string]any{"autoAttach": true})
		p := ext.waitPending(t, "attach")
		sendExt(bridge, ext, map[string]any{"t": "tabUpdated", "tab": mkTab(1, func(s *TabSnapshot) { s.Discarded = true })})
		ackOne(bridge, ext, p[0].ID, true, map[string]any{}, "")
		cdp.waitReply(t, aaID)
		if containsString(cdp.attachedTargetIDs(), "TAB"+anon+".1") || !containsString(cdp.destroyedTargetIDs(), "PAGE"+anon+".1") {
			t.Fatalf("discarded tab should be retracted, not attached")
		}
		ext.waitPending(t, "detach")
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:2396 "does not reattach a newly announced tab discarded during attach"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, nil, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		cdp.waitReply(t, sendCDP(bridge, conn, "", "Target.setDiscoverTargets", map[string]any{"discover": true}))
		cdp.waitReply(t, sendCDP(bridge, conn, "", "Target.setAutoAttach", map[string]any{"autoAttach": true}))
		sendExt(bridge, ext, map[string]any{"t": "tabCreated", "tab": mkTab(1)})
		p := ext.waitPending(t, "attach")
		sendExt(bridge, ext, map[string]any{"t": "tabUpdated", "tab": mkTab(1, func(s *TabSnapshot) { s.Discarded = true })})
		ackOne(bridge, ext, p[0].ID, true, map[string]any{}, "")
		ext.waitPending(t, "detach")
		if containsString(cdp.attachedTargetIDs(), "TAB"+anon+".1") {
			t.Fatal("discarded newly announced tab must not attach")
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:2505 "reannounces a held tab on revival without attaching it while discarded"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		cdp.waitReply(t, sendCDP(bridge, conn, "", "Target.setDiscoverTargets", map[string]any{"discover": true}))
		aaID := sendCDP(bridge, conn, "", "Target.setAutoAttach", map[string]any{"autoAttach": true})
		ackOne(bridge, ext, ext.waitPending(t, "attach")[0].ID, true, map[string]any{}, "")
		cdp.waitReply(t, aaID)

		rep := newFakeExtSock()
		connectExt(bridge, rep, []TabSnapshot{mkTab(1, func(s *TabSnapshot) { s.Discarded = true })}, nil)
		time.Sleep(15 * time.Millisecond)
		if len(rep.rpcs("attach")) != 0 {
			t.Fatal("must not attach discarded tab on replacement hello")
		}
		sendExt(bridge, rep, map[string]any{"t": "tabUpdated", "tab": mkTab(1, func(s *TabSnapshot) { s.Discarded = false })})
		ackOne(bridge, rep, rep.waitPending(t, "attach")[0].ID, true, map[string]any{}, "")
		waitCond(t, "revived tab re-attached", func() bool {
			n := 0
			for _, id := range cdp.attachedTargetIDs() {
				if id == "TAB"+anon+".1" {
					n++
				}
			}
			return n == 2
		})
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:2538 "clears cancelled attach state when a discarded extension reconnects"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext1 := newFakeExtSock()
		connectExt(bridge, ext1, []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		cdp.waitReply(t, sendCDP(bridge, conn, "", "Target.setDiscoverTargets", map[string]any{"discover": true}))
		sendCDP(bridge, conn, "", "Target.setAutoAttach", map[string]any{"autoAttach": true})
		ext1.waitPending(t, "attach")
		sendExt(bridge, ext1, map[string]any{"t": "tabUpdated", "tab": mkTab(1, func(s *TabSnapshot) { s.Discarded = true })})

		rep := newFakeExtSock()
		connectExt(bridge, rep, []TabSnapshot{mkTab(1, func(s *TabSnapshot) { s.Discarded = true })}, nil)
		time.Sleep(15 * time.Millisecond)
		if len(rep.rpcs("attach")) != 0 {
			t.Fatal("must not attach discarded tab on reconnect")
		}
		sendExt(bridge, rep, map[string]any{"t": "tabUpdated", "tab": mkTab(1)})
		p1 := rep.waitPending(t, "attach")
		sendExt(bridge, rep, map[string]any{"t": "tabUpdated", "tab": mkTab(1, func(s *TabSnapshot) { s.Discarded = true })})
		ackOne(bridge, rep, p1[0].ID, false, nil, "discarded tab again")
		time.Sleep(15 * time.Millisecond)
		sendExt(bridge, rep, map[string]any{"t": "tabUpdated", "tab": mkTab(1)})
		ackOne(bridge, rep, rep.waitPending(t, "attach")[0].ID, true, map[string]any{}, "")
		waitCond(t, "tab 1 attached", func() bool { return containsString(cdp.attachedTargetIDs(), "TAB"+anon+".1") })
	})
}

func TestUpstreamBridge_04_FrontAppSwitchesForeignOverlayBusy(t *testing.T) {
	anon := InstanceCode("anon", "")
	pageID := func(tabID int, inst string) string {
		if inst == "" {
			inst = "anon"
		}
		return fmt.Sprintf("PAGE%s.%d", InstanceCode(inst, ""), tabID)
	}

	const chooser = "Page.setInterceptFileChooserDialog"
	const focus = "Emulation.setFocusEmulationEnabled"

	setSw := func(t *testing.T, bridge *RelayBridge, ext *fakeExtSock, cdp *fakeCDPSock, conn int, sid, method string, en bool) {
		t.Helper()
		id := sendCDP(bridge, conn, sid, method, map[string]any{"enabled": en})
		ackOne(bridge, ext, ext.waitPending(t, "send")[0].ID, true, map[string]any{}, "")
		cdp.waitReply(t, id)
	}

	// ---- Front app (lines 2656-2839) ----
	t.Run(`upstream: browser-relay-bridge.test.ts:2656 "holds no app in front for a window when $case"`, func(t *testing.T) {
		for _, tc := range []struct {
			name     string
			inputTab int
			advance  time.Duration
		}{
			{"no input", 0, 0},
			{"over a second ago", 1, 1001 * time.Millisecond},
			{"other tab", 2, 0},
		} {
			now := time.Now()
			var clockMu sync.Mutex
			var holds atomic.Int32
			bridge := NewRelayBridge(BridgeOptions{Clock: func() time.Time {
				clockMu.Lock()
				defer clockMu.Unlock()
				return now
			}})
			bridge.KeepFrontAppWith(func() (FrontApp, error) {
				return fakeFrontApp{fn: func(time.Duration) (int, error) {
					holds.Add(1)
					return 1, nil
				}}, nil
			})
			ext := newFakeExtSock()
			connectExt(bridge, ext, []TabSnapshot{mkTab(1), mkTab(2)}, nil)
			cdp := newFakeCDPSock()
			conn := bridge.CDPConnected(cdp, "")
			s1 := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
			s2 := attachPageTab(t, bridge, ext, cdp, conn, 2, "anon")
			if tc.inputTab != 0 {
				sid := s1
				if tc.inputTab == 2 {
					sid = s2
				}
				id := sendCDP(bridge, conn, sid, "Input.dispatchMouseEvent", map[string]any{"type": "mousePressed", "x": 5, "y": 5})
				ackOne(bridge, ext, ext.waitPending(t, "send")[0].ID, true, map[string]any{}, "")
				cdp.waitReply(t, id)
			}
			clockMu.Lock()
			now = now.Add(tc.advance)
			clockMu.Unlock()
			sendExt(bridge, ext, map[string]any{"t": "cdpEvent", "tabId": 1, "method": "Page.windowOpen", "params": map[string]any{}})
			time.Sleep(15 * time.Millisecond)
			if holds.Load() != 0 {
				t.Fatalf("%s: expected 0 holds, got %d", tc.name, holds.Load())
			}
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:2682 "reads the front app only on omp's inputs that can open a window, not on moves, key-ups or inserted text"`, func(t *testing.T) {
		var reads, holds atomic.Int32
		bridge := NewRelayBridge(BridgeOptions{})
		bridge.KeepFrontAppWith(func() (FrontApp, error) {
			reads.Add(1)
			return fakeFrontApp{fn: func(time.Duration) (int, error) {
				holds.Add(1)
				return 1, nil
			}}, nil
		})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		sid := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		for _, inp := range [][2]any{
			{"Input.dispatchMouseEvent", map[string]any{"type": "mouseMoved", "x": 5, "y": 5}},
			{"Input.dispatchMouseEvent", map[string]any{"type": "mouseWheel", "x": 5, "y": 5}},
			{"Input.dispatchKeyEvent", map[string]any{"type": "keyUp", "key": "a"}},
			{"Input.insertText", map[string]any{"text": "a"}},
		} {
			id := sendCDP(bridge, conn, sid, inp[0].(string), inp[1].(map[string]any))
			ackOne(bridge, ext, ext.waitPending(t, "send")[0].ID, true, map[string]any{}, "")
			cdp.waitReply(t, id)
		}
		sendExt(bridge, ext, map[string]any{"t": "cdpEvent", "tabId": 1, "method": "Page.windowOpen", "params": map[string]any{}})
		time.Sleep(15 * time.Millisecond)
		if reads.Load() != 0 || holds.Load() != 0 {
			t.Fatalf("unexpected read/hold on non-activating input")
		}
		id := sendCDP(bridge, conn, sid, "Input.dispatchKeyEvent", map[string]any{"type": "keyDown", "key": "Enter"})
		ackOne(bridge, ext, ext.waitPending(t, "send")[0].ID, true, map[string]any{}, "")
		cdp.waitReply(t, id)
		sendExt(bridge, ext, map[string]any{"t": "cdpEvent", "tabId": 1, "method": "Page.windowOpen", "params": map[string]any{}})
		waitCond(t, "hold after keyDown", func() bool { return holds.Load() == 1 })
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:2710 "still forwards omp's input when reading the front app throws, and stops reading it"`, func(t *testing.T) {
		var reads atomic.Int32
		bridge := NewRelayBridge(BridgeOptions{})
		bridge.KeepFrontAppWith(func() (FrontApp, error) {
			reads.Add(1)
			return nil, errors.New("missing native addon")
		})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		sid := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		for _, typ := range []string{"mousePressed", "mouseReleased"} {
			id := sendCDP(bridge, conn, sid, "Input.dispatchMouseEvent", map[string]any{"type": typ, "x": 5, "y": 5})
			ackOne(bridge, ext, ext.waitPending(t, "send")[0].ID, true, map[string]any{}, "")
			if cdp.waitReply(t, id)["error"] != nil {
				t.Fatalf("input %s failed", typ)
			}
		}
		if reads.Load() != 1 {
			t.Fatalf("expected 1 read attempt before disabling, got %d", reads.Load())
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:2731 "reads no front app while a hold runs, so a window opened after it never holds the browser in front"`, func(t *testing.T) {
		var reads, holds atomic.Int32
		unblock := make(chan struct{})
		bridge := NewRelayBridge(BridgeOptions{})
		bridge.KeepFrontAppWith(func() (FrontApp, error) {
			reads.Add(1)
			return fakeFrontApp{fn: func(time.Duration) (int, error) {
				holds.Add(1)
				<-unblock
				return 1, nil
			}}, nil
		})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		sid := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		id := sendCDP(bridge, conn, sid, "Input.dispatchMouseEvent", map[string]any{"type": "mousePressed", "x": 5, "y": 5})
		ackOne(bridge, ext, ext.waitPending(t, "send")[0].ID, true, map[string]any{}, "")
		cdp.waitReply(t, id)
		sendExt(bridge, ext, map[string]any{"t": "cdpEvent", "tabId": 1, "method": "Page.windowOpen", "params": map[string]any{}})
		waitCond(t, "first hold", func() bool { return holds.Load() == 1 })
		// Click while hold runs -> must not read front app.
		id2 := sendCDP(bridge, conn, sid, "Input.dispatchMouseEvent", map[string]any{"type": "mousePressed", "x": 5, "y": 5})
		ackOne(bridge, ext, ext.waitPending(t, "send")[0].ID, true, map[string]any{}, "")
		cdp.waitReply(t, id2)
		close(unblock)
		time.Sleep(15 * time.Millisecond)
		sendExt(bridge, ext, map[string]any{"t": "cdpEvent", "tabId": 1, "method": "Page.windowOpen", "params": map[string]any{}})
		time.Sleep(15 * time.Millisecond)
		if reads.Load() != 1 || holds.Load() != 1 {
			t.Fatalf("expected reads=1 holds=1, got reads=%d holds=%d", reads.Load(), holds.Load())
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:2768 "ends a hold at its deadline even while the native hold has not settled"`, func(t *testing.T) {
		now := time.Now()
		var clockMu sync.Mutex
		var holds atomic.Int32
		never := make(chan struct{})
		defer close(never)
		bridge := NewRelayBridge(BridgeOptions{Clock: func() time.Time {
			clockMu.Lock()
			defer clockMu.Unlock()
			return now
		}})
		bridge.KeepFrontAppWith(func() (FrontApp, error) {
			return fakeFrontApp{fn: func(time.Duration) (int, error) {
				holds.Add(1)
				<-never
				return 0, nil
			}}, nil
		})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		sid := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		id := sendCDP(bridge, conn, sid, "Input.dispatchMouseEvent", map[string]any{"type": "mousePressed", "x": 5, "y": 5})
		ackOne(bridge, ext, ext.waitPending(t, "send")[0].ID, true, map[string]any{}, "")
		cdp.waitReply(t, id)
		sendExt(bridge, ext, map[string]any{"t": "cdpEvent", "tabId": 1, "method": "Page.windowOpen", "params": map[string]any{}})
		waitCond(t, "first hold", func() bool { return holds.Load() == 1 })
		clockMu.Lock()
		now = now.Add(keepFrontDuration)
		clockMu.Unlock()
		id2 := sendCDP(bridge, conn, sid, "Input.dispatchMouseEvent", map[string]any{"type": "mousePressed", "x": 5, "y": 5})
		ackOne(bridge, ext, ext.waitPending(t, "send")[0].ID, true, map[string]any{}, "")
		cdp.waitReply(t, id2)
		sendExt(bridge, ext, map[string]any{"t": "cdpEvent", "tabId": 1, "method": "Page.windowOpen", "params": map[string]any{}})
		waitCond(t, "second hold after deadline", func() bool { return holds.Load() == 2 })
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:2803 "keeps a rejected native hold guarded until its original deadline"`, func(t *testing.T) {
		now := time.Now()
		var clockMu sync.Mutex
		var reads, holds atomic.Int32
		bridge := NewRelayBridge(BridgeOptions{Clock: func() time.Time {
			clockMu.Lock()
			defer clockMu.Unlock()
			return now
		}})
		bridge.KeepFrontAppWith(func() (FrontApp, error) {
			reads.Add(1)
			return fakeFrontApp{fn: func(time.Duration) (int, error) {
				holds.Add(1)
				return 0, errors.New("desktop control is busy")
			}}, nil
		})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		sid := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		id := sendCDP(bridge, conn, sid, "Input.dispatchMouseEvent", map[string]any{"type": "mousePressed", "x": 5, "y": 5})
		ackOne(bridge, ext, ext.waitPending(t, "send")[0].ID, true, map[string]any{}, "")
		cdp.waitReply(t, id)
		sendExt(bridge, ext, map[string]any{"t": "cdpEvent", "tabId": 1, "method": "Page.windowOpen", "params": map[string]any{}})
		waitCond(t, "first rejected hold", func() bool { return holds.Load() == 1 })
		// Still within deadline -> guarded.
		id2 := sendCDP(bridge, conn, sid, "Input.dispatchMouseEvent", map[string]any{"type": "mousePressed", "x": 5, "y": 5})
		ackOne(bridge, ext, ext.waitPending(t, "send")[0].ID, true, map[string]any{}, "")
		cdp.waitReply(t, id2)
		sendExt(bridge, ext, map[string]any{"t": "cdpEvent", "tabId": 1, "method": "Page.windowOpen", "params": map[string]any{}})
		time.Sleep(15 * time.Millisecond)
		if reads.Load() != 1 || holds.Load() != 1 {
			t.Fatalf("expected guarded until deadline, reads=%d holds=%d", reads.Load(), holds.Load())
		}
		clockMu.Lock()
		now = now.Add(keepFrontDuration)
		clockMu.Unlock()
		id3 := sendCDP(bridge, conn, sid, "Input.dispatchMouseEvent", map[string]any{"type": "mousePressed", "x": 5, "y": 5})
		ackOne(bridge, ext, ext.waitPending(t, "send")[0].ID, true, map[string]any{}, "")
		cdp.waitReply(t, id3)
		sendExt(bridge, ext, map[string]any{"t": "cdpEvent", "tabId": 1, "method": "Page.windowOpen", "params": map[string]any{}})
		waitCond(t, "second hold after deadline", func() bool { return holds.Load() == 2 })
	})

	// ---- File chooser & focus switches (lines 2890-3149) ----
	t.Run(`upstream: browser-relay-bridge.test.ts:2890 "keeps interception on while another session on the tab still waits for a chooser"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		w := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		u := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		setSw(t, bridge, ext, cdp, conn, w, chooser, true)
		setSw(t, bridge, ext, cdp, conn, u, chooser, true)
		setSw(t, bridge, ext, cdp, conn, u, chooser, false)
		setSw(t, bridge, ext, cdp, conn, w, chooser, false)
		want := []any{true, true, true, false}
		if got := switchSendValues(ext, chooser); !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:2906 "switches interception off when its last owner detaches or disconnects without disabling it"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		holder := newFakeCDPSock()
		_ = attachPageTab(t, bridge, ext, holder, bridge.CDPConnected(holder, ""), 1, "anon")
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		det := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		setSw(t, bridge, ext, cdp, conn, det, chooser, true)
		cdp.waitReply(t, sendCDP(bridge, conn, "", "Target.detachFromTarget", map[string]any{"sessionId": det}))
		ackOne(bridge, ext, ext.waitPending(t, "send")[0].ID, true, map[string]any{}, "")
		killed := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		setSw(t, bridge, ext, cdp, conn, killed, chooser, true)
		bridge.CDPClosed(conn)
		ackOne(bridge, ext, ext.waitPending(t, "send")[0].ID, true, map[string]any{}, "")
		want := []any{true, false, true, false}
		if got := switchSendValues(ext, chooser); !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:2953 "keeps a later enable when an earlier overlapping enable on the session fails"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		holder := newFakeCDPSock()
		_ = attachPageTab(t, bridge, ext, holder, bridge.CDPConnected(holder, ""), 1, "anon")
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		sid := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		sendCDP(bridge, conn, sid, chooser, map[string]any{"enabled": true})
		p1 := ext.waitPending(t, "send")
		later := sendCDP(bridge, conn, sid, chooser, map[string]any{"enabled": true})
		ackOne(bridge, ext, p1[0].ID, false, nil, "boom")
		p2 := ext.waitPending(t, "send")
		ackOne(bridge, ext, p2[0].ID, true, map[string]any{}, "")
		if cdp.waitReply(t, later)["error"] != nil {
			t.Fatal("later enable should succeed")
		}
		cdp.waitReply(t, sendCDP(bridge, conn, "", "Target.detachFromTarget", map[string]any{"sessionId": sid}))
		pOff := ext.waitPending(t, "send")
		if pOff[0].Params["enabled"] != false {
			t.Fatalf("expected enabled:false on detach, got %+v", pOff[0])
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:2979 "keeps interception on for an owner whose disable failed while another owner's disable succeeded"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		s1 := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		s2 := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		setSw(t, bridge, ext, cdp, conn, s1, chooser, true)
		setSw(t, bridge, ext, cdp, conn, s2, chooser, true)
		failed := sendCDP(bridge, conn, s1, chooser, map[string]any{"enabled": false})
		p1 := ext.waitPending(t, "send")
		okID := sendCDP(bridge, conn, s2, chooser, map[string]any{"enabled": false})
		ackOne(bridge, ext, p1[0].ID, false, nil, "rpc failed")
		cdp.waitReply(t, failed)
		p2 := ext.waitPending(t, "send")
		if p2[0].Params["enabled"] != true {
			t.Fatalf("s1 rollback must keep tab enabled:true, got %+v", p2[0])
		}
		ackOne(bridge, ext, p2[0].ID, true, map[string]any{}, "")
		cdp.waitReply(t, okID)
		setSw(t, bridge, ext, cdp, conn, s1, chooser, false)
		vals := switchSendValues(ext, chooser)
		if vals[len(vals)-1] != false {
			t.Fatalf("expected final enabled:false, got %v", vals)
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:3004 "finishes focus reset before detaching a released tab and re-adopting it"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		sid := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		setSw(t, bridge, ext, cdp, conn, sid, focus, true)
		bridge.CDPClosed(conn)
		pFocusOff := ext.waitPending(t, "send")
		if len(ext.rpcs("detach")) != 0 {
			t.Fatal("detach must wait for focus reset")
		}
		next := newFakeCDPSock()
		nextConn := bridge.CDPConnected(next, "")
		reID := sendCDP(bridge, nextConn, "", "Target.attachToTarget", map[string]any{"targetId": "PAGE" + anon + ".1", "flatten": true})
		time.Sleep(15 * time.Millisecond)
		if len(ext.pending("attach")) != 0 {
			t.Fatal("reattach must wait for detach")
		}
		ackOne(bridge, ext, pFocusOff[0].ID, true, map[string]any{}, "")
		pDet := ext.waitPending(t, "detach")
		ackOne(bridge, ext, pDet[0].ID, true, map[string]any{}, "")
		pAtt := ext.waitPending(t, "attach")
		ackOne(bridge, ext, pAtt[0].ID, true, map[string]any{}, "")
		if next.waitReply(t, reID)["error"] != nil {
			t.Fatal("reattach failed")
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:3048 "keeps a tab focus-emulated while any session asks, and ends it when the last client goes away"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		reg := newFakeCDPSock()
		_ = attachPageTab(t, bridge, ext, reg, bridge.CDPConnected(reg, ""), 1, "anon")
		c1, c2 := newFakeCDPSock(), newFakeCDPSock()
		conn1, conn2 := bridge.CDPConnected(c1, ""), bridge.CDPConnected(c2, "")
		s1 := attachPageTab(t, bridge, ext, c1, conn1, 1, "anon")
		s2 := attachPageTab(t, bridge, ext, c2, conn2, 1, "anon")
		setSw(t, bridge, ext, c1, conn1, s1, focus, true)
		setSw(t, bridge, ext, c2, conn2, s2, focus, true)
		bridge.CDPClosed(conn1)
		time.Sleep(15 * time.Millisecond)
		if !reflect.DeepEqual(switchSendValues(ext, focus), []any{true, true}) {
			t.Fatalf("focus must stay on while conn2 remains: %v", switchSendValues(ext, focus))
		}
		bridge.CDPClosed(conn2)
		ackOne(bridge, ext, ext.waitPending(t, "send")[0].ID, true, map[string]any{}, "")
		if !reflect.DeepEqual(switchSendValues(ext, focus), []any{true, true, false}) || len(ext.rpcs("detach")) != 0 {
			t.Fatalf("expected [true true false] and no detach")
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:3075 "restores a tab's switches after a service-worker restart drops the attachment"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		sid := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		setSw(t, bridge, ext, cdp, conn, sid, focus, true)
		restarted := newFakeExtSock()
		connectExt(bridge, restarted, []TabSnapshot{mkTab(1)}, nil)
		ackOne(bridge, restarted, restarted.waitPending(t, "attach")[0].ID, true, map[string]any{}, "")
		p := restarted.waitPending(t, "send")
		if p[0].Method != focus || p[0].Params["enabled"] != true {
			t.Fatalf("expected focus restored after reattach, got %+v", p[0])
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:3092 "brings a surviving attachment's switches to the live sessions' wishes when the extension reconnects"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		reg := newFakeCDPSock()
		_ = attachPageTab(t, bridge, ext, reg, bridge.CDPConnected(reg, ""), 1, "anon")
		kCdp, lCdp := newFakeCDPSock(), newFakeCDPSock()
		kConn, lConn := bridge.CDPConnected(kCdp, ""), bridge.CDPConnected(lCdp, "")
		kSid := attachPageTab(t, bridge, ext, kCdp, kConn, 1, "anon")
		lSid := attachPageTab(t, bridge, ext, lCdp, lConn, 1, "anon")
		setSw(t, bridge, ext, kCdp, kConn, kSid, focus, true)
		setSw(t, bridge, ext, kCdp, kConn, kSid, chooser, true)
		setSw(t, bridge, ext, lCdp, lConn, lSid, focus, true)
		bridge.ExtClosed(ext)
		bridge.CDPClosed(kConn)
		time.Sleep(15 * time.Millisecond)
		back := newFakeExtSock()
		connectExt(bridge, back, []TabSnapshot{mkTab(1)}, []int{1})
		p := back.waitPending(t, "send")
		if p[0].Method != chooser || p[0].Params["enabled"] != false {
			t.Fatalf("expected unsettled chooser=false, got %+v", p[0])
		}
		if len(switchSendValues(back, focus)) != 0 {
			t.Fatalf("focus still wanted; should not be resent")
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:3132 "settles a switch whose reply was lost with the extension socket"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		sid := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		sendCDP(bridge, conn, sid, focus, map[string]any{"enabled": true})
		ext.waitPending(t, "send")
		bridge.ExtClosed(ext)
		time.Sleep(20 * time.Millisecond)
		back := newFakeExtSock()
		connectExt(bridge, back, []TabSnapshot{mkTab(1)}, []int{1})
		p := back.waitPending(t, "send")
		if p[0].Method != focus || p[0].Params["enabled"] != false {
			t.Fatalf("expected lost switch settled to false, got %+v", p[0])
		}
	})

	// ---- Foreign frames, downloads, hidden frames, overlay, busy (lines 3152-3821) ----
	t.Run(`upstream: browser-relay-bridge.test.ts:3152 "lets the extension empty another extension's frames only in a tab a client claimed"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1), mkTab(2)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, pageID(1, "anon"))
		s1 := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		s2 := attachPageTab(t, bridge, ext, cdp, conn, 2, "anon")
		sendCDP(bridge, conn, s1, "Page.getFrameTree", nil)
		sendCDP(bridge, conn, s2, "Page.getFrameTree", nil)
		waitCond(t, "2 sends", func() bool { return len(ext.rpcs("send")) == 2 })
		for _, rpc := range append(ext.rpcs("attach"), ext.rpcs("send")...) {
			want := rpc.TabID == 1
			if rpc.ClearForeignFrames == nil || *rpc.ClearForeignFrames != want {
				t.Fatalf("tab %d op %s: clearForeignFrames=%v, want %v", rpc.TabID, rpc.Op, rpc.ClearForeignFrames, want)
			}
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:3174 "takes the claim an older omp worker sends on its page session"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		sid := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		cID := sendCDP(bridge, conn, sid, "OMP.claimTarget", nil)
		if cdp.waitReply(t, cID)["error"] != nil {
			t.Fatal("OMP.claimTarget failed")
		}
		sendCDP(bridge, conn, sid, "Page.getFrameTree", nil)
		p := ext.waitPending(t, "send")
		if p[0].Method != "Page.getFrameTree" || p[0].ClearForeignFrames == nil || !*p[0].ClearForeignFrames {
			t.Fatalf("expected clearForeignFrames=true after OMP.claimTarget, got %+v", p[0])
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:3226 "looks up where Chrome saved a completed download"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExtInstance(bridge, ext, "anon", "", []TabSnapshot{mkTab(1)}, map[string]any{"downloadsProtocol": 1})
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		sid := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		id := sendCDP(bridge, conn, sid, "OMP.downloadPath", map[string]any{"guid": "guid-1"})
		p := ext.waitPending(t, "findDownload")
		ackOne(bridge, ext, p[0].ID, true, map[string]any{"filename": "/tmp/r (1).csv", "state": "complete"}, "")
		if rep := cdp.waitReply(t, id); rep["result"].(map[string]any)["filePath"] != "/tmp/r (1).csv" {
			t.Fatalf("unexpected reply: %+v", rep)
		}
		// in_progress and null must fail.
		for _, res := range []any{map[string]any{"filename": "/tmp/r.csv", "state": "in_progress"}, nil} {
			id2 := sendCDP(bridge, conn, sid, "OMP.downloadPath", map[string]any{"guid": "guid-2"})
			p2 := ext.waitPending(t, "findDownload")
			ext.markAcked(p2[0].ID)
			sendExt(bridge, ext, map[string]any{"t": "rpcResult", "id": p2[0].ID, "ok": true, "result": res})
			if rep := cdp.waitReply(t, id2); !strings.Contains(fmt.Sprint(rep["error"]), "Chrome has no completed download") {
				t.Fatalf("expected incomplete download error for %v, got %+v", res, rep)
			}
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:3249 "tells an extension installed before download tracking to update, without asking it"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		sid := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		id := sendCDP(bridge, conn, sid, "OMP.downloadPath", map[string]any{"guid": "g1"})
		rep := cdp.waitReply(t, id)
		if len(ext.rpcs("findDownload")) != 0 || !strings.Contains(fmt.Sprint(rep["error"]), "browser-relay install") {
			t.Fatalf("unexpected reply: %+v", rep)
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:3262 "holds what the extension hid for the worker driving the tab, and past a Chrome detach for the next one"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		menu := map[string]any{"extensionId": strings.Repeat("a", 32), "path": "inline/menu.html", "title": "Vault"}
		card := map[string]any{"extensionId": strings.Repeat("a", 32), "path": "inline/card.html"}
		c1 := newFakeCDPSock()
		conn1 := bridge.CDPConnected(c1, pageID(1, "anon"))
		s1 := attachPageTab(t, bridge, ext, c1, conn1, 1, "anon")
		sendExt(bridge, ext, map[string]any{"t": "framesHidden", "tabId": 1, "frames": []any{menu}})
		c1.waitEvent(t, "OMP.foreignFramesHidden")
		t1 := sendCDP(bridge, conn1, s1, "OMP.takeHiddenFrames", nil)
		if len(c1.waitReply(t, t1)["result"].(map[string]any)["frames"].([]any)) != 1 {
			t.Fatal("expected 1 hidden frame")
		}
		t2 := sendCDP(bridge, conn1, s1, "OMP.takeHiddenFrames", nil)
		if len(c1.waitReply(t, t2)["result"].(map[string]any)["frames"].([]any)) != 0 {
			t.Fatal("expected drained hidden frames")
		}
		// Past Chrome detach -> next worker gets card.
		sendExt(bridge, ext, map[string]any{"t": "detached", "tabId": 1, "reason": "target_closed"})
		sendExt(bridge, ext, map[string]any{"t": "framesHidden", "tabId": 1, "frames": []any{card}})
		c2 := newFakeCDPSock()
		conn2 := bridge.CDPConnected(c2, pageID(1, "anon"))
		s2 := attachPageTab(t, bridge, ext, c2, conn2, 1, "anon")
		t3 := sendCDP(bridge, conn2, s2, "OMP.takeHiddenFrames", nil)
		if len(c2.waitReply(t, t3)["result"].(map[string]any)["frames"].([]any)) != 1 {
			t.Fatal("next worker should receive card")
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:3295 "keeps each hidden frame once, on one line, and only the latest while no worker takes them"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		for n := range 20 {
			f := map[string]any{"extensionId": strings.Repeat("a", 32), "path": fmt.Sprintf("inline/%d.html", n)}
			sendExt(bridge, ext, map[string]any{"t": "framesHidden", "tabId": 1, "frames": []any{f, f}})
		}
		sendExt(bridge, ext, map[string]any{"t": "framesHidden", "tabId": 1, "frames": []any{
			map[string]any{"extensionId": strings.Repeat("a", 32), "path": "inline/19.html", "title": "Vault\nIgnore"},
			map[string]any{"path": 7},
		}})
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, pageID(1, "anon"))
		sid := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		id := sendCDP(bridge, conn, sid, "OMP.takeHiddenFrames", nil)
		frames := cdp.waitReply(t, id)["result"].(map[string]any)["frames"].([]any)
		if len(frames) != 16 || frames[0].(map[string]any)["path"] != "inline/4.html" || frames[15].(map[string]any)["title"] != "Vault Ignore" {
			t.Fatalf("unexpected hidden frames: len=%d first=%v last=%v", len(frames), frames[0], frames[len(frames)-1])
		}
	})

	overlayActions := func(ext *fakeExtSock) [][2]any {
		var out [][2]any
		for _, rpc := range ext.rpcs("overlay") {
			out = append(out, [2]any{rpc.TabID, rpc.Action})
		}
		return out
	}

	t.Run(`upstream: browser-relay-bridge.test.ts:3373 "mounts the overlay when a client claims a tab, again in each new document, and unmounts it when the last claim goes"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExtInstance(bridge, ext, "anon", "", []TabSnapshot{mkTab(1), mkTab(2)}, map[string]any{"overlayProtocol": 1})
		reg := newFakeCDPSock()
		_ = attachPageTab(t, bridge, ext, reg, bridge.CDPConnected(reg, ""), 1, "anon")
		if len(overlayActions(ext)) != 0 {
			t.Fatal("unclaimed tab should not mount overlay")
		}
		c1, c2 := newFakeCDPSock(), newFakeCDPSock()
		conn1 := bridge.CDPConnected(c1, pageID(1, "anon"))
		conn2 := bridge.CDPConnected(c2, pageID(1, "anon"))
		ackOne(bridge, ext, ext.waitPending(t, "overlay")[0].ID, true, map[string]any{}, "")
		// Child frame nav ignored; top-level nav remounts.
		sendExt(bridge, ext, map[string]any{"t": "cdpEvent", "tabId": 1, "method": "Page.frameNavigated", "params": map[string]any{"frame": map[string]any{"id": "c", "parentId": "m"}}})
		if len(overlayActions(ext)) != 1 {
			t.Fatal("child frame navigation must not remount overlay")
		}
		sendExt(bridge, ext, map[string]any{"t": "cdpEvent", "tabId": 1, "method": "Page.frameNavigated", "params": map[string]any{"frame": map[string]any{"id": "m"}}})
		ackOne(bridge, ext, ext.waitPending(t, "overlay")[0].ID, true, map[string]any{}, "")
		bridge.CDPClosed(conn1)
		if len(overlayActions(ext)) != 2 {
			t.Fatal("must keep overlay while second claim holds")
		}
		bridge.CDPClosed(conn2)
		pUnm := ext.waitPending(t, "overlay")
		if pUnm[0].Action != OverlayUnmount {
			t.Fatalf("expected unmount, got %+v", pUnm[0])
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:3421 "takes the overlay down when Chrome ends the attachment and mounts it again once the tab is attached again"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExtInstance(bridge, ext, "anon", "", []TabSnapshot{mkTab(1)}, map[string]any{"overlayProtocol": 1})
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, pageID(1, "anon"))
		_ = attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		ackOne(bridge, ext, ext.waitPending(t, "overlay")[0].ID, true, map[string]any{}, "")
		sendExt(bridge, ext, map[string]any{"t": "detached", "tabId": 1, "reason": "target_closed"})
		pUnm := ext.waitPending(t, "overlay")
		if pUnm[0].Action != OverlayUnmount {
			t.Fatalf("expected unmount on detach, got %+v", pUnm[0])
		}
		ackOne(bridge, ext, pUnm[0].ID, true, map[string]any{}, "")
		_ = attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		pMnt := ext.waitPending(t, "overlay")
		if pMnt[0].Action != OverlayMount {
			t.Fatalf("expected remount on reattach, got %+v", pMnt[0])
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:3445 "draws only root input Chrome acknowledged from the client that claims the tab"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExtInstance(bridge, ext, "anon", "", []TabSnapshot{mkTab(1)}, map[string]any{"overlayProtocol": 1})
		reg := newFakeCDPSock()
		regConn := bridge.CDPConnected(reg, "")
		regSid := attachPageTab(t, bridge, ext, reg, regConn, 1, "anon")
		drv := newFakeCDPSock()
		drvConn := bridge.CDPConnected(drv, pageID(1, "anon"))
		drvSid := attachPageTab(t, bridge, ext, drv, drvConn, 1, "anon")
		ackOne(bridge, ext, ext.waitPending(t, "overlay")[0].ID, true, map[string]any{}, "")

		countInputs := func() int {
			ext.mu.Lock()
			defer ext.mu.Unlock()
			n := 0
			for _, m := range ext.messages {
				if m.T == "input" {
					n++
				}
			}
			return n
		}
		// Unclaiming registry input ignored.
		id := sendCDP(bridge, regConn, regSid, "Input.dispatchMouseEvent", map[string]any{"type": "mouseMoved", "x": 5, "y": 5})
		ackOne(bridge, ext, ext.waitPending(t, "send")[0].ID, true, map[string]any{}, "")
		reg.waitReply(t, id)
		if countInputs() != 0 {
			t.Fatal("unclaiming input must not draw")
		}
		// Refused driver input ignored.
		id = sendCDP(bridge, drvConn, drvSid, "Input.dispatchMouseEvent", map[string]any{"type": "mousePressed", "x": 10, "y": 20, "button": "right", "clickCount": 2})
		ackOne(bridge, ext, ext.waitPending(t, "send")[0].ID, false, nil, "refused")
		drv.waitReply(t, id)
		if countInputs() != 0 {
			t.Fatal("refused input must not draw")
		}
		// Acknowledged driver inputs draw; cancelled release point (-7919) ignored; insertText never leaks text.
		for _, tc := range []struct {
			method string
			params map[string]any
			draws  bool
		}{
			{"Input.dispatchMouseEvent", map[string]any{"type": "mouseMoved", "x": 10, "y": 20}, true},
			{"Input.dispatchMouseEvent", map[string]any{"type": "mousePressed", "x": 10, "y": 20, "button": "right", "clickCount": 2}, true},
			{"Input.dispatchMouseEvent", map[string]any{"type": "mouseReleased", "x": 10, "y": 20, "button": "right"}, true},
			{"Input.dispatchMouseEvent", map[string]any{"type": "mouseMoved", "x": -7919, "y": -7919}, false},
			{"Input.dispatchMouseEvent", map[string]any{"type": "mouseWheel", "x": 3, "y": 4}, true},
			{"Input.dispatchKeyEvent", map[string]any{"type": "keyDown", "key": "a"}, true},
			{"Input.insertText", map[string]any{"text": "secret"}, true},
		} {
			before := countInputs()
			cid := sendCDP(bridge, drvConn, drvSid, tc.method, tc.params)
			ackOne(bridge, ext, ext.waitPending(t, "send")[0].ID, true, map[string]any{}, "")
			drv.waitReply(t, cid)
			want := before
			if tc.draws {
				want++
				waitCond(t, "overlay input", func() bool { return countInputs() == want })
			} else if countInputs() != want {
				t.Fatalf("%s %v: got %d inputs, want %d", tc.method, tc.params, countInputs(), want)
			}
		}
		ext.mu.Lock()
		for _, m := range ext.messages {
			if m.T == "input" && strings.Contains(fmt.Sprint(m), "secret") {
				ext.mu.Unlock()
				t.Fatal("typed secret leaked into overlay input")
			}
		}
		ext.mu.Unlock()
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:3508 "draws in the browser of the claimed tab when another browser has a tab with the same id"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		chrome, edge := newFakeExtSock(), newFakeExtSock()
		connectExtInstance(bridge, chrome, "chrome", "", []TabSnapshot{mkTab(1)}, map[string]any{"overlayProtocol": 1})
		connectExtInstance(bridge, edge, "edge", "", []TabSnapshot{mkTab(1)}, map[string]any{"overlayProtocol": 1})
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, pageID(1, "chrome"))
		sid := attachPageTab(t, bridge, chrome, cdp, conn, 1, "chrome")
		ackOne(bridge, chrome, chrome.waitPending(t, "overlay")[0].ID, true, map[string]any{}, "")
		id := sendCDP(bridge, conn, sid, "Input.dispatchMouseEvent", map[string]any{"type": "mouseMoved", "x": 1, "y": 2})
		ackOne(bridge, chrome, chrome.waitPending(t, "send")[0].ID, true, map[string]any{}, "")
		cdp.waitReply(t, id)
		if len(edge.rpcs("overlay")) != 0 {
			t.Fatal("edge must not receive chrome overlay RPCs")
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:3528 "hides the overlay before a capture and shows it after the last of overlapping captures"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExtInstance(bridge, ext, "anon", "", []TabSnapshot{mkTab(1)}, map[string]any{"overlayProtocol": 1})
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, pageID(1, "anon"))
		s1 := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		s2 := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		ackOne(bridge, ext, ext.waitPending(t, "overlay")[0].ID, true, map[string]any{}, "")

		c1 := sendCDP(bridge, conn, s1, "Page.captureScreenshot", map[string]any{"format": "png"})
		pHide := ext.waitPending(t, "overlay")
		if pHide[0].Action != OverlayHide || len(ext.rpcs("send")) != 0 {
			t.Fatal("capture must wait for hide")
		}
		ackOne(bridge, ext, pHide[0].ID, true, map[string]any{}, "")
		cap1 := ext.waitPending(t, "send")[0]
		c2 := sendCDP(bridge, conn, s2, "Page.captureScreenshot", map[string]any{"format": "png"})
		waitCond(t, "2nd capture", func() bool { return len(ext.pending("send")) == 2 })
		var cap2 RelayToExtMessage
		for _, r := range ext.pending("send") {
			if r.ID != cap1.ID {
				cap2 = r
			}
		}
		ackOne(bridge, ext, cap1.ID, true, map[string]any{"data": "AA=="}, "")
		cdp.waitReply(t, c1)
		if len(overlayActions(ext)) != 2 {
			t.Fatal("overlay must stay hidden while 2nd capture runs")
		}
		ackOne(bridge, ext, cap2.ID, true, map[string]any{"data": "AA=="}, "")
		cdp.waitReply(t, c2)
		pShow := ext.waitPending(t, "overlay")
		if pShow[0].Action != OverlayShow {
			t.Fatalf("expected show after last capture, got %+v", pShow[0])
		}
		ackOne(bridge, ext, pShow[0].ID, true, map[string]any{}, "")
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:3663 "sends nothing new to an extension that draws no overlay"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, pageID(1, "anon"))
		sid := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		sendCDP(bridge, conn, sid, "Page.captureScreenshot", map[string]any{})
		sendCDP(bridge, conn, sid, "Page.startScreencast", map[string]any{})
		waitCond(t, "2 sends immediately", func() bool { return len(ext.pending("send")) == 2 })
		ackOp(bridge, ext, "send", map[string]any{})
		bridge.CDPClosed(conn)
		if len(ext.rpcs("overlay")) != 0 {
			t.Fatal("non-overlay extension must receive 0 overlay RPCs")
		}
	})

	// ---- Busy extension (lines 3775-3821) ----
	t.Run(`upstream: browser-relay-bridge.test.ts:3775 "defers reloading an outdated extension while it is busy, and reloads it once it is not"`, func(t *testing.T) {
		origVer := RelayVersion
		RelayVersion = "19.1.1"
		defer func() { RelayVersion = origVer }()
		var calls atomic.Int32
		bridge := NewRelayBridge(BridgeOptions{UpdateExtension: func(string) (bool, error) { calls.Add(1); return true, nil }})
		client := bridge.CDPConnected(newFakeCDPSock(), "")
		ext := newFakeExtSock()
		connectExtInstance(bridge, ext, "chrome", "", []TabSnapshot{mkTab(1)}, map[string]any{"extensionId": strings.Repeat("a", 32), "ompVersion": "1.0.0", "reloadProtocol": 1})
		sendExt(bridge, ext, map[string]any{"t": "busy", "busy": true})
		bridge.CDPClosed(client)
		time.Sleep(25 * time.Millisecond)
		if calls.Load() != 0 {
			t.Fatal("must defer while busy")
		}
		sendExt(bridge, ext, map[string]any{"t": "busy", "busy": false})
		waitCond(t, "reload once idle", func() bool { return ext.reloads() == 1 })
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:3792 "does not reload an extension that turned busy while its files were written, until it is idle again"`, func(t *testing.T) {
		origVer := RelayVersion
		RelayVersion = "19.1.1"
		defer func() { RelayVersion = origVer }()
		release := make(chan bool, 2)
		var calls atomic.Int32
		bridge := NewRelayBridge(BridgeOptions{UpdateExtension: func(string) (bool, error) {
			calls.Add(1)
			return <-release, nil
		}})
		ext := newFakeExtSock()
		connectExtInstance(bridge, ext, "chrome", "", []TabSnapshot{mkTab(1)}, map[string]any{"extensionId": strings.Repeat("a", 32), "ompVersion": "1.0.0", "reloadProtocol": 1})
		waitCond(t, "1st update started", func() bool { return calls.Load() == 1 })
		sendExt(bridge, ext, map[string]any{"t": "busy", "busy": true})
		release <- true
		time.Sleep(25 * time.Millisecond)
		if ext.reloads() != 0 {
			t.Fatal("must not reload after turning busy mid-write")
		}
		sendExt(bridge, ext, map[string]any{"t": "busy", "busy": false})
		waitCond(t, "2nd update started", func() bool { return calls.Load() == 2 })
		release <- true
		waitCond(t, "reloaded", func() bool { return ext.reloads() == 1 })
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:3807 "starts a new socket of the same browser idle"`, func(t *testing.T) {
		origVer := RelayVersion
		RelayVersion = "19.1.1"
		defer func() { RelayVersion = origVer }()
		bridge := NewRelayBridge(BridgeOptions{UpdateExtension: func(string) (bool, error) { return true, nil }})
		client := bridge.CDPConnected(newFakeCDPSock(), "")
		old := newFakeExtSock()
		connectExtInstance(bridge, old, "chrome", "", []TabSnapshot{mkTab(1)}, map[string]any{"extensionId": strings.Repeat("a", 32), "ompVersion": "1.0.0", "reloadProtocol": 1})
		sendExt(bridge, old, map[string]any{"t": "busy", "busy": true})
		fresh := newFakeExtSock()
		connectExtInstance(bridge, fresh, "chrome", "", []TabSnapshot{mkTab(1)}, map[string]any{"extensionId": strings.Repeat("a", 32), "ompVersion": "1.0.0", "reloadProtocol": 1})
		bridge.CDPClosed(client)
		waitCond(t, "fresh socket reloads", func() bool { return fresh.reloads() == 1 })
	})
}

func containsString(list []string, target string) bool {
	for _, s := range list {
		if s == target {
			return true
		}
	}
	return false
}
