package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// --- Pass-5 Finding 1 (real browser): open without url + adopted tab + about:blank + goto ---

func TestRealPass5_F1_NoURLAndAdoptedTabSelectorHelpers(t *testing.T) {
	m, chrom, req := setupRealBrowserTest(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		switch r.URL.Path {
		case "/preexisting":
			_, _ = io.WriteString(w, `<!doctype html><html><head><title>Preexisting</title></head><body>
  <div id="pre-text">already-here</div>
  <input id="pre-inp" value="init">
  <button id="pre-btn" onclick="window.__preHit = true">ClickMe</button>
</body></html>`)
		case "/after":
			_, _ = io.WriteString(w, `<!doctype html><html><head><title>After</title></head><body>
  <div id="after-text">navigated-ok</div>
  <input id="after-inp">
</body></html>`)
		default:
			_, _ = io.WriteString(w, `<!doctype html><html><body>ok</body></html>`)
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. Open tab WITHOUT url (lands on about:blank whose document predates addScriptToEvaluateOnNewDocument).
	openBlank, _ := json.Marshal(map[string]any{"action": "open", "name": "nourl", "app": "chromium"})
	if _, err := m.Invoke(ctx, req, openBlank); err != nil {
		t.Fatalf("open without url failed: %v", err)
	}
	// Inject DOM onto about:blank via evaluate, then run selector helpers immediately.
	if _, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"nourl","chain":[{"method":"evaluate","args":["(() => { document.body.innerHTML = '<div id=\"b1\">blank-dom</div><input id=\"bi\"><button id=\"bb\" onclick=\"window.__bhit=true\">Go</button>'; })()"]}]}`)); err != nil {
		t.Fatalf("evaluate on about:blank failed: %v", err)
	}
	if r, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"nourl","chain":[{"method":"text","args":["#b1"]}]}`)); err != nil || r.Details.Value != "blank-dom" {
		t.Fatalf("text on no-url about:blank failed: res=%#v err=%v", r, err)
	}
	if r, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"nourl","chain":[{"method":"count","args":["#b1"]}]}`)); err != nil || r.Details.Value != float64(1) {
		t.Fatalf("count on no-url about:blank failed: res=%#v err=%v", r, err)
	}
	if _, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"nourl","chain":[{"method":"fill","args":["#bi","typed-blank"]}]}`)); err != nil {
		t.Fatalf("fill on no-url about:blank failed: %v", err)
	}
	if _, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"nourl","chain":[{"method":"click","args":["#bb"]}]}`)); err != nil {
		t.Fatalf("click on no-url about:blank failed: %v", err)
	}
	if r, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"nourl","chain":[{"method":"evaluate","args":["Boolean(window.__bhit)"]}]}`)); err != nil || r.Details.Value != true {
		t.Fatalf("click did not land on no-url about:blank: res=%#v err=%v", r, err)
	}
	// Navigate from about:blank via goto and verify selector helpers still work.
	if _, err := m.Invoke(ctx, req, json.RawMessage(fmt.Sprintf(`{"action":"call","name":"nourl","chain":[{"method":"goto","args":[%q]}]}`, srv.URL+"/after"))); err != nil {
		t.Fatalf("goto from no-url tab failed: %v", err)
	}
	if r, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"nourl","chain":[{"method":"text","args":["#after-text"]}]}`)); err != nil || r.Details.Value != "navigated-ok" {
		t.Fatalf("text after goto from no-url tab failed: res=%#v err=%v", r, err)
	}

	// 2. Adopt an existing page whose document predates our attach/script registration.
	chrom.mu.Lock()
	var cdpURL string
	var proc *chromiumProcess
	for _, p := range chrom.browsers {
		cdpURL = p.cdpURL
		proc = p
		break
	}
	chrom.mu.Unlock()
	if cdpURL == "" || proc == nil {
		t.Fatal("expected running Chromium CDP endpoint")
	}
	RegisterBackend(string(KindConnected), chrom)

	// Create a raw target directly on the browser already loaded at /preexisting
	// (no Page.addScriptToEvaluateOnNewDocument registered on it yet).
	var createResp struct {
		TargetID string `json:"targetId"`
	}
	if err := proc.client.Send(ctx, "", "Target.createTarget", map[string]any{"url": srv.URL + "/preexisting"}, &createResp); err != nil {
		t.Fatalf("raw Target.createTarget failed: %v", err)
	}
	defer func() {
		_ = proc.client.Send(context.Background(), "", "Target.closeTarget", map[string]any{"targetId": createResp.TargetID}, nil)
	}()
	// Wait briefly for /preexisting to finish loading before adopting.
	time.Sleep(250 * time.Millisecond)

	adoptP, _ := json.Marshal(map[string]any{"action": "open", "name": "adopted", "app": cdpURL, "target": createResp.TargetID})
	if _, err := m.Invoke(ctx, req, adoptP); err != nil {
		t.Fatalf("adopt existing target failed: %v", err)
	}
	if r, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"adopted","chain":[{"method":"text","args":["#pre-text"]}]}`)); err != nil || r.Details.Value != "already-here" {
		t.Fatalf("text on adopted tab failed: res=%#v err=%v", r, err)
	}
	if r, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"adopted","chain":[{"method":"count","args":["#pre-btn"]}]}`)); err != nil || r.Details.Value != float64(1) {
		t.Fatalf("count on adopted tab failed: res=%#v err=%v", r, err)
	}
	if _, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"adopted","chain":[{"method":"fill","args":["#pre-inp","adopted-val"]}]}`)); err != nil {
		t.Fatalf("fill on adopted tab failed: %v", err)
	}
	if _, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"adopted","chain":[{"method":"click","args":["#pre-btn"]}]}`)); err != nil {
		t.Fatalf("click on adopted tab failed: %v", err)
	}
	if r, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"adopted","chain":[{"method":"evaluate","args":["Boolean(window.__preHit)"]}]}`)); err != nil || r.Details.Value != true {
		t.Fatalf("click did not land on adopted tab: res=%#v err=%v", r, err)
	}
	// Navigate adopted tab via goto and verify selector helpers still work.
	if _, err := m.Invoke(ctx, req, json.RawMessage(fmt.Sprintf(`{"action":"call","name":"adopted","chain":[{"method":"goto","args":[%q]}]}`, srv.URL+"/after"))); err != nil {
		t.Fatalf("goto from adopted tab failed: %v", err)
	}
	if r, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"adopted","chain":[{"method":"text","args":["#after-text"]}]}`)); err != nil || r.Details.Value != "navigated-ok" {
		t.Fatalf("text after goto from adopted tab failed: res=%#v err=%v", r, err)
	}
}

// --- Pass-5 Finding 2 (real browser): 100 tries of script-chained navigation (/hop -> /hop2 -> /b) ---

func TestRealPass5_F2_ScriptChainedNavigation100Tries(t *testing.T) {
	m, _, req := setupRealBrowserTest(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		switch r.URL.Path {
		case "/hop":
			_, _ = io.WriteString(w, `<!doctype html><html><body><script>location.href = "/hop2";</script></body></html>`)
		case "/hop2":
			_, _ = io.WriteString(w, `<!doctype html><html><body><script>location.href = "/b";</script></body></html>`)
		case "/b":
			_, _ = io.WriteString(w, `<!doctype html><html><body><div id="bp">landed-b</div><a id="go" href="/hop">Hop</a></body></html>`)
		default:
			_, _ = io.WriteString(w, `<!doctype html><html><body><a id="go" href="/hop">Hop</a></body></html>`)
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	openP, _ := json.Marshal(map[string]any{"action": "open", "name": "chain", "url": srv.URL, "app": "chromium"})
	if _, err := m.Invoke(ctx, req, openP); err != nil {
		t.Fatal(err)
	}
	clickGo := json.RawMessage(`{"action":"call","name":"chain","chain":[{"method":"click","args":["#go"]}]}`)
	readBP := json.RawMessage(`{"action":"call","name":"chain","chain":[{"method":"text","args":["#bp"]}]}`)
	for i := range 100 {
		if _, err := m.Invoke(ctx, req, clickGo); err != nil {
			t.Fatalf("iter %d click(#go) failed: %v", i, err)
		}
		res, err := m.Invoke(ctx, req, readBP)
		if err != nil {
			t.Fatalf("iter %d text(#bp) after script-chained nav failed: %v", i, err)
		}
		// If text(#bp) landed on intermediate /hop2 before it hopped, wait for /b via waitForSelector
		// is not needed when our multi-hop nav retry caught it; fail if it ever returns a context error.
		if res.Details.Value != "landed-b" {
			// Verify #bp is reachable without error if /hop2 script ran asynchronously after load.
			if _, werr := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"chain","chain":[{"method":"waitFor","args":["#bp"]}]}`)); werr != nil {
				t.Fatalf("iter %d waitFor(#bp) after script-chained nav failed: %v", i, werr)
			}
			res2, err2 := m.Invoke(ctx, req, readBP)
			if err2 != nil || res2.Details.Value != "landed-b" {
				t.Fatalf("iter %d expected landed-b, got %#v err=%v", i, res2, err2)
			}
		}
	}
}

// --- Pass-5 Finding 4 (real browser): whole-call evaluate timeout keeps page & JS state intact ---

func TestRealPass5_F4_WholeCallTimeoutKeepsPageAndJSState(t *testing.T) {
	m, _, req := setupRealBrowserTest(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<!doctype html><html><body><div id="keep">alive</div></body></html>`)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	openP, _ := json.Marshal(map[string]any{"action": "open", "name": "keep", "url": srv.URL, "app": "chromium"})
	if _, err := m.Invoke(ctx, req, openP); err != nil {
		t.Fatal(err)
	}
	// Set page JS state window.x = 1.
	if _, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"keep","chain":[{"method":"evaluate","args":["window.x = 1"]}]}`)); err != nil {
		t.Fatal(err)
	}
	// Run a never-settling evaluate with timeout: 2.
	_, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"keep","timeout":2,"chain":[{"method":"evaluate","args":["new Promise(() => {})"]}]}`))
	if err == nil || !strings.Contains(err.Error(), "Browser code execution timed out after 2000ms") {
		t.Fatalf("expected whole-call timeout error, got %v", err)
	}
	if !strings.Contains(err.Error(), "reattached") || !strings.Contains(err.Error(), "Tab state set since it was opened was reset") {
		t.Fatalf("expected reattach message with omp RECYCLE_RESETS note, got: %v", err)
	}
	// Tab must still be usable AND window.x must still equal 1 (page was not closed or reloaded).
	r, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"keep","chain":[{"method":"evaluate","args":["window.x"]}]}`))
	if err != nil || r.Details.Value != float64(1) {
		t.Fatalf("expected window.x == 1 after whole-call timeout, got %#v err=%v", r, err)
	}
	txt, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"keep","chain":[{"method":"text","args":["#keep"]}]}`))
	if err != nil || txt.Details.Value != "alive" {
		t.Fatalf("expected selector helper working after reattach, got %#v err=%v", txt, err)
	}
}

// --- Pass-5 Finding 5 (real browser): killing browser process reports disconnect at once (not 15s selector timeout) ---

func TestRealPass5_F5_BrowserProcessDeathReportedImmediately(t *testing.T) {
	m, chrom, req := setupRealBrowserTest(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<!doctype html><html><body>ok</body></html>`)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	openP, _ := json.Marshal(map[string]any{"action": "open", "name": "die", "url": srv.URL, "app": "chromium"})
	if _, err := m.Invoke(ctx, req, openP); err != nil {
		t.Fatal(err)
	}
	chrom.mu.Lock()
	var pid int
	var profDir string
	for _, p := range chrom.browsers {
		if p.launched != nil {
			pid = p.launched.pid
			profDir = p.launched.userDataDir
		}
	}
	chrom.mu.Unlock()
	if pid <= 0 {
		t.Fatal("expected launched browser pid")
	}

	errCh := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"die","chain":[{"method":"waitFor","args":["#never",{"timeout":15000}]}]}`))
		errCh <- err
	}()
	time.Sleep(250 * time.Millisecond)

	// Kill ONLY our own launched test browser PID (cross-platform: works on Unix and Windows).
	if proc, ferr := os.FindProcess(pid); ferr == nil && proc != nil {
		_ = proc.Kill()
	}
	err := <-errCh
	elapsed := time.Since(start)
	if err == nil || !strings.Contains(err.Error(), "Browser process exited") {
		t.Fatalf("expected immediate browser-exited error, got err=%v in %v", err, elapsed)
	}
	// Product contract: browser process death must fail promptly instead of waiting for the 15s waitFor timeout.
	if elapsed >= 12*time.Second {
		t.Fatalf("browser death waited for full waitFor timeout (%v) instead of failing on disconnect", elapsed)
	}
	// Profile dir should be reaped promptly even after SIGKILL of the browser process.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, serr := os.Stat(profDir); os.IsNotExist(serr) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, serr := os.Stat(profDir); !os.IsNotExist(serr) {
		t.Fatalf("expected temp profile %q cleaned after browser SIGKILL, stat err=%v", profDir, serr)
	}
}

// --- Pass-5 Finding 6 (real browser): target=_blank click must NOT make subsequent clicks take 5s ---

func TestRealPass5_F6_TargetBlankDoesNotStallOriginalTabClicks(t *testing.T) {
	m, _, req := setupRealBrowserTest(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.URL.Path == "/popup" {
			_, _ = io.WriteString(w, `<!doctype html><html><body>popup</body></html>`)
			return
		}
		_, _ = io.WriteString(w, `<!doctype html><html><body>
  <a id="lblank" href="/popup" target="_blank">Open Blank</a>
  <button id="present" onclick="window.__hits = (window.__hits || 0) + 1">Hit</button>
</body></html>`)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	openP, _ := json.Marshal(map[string]any{"action": "open", "name": "orig", "url": srv.URL, "app": "chromium"})
	if _, err := m.Invoke(ctx, req, openP); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"orig","chain":[{"method":"click","args":["#lblank"]}]}`)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)

	// 3 consecutive clicks on the original tab must each finish well below Chrome's 5s mouseMoved stall.
	for i := range 3 {
		start := time.Now()
		if _, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"orig","chain":[{"method":"click","args":["#present"]}]}`)); err != nil {
			t.Fatalf("click %d after target=_blank failed: %v", i, err)
		}
		elapsed := time.Since(start)
		if elapsed >= 4500*time.Millisecond {
			t.Fatalf("click %d after target=_blank hit Chrome's 5s mouseMoved stall (%v)", i, elapsed)
		}
	}
	r, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"orig","chain":[{"method":"evaluate","args":["window.__hits"]}]}`))
	if err != nil || r.Details.Value != float64(3) {
		t.Fatalf("expected 3 hits on original tab, got %#v err=%v", r, err)
	}
}

// --- Pass-5/6 (real browser): slow navigation (>5s) is NOT cancelled at barrier bound; hanging link stays bounded ---

func TestRealPass5_F7_SlowNavigationArrivesAndHangingLinkBounded(t *testing.T) {
	m, _, req := setupRealBrowserTest(t)
	unblock := make(chan struct{})
	var slowAborted bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/slow7":
			select {
			case <-time.After(7 * time.Second):
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = io.WriteString(w, `<!doctype html><html><head><title>Slow7</title></head><body><div id="sl">arrived-7s</div></body></html>`)
			case <-r.Context().Done():
				slowAborted = true
			}
		case "/never":
			select {
			case <-unblock:
			case <-r.Context().Done():
			}
		default:
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, `<!doctype html><html><body>
  <a id="slow" href="/slow7">Slow 7s</a>
  <a id="hang" href="/never">Hang</a>
  <div id="stay">still-here</div>
</body></html>`)
		}
	}))
	defer srv.Close()
	defer close(unblock)

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	// 1. Slow 7s link + waitFor("#sl", {timeout: 15000}) must NOT cancel the load at 4-5s; page must arrive.
	open1, _ := json.Marshal(map[string]any{"action": "open", "name": "slowwait", "url": srv.URL, "app": "chromium"})
	if _, err := m.Invoke(ctx, req, open1); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"slowwait","chain":[{"method":"click","args":["#slow"]}]}`)); err != nil {
		t.Fatal(err)
	}
	wRes, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"slowwait","timeout":20,"chain":[{"method":"waitFor","args":["#sl",{"timeout":15000}]}]}`))
	if err != nil || wRes.Details.Value != true {
		t.Fatalf("expected waitFor(#sl, 15s) to succeed when 7s page arrives, got res=%#v err=%v", wRes, err)
	}
	if slowAborted {
		t.Fatalf("server saw /slow7 request aborted during waitFor")
	}
	txtRes, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"slowwait","chain":[{"method":"text","args":["#sl"]}]}`))
	if err != nil || txtRes.Details.Value != "arrived-7s" {
		t.Fatalf("expected #sl text 'arrived-7s', got %#v err=%v", txtRes, err)
	}

	// 2. Slow 7s link + immediate text("#sl"): barrier clears at 5s without stopping the load
	// (matching omp tab-worker.ts:3022), so /slow7 is NOT cancelled and still arrives at 7s.
	open2, _ := json.Marshal(map[string]any{"action": "open", "name": "slowtext", "url": srv.URL, "app": "chromium"})
	if _, err := m.Invoke(ctx, req, open2); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"slowtext","chain":[{"method":"click","args":["#slow"]}]}`)); err != nil {
		t.Fatal(err)
	}
	// text("#sl") returns once barrier clears at 5s without cancelling the load.
	if _, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"slowtext","chain":[{"method":"text","args":["#sl"]}]}`)); err != nil {
		t.Fatalf("expected text(#sl) not to abort /slow7 with pending-navigation error, got: %v", err)
	}
	// Verify /slow7 still arrives naturally and was NOT cancelled.
	if _, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"slowtext","timeout":15,"chain":[{"method":"waitFor","args":["#sl",{"timeout":10000}]}]}`)); err != nil {
		t.Fatalf("expected /slow7 to finish arriving after text(), got %v", err)
	}
	if slowAborted {
		t.Fatalf("server saw /slow7 request aborted after text()")
	}

	// 3. Truly hanging link (/never) followed by waitFor("#sl", {timeout: 3000}) stays bounded by waitFor's timeout.
	open3, _ := json.Marshal(map[string]any{"action": "open", "name": "hangtab", "url": srv.URL, "app": "chromium"})
	if _, err := m.Invoke(ctx, req, open3); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"hangtab","chain":[{"method":"click","args":["#hang"]}]}`)); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"hangtab","timeout":10,"chain":[{"method":"waitFor","args":["#sl",{"timeout":3000}]}]}`))
	elapsed := time.Since(start)
	if err == nil || !strings.Contains(err.Error(), "timed out after 3000ms") {
		t.Fatalf("expected waitFor 3000ms timeout on hanging link, got err=%v in %v", err, elapsed)
	}
}

// --- Pass-5/7 acceptance: default 2000-iteration navigating-click loop starting from a no-URL tab ---

func TestRealPass5_F1_NoURLTabNavigatingClickLoop2000Iterations(t *testing.T) {
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

	openPeer, _ := json.Marshal(map[string]any{"action": "open", "name": "nourl-peer", "url": srv.URL + "/peer", "app": "chromium"})
	if _, err := m.Invoke(ctx, req, openPeer); err != nil {
		t.Fatalf("open(nourl-peer) failed: %v", err)
	}
	verifyPeerAlive := func(iter int) {
		t.Helper()
		pr, perr := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"nourl-peer","chain":[{"method":"text","args":["#peer-ok"]}]}`))
		if perr != nil || pr.Details.Value != "PEER-ALIVE" {
			t.Fatalf("iter %d: sibling tab broken after wedge: res=%#v err=%v", iter, pr, perr)
		}
	}

	openAndGoto := func(path string) *cdpTab {
		if m.getLiveTab(ctx, tabKey{owner: req.OwnerSessionID, name: "nourl-loop"}) != nil {
			_, _ = m.Invoke(ctx, req, json.RawMessage(`{"action":"close","name":"nourl-loop"}`))
		}
		openBlank, _ := json.Marshal(map[string]any{"action": "open", "name": "nourl-loop", "app": "chromium"})
		if _, err := m.Invoke(ctx, req, openBlank); err != nil {
			t.Fatal(err)
		}
		if _, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"nourl-loop","chain":[{"method":"count","args":["body"]}]}`)); err != nil {
			t.Fatalf("count on initial no-url tab failed: %v", err)
		}
		if _, err := m.Invoke(ctx, req, json.RawMessage(fmt.Sprintf(`{"action":"call","name":"nourl-loop","chain":[{"method":"goto","args":[%q]}]}`, srv.URL+path))); err != nil {
			t.Fatalf("goto(%s) from no-url tab failed: %v", path, err)
		}
		mt := m.getLiveTab(ctx, tabKey{owner: req.OwnerSessionID, name: "nourl-loop"})
		if mt == nil {
			t.Fatal("tab nourl-loop missing after open")
		}
		ct, _ := mt.tab.(*cdpTab)
		return ct
	}

	tabRef := openAndGoto("/a")
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
	clickLink := json.RawMessage(`{"action":"call","name":"nourl-loop","chain":[{"method":"click","args":["#link"]}]}`)
	readMarker := json.RawMessage(`{"action":"call","name":"nourl-loop","chain":[{"method":"text","args":["#marker"]}]}`)
	verifyReopenedMarker := func(iter int, wantMarker string) {
		t.Helper()
		mr, merr := m.Invoke(ctx, req, readMarker)
		if merr != nil || mr.Details.Value != wantMarker {
			t.Fatalf("iter %d: reopened no-url tab marker mismatch: want %q, got %#v err=%v", iter, wantMarker, mr, merr)
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
				tabRef = openAndGoto(curPath)
				verifyReopenedMarker(i, curMarker)
				continue
			}
			t.Fatalf("iteration %d click(#link) failed after %v (browserAlive=%v): %v", i, elapsed, tabRef.proc.client.Alive(), err)
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
				tabRef = openAndGoto(nextPath)
				verifyReopenedMarker(i, want)
				continue
			}
			t.Fatalf("iteration %d text(#marker) after navigating click failed after %v (browserAlive=%v): %v", i, elapsed, tabRef.proc.client.Alive(), err)
		}
		if res.Details.Value != want {
			t.Fatalf("iteration %d expected marker %q, got %#v", i, want, res.Details.Value)
		}
	}
	verifyPeerAlive(iterations)
	t.Logf("completed %d no-url navigating-click iterations (wedges=%d, maxWedges=%d)", iterations, wedges, maxWedges)
}
