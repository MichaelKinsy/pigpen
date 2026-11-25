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
	"sync"
	"testing"
	"time"
)

func setupRealBrowserTest(t *testing.T) (*Manager, *ChromiumBackend, InvokeRequest) {
	t.Helper()
	if os.Getenv("OMP_REAL_BROWSER") == "" {
		t.Skip("skipping real headless Chromium test (set OMP_REAL_BROWSER=1 to run)")
	}
	if _, err := FindChromiumExecutable(os.LookupEnv); err != nil {
		t.Skipf("no Chromium executable: %v", err)
	}
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
	RegisterBackend(string(KindHeadless), chrom)

	m := NewManager()
	m.env = envLookup(map[string]string{})
	s := DefaultSettings()
	s.Tern, s.WebKit, s.Cmux, s.Relay = false, false, false, false
	s.Headless = true
	s.ScreenshotDir = t.TempDir()
	m.SetSettingsProvider(func() Settings { return s })
	req := InvokeRequest{Cwd: t.TempDir(), OwnerSessionID: "pass2-" + t.Name()}
	t.Cleanup(func() {
		// Capture any launched PIDs & profile dirs and verify full OS cleanup after release.
		chrom.mu.Lock()
		var pids []int
		var dirs []string
		for _, p := range chrom.browsers {
			if p.launched != nil {
				pids = append(pids, p.launched.pid)
				dirs = append(dirs, p.launched.userDataDir)
			}
		}
		chrom.mu.Unlock()
		m.ReleaseAllTabs(context.Background(), true)
		for _, d := range dirs {
			if d != "" {
				if _, err := os.Stat(d); !os.IsNotExist(err) {
					t.Errorf("leftover temp profile dir %q", d)
				}
			}
		}
		for _, pid := range pids {
			if processAlive(pid) {
				t.Errorf("leftover Chromium pid %d", pid)
			}
		}
	})
	return m, chrom, req
}

// Pass-2 Finding 1 (real browser): open() on page calling confirm() during load
// fails fast (no hang), releases lease & per-name lock so same tab name works next.
func TestRealPass2_F1_OpenWithConfirmDuringLoad(t *testing.T) {
	m, _, req := setupRealBrowserTest(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.URL.Path == "/ok" {
			_, _ = io.WriteString(w, `<!doctype html><html><head><title>Recovered</title></head><body>OK</body></html>`)
			return
		}
		_, _ = io.WriteString(w, `<!doctype html><html><body><script>confirm("Blocking load?");</script></body></html>`)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	// Warm up Chromium once so the 4s timeout below measures confirm()-during-load
	// handling rather than cold browser startup under heavy CPU load.
	warmP, _ := json.Marshal(map[string]any{"action": "open", "name": "f1-warm", "app": "chromium"})
	if _, err := m.Invoke(ctx, req, warmP); err != nil {
		t.Fatalf("warm open failed: %v", err)
	}

	openDlg, _ := json.Marshal(map[string]any{"action": "open", "name": "f1", "url": srv.URL + "/dlg", "app": "chromium", "timeout": 12})
	_, err := m.Invoke(ctx, req, openDlg)
	if err == nil || !strings.Contains(err.Error(), "dialog") {
		t.Fatalf("expected dialog-blocks error on open, got err=%v", err)
	}
	openOK, _ := json.Marshal(map[string]any{"action": "open", "name": "f1", "url": srv.URL + "/ok", "app": "chromium"})
	res, err := m.Invoke(ctx, req, openOK)
	if err != nil || !strings.Contains(res.Text, "Recovered") {
		t.Fatalf("reopening same tab name after dialog failure failed: res=%+v err=%v", res, err)
	}
}

// Pass-2 Finding 2 (real browser, security): tab.id(n) runs strictly in isolated world;
// concurrent id() calls do not collide; hostile page overriding JSON/Promise/Array.prototype.map/
// document.querySelector/__ompBrowserKit/__ompResolvedEl cannot hijack id() click or reads.
func TestRealPass2_F2_IsolatedElementHandlesHostilePageAndConcurrency(t *testing.T) {
	m, _, req := setupRealBrowserTest(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<!doctype html><html><body>
  <button id="btnA" onclick="window.__clickedA = true">Alpha</button>
  <button id="btnB" onclick="window.__clickedB = true">Beta</button>
  <script>
    // Hostile page tries to hijack globals and browser kit names.
    globalThis.__ompBrowserKit = {
      isVisible: () => "HIJACKED",
      isEnabled: () => "HIJACKED",
      checkClickActionable: () => ({ ok: true, x: 3, y: 3 }),
      queryFirst: () => null,
      queryAll: () => []
    };
    globalThis.__ompResolvedEl = document.body;
    globalThis.JSON = { stringify: () => "EVIL", parse: () => ({}) };
    globalThis.Promise = function() { throw new Error("evil promise"); };
    Array.prototype.map = function() { return ["EVIL"]; };
    document.querySelector = () => null;
    document.querySelectorAll = () => [];
  </script>
</body></html>`)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	openP, _ := json.Marshal(map[string]any{"action": "open", "name": "f2", "url": srv.URL, "app": "chromium"})
	if _, err := m.Invoke(ctx, req, openP); err != nil {
		t.Fatal(err)
	}

	obsRes, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"f2","chain":[{"method":"observe","args":[]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	obs := obsRes.Details.Value.(Observation)
	var idA, idB int
	for _, el := range obs.Elements {
		if el.Name == "Alpha" {
			idA = el.ID
		} else if el.Name == "Beta" {
			idB = el.ID
		}
	}
	if idA == 0 || idB == 0 {
		t.Fatalf("expected Alpha & Beta ids, got %+v", obs.Elements)
	}

	// Hostile page cannot spoof isVisible or hijack click coordinates.
	visRes, err := m.Invoke(ctx, req, json.RawMessage(fmt.Sprintf(`{"action":"call","name":"f2","chain":[{"method":"id","args":[%d]},{"method":"isVisible","args":[]}]}`, idA)))
	if err != nil || visRes.Details.Value != true {
		t.Fatalf("expected true isVisible on hostile page, got %#v err=%v", visRes, err)
	}
	if _, err := m.Invoke(ctx, req, json.RawMessage(fmt.Sprintf(`{"action":"call","name":"f2","chain":[{"method":"id","args":[%d]},{"method":"click","args":[]}]}`, idA))); err != nil {
		t.Fatalf("id(%d).click() on hostile page failed: %v", idA, err)
	}
	hitA, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"f2","chain":[{"method":"evaluate","args":["Boolean(window.__clickedA)"]}]}`))
	if err != nil || hitA.Details.Value != true {
		t.Fatalf("expected Alpha button to be clicked despite hostile page, got %#v err=%v", hitA, err)
	}

	// 20 concurrent id(idA).attr("id") / id(idB).attr("id") calls must never cross-talk.
	var wg sync.WaitGroup
	errCh := make(chan error, 40)
	for range 20 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			r, err := m.Invoke(ctx, req, json.RawMessage(fmt.Sprintf(`{"action":"call","name":"f2","chain":[{"method":"id","args":[%d]},{"method":"attr","args":["id"]}]}`, idA)))
			if err != nil || r.Details.Value != "btnA" {
				errCh <- fmt.Errorf("idA attr mismatch: val=%#v err=%v", r, err)
			}
		}()
		go func() {
			defer wg.Done()
			r, err := m.Invoke(ctx, req, json.RawMessage(fmt.Sprintf(`{"action":"call","name":"f2","chain":[{"method":"id","args":[%d]},{"method":"attr","args":["id"]}]}`, idB)))
			if err != nil || r.Details.Value != "btnB" {
				errCh <- fmt.Errorf("idB attr mismatch: val=%#v err=%v", r, err)
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for e := range errCh {
		t.Fatal(e)
	}
}

// Pass-2 Findings 3, 4, 7, 10, 11 (real browser):
// - F3: tab.id(n).click() opening confirm() finishes fast via dialog race
// - F4: waitFor / waitForSelector return bool (true on match, false on hidden-missing)
// - F7: tab.ref(eN).evaluate(fn) runs in main world and sees page globals
// - F10: fill/focus/hover/check/select auto-wait for delayed element
// - F11: nested iframe (iframe inside iframe) click hits exact coordinates
func TestRealPass2_F3_F4_F7_F10_F11(t *testing.T) {
	m, _, req := setupRealBrowserTest(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		switch r.URL.Path {
		case "/inner2":
			_, _ = io.WriteString(w, `<!doctype html><html><body style="margin:0"><button id="deep-btn" style="width:100px;height:40px" onclick="document.getElementById('deep-out').textContent='nested-hit'">Deep</button><span id="deep-out">no</span></body></html>`)
		case "/inner1":
			_, _ = io.WriteString(w, `<!doctype html><html><body style="margin:0"><iframe id="f2" name="inner2" src="/inner2" style="margin-left:60px;margin-top:50px;width:160px;height:100px;border:0"></iframe></body></html>`)
		default:
			_, _ = io.WriteString(w, `<!doctype html><html><body style="margin:0">
  <button id="dlg-id" onclick="window.__idDlg = confirm('Sure?')">IdDialog</button>
  <button id="ref-btn">RefTarget</button>
  <div id="late-host"></div>
  <iframe id="f1" name="inner1" src="/inner1" style="margin-left:90px;margin-top:70px;width:260px;height:200px;border:0"></iframe>
  <script>
    window.__pageSecret = "main-world-ok";
    setTimeout(() => {
      const inp = document.createElement("input");
      inp.id = "late-input";
      document.getElementById("late-host").appendChild(inp);
    }, 350);
  </script>
</body></html>`)
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	openP, _ := json.Marshal(map[string]any{"action": "open", "name": "f3", "url": srv.URL, "app": "chromium"})
	if _, err := m.Invoke(ctx, req, openP); err != nil {
		t.Fatal(err)
	}

	// F10: fill auto-waits for #late-input added after 350ms.
	if _, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"f3","chain":[{"method":"fill","args":["#late-input","waited"]}]}`)); err != nil {
		t.Fatalf("F10 fill auto-wait failed: %v", err)
	}
	vRes, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"f3","chain":[{"method":"value","args":["#late-input"]}]}`))
	if err != nil || vRes.Details.Value != "waited" {
		t.Fatalf("F10 expected 'waited', got %#v err=%v", vRes, err)
	}

	// F4: waitFor returns true on match; waitForSelector hidden on missing returns false.
	wf1, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"f3","chain":[{"method":"waitFor","args":["#ref-btn"]}]}`))
	if err != nil || wf1.Details.Value != true {
		t.Fatalf("F4 waitFor expected true, got %#v err=%v", wf1, err)
	}
	wf2, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"f3","chain":[{"method":"waitForSelector","args":["#absent",{"hidden":true}]}]}`))
	if err != nil || wf2.Details.Value != false {
		t.Fatalf("F4 waitForSelector(hidden, absent) expected false, got %#v err=%v", wf2, err)
	}

	// F7: tab.ref(eN).evaluate(fn) sees main-world page globals and returns element id.
	ariaRes, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"f3","chain":[{"method":"ariaSnapshot","args":[]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	ariaStr, _ := ariaRes.Details.Value.(string)
	var refID string
	for _, line := range strings.Split(ariaStr, "\n") {
		if strings.Contains(line, "RefTarget") {
			refID = extractRef(line)
			break
		}
	}
	if refID == "" {
		t.Fatalf("could not find RefTarget ref in ariaSnapshot:\n%s", ariaStr)
	}
	refEval, err := m.Invoke(ctx, req, json.RawMessage(fmt.Sprintf(
		`{"action":"call","name":"f3","chain":[{"method":"ref","args":[%q]},{"method":"evaluate","args":[{"__omp_fn":"(el) => el.id + ':' + window.__pageSecret"}]}]}`,
		refID,
	)))
	if err != nil || refEval.Details.Value != "ref-btn:main-world-ok" {
		t.Fatalf("F7 tab.ref(%q).evaluate() failed: res=%#v err=%v", refID, refEval, err)
	}

	// F11: Nested iframe (inner2 inside inner1) click lands on #deep-btn.
	if _, err := m.Invoke(ctx, req, json.RawMessage(fmt.Sprintf(
		`{"action":"call","name":"f3","chain":[{"method":"frame","args":[%q]},{"method":"click","args":["#deep-btn"]}]}`,
		srv.URL+"/inner2",
	))); err != nil {
		t.Fatalf("F11 nested frame click failed: %v", err)
	}
	deepTxt, err := m.Invoke(ctx, req, json.RawMessage(fmt.Sprintf(
		`{"action":"call","name":"f3","chain":[{"method":"frame","args":[%q]},{"method":"text","args":["#deep-out"]}]}`,
		srv.URL+"/inner2",
	)))
	if err != nil || deepTxt.Details.Value != "nested-hit" {
		t.Fatalf("F11 nested frame click missed target: got %#v err=%v", deepTxt, err)
	}

	// F3: tab.id(n).click() opening confirm() finishes fast (not hanging to deadline).
	obsRes, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"f3","chain":[{"method":"observe","args":[]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var dlgBtnID int
	for _, el := range obsRes.Details.Value.(Observation).Elements {
		if el.Name == "IdDialog" {
			dlgBtnID = el.ID
		}
	}
	if dlgBtnID == 0 {
		t.Fatalf("did not find IdDialog in observe()")
	}
	if _, err := m.Invoke(ctx, req, json.RawMessage(fmt.Sprintf(
		`{"action":"call","name":"f3","timeout":12,"chain":[{"method":"id","args":[%d]},{"method":"click","args":[]}]}`,
		dlgBtnID,
	))); err != nil {
		t.Fatalf("F3 id(%d).click() opening confirm failed: %v", dlgBtnID, err)
	}
	if _, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"f3","chain":[{"method":"handleDialog","args":[{"accept":true}]}]}`)); err != nil {
		t.Fatalf("handleDialog after F3 failed: %v", err)
	}
}

// Pass-2 Findings 5 & 6 (real browser):
// - F5: session end immediately cancels an in-flight hanging evaluate(new Promise(...))
// - F6: clearCookies() only deletes cookies for the calling tab's origin, leaving another tab's cookies intact
func TestRealPass2_F5_SessionEndCancelsInflight_And_F6_ScopedClearCookies(t *testing.T) {
	m, _, req := setupRealBrowserTest(t)
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<!doctype html><html><body>Site1</body></html>`)
	}))
	defer srv1.Close()

	// Use 127.0.0.1 for tab c1 and localhost for tab c2 (distinct origins on same server).
	url1 := strings.Replace(srv1.URL, "localhost", "127.0.0.1", 1)
	url2 := strings.Replace(srv1.URL, "127.0.0.1", "localhost", 1)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	p1, _ := json.Marshal(map[string]any{"action": "open", "name": "c1", "url": url1, "app": "chromium"})
	p2, _ := json.Marshal(map[string]any{"action": "open", "name": "c2", "url": url2, "app": "chromium"})
	if _, err := m.Invoke(ctx, req, p1); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Invoke(ctx, req, p2); err != nil {
		t.Fatal(err)
	}

	// Set cookies on both tabs.
	if _, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"c1","chain":[{"method":"setCookies","args":[{"name":"k1","value":"v1"}]}]}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"c2","chain":[{"method":"setCookies","args":[{"name":"k2","value":"v2"}]}]}`)); err != nil {
		t.Fatal(err)
	}

	// F6: clearCookies() on c1 must NOT wipe c2's cookie.
	if _, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"c1","chain":[{"method":"clearCookies","args":[]}]}`)); err != nil {
		t.Fatal(err)
	}
	c1Cookies, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"c1","chain":[{"method":"cookies","args":[]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if arr, _ := c1Cookies.Details.Value.([]map[string]any); len(arr) != 0 {
		t.Fatalf("F6 expected 0 cookies on c1 after clearCookies, got %+v", arr)
	}
	c2Cookies, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"c2","chain":[{"method":"cookies","args":[]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if arr, _ := c2Cookies.Details.Value.([]map[string]any); len(arr) != 1 || arr[0]["name"] != "k2" {
		t.Fatalf("F6 expected c2 cookie k2 preserved, got %+v", arr)
	}

	// F5: Session end immediately cancels an in-flight never-resolving evaluate().
	evalErrCh := make(chan error, 1)
	go func() {
		_, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"c1","timeout":15,"chain":[{"method":"evaluate","args":["new Promise(() => {})"]}]}`))
		evalErrCh <- err
	}()
	time.Sleep(150 * time.Millisecond)
	m.ReleaseTabsForOwner(context.Background(), req.OwnerSessionID, true)
	select {
	case err := <-evalErrCh:
		if err == nil || !strings.Contains(err.Error(), "closed") {
			t.Fatalf("F5 expected 'Tab ... was closed' error on session end, got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("F5 timed out waiting for in-flight evaluate to cancel on session end")
	}
}
