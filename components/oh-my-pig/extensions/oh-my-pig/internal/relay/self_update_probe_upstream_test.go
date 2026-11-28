package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 1-to-1 port of remaining upstream cases across:
//   - browser-relay-self-update.test.ts (all 25 cases accounted for)
//   - browser-relay-probe.test.ts (all 23 cases accounted for)
//   - browser-relay-native-host.test.ts (all 8 cases accounted for)
//   - browser-relay-server.test.ts (all 5 cases accounted for)
//   - browser-relay-cli.test.ts (all 2 cases accounted for)

func TestUpstreamSelfUpdate_AllCases(t *testing.T) {
	origVer := RelayVersion
	RelayVersion = "19.1.1"
	defer func() { RelayVersion = origVer }()
	extID := strings.Repeat("a", 32)

	t.Run(`upstream: browser-relay-self-update.test.ts:116 "rewrites an older extension that reloads on request, then asks it to reload"`, func(t *testing.T) {
		release := make(chan bool, 1)
		var calls atomic.Int32
		bridge := NewRelayBridge(BridgeOptions{UpdateExtension: func(id string) (bool, error) {
			if id != extID {
				t.Errorf("unexpected id %s", id)
			}
			calls.Add(1)
			return <-release, nil
		}})
		sock := newFakeExtSock()
		connectExtInstance(bridge, sock, "chrome", "", []TabSnapshot{mkTab(1)}, map[string]any{"extensionId": extID, "ompVersion": "18.6.1", "reloadProtocol": 1})
		waitCond(t, "update called", func() bool { return calls.Load() == 1 })
		if bridge.Ready() || sock.reloads() != 0 {
			t.Fatal("must not be ready or reload before files finish")
		}
		release <- true
		waitCond(t, "reloaded and closed", func() bool { return sock.reloads() == 1 && sock.isClosed() })
		if bridge.Ready() {
			t.Fatal("must not be ready after retiring socket")
		}
		reloaded := newFakeExtSock()
		connectExtInstance(bridge, reloaded, "chrome", "", []TabSnapshot{mkTab(1)}, map[string]any{"extensionId": extID, "ompVersion": RelayVersion, "reloadProtocol": 1})
		if !bridge.Ready() || bridge.VersionInfo("ws://x/cdp")["ompExtensionVersion"] != RelayVersion {
			t.Fatal("expected ready with current version after reload")
		}
	})

	t.Run(`upstream: browser-relay-self-update.test.ts:136 "sends nothing more to a browser it asked to reload while another browser stays connected"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{UpdateExtension: func(string) (bool, error) { return true, nil }})
		reloading := newFakeExtSock()
		connectExtInstance(bridge, reloading, "chrome", "", []TabSnapshot{mkTab(1)}, map[string]any{"extensionId": extID, "ompVersion": "18.6.1", "reloadProtocol": 1})
		connectExtInstance(bridge, newFakeExtSock(), "edge", "", []TabSnapshot{mkTab(1)}, map[string]any{"extensionId": extID, "ompVersion": RelayVersion, "reloadProtocol": 1})
		waitCond(t, "reloading closed", func() bool { return reloading.reloads() == 1 && reloading.isClosed() })
		if !bridge.Ready() {
			t.Fatal("edge should keep bridge ready")
		}
		reloading.mu.Lock()
		sentBefore := len(reloading.messages)
		reloading.mu.Unlock()
		conn := bridge.CDPConnected(newFakeCDPSock(), "")
		sendCDP(bridge, conn, "", "Target.attachToTarget", map[string]any{"targetId": fmt.Sprintf("PAGE%s.1", InstanceCode("chrome", "")), "flatten": true})
		time.Sleep(20 * time.Millisecond)
		reloading.mu.Lock()
		sentAfter := len(reloading.messages)
		reloading.mu.Unlock()
		if sentAfter != sentBefore {
			t.Fatalf("must not send after reload; before=%d after=%d", sentBefore, sentAfter)
		}
	})

	t.Run(`upstream: browser-relay-self-update.test.ts:162 "leaves an extension without the reload capability for the manual update"`, func(t *testing.T) {
		var calls atomic.Int32
		bridge := NewRelayBridge(BridgeOptions{UpdateExtension: func(string) (bool, error) { calls.Add(1); return true, nil }})
		connectExtInstance(bridge, newFakeExtSock(), "c", "", []TabSnapshot{mkTab(1)}, map[string]any{"extensionId": extID, "ompVersion": "18.6.1", "reloadProtocol": 0})
		time.Sleep(15 * time.Millisecond)
		if calls.Load() != 0 || !bridge.Ready() {
			t.Fatal("must leave non-reloadable extension alone")
		}
	})

	t.Run(`upstream: browser-relay-self-update.test.ts:169 "leaves an extension installed by this omp or a newer one alone"`, func(t *testing.T) {
		var calls atomic.Int32
		bridge := NewRelayBridge(BridgeOptions{UpdateExtension: func(string) (bool, error) { calls.Add(1); return true, nil }})
		connectExtInstance(bridge, newFakeExtSock(), "c1", "", []TabSnapshot{mkTab(1)}, map[string]any{"extensionId": extID, "ompVersion": RelayVersion, "reloadProtocol": 1})
		connectExtInstance(bridge, newFakeExtSock(), "c2", "", []TabSnapshot{mkTab(1)}, map[string]any{"extensionId": extID, "ompVersion": "999.0.0", "reloadProtocol": 1})
		time.Sleep(15 * time.Millisecond)
		if calls.Load() != 0 {
			t.Fatal("must not update current or newer extension")
		}
	})

	t.Run(`upstream: browser-relay-self-update.test.ts:176 "leaves an extension with no omp version stamp alone"`, func(t *testing.T) {
		var calls atomic.Int32
		bridge := NewRelayBridge(BridgeOptions{UpdateExtension: func(string) (bool, error) { calls.Add(1); return true, nil }})
		connectExtInstance(bridge, newFakeExtSock(), "c1", "", []TabSnapshot{mkTab(1)}, map[string]any{"extensionId": extID, "ompVersion": "", "reloadProtocol": 1})
		connectExtInstance(bridge, newFakeExtSock(), "c2", "", []TabSnapshot{mkTab(1)}, map[string]any{"extensionId": extID, "ompVersion": "dev", "reloadProtocol": 1})
		time.Sleep(15 * time.Millisecond)
		if calls.Load() != 0 {
			t.Fatal("must not update unstamped or non-semver extension")
		}
	})

	t.Run(`upstream: browser-relay-self-update.test.ts:183 "leaves an extension whose socket named no Chrome extension id alone"`, func(t *testing.T) {
		var calls atomic.Int32
		bridge := NewRelayBridge(BridgeOptions{UpdateExtension: func(string) (bool, error) { calls.Add(1); return true, nil }})
		connectExtInstance(bridge, newFakeExtSock(), "c1", "", []TabSnapshot{mkTab(1)}, map[string]any{"extensionId": "", "ompVersion": "18.6.1", "reloadProtocol": 1})
		time.Sleep(15 * time.Millisecond)
		if calls.Load() != 0 {
			t.Fatal("must not update without extensionId")
		}
	})

	t.Run(`upstream: browser-relay-self-update.test.ts:189 "waits while a CDP client is connected and updates once the last one leaves"`, func(t *testing.T) {
		var calls atomic.Int32
		bridge := NewRelayBridge(BridgeOptions{UpdateExtension: func(string) (bool, error) { calls.Add(1); return true, nil }})
		c1 := bridge.CDPConnected(newFakeCDPSock(), "")
		c2 := bridge.CDPConnected(newFakeCDPSock(), "")
		sock := newFakeExtSock()
		connectExtInstance(bridge, sock, "c", "", []TabSnapshot{mkTab(1)}, map[string]any{"extensionId": extID, "ompVersion": "18.6.1", "reloadProtocol": 1})
		bridge.CDPClosed(c1)
		time.Sleep(15 * time.Millisecond)
		if calls.Load() != 0 {
			t.Fatal("must wait for second CDP client")
		}
		bridge.CDPClosed(c2)
		waitCond(t, "reload after last client left", func() bool { return sock.reloads() == 1 })
	})

	t.Run(`upstream: browser-relay-self-update.test.ts:207 "waits while one of the extension's tabs holds a debugger attachment"`, func(t *testing.T) {
		var calls atomic.Int32
		bridge := NewRelayBridge(BridgeOptions{UpdateExtension: func(string) (bool, error) { calls.Add(1); return true, nil }})
		connectExtInstance(bridge, newFakeExtSock(), "c", "", []TabSnapshot{mkTab(1)}, map[string]any{"extensionId": extID, "ompVersion": "18.6.1", "reloadProtocol": 1, "attachedTabIds": []int{1}})
		time.Sleep(20 * time.Millisecond)
		if calls.Load() != 0 || !bridge.Ready() {
			t.Fatal("must wait while tab is attached")
		}
	})

	t.Run(`upstream: browser-relay-self-update.test.ts:214 "does not reload when a CDP client connected while the files were written"`, func(t *testing.T) {
		release := make(chan bool, 1)
		bridge := NewRelayBridge(BridgeOptions{UpdateExtension: func(string) (bool, error) { return <-release, nil }})
		sock := newFakeExtSock()
		connectExtInstance(bridge, sock, "c", "", []TabSnapshot{mkTab(1)}, map[string]any{"extensionId": extID, "ompVersion": "18.6.1", "reloadProtocol": 1})
		bridge.CDPConnected(newFakeCDPSock(), "")
		release <- true
		time.Sleep(20 * time.Millisecond)
		if sock.reloads() != 0 || !bridge.Ready() {
			t.Fatal("must abort reload if CDP client connected mid-write")
		}
	})

	t.Run(`upstream: browser-relay-self-update.test.ts:224 "does not reload when the files were not rewritten"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{UpdateExtension: func(string) (bool, error) { return false, nil }})
		sock := newFakeExtSock()
		connectExtInstance(bridge, sock, "c", "", []TabSnapshot{mkTab(1)}, map[string]any{"extensionId": extID, "ompVersion": "18.6.1", "reloadProtocol": 1})
		time.Sleep(20 * time.Millisecond)
		if sock.reloads() != 0 || !bridge.Ready() {
			t.Fatal("must not reload if updater returned false")
		}
	})

	t.Run(`upstream: browser-relay-self-update.test.ts:233 "waits for a tab create that outlived its timeout, whose late result closes the tab"`, func(t *testing.T) {
		var calls atomic.Int32
		bridge := NewRelayBridge(BridgeOptions{
			RPCTimeout:      30 * time.Millisecond,
			LateRPCKeep:     300 * time.Millisecond,
			UpdateExtension: func(string) (bool, error) { calls.Add(1); return true, nil },
		})
		conn := bridge.CDPConnected(newFakeCDPSock(), "")
		sock := newFakeExtSock()
		connectExtInstance(bridge, sock, "chrome", "", []TabSnapshot{mkTab(1)}, map[string]any{"extensionId": extID, "ompVersion": "18.6.1", "reloadProtocol": 1})
		sendCDP(bridge, conn, "", "Target.createTarget", map[string]any{"background": true})
		pCreate := sock.waitPending(t, "createTab")
		time.Sleep(50 * time.Millisecond)
		bridge.CDPClosed(conn)
		time.Sleep(20 * time.Millisecond)
		if calls.Load() != 0 {
			t.Fatal("must wait for late createTab RPC")
		}
		ackOne(bridge, sock, pCreate[0].ID, true, map[string]any{"tab": mkTab(2)}, "")
		pClose := sock.waitPending(t, "removeTab")
		if calls.Load() != 0 {
			t.Fatal("must wait for removeTab RPC")
		}
		ackOne(bridge, sock, pClose[0].ID, true, map[string]any{}, "")
		waitCond(t, "update after late close", func() bool { return calls.Load() == 1 })
	})

	t.Run(`upstream: browser-relay-self-update.test.ts:312 "waits out a tab create and its close when the client left mid-create, so the tab omp opened is closed"`, func(t *testing.T) {
		var calls atomic.Int32
		bridge := NewRelayBridge(BridgeOptions{UpdateExtension: func(string) (bool, error) { calls.Add(1); return true, nil }})
		conn := bridge.CDPConnected(newFakeCDPSock(), "")
		sock := newFakeExtSock()
		connectExtInstance(bridge, sock, "chrome", "", []TabSnapshot{mkTab(1)}, map[string]any{"extensionId": extID, "ompVersion": "18.6.1", "reloadProtocol": 1})
		sendCDP(bridge, conn, "", "Target.createTarget", map[string]any{"background": true})
		pCreate := sock.waitPending(t, "createTab")
		bridge.CDPClosed(conn)
		time.Sleep(15 * time.Millisecond)
		if calls.Load() != 0 {
			t.Fatal("must wait for in-flight createTab")
		}
		ackOne(bridge, sock, pCreate[0].ID, true, map[string]any{"tab": mkTab(2)}, "")
		pClose := sock.waitPending(t, "removeTab")
		if calls.Load() != 0 {
			t.Fatal("must wait for removeTab")
		}
		ackOne(bridge, sock, pClose[0].ID, true, map[string]any{}, "")
		waitCond(t, "reload after close", func() bool { return sock.reloads() == 1 })
	})

	t.Run(`upstream: browser-relay-self-update.test.ts:354 "waits for a tab close queued for the next hello, which a reload's new browser session would drop"`, func(t *testing.T) {
		var calls atomic.Int32
		bridge := NewRelayBridge(BridgeOptions{UpdateExtension: func(string) (bool, error) { calls.Add(1); return true, nil }})
		conn := bridge.CDPConnected(newFakeCDPSock(), "")
		sock := newFakeExtSock()
		connectExtInstance(bridge, sock, "chrome", "", []TabSnapshot{mkTab(1)}, map[string]any{"extensionId": extID, "ompVersion": "18.6.1", "reloadProtocol": 1})
		sendCDP(bridge, conn, "", "Target.closeTarget", map[string]any{"targetId": fmt.Sprintf("PAGE%s.1", InstanceCode("chrome", ""))})
		ackOne(bridge, sock, sock.waitPending(t, "removeTab")[0].ID, false, nil, "dragging")
		bridge.CDPClosed(conn)
		time.Sleep(20 * time.Millisecond)
		if calls.Load() != 0 {
			t.Fatal("must wait while closeOnHello queued")
		}
		again := newFakeExtSock()
		connectExtInstance(bridge, again, "chrome", "", []TabSnapshot{mkTab(1)}, map[string]any{"extensionId": extID, "ompVersion": "18.6.1", "reloadProtocol": 1})
		pRetry := again.waitPending(t, "removeTab")
		if calls.Load() != 0 {
			t.Fatal("must wait for retry removeTab")
		}
		ackOne(bridge, again, pRetry[0].ID, true, map[string]any{}, "")
		waitCond(t, "reload after queued close", func() bool { return again.reloads() == 1 })
	})

	t.Run(`upstream: browser-relay-self-update.test.ts:395 "reloads once per version, so an extension that comes back with a version it had is not reloaded again"`, func(t *testing.T) {
		var calls atomic.Int32
		bridge := NewRelayBridge(BridgeOptions{UpdateExtension: func(string) (bool, error) { calls.Add(1); return true, nil }})
		s1 := newFakeExtSock()
		connectExtInstance(bridge, s1, "chrome", "", []TabSnapshot{mkTab(1)}, map[string]any{"extensionId": extID, "ompVersion": "18.6.1", "reloadProtocol": 1})
		waitCond(t, "1st reload", func() bool { return s1.reloads() == 1 })
		again := newFakeExtSock()
		connectExtInstance(bridge, again, "chrome", "", []TabSnapshot{mkTab(1)}, map[string]any{"extensionId": extID, "ompVersion": "18.6.1", "reloadProtocol": 1})
		time.Sleep(20 * time.Millisecond)
		if again.reloads() != 0 {
			t.Fatal("must not loop on 18.6.1")
		}
		bridge.ExtClosed(again)
		newer := newFakeExtSock()
		connectExtInstance(bridge, newer, "chrome", "", []TabSnapshot{mkTab(1)}, map[string]any{"extensionId": extID, "ompVersion": "18.7.0", "reloadProtocol": 1})
		waitCond(t, "reload on 18.7.0", func() bool { return newer.reloads() == 1 })
		if calls.Load() != 2 {
			t.Fatalf("expected 2 calls, got %d", calls.Load())
		}
	})

	t.Run(`upstream: browser-relay-self-update.test.ts:469 "waits for the last CDP client to leave before reloading"`, func(t *testing.T) {
		rebuilt := "rebuilt"
		bridge := NewRelayBridge(BridgeOptions{InstalledStamp: func(string) (*RelayExtensionStamp, error) {
			return &RelayExtensionStamp{OmpVersion: RelayVersion, OmpBuild: &rebuilt}, nil
		}})
		conn := bridge.CDPConnected(newFakeCDPSock(), "")
		sock := newFakeExtSock()
		connectExtInstance(bridge, sock, "chrome", "", []TabSnapshot{mkTab(1)}, map[string]any{"extensionId": extID, "ompVersion": RelayVersion, "ompBuild": "stale", "reloadProtocol": 1})
		time.Sleep(20 * time.Millisecond)
		if sock.reloads() != 0 {
			t.Fatal("must wait for CDP client to leave")
		}
		bridge.CDPClosed(conn)
		waitCond(t, "reload after client closed", func() bool { return sock.reloads() == 1 })
	})

	t.Run(`upstream: browser-relay-self-update.test.ts:484 "never reloads an extension whose folder omp does not manage"`, func(t *testing.T) {
		bridge := NewRelayBridge(BridgeOptions{InstalledStamp: func(string) (*RelayExtensionStamp, error) { return nil, nil }})
		sock := newFakeExtSock()
		connectExtInstance(bridge, sock, "chrome", "", []TabSnapshot{mkTab(1)}, map[string]any{"extensionId": extID, "ompVersion": RelayVersion, "ompBuild": "any", "reloadProtocol": 1})
		time.Sleep(20 * time.Millisecond)
		if sock.reloads() != 0 {
			t.Fatal("must never reload unmanaged folder")
		}
	})

	t.Run(`upstream: browser-relay-self-update.test.ts:565 "repairs a corrupted nested icon even when the installed version matches"`, func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "ext")
		if _, err := WriteRelayExtension(dir, RelayVersion, nil); err != nil {
			t.Fatal(err)
		}
		icon := filepath.Join(dir, "icons", "oh-my-pig-32.png")
		orig, err := os.ReadFile(icon)
		if err != nil {
			t.Fatal(err)
		}
		corrupt := append([]byte(nil), orig...)
		corrupt[0] ^= 0xff
		if err := os.WriteFile(icon, corrupt, 0o644); err != nil {
			t.Fatal(err)
		}
		wrote, err := WriteRelayExtension(dir, RelayVersion, nil)
		if err != nil || !wrote {
			t.Fatalf("expected repair write=true, got wrote=%v err=%v", wrote, err)
		}
		repaired, _ := os.ReadFile(icon)
		if string(repaired) != string(orig) {
			t.Fatal("icon was not repaired")
		}
		wrote2, err := WriteRelayExtension(dir, RelayVersion, nil)
		if err != nil || wrote2 {
			t.Fatalf("expected idempotent write=false, got wrote=%v err=%v", wrote2, err)
		}
	})

	t.Run(`upstream: browser-relay-self-update.test.ts:577 "writes background.js last, so an update that fails midway leaves the old version reported"`, func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX read-only directory test")
		}
		dir := filepath.Join(t.TempDir(), "ext")
		if _, err := WriteRelayExtension(dir, "18.6.1", nil); err != nil {
			t.Fatal(err)
		}
		oldBG, _ := os.ReadFile(filepath.Join(dir, "background.js"))
		// Make manifest.json a non-empty directory so renaming manifest.json.tmp over it fails before background.js is touched.
		manifestPath := filepath.Join(dir, "manifest.json")
		_ = os.Remove(manifestPath)
		_ = os.MkdirAll(filepath.Join(manifestPath, "block"), 0o755)
		defer os.RemoveAll(manifestPath)
		if _, err := WriteRelayExtension(dir, RelayVersion, nil); err == nil {
			t.Fatal("expected WriteRelayExtension to fail when manifest.json cannot be replaced")
		}
		curBG, _ := os.ReadFile(filepath.Join(dir, "background.js"))
		if string(curBG) != string(oldBG) {
			t.Fatal("background.js was modified despite earlier manifest failure")
		}
	})
}

func TestUpstreamProbe_RemainingCases(t *testing.T) {
	tmp := isolateRelayEnv(t)
	_ = tmp
	origVer := RelayVersion
	RelayVersion = "19.1.1"
	defer func() { RelayVersion = origVer }()

	t.Run(`upstream: browser-relay-probe.test.ts:140 "keeps polling a young relay and reports ready once the extension handshakes"`, func(t *testing.T) {
		srv := startPublishedRelayServer(t, RelayServerOptions{Port: 0})
		base := fmt.Sprintf("http://127.0.0.1:%d", srv.Port)
		done := make(chan RelayWaitOutcome, 1)
		go func() {
			o, _ := WaitForRelayExtension(context.Background(), base, nil)
			done <- o
		}()
		resp := authGet(t, base+"/json/version", srv.AuthToken)
		_ = resp.Body.Close()
		if resp.StatusCode != 503 {
			t.Fatalf("expected 503 before hello, got %d", resp.StatusCode)
		}
		ext := dialExtAndHello(t, srv, testExtHello, "")
		defer ext.Close()
		if got := <-done; got != RelayOutcomeReady {
			t.Fatalf("expected ready, got %q", got)
		}
	})

	t.Run(`upstream: browser-relay-probe.test.ts:151 "waits through a relay's brief 503 while its browser links again"`, func(t *testing.T) {
		srv := startPublishedRelayServer(t, RelayServerOptions{Port: 0})
		base := fmt.Sprintf("http://127.0.0.1:%d", srv.Port)
		first := dialExtAndHello(t, srv, testExtHello, "")
		if o, _ := WaitForRelayExtension(context.Background(), base, nil); o != RelayOutcomeReady {
			t.Fatalf("want ready, got %q", o)
		}
		_ = first.Close()
		waitCond(t, "503 after disconnect", func() bool { return !srv.Bridge.Ready() })
		done := make(chan RelayWaitOutcome, 1)
		go func() {
			o, _ := WaitForRelayExtension(context.Background(), base, nil)
			done <- o
		}()
		second := dialExtAndHello(t, srv, testExtHello, "")
		defer second.Close()
		if got := <-done; got != RelayOutcomeReady {
			t.Fatalf("expected ready after relink, got %q", got)
		}
	})

	t.Run(`upstream: browser-relay-probe.test.ts:223 "flags an extension installed before omp stamped its version"`, func(t *testing.T) {
		srv := startPublishedRelayServer(t, RelayServerOptions{Port: 0})
		ext := dialExtAndHello(t, srv, testExtHello, "")
		defer ext.Close()
		n := RelayExtensionNotice(context.Background(), fmt.Sprintf("http://127.0.0.1:%d", srv.Port))
		if !strings.Contains(n, "an older") || !strings.Contains(n, UpdateRelayExtension) {
			t.Fatalf("unexpected unstamped notice: %q", n)
		}
	})

	t.Run(`upstream: browser-relay-probe.test.ts:231 "still fails a stamped extension that lacks the relay protocol instead of only noting it"`, func(t *testing.T) {
		srv := startPublishedRelayServer(t, RelayServerOptions{Port: 0})
		leg := map[string]any{"t": "hello", "userAgent": "t", "browserVersion": "Chrome/151", "ompVersion": "18.6.1", "tabs": []any{}, "attachedTabIds": []any{}}
		ext := dialExtAndHello(t, srv, leg, "")
		defer ext.Close()
		if o, _ := WaitForRelayExtension(context.Background(), fmt.Sprintf("http://127.0.0.1:%d", srv.Port), nil); o != RelayOutcomeOutdatedExtension {
			t.Fatalf("want outdated-extension, got %q", o)
		}
	})

	for _, order := range []string{"first", "last"} {
		t.Run(fmt.Sprintf(`upstream: browser-relay-probe.test.ts:237 "names a stale extension when another browser on the relay is current (%s)"`, order), func(t *testing.T) {
			srv := startPublishedRelayServer(t, RelayServerOptions{Port: 0})
			oldH := map[string]any{"t": "hello", "instanceId": "old", "userAgent": "t", "browserVersion": "Chrome/151", "discardedTabsProtocol": 1, "ompVersion": "18.6.1", "tabs": []any{}, "attachedTabIds": []any{}}
			curH := map[string]any{"t": "hello", "instanceId": "cur", "userAgent": "t", "browserVersion": "Chrome/151", "discardedTabsProtocol": 1, "ompVersion": RelayVersion, "tabs": []any{}, "attachedTabIds": []any{}}
			hellos := []map[string]any{oldH, curH}
			if order == "last" {
				hellos = []map[string]any{curH, oldH}
			}
			for _, h := range hellos {
				ws := dialExtAndHello(t, srv, h, "")
				defer ws.Close()
			}
			if n := RelayExtensionNotice(context.Background(), fmt.Sprintf("http://127.0.0.1:%d", srv.Port)); !strings.Contains(n, "18.6.1") {
				t.Fatalf("expected 18.6.1 notice, got %q", n)
			}
		})

		t.Run(fmt.Sprintf(`upstream: browser-relay-probe.test.ts:248 "names a stale extension beside one whose stamp is not a version (%s)"`, order), func(t *testing.T) {
			srv := startPublishedRelayServer(t, RelayServerOptions{Port: 0})
			oldH := map[string]any{"t": "hello", "instanceId": "old", "userAgent": "t", "browserVersion": "Chrome/151", "discardedTabsProtocol": 1, "ompVersion": "18.6.1", "tabs": []any{}, "attachedTabIds": []any{}}
			custH := map[string]any{"t": "hello", "instanceId": "cust", "userAgent": "t", "browserVersion": "Chrome/151", "discardedTabsProtocol": 1, "ompVersion": "build rc2", "tabs": []any{}, "attachedTabIds": []any{}}
			hellos := []map[string]any{oldH, custH}
			if order == "last" {
				hellos = []map[string]any{custH, oldH}
			}
			for _, h := range hellos {
				ws := dialExtAndHello(t, srv, h, "")
				defer ws.Close()
			}
			if n := RelayExtensionNotice(context.Background(), fmt.Sprintf("http://127.0.0.1:%d", srv.Port)); !strings.Contains(n, "18.6.1") {
				t.Fatalf("expected 18.6.1 notice, got %q", n)
			}
		})
	}

	t.Run(`upstream: browser-relay-probe.test.ts:259 "says nothing for an extension whose stamp is not a version, since its age is unknown"`, func(t *testing.T) {
		srv := startPublishedRelayServer(t, RelayServerOptions{Port: 0})
		h := map[string]any{"t": "hello", "userAgent": "t", "browserVersion": "Chrome/151", "discardedTabsProtocol": 1, "ompVersion": "build rc2", "tabs": []any{}, "attachedTabIds": []any{}}
		ws := dialExtAndHello(t, srv, h, "")
		defer ws.Close()
		if n := RelayExtensionNotice(context.Background(), fmt.Sprintf("http://127.0.0.1:%d", srv.Port)); n != "" {
			t.Fatalf("expected empty notice, got %q", n)
		}
	})

	t.Run(`upstream: browser-relay-probe.test.ts:282 "keeps naming a connected stale browser after the current one's service worker reconnects"`, func(t *testing.T) {
		srv := startPublishedRelayServer(t, RelayServerOptions{Port: 0})
		oldH := map[string]any{"t": "hello", "instanceId": "old", "userAgent": "t", "browserVersion": "Chrome/151", "discardedTabsProtocol": 1, "ompVersion": "18.6.1", "tabs": []any{}, "attachedTabIds": []any{}}
		curH := map[string]any{"t": "hello", "instanceId": "cur", "userAgent": "t", "browserVersion": "Chrome/151", "discardedTabsProtocol": 1, "ompVersion": RelayVersion, "tabs": []any{}, "attachedTabIds": []any{}}
		wsOld := dialExtAndHello(t, srv, oldH, "")
		defer wsOld.Close()
		wsCur1 := dialExtAndHello(t, srv, curH, "")
		defer wsCur1.Close()
		wsCur2 := dialExtAndHello(t, srv, curH, "")
		defer wsCur2.Close()
		if n := RelayExtensionNotice(context.Background(), fmt.Sprintf("http://127.0.0.1:%d", srv.Port)); !strings.Contains(n, "18.6.1") {
			t.Fatalf("expected 18.6.1 notice after current reconnect, got %q", n)
		}
	})

	t.Run(`upstream: browser-relay-probe.test.ts:291 "says nothing when the relay is too old to report its extension's version"`, func(t *testing.T) {
		fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 200, map[string]any{
				"ompRelayVersion":                   "18.5.1",
				"ompRelayDiscardedTabsProtocol":     "1",
				"ompExtensionDiscardedTabsProtocol": "1",
			})
		}))
		defer fake.Close()
		if n := RelayExtensionNotice(context.Background(), fake.URL); n != "" {
			t.Fatalf("expected empty notice, got %q", n)
		}
	})
	t.Run(`upstream: browser-relay-probe.test.ts:305 "gives up on the read when the open's deadline passes instead of waiting out its own timeout"`, func(t *testing.T) {
		block := make(chan struct{})
		fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-block
		}))
		defer fake.Close()
		defer close(block) // LIFO: close(block) runs before fake.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		start := time.Now()
		if n := RelayExtensionNotice(ctx, fake.URL); n != "" {
			t.Fatalf("expected empty notice on deadline, got %q", n)
		}
		if time.Since(start) > time.Second {
			t.Fatalf("took too long: %v", time.Since(start))
		}
	})

	t.Run(`upstream: browser-relay-native-host.test.ts:209 "expands ~ to the home directory"`, func(t *testing.T) {
		home, _ := os.UserHomeDir()
		if ResolveSessionCwd("~") != home || ResolveSessionCwd("") != home {
			t.Fatalf("ResolveSessionCwd ~ / '' failed")
		}
		if got := ResolveSessionCwd("~/src/x"); got != filepath.Join(home, "src", "x") {
			t.Fatalf("ResolveSessionCwd ~/src/x = %q", got)
		}
	})

	t.Run(`upstream: browser-relay-cli.test.ts:170 "reports the omp that installed the running script when Chrome restarts onto a newer manifest"`, func(t *testing.T) {
		regFile := filepath.Join(t.TempDir(), "installs.json")
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
		// Stamp 18.6.1 in background.js, verify ReadRelayExtensionStamp reads 18.6.1, and connect worker with that stamp.
		files, err := RelayExtensionFiles("18.6.1", nil)
		if err != nil {
			t.Fatal(err)
		}
		st := ReadRelayExtensionStamp(string(files["background.js"]))
		if st == nil || st.OmpVersion != "18.6.1" {
			t.Fatalf("unexpected stamp: %+v", st)
		}
		ws := dialExtAndHello(t, srv, map[string]any{
			"t": "hello", "instanceId": "restart-worker", "userAgent": "t", "browserVersion": "Chrome/151",
			"discardedTabsProtocol": 1, "reloadProtocol": 1, "ompVersion": st.OmpVersion,
			"tabs": []any{}, "attachedTabIds": []any{},
		}, "chrome-extension://"+strings.Repeat("b", 32))
		defer ws.Close()
		select {
		case <-stopped:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for update stopped")
		}
		n := RelayExtensionNotice(context.Background(), fmt.Sprintf("http://127.0.0.1:%d", srv.Port))
		if !strings.Contains(n, "18.6.1") || !strings.Contains(n, RelayVersion) {
			t.Fatalf("expected notice naming 18.6.1 and %s, got %q", RelayVersion, n)
		}
	})
}

var _ = sync.Mutex{}
var _ = json.Marshal
