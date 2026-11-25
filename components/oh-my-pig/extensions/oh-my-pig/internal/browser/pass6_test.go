package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- Pass-6 Finding 1 (unit): configureSession installs identical full state on open and reattach, and fails closed if Fetch.enable fails ---

func TestPass6_ConfigureSessionStateBeforeAndAfterReattach(t *testing.T) {
	type callRec struct {
		session string
		method  string
		params  string
	}
	var mu sync.Mutex
	var calls []callRec
	var failFetchOnS2 atomic.Bool
	var attachCount atomic.Int32

	srv := startFakeCDPServer(t, func(method string, params json.RawMessage) (any, error) {
		switch method {
		case "Target.createTarget":
			return map[string]any{"targetId": "t1"}, nil
		case "Target.attachToTarget":
			n := attachCount.Add(1)
			return map[string]any{"sessionId": fmt.Sprintf("s%d", n)}, nil
		case "Page.getFrameTree":
			return map[string]any{"frameTree": map[string]any{"frame": map[string]any{"id": "main-f", "url": "http://127.0.0.1/"}}}, nil
		case "Page.addScriptToEvaluateOnNewDocument":
			mu.Lock()
			calls = append(calls, callRec{session: fmt.Sprintf("s%d", attachCount.Load()), method: method, params: string(params)})
			mu.Unlock()
			return map[string]any{"identifier": "init-1"}, nil
		case "Runtime.evaluate":
			var p struct {
				Expression string `json:"expression"`
			}
			_ = json.Unmarshal(params, &p)
			if p.Expression == "navigator.userAgent" {
				return map[string]any{"result": map[string]any{"type": "string", "value": "BaseUA/1.0"}}, nil
			}
			return map[string]any{"result": map[string]any{"type": "number", "value": float64(1)}}, nil
		case "Fetch.enable":
			sid := fmt.Sprintf("s%d", attachCount.Load())
			mu.Lock()
			calls = append(calls, callRec{session: sid, method: method, params: string(params)})
			mu.Unlock()
			if sid == "s2" && failFetchOnS2.Load() {
				return nil, fmt.Errorf("Fetch.enable refused")
			}
			return map[string]any{}, nil
		default:
			mu.Lock()
			calls = append(calls, callRec{session: fmt.Sprintf("s%d", attachCount.Load()), method: method, params: string(params)})
			mu.Unlock()
			return map[string]any{}, nil
		}
	})

	dlDir := t.TempDir()
	b := NewChromiumBackend()
	tab, err := b.Open(context.Background(), OpenOptions{
		Name:           "sec",
		Endpoint:       srv.URL,
		Viewport:       &Viewport{Width: 900, Height: 600, DeviceScaleFactor: 2},
		UserAgent:      "CustomUA/2.0",
		Insecure:       true,
		DownloadsPath:  dlDir,
		InitScripts:    []string{"window.__initA = 1;"},
		AllowedDomains: []string{"127.0.0.1"},
	})
	if err != nil {
		t.Fatalf("open failed: %v", err)
	}
	defer tab.Close(context.Background(), false)
	ct := tab.(*cdpTab)

	// Reattach once and verify every single piece of session configuration was sent on BOTH s1 and s2.
	if !ct.reattachSession(context.Background()) {
		t.Fatalf("expected reattachSession to succeed")
	}

	required := []struct {
		method string
		substr string
	}{
		{"Fetch.enable", `"urlPattern":"*"`},
		{"Target.setAutoAttach", `"waitForDebuggerOnStart":true`},
		{"Page.enable", ""},
		{"Runtime.enable", ""},
		{"Network.enable", ""},
		{"DOM.enable", ""},
		{"Inspector.enable", ""},
		{"Accessibility.enable", ""},
		{"Page.setLifecycleEventsEnabled", `"enabled":true`},
		{"Emulation.setFocusEmulationEnabled", `"enabled":true`},
		{"Page.addScriptToEvaluateOnNewDocument", `"worldName":"omp-browser-kit"`},
		{"Emulation.setDeviceMetricsOverride", `"deviceScaleFactor":2`},
		{"Network.setUserAgentOverride", `"userAgent":"CustomUA/2.0"`},
		{"Security.setIgnoreCertificateErrors", `"ignore":true`},
		{"Browser.setDownloadBehavior", dlDir},
		{"Page.addScriptToEvaluateOnNewDocument", `window.__initA = 1;`},
	}
	mu.Lock()
	snapshot := append([]callRec(nil), calls...)
	mu.Unlock()

	for _, sid := range []string{"s1", "s2"} {
		for _, req := range required {
			found := false
			for _, c := range snapshot {
				if c.session == sid && c.method == req.method && (req.substr == "" || strings.Contains(c.params, req.substr)) {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("session %s missing %s (substr=%q)", sid, req.method, req.substr)
			}
		}
	}

	// Verify fail-closed: if Fetch.enable fails on reattach, reattachSession returns false.
	attachCount.Store(1) // next attach becomes s2
	failFetchOnS2.Store(true)
	if ct.reattachSession(context.Background()) {
		t.Fatalf("expected reattachSession to fail closed when Fetch.enable fails")
	}
}

// --- Pass-6 Finding 2 (unit): actions aborted on context clear are sent AT MOST ONCE ---

func TestPass6_ActionSentAtMostOnceOnContextAbort(t *testing.T) {
	var actionEvalCount atomic.Int32
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
		case "Runtime.evaluate":
			var p struct {
				ContextID  int64  `json:"contextId"`
				Expression string `json:"expression"`
			}
			_ = json.Unmarshal(params, &p)
			if strings.Contains(p.Expression, "queryAll") {
				return map[string]any{"result": map[string]any{"type": "number", "value": float64(1)}}, nil
			}
			if strings.HasPrefix(p.Expression, "Boolean(globalThis.__ompBrowserKit.queryFirst(") {
				return map[string]any{"result": map[string]any{"type": "boolean", "value": true}}, nil
			}
			if strings.Contains(p.Expression, "selectOptions") || strings.Contains(p.Expression, "tab.fill()") || strings.Contains(p.Expression, ".focus()") {
				actionEvalCount.Add(1)
				// Simulate Chrome clearing the context and never replying to the evaluate.
				<-hangDone
				return nil, fmt.Errorf("closed")
			}
			return map[string]any{"result": map[string]any{"type": "string", "value": ""}}, nil
		default:
			return map[string]any{}, nil
		}
	})

	b := NewChromiumBackend()
	tab, err := b.Open(context.Background(), OpenOptions{Name: "once", Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer tab.Close(context.Background(), false)
	ct := tab.(*cdpTab)

	for _, tc := range []struct {
		name string
		step CallStep
	}{
		{"select", CallStep{Method: "select", Args: []json.RawMessage{json.RawMessage(`"#sel"`), json.RawMessage(`"z"`)}}},
		{"fill", CallStep{Method: "fill", Args: []json.RawMessage{json.RawMessage(`"#q"`), json.RawMessage(`"x"`)}}},
		{"focus", CallStep{Method: "focus", Args: []json.RawMessage{json.RawMessage(`"#k"`)}}},
		{"press", CallStep{Method: "press", Args: []json.RawMessage{json.RawMessage(`"Enter"`), json.RawMessage(`{"selector":"#k"}`)}}},
	} {
		actionEvalCount.Store(0)
		// Install isolated world 500 as already populated.
		ct.mu.Lock()
		ct.isolatedWorlds["main-f"] = 500
		ct.kitInstalled[500] = true
		ct.mu.Unlock()

		go func() {
			time.Sleep(40 * time.Millisecond)
			ct.onCDPEvent(CDPEvent{
				SessionID: ct.sessionID,
				Method:    "Runtime.executionContextsCleared",
				Params:    json.RawMessage(`{}`),
			})
		}()

		_, err := tab.Call(context.Background(), CallContext{TimeoutMs: 5000}, []CallStep{tc.step})
		if err == nil || !strings.Contains(err.Error(), "Execution context was destroyed") {
			t.Fatalf("%s: expected 'Execution context was destroyed', got %v", tc.name, err)
		}
		if got := actionEvalCount.Load(); got != 1 {
			t.Fatalf("%s: expected action expression sent exactly ONCE, got %d", tc.name, got)
		}
	}
}

// --- Pass-6 Finding 6 (unit): covered/hidden refusal always wins over bare per-op deadline ---

func TestPass6_CoveredClickAlwaysReportsReasonOnDeadline(t *testing.T) {
	var evalCalls atomic.Int32
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
				return map[string]any{"result": map[string]any{"type": "number", "value": float64(1)}}, nil
			}
			if strings.Contains(p.Expression, "checkClickActionable") {
				n := evalCalls.Add(1)
				if n == 1 {
					// Return covered with moving rect so loop continues.
					return map[string]any{"result": map[string]any{"type": "object", "value": map[string]any{
						"ok": false, "reason": "covered", "coveredBy": "<div#overlay>",
						"rect": map[string]any{"x": float64(10), "y": float64(10), "width": float64(100), "height": float64(30)},
					}}}, nil
				}
				// Second evaluation stalls until caller's opCtx expires mid-evaluate.
				time.Sleep(400 * time.Millisecond)
				return map[string]any{"result": map[string]any{"type": "object", "value": map[string]any{"ok": false, "reason": "covered", "coveredBy": "<div#overlay>"}}}, nil
			}
			return map[string]any{"result": map[string]any{"type": "string", "value": ""}}, nil
		default:
			return map[string]any{}, nil
		}
	})

	b := NewChromiumBackend()
	tab, err := b.Open(context.Background(), OpenOptions{Name: "cov", Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer tab.Close(context.Background(), false)

	// Tight 200ms opCtx so deadline expires while the 2nd checkClickActionable evaluate is in flight.
	// elementActionableCenter returns clickRefusedError so perOpTimeoutError formats omp's exact hint:
	// `tab.click("#btn") timed out after 8000ms; the element never became clickable (last check: covered by <div#overlay>; selector matches 1 element(s))`.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	ct := tab.(*cdpTab)
	_, _, err = ct.elementActionableCenter(ctx, `tab.click("#btn")`, `document.querySelector("#btn")`, false, 8000, frameTarget{})
	var refused *clickRefusedError
	if err == nil || !errors.As(err, &refused) || refused.refusal != "covered by <div#overlay>" {
		t.Fatalf("expected clickRefusedError naming covered by <div#overlay>, got: %v", err)
	}
	formatted := ct.perOpTimeoutError(`tab.click("#btn")`, "#btn", 8000, err, frameTarget{})
	want := `tab.click("#btn") timed out after 8000ms; the element never became clickable (last check: covered by <div#overlay>; selector matches 1 element(s))`
	if formatted.Error() != want {
		t.Fatalf("perOpTimeoutError mismatch:\n got: %s\nwant: %s", formatted.Error(), want)
	}
}

// --- Pass-7 (unit): per-op timeout bounds a wedged/busy op with omp's named error and KEEPS the tab + peers alive (no DOM.getDocument close) ---

func TestPass6_PerOpTimeoutRecyclesWedgedTabAndSparesPeer(t *testing.T) {
	hangDone := make(chan struct{})
	defer close(hangDone)
	var wedgeVictim atomic.Bool
	var domProbeSent atomic.Bool
	var targetSeq atomic.Int32

	srv := startFakeCDPServer(t, func(method string, params json.RawMessage) (any, error) {
		switch method {
		case "Target.createTarget":
			n := targetSeq.Add(1)
			return map[string]any{"targetId": fmt.Sprintf("t%d", n)}, nil
		case "Target.attachToTarget":
			var p struct {
				TargetID string `json:"targetId"`
			}
			_ = json.Unmarshal(params, &p)
			return map[string]any{"sessionId": "sess-" + p.TargetID}, nil
		case "Page.getFrameTree":
			return map[string]any{"frameTree": map[string]any{"frame": map[string]any{"id": "main-f", "url": "http://example.test/"}}}, nil
		case "Page.createIsolatedWorld":
			return map[string]any{"executionContextId": 99}, nil
		case "DOM.getDocument":
			domProbeSent.Store(true)
			return map[string]any{"root": map[string]any{"nodeId": 1}}, nil
		case "Runtime.evaluate":
			if wedgeVictim.Load() && targetSeq.Load() == 2 {
				<-hangDone
				return nil, fmt.Errorf("closed")
			}
			return map[string]any{"result": map[string]any{"type": "string", "value": "ok"}}, nil
		default:
			return map[string]any{}, nil
		}
	})

	orig := RegisteredBackends()
	t.Cleanup(func() {
		for k := range RegisteredBackends() {
			RegisterBackend(k, nil)
		}
		for k, v := range orig {
			RegisterBackend(k, v)
		}
	})
	chrom := NewChromiumBackend()
	RegisterBackend(string(KindConnected), chrom)
	m := NewManager()
	m.env = envLookup(map[string]string{})
	req := InvokeRequest{Cwd: t.TempDir(), OwnerSessionID: "wedge-sess"}
	defer m.ReleaseAllTabs(context.Background(), true)

	ctx := context.Background()
	for _, name := range []string{"peer", "victim"} {
		openP, _ := json.Marshal(map[string]any{"action": "open", "name": name, "app": srv.URL})
		if _, err := m.Invoke(ctx, req, openP); err != nil {
			t.Fatalf("open %s: %v", name, err)
		}
	}

	// Stall victim and call text("#a") with short 3s tool budget (quickOpMs=1000ms).
	// Per omp (tab-worker.ts:2921-2944): fails fast at 1000ms with selector label and
	// count-probe failure hint, NEVER probes DOM.getDocument, and KEEPS the tab open!
	wedgeVictim.Store(true)
	_, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"victim","timeout":3,"chain":[{"method":"text","args":["#a"]}]}`))
	wedgeVictim.Store(false)

	if err == nil || !strings.Contains(err.Error(), `tab.text("#a") timed out after 1000ms; could not count matches: no answer within 1000ms`) {
		t.Fatalf("expected omp named per-op timeout with selector label and match-count probe note, got %v", err)
	}
	if domProbeSent.Load() {
		t.Fatalf("perOpTimeoutError must not send DOM.getDocument")
	}
	if got := m.getLiveTab(ctx, tabKey{owner: req.OwnerSessionID, name: "victim"}); got == nil {
		t.Fatalf("expected victim tab to stay open after per-op timeout (matching omp)")
	}
	// Both peer and victim (once unbusy) must still be alive and usable.
	peerRes, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"peer","chain":[{"method":"text","args":["#a"]}]}`))
	if err != nil || peerRes.Details.Value != "ok" {
		t.Fatalf("peer tab broken after victim timeout: res=%#v err=%v", peerRes, err)
	}
	victimRes, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"victim","chain":[{"method":"text","args":["#a"]}]}`))
	if err != nil || victimRes.Details.Value != "ok" {
		t.Fatalf("victim tab unusable after transient stall cleared: res=%#v err=%v", victimRes, err)
	}
}

// --- Pass-6 Finding 1 (real browser, security): allowed_domains + DSF + UA + init_scripts survive whole-call timeout reattach and slow post-nav probe ---

func TestRealPass6_F1_AllowedDomainsAndSessionStateAfterReattach(t *testing.T) {
	m, _, req := setupRealBrowserTest(t)

	var blockedHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/blocked" {
			blockedHits.Add(1)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, `<!doctype html><html><body><div id="bp">SHOULD-NOT-LOAD</div></body></html>`)
			return
		}
		if r.URL.Path == "/slow2" {
			time.Sleep(2200 * time.Millisecond)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, `<!doctype html><html><body><div id="s2">slow-arrived</div></body></html>`)
			return
		}
		if r.URL.Path == "/next" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, `<!doctype html><html><body><div id="nx">next-ok</div></body></html>`)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<!doctype html><html><body>
  <div id="ok">allowed-page</div>
  <a id="slowlink" href="/slow2">Slow 2s</a>
</body></html>`)
	}))
	defer srv.Close()

	// Ensure srv.URL uses 127.0.0.1 and disallowed URL uses localhost on the same port.
	allowedURL := strings.Replace(srv.URL, "localhost", "127.0.0.1", 1)
	disallowedURL := strings.Replace(allowedURL, "127.0.0.1", "localhost", 1) + "/blocked"

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	openP, _ := json.Marshal(map[string]any{
		"action":          "open",
		"name":            "sec",
		"url":             allowedURL,
		"app":             "chromium",
		"allowed_domains": []string{"127.0.0.1"},
		"viewport":        map[string]any{"width": 900, "height": 600, "scale": 2},
		"user_agent":      "CustomAgent/6.0",
		"init_scripts":    []string{"window.__openInit = 'present';"},
	})
	if _, err := m.Invoke(ctx, req, openP); err != nil {
		t.Fatalf("open sec tab failed: %v", err)
	}

	assertSessionState := func(stage string) {
		t.Helper()
		// 1. Disallowed host MUST be blocked with 0 server hits.
		_, err := m.Invoke(ctx, req, json.RawMessage(fmt.Sprintf(`{"action":"call","name":"sec","chain":[{"method":"goto","args":[%q]}]}`, disallowedURL)))
		if err == nil || !strings.Contains(err.Error(), "ERR_BLOCKED_BY_CLIENT") {
			t.Fatalf("[%s] expected ERR_BLOCKED_BY_CLIENT on disallowed host %s, got err=%v", stage, disallowedURL, err)
		}
		if hits := blockedHits.Load(); hits != 0 {
			t.Fatalf("[%s] disallowed host received %d HTTP requests (want 0)", stage, hits)
		}
		// 2. Viewport DSF == 2 and User-Agent == CustomAgent/6.0.
		r, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"sec","chain":[{"method":"evaluate","args":["({w: innerWidth, h: innerHeight, dpr: devicePixelRatio, ua: navigator.userAgent, init: window.__openInit || ''})"]}]}`))
		if err != nil {
			t.Fatalf("[%s] state eval failed: %v", stage, err)
		}
		mv, _ := r.Details.Value.(map[string]any)
		if mv["dpr"] != float64(2) || mv["w"] != float64(900) || mv["h"] != float64(600) {
			t.Fatalf("[%s] viewport/DSF lost: got %#v", stage, mv)
		}
		if mv["ua"] != "CustomAgent/6.0" {
			t.Fatalf("[%s] user_agent lost: got %#v", stage, mv)
		}
	}

	assertSessionState("before-reattach")

	// Set JS state and verify it survives whole-call timeout reattach.
	if _, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"sec","chain":[{"method":"evaluate","args":["window.__survive = 42"]}]}`)); err != nil {
		t.Fatal(err)
	}

	// Trigger whole-call timeout -> reattachSession.
	_, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"sec","timeout":2,"chain":[{"method":"evaluate","args":["new Promise(() => {})"]}]}`))
	if err == nil || !strings.Contains(err.Error(), "reattached") || !strings.Contains(err.Error(), "Tab state set since it was opened was reset") {
		t.Fatalf("expected whole-call timeout with reattach + reset note, got: %v", err)
	}

	// Verify JS state survived without reloading.
	surv, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"sec","chain":[{"method":"evaluate","args":["window.__survive"]}]}`))
	if err != nil || surv.Details.Value != float64(42) {
		t.Fatalf("expected window.__survive == 42 after reattach, got %#v err=%v", surv, err)
	}

	assertSessionState("after-whole-call-timeout")

	// Navigate to /next on allowed host and confirm open-time init_script still runs on new document.
	if _, err := m.Invoke(ctx, req, json.RawMessage(fmt.Sprintf(`{"action":"call","name":"sec","chain":[{"method":"goto","args":[%q]}]}`, allowedURL+"/next"))); err != nil {
		t.Fatalf("goto /next failed: %v", err)
	}
	initRes, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"sec","chain":[{"method":"evaluate","args":["window.__openInit"]}]}`))
	if err != nil || initRes.Details.Value != "present" {
		t.Fatalf("open-time init_script did not run after reattach + goto: got %#v err=%v", initRes, err)
	}

	// Slow navigation (>1.5s) after click must NOT silently drop allowed_domains either.
	if _, err := m.Invoke(ctx, req, json.RawMessage(fmt.Sprintf(`{"action":"call","name":"sec","chain":[{"method":"goto","args":[%q]}]}`, allowedURL))); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"sec","chain":[{"method":"click","args":["#slowlink"]}]}`)); err != nil {
		t.Fatal(err)
	}
	s2Res, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"sec","chain":[{"method":"text","args":["#s2"]}]}`))
	if err != nil || s2Res.Details.Value != "slow-arrived" {
		t.Fatalf("expected slow-arrived after 2.2s nav, got %#v err=%v", s2Res, err)
	}
	assertSessionState("after-slow-nav-probe")
}

// --- Pass-6 Finding 2 (real browser): click, fill, select, check, press, type, drag, uploadFile run EXACTLY ONCE when their own effect navigates ---

func TestRealPass6_F2_ActionsRunAtMostOnceWhenEffectNavigates(t *testing.T) {
	m, _, req := setupRealBrowserTest(t)

	var mu sync.Mutex
	serverHits := map[string]int{}
	recordHit := func(action string) {
		mu.Lock()
		serverHits[action]++
		mu.Unlock()
	}

	pageHTML := func(step int) string {
		next := step + 1
		return fmt.Sprintf(`<!doctype html><html><body style="margin:0;padding:10px">
<div id="step">step-%d</div>
<button id="btn" onclick="hit('click')">Go</button>
<input id="inp" type="text" oninput="hit('fill')">
<select id="sel" onchange="hit('select')"><option value="a">A</option><option value="z">Z</option></select>
<input id="chk" type="checkbox" onchange="hit('check')">
<input id="prs" type="text" onkeydown="if(event.key==='Enter') hit('press')">
<input id="typ" type="text" oninput="hit('type')">
<div id="drag-from" style="width:60px;height:40px;background:#ccc;display:inline-block">From</div>
<div id="drag-to" style="width:60px;height:40px;background:#aaa;display:inline-block;margin-left:20px" onmouseup="hit('drag')">To</div>
<input id="upl" type="file" onchange="hit('uploadFile')">
<button id="foc" onfocus="hit('focus')">Foc</button>
<script>
function hit(name) {
  const k = 'hits_' + name;
  sessionStorage.setItem(k, String((Number(sessionStorage.getItem(k) || '0')) + 1));
  navigator.sendBeacon('/hit?a=' + encodeURIComponent(name));
  location.href = '/step%d';
}
</script>
</body></html>`, step, next)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/hit":
			recordHit(r.URL.Query().Get("a"))
			w.WriteHeader(http.StatusNoContent)
			return
		case "/step1":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, pageHTML(1))
		case "/step2":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, pageHTML(2))
		case "/step3":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, pageHTML(3))
		case "/sel-done":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, `<!doctype html><html><body><div id="done">no-select-here</div></body></html>`)
		case "/sel-only":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, `<!doctype html><html><body>
<select id="sel" onchange="navigator.sendBeacon('/hit?a=select-missing'); location.href='/sel-done';">
  <option value="a">A</option><option value="z">Z</option>
</select></body></html>`)
		default:
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, pageHTML(1))
		}
	}))
	defer srv.Close()

	// Create a small temp file for uploadFile.
	upFile := filepath.Join(t.TempDir(), "sample.txt")
	if err := os.WriteFile(upFile, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	openP, _ := json.Marshal(map[string]any{"action": "open", "name": "actonce", "url": srv.URL + "/step1", "app": "chromium"})
	if _, err := m.Invoke(ctx, req, openP); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		chain []map[string]any
	}{
		{"click", []map[string]any{{"method": "click", "args": []any{"#btn"}}}},
		{"fill", []map[string]any{{"method": "fill", "args": []any{"#inp", "x"}}}},
		{"select", []map[string]any{{"method": "select", "args": []any{"#sel", "z"}}}},
		{"check", []map[string]any{{"method": "check", "args": []any{"#chk"}}}},
		{"focus", []map[string]any{{"method": "focus", "args": []any{"#foc"}}}},
		{"press", []map[string]any{{"method": "press", "args": []any{"Enter", map[string]any{"selector": "#prs"}}}}},
		{"type", []map[string]any{{"method": "type", "args": []any{"#typ", "a"}}}},
		{"drag", []map[string]any{{"method": "drag", "args": []any{"#drag-from", "#drag-to"}}}},
		{"uploadFile", []map[string]any{{"method": "uploadFile", "args": []any{"#upl", upFile}}}},
	}

	for _, tc := range cases {
		// Reset to /step1 and clear sessionStorage counter.
		if _, err := m.Invoke(ctx, req, json.RawMessage(fmt.Sprintf(`{"action":"call","name":"actonce","chain":[{"method":"goto","args":[%q]}]}`, srv.URL+"/step1"))); err != nil {
			t.Fatalf("%s: goto /step1 failed: %v", tc.name, err)
		}
		if _, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"actonce","chain":[{"method":"clearStorage","args":["session"]}]}`)); err != nil {
			t.Fatalf("%s: clearStorage failed: %v", tc.name, err)
		}
		callP, _ := json.Marshal(map[string]any{"action": "call", "name": "actonce", "chain": tc.chain})
		if _, err := m.Invoke(ctx, req, callP); err != nil {
			t.Fatalf("%s: action failed: %v", tc.name, err)
		}
		// Wait for navigation to settle and read #step + sessionStorage counter.
		stepRes, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"actonce","chain":[{"method":"text","args":["#step"]}]}`))
		if err != nil {
			t.Fatalf("%s: text(#step) failed: %v", tc.name, err)
		}
		if stepRes.Details.Value != "step-2" {
			t.Fatalf("%s: expected landing on step-2 (single navigation), got %#v", tc.name, stepRes.Details.Value)
		}
		stRes, err := m.Invoke(ctx, req, json.RawMessage(fmt.Sprintf(`{"action":"call","name":"actonce","chain":[{"method":"storage","args":["session",%q]}]}`, "hits_"+tc.name)))
		if err != nil {
			t.Fatalf("%s: storage read failed: %v", tc.name, err)
		}
		if stRes.Details.Value != "1" {
			t.Fatalf("%s: expected sessionStorage handler count \"1\", got %#v", tc.name, stRes.Details.Value)
		}
		mu.Lock()
		srvCount := serverHits[tc.name]
		mu.Unlock()
		if srvCount != 1 {
			t.Fatalf("%s: expected server hit count 1, got %d", tc.name, srvCount)
		}
		t.Logf("action=%s: sessionStorage=1 serverHits=%d landed=%v", tc.name, srvCount, stepRes.Details.Value)
	}

	// Also verify select() navigating to a page WITHOUT #sel returns ["z"] cleanly (never fails with No element matched).
	if _, err := m.Invoke(ctx, req, json.RawMessage(fmt.Sprintf(`{"action":"call","name":"actonce","chain":[{"method":"goto","args":[%q]}]}`, srv.URL+"/sel-only"))); err != nil {
		t.Fatal(err)
	}
	selRes, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"actonce","chain":[{"method":"select","args":["#sel","z"]}]}`))
	if err != nil {
		t.Fatalf("select navigating to page without #sel failed: %v", err)
	}
	if arr, _ := selRes.Details.Value.([]any); len(arr) != 1 || arr[0] != "z" {
		t.Fatalf("expected select to return [\"z\"], got %#v", selRes.Details.Value)
	}
	doneRes, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"actonce","chain":[{"method":"text","args":["#done"]}]}`))
	if err != nil || doneRes.Details.Value != "no-select-here" {
		t.Fatalf("expected /sel-done after select, got %#v err=%v", doneRes, err)
	}
	mu.Lock()
	missHits := serverHits["select-missing"]
	mu.Unlock()
	if missHits != 1 {
		t.Fatalf("select-missing server hits: want 1, got %d", missHits)
	}
}

// --- Pass-6 follow-up (unit): screenshot timeout uses separate screencast session, detaches it, and keeps the tab usable for the next call ---

func TestPass6_ScreenshotTimeoutKeepsTabUsableAndDetachesScreencastSession(t *testing.T) {
	hangDone := make(chan struct{})
	defer close(hangDone)
	var attachCount atomic.Int32
	var scStarted atomic.Int32
	var scStopped atomic.Int32
	var scDetached atomic.Int32
	var shotCalls atomic.Int32

	srv := startFakeCDPServer(t, func(method string, params json.RawMessage) (any, error) {
		switch method {
		case "Target.createTarget":
			return map[string]any{"targetId": "t1"}, nil
		case "Target.attachToTarget":
			n := attachCount.Add(1)
			return map[string]any{"sessionId": fmt.Sprintf("s%d", n)}, nil
		case "Target.detachFromTarget":
			var p struct {
				SessionID string `json:"sessionId"`
			}
			_ = json.Unmarshal(params, &p)
			if p.SessionID == "s2" {
				scDetached.Add(1)
			}
			return map[string]any{}, nil
		case "Page.startScreencast":
			scStarted.Add(1)
			return map[string]any{}, nil
		case "Page.stopScreencast":
			scStopped.Add(1)
			return map[string]any{}, nil
		case "Page.captureScreenshot":
			shotCalls.Add(1)
			<-hangDone
			return nil, fmt.Errorf("closed")
		case "Page.getFrameTree":
			return map[string]any{"frameTree": map[string]any{"frame": map[string]any{"id": "main-f", "url": "http://example.test/"}}}, nil
		case "Page.createIsolatedWorld":
			return map[string]any{"executionContextId": 99}, nil
		case "DOM.getDocument":
			// Renderer is still alive even though captureScreenshot stalled.
			return map[string]any{"root": map[string]any{"nodeId": 1}}, nil
		case "Runtime.evaluate":
			return map[string]any{"result": map[string]any{"type": "string", "value": "still-alive"}}, nil
		default:
			return map[string]any{}, nil
		}
	})

	orig := RegisteredBackends()
	t.Cleanup(func() {
		for k := range RegisteredBackends() {
			RegisterBackend(k, nil)
		}
		for k, v := range orig {
			RegisterBackend(k, v)
		}
	})
	chrom := NewChromiumBackend()
	RegisterBackend(string(KindConnected), chrom)
	m := NewManager()
	m.env = envLookup(map[string]string{})
	req := InvokeRequest{Cwd: t.TempDir(), OwnerSessionID: "shot-to-sess"}
	defer m.ReleaseAllTabs(context.Background(), true)

	ctx := context.Background()
	openP, _ := json.Marshal(map[string]any{"action": "open", "name": "shot", "app": srv.URL})
	if _, err := m.Invoke(ctx, req, openP); err != nil {
		t.Fatal(err)
	}

	// Call screenshot with 3s tool timeout (quickOpMs = 1000ms) -> times out cleanly.
	_, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"shot","timeout":3,"chain":[{"method":"screenshot","args":[]}]}`))
	if err == nil || !strings.Contains(err.Error(), "tab.screenshot() timed out after 1000ms") {
		t.Fatalf("expected named screenshot timeout, got: %v", err)
	}
	if scStarted.Load() != 1 || scStopped.Load() != 1 || scDetached.Load() != 1 {
		t.Fatalf("expected separate screencast session started/stopped/detached once, got start=%d stop=%d detach=%d",
			scStarted.Load(), scStopped.Load(), scDetached.Load())
	}

	// Tab must still be open and usable for the next call!
	res, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"shot","chain":[{"method":"text","args":["#ok"]}]}`))
	if err != nil || res.Details.Value != "still-alive" {
		t.Fatalf("expected tab usable after screenshot timeout, got res=%#v err=%v", res, err)
	}
}
