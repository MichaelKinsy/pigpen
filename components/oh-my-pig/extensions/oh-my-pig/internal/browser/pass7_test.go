package browser

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- Pass-7 Finding 1 (unit): child-target Fetch.enable fail-closed & ordering ---

func TestPass7_F1_ChildTargetAutoAttachFailClosedAndOrdering(t *testing.T) {
	var mu sync.Mutex
	var events []string
	var failIframeFetch atomic.Bool

	srv := startFakeCDPServerWithSession(t, func(sessionID, method string, params json.RawMessage) (any, error) {
		switch method {
		case "Target.createTarget":
			return map[string]any{"targetId": "t1"}, nil
		case "Target.attachToTarget":
			return map[string]any{"sessionId": "s1"}, nil
		case "Page.getFrameTree":
			return map[string]any{"frameTree": map[string]any{"frame": map[string]any{"id": "main-f", "url": "http://127.0.0.1/"}}}, nil
		case "Fetch.enable":
			if failIframeFetch.Load() && sessionID != "s1" {
				mu.Lock()
				events = append(events, sessionID+":Fetch.enable:ERR")
				mu.Unlock()
				return nil, errors.New("Fetch.enable refused on child")
			}
			mu.Lock()
			events = append(events, sessionID+":Fetch.enable:OK")
			mu.Unlock()
			return map[string]any{}, nil
		case "Target.setAutoAttach":
			mu.Lock()
			events = append(events, sessionID+":Target.setAutoAttach")
			mu.Unlock()
			return map[string]any{}, nil
		case "Runtime.runIfWaitingForDebugger":
			mu.Lock()
			events = append(events, sessionID+":Runtime.runIfWaitingForDebugger")
			mu.Unlock()
			return map[string]any{}, nil
		case "Target.detachFromTarget":
			mu.Lock()
			events = append(events, sessionID+":Target.detachFromTarget")
			mu.Unlock()
			return map[string]any{}, nil
		default:
			return map[string]any{}, nil
		}
	})

	b := NewChromiumBackend()
	tab, err := b.Open(context.Background(), OpenOptions{
		Name:           "oopif-unit",
		Endpoint:       srv.URL,
		AllowedDomains: []string{"127.0.0.1"},
	})
	if err != nil {
		t.Fatalf("open failed: %v", err)
	}
	defer tab.Close(context.Background(), false)
	ct := tab.(*cdpTab)

	// 1. Healthy iframe child: Fetch.enable + Target.setAutoAttach run on child-ok BEFORE Runtime.runIfWaitingForDebugger.
	mu.Lock()
	events = nil
	mu.Unlock()
	ct.onCDPEvent(CDPEvent{
		SessionID: ct.sessionID,
		Method:    "Target.attachedToTarget",
		Params:    json.RawMessage(`{"sessionId":"child-ok","targetInfo":{"targetId":"if1","type":"iframe","url":"http://a.localhost/"},"waitingForDebugger":true}`),
	})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(events)
		mu.Unlock()
		if n >= 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	gotOK := append([]string(nil), events...)
	mu.Unlock()
	wantOK := []string{"child-ok:Fetch.enable:OK", "child-ok:Target.setAutoAttach", "child-ok:Runtime.runIfWaitingForDebugger"}
	if strings.Join(gotOK, ",") != strings.Join(wantOK, ",") {
		t.Fatalf("iframe child setup order/session mismatch: got %v, want %v", gotOK, wantOK)
	}

	// 2. Fail-closed iframe child: if Fetch.enable fails on child-fail,
	// Runtime.runIfWaitingForDebugger MUST NOT run, AND tab.errors() must record
	// the child URL and failure so the model can see why the frame is held.
	mu.Lock()
	events = nil
	mu.Unlock()
	failIframeFetch.Store(true)
	ct.onCDPEvent(CDPEvent{
		SessionID: ct.sessionID,
		Method:    "Target.attachedToTarget",
		Params:    json.RawMessage(`{"sessionId":"child-fail","targetInfo":{"targetId":"if2","type":"iframe","url":"http://a.localhost/held-frame"},"waitingForDebugger":true}`),
	})
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		res := ct.queryErrors(nil)
		if m, ok := res.Value.(map[string]any); ok {
			if entries, ok := m["entries"].([]cdpErrorEntry); ok && len(entries) > 0 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	gotFail := append([]string(nil), events...)
	mu.Unlock()
	for _, ev := range gotFail {
		if strings.Contains(ev, "Runtime.runIfWaitingForDebugger") {
			t.Fatalf("iframe child resumed despite Fetch.enable failing (must fail closed): %v", gotFail)
		}
	}
	if len(gotFail) == 0 || gotFail[0] != "child-fail:Fetch.enable:ERR" {
		t.Fatalf("expected child-fail:Fetch.enable:ERR on failed child, got %v", gotFail)
	}
	errRes := ct.queryErrors(nil)
	errMap, _ := errRes.Value.(map[string]any)
	entries, _ := errMap["entries"].([]cdpErrorEntry)
	if len(entries) == 0 || !strings.Contains(entries[0].Text, "http://a.localhost/held-frame") || !strings.Contains(entries[0].Text, "Fetch.enable") {
		t.Fatalf("expected fail-closed child error in tab.errors() naming URL and Fetch.enable, got %#v", entries)
	}
}

// --- Pass-8 Finding 5 (unit): nested child sessions are pruned on parent detach & across 20 navigations ---

func TestPass8_F5_NestedChildSessionsCleanedUpAcrossNavigations(t *testing.T) {
	srv := startFakeCDPServerWithSession(t, func(_, method string, _ json.RawMessage) (any, error) {
		switch method {
		case "Target.createTarget":
			return map[string]any{"targetId": "t1"}, nil
		case "Target.attachToTarget":
			return map[string]any{"sessionId": "s1"}, nil
		case "Page.getFrameTree":
			return map[string]any{"frameTree": map[string]any{"frame": map[string]any{"id": "main-f", "url": "http://127.0.0.1/"}}}, nil
		default:
			return map[string]any{}, nil
		}
	})
	b := NewChromiumBackend()
	tab, err := b.Open(context.Background(), OpenOptions{
		Name:           "nested-clean",
		Endpoint:       srv.URL,
		AllowedDomains: []string{"127.0.0.1", "a.localhost", "c.localhost"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer tab.Close(context.Background(), false)
	ct := tab.(*cdpTab)

	// 1. Subtree detach: attach top OOPIF -> nested OOPIF + worker -> nested worker,
	// then emit Target.detachedFromTarget ONLY for the top OOPIF (as Chrome does).
	// All 4 entries must be dropped.
	ct.onCDPEvent(CDPEvent{SessionID: "s1", Method: "Target.attachedToTarget", Params: json.RawMessage(`{"sessionId":"if-top","targetInfo":{"targetId":"t-if1","type":"iframe","url":"http://a.localhost/"},"waitingForDebugger":false}`)})
	ct.onCDPEvent(CDPEvent{SessionID: "if-top", Method: "Target.attachedToTarget", Params: json.RawMessage(`{"sessionId":"w-top","targetInfo":{"targetId":"t-w1","type":"worker","url":"http://a.localhost/w.js"},"waitingForDebugger":false}`)})
	ct.onCDPEvent(CDPEvent{SessionID: "if-top", Method: "Target.attachedToTarget", Params: json.RawMessage(`{"sessionId":"if-nested","targetInfo":{"targetId":"t-if2","type":"iframe","url":"http://c.localhost/"},"waitingForDebugger":false}`)})
	ct.onCDPEvent(CDPEvent{SessionID: "if-nested", Method: "Target.attachedToTarget", Params: json.RawMessage(`{"sessionId":"w-nested","targetInfo":{"targetId":"t-w2","type":"worker","url":"http://c.localhost/w2.js"},"waitingForDebugger":false}`)})

	ct.mu.Lock()
	before := len(ct.childSessions)
	ct.mu.Unlock()
	if before != 4 {
		t.Fatalf("expected 4 child sessions before detach, got %d", before)
	}

	ct.onCDPEvent(CDPEvent{SessionID: "s1", Method: "Target.detachedFromTarget", Params: json.RawMessage(`{"sessionId":"if-top"}`)})
	ct.mu.Lock()
	afterDetach := len(ct.childSessions)
	afterFetch := len(ct.fetchSessions)
	afterParent := len(ct.childParent)
	ct.mu.Unlock()
	if afterDetach != 0 || afterParent != 0 || afterFetch != 1 {
		t.Fatalf("expected subtree pruned (child=0 parent=0 fetch=1), got child=%d parent=%d fetch=%d", afterDetach, afterParent, afterFetch)
	}

	// 2. Across 20 main-frame navigations with nested OOPIFs + workers (even if
	// Chrome omits some detach events), map sizes remain bounded and stable.
	for i := range 20 {
		topID := fmt.Sprintf("nav%d-if1", i)
		w1ID := fmt.Sprintf("nav%d-w1", i)
		subID := fmt.Sprintf("nav%d-if2", i)
		w2ID := fmt.Sprintf("nav%d-w2", i)
		ct.onCDPEvent(CDPEvent{SessionID: "s1", Method: "Page.frameNavigated", Params: json.RawMessage(fmt.Sprintf(`{"frame":{"id":"main-f","url":"http://127.0.0.1/p%d"}}`, i))})
		ct.onCDPEvent(CDPEvent{SessionID: "s1", Method: "Target.attachedToTarget", Params: json.RawMessage(fmt.Sprintf(`{"sessionId":%q,"targetInfo":{"targetId":"t1","type":"iframe","url":"http://a.localhost/"},"waitingForDebugger":false}`, topID))})
		ct.onCDPEvent(CDPEvent{SessionID: topID, Method: "Target.attachedToTarget", Params: json.RawMessage(fmt.Sprintf(`{"sessionId":%q,"targetInfo":{"targetId":"t2","type":"worker","url":"http://a.localhost/w.js"},"waitingForDebugger":false}`, w1ID))})
		ct.onCDPEvent(CDPEvent{SessionID: topID, Method: "Target.attachedToTarget", Params: json.RawMessage(fmt.Sprintf(`{"sessionId":%q,"targetInfo":{"targetId":"t3","type":"iframe","url":"http://c.localhost/"},"waitingForDebugger":false}`, subID))})
		ct.onCDPEvent(CDPEvent{SessionID: subID, Method: "Target.attachedToTarget", Params: json.RawMessage(fmt.Sprintf(`{"sessionId":%q,"targetInfo":{"targetId":"t4","type":"worker","url":"http://c.localhost/w2.js"},"waitingForDebugger":false}`, w2ID))})
	}
	ct.mu.Lock()
	finalChild := len(ct.childSessions)
	finalParent := len(ct.childParent)
	finalFetch := len(ct.fetchSessions)
	ct.mu.Unlock()
	if finalChild != 4 || finalParent != 4 || finalFetch != 5 {
		t.Fatalf("expected stable map sizes after 20 navigations (child=4 parent=4 fetch=5), got child=%d parent=%d fetch=%d", finalChild, finalParent, finalFetch)
	}
}

// --- Pass-7 Finding 6 (unit): omp timeout labels, hints, and failed-fast wording ---

func TestPass7_F6_TimeoutLabelsHintsAndWording(t *testing.T) {
	// 1. opLabelAndSelector matches omp's labels verbatim.
	cases := []struct {
		prefix, method string
		args           []any
		wantLabel      string
		wantSel        string
	}{
		{"tab.", "click", []any{"#submit"}, `tab.click("#submit")`, "#submit"},
		{"tab.", "fill", []any{"#email", "a@b.c"}, `tab.fill("#email")`, "#email"},
		{"tab.", "text", []any{"#msg"}, `tab.text("#msg")`, "#msg"},
		{"tab.", "text", nil, `tab.text("body")`, "body"},
		{"tab.", "attr", []any{"#link", "href"}, `tab.attr("#link", "href")`, "#link"},
		{"tab.", "screenshot", nil, `tab.screenshot()`, ""},
		{"tab.", "screenshot", []any{map[string]any{"fullPage": true}}, `tab.screenshot({ fullPage: true })`, ""},
		{"tab.", "screenshot", []any{map[string]any{"selector": "#hero"}}, `tab.screenshot({ selector: "#hero" })`, ""},
		{"tab.", "ariaSnapshot", []any{"#main"}, `tab.ariaSnapshot("#main")`, ""},
		{"tab.", "ariaSnapshot", nil, `tab.ariaSnapshot()`, ""},
		{"frame.", "click", []any{"#btn"}, `frame.click("#btn")`, "#btn"},
	}
	for _, tc := range cases {
		gotLabel, gotSel := opLabelAndSelector(tc.prefix, tc.method, tc.args)
		if gotLabel != tc.wantLabel || gotSel != tc.wantSel {
			t.Fatalf("opLabelAndSelector(%q,%q,%v) = (%q,%q), want (%q,%q)",
				tc.prefix, tc.method, tc.args, gotLabel, gotSel, tc.wantLabel, tc.wantSel)
		}
	}

	// 2. All perOpTimeoutError hint branches against a fake CDP server.
	var matchCount atomic.Int32
	var stallCountProbe atomic.Bool
	var failCountProbe atomic.Bool
	srv := startFakeCDPServer(t, func(method string, params json.RawMessage) (any, error) {
		switch method {
		case "Target.createTarget":
			return map[string]any{"targetId": "t1"}, nil
		case "Target.attachToTarget":
			return map[string]any{"sessionId": "s1"}, nil
		case "Page.getFrameTree":
			return map[string]any{"frameTree": map[string]any{"frame": map[string]any{"id": "main-f", "url": "http://example.test/"}}}, nil
		case "Page.createIsolatedWorld":
			return map[string]any{"executionContextId": 99}, nil
		case "Runtime.evaluate":
			var p struct {
				Expression string `json:"expression"`
			}
			_ = json.Unmarshal(params, &p)
			if strings.Contains(p.Expression, "queryAll") {
				if stallCountProbe.Load() {
					time.Sleep(1500 * time.Millisecond)
				}
				if failCountProbe.Load() {
					return nil, errors.New("frame detached\nstack line")
				}
				return map[string]any{"result": map[string]any{"type": "number", "value": float64(matchCount.Load())}}, nil
			}
			return map[string]any{"result": map[string]any{"type": "string", "value": ""}}, nil
		default:
			return map[string]any{}, nil
		}
	})
	b := NewChromiumBackend()
	tab, err := b.Open(context.Background(), OpenOptions{Name: "hints", Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer tab.Close(context.Background(), false)
	ct := tab.(*cdpTab)

	// (a) 0 matches -> formatSelectorMatchHint(0)
	matchCount.Store(0)
	got := ct.perOpTimeoutError(`tab.click("#missing")`, "#missing", 8000, context.DeadlineExceeded, frameTarget{}).Error()
	want := `tab.click("#missing") timed out after 8000ms; selector currently matches no elements — run tab.observe() or tab.ariaSnapshot() to inspect the page`
	if got != want {
		t.Fatalf("0-match hint:\n got: %s\nwant: %s", got, want)
	}

	// (b) N>0 matches -> formatSelectorMatchHint(N)
	matchCount.Store(3)
	got = ct.perOpTimeoutError(`tab.fill("#inp")`, "#inp", 8000, context.DeadlineExceeded, frameTarget{}).Error()
	want = `tab.fill("#inp") timed out after 8000ms; selector currently matches 3 element(s) but the action never became possible — the element may be hidden or covered (try tab.scrollIntoView() or a more specific selector)`
	if got != want {
		t.Fatalf("N-match hint:\n got: %s\nwant: %s", got, want)
	}

	// (c) ClickRefusedError + count probe -> `; the element never became clickable (last check: ...; selector matches N element(s))`
	matchCount.Store(2)
	got = ct.perOpTimeoutError(`tab.click("#btn")`, "#btn", 8000, &clickRefusedError{refusal: "covered by <div#modal>"}, frameTarget{}).Error()
	want = `tab.click("#btn") timed out after 8000ms; the element never became clickable (last check: covered by <div#modal>; selector matches 2 element(s))`
	if got != want {
		t.Fatalf("refusal+count hint:\n got: %s\nwant: %s", got, want)
	}

	// (d) ClickRefusedError with aria-ref selector (no count probe)
	got = ct.perOpTimeoutError(`tab.click("aria-ref=e5")`, "aria-ref=e5", 8000, &clickRefusedError{refusal: "zero-size"}, frameTarget{}).Error()
	want = `tab.click("aria-ref=e5") timed out after 8000ms; the element never became clickable (last check: zero-size)`
	if got != want {
		t.Fatalf("refusal aria-ref hint:\n got: %s\nwant: %s", got, want)
	}

	// (e) TypingInterruptedError (no count probe)
	got = ct.perOpTimeoutError(`tab.type("#q")`, "#q", 8000, &typingInterruptedError{progress: "typed 4 of 10 characters before timeout"}, frameTarget{}).Error()
	want = `tab.type("#q") timed out after 8000ms; typed 4 of 10 characters before timeout`
	if got != want {
		t.Fatalf("typing interrupted hint:\n got: %s\nwant: %s", got, want)
	}

	// (f) Count probe error (first line only)
	failCountProbe.Store(true)
	got = ct.perOpTimeoutError(`tab.click("#x")`, "#x", 8000, context.DeadlineExceeded, frameTarget{}).Error()
	want = `tab.click("#x") timed out after 8000ms; could not count matches: CDP error -32000: frame detached`
	if got != want {
		t.Fatalf("count probe error hint:\n got: %s\nwant: %s", got, want)
	}
	failCountProbe.Store(false)

	// (g) Count probe timeout -> no answer within 1000ms
	stallCountProbe.Store(true)
	got = ct.perOpTimeoutError(`tab.click("#x")`, "#x", 8000, context.DeadlineExceeded, frameTarget{}).Error()
	want = `tab.click("#x") timed out after 8000ms; could not count matches: no answer within 1000ms`
	if got != want {
		t.Fatalf("count probe timeout hint:\n got: %s\nwant: %s", got, want)
	}
	stallCountProbe.Store(false)

	// (h) longerBudgetHint omitted at 300s cap, present below 300s
	if h := longerBudgetHint(300_000); h != "" {
		t.Fatalf("expected empty longerBudgetHint at 300s max, got %q", h)
	}
	if h := longerBudgetHint(30_000); !strings.Contains(h, "raise the Eval cell's `timeout`") || !strings.Contains(h, "tools.maxTimeout") {
		t.Fatalf("unexpected longerBudgetHint(30s): %q", h)
	}
}

// --- Pass-7 Finding 7 (unit): omp captureAfterFrame bounded error (waitForRenderFrame=false -> fromSurface:false -> named error) ---

func TestPass7_F7_ScreenshotBoundedStalledRendererError(t *testing.T) {
	hangDone := make(chan struct{})
	defer close(hangDone)
	var sawFromSurfaceFalse atomic.Bool

	srv := startFakeCDPServer(t, func(method string, params json.RawMessage) (any, error) {
		switch method {
		case "Target.createTarget":
			return map[string]any{"targetId": "t1"}, nil
		case "Target.attachToTarget":
			return map[string]any{"sessionId": "s1"}, nil
		case "Page.getFrameTree":
			return map[string]any{"frameTree": map[string]any{"frame": map[string]any{"id": "main-f", "url": "http://example.test/"}}}, nil
		case "Runtime.evaluate":
			var p struct {
				Expression string `json:"expression"`
			}
			_ = json.Unmarshal(params, &p)
			if strings.Contains(p.Expression, "requestAnimationFrame") {
				// Simulate waitForRenderFrame returning false (no animation frame drawn).
				return map[string]any{"result": map[string]any{"type": "boolean", "value": false}}, nil
			}
			return map[string]any{"result": map[string]any{"type": "string", "value": ""}}, nil
		case "Page.captureScreenshot":
			var p struct {
				FromSurface *bool `json:"fromSurface"`
			}
			_ = json.Unmarshal(params, &p)
			if p.FromSurface != nil && !*p.FromSurface {
				sawFromSurfaceFalse.Store(true)
			}
			<-hangDone
			return nil, errors.New("closed")
		default:
			return map[string]any{}, nil
		}
	})

	b := NewChromiumBackend()
	tab, err := b.Open(context.Background(), OpenOptions{Name: "stalled-shot", Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer tab.Close(context.Background(), false)

	// Default 30s budget (quickOpMs = 20s): captureAfterFrame must fail at ~5s
	// with omp's exact named error instead of waiting 20s!
	_, err = tab.Call(context.Background(), CallContext{TimeoutMs: 30_000, Cwd: t.TempDir()}, []CallStep{{Method: "screenshot"}})
	want := "Screenshot failed: the page's renderer stopped drawing (no animation frame in 1500ms and no capture in 5000ms). DOM helpers still work; reopen the tab with kill:true to get a fresh renderer."
	if err == nil || err.Error() != want {
		t.Fatalf("stalled screenshot error mismatch:\n got: %v\nwant: %s", err, want)
	}
	if !sawFromSurfaceFalse.Load() {
		t.Fatalf("expected fromSurface:false fallback capture when waitForRenderFrame returned false")
	}
}

// --- Pass-7 Findings 1 & 5 (real Chrome): allowed_domains enforcement across OOPIF, dedicated worker, WS, SW, SharedWorker, popups ---

func TestRealPass7_F1_F5_AllowedDomainsOOPIFAndCoverageMatrix(t *testing.T) {
	m, _, req := setupRealBrowserTest(t)

	var mu sync.Mutex
	bHits := map[string]int{}
	allowedMarkers := map[string]int{}
	recordB := func(kind, path string) {
		mu.Lock()
		bHits[kind+":"+path]++
		mu.Unlock()
	}
	recordMarker := func(name string) {
		mu.Lock()
		allowedMarkers[name]++
		mu.Unlock()
	}

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := strings.Split(r.Host, ":")[0]
		port := strings.Split(strings.TrimPrefix(srv.URL, "http://"), ":")[1]

		// Handle raw WebSocket upgrade request on /ws.
		if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			if host == "b.localhost" {
				recordB("ws", r.URL.Path)
			}
			hj, ok := w.(http.Hijacker)
			if !ok {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			conn, rw, err := hj.Hijack()
			if err != nil {
				return
			}
			defer conn.Close()
			key := r.Header.Get("Sec-WebSocket-Key")
			sum := sha1.Sum([]byte(key + wsGUID))
			accept := base64.StdEncoding.EncodeToString(sum[:])
			_, _ = io.WriteString(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: "+accept+"\r\n\r\n")
			_ = rw.Flush()
			_, _ = bufio.NewReader(conn).ReadByte()
			return
		}

		if host == "b.localhost" {
			recordB("http", r.URL.Path)
			w.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(w, "disallowed")
			return
		}

		if r.URL.Path == "/marker" {
			recordMarker(r.URL.Query().Get("from"))
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			_, _ = io.WriteString(w, "ok")
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		switch r.URL.Path {
		case "/oopif":
			// 127.0.0.1 embeds cross-site OOPIF on a.localhost (allowed).
			_, _ = fmt.Fprintf(w, `<!doctype html><html><body>
  <iframe id="cross" src="http://a.localhost:%s/frame"></iframe>
</body></html>`, port)
		case "/frame":
			// OOPIF on a.localhost emits an allowed-host marker AND tries disallowed img, fetch, nested OOPIF, and dedicated worker.
			_, _ = fmt.Fprintf(w, `<!doctype html><html><body>
  <img src="http://b.localhost:%s/oopif-img">
  <iframe id="nested" src="http://c.localhost:%s/nested-frame"></iframe>
  <script>
    fetch("http://127.0.0.1:%s/marker?from=oopif").catch(() => {});
    fetch("http://b.localhost:%s/oopif-fetch").catch(() => {});
    new Worker("/oopif-worker.js");
  </script>
</body></html>`, port, port, port, port)
		case "/nested-frame":
			_, _ = fmt.Fprintf(w, `<!doctype html><html><body>
  <img src="http://b.localhost:%s/nested-oopif-img">
  <script>
    fetch("http://127.0.0.1:%s/marker?from=nested-oopif").catch(() => {});
    fetch("http://b.localhost:%s/nested-oopif-fetch").catch(() => {});
    new Worker("/nested-worker.js");
  </script>
</body></html>`, port, port, port)
		case "/oopif-worker.js":
			w.Header().Set("Content-Type", "application/javascript")
			_, _ = fmt.Fprintf(w, `
fetch("http://127.0.0.1:%s/marker?from=oopif-worker").catch(() => {});
fetch("http://b.localhost:%s/oopif-worker-fetch").catch(() => {});`, port, port)
		case "/nested-worker.js":
			w.Header().Set("Content-Type", "application/javascript")
			_, _ = fmt.Fprintf(w, `
fetch("http://127.0.0.1:%s/marker?from=nested-worker").catch(() => {});
fetch("http://b.localhost:%s/nested-worker-fetch").catch(() => {});`, port, port)
		case "/main-kinds":
			_, _ = fmt.Fprintf(w, `<!doctype html><html><body>
  <a id="pop" target="_blank" href="http://b.localhost:%s/popup-blank">Pop</a>
  <button id="wopen" onclick="window.open('http://b.localhost:%s/window-open', '_blank')">WOpen</button>
  <script>
    new Worker("/dw.js");
    try { const ws = new WebSocket("ws://b.localhost:%s/ws"); ws.onerror = () => {}; } catch {}
    try { const shw = new SharedWorker("/shw.js"); shw.port.start(); } catch {}
    if (navigator.serviceWorker) { navigator.serviceWorker.register("/sw.js").catch(() => {}); }
  </script>
</body></html>`, port, port, port)
		case "/dw.js":
			w.Header().Set("Content-Type", "application/javascript")
			_, _ = fmt.Fprintf(w, `
fetch("http://127.0.0.1:%s/marker?from=main-worker").catch(() => {});
fetch("http://b.localhost:%s/dw-fetch").catch(() => {});`, port, port)
		case "/shw.js":
			w.Header().Set("Content-Type", "application/javascript")
			_, _ = fmt.Fprintf(w, `fetch("http://b.localhost:%s/shw-fetch").catch(() => {});`, port)
		case "/sw.js":
			w.Header().Set("Content-Type", "application/javascript")
			_, _ = fmt.Fprintf(w, `self.addEventListener("install", e => { e.waitUntil(fetch("http://b.localhost:%s/sw-install").catch(() => {})); });`, port)
		default:
			_, _ = io.WriteString(w, `<!doctype html><html><body>ok</body></html>`)
		}
	}))
	defer srv.Close()

	baseURL := strings.Replace(srv.URL, "localhost", "127.0.0.1", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	openP, _ := json.Marshal(map[string]any{
		"action":          "open",
		"name":            "oopif-sec",
		"url":             baseURL + "/oopif",
		"app":             "chromium",
		"allowed_domains": []string{"127.0.0.1", "a.localhost", "c.localhost"},
	})
	if _, err := m.Invoke(ctx, req, openP); err != nil {
		t.Fatalf("open oopif-sec failed: %v", err)
	}
	// Wait until all 4 OOPIF / nested OOPIF / worker contexts report their allowed marker.
	waitMarkers := func(names ...string) {
		t.Helper()
		dl := time.Now().Add(5 * time.Second)
		for time.Now().Before(dl) {
			mu.Lock()
			allOK := true
			for _, n := range names {
				if allowedMarkers[n] == 0 {
					allOK = false
					break
				}
			}
			mu.Unlock()
			if allOK {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		mu.Lock()
		t.Fatalf("timed out waiting for allowed-host control markers %v; got %v", names, allowedMarkers)
		mu.Unlock()
	}
	waitMarkers("oopif", "nested-oopif", "oopif-worker", "nested-worker")

	// Navigate to /main-kinds to test dedicated worker + document/measure WS, SW, SharedWorker, target=_blank, window.open.
	if _, err := m.Invoke(ctx, req, json.RawMessage(fmt.Sprintf(`{"action":"call","name":"oopif-sec","chain":[{"method":"goto","args":[%q]}]}`, baseURL+"/main-kinds"))); err != nil {
		t.Fatalf("goto /main-kinds failed: %v", err)
	}
	waitMarkers("main-worker")
	time.Sleep(500 * time.Millisecond)
	if _, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"oopif-sec","chain":[{"method":"click","args":["#pop"]}]}`)); err != nil {
		t.Fatalf("click #pop failed: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if _, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"oopif-sec","chain":[{"method":"click","args":["#wopen"]}]}`)); err != nil {
		t.Fatalf("click #wopen failed: %v", err)
	}
	time.Sleep(400 * time.Millisecond)

	mu.Lock()
	snapshot := make(map[string]int, len(bHits))
	for k, v := range bHits {
		snapshot[k] = v
	}
	markerSnap := make(map[string]int, len(allowedMarkers))
	for k, v := range allowedMarkers {
		markerSnap[k] = v
	}
	mu.Unlock()
	t.Logf("allowed-host control markers arrived: %v; disallowed b.localhost hits: %v", markerSnap, snapshot)

	// Enforced kinds (matching omp/Puppeteer): MUST have 0 hits on b.localhost!
	for _, mustBeZero := range []string{
		"http:/oopif-img",
		"http:/oopif-fetch",
		"http:/nested-oopif-img",
		"http:/nested-oopif-fetch",
		"http:/oopif-worker-fetch",
		"http:/nested-worker-fetch",
		"http:/dw-fetch",
	} {
		if got := snapshot[mustBeZero]; got != 0 {
			t.Fatalf("expected 0 hits on disallowed %s (enforced by omp), got %d (all=%v)", mustBeZero, got, snapshot)
		}
	}
}

// --- Pass-7 Finding 2 (real Chrome, security): 0 requests escape allowed_domains across 3 whole-call-timeout reattaches ---

func TestRealPass7_F2_ZeroRequestsEscapeAcrossThreeReattaches(t *testing.T) {
	m, _, req := setupRealBrowserTest(t)

	var leaked atomic.Int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := strings.Split(r.Host, ":")[0]
		if host == "localhost" {
			leaked.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "LEAKED")
			return
		}
		port := strings.Split(strings.TrimPrefix(srv.URL, "http://"), ":")[1]
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintf(w, `<!doctype html><html><body>
  <div id="ok">storm</div>
  <script>
    window.__reattachKeep = 777;
    setInterval(() => {
      fetch("http://localhost:%s/gap?n=" + Math.random()).catch(() => {});
    }, 2);
  </script>
</body></html>`, port)
	}))
	defer srv.Close()

	allowedURL := strings.Replace(srv.URL, "localhost", "127.0.0.1", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	openP, _ := json.Marshal(map[string]any{
		"action":          "open",
		"name":            "storm",
		"url":             allowedURL,
		"app":             "chromium",
		"allowed_domains": []string{"127.0.0.1"},
	})
	if _, err := m.Invoke(ctx, req, openP); err != nil {
		t.Fatalf("open storm tab failed: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if got := leaked.Load(); got != 0 {
		t.Fatalf("disallowed host saw %d hits before reattach (want 0)", got)
	}

	// Trigger 3 consecutive whole-call timeouts -> 3 reattachSessions while the
	// page fires fetch("http://localhost:P/gap") every 2ms.
	// Anchor the unresolved promise on window.__hold so V8 GC (triggered by the
	// 2ms fetch storm) cannot collect it and reject with "Promise was collected".
	for round := 1; round <= 3; round++ {
		_, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"storm","timeout":2,"chain":[{"method":"evaluate","args":["new Promise(r => { window.__hold = r; })"]}]}`))
		if err == nil || !strings.Contains(err.Error(), "omp reattached the tab to a new worker without reloading its page.") {
			t.Fatalf("round %d: expected whole-call timeout with omp verbatim reattach text, got: %v", round, err)
		}
		if !strings.Contains(err.Error(), "(stalled on tab.evaluate()") || !strings.Contains(err.Error(), "raise the Eval cell's `timeout`") {
			t.Fatalf("round %d: expected stalled-op clause and longerBudgetHint in whole-call timeout error, got: %v", round, err)
		}
		time.Sleep(150 * time.Millisecond)
		if got := leaked.Load(); got != 0 {
			t.Fatalf("round %d: %d request(s) escaped allowed_domains during reattach (want 0)", round, got)
		}
	}

	// Page JS state must survive all 3 reattaches intact.
	r, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"storm","chain":[{"method":"evaluate","args":["window.__reattachKeep"]}]}`))
	if err != nil || r.Details.Value != float64(777) {
		t.Fatalf("expected window.__reattachKeep == 777 after 3 reattaches, got %#v err=%v", r, err)
	}
}

// --- Pass-7 Finding 3 (real Chrome, blocking): busy handler & hung navigation keep tab alive after per-op timeout ---

func TestRealPass7_F3_BusyHandlerAndHungNavigationKeepTabAlive(t *testing.T) {
	m, _, req := setupRealBrowserTest(t)

	unblock := make(chan struct{})
	defer close(unblock)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/never":
			select {
			case <-unblock:
			case <-r.Context().Done():
			}
			return
		case "/escaped":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, `<!doctype html><html><body><div id="esc">ESCAPED-OK</div></body></html>`)
			return
		default:
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, `<!doctype html><html><body>
  <button id="busy" onclick="window.__state='ran'; const end=Date.now()+2600; while(Date.now()<end){}">Busy 2.6s</button>
  <a id="hanglink" href="/never">Never answers</a>
  <div id="marker">LIVE</div>
  <script>window.__state = "init";</script>
</body></html>`)
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 1. Busy synchronous click handler (2.6s) with timeout:3 (action ceiling 1000ms).
	openBusy, _ := json.Marshal(map[string]any{"action": "open", "name": "busytab", "url": srv.URL, "app": "chromium"})
	if _, err := m.Invoke(ctx, req, openBusy); err != nil {
		t.Fatalf("open busytab failed: %v", err)
	}
	_, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"busytab","timeout":3,"chain":[{"method":"click","args":["#busy"]}]}`))
	if err == nil || !strings.Contains(err.Error(), `tab.click("#busy") timed out after 1000ms`) {
		t.Fatalf("expected omp named per-op timeout on busy click, got: %v", err)
	}
	if strings.Contains(err.Error(), "became unresponsive") || strings.Contains(err.Error(), "tab was closed") {
		t.Fatalf("busy page must NOT be declared wedged or closed: %v", err)
	}
	// Tab and its JS state must remain alive and usable once the 2.6s handler finishes!
	stRes, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"busytab","chain":[{"method":"evaluate","args":["window.__state"]}]}`))
	if err != nil || stRes.Details.Value != "ran" {
		t.Fatalf("expected busytab alive with window.__state=='ran', got %#v err=%v", stRes, err)
	}

	// 2. Hung navigation (/never) followed by text("#marker") with timeout:4 (quickOpMs=2000ms):
	// fails with omp's named timeout and keeps the tab alive so goto("/escaped") leaves the hung load!
	openHang, _ := json.Marshal(map[string]any{"action": "open", "name": "hangkeep", "url": srv.URL, "app": "chromium"})
	if _, err := m.Invoke(ctx, req, openHang); err != nil {
		t.Fatalf("open hangkeep failed: %v", err)
	}
	if _, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"hangkeep","chain":[{"method":"click","args":["#hanglink"]}]}`)); err != nil {
		t.Fatalf("click #hanglink failed: %v", err)
	}
	_, err = m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"hangkeep","timeout":4,"chain":[{"method":"text","args":["#marker"]}]}`))
	if err == nil || !strings.Contains(err.Error(), `tab.text("#marker") timed out after 2000ms`) {
		t.Fatalf("expected omp named per-op timeout on hung navigation, got: %v", err)
	}
	if strings.Contains(err.Error(), "became unresponsive") || strings.Contains(err.Error(), "tab was closed") {
		t.Fatalf("hung navigation must NOT close the tab: %v", err)
	}
	// Tab must still be alive so tab.goto("/escaped") escapes the hung navigation!
	if _, err := m.Invoke(ctx, req, json.RawMessage(fmt.Sprintf(`{"action":"call","name":"hangkeep","chain":[{"method":"goto","args":[%q]}]}`, srv.URL+"/escaped"))); err != nil {
		t.Fatalf("goto /escaped after hung navigation timeout failed: %v", err)
	}
	escRes, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"hangkeep","chain":[{"method":"text","args":["#esc"]}]}`))
	if err != nil || escRes.Details.Value != "ESCAPED-OK" {
		t.Fatalf("expected ESCAPED-OK after leaving hung navigation, got %#v err=%v", escRes, err)
	}
}

// --- Pass-7 Finding 4 (real Chrome): selector actions succeed cleanly when a slow page arrives after 6.5s (no raw CDP context error) ---

func TestRealPass7_F4_SelectorActionsOnSlow6500msNavigation(t *testing.T) {
	m, _, req := setupRealBrowserTest(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		switch r.URL.Path {
		case "/slow65":
			time.Sleep(6500 * time.Millisecond)
			_, _ = io.WriteString(w, `<!doctype html><html><body>
  <div id="marker">ARRIVED</div>
  <button id="other" onclick="window.__otherHit = (window.__otherHit || 0) + 1">Other</button>
  <input id="inp" type="text">
  <input id="pinp" type="text" onkeydown="if(event.key==='Enter') window.__pressHit = (window.__pressHit || 0) + 1">
</body></html>`)
		case "/slow65-missing":
			time.Sleep(6500 * time.Millisecond)
			_, _ = io.WriteString(w, `<!doctype html><html><body><div id="marker">NO-TARGET</div></body></html>`)
		default:
			_, _ = io.WriteString(w, `<!doctype html><html><body>
  <a id="go-slow" href="/slow65">Slow 6.5s</a>
  <a id="go-miss" href="/slow65-missing">Slow 6.5s missing</a>
</body></html>`)
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	openP, _ := json.Marshal(map[string]any{"action": "open", "name": "slowact", "url": srv.URL, "app": "chromium"})
	if _, err := m.Invoke(ctx, req, openP); err != nil {
		t.Fatalf("open slowact failed: %v", err)
	}

	// 1. click("#other") after 6.5s link -> succeeds on arriving page (never raw CDP context error).
	if _, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"slowact","chain":[{"method":"click","args":["#go-slow"]}]}`)); err != nil {
		t.Fatalf("click #go-slow: %v", err)
	}
	if _, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"slowact","chain":[{"method":"click","args":["#other"]}]}`)); err != nil {
		t.Fatalf("click(#other) after 6.5s slow page failed (want success, not raw CDP context error): %v", err)
	}
	hitRes, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"slowact","chain":[{"method":"evaluate","args":["window.__otherHit"]}]}`))
	if err != nil || hitRes.Details.Value != float64(1) {
		t.Fatalf("expected #other clicked once on arriving page, got %#v err=%v", hitRes, err)
	}

	// 2. fill("#inp", "v") after 6.5s link -> succeeds on arriving page.
	if _, err := m.Invoke(ctx, req, json.RawMessage(fmt.Sprintf(`{"action":"call","name":"slowact","chain":[{"method":"goto","args":[%q]}]}`, srv.URL))); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"slowact","chain":[{"method":"click","args":["#go-slow"]}]}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"slowact","chain":[{"method":"fill","args":["#inp","v"]}]}`)); err != nil {
		t.Fatalf("fill(#inp) after 6.5s slow page failed: %v", err)
	}
	valRes, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"slowact","chain":[{"method":"value","args":["#inp"]}]}`))
	if err != nil || valRes.Details.Value != "v" {
		t.Fatalf("expected #inp value 'v', got %#v err=%v", valRes, err)
	}

	// 3. press("Enter", {selector: "#pinp"}) after 6.5s link -> succeeds on arriving page with handler count == 1.
	if _, err := m.Invoke(ctx, req, json.RawMessage(fmt.Sprintf(`{"action":"call","name":"slowact","chain":[{"method":"goto","args":[%q]}]}`, srv.URL))); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"slowact","chain":[{"method":"click","args":["#go-slow"]}]}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"slowact","chain":[{"method":"press","args":["Enter",{"selector":"#pinp"}]}]}`)); err != nil {
		t.Fatalf("press(Enter, {selector:#pinp}) after 6.5s slow page failed: %v", err)
	}
	pressRes, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"slowact","chain":[{"method":"evaluate","args":["window.__pressHit"]}]}`))
	if err != nil || pressRes.Details.Value != float64(1) {
		t.Fatalf("expected #pinp keydown handler count 1 on arriving page, got %#v err=%v", pressRes, err)
	}

	// 4. Slow 6.5s page lacking #missing: omp's fixed-deadline zero-match watchdog
	// fails fast at the first conclusive zero on the arriving page with exact wording:
	// `tab.click("#missing") failed fast after 2000ms; selector currently matches no elements ...`.
	if _, err := m.Invoke(ctx, req, json.RawMessage(fmt.Sprintf(`{"action":"call","name":"slowact","chain":[{"method":"goto","args":[%q]}]}`, srv.URL))); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"slowact","chain":[{"method":"click","args":["#go-miss"]}]}`)); err != nil {
		t.Fatal(err)
	}
	_, err = m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"slowact","chain":[{"method":"click","args":["#missing"]}]}`))
	if err == nil {
		t.Fatalf("expected fail-fast error for #missing on slow page")
	}
	want := `tab.click("#missing") failed fast after 2000ms; selector currently matches no elements — run tab.observe() or tab.ariaSnapshot() to inspect the page`
	if err.Error() != want {
		t.Fatalf("slow arriving missing-selector error mismatch:\n got: %s\nwant: %s", err.Error(), want)
	}
}

// --- Pass-7 Findings 7 & 10 (real Chrome): two-tab screenshot and hover on older tab (locked display) ---

func TestRealPass7_F7_F10_OlderTabScreenshotAndHover(t *testing.T) {
	m, _, req := setupRealBrowserTest(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.URL.Path == "/tab2" {
			_, _ = io.WriteString(w, `<!doctype html><html><body><div id="t2">SECOND-TAB</div></body></html>`)
			return
		}
		_, _ = io.WriteString(w, `<!doctype html><html><body style="margin:0;padding:12px">
  <div id="t1">FIRST-TAB</div>
  <button id="hbtn" style="width:120px;height:40px">HoverTarget</button>
</body></html>`)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	// Open older tab first, THEN open newer tab second.
	open1, _ := json.Marshal(map[string]any{"action": "open", "name": "older", "url": srv.URL + "/tab1", "app": "chromium"})
	if _, err := m.Invoke(ctx, req, open1); err != nil {
		t.Fatalf("open older tab failed: %v", err)
	}
	open2, _ := json.Marshal(map[string]any{"action": "open", "name": "newer", "url": srv.URL + "/tab2", "app": "chromium"})
	if _, err := m.Invoke(ctx, req, open2); err != nil {
		t.Fatalf("open newer tab failed: %v", err)
	}

	// 1. Viewport screenshot of the OLDER tab must succeed cleanly (not 20s timeout).
	shot1, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"older","chain":[{"method":"screenshot","args":[]}]}`))
	if err != nil {
		t.Fatalf("older tab viewport screenshot failed: %v", err)
	}
	if len(shot1.Details.Screenshots) != 1 || shot1.Details.Screenshots[0].Bytes < 100 {
		t.Fatalf("expected non-empty viewport screenshot for older tab, got %+v", shot1.Details.Screenshots)
	}

	// 2. Element screenshot of the OLDER tab must also succeed.
	shotEl, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"older","chain":[{"method":"screenshot","args":[{"selector":"#hbtn"}]}]}`))
	if err != nil {
		t.Fatalf("older tab element screenshot failed: %v", err)
	}
	if len(shotEl.Details.Screenshots) != 1 || shotEl.Details.Screenshots[0].Bytes < 100 {
		t.Fatalf("expected non-empty element screenshot for older tab, got %+v", shotEl.Details.Screenshots)
	}

	// 3. Re-open/capture newer tab, then hover on the OLDER tab: hover must NOT hit Chrome's 5s stall.
	if _, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"newer","chain":[{"method":"screenshot","args":[]}]}`)); err != nil {
		t.Fatalf("newer tab screenshot failed: %v", err)
	}
	for i := range 3 {
		t0 := time.Now()
		if _, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"older","chain":[{"method":"hover","args":["#hbtn"]}]}`)); err != nil {
			t.Fatalf("older tab hover %d failed: %v", i, err)
		}
		if elapsed := time.Since(t0); elapsed >= 4500*time.Millisecond {
			t.Fatalf("older tab hover %d stalled for %v (hit 5s background-tab input timeout)", i, elapsed)
		}
	}
	hovRes, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"older","chain":[{"method":"evaluate","args":["document.querySelector('#hbtn').matches(':hover')"]}]}`))
	if err != nil || hovRes.Details.Value != true {
		t.Fatalf("expected #hbtn to match :hover after hover(), got %#v err=%v", hovRes, err)
	}
}
