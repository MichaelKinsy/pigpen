//go:build !windows

package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/approval"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/relay"
)

// TestRealChromeRelayEndToEnd exercises the complete oh-my-pig Chrome relay stack
// against real Chrome for Testing (--headless=new, temporary --user-data-dir,
// temporary HOME / PIG_CODING_AGENT_DIR, local httptest server only):
//
//	(a) Chrome launches the built Binary's `browser-relay native-host`; link/linked/hello
//	    complete and `<pid>-<port>` endpoint file appears with 0600 permissions & bearer token.
//	(b) browser tool path (`app: "relay"`): list targets, open tab, goto, read text,
//	    click a link that navigates, type into an input, take a screenshot (overlay hidden
//	    and restored), and run a download to get its saved file path via OMP.downloadPath.
//	(c) Two clients on the same tab at once without dropping Chrome's single debugger attachment.
//	(d) Tab closed from Chrome's side under an open session -> clear error surfaced to model.
//	(e) Native host killed while a session is open -> Chrome restarts it -> session sees clean
//	    error and a new open/call works.
//	(f) Second Chrome for Testing instance with a separate temp profile follows the first leader
//	    over `/ext` (authenticated with bearer token) and promotes to leader when the first exits.
//	(g) Approval gate (`tools.approvalMode` / `tools.approval.browser: deny`) refuses the `browser`
//	    prelude before anything reaches Chrome.
//
// Gated behind OMP_REAL_BROWSER=1 and OMP_RELAY_BINARY, the path of a built oh-my-pig Binary: Chrome starts
// that Binary as the native host, which the test binary cannot be.
func TestRealChromeRelayEndToEnd(t *testing.T) {
	if os.Getenv("OMP_REAL_BROWSER") == "" {
		t.Skip("skipping real Chrome for Testing relay test (set OMP_REAL_BROWSER=1 to run)")
	}
	if runtime.GOOS == "windows" {
		t.Skip("POSIX launcher test; Windows uses HKCU registry lookup")
	}

	realHome := os.Getenv("HOME")
	chromeBin := findRealChromeForTesting(realHome)
	if chromeBin == "" {
		t.Skip("Chrome for Testing binary not found")
	}

	relayBin := os.Getenv("OMP_RELAY_BINARY")
	if relayBin == "" {
		t.Skip("set OMP_RELAY_BINARY to a built oh-my-pig Binary to run the relay end-to-end test")
	}
	if st, err := os.Stat(relayBin); err != nil || st.IsDir() {
		t.Fatalf("OMP_RELAY_BINARY=%s is not a file", relayBin)
	}

	// Isolate HOME, PIG_CODING_AGENT_DIR, PIG_HOME into a temp directory.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(tmp, "agent"))
	t.Setenv("PIG_HOME", tmp)

	// Local httptest server: pages + navigating link + input field + downloadable file.
	var pageHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			pageHits.Add(1)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, `<!doctype html><html><head><title>Relay Start</title></head><body>
				<h1 id="heading">Hello Relay Start</h1>
				<a id="next-link" href="/page2">Go to Page 2</a>
			</body></html>`)
		case "/page2":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, `<!doctype html><html><head><title>Relay Page 2</title></head><body>
				<h1 id="h2">Arrived at Page 2</h1>
				<input id="name-input" type="text" value="" />
				<a id="dl-link" href="/file.csv" download="report.csv">Download CSV</a>
			</body></html>`)
		case "/file.csv":
			w.Header().Set("Content-Type", "text/csv")
			w.Header().Set("Content-Disposition", `attachment; filename="report.csv"`)
			_, _ = io.WriteString(w, "col1,col2\nalpha,42\n")
		case "/profile2":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, `<!doctype html><html><head><title>Second Chrome Profile</title></head><body><p>Profile Two Body</p></body></html>`)
		case "/blocked":
			pageHits.Add(1)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, `<!doctype html><html><body>Should never load</body></html>`)
		default:
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, `<!doctype html><html><head><title>Home</title></head><body>Home</body></html>`)
		}
	}))
	defer srv.Close()

	// Install the embedded relay-only extension and write the native host launcher pointing at relayBin.
	installDir, err := relay.InstallRelayExtension(relay.DefaultExtensionDir(), relay.InstallLocations{
		RelayURLSet: true,
	})
	if err != nil {
		t.Fatalf("InstallRelayExtension: %v", err)
	}
	extID := relay.ExtensionIDForPath(installDir, runtime.GOOS)

	// Wrap relayBin so Chrome's spawned native host inherits our isolated temp env
	// (Chrome on macOS can strip or alter env vars; explicit export guarantees temp isolation).
	wrapper := filepath.Join(tmp, "run-oh-my-pig.sh")
	wrapperBody := fmt.Sprintf("#!/bin/sh\nexport HOME=%q\nexport PIG_CODING_AGENT_DIR=%q\nexport PIG_HOME=%q\nexec %q \"$@\"\n",
		tmp, filepath.Join(tmp, "agent"), tmp, relayBin)
	if err := os.WriteFile(wrapper, []byte(wrapperBody), 0o700); err != nil {
		t.Fatal(err)
	}
	targets, err := relay.WriteRelayNativeHost([]string{extID}, relay.NativeHostOptions{
		Platform: runtime.GOOS,
		Home:     tmp,
		Dir:      relay.Root(),
		Command:  []string{wrapper},
	})
	if err != nil || len(targets) == 0 {
		t.Fatalf("WriteRelayNativeHost: %v (targets=%v)", err, targets)
	}
	manifestBytes, err := os.ReadFile(targets[0])
	if err != nil {
		t.Fatal(err)
	}

	dlDir := filepath.Join(tmp, "downloads")
	if err := os.MkdirAll(dlDir, 0o755); err != nil {
		t.Fatal(err)
	}

	launchChrome := func(profileName, startURL string) (*exec.Cmd, string, string) {
		userDataDir := filepath.Join(tmp, profileName)
		nmhDir := filepath.Join(userDataDir, "NativeMessagingHosts")
		if err := os.MkdirAll(nmhDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(nmhDir, relay.NativeHostName+".json"), manifestBytes, 0o644); err != nil {
			t.Fatal(err)
		}
		// Point Chrome's default downloads folder into our temp dir so downloads never touch real ~/Downloads.
		defDir := filepath.Join(userDataDir, "Default")
		if err := os.MkdirAll(defDir, 0o755); err != nil {
			t.Fatal(err)
		}
		prefs := map[string]any{
			"download": map[string]any{
				"default_directory":   dlDir,
				"prompt_for_download": false,
			},
		}
		pb, _ := json.Marshal(prefs)
		if err := os.WriteFile(filepath.Join(defDir, "Preferences"), pb, 0o644); err != nil {
			t.Fatal(err)
		}

		cmd := exec.Command(chromeBin,
			"--headless=new",
			"--no-first-run",
			"--no-default-browser-check",
			"--use-mock-keychain",
			"--password-store=basic",
			"--no-proxy-server",
			"--disable-background-timer-throttling",
			"--disable-backgrounding-occluded-windows",
			"--disable-renderer-backgrounding",
			"--remote-debugging-port=0",
			"--user-data-dir="+userDataDir,
			"--disable-extensions-except="+installDir,
			"--load-extension="+installDir,
			startURL,
		)
		cmd.Env = append(os.Environ(),
			"HOME="+tmp,
			"PIG_CODING_AGENT_DIR="+filepath.Join(tmp, "agent"),
			"PIG_HOME="+tmp,
		)
		if err := cmd.Start(); err != nil {
			t.Fatalf("start Chrome (%s): %v", profileName, err)
		}
		t.Cleanup(func() {
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
				_, _ = cmd.Process.Wait()
			}
		})
		// Wait for DevToolsActivePort so we can inspect/close tabs from Chrome's own side in (d).
		var cdpPort string
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			if b, err := os.ReadFile(filepath.Join(userDataDir, "DevToolsActivePort")); err == nil && len(b) > 0 {
				cdpPort = strings.TrimSpace(strings.Split(string(b), "\n")[0])
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		return cmd, userDataDir, cdpPort
	}

	// ---- (a) Launch Chrome #1 -> native-host starts, links, says hello, publishes endpoint file ----
	chrome1, _, chrome1DevPort := launchChrome("chrome-profile-1", srv.URL+"/")

	var ep relay.DiscoveredEndpoint
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		entries := relay.DiscoverRelayEndpointEntries()
		if len(entries) > 0 {
			out, _ := relay.WaitForRelayExtension(context.Background(), entries[0].URL, &relay.WaitOptions{HelloWindow: 2 * time.Second})
			if out == relay.RelayOutcomeReady {
				ep = entries[0]
				break
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	if ep.URL == "" {
		t.Fatalf("(a) Chrome did not start native-host and become ready within 20s (entries=%+v)", relay.DiscoverRelayEndpointEntries())
	}
	t.Logf("(a) OK: endpoint=%s pid=%d tokenLen=%d", ep.URL, ep.PID, len(ep.Token))

	// Register RelayBackend for Manager.
	prevRelay, _ := LookupBackend(string(KindRelay))
	cb := NewChromiumBackend()
	RegisterBackend(string(KindRelay), NewRelayBackend(cb))
	defer RegisterBackend(string(KindRelay), prevRelay)

	mgr := NewManager()
	mgr.env = envLookup(map[string]string{})
	sRelay := DefaultSettings()
	ctx := context.Background()
	req := InvokeRequest{Cwd: tmp, OwnerSessionID: "real-sess-1", Settings: &sRelay}

	// ---- (b) list targets, open, goto, text, click navigating link, type, screenshot, download ----
	tRes0, err := mgr.Invoke(ctx, req, json.RawMessage(`{"action":"targets","app":"relay"}`))
	if err != nil {
		t.Fatalf("(b) initial targets failed: %v", err)
	}
	t.Logf("(b) initial targets: %s", strings.ReplaceAll(tRes0.Text, "\n", " | "))
	openRes, err := mgr.Invoke(ctx, req, json.RawMessage(fmt.Sprintf(`{"action":"open","name":"main","app":"relay","url":%q,"timeout":10}`, srv.URL+"/start")))
	if err != nil {
		if tab := mgr.getLiveTab(ctx, tabKey{owner: "real-sess-1", name: "main"}); tab != nil {
			if rt, ok := tab.tab.(*relayTab); ok {
				t.Logf("CDP trace:\n%s", rt.CDPTrace(40))
			}
		}
		t.Fatalf("(b) open failed (pageHits=%d): %v", pageHits.Load(), err)
	}
	t.Logf("(b) open: %s", openRes.Text)
	tRes, err := mgr.Invoke(ctx, req, json.RawMessage(`{"action":"targets","app":"relay"}`))
	if err != nil || !strings.Contains(tRes.Text, srv.URL+"/start") {
		t.Fatalf("(b) targets after open failed: res=%+v err=%v", tRes, err)
	}
	t.Logf("(b) targets after open: %s", strings.ReplaceAll(tRes.Text, "\n", " | "))

	txtRes, err := mgr.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"main","chain":[{"method":"text","args":["#heading"]}]}`))
	if err != nil || !strings.Contains(fmt.Sprint(txtRes.Details.Value), "Hello Relay Start") {
		t.Fatalf("(b) text failed: res=%+v err=%v", txtRes, err)
	}
	t.Logf("(b) text(#heading): %v", txtRes.Details.Value)

	// Click navigating link -> /page2.
	if _, err := mgr.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"main","chain":[{"method":"click","args":["#next-link"]}]}`)); err != nil {
		t.Fatalf("(b) click(#next-link) failed: %v", err)
	}
	if _, err := mgr.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"main","chain":[{"method":"waitForUrl","args":["/page2"]}]}`)); err != nil {
		t.Fatalf("(b) waitForUrl(/page2) failed: %v", err)
	}
	h2Res, err := mgr.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"main","chain":[{"method":"text","args":["#h2"]}]}`))
	if err != nil || !strings.Contains(fmt.Sprint(h2Res.Details.Value), "Arrived at Page 2") {
		t.Fatalf("(b) text after click nav failed: res=%+v err=%v", h2Res, err)
	}
	// Type into #name-input and read value back.
	if _, err := mgr.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"main","chain":[{"method":"type","args":["#name-input","oh-my-pig"]}]}`)); err != nil {
		t.Fatalf("(b) type failed: %v", err)
	}
	valRes, err := mgr.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"main","chain":[{"method":"value","args":["#name-input"]}]}`))
	if err != nil || fmt.Sprint(valRes.Details.Value) != "oh-my-pig" {
		t.Fatalf("(b) value(#name-input) = %v (err=%v), want oh-my-pig", valRes, err)
	}
	t.Logf("(b) typed & verified value: %v", valRes.Details.Value)
	// Activate the tab through Chrome's own DevTools HTTP endpoint (as a user
	// selecting the tab would) before taking the screenshot: `--headless=new`
	// does not produce compositor frames for unselected background tabs and
	// rejects `fromSurface: false` ("Only screenshots from surface are allowed."),
	// whereas product code must never activate a tab in the user's real Chrome.
	if chrome1DevPort == "" {
		t.Fatalf("(b) chrome1DevPort empty; cannot activate tab via Chrome /json/activate")
	}
	var actTargets []struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	cResp, err := http.Get("http://127.0.0.1:" + chrome1DevPort + "/json/list")
	if err != nil {
		t.Fatalf("(b) Chrome /json/list failed: %v", err)
	}
	_ = json.NewDecoder(cResp.Body).Decode(&actTargets)
	cResp.Body.Close()
	var page2ID string
	for _, ct := range actTargets {
		if strings.Contains(ct.URL, "/page2") {
			page2ID = ct.ID
			break
		}
	}
	if page2ID == "" {
		t.Fatalf("(b) could not find /page2 target on Chrome /json/list: %+v", actTargets)
	}
	actResp, err := http.Get("http://127.0.0.1:" + chrome1DevPort + "/json/activate/" + page2ID)
	if err != nil {
		t.Fatalf("(b) Chrome /json/activate failed: %v", err)
	}
	actResp.Body.Close()

	// Screenshot (overlay hidden during capture, restored after).
	ssRes, err := mgr.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"main","chain":[{"method":"screenshot"}]}`))
	if err != nil || len(ssRes.Images) != 1 {
		t.Fatalf("(b) screenshot failed: res=%+v err=%v", ssRes, err)
	}
	pngBytes, err := base64.StdEncoding.DecodeString(ssRes.Images[0].Data)
	if err != nil || len(pngBytes) < 100 {
		t.Fatalf("(b) invalid screenshot PNG: len=%d err=%v", len(pngBytes), err)
	}
	t.Logf("(b) screenshot: %s (%d bytes)", ssRes.Text, len(pngBytes))
	// Download via clicking #dl-link and checking waitForDownload / downloads().
	dlDone := make(chan struct {
		res *InvokeResult
		err error
	}, 1)
	go func() {
		r, e := mgr.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"main","chain":[{"method":"waitForDownload","args":[{"timeout":10000}]}]}`))
		dlDone <- struct {
			res *InvokeResult
			err error
		}{r, e}
	}()
	time.Sleep(150 * time.Millisecond)
	if _, err := mgr.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"main","chain":[{"method":"click","args":["#dl-link"]}]}`)); err != nil {
		t.Fatalf("(b) click(#dl-link) failed: %v", err)
	}
	dlOutcome := <-dlDone
	if dlOutcome.err != nil {
		t.Fatalf("(b) waitForDownload failed: %v", dlOutcome.err)
	}
	dlJSON, _ := json.Marshal(dlOutcome.res.Details.Value)
	t.Logf("(b) download: %s", string(dlJSON))
	if !strings.Contains(string(dlJSON), "report") {
		t.Fatalf("(b) expected report.csv download path, got %s", string(dlJSON))
	}

	// Also exercise explicit goto.
	if _, err := mgr.Invoke(ctx, req, json.RawMessage(fmt.Sprintf(`{"action":"call","name":"main","chain":[{"method":"goto","args":[%q]}]}`, srv.URL+"/start"))); err != nil {
		t.Fatalf("(b) goto failed: %v", err)
	}

	// ---- (c) Two clients on the same tab at once ----
	req2 := InvokeRequest{Cwd: tmp, OwnerSessionID: "real-sess-2", Settings: &sRelay}
	if _, err := mgr.Invoke(ctx, req2, json.RawMessage(`{"action":"open","name":"peer","app":"relay","target":"Relay Start"}`)); err != nil {
		t.Fatalf("(c) second client adopt failed: %v", err)
	}
	r1, err1 := mgr.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"main","chain":[{"method":"text","args":["#heading"]}]}`))
	r2, err2 := mgr.Invoke(ctx, req2, json.RawMessage(`{"action":"call","name":"peer","chain":[{"method":"text","args":["#heading"]}]}`))
	if err1 != nil || err2 != nil {
		t.Fatalf("(c) two clients on same tab failed: err1=%v err2=%v", err1, err2)
	}
	t.Logf("(c) OK: client1=%v client2=%v", r1.Details.Value, r2.Details.Value)

	// ---- (d) Tab closed from Chrome's side under an open session ----
	if chrome1DevPort == "" {
		t.Fatal("(d) missing DevToolsActivePort for Chrome #1")
	}
	// Find the /start tab in Chrome's own /json/list and close it directly via /json/close/<id>.
	var chromeTargets []struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		URL  string `json:"url"`
	}
	respList, err := http.Get("http://127.0.0.1:" + chrome1DevPort + "/json/list")
	if err != nil {
		t.Fatalf("(d) GET Chrome /json/list: %v", err)
	}
	_ = json.NewDecoder(respList.Body).Decode(&chromeTargets)
	_ = respList.Body.Close()
	closedChromeTarget := false
	for _, ct := range chromeTargets {
		if ct.Type == "page" && strings.Contains(ct.URL, "/start") {
			rc, err := http.Get("http://127.0.0.1:" + chrome1DevPort + "/json/close/" + ct.ID)
			if err == nil {
				_ = rc.Body.Close()
				closedChromeTarget = true
				break
			}
		}
	}
	if !closedChromeTarget {
		t.Fatalf("(d) could not find /start tab in Chrome /json/list: %+v", chromeTargets)
	}
	time.Sleep(300 * time.Millisecond)
	_, closedErr := mgr.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"main","chain":[{"method":"text"}]}`))
	if closedErr == nil {
		t.Fatal("(d) expected error after tab closed from Chrome side")
	}
	t.Logf("(d) OK: model error after Chrome closed tab: %v", closedErr)
	_, _ = mgr.Invoke(ctx, req2, json.RawMessage(`{"action":"close","name":"peer"}`))

	// ---- (e) Kill native host while a session is open -> Chrome restarts it -> new call works ----
	if _, err := mgr.Invoke(ctx, req, json.RawMessage(fmt.Sprintf(`{"action":"open","name":"survive","app":"relay","url":%q}`, srv.URL+"/start"))); err != nil {
		t.Fatalf("(e) open survive tab failed: %v", err)
	}
	oldPID := ep.PID
	if err := syscall.Kill(oldPID, syscall.SIGKILL); err != nil {
		t.Fatalf("(e) kill native host pid %d: %v", oldPID, err)
	}
	time.Sleep(300 * time.Millisecond)
	_, killedErr := mgr.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"survive","chain":[{"method":"text"}]}`))
	if killedErr == nil {
		t.Fatal("(e) expected error on open session after native host SIGKILL")
	}
	t.Logf("(e) error seen by open session after SIGKILL: %v", killedErr)

	// Wait for Chrome extension to reconnect and spawn a fresh native host.
	var newEP relay.DiscoveredEndpoint
	deadline = time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		for _, cand := range relay.DiscoverRelayEndpointEntries() {
			if cand.PID != oldPID {
				out, _ := relay.WaitForRelayExtension(context.Background(), cand.URL, &relay.WaitOptions{HelloWindow: 2 * time.Second})
				if out == relay.RelayOutcomeReady {
					newEP = cand
					break
				}
			}
		}
		if newEP.URL != "" {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if newEP.URL == "" {
		t.Fatalf("(e) Chrome did not restart native host after SIGKILL (entries=%+v)", relay.DiscoverRelayEndpointEntries())
	}
	t.Logf("(e) Chrome restarted native host: oldPID=%d newPID=%d url=%s", oldPID, newEP.PID, newEP.URL)
	if _, err := mgr.Invoke(ctx, req, json.RawMessage(fmt.Sprintf(`{"action":"open","name":"after-restart","app":"relay","url":%q}`, srv.URL+"/start"))); err != nil {
		t.Fatalf("(e) new open after restart failed: %v", err)
	}
	afterTxt, err := mgr.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"after-restart","chain":[{"method":"text","args":["#heading"]}]}`))
	if err != nil || !strings.Contains(fmt.Sprint(afterTxt.Details.Value), "Hello Relay Start") {
		t.Fatalf("(e) call after restart failed: res=%+v err=%v", afterTxt, err)
	}
	t.Logf("(e) OK: new call after restart succeeded: %v", afterTxt.Details.Value)

	// ---- (f) Second Chrome for Testing instance follows first leader and takes over on exit ----
	chrome2, _, _ := launchChrome("chrome-profile-2", srv.URL+"/profile2")
	_ = chrome2

	// Wait for Chrome #2's tab to appear through the first leader's relay.
	sawProfile2 := false
	deadline = time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		res, err := mgr.Invoke(ctx, req, json.RawMessage(`{"action":"targets","app":"relay"}`))
		if err == nil && strings.Contains(res.Text, "/profile2") {
			sawProfile2 = true
			t.Logf("(f) follower joined leader; targets=%s", strings.ReplaceAll(res.Text, "\n", " | "))
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !sawProfile2 {
		t.Fatalf("(f) second Chrome profile did not appear through first leader")
	}

	// Kill Chrome #1 (and its leader native host if still alive) -> Chrome #2's follower promotes to leader.
	leaderBefore := newEP.PID
	_ = chrome1.Process.Kill()
	_, _ = chrome1.Process.Wait()
	_ = syscall.Kill(leaderBefore, syscall.SIGTERM)

	var promoted relay.DiscoveredEndpoint
	deadline = time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		for _, cand := range relay.DiscoverRelayEndpointEntries() {
			if cand.PID != leaderBefore {
				out, _ := relay.WaitForRelayExtension(context.Background(), cand.URL, &relay.WaitOptions{HelloWindow: 2 * time.Second})
				if out == relay.RelayOutcomeReady {
					promoted = cand
					break
				}
			}
		}
		if promoted.URL != "" {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if promoted.URL == "" {
		t.Fatalf("(f) follower did not promote to leader after Chrome #1 exited (entries=%+v)", relay.DiscoverRelayEndpointEntries())
	}
	resAfterHandover, err := mgr.Invoke(ctx, req, json.RawMessage(`{"action":"targets","app":"relay"}`))
	if err != nil || !strings.Contains(resAfterHandover.Text, "/profile2") {
		t.Fatalf("(f) targets after handover failed: res=%+v err=%v", resAfterHandover, err)
	}
	t.Logf("(f) OK: follower promoted (pid=%d url=%s); targets=%s", promoted.PID, promoted.URL, strings.ReplaceAll(resAfterHandover.Text, "\n", " | "))

	// ---- (g) Approval gate refuses browser prelude before anything reaches Chrome ----
	hitsBefore := pageHits.Load()
	prevSetting := approval.Setting
	approval.Setting = func(_ sdk.Context, key string) any {
		switch key {
		case "tools.approvalMode":
			return "readonly"
		case "tools.approval.browser":
			return "deny"
		}
		return nil
	}
	defer func() { approval.Setting = prevSetting }()

	fakeSDK := sdk.Context{}
	denyErr := approval.CheckPrelude(fakeSDK, "browser", "exec", json.RawMessage(fmt.Sprintf(`{"action":"open","app":"relay","url":%q}`, srv.URL+"/blocked")))
	if denyErr == nil {
		t.Fatal("(g) expected approval.CheckPrelude to deny browser prelude")
	}
	if pageHits.Load() != hitsBefore {
		t.Fatalf("(g) Chrome hit /blocked despite approval denial")
	}
	t.Logf("(g) OK: approval gate refused browser prelude before reaching Chrome: %v", denyErr)
}

func findRealChromeForTesting(realHome string) string {
	if p := os.Getenv("OMP_CHROME_PATH"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if realHome == "" {
		return ""
	}
	candidates, _ := filepath.Glob(filepath.Join(
		realHome, ".omp", "puppeteer", "chrome", "*", "*",
		"Google Chrome for Testing.app", "Contents", "MacOS", "Google Chrome for Testing",
	))
	if len(candidates) > 0 {
		return candidates[len(candidates)-1]
	}
	linuxCandidates, _ := filepath.Glob(filepath.Join(
		realHome, ".omp", "puppeteer", "chrome", "*", "chrome-linux64", "chrome",
	))
	if len(linuxCandidates) > 0 {
		return linuxCandidates[len(linuxCandidates)-1]
	}
	return ""
}
