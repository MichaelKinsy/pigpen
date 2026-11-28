package browser

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
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

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/eval"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
)

// --- Findings 5 & 6: waitFor* timeouts/errors, extract/storage/addInitScript/null-reads/emulate shapes ---

func TestWaitForThrowsAndOptionsValidation(t *testing.T) {
	// Strict selector-wait option parsing.
	if _, err := parseSelectorWaitOptions(map[string]any{"bogus": true}); err == nil || !strings.Contains(err.Error(), "unknown option") {
		t.Fatalf("expected unknown option error, got %v", err)
	}
	if _, err := parseSelectorWaitOptions(map[string]any{"visible": true, "hidden": true}); err == nil {
		t.Fatalf("expected visible+hidden conflict error")
	}
	if _, err := parseSelectorWaitOptions(map[string]any{"state": "visible", "hidden": true}); err == nil {
		t.Fatalf("expected state/hidden conflict error")
	}
	w0, err := parseSelectorWaitOptions(map[string]any{"timeout": float64(0), "state": "detached"})
	if err != nil || !w0.HasTimeout || *w0.Timeout != 0 || w0.State != "detached" {
		t.Fatalf("unexpected parsed wait opts: %+v err=%v", w0, err)
	}
	// timeout: 0 means whole budget (cellTimeoutMs - 2000).
	if got := resolveWaitTimeout(60_000, w0.Timeout); got != 58_000 {
		t.Fatalf("timeout:0 expected 58000ms budget, got %d", got)
	}

	srv := startFakeCDPServer(t, func(method string, params json.RawMessage) (any, error) {
		switch method {
		case "Target.createTarget":
			return map[string]any{"targetId": "t1"}, nil
		case "Target.attachToTarget":
			return map[string]any{"sessionId": "s1"}, nil
		case "Page.getFrameTree":
			return map[string]any{"frameTree": map[string]any{"frame": map[string]any{"id": "f1", "url": "http://example.test"}}}, nil
		case "Page.createIsolatedWorld":
			return map[string]any{"executionContextId": 99}, nil
		case "Runtime.evaluate":
			var p struct {
				Expression string `json:"expression"`
			}
			_ = json.Unmarshal(params, &p)
			if strings.Contains(p.Expression, "queryAll") {
				return map[string]any{"result": map[string]any{"type": "object", "value": map[string]any{"ok": false, "count": float64(0)}}}, nil
			}
			return map[string]any{"result": map[string]any{"type": "boolean", "value": false}}, nil
		default:
			return map[string]any{}, nil
		}
	})
	b := NewChromiumBackend()
	tab, err := b.Open(context.Background(), OpenOptions{Name: "w", Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer tab.Close(context.Background(), false)

	_, err = tab.Call(context.Background(), CallContext{TimeoutMs: 5000}, []CallStep{{
		Method: "waitFor",
		Args:   []json.RawMessage{json.RawMessage(`"#missing"`), json.RawMessage(`{"timeout":120}`)},
	}})
	if err == nil || !strings.Contains(err.Error(), "timed out") || !strings.Contains(err.Error(), "matches no elements") {
		t.Fatalf("expected omp timeout error with zero-match hint, got %v", err)
	}
}

func TestReturnShapesExtractStorageInitScriptNullReadsEmulate(t *testing.T) {
	var lastEvalExpr string
	srv := startFakeCDPServer(t, func(method string, params json.RawMessage) (any, error) {
		switch method {
		case "Target.createTarget":
			return map[string]any{"targetId": "t1"}, nil
		case "Target.attachToTarget":
			return map[string]any{"sessionId": "s1"}, nil
		case "Page.getFrameTree":
			return map[string]any{"frameTree": map[string]any{"frame": map[string]any{"id": "f1", "url": "http://example.test"}}}, nil
		case "Page.createIsolatedWorld":
			return map[string]any{"executionContextId": 99}, nil
		case "Page.addScriptToEvaluateOnNewDocument":
			return map[string]any{"identifier": "script-42"}, nil
		case "Runtime.evaluate":
			var p struct {
				Expression string `json:"expression"`
			}
			_ = json.Unmarshal(params, &p)
			lastEvalExpr = p.Expression
			if strings.Contains(p.Expression, "htmlToMarkdown") {
				return map[string]any{"result": map[string]any{"type": "string", "value": "# Heading\n\nBody"}}, nil
			}
			if strings.Contains(p.Expression, "localStorage.getItem") {
				return map[string]any{"result": map[string]any{"type": "string", "value": "val1"}}, nil
			}
			if strings.Contains(p.Expression, "queryFirst") && (strings.Contains(p.Expression, ": null") || strings.Contains(p.Expression, "return null")) {
				return map[string]any{"result": map[string]any{"type": "object", "subtype": "null", "value": nil}}, nil
			}
			return map[string]any{"result": map[string]any{"type": "string", "value": ""}}, nil
		default:
			return map[string]any{}, nil
		}
	})
	b := NewChromiumBackend()
	tab, err := b.Open(context.Background(), OpenOptions{Name: "shapes", Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer tab.Close(context.Background(), false)

	// (1) extract returns string (default markdown).
	exRes, err := tab.Call(context.Background(), CallContext{}, []CallStep{{Method: "extract"}})
	if err != nil {
		t.Fatalf("extract failed: %v", err)
	}
	if s, ok := exRes.Value.(string); !ok || s != "# Heading\n\nBody" {
		t.Fatalf("expected markdown string from extract, got %#v", exRes.Value)
	}

	// (2) storage("local", {key: "k"}) queries getItem("k"), not whole store.
	stRes, err := tab.Call(context.Background(), CallContext{}, []CallStep{{
		Method: "storage",
		Args:   []json.RawMessage{json.RawMessage(`"local"`), json.RawMessage(`{"key":"token"}`)},
	}})
	if err != nil || stRes.Value != "val1" || !strings.Contains(lastEvalExpr, `localStorage.getItem("token")`) {
		t.Fatalf("expected single-key storage read, got val=%#v expr=%q err=%v", stRes, lastEvalExpr, err)
	}

	// (3) addInitScript returns {id}.
	isRes, err := tab.Call(context.Background(), CallContext{}, []CallStep{{
		Method: "addInitScript",
		Args:   []json.RawMessage{json.RawMessage(`"window.__x = 1;"`)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if m, ok := isRes.Value.(map[string]any); !ok || m["id"] != "script-42" {
		t.Fatalf("expected {id: script-42}, got %#v", isRes.Value)
	}

	// (4) text/html/value/attr/box/styles return nil on no match (never throw).
	for _, step := range []CallStep{
		{Method: "text", Args: []json.RawMessage{json.RawMessage(`"#nope"`)}},
		{Method: "html", Args: []json.RawMessage{json.RawMessage(`"#nope"`)}},
		{Method: "value", Args: []json.RawMessage{json.RawMessage(`"#nope"`)}},
		{Method: "attr", Args: []json.RawMessage{json.RawMessage(`"#nope"`), json.RawMessage(`"href"`)}},
		{Method: "box", Args: []json.RawMessage{json.RawMessage(`"#nope"`)}},
		{Method: "styles", Args: []json.RawMessage{json.RawMessage(`"#nope"`)}},
	} {
		r, err := tab.Call(context.Background(), CallContext{}, []CallStep{step})
		if err != nil || r.Value != nil {
			t.Fatalf("%s on missing element: want nil value & nil err, got val=%#v err=%v", step.Method, r, err)
		}
	}

	// (5) emulate returns state, supports device/reducedMotion/null clear, rejects invalid keys; devices() is sorted KnownDevices.
	if _, err := tab.Call(context.Background(), CallContext{}, []CallStep{{
		Method: "emulate",
		Args:   []json.RawMessage{json.RawMessage(`{"unknownKey":1}`)},
	}}); err == nil {
		t.Fatalf("expected emulate to reject unknown key")
	}
	emRes, err := tab.Call(context.Background(), CallContext{}, []CallStep{{
		Method: "emulate",
		Args:   []json.RawMessage{json.RawMessage(`{"device":"iPhone 15","reducedMotion":"reduce"}`)},
	}})
	if err != nil {
		t.Fatalf("emulate failed: %v", err)
	}
	if m, ok := emRes.Value.(map[string]any); !ok || m["device"] != "iPhone 15" || m["reducedMotion"] != true {
		t.Fatalf("unexpected emulate state: %#v", emRes.Value)
	}
	devsRes, err := tab.Call(context.Background(), CallContext{}, []CallStep{{Method: "devices"}})
	if err != nil {
		t.Fatal(err)
	}
	if devs, ok := devsRes.Value.([]string); !ok || len(devs) < 20 {
		t.Fatalf("expected sorted KnownDevices list, got %#v", devsRes.Value)
	}
}

// --- Findings 7, 8, 9, 10: PointerSpace, key events & unknown key, dialog race & setDialogs, frames ---

func TestPointerSpaceAndScreenshotValidation(t *testing.T) {
	var ps PointerSpace
	ps.Use(&PointerFrame{ViewportWidth: 1365, ViewportHeight: 768, Width: 1000, Height: 562})
	vx, vy, err := ps.ToViewport(500, 281, false, &Viewport{Width: 1365, Height: 768})
	if err != nil || vx < 680 || vx > 685 || vy < 382 || vy > 386 {
		t.Fatalf("unexpected scaled pointer point: (%v,%v) err=%v", vx, vy, err)
	}
	// css:true bypasses scaling.
	cx, cy, err := ps.ToViewport(500, 281, true, &Viewport{Width: 1365, Height: 768})
	if err != nil || cx != 500 || cy != 281 {
		t.Fatalf("css:true should pass through verbatim, got (%v,%v) err=%v", cx, cy, err)
	}
	// Resized viewport rejects unscaled screenshot point.
	if _, _, err := ps.ToViewport(100, 100, false, &Viewport{Width: 800, Height: 600}); err == nil || !strings.Contains(err.Error(), "css: true") {
		t.Fatalf("expected viewport-changed error, got %v", err)
	}
	// Unknown / unported screenshot options are rejected.
	if err := validateScreenshotOpts(map[string]any{"bogus": 1}); err == nil {
		t.Fatalf("expected unknown screenshot option error")
	}
	if err := validateScreenshotOpts(map[string]any{"annotate": true}); err == nil {
		t.Fatalf("expected annotate error")
	}
	if err := validateScreenshotOpts(map[string]any{"quality": float64(80), "format": "png"}); err == nil {
		t.Fatalf("expected quality-with-png error")
	}
}

func TestTypeSendsKeyEventsAndPressRejectsUnknownKeys(t *testing.T) {
	var events []map[string]any
	var mu sync.Mutex
	srv := startFakeCDPServer(t, func(method string, params json.RawMessage) (any, error) {
		switch method {
		case "Target.createTarget":
			return map[string]any{"targetId": "t1"}, nil
		case "Target.attachToTarget":
			return map[string]any{"sessionId": "s1"}, nil
		case "Input.dispatchKeyEvent":
			var m map[string]any
			_ = json.Unmarshal(params, &m)
			mu.Lock()
			events = append(events, m)
			mu.Unlock()
			return map[string]any{}, nil
		default:
			return map[string]any{}, nil
		}
	})
	b := NewChromiumBackend()
	tab, err := b.Open(context.Background(), OpenOptions{Name: "keys", Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer tab.Close(context.Background(), false)

	ct := tab.(*cdpTab)
	if _, err := ct.typeText(context.Background(), "ab"); err != nil {
		t.Fatalf("typeText failed: %v", err)
	}
	mu.Lock()
	got := append([]map[string]any(nil), events...)
	mu.Unlock()
	if len(got) != 4 || got[0]["type"] != "keyDown" || got[0]["key"] != "a" || got[1]["type"] != "keyUp" {
		t.Fatalf("expected keyDown/keyUp per char, got %+v", got)
	}
	if _, err := tab.Call(context.Background(), CallContext{}, []CallStep{{
		Method: "press",
		Args:   []json.RawMessage{json.RawMessage(`"NotARealKeyName"`)},
	}}); err == nil || !strings.Contains(err.Error(), "Unknown key") {
		t.Fatalf("expected Unknown key error, got %v", err)
	}
}

func TestDialogRaceSetDialogsAndHandleDialogEmpty(t *testing.T) {
	var handledAccept *bool
	srv := startFakeCDPServer(t, func(method string, params json.RawMessage) (any, error) {
		switch method {
		case "Target.createTarget":
			return map[string]any{"targetId": "t1"}, nil
		case "Target.attachToTarget":
			return map[string]any{"sessionId": "s1"}, nil
		case "Page.handleJavaScriptDialog":
			var p struct {
				Accept bool `json:"accept"`
			}
			_ = json.Unmarshal(params, &p)
			handledAccept = &p.Accept
			return map[string]any{}, nil
		default:
			return map[string]any{}, nil
		}
	})
	b := NewChromiumBackend()
	tab, err := b.Open(context.Background(), OpenOptions{Name: "dlg", Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer tab.Close(context.Background(), false)
	ct := tab.(*cdpTab)

	// handleDialog with nothing pending returns omp's message.
	if _, err := tab.Call(context.Background(), CallContext{}, []CallStep{{Method: "handleDialog"}}); err == nil || !strings.Contains(err.Error(), "found no pending confirm or prompt") {
		t.Fatalf("expected no-pending-dialog error, got %v", err)
	}

	// Racing an in-flight call against a confirm dialog opening:
	// answersDialog=false fails immediately with omp's dialog-blocks message.
	errCh := make(chan error, 1)
	go func() {
		_, err := ct.runWithDialogRace(context.Background(), "tab.evaluate()", false, func(ctx context.Context) (*CallResult, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		})
		errCh <- err
	}()
	time.Sleep(25 * time.Millisecond)
	ct.handleDialogOpening("confirm", "Delete item?", "")
	select {
	case err := <-errCh:
		if err == nil || !strings.Contains(err.Error(), "opened while it ran") {
			t.Fatalf("expected dialog-opened error, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for dialog race")
	}

	// setDialogs("accept") must settle the already-pending dialog too.
	if _, err := tab.Call(context.Background(), CallContext{}, []CallStep{{
		Method: "setDialogs",
		Args:   []json.RawMessage{json.RawMessage(`"accept"`)},
	}}); err != nil {
		t.Fatalf("setDialogs failed: %v", err)
	}
	if handledAccept == nil || !*handledAccept {
		t.Fatalf("expected setDialogs(accept) to settle pending dialog with accept=true")
	}
}

// --- Findings 11, 12, 21: Per-session settings, concurrent open serialization, lifecycle.OnEnd with fake second backend ---

func TestPerSessionSettingsConcurrentOpensAndLifecycleMultiBackend(t *testing.T) {
	orig := RegisteredBackends()
	t.Cleanup(func() {
		for k := range RegisteredBackends() {
			RegisterBackend(k, nil)
		}
		for k, v := range orig {
			RegisterBackend(k, v)
		}
		lifecycle.ResetForTests()
	})

	var headlessOpens atomic.Int32
	bHeadless := &countingBackend{onOpen: func(OpenOptions) {
		time.Sleep(20 * time.Millisecond)
		headlessOpens.Add(1)
	}}
	bSecond := &countingBackend{}
	RegisterBackend(string(KindHeadless), bHeadless)
	RegisterBackend(string(KindTern), bSecond)

	// SettingsFromMap reads live browser.* and tools.maxTimeout settings.
	sDisabled := SettingsFromMap(map[string]any{
		"browser": map[string]any{"enabled": false, "headless": false, "cdpUrl": "http://127.0.0.1:9222", "idleCloseSec": float64(60)},
		"tools":   map[string]any{"maxTimeout": float64(120)},
	})
	if sDisabled.Enabled || sDisabled.Headless || sDisabled.CdpURL != "http://127.0.0.1:9222" || sDisabled.IdleCloseSec != 60 || sDisabled.MaxTimeoutSec != 120 {
		t.Fatalf("SettingsFromMap did not parse nested settings: %+v", sDisabled)
	}
	sFlat := SettingsFromMap(map[string]any{"browser.enabled": false, "browser.freezeOnTurnEnd": false})
	if sFlat.Enabled || sFlat.FreezeOnTurnEnd {
		t.Fatalf("SettingsFromMap did not parse dotted settings: %+v", sFlat)
	}
	m := NewManager()
	m.env = envLookup(map[string]string{})
	// Concurrent opens for the same (session, name) are serialized: only 1 open, 0 leaked tabs.
	s := DefaultSettings()
	s.Tern, s.WebKit, s.Cmux, s.Relay = false, false, false, false
	m.SetSettingsProvider(func() Settings { return s })
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = m.Invoke(context.Background(), InvokeRequest{OwnerSessionID: "conc-sess"}, json.RawMessage(`{"action":"open","name":"shared","app":"chromium"}`))
		}()
	}
	wg.Wait()
	if headlessOpens.Load() != 1 {
		t.Fatalf("expected 1 serialized open for concurrent same-name opens, got %d", headlessOpens.Load())
	}

	// lifecycle.OnEnd releases every backend's tabs for ONLY the ending session (not sibling sessions).
	ext := sdk.New("test-browser")
	Register(ext)
	RegisterBackend(string(KindHeadless), bHeadless)
	RegisterBackend(string(KindTern), bSecond)
	t.Setenv("TERN_PANE_SOCKET", "/tmp/fake-tern.sock")
	t.Setenv("TERN_PANE", "42")

	dm := DefaultManager()
	dm.ReleaseAllTabs(context.Background(), true)
	t.Cleanup(func() { dm.ReleaseAllTabs(context.Background(), true) })

	// Open a headless tab and a fake-Tern-backend tab in session A, plus a tab in session B.
	sOff := DefaultSettings()
	sOff.WebKit, sOff.Cmux, sOff.Relay = false, false, false
	if _, err := dm.Invoke(context.Background(), InvokeRequest{OwnerSessionID: "sess-A", Settings: &sOff}, json.RawMessage(`{"action":"open","name":"h","app":"chromium"}`)); err != nil {
		t.Fatalf("open headless in sess-A: %v", err)
	}
	if _, err := dm.Invoke(context.Background(), InvokeRequest{OwnerSessionID: "sess-A", Settings: &sOff}, json.RawMessage(`{"action":"open","name":"t","app":"tern"}`)); err != nil {
		t.Fatalf("open tern in sess-A: %v", err)
	}
	if _, err := dm.Invoke(context.Background(), InvokeRequest{OwnerSessionID: "sess-B", Settings: &sOff}, json.RawMessage(`{"action":"open","name":"h","app":"chromium"}`)); err != nil {
		t.Fatalf("open headless in sess-B: %v", err)
	}

	closedHeadlessBefore := bHeadless.closed.Load()
	closedSecondBefore := bSecond.closed.Load()

	// Ending sess-A via lifecycle.End must close both backends' tabs in sess-A, leaving sess-B intact.
	lifecycle.End("sess-A")
	if bHeadless.closed.Load()-closedHeadlessBefore != 1 {
		t.Fatalf("expected 1 headless tab closed on sess-A end, got %d", bHeadless.closed.Load()-closedHeadlessBefore)
	}
	if bSecond.closed.Load()-closedSecondBefore != 1 {
		t.Fatalf("expected 1 second-backend tab closed on sess-A end, got %d", bSecond.closed.Load()-closedSecondBefore)
	}
	if got := dm.ListTabsForOwner("sess-A"); len(got) != 0 {
		t.Fatalf("expected 0 tabs left for sess-A, got %+v", got)
	}
	if got := dm.ListTabsForOwner("sess-B"); len(got) != 1 {
		t.Fatalf("expected sess-B's tab to remain open, got %+v", got)
	}

	// Unknown / empty session ID releases nothing.
	lifecycle.End("")
	if got := dm.ListTabsForOwner("sess-B"); len(got) != 1 {
		t.Fatalf("empty session id must never release other sessions' tabs")
	}

	// Verify Register(ext) idempotency does not clobber defaultChromium or live tabs.
	origChrom := defaultChromium
	Register(ext)
	if defaultChromium != origChrom {
		t.Fatalf("second Register() replaced package-level defaultChromium")
	}
	if _, ok := eval.LookupPrelude("browser"); !ok {
		t.Fatalf("expected browser prelude to remain registered")
	}
}

// --- Findings 14, 16, 17, 20: ResolveSpawnArgs, WebSocket framing, RedactURLForLog, DecodePrelude*, timeouts, status chain, init scripts ---

func TestSpawnArgsRedactionDecodersAndMinorFixes(t *testing.T) {
	// Finding 14: non-Chromium (Electron) gets no --user-data-dir; Chromium gets persistent profile.
	electronArgs := ResolveSpawnArgs("/Applications/Slack.app/Contents/MacOS/Slack", []string{"--foo"}, "/tmp")
	if len(electronArgs) != 1 || electronArgs[0] != "--foo" {
		t.Fatalf("Electron app args should be untouched, got %+v", electronArgs)
	}
	chromeArgs := ResolveSpawnArgs("/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", nil, "/tmp")
	joined := strings.Join(chromeArgs, " ")
	if !strings.Contains(joined, "--no-first-run") || !strings.Contains(joined, "--user-data-dir=") || !strings.Contains(joined, "browser-profiles") {
		t.Fatalf("expected persistent profile + flags for spawned Chromium, got %s", joined)
	}

	// Finding 17: RedactURLForLog strips fragments and handles unparseable URLs with userinfo.
	if got := RedactURLForLog("http://user:pass@host:9222/p?token=secret&a=1#access_token=xyz"); strings.Contains(got, "pass") || strings.Contains(got, "secret") || strings.Contains(got, "xyz") || !strings.Contains(got, "a=1") {
		t.Fatalf("unexpected redacted URL: %q", got)
	}
	if got := RedactURLForLog("http://alice:pw@bad host:9999?token=1#frag"); strings.Contains(got, "alice") || strings.Contains(got, "pw") || strings.Contains(got, "token") || strings.Contains(got, "frag") {
		t.Fatalf("unparseable URL leaked credentials: %q", got)
	}

	// Exported prelude decoders.
	if src, ok := DecodePreludeFn(map[string]any{"__omp_fn": "() => 1"}); !ok || src != "() => 1" {
		t.Fatalf("DecodePreludeFn failed: %q %v", src, ok)
	}
	if src, flags, ok := DecodePreludeRegExp(map[string]any{"__omp_re": map[string]any{"source": "foo", "flags": "i"}}); !ok || src != "foo" || flags != "i" {
		t.Fatalf("DecodePreludeRegExp failed: %q %q %v", src, flags, ok)
	}

	// Finding 20: ClampTimeoutSec does not round up; TabCallBudgetSeconds ignores JSON null; FormatStatusCallChain masks fill/type text.
	req25 := 2.5
	if got := ClampTimeoutSec(&req25, 0); got != 2.5 {
		t.Fatalf("expected ClampTimeoutSec(2.5)=2.5, got %v", got)
	}
	if b := TabCallBudgetSeconds([]CallStep{{Method: "waitFor", Args: []json.RawMessage{json.RawMessage(`"#x"`), json.RawMessage(`{"timeout":null}`)}}}, 0, false); b != nil {
		t.Fatalf("expected JSON null timeout not to trigger max budget, got %v", *b)
	}
	status := FormatStatusCallChain("main", []CallStep{{Method: "fill", Args: []json.RawMessage{json.RawMessage(`"#pw"`), json.RawMessage(`"supersecret"`)}}})
	if strings.Contains(status, "supersecret") || !strings.Contains(status, "<hidden>") {
		t.Fatalf("expected fill value hidden in status chain, got %q", status)
	}

	// resolveInitScriptSources stats every entry (including filenames containing parens/semicolons).
	dir := t.TempDir()
	weirdFile := filepath.Join(dir, "init(1).js")
	if err := os.WriteFile(weirdFile, []byte("window.__loaded = true;"), 0o644); err != nil {
		t.Fatal(err)
	}
	srcs, err := resolveInitScriptSources([]string{"init(1).js", "window.__inline = 1;"}, dir)
	if err != nil || len(srcs) != 2 || srcs[0] != "window.__loaded = true;" || srcs[1] != "window.__inline = 1;" {
		t.Fatalf("unexpected resolved init scripts: %+v err=%v", srcs, err)
	}
}

func TestWebSocketFragmentationPingOversizedAndCloseFrame(t *testing.T) {
	var sawClose atomic.Bool
	var sawPong atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Sec-WebSocket-Key")
		sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		accept := base64.StdEncoding.EncodeToString(sum[:])
		hj := w.(http.Hijacker)
		conn, rw, err := hj.Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.WriteString(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: "+accept+"\r\n\r\n")
		_ = rw.Flush()

		// Send ping, then fragmented "hello" + " world".
		_, _ = conn.Write([]byte{0x89, 2, 'h', 'i'})
		_, _ = conn.Write([]byte{0x01, 5, 'h', 'e', 'l', 'l', 'o'})
		_, _ = conn.Write([]byte{0x80, 6, ' ', 'w', 'o', 'r', 'l', 'd'})

		// Read client frames (expect pong then close).
		br := bufio.NewReader(conn)
		for range 4 {
			var h [2]byte
			if _, err := io.ReadFull(br, h[:]); err != nil {
				return
			}
			op := h[0] & 0x0f
			l := int(h[1] & 0x7f)
			var mask [4]byte
			_, _ = io.ReadFull(br, mask[:])
			if l > 0 {
				buf := make([]byte, l)
				_, _ = io.ReadFull(br, buf)
			}
			if op == 0xA {
				sawPong.Store(true)
			}
			if op == 0x8 {
				sawClose.Store(true)
				return
			}
		}
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ws, err := dialWebSocket(context.Background(), wsURL)
	if err != nil {
		t.Fatalf("dialWebSocket failed: %v", err)
	}
	msg, err := ws.ReadMessage()
	if err != nil || string(msg) != "hello world" {
		t.Fatalf("expected reassembled 'hello world', got %q err=%v", string(msg), err)
	}
	_ = ws.Close()
	time.Sleep(30 * time.Millisecond)
	if !sawPong.Load() {
		t.Fatalf("expected client to reply to server ping with pong")
	}
	if !sawClose.Load() {
		t.Fatalf("expected client Close() to transmit WS close frame before closing socket")
	}

	// Oversized frame header must be rejected immediately without allocating payload.
	srvBig := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Sec-WebSocket-Key")
		sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		accept := base64.StdEncoding.EncodeToString(sum[:])
		hj := w.(http.Hijacker)
		conn, rw, err := hj.Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.WriteString(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: "+accept+"\r\n\r\n")
		_ = rw.Flush()
		// Claim a 100 MiB frame without sending payload.
		var hdr [10]byte
		hdr[0] = 0x81
		hdr[1] = 127
		binary.BigEndian.PutUint64(hdr[2:10], 100*1024*1024)
		_, _ = conn.Write(hdr[:])
	}))
	defer srvBig.Close()
	ws2, err := dialWebSocket(context.Background(), "ws"+strings.TrimPrefix(srvBig.URL, "http"))
	if err != nil {
		t.Fatal(err)
	}
	defer ws2.Close()
	if _, err := ws2.ReadMessage(); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("expected oversized WS frame error, got %v", err)
	}
}
