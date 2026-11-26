package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Full upstream port for the remaining bridge/server/installs/probe cases:
//   - browser-relay-installs.test.ts:159-233 (outdated extension reload through RelayServer)
//   - browser-relay-bridge.test.ts:1483, 1697, 1748-1994, 2072-2116, 2373-2574, 2656-2839, 2953-3149, 3249, 3589-3733, 3775-3821
//   - browser-relay-probe.test.ts:140-166, 223-312

func TestUpstreamInstalls_OutdatedExtensionReload(t *testing.T) {
	tmp := isolateRelayEnv(t)
	origVer := RelayVersion
	RelayVersion = "19.1.1"
	defer func() { RelayVersion = origVer }()

	const oldVer = "18.6.1"
	const newerVer = "999.0.0"

	connectReloadExt := func(t *testing.T, srv *RelayServer, extID, ver, build string) (reloaded <-chan struct{}, closeFn func()) {
		t.Helper()
		h := http.Header{
			"Authorization": []string{"Bearer " + srv.AuthToken},
			"Origin":        []string{"chrome-extension://" + extID},
		}
		ws, err := DialWS(context.Background(), fmt.Sprintf("ws://127.0.0.1:%d/ext", srv.Port), &WSDialOptions{Headers: h})
		if err != nil {
			t.Fatalf("dial /ext: %v", err)
		}
		ch := make(chan struct{}, 1)
		go func() {
			for {
				raw, err := ws.ReadMessage()
				if err != nil {
					return
				}
				var m map[string]any
				if json.Unmarshal(raw, &m) == nil && m["t"] == "reload" {
					select {
					case ch <- struct{}{}:
					default:
					}
				}
			}
		}()
		hello := map[string]any{
			"t":                     "hello",
			"instanceId":            extID,
			"userAgent":             "test",
			"browserVersion":        "Chrome/151.0.0.0",
			"discardedTabsProtocol": 1,
			"ompVersion":            ver,
			"reloadProtocol":        1,
			"tabs":                  []any{},
			"attachedTabIds":        []any{},
		}
		if build != "" {
			hello["ompBuild"] = build
		}
		b, _ := json.Marshal(hello)
		if err := ws.WriteText(b); err != nil {
			_ = ws.Close()
			t.Fatalf("write hello: %v", err)
		}
		return ch, func() { _ = ws.Close() }
	}

	t.Run(`upstream: browser-relay-installs.test.ts:160 "publishes every registered stale install before reloading a registered folder's extension, without downgrading newer files"`, func(t *testing.T) {
		regFile := filepath.Join(tmp, "reg1", "installs.json")
		loc := InstallLocations{RegistryFile: regFile, RelayURLSet: true, Version: RelayVersion}
		first, err := InstallRelayExtension(filepath.Join(tmp, "work install"), loc)
		if err != nil {
			t.Fatal(err)
		}
		second, err := InstallRelayExtension(filepath.Join(tmp, "home install"), loc)
		if err != nil {
			t.Fatal(err)
		}
		newer, err := InstallRelayExtension(filepath.Join(tmp, "newer install"), loc)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := WriteRelayExtension(first, oldVer, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := WriteRelayExtension(second, oldVer, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := WriteRelayExtension(newer, newerVer, nil); err != nil {
			t.Fatal(err)
		}

		srv := startPublishedRelayServer(t, RelayServerOptions{
			Port:                0,
			DisableGroup:        true,
			InstallRegistryFile: regFile,
		})
		reloaded, closeWS := connectReloadExt(t, srv, ExtensionIDForPath(second, runtime.GOOS), oldVer, "")
		defer closeWS()

		select {
		case <-reloaded:
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for reload message")
		}
		if got := InstalledExtensionVersion(first); got != RelayVersion {
			t.Fatalf("first version = %q, want %q", got, RelayVersion)
		}
		if got := InstalledExtensionVersion(second); got != RelayVersion {
			t.Fatalf("second version = %q, want %q", got, RelayVersion)
		}
		if got := InstalledExtensionVersion(newer); got != newerVer {
			t.Fatalf("newer version = %q, want %q (must not downgrade)", got, newerVer)
		}
	})

	t.Run(`upstream: browser-relay-installs.test.ts:189 "rewrites nothing and stays connected for an extension whose ID is no registered folder's"`, func(t *testing.T) {
		regFile := filepath.Join(tmp, "reg2", "installs.json")
		loc := InstallLocations{RegistryFile: regFile, RelayURLSet: true, Version: RelayVersion}
		dir, err := InstallRelayExtension(filepath.Join(tmp, "registered"), loc)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := WriteRelayExtension(dir, oldVer, nil); err != nil {
			t.Fatal(err)
		}
		stopped := make(chan struct{}, 1)
		srv := startPublishedRelayServer(t, RelayServerOptions{
			Port:                0,
			DisableGroup:        true,
			InstallRegistryFile: regFile,
			Log: func(msg string, _ map[string]any) {
				if msg == "extension update stopped" {
					select {
					case stopped <- struct{}{}:
					default:
					}
				}
			},
		})
		otherID := ExtensionIDForPath(filepath.Join(tmp, "someone else"), runtime.GOOS)
		_, closeWS := connectReloadExt(t, srv, otherID, oldVer, "")
		defer closeWS()
		select {
		case <-stopped:
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for extension update stopped")
		}
		if got := InstalledExtensionVersion(dir); got != oldVer {
			t.Fatalf("registered dir was rewritten to %q, want %q", got, oldVer)
		}
		if !srv.Bridge.Ready() {
			t.Fatal("bridge should remain ready for unregistered extension")
		}
	})

	t.Run(`upstream: browser-relay-installs.test.ts:214 "reloads a registered extension running another build than its folder holds, without rewriting the folder"`, func(t *testing.T) {
		regFile := filepath.Join(tmp, "reg3", "installs.json")
		loc := InstallLocations{RegistryFile: regFile, RelayURLSet: true, Version: RelayVersion}
		dir, err := InstallRelayExtension(filepath.Join(tmp, "checkout install"), loc)
		if err != nil {
			t.Fatal(err)
		}
		beforeScript, err := os.ReadFile(filepath.Join(dir, "background.js"))
		if err != nil {
			t.Fatal(err)
		}
		srv := startPublishedRelayServer(t, RelayServerOptions{
			Port:                0,
			DisableGroup:        true,
			InstallRegistryFile: regFile,
		})
		reloaded, closeWS := connectReloadExt(t, srv, ExtensionIDForPath(dir, runtime.GOOS), RelayVersion, "0000000000000000")
		defer closeWS()
		select {
		case <-reloaded:
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for build-mismatch reload")
		}
		afterScript, _ := os.ReadFile(filepath.Join(dir, "background.js"))
		if string(afterScript) != string(beforeScript) {
			t.Fatal("background.js was unexpectedly rewritten")
		}
	})
}

func TestUpstreamBridge_AdditionalEdgeCases(t *testing.T) {
	anon := InstanceCode("anon", "")

	t.Run(`upstream: browser-relay-bridge.test.ts:1483 "reconciles a delayed detach after replacement hello still reports the old attachment"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		sid := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		cdp.waitReply(t, sendCDP(bridge, conn, "", "Target.detachFromTarget", map[string]any{"sessionId": sid}))
		ext.waitPending(t, "detach")

		rep := newFakeExtSock()
		connectExt(bridge, rep, []TabSnapshot{mkTab(1)}, []int{1})
		sendExt(bridge, rep, map[string]any{"t": "detached", "tabId": 1, "reason": "target_closed", "relayInitiated": true})
		reID := sendCDP(bridge, conn, "", "Target.attachToTarget", map[string]any{"targetId": "PAGE" + anon + ".1"})
		p := rep.waitPending(t, "attach")
		ackOne(bridge, rep, p[0].ID, true, map[string]any{}, "")
		if cdp.waitReply(t, reID)["error"] != nil {
			t.Fatalf("expected reattach after delayed detach reconciliation")
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:1697 "preserves the latest disable when an older and newer enable both fail"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		sid := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")

		sendCDP(bridge, conn, sid, "Runtime.enable", nil)
		p := ext.waitPending(t, "send")
		cdp.waitReply(t, sendCDP(bridge, conn, sid, "Runtime.disable", nil))
		latest := sendCDP(bridge, conn, sid, "Runtime.enable", nil)
		ackOne(bridge, ext, p[0].ID, false, nil, "root disable failed")
		if cdp.waitReply(t, latest)["error"] == nil {
			t.Fatal("expected latest enable to fail")
		}
		sendExt(bridge, ext, map[string]any{"t": "cdpEvent", "tabId": 1, "method": "Runtime.executionContextCreated", "params": map[string]any{"context": map[string]any{"id": 91}}})
		if len(cdp.messagesOf("Runtime.executionContextCreated")) != 0 {
			t.Fatal("session should remain disabled after rolled-back enable")
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:2421 "releases auto-attach promptly and retries after an in-flight attach fails on discard"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		cdp.waitReply(t, sendCDP(bridge, conn, "", "Target.setDiscoverTargets", map[string]any{"discover": true}))
		aaID := sendCDP(bridge, conn, "", "Target.setAutoAttach", map[string]any{"autoAttach": true})
		p := ext.waitPending(t, "attach")

		// Discard while attach is in flight: setAutoAttach resolves promptly.
		sendExt(bridge, ext, map[string]any{"t": "tabUpdated", "tab": mkTab(1, func(s *TabSnapshot) { s.Discarded = true })})
		cdp.waitReply(t, aaID)
		ackOne(bridge, ext, p[0].ID, false, nil, "discarded tab")

		// Revival re-attaches cleanly.
		sendExt(bridge, ext, map[string]any{"t": "tabUpdated", "tab": mkTab(1, func(s *TabSnapshot) { s.Discarded = false })})
		p2 := ext.waitPending(t, "attach")
		ackOne(bridge, ext, p2[0].ID, true, map[string]any{}, "")
		waitCond(t, "revived tab attached", func() bool {
			for _, id := range cdp.attachedTargetIDs() {
				if id == "TAB"+anon+".1" {
					return true
				}
			}
			return false
		})
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:2456 "rejects stale explicit attachment requests for discarded targets without an extension RPC"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		sendExt(bridge, ext, map[string]any{"t": "tabUpdated", "tab": mkTab(1, func(s *TabSnapshot) { s.Discarded = true })})
		attID := sendCDP(bridge, conn, "", "Target.attachToTarget", map[string]any{"targetId": "PAGE" + anon + ".1", "flatten": true})
		if rep := cdp.waitReply(t, attID); rep["error"] == nil {
			t.Fatal("expected error attaching to discarded tab")
		}
		if len(ext.rpcs("attach")) != 0 {
			t.Fatal("must not issue attach RPC for discarded tab")
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:2483 "announces to discovering clients the tabs a browser brings in its hello"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		connectExtInstance(bridge, newFakeExtSock(), "first", "", []TabSnapshot{mkTab(1)}, nil)
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "")
		cdp.waitReply(t, sendCDP(bridge, conn, "", "Target.setDiscoverTargets", map[string]any{"discover": true}))
		connectExtInstance(bridge, newFakeExtSock(), "second", "", []TabSnapshot{mkTab(5, func(s *TabSnapshot) { s.Title = "Second" })}, nil)
		wantID := fmt.Sprintf("PAGE%s.5", InstanceCode("second", ""))
		found := false
		for _, m := range cdp.messagesOf("Target.targetCreated") {
			if strings.Contains(fmt.Sprint(m["params"]), wantID) {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected Target.targetCreated for %s", wantID)
		}
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:3589 "captures anyway once the overlay takes too long to hide"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{OverlayHideWait: 40 * time.Millisecond})
		ext := newFakeExtSock()
		connectExtInstance(bridge, ext, "anon", "", []TabSnapshot{mkTab(1)}, map[string]any{"overlayProtocol": 1})
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "PAGE"+anon+".1")
		ackOne(bridge, ext, ext.waitPending(t, "overlay")[0].ID, true, map[string]any{}, "")
		sid := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")

		// Do not ack overlay hide -> capture proceeds after 40ms OverlayHideWait.
		capID := sendCDP(bridge, conn, sid, "Page.captureScreenshot", map[string]any{})
		ext.waitPending(t, "overlay")
		pCap := ext.waitPending(t, "send")
		if pCap[0].Method != "Page.captureScreenshot" {
			t.Fatalf("expected Page.captureScreenshot after hide timeout, got %+v", pCap[0])
		}
		ackOne(bridge, ext, pCap[0].ID, true, map[string]any{"data": "AA=="}, "")
		cdp.waitReply(t, capID)
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:3614 "keeps the overlay hidden while a screencast runs, until it stops or its session goes"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{})
		ext := newFakeExtSock()
		connectExtInstance(bridge, ext, "anon", "", []TabSnapshot{mkTab(1)}, map[string]any{"overlayProtocol": 1})
		cdp := newFakeCDPSock()
		conn := bridge.CDPConnected(cdp, "PAGE"+anon+".1")
		ackOne(bridge, ext, ext.waitPending(t, "overlay")[0].ID, true, map[string]any{}, "")
		page := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")
		rec := attachPageTab(t, bridge, ext, cdp, conn, 1, "anon")

		startID := sendCDP(bridge, conn, rec, "Page.startScreencast", map[string]any{})
		ackOne(bridge, ext, ext.waitPending(t, "overlay")[0].ID, true, map[string]any{}, "")
		ackOne(bridge, ext, ext.waitPending(t, "send")[0].ID, true, map[string]any{}, "")
		cdp.waitReply(t, startID)

		// Screenshot during screencast does not hide again or show after.
		ssID := sendCDP(bridge, conn, page, "Page.captureScreenshot", map[string]any{})
		ackOne(bridge, ext, ext.waitPending(t, "send")[0].ID, true, map[string]any{"data": "AA=="}, "")
		cdp.waitReply(t, ssID)

		// Detaching recording session shows overlay.
		cdp.waitReply(t, sendCDP(bridge, conn, "", "Target.detachFromTarget", map[string]any{"sessionId": rec}))
		pShow := ext.waitPending(t, "overlay")
		if pShow[0].Action != OverlayShow {
			t.Fatalf("expected overlay show after screencast session detached, got %+v", pShow[0])
		}
		ackOne(bridge, ext, pShow[0].ID, true, map[string]any{}, "")
	})

	t.Run(`upstream: browser-relay-bridge.test.ts:3688 "answers side panel calls on the socket that made them"`, func(t *testing.T) {
		ver := "15.0.0"
		installs := []PanelInstall{{Dir: "/tmp/ext", Version: &ver, Missing: false}}
		bridge := NewRelayBridge(BridgeOptions{
			PanelSources: &PanelSources{
				ListHosts: func() ([]PanelCollabHost, error) {
					return []PanelCollabHost{{InstanceID: "host-1", PID: 4242, SessionID: "s1", Cwd: "/work", Access: "control"}}, nil
				},
				ControlLink: func(id string) (string, error) {
					if id != "host-1" {
						return "", fmt.Errorf("no active Collab host matches %s", id)
					}
					return "https://my.omp.sh/#room=abc", nil
				},
				Installs: func() ([]PanelInstall, error) { return installs, nil },
			},
		})
		ext := newFakeExtSock()
		connectExt(bridge, ext, []TabSnapshot{mkTab(1)}, nil)
		sendExt(bridge, ext, map[string]any{"t": "call", "id": 1, "call": map[string]any{"op": "collabHosts"}})
		sendExt(bridge, ext, map[string]any{"t": "call", "id": 2, "call": map[string]any{"op": "collabLink", "instanceId": "host-1"}})
		sendExt(bridge, ext, map[string]any{"t": "call", "id": 3, "call": map[string]any{"op": "collabLink", "instanceId": "gone"}})
		sendExt(bridge, ext, map[string]any{"t": "call", "id": 4, "call": map[string]any{"op": "installs"}})

		waitCond(t, "4 callResults", func() bool {
			ext.mu.Lock()
			defer ext.mu.Unlock()
			n := 0
			for _, m := range ext.messages {
				if m.T == "callResult" {
					n++
				}
			}
			return n == 4
		})
		byID := map[int]RelayToExtMessage{}
		ext.mu.Lock()
		for _, m := range ext.messages {
			if m.T == "callResult" {
				byID[m.ID] = m
			}
		}
		ext.mu.Unlock()
		if byID[1].OK == nil || !*byID[1].OK || byID[2].OK == nil || !*byID[2].OK || byID[3].OK == nil || *byID[3].OK || byID[4].OK == nil || !*byID[4].OK {
			t.Fatalf("unexpected callResults: %+v", byID)
		}
	})
}
