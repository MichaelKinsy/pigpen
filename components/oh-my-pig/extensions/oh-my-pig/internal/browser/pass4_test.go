package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// --- Pass-4 Finding 1 (deterministic unit): ZERO CDP commands sent between Page.frameRequestedNavigation and commit ---

func TestPass4_F1_NoCDPTrafficDuringRequestedNavigationWindow(t *testing.T) {
	var inWindow atomic.Bool
	var violations []string
	var mu sync.Mutex

	srv := startFakeCDPServer(t, func(method string, params json.RawMessage) (any, error) {
		if inWindow.Load() {
			mu.Lock()
			violations = append(violations, method)
			mu.Unlock()
		}
		switch method {
		case "Target.createTarget":
			return map[string]any{"targetId": "t1"}, nil
		case "Target.attachToTarget":
			return map[string]any{"sessionId": "s1"}, nil
		case "Page.getFrameTree":
			return map[string]any{"frameTree": map[string]any{"frame": map[string]any{"id": "main-f", "url": "http://example.test/a"}}}, nil
		case "Page.createIsolatedWorld":
			return map[string]any{"executionContextId": 77}, nil
		case "Runtime.evaluate":
			var p struct {
				Expression string `json:"expression"`
			}
			_ = json.Unmarshal(params, &p)
			if strings.Contains(p.Expression, "document.readyState") {
				return map[string]any{"result": map[string]any{"type": "string", "value": "complete"}}, nil
			}
			if strings.Contains(p.Expression, "queryAll") {
				return map[string]any{"result": map[string]any{"type": "number", "value": float64(1)}}, nil
			}
			return map[string]any{"result": map[string]any{"type": "string", "value": "ok"}}, nil
		default:
			return map[string]any{}, nil
		}
	})

	b := NewChromiumBackend()
	tab, err := b.Open(context.Background(), OpenOptions{Name: "navwin", Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer tab.Close(context.Background(), false)
	ct := tab.(*cdpTab)

	// Simulate Chrome's exact anchorClick event sequence from our real trace:
	// frameScheduledNavigation -> frameRequestedNavigation -> frameStartedNavigating ->
	// frameStartedLoading -> frameClearedScheduledNavigation (BEFORE commit!).
	// frameClearedScheduledNavigation MUST NOT disarm the barrier.
	inWindow.Store(true)
	ct.onCDPEvent(CDPEvent{
		SessionID: ct.sessionID,
		Method:    "Page.frameScheduledNavigation",
		Params:    json.RawMessage(`{"frameId":"main-f","delay":0,"reason":"anchorClick","url":"http://example.test/b"}`),
	})
	ct.onCDPEvent(CDPEvent{
		SessionID: ct.sessionID,
		Method:    "Page.frameRequestedNavigation",
		Params:    json.RawMessage(`{"frameId":"main-f","disposition":"currentTab","url":"http://example.test/b"}`),
	})
	ct.onCDPEvent(CDPEvent{
		SessionID: ct.sessionID,
		Method:    "Page.frameStartedNavigating",
		Params:    json.RawMessage(`{"frameId":"main-f","url":"http://example.test/b","navigationType":"differentDocument"}`),
	})
	ct.onCDPEvent(CDPEvent{
		SessionID: ct.sessionID,
		Method:    "Page.frameStartedLoading",
		Params:    json.RawMessage(`{"frameId":"main-f"}`),
	})
	ct.onCDPEvent(CDPEvent{
		SessionID: ct.sessionID,
		Method:    "Page.frameClearedScheduledNavigation",
		Params:    json.RawMessage(`{"frameId":"main-f"}`),
	})
	// Commit arrives 120ms later (well over several mainFramePollMs ticks).
	go func() {
		time.Sleep(120 * time.Millisecond)
		inWindow.Store(false)
		ct.onCDPEvent(CDPEvent{
			SessionID: ct.sessionID,
			Method:    "Page.frameNavigated",
			Params:    json.RawMessage(`{"frame":{"id":"main-f","url":"http://example.test/b"}}`),
		})
		ct.onCDPEvent(CDPEvent{
			SessionID: ct.sessionID,
			Method:    "Runtime.executionContextCreated",
			Params:    json.RawMessage(`{"context":{"id":88,"name":"omp-browser-kit","auxData":{"isDefault":false,"frameId":"main-f"}}}`),
		})
	}()

	start := time.Now()
	res, err := tab.Call(context.Background(), CallContext{TimeoutMs: 5000}, []CallStep{{
		Method: "count",
		Args:   []json.RawMessage{json.RawMessage(`"#target"`)},
	}})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("count after requested navigation failed: %v", err)
	}
	if res.Value != float64(1) {
		t.Fatalf("expected count=1, got %#v", res.Value)
	}
	if elapsed < 100*time.Millisecond {
		t.Fatalf("expected helper to wait for commit (~120ms), returned too early in %v", elapsed)
	}
	mu.Lock()
	gotViolations := append([]string(nil), violations...)
	mu.Unlock()
	if len(gotViolations) != 0 {
		t.Fatalf("CDP commands were sent during the requested-navigation window: %v", gotViolations)
	}
}

// Deterministic regression for the exact wedged trace: Chrome clears contexts
// and commits a new document while a Runtime.evaluate targeting the old context
// receives NO reply from Chrome. sendInContext must abort promptly (~250ms),
// invalidate only the old world (preserving the newly created world), and retry
// cleanly without stalling for the whole 30s call budget.
func TestPass4_F1_DroppedReplyOnClearedContextRecoversWithoutStall(t *testing.T) {
	var createWorldCalls atomic.Int32
	hangDone := make(chan struct{})
	defer close(hangDone)

	srv := startFakeCDPServer(t, func(method string, params json.RawMessage) (any, error) {
		switch method {
		case "Target.createTarget":
			return map[string]any{"targetId": "t1"}, nil
		case "Target.attachToTarget":
			return map[string]any{"sessionId": "s1"}, nil
		case "Page.getFrameTree":
			return map[string]any{"frameTree": map[string]any{"frame": map[string]any{"id": "main-f", "url": "http://example.test/a"}}}, nil
		case "Page.createIsolatedWorld":
			createWorldCalls.Add(1)
			return map[string]any{"executionContextId": 765}, nil
		case "Runtime.evaluate":
			var p struct {
				ContextID  int64  `json:"contextId"`
				Expression string `json:"expression"`
			}
			_ = json.Unmarshal(params, &p)
			if p.ContextID == 763 {
				// Simulate Chrome silently dropping the reply to #3506 on old context 763.
				<-hangDone
				return nil, fmt.Errorf("closed")
			}
			if strings.Contains(p.Expression, "document.readyState") {
				return map[string]any{"result": map[string]any{"type": "string", "value": "complete"}}, nil
			}
			if p.ContextID == 765 {
				return map[string]any{"result": map[string]any{"type": "string", "value": "B"}}, nil
			}
			return map[string]any{"result": map[string]any{"type": "string", "value": ""}}, nil
		default:
			return map[string]any{}, nil
		}
	})

	b := NewChromiumBackend()
	tab, err := b.Open(context.Background(), OpenOptions{Name: "drop", Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer tab.Close(context.Background(), false)
	ct := tab.(*cdpTab)

	// Seed old isolated world 763 as installed (before navigation commits).
	ct.onCDPEvent(CDPEvent{
		SessionID: ct.sessionID,
		Method:    "Runtime.executionContextCreated",
		Params:    json.RawMessage(`{"context":{"id":763,"name":"omp-browser-kit","auxData":{"isDefault":false,"frameId":"main-f"}}}`),
	})

	// While text("#marker") is in-flight on 763, Chrome emits executionContextsCleared,
	// frameNavigated, and executionContextCreated for the new world 765, then never replies to 763.
	go func() {
		time.Sleep(30 * time.Millisecond)
		ct.onCDPEvent(CDPEvent{
			SessionID: ct.sessionID,
			Method:    "Runtime.executionContextsCleared",
			Params:    json.RawMessage(`{}`),
		})
		ct.onCDPEvent(CDPEvent{
			SessionID: ct.sessionID,
			Method:    "Page.frameNavigated",
			Params:    json.RawMessage(`{"frame":{"id":"main-f","url":"http://example.test/b"}}`),
		})
		ct.onCDPEvent(CDPEvent{
			SessionID: ct.sessionID,
			Method:    "Runtime.executionContextCreated",
			Params:    json.RawMessage(`{"context":{"id":765,"name":"omp-browser-kit","auxData":{"isDefault":false,"frameId":"main-f"}}}`),
		})
	}()

	res, err := tab.Call(context.Background(), CallContext{TimeoutMs: 30000}, []CallStep{{
		Method: "text",
		Args:   []json.RawMessage{json.RawMessage(`"#marker"`)},
	}})
	if err != nil {
		t.Fatalf("expected recovery on new context 765, got err=%v", err)
	}
	if res.Value != "B" {
		t.Fatalf("expected value %q from new context 765, got %#v", "B", res.Value)
	}
	if n := createWorldCalls.Load(); n != 0 {
		t.Fatalf("expected reuse of newly created world 765 without calling Page.createIsolatedWorld, got %d calls", n)
	}
	// Verify per-tab CDP trace captured outbound commands, cancellation, and retry reply.
	if tr := ct.CDPTrace(20); !strings.Contains(tr, "Runtime.evaluate") || !strings.Contains(tr, "context canceled") || !strings.Contains(tr, `"value":"B"`) {
		t.Fatalf("expected CDPTrace to include evaluate, canceled old call, and retry reply, got:\n%s", tr)
	}
}

// Deterministic regression matching omp (tab-worker.ts:3148, 4366-4376):
// when goto times out (including if a readyState poll stalls), waitForLifecycle
// bounds each readyState poll, sends best-effort Page.stopLoading, and returns
// NavigationTimeoutError promptly within the goto budget.
func TestPass6_GotoTimeoutSendsStopLoadingAndBoundsReadyStatePoll(t *testing.T) {
	var stopLoadingSent atomic.Bool
	hangDone := make(chan struct{})
	defer close(hangDone)

	var stallNav atomic.Bool
	srv := startFakeCDPServer(t, func(method string, params json.RawMessage) (any, error) {
		if stallNav.Load() {
			switch method {
			case "Page.stopLoading":
				stopLoadingSent.Store(true)
				return map[string]any{}, nil
			case "Runtime.evaluate":
				<-hangDone
				return nil, fmt.Errorf("closed")
			case "Page.navigate":
				return map[string]any{"frameId": "main-f"}, nil
			default:
				return map[string]any{}, nil
			}
		}
		switch method {
		case "Target.createTarget":
			return map[string]any{"targetId": "t1"}, nil
		case "Target.attachToTarget":
			return map[string]any{"sessionId": "s1"}, nil
		case "Page.getFrameTree":
			return map[string]any{"frameTree": map[string]any{"frame": map[string]any{"id": "main-f", "url": "http://example.test/"}}}, nil
		default:
			return map[string]any{}, nil
		}
	})

	b := NewChromiumBackend()
	tab, err := b.Open(context.Background(), OpenOptions{Name: "slowgoto", Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer tab.Close(context.Background(), false)

	stallNav.Store(true)
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	ct := tab.(*cdpTab)
	err = ct.navigate(ctx, "http://example.test/next", "load", 400)
	if err == nil || !strings.Contains(err.Error(), "timed out after 400ms; pending navigation stopped") {
		t.Fatalf("expected NavigationTimeoutError from stalled goto, got %v", err)
	}
	if !stopLoadingSent.Load() {
		t.Fatalf("expected best-effort Page.stopLoading on goto timeout (matching omp tab-worker.ts:3148)")
	}
}

// --- Pass-4 Finding 3 (unit): ensureKit NEVER falls back to installing kit in page's main world ---

func TestPass4_F3_EnsureKitNeverFallsBackToMainWorld(t *testing.T) {
	var mainWorldKitEval atomic.Bool
	srv := startFakeCDPServer(t, func(method string, params json.RawMessage) (any, error) {
		switch method {
		case "Target.createTarget":
			return map[string]any{"targetId": "t1"}, nil
		case "Target.attachToTarget":
			return map[string]any{"sessionId": "s1"}, nil
		case "Page.getFrameTree":
			return map[string]any{"frameTree": map[string]any{"frame": map[string]any{"id": "main-f", "url": "http://example.test/"}}}, nil
		case "Page.createIsolatedWorld":
			return nil, fmt.Errorf("frame detached")
		case "Runtime.evaluate":
			var p struct {
				Expression string `json:"expression"`
				ContextID  int64  `json:"contextId"`
			}
			_ = json.Unmarshal(params, &p)
			if p.ContextID == 0 && strings.Contains(p.Expression, "__ompBrowserKit") {
				mainWorldKitEval.Store(true)
			}
			return map[string]any{"result": map[string]any{"type": "string", "value": ""}}, nil
		default:
			return map[string]any{}, nil
		}
	})
	b := NewChromiumBackend()
	tab, err := b.Open(context.Background(), OpenOptions{Name: "iso", Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer tab.Close(context.Background(), false)

	_, err = tab.Call(context.Background(), CallContext{TimeoutMs: 5000}, []CallStep{{
		Method: "count",
		Args:   []json.RawMessage{json.RawMessage(`"#x"`)},
	}})
	if err == nil || !strings.Contains(err.Error(), "Page.createIsolatedWorld") {
		t.Fatalf("expected Page.createIsolatedWorld error, got %v", err)
	}
	if mainWorldKitEval.Load() {
		t.Fatalf("ensureKit installed __ompBrowserKit in the page's MAIN world after createIsolatedWorld failed")
	}
}

// --- Pass-4 Finding 4 (unit): two sessions with different browser.cdpUrl get isolated Documentation/Guidance ---

func TestPass4_F4_TwoSessionsDocumentationGuidanceIsolated(t *testing.T) {
	origSetting := Setting
	t.Cleanup(func() { Setting = origSetting })

	// Start two fake extension hosts with distinct session IDs.
	extA := sdk.New("test-a")
	extB := sdk.New("test-b")
	var ctxA, ctxB sdk.Context
	var mu sync.Mutex
	extA.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		mu.Lock()
		ctxA = ctx
		mu.Unlock()
		return nil, nil
	})
	extB.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		mu.Lock()
		ctxB = ctx
		mu.Unlock()
		return nil, nil
	})
	hA := startTestFakeHost(t, extA, "sess-remote-A")
	hB := startTestFakeHost(t, extB, "sess-local-B")
	hA.fire("session_start", map[string]any{})
	hB.fire("session_start", map[string]any{})

	Setting = func(ctx sdk.Context, key string) any {
		id := sessionIDFromSDK(ctx)
		if key == "browser.enabled" {
			return true
		}
		if key == "browser.cdpUrl" {
			if id == "sess-remote-A" {
				return "http://10.0.0.5:9222"
			}
			return ""
		}
		return nil
	}

	m := NewManager()
	m.platform = "darwin"
	p := PreludeDefinition(m)

	// Session A calls Enabled, then Session B calls Enabled (interleaved).
	if !p.Enabled(ctxA) || !p.Enabled(ctxB) {
		t.Fatalf("expected both sessions enabled")
	}

	// Session A MUST still see the remote-CDP X11 guidance even though B called Enabled last.
	docA := p.DocumentationText(ctxA)
	guidA := p.GuidanceText(ctxA)
	docB := p.DocumentationText(ctxB)
	guidB := p.GuidanceText(ctxB)

	if !strings.Contains(docA, "X11") && !strings.Contains(docA, "display") {
		t.Fatalf("session A documentation lost X11 note after session B Enabled():\n%s", docA)
	}
	if !strings.Contains(guidA, "X11") && !strings.Contains(guidA, "display") {
		t.Fatalf("session A guidance lost X11 note after session B Enabled():\n%s", guidA)
	}
	if strings.Contains(docB, "X11") {
		t.Fatalf("session B documentation wrongly leaked session A's X11 note")
	}
	if strings.Contains(guidB, "X11") {
		t.Fatalf("session B guidance wrongly leaked session A's X11 note")
	}
}

// --- Pass-4 Finding 5 (unit): stale empty profile dir cleanup ---

func TestPass4_F5_SweepEmptyStaleProfileDirs(t *testing.T) {
	base := t.TempDir()

	// 1. Old empty prefixed dir (no marker, older than 10m) -> MUST BE REMOVED.
	oldEmpty := filepath.Join(base, tempProfilePrefix+"old-empty")
	if err := os.MkdirAll(oldEmpty, 0o700); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-15 * time.Minute)
	if err := os.Chtimes(oldEmpty, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	// 2. Fresh empty prefixed dir (just created, no marker yet) -> MUST BE KEPT.
	freshEmpty := filepath.Join(base, tempProfilePrefix+"fresh-empty")
	if err := os.MkdirAll(freshEmpty, 0o700); err != nil {
		t.Fatal(err)
	}

	// 3. Old non-empty prefixed dir without marker -> MUST BE KEPT (no marker).
	oldNonEmpty := filepath.Join(base, tempProfilePrefix+"old-nonempty")
	if err := os.MkdirAll(oldNonEmpty, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldNonEmpty, "data.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(oldNonEmpty, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	cleanupStaleProfilesIn(base)

	if _, err := os.Stat(oldEmpty); !os.IsNotExist(err) {
		t.Fatalf("expected old empty prefixed dir to be swept, stat err=%v", err)
	}
	if _, err := os.Stat(freshEmpty); err != nil {
		t.Fatalf("expected fresh empty prefixed dir to be kept, stat err=%v", err)
	}
	if _, err := os.Stat(oldNonEmpty); err != nil {
		t.Fatalf("expected non-empty unmarked dir to be kept, stat err=%v", err)
	}
}

// --- Pass-4 Finding 2 (real browser): crashing a tab's renderer fails cleanly, recycles tab, and spares other tabs & sessions ---

func TestRealPass4_F2_RendererCrashRecyclesTabAndSparesOtherSessions(t *testing.T) {
	m, _, reqA := setupRealBrowserTest(t)
	reqB := InvokeRequest{Cwd: t.TempDir(), OwnerSessionID: "pass4-other-sess"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<!doctype html><html><head><title>Healthy</title></head><body><div id="ok">`+r.URL.Path+`</div></body></html>`)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	// Open tab "victim" and sibling tab "peer" in session A, plus tab "other" in session B.
	for _, spec := range []struct {
		req  InvokeRequest
		name string
		path string
	}{
		{reqA, "victim", "/victim"},
		{reqA, "peer", "/peer"},
		{reqB, "other", "/other"},
	} {
		p, _ := json.Marshal(map[string]any{"action": "open", "name": spec.name, "url": srv.URL + spec.path, "app": "chromium"})
		if _, err := m.Invoke(ctx, spec.req, p); err != nil {
			t.Fatalf("open %s: %v", spec.name, err)
		}
	}

	// Grab the underlying cdpTab for "victim" and crash its renderer via CDP Page.crash
	// while an in-flight call is waiting on it.
	mt := m.getLiveTab(ctx, tabKey{owner: reqA.OwnerSessionID, name: "victim"})
	if mt == nil {
		t.Fatal("victim tab not found")
	}
	victim := mt.tab.(*cdpTab)

	errCh := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := m.Invoke(ctx, reqA, json.RawMessage(`{"action":"call","name":"victim","chain":[{"method":"waitFor","args":["#never",{"timeout":15000}]}]}`))
		errCh <- err
	}()
	time.Sleep(150 * time.Millisecond)

	// Send Page.crash directly to the victim's CDP session.
	crashCtx, crashCancel := context.WithTimeout(ctx, 2*time.Second)
	_ = victim.proc.client.Send(crashCtx, victim.sessionID, "Page.crash", nil, nil)
	crashCancel()

	err := <-errCh
	elapsed := time.Since(start)
	if err == nil || !strings.Contains(err.Error(), "Browser tab's renderer crashed") {
		t.Fatalf("expected omp renderer crashed error, got err=%v in %v", err, elapsed)
	}
	// Product contract: Page.crash must wake the 15s waitFor promptly instead of waiting for the full 15s timeout.
	if elapsed >= 12*time.Second {
		t.Fatalf("crashed tab call waited for 15s waitFor timeout (%v) instead of waking on crash", elapsed)
	}
	// Victim tab must be recycled (no longer registered, does not stall later calls).
	if victim.Alive() {
		t.Fatalf("expected crashed victim tab Alive() == false")
	}
	if got := m.getLiveTab(ctx, tabKey{owner: reqA.OwnerSessionID, name: "victim"}); got != nil {
		t.Fatalf("expected crashed victim tab to be removed from manager")
	}

	// Sibling tab in session A and tab in session B must still work on the shared Chromium process!
	peerRes, err := m.Invoke(ctx, reqA, json.RawMessage(`{"action":"call","name":"peer","chain":[{"method":"text","args":["#ok"]}]}`))
	if err != nil || peerRes.Details.Value != "/peer" {
		t.Fatalf("sibling tab 'peer' broken after victim crash: res=%#v err=%v", peerRes, err)
	}
	otherRes, err := m.Invoke(ctx, reqB, json.RawMessage(`{"action":"call","name":"other","chain":[{"method":"text","args":["#ok"]}]}`))
	if err != nil || otherRes.Details.Value != "/other" {
		t.Fatalf("other session tab 'other' broken after victim crash: res=%#v err=%v", otherRes, err)
	}

	// Reopening "victim" must succeed cleanly.
	reopenP, _ := json.Marshal(map[string]any{"action": "open", "name": "victim", "url": srv.URL + "/reborn", "app": "chromium"})
	if _, err := m.Invoke(ctx, reqA, reopenP); err != nil {
		t.Fatalf("reopening victim after crash failed: %v", err)
	}
	rebornRes, err := m.Invoke(ctx, reqA, json.RawMessage(`{"action":"call","name":"victim","chain":[{"method":"text","args":["#ok"]}]}`))
	if err != nil || rebornRes.Details.Value != "/reborn" {
		t.Fatalf("reborn victim tab read failed: res=%#v err=%v", rebornRes, err)
	}
}

// --- Pass-4/7 (real browser, default 2000 iterations): rapid click->helper loop ---
// Runs with a sibling peer tab ("loop-peer") open on the same Chromium process.
// A Chrome page wedge (dropped reply, ~1/2000-3000 clicks) is bounded by the
// per-op ceiling (matching omp: tab stays open, no DOM.getDocument probe that
// kills busy pages); if a renderer crash occurs, only that tab closes while the
// sibling peer tab stays alive and usable and the loop tab can be reopened.
// Override iteration count via OMP_LOOP_ITERS (default 2000) when running on
// a locked, heavily loaded machine.

func loopIterationCount(t *testing.T) int {
	t.Helper()
	if raw := strings.TrimSpace(os.Getenv("OMP_LOOP_ITERS")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			return n
		}
	}
	return 2000
}

func TestRealPass4_F1_NavigatingClickLoop2000Iterations(t *testing.T) {
	m, _, req := setupRealBrowserTest(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.URL.Path == "/peer" {
			_, _ = io.WriteString(w, `<!doctype html><html><head><title>Peer</title></head><body><div id="peer-ok">PEER-ALIVE</div></body></html>`)
			return
		}
		if r.URL.Path == "/b" {
			_, _ = io.WriteString(w, `<!doctype html><html><head><title>Page B</title></head><body><a id="link" href="/a">To A</a><div id="marker">B</div></body></html>`)
			return
		}
		_, _ = io.WriteString(w, `<!doctype html><html><head><title>Page A</title></head><body><a id="link" href="/b">To B</a><div id="marker">A</div></body></html>`)
	}))
	defer srv.Close()

	iterations := loopIterationCount(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	// Keep a sibling tab open on the same Chromium process to verify isolation
	// whenever a wedge timeout or renderer crash occurs on "loop".
	openPeer, _ := json.Marshal(map[string]any{"action": "open", "name": "loop-peer", "url": srv.URL + "/peer", "app": "chromium"})
	if _, err := m.Invoke(ctx, req, openPeer); err != nil {
		t.Fatalf("open(loop-peer) failed: %v", err)
	}
	verifyPeerAlive := func(iter int) {
		t.Helper()
		pr, perr := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"loop-peer","chain":[{"method":"text","args":["#peer-ok"]}]}`))
		if perr != nil || pr.Details.Value != "PEER-ALIVE" {
			t.Fatalf("iter %d: sibling tab broken after wedge: res=%#v err=%v", iter, pr, perr)
		}
	}

	openOn := func(path string) *cdpTab {
		// Close any kept tab first if reopening after a wedge timeout.
		if m.getLiveTab(ctx, tabKey{owner: req.OwnerSessionID, name: "loop"}) != nil {
			_, _ = m.Invoke(ctx, req, json.RawMessage(`{"action":"close","name":"loop"}`))
		}
		openP, _ := json.Marshal(map[string]any{"action": "open", "name": "loop", "url": srv.URL + path, "app": "chromium"})
		if _, err := m.Invoke(ctx, req, openP); err != nil {
			t.Fatalf("open(%s) failed: %v", path, err)
		}
		mt := m.getLiveTab(ctx, tabKey{owner: req.OwnerSessionID, name: "loop"})
		if mt == nil {
			t.Fatalf("tab 'loop' missing right after open(%s)", path)
		}
		ct, _ := mt.tab.(*cdpTab)
		return ct
	}

	tabRef := openOn("/a")
	dumpTrace := func(iter int) string {
		if tabRef == nil {
			return ""
		}
		tabRef.mu.Lock()
		navReq := tabRef.mainFrameNavigationRequested
		mainF := tabRef.mainFrameID
		iso := tabRef.isolatedWorlds[mainF]
		kit := tabRef.kitInstalled[iso]
		tabRef.mu.Unlock()
		return fmt.Sprintf("\nstate at iter=%d: mainFrame=%q navReq=%v isoWorld=%d kitInstalled=%v crashed=%v closed=%v\n--- CDP trace (last 120) ---\n%s",
			iter, mainF, navReq, iso, kit, tabRef.crashed.Load(), tabRef.closed.Load(), tabRef.CDPTrace(120))
	}

	isBoundedWedgeErr := func(err error) bool {
		if err == nil {
			return false
		}
		msg := err.Error()
		return strings.Contains(msg, "timed out after ") || strings.Contains(msg, "renderer crashed")
	}

	const maxIterBound = 26 * time.Second
	// Chrome 150 page wedges occur at ~1/2000-3000 clicks (see UPSTREAM.md); cap
	// tolerated wedges at iterations/500 + 1 so a regression causing every click
	// or read to time out can never silently pass with wedges=N.
	maxWedges := iterations/500 + 1
	clickLink := json.RawMessage(`{"action":"call","name":"loop","chain":[{"method":"click","args":["#link"]}]}`)
	readMarker := json.RawMessage(`{"action":"call","name":"loop","chain":[{"method":"text","args":["#marker"]}]}`)
	verifyReopenedMarker := func(iter int, wantMarker string) {
		t.Helper()
		mr, merr := m.Invoke(ctx, req, readMarker)
		if merr != nil || mr.Details.Value != wantMarker {
			t.Fatalf("iter %d: reopened tab marker mismatch: want %q, got %#v err=%v", iter, wantMarker, mr, merr)
		}
	}
	wedges := 0
	for i := range iterations {
		curPath := "/a"
		curMarker := "A"
		want := "B"
		if i%2 == 1 {
			curPath = "/b"
			curMarker = "B"
			want = "A"
		}
		iterStart := time.Now()
		if _, err := m.Invoke(ctx, req, clickLink); err != nil {
			elapsed := time.Since(iterStart)
			if isBoundedWedgeErr(err) && elapsed <= maxIterBound && tabRef.proc.client.Alive() {
				wedges++
				if wedges > maxWedges {
					t.Fatalf("iter %d: wedge count %d exceeded cap %d (latest click err: %v)", i, wedges, maxWedges, err)
				}
				t.Logf("Chrome wedge on click at iter %d (bounded in %v): %v", i, elapsed, err)
				verifyPeerAlive(i)
				tabRef = openOn(curPath)
				verifyReopenedMarker(i, curMarker)
				continue
			}
			t.Fatalf("iteration %d click(#link) failed after %v (browserAlive=%v): %v%s", i, elapsed, tabRef.proc.client.Alive(), err, dumpTrace(i))
		}
		res, err := m.Invoke(ctx, req, readMarker)
		if err != nil {
			elapsed := time.Since(iterStart)
			if isBoundedWedgeErr(err) && elapsed <= maxIterBound && tabRef.proc.client.Alive() {
				wedges++
				if wedges > maxWedges {
					t.Fatalf("iter %d: wedge count %d exceeded cap %d (latest text err: %v)", i, wedges, maxWedges, err)
				}
				t.Logf("Chrome wedge on text at iter %d (bounded in %v): %v", i, elapsed, err)
				verifyPeerAlive(i)
				nextPath := "/b"
				if i%2 == 1 {
					nextPath = "/a"
				}
				tabRef = openOn(nextPath)
				verifyReopenedMarker(i, want)
				continue
			}
			t.Fatalf("iteration %d text(#marker) after navigating click failed after %v (browserAlive=%v): %v%s", i, elapsed, tabRef.proc.client.Alive(), err, dumpTrace(i))
		}
		if res.Details.Value != want {
			t.Fatalf("iteration %d expected marker %q, got %#v%s", i, want, res.Details.Value, dumpTrace(i))
		}
	}
	verifyPeerAlive(iterations)
	t.Logf("completed %d navigating-click iterations (wedges=%d, maxWedges=%d)", iterations, wedges, maxWedges)
}
