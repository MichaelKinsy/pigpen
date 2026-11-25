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
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// --- Pass-3 Finding 1 (real browser): navigating click followed immediately by helpers ---

func TestRealPass3_F1_HelpersImmediatelyAfterNavigatingClick(t *testing.T) {
	m, _, req := setupRealBrowserTest(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.URL.Path == "/next" {
			_, _ = io.WriteString(w, `<!doctype html><html><head><title>Next</title></head><body>
  <h1 id="heading">Arrived</h1>
  <button id="danger" onclick="window.__dangerClicked = true">Danger</button>
</body></html>`)
			return
		}
		_, _ = io.WriteString(w, `<!doctype html><html><head><title>Start</title></head><body>
  <a id="link" href="/next">Go Next</a>
</body></html>`)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	// Keep one warm tab open so the 5 iterations exercise rapid navigating clicks
	// rather than 5 cold Chrome launches under CPU load.
	warmP, _ := json.Marshal(map[string]any{"action": "open", "name": "p3f1-warm", "app": "chromium"})
	if _, err := m.Invoke(ctx, req, warmP); err != nil {
		t.Fatalf("warm open: %v", err)
	}
	// Run 5 iterations to ensure no timing-dependent flake after navigating click.
	for i := range 5 {
		tabName := fmt.Sprintf("p3f1-%d", i)
		openP, _ := json.Marshal(map[string]any{"action": "open", "name": tabName, "url": srv.URL, "app": "chromium"})
		if _, err := m.Invoke(ctx, req, openP); err != nil {
			t.Fatalf("iter %d open: %v", i, err)
		}
		// Prime the isolated world cache on the initial document.
		if _, err := m.Invoke(ctx, req, json.RawMessage(fmt.Sprintf(`{"action":"call","name":%q,"chain":[{"method":"count","args":["#link"]}]}`, tabName))); err != nil {
			t.Fatalf("iter %d prime count: %v", i, err)
		}
		// Click link that immediately triggers a full document navigation.
		if _, err := m.Invoke(ctx, req, json.RawMessage(fmt.Sprintf(`{"action":"call","name":%q,"chain":[{"method":"click","args":["#link"]}]}`, tabName))); err != nil {
			t.Fatalf("iter %d navigating click: %v", i, err)
		}
		// Immediately click #danger on the new page with zero sleep.
		if _, err := m.Invoke(ctx, req, json.RawMessage(fmt.Sprintf(`{"action":"call","name":%q,"chain":[{"method":"click","args":["#danger"]}]}`, tabName))); err != nil {
			t.Fatalf("iter %d immediate click(#danger) after navigation failed: %v", i, err)
		}
		hit, err := m.Invoke(ctx, req, json.RawMessage(fmt.Sprintf(`{"action":"call","name":%q,"chain":[{"method":"evaluate","args":["Boolean(window.__dangerClicked)"]}]}`, tabName)))
		if err != nil || hit.Details.Value != true {
			t.Fatalf("iter %d expected #danger clicked, got %#v err=%v", i, hit, err)
		}
		// Also verify one-shot reads (text, count, isVisible, ariaSnapshot) immediately after navigation.
		if _, err := m.Invoke(ctx, req, json.RawMessage(fmt.Sprintf(`{"action":"call","name":%q,"chain":[{"method":"goto","args":[%q]}]}`, tabName, srv.URL))); err != nil {
			t.Fatalf("iter %d goto back: %v", i, err)
		}
		if _, err := m.Invoke(ctx, req, json.RawMessage(fmt.Sprintf(`{"action":"call","name":%q,"chain":[{"method":"click","args":["#link"]}]}`, tabName))); err != nil {
			t.Fatalf("iter %d second navigating click: %v", i, err)
		}
		for _, call := range []string{
			`{"method":"count","args":["#danger"]}`,
			`{"method":"isVisible","args":["#danger"]}`,
			`{"method":"text","args":["#heading"]}`,
			`{"method":"ariaSnapshot","args":[]}`,
		} {
			if _, err := m.Invoke(ctx, req, json.RawMessage(fmt.Sprintf(`{"action":"call","name":%q,"chain":[%s]}`, tabName, call))); err != nil {
				t.Fatalf("iter %d immediate %s after nav failed: %v", i, call, err)
			}
		}
		closeP, _ := json.Marshal(map[string]any{"action": "close", "name": tabName})
		_, _ = m.Invoke(ctx, req, closeP)
	}
}

// --- Pass-3 Finding 2 (real browser): open() navigation timeout keeps loaded tab and reports nav stage ---

func TestRealPass3_F2_OpenNavigationTimeoutKeepsTab(t *testing.T) {
	m, _, req := setupRealBrowserTest(t)
	unblock := make(chan struct{})
	defer close(unblock)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/hang.png" {
			w.Header().Set("Content-Type", "image/png")
			w.WriteHeader(http.StatusOK)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			select {
			case <-unblock:
			case <-r.Context().Done():
			}
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<!doctype html><html><head><title>Partial Loaded</title></head><body>
  <div id="loaded">ready-content</div>
  <img src="/hang.png" alt="hang">
</body></html>`)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	// Warm up browser process first so the open budget is spent on navigation, not cold launch.
	warmP, _ := json.Marshal(map[string]any{"action": "open", "name": "warm", "app": "chromium"})
	if _, err := m.Invoke(ctx, req, warmP); err != nil {
		t.Fatalf("warm open: %v", err)
	}

	// Use a 5s open budget so target attach + Page.enable comfortably finish even
	// under 24 CPU hogs, while /hang.png still triggers NavigationTimeoutError.
	openSlow, _ := json.Marshal(map[string]any{
		"action":  "open",
		"name":    "slow",
		"url":     srv.URL + "/page",
		"app":     "chromium",
		"timeout": 5,
	})
	_, err := m.Invoke(ctx, req, openSlow)
	if err == nil {
		t.Fatalf("expected navigation timeout error on open")
	}
	msg := err.Error()
	if !strings.Contains(msg, "tab.goto(") || !strings.Contains(msg, `Tab "slow" stays open on what loaded`) {
		t.Fatalf("expected NavigationTimeoutError keeping tab open, got: %s", msg)
	}
	if strings.Contains(msg, "while launching or connecting") {
		t.Fatalf("open reported wrong launch/connect stage on nav timeout: %s", msg)
	}

	// The partially loaded tab must still be registered and reachable.
	txtRes, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"slow","chain":[{"method":"text","args":["#loaded"]}]}`))
	if err != nil || txtRes.Details.Value != "ready-content" {
		t.Fatalf("expected kept tab 'slow' to serve loaded content, got %#v err=%v", txtRes, err)
	}
}

// --- Pass-3 Finding 3 (real browser, security): hostile page cannot spoof frame offsets by name/URL or nested frames ---

func TestRealPass3_F3_HostileFrameOffsetsByNameAndNested(t *testing.T) {
	m, _, req := setupRealBrowserTest(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		switch r.URL.Path {
		case "/deep":
			_, _ = io.WriteString(w, `<!doctype html><html><body style="margin:0">
  <button id="fb" style="width:120px;height:40px" onclick="document.getElementById('out').textContent='HIT'">FrameBtn</button>
  <span id="out">no</span>
</body></html>`)
		case "/mid":
			_, _ = io.WriteString(w, `<!doctype html><html><body style="margin:0">
  <iframe id="inner" name="deepframe" src="/deep" style="margin-left:70px;margin-top:60px;width:180px;height:90px;border:4px solid #000;padding:6px"></iframe>
  <script>
    // Hostile middle frame tries to spoof offset measurements in main world.
    Element.prototype.getBoundingClientRect = () => ({ left: 9999, top: 9999, right: 9999, bottom: 9999, width: 0, height: 0, x: 9999, y: 9999 });
    window.getComputedStyle = () => ({ borderLeftWidth: "500px", borderTopWidth: "500px", paddingLeft: "500px", paddingTop: "500px" });
    window.parseFloat = () => 7777;
  </script>
</body></html>`)
		case "/direct":
			_, _ = io.WriteString(w, `<!doctype html><html><body style="margin:0">
  <button id="db" style="width:120px;height:40px" onclick="document.getElementById('dout').textContent='DIRECT-HIT'">DirectBtn</button>
  <span id="dout">no</span>
</body></html>`)
		default:
			_, _ = io.WriteString(w, `<!doctype html><html><body style="margin:0">
  <iframe id="hf" name="hframe" src="/direct" style="margin-left:80px;margin-top:50px;width:180px;height:80px;border:5px solid #000;padding:5px"></iframe>
  <iframe id="outer" name="outerframe" src="/mid" style="margin-left:90px;margin-top:160px;width:300px;height:200px;border:4px solid #000;padding:4px"></iframe>
  <script>
    // Hostile top page hijacks getBoundingClientRect, getComputedStyle, and parseFloat.
    Element.prototype.getBoundingClientRect = () => ({ left: 8888, top: 8888, right: 8888, bottom: 8888, width: 0, height: 0, x: 8888, y: 8888 });
    window.getComputedStyle = () => ({ borderLeftWidth: "400px", borderTopWidth: "400px", paddingLeft: "400px", paddingTop: "400px" });
    window.parseFloat = () => 6666;
  </script>
</body></html>`)
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	openP, _ := json.Marshal(map[string]any{"action": "open", "name": "f3sec", "url": srv.URL, "app": "chromium"})
	if _, err := m.Invoke(ctx, req, openP); err != nil {
		t.Fatal(err)
	}

	// 1. Frame addressed by name ("hframe") on a hostile top page must still hit #db.
	if _, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"f3sec","chain":[{"method":"frame","args":["hframe"]},{"method":"click","args":["#db"]}]}`)); err != nil {
		t.Fatalf("hostile frame by name click failed: %v", err)
	}
	res1, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"f3sec","chain":[{"method":"frame","args":["hframe"]},{"method":"text","args":["#dout"]}]}`))
	if err != nil || res1.Details.Value != "DIRECT-HIT" {
		t.Fatalf("expected DIRECT-HIT via hostile frame name lookup, got %#v err=%v", res1, err)
	}

	// 2. Nested frame ("deepframe") whose top AND middle ancestor pages are hostile must hit #fb.
	if _, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"f3sec","chain":[{"method":"frame","args":["deepframe"]},{"method":"click","args":["#fb"]}]}`)); err != nil {
		t.Fatalf("hostile nested frame click failed: %v", err)
	}
	res2, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"f3sec","chain":[{"method":"frame","args":["deepframe"]},{"method":"text","args":["#out"]}]}`))
	if err != nil || res2.Details.Value != "HIT" {
		t.Fatalf("expected HIT via hostile nested frame, got %#v err=%v", res2, err)
	}
}

// --- Pass-3 Finding 4 (real browser): waitForDownload cancelled immediately on tab close and session end ---

func TestRealPass3_F4_WaitForDownloadCancelledOnCloseAndSessionEnd(t *testing.T) {
	m, _, req := setupRealBrowserTest(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<!doctype html><html><body>idle</body></html>`)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// 1. Tab close cancels waitForDownload immediately.
	open1, _ := json.Marshal(map[string]any{"action": "open", "name": "dl1", "url": srv.URL, "app": "chromium"})
	if _, err := m.Invoke(ctx, req, open1); err != nil {
		t.Fatal(err)
	}
	errCh := make(chan error, 1)
	go func() {
		_, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"dl1","chain":[{"method":"waitForDownload","args":[{"timeout":15000}]}]}`))
		errCh <- err
	}()
	time.Sleep(150 * time.Millisecond)
	close1, _ := json.Marshal(map[string]any{"action": "close", "name": "dl1"})
	if _, err := m.Invoke(ctx, req, close1); err != nil {
		t.Fatal(err)
	}
	err := <-errCh
	if err == nil || !strings.Contains(err.Error(), `Tab "dl1" was closed`) {
		t.Fatalf("expected Tab closed error on waitForDownload, got err=%v", err)
	}

	// 2. Session end cancels waitForDownload immediately.
	open2, _ := json.Marshal(map[string]any{"action": "open", "name": "dl2", "url": srv.URL, "app": "chromium"})
	if _, err := m.Invoke(ctx, req, open2); err != nil {
		t.Fatal(err)
	}
	errCh2 := make(chan error, 1)
	go func() {
		_, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"dl2","chain":[{"method":"waitForDownload","args":[{"timeout":15000}]}]}`))
		errCh2 <- err
	}()
	time.Sleep(150 * time.Millisecond)
	m.ReleaseTabsForOwner(ctx, req.OwnerSessionID, true)
	err2 := <-errCh2
	if err2 == nil || !strings.Contains(err2.Error(), `Tab "dl2" was closed`) {
		t.Fatalf("expected Tab closed error on session end, got err=%v", err2)
	}
}

// --- Pass-3 Finding 6 & notes (real browser): multi-line user errors, press/drag fail-fast ---

func TestRealPass3_F6_MultilineEvaluateErrorAndPressDragFailFast(t *testing.T) {
	m, _, req := setupRealBrowserTest(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<!doctype html><html><body><div id="only">hi</div></body></html>`)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	openP, _ := json.Marshal(map[string]any{"action": "open", "name": "f6", "url": srv.URL, "app": "chromium"})
	if _, err := m.Invoke(ctx, req, openP); err != nil {
		t.Fatal(err)
	}

	// Multi-line user exception in tab.evaluate must keep all lines (stack stripped).
	_, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"f6","chain":[{"method":"evaluate","args":[{"__omp_fn":"() => { throw new Error('line1\\nline2\\nline3'); }"}]}]}`))
	if err == nil {
		t.Fatalf("expected error from throwing evaluate")
	}
	if !strings.Contains(err.Error(), "line1\nline2\nline3") {
		t.Fatalf("expected full multi-line user error, got: %q", err.Error())
	}
	if strings.Contains(err.Error(), "    at ") {
		t.Fatalf("expected stack frames stripped, got: %q", err.Error())
	}

	// press({selector}) with missing selector fails at once (no 2s zero-match wait).
	_, err = m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"f6","chain":[{"method":"press","args":["Enter",{"selector":"#missing-press"}]}]}`))
	if err == nil || !strings.Contains(err.Error(), "No element found for selector") {
		t.Fatalf("expected immediate missing-selector error from press, got %v", err)
	}

	// drag with missing selector fails at once (no 2s zero-match wait).
	_, err = m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"f6","chain":[{"method":"drag","args":["#missing-from","#only"]}]}`))
	if err == nil || !strings.Contains(err.Error(), "Drag from selector did not resolve") {
		t.Fatalf("expected immediate missing-selector error from drag, got %v", err)
	}
}

// --- Pass-3 Finding 5 (unit): stale profile marker requires both owner and browser PIDs dead + SingletonLock ---

func TestPass3_F5_StaleProfileBothPIDsAndProcessAlive(t *testing.T) {
	base := t.TempDir()

	// 1. Dead owner PID, LIVE browser PID -> MUST BE KEPT.
	liveBrowserDir := filepath.Join(base, tempProfilePrefix+"live-browser")
	if err := os.MkdirAll(liveBrowserDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeProfileOwnerMarker(liveBrowserDir, 99999998, os.Getpid())

	// 2. Dead owner + dead browser in marker, LIVE SingletonLock symlink -> MUST BE KEPT.
	liveLockDir := filepath.Join(base, tempProfilePrefix+"live-lock")
	if err := os.MkdirAll(liveLockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeProfileOwnerMarker(liveLockDir, 99999997, 99999996)
	_ = os.Symlink("localhost-"+strconv.Itoa(os.Getpid()), filepath.Join(liveLockDir, "SingletonLock"))

	// 3. Dead owner + dead browser PIDs and no live SingletonLock -> MUST BE REMOVED.
	deadBothDir := filepath.Join(base, tempProfilePrefix+"dead-both")
	if err := os.MkdirAll(deadBothDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeProfileOwnerMarker(deadBothDir, 99999995, 99999994)

	cleanupStaleProfilesIn(base)

	if _, err := os.Stat(liveBrowserDir); err != nil {
		t.Fatalf("profile with live browser PID was wrongly deleted: %v", err)
	}
	if _, err := os.Stat(liveLockDir); err != nil {
		t.Fatalf("profile with live SingletonLock was wrongly deleted: %v", err)
	}
	if _, err := os.Stat(deadBothDir); !os.IsNotExist(err) {
		t.Fatalf("profile with both PIDs dead should be removed, stat err=%v", err)
	}
	// PID 1 (init/launchd) is owned by root (EPERM on Unix) and must count as alive.
	if !processAlive(1) {
		t.Fatalf("expected processAlive(1) == true (EPERM means alive, only ESRCH means dead)")
	}
}

// --- Pass-3 Finding 6 (unit): cleanJSException, ref().evaluate no silent fallback, session-scoped Documentation/Guidance ---

func TestPass3_F6_CleanJSExceptionEvaluateFallbackAndSessionDocs(t *testing.T) {
	// 1. cleanJSException keeps multi-line messages and drops stack frames.
	got := cleanJSException("Error: line1\nline2\n    at foo (http://localhost/app.js:1:2)\n    at bar:2:3")
	if got != "line1\nline2" {
		t.Fatalf("cleanJSException: want %q, got %q", "line1\nline2", got)
	}
	// 2. evaluateOnObjectID fails cleanly instead of falling back to isolated world when DOM.resolveNode fails.
	srv := startFakeCDPServer(t, func(method string, _ json.RawMessage) (any, error) {
		switch method {
		case "Target.createTarget":
			return map[string]any{"targetId": "t1"}, nil
		case "Target.attachToTarget":
			return map[string]any{"sessionId": "s1"}, nil
		case "DOM.describeNode":
			return map[string]any{"node": map[string]any{"backendNodeId": 42}}, nil
		case "DOM.resolveNode":
			return nil, fmt.Errorf("No node with given id found")
		default:
			return map[string]any{}, nil
		}
	})
	b := NewChromiumBackend()
	tab, err := b.Open(context.Background(), OpenOptions{Name: "ev", Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer tab.Close(context.Background(), false)
	ct := tab.(*cdpTab)
	_, err = ct.evaluateOnObjectID(context.Background(), "iso-obj-1", []any{map[string]any{"__omp_fn": "(el) => el.tagName"}})
	if err == nil || !strings.Contains(err.Error(), "element is gone") {
		t.Fatalf("expected element-gone error when main-world resolve fails, got: %v", err)
	}

	// 3. Documentation & Guidance reflect session settings via Setting hook (e.g. remote cdpUrl -> X11 note).
	origSetting := Setting
	t.Cleanup(func() { Setting = origSetting })
	Setting = func(_ sdk.Context, key string) any {
		if key == "browser.cdpUrl" {
			return "http://10.0.0.5:9222"
		}
		if key == "browser.enabled" {
			return true
		}
		return nil
	}
	m := NewManager()
	m.platform = "darwin"
	p := PreludeDefinition(m)
	var zeroCtx sdk.Context
	if !p.Enabled(zeroCtx) {
		t.Fatalf("expected prelude enabled")
	}
	if !strings.Contains(p.DocumentationText(zeroCtx), "X11") && !strings.Contains(p.DocumentationText(zeroCtx), "display") {
		t.Fatalf("expected DocumentationText(ctx) to include X11 guidance for remote cdpUrl")
	}
	if !strings.Contains(p.GuidanceText(zeroCtx), "X11") && !strings.Contains(p.GuidanceText(zeroCtx), "display") {
		t.Fatalf("expected GuidanceText(ctx) to include X11 guidance for remote cdpUrl")
	}
	if !strings.Contains(DocumentationForContext(zeroCtx, m), "X11") && !strings.Contains(DocumentationForContext(zeroCtx, m), "display") {
		t.Fatalf("expected DocumentationForContext to include X11 guidance for remote cdpUrl")
	}
}
