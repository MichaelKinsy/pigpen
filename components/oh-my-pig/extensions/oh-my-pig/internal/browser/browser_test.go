package browser

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/eval"
)

func envLookup(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

// --- Backend resolution tests (ported from omp's browser-*-kind.test.ts) ---

func TestResolveTernKind(t *testing.T) {
	ternEnv := map[string]string{
		"TERN_PANE_SOCKET": "/tmp/tern.sock",
		"TERN_PANE":        "42",
	}

	// upstream: browser-tern-kind.test.ts: "needs both the daemon socket and a numeric pane"
	if got := ResolveTernKind(true, envLookup(ternEnv)); got == nil || got.Kind != KindTern || got.SocketPath != "/tmp/tern.sock" || got.Pane != 42 {
		t.Fatalf("expected tern kind, got %+v", got)
	}
	if got := ResolveTernKind(true, envLookup(map[string]string{"TERN_PANE_SOCKET": "/tmp/tern.sock"})); got != nil {
		t.Fatalf("expected nil without TERN_PANE, got %+v", got)
	}
	if got := ResolveTernKind(true, envLookup(map[string]string{"TERN_PANE_SOCKET": "/tmp/tern.sock", "TERN_PANE": "pane-1"})); got != nil {
		t.Fatalf("expected nil for non-numeric TERN_PANE, got %+v", got)
	}

	// upstream: browser-tern-kind.test.ts: "lets PI_BROWSER_TERN override the setting in both directions"
	if got := ResolveTernKind(true, envLookup(map[string]string{
		"TERN_PANE_SOCKET": "/tmp/tern.sock", "TERN_PANE": "42", "PI_BROWSER_TERN": "0",
	})); got != nil {
		t.Fatalf("PI_BROWSER_TERN=0 should disable Tern, got %+v", got)
	}
	if got := ResolveTernKind(false, envLookup(ternEnv)); got != nil {
		t.Fatalf("settingEnabled=false should disable Tern, got %+v", got)
	}
	if got := ResolveTernKind(false, envLookup(map[string]string{
		"TERN_PANE_SOCKET": "/tmp/tern.sock", "TERN_PANE": "42", "PI_BROWSER_TERN": "1",
	})); got == nil || got.Kind != KindTern {
		t.Fatalf("PI_BROWSER_TERN=1 should enable Tern over setting=false, got %+v", got)
	}
}

func TestResolveBrowserKind_Table(t *testing.T) {
	baseSettings := func() Settings {
		s := DefaultSettings()
		s.WebKit = false
		s.Relay = false
		return s
	}
	ternEnv := map[string]string{
		"TERN_PANE_SOCKET": "/tmp/tern.sock",
		"TERN_PANE":        "42",
	}
	cmuxTernEnv := map[string]string{
		"TERN_PANE_SOCKET": "/tmp/tern.sock",
		"TERN_PANE":        "42",
		"CMUX_SOCKET_PATH": "/tmp/cmux.sock",
	}

	// upstream: browser-tern-kind.test.ts: "prefers a Tern PiP over cmux and Chromium inside a Tern pane"
	k, err := ResolveBrowserKind(ResolveParams{}, ResolveSession{Settings: baseSettings(), PID: 100}, envLookup(cmuxTernEnv))
	if err != nil || k.Kind != KindTern || k.Pane != 42 {
		t.Fatalf("expected Tern over cmux/Chromium, got %+v, err=%v", k, err)
	}

	// upstream: browser-tern-kind.test.ts: "keeps explicit app options, the relay and browser.cdpUrl ahead of Tern"
	k, err = ResolveBrowserKind(ResolveParams{App: "http://127.0.0.1:9222", HasApp: true}, ResolveSession{Settings: baseSettings()}, envLookup(ternEnv))
	if err != nil || k.Kind != KindConnected || k.CdpURL != "http://127.0.0.1:9222" {
		t.Fatalf("expected connected, got %+v, err=%v", k, err)
	}
	sRelay := baseSettings()
	sRelay.Relay = true
	k, err = ResolveBrowserKind(ResolveParams{}, ResolveSession{Settings: sRelay}, envLookup(ternEnv))
	if err != nil || k.Kind != KindRelay {
		t.Fatalf("expected relay ahead of Tern, got %+v, err=%v", k, err)
	}
	sCdp := baseSettings()
	sCdp.CdpURL = "http://127.0.0.1:9333/"
	k, err = ResolveBrowserKind(ResolveParams{}, ResolveSession{Settings: sCdp}, envLookup(ternEnv))
	if err != nil || k.Kind != KindConnected || k.CdpURL != "http://127.0.0.1:9333" {
		t.Fatalf("expected browser.cdpUrl ahead of Tern, got %+v, err=%v", k, err)
	}

	// upstream: browser-tern-kind.test.ts: "keeps the Tern PiP for headed:false"
	k, err = ResolveBrowserKind(ResolveParams{Headed: new(false)}, ResolveSession{Settings: baseSettings()}, envLookup(cmuxTernEnv))
	if err != nil || k.Kind != KindTern {
		t.Fatalf("headed:false should keep Tern, got %+v, err=%v", k, err)
	}

	// upstream: browser-tern-kind.test.ts: "skips Tern for app: \"own\" and the setting, falling through to cmux only for the setting"
	k, err = ResolveBrowserKind(ResolveParams{App: "own", HasApp: true}, ResolveSession{Settings: baseSettings()}, envLookup(cmuxTernEnv))
	if err != nil || k.Kind != KindHeadless {
		t.Fatalf("app:own should pick headless Chromium, got %+v, err=%v", k, err)
	}
	sNoTern := baseSettings()
	sNoTern.Tern = false
	k, err = ResolveBrowserKind(ResolveParams{}, ResolveSession{Settings: sNoTern}, envLookup(cmuxTernEnv))
	if err != nil || k.Kind != KindCmux {
		t.Fatalf("browser.tern=false should fall through to cmux, got %+v, err=%v", k, err)
	}

	// upstream: browser-tern-kind.test.ts: "target adopts in Tern even with the browser.tern setting off, unless app: \"own\""
	k, err = ResolveBrowserKind(ResolveParams{Target: "github", HasTarget: true}, ResolveSession{Settings: sNoTern}, envLookup(cmuxTernEnv))
	if err != nil || k.Kind != KindTern {
		t.Fatalf("target should adopt in Tern despite browser.tern=false, got %+v, err=%v", k, err)
	}
	k, err = ResolveBrowserKind(ResolveParams{Target: "github", HasTarget: true, App: "own", HasApp: true}, ResolveSession{Settings: sNoTern}, envLookup(cmuxTernEnv))
	if err != nil || k.Kind != KindHeadless {
		t.Fatalf("target + app:own should pick headless, got %+v, err=%v", k, err)
	}

	// upstream: browser-tern-kind.test.ts: "app: \"tern\" forces Tern over settings and errors outside Tern"
	sNoTern.CdpURL = "http://127.0.0.1:9333"
	k, err = ResolveBrowserKind(ResolveParams{App: "tern", HasApp: true}, ResolveSession{Settings: sNoTern}, envLookup(ternEnv))
	if err != nil || k.Kind != KindTern {
		t.Fatalf("app:tern should win over settings, got %+v, err=%v", k, err)
	}
	_, err = ResolveBrowserKind(ResolveParams{App: "tern", HasApp: true}, ResolveSession{Settings: baseSettings()}, envLookup(map[string]string{}))
	if err == nil || !strings.Contains(err.Error(), "requires running inside a Tern pane") {
		t.Fatalf("app:tern outside Tern should error, got %v", err)
	}

	// upstream: browser-tern-kind.test.ts: "opens managed Chromium instead of a Tern PiP or a cmux surface" (when another omp owns terminal)
	otherOwnerEnv := map[string]string{
		"TERN_PANE_SOCKET": "/tmp/tern.sock",
		"TERN_PANE":        "42",
		"CMUX_SOCKET_PATH": "/tmp/cmux.sock",
		TerminalOwnerEnv:   "99999",
	}
	k, err = ResolveBrowserKind(ResolveParams{}, ResolveSession{Settings: baseSettings(), PID: 100}, envLookup(otherOwnerEnv))
	if err != nil || k.Kind != KindHeadless {
		t.Fatalf("other terminal owner should skip Tern/cmux, got %+v, err=%v", k, err)
	}
	// But explicit app:"tern" or target still uses Tern even under another owner.
	k, err = ResolveBrowserKind(ResolveParams{App: "tern", HasApp: true}, ResolveSession{Settings: baseSettings(), PID: 100}, envLookup(otherOwnerEnv))
	if err != nil || k.Kind != KindTern {
		t.Fatalf("explicit app:tern under another owner should still select Tern, got %+v, err=%v", k, err)
	}
	k, err = ResolveBrowserKind(ResolveParams{Target: "github", HasTarget: true}, ResolveSession{Settings: baseSettings(), PID: 100}, envLookup(otherOwnerEnv))
	if err != nil || k.Kind != KindTern {
		t.Fatalf("target under another owner should still select Tern, got %+v, err=%v", k, err)
	}

	// upstream: browser-relay-kind.test.ts: "skips the relay, Tern and cmux the parent would use for an open without app" (subagent)
	subSess := ResolveSession{Settings: sRelay, TaskDepth: 1, PID: 100}
	k, err = ResolveBrowserKind(ResolveParams{}, subSess, envLookup(cmuxTernEnv))
	if err != nil || k.Kind != KindHeadless {
		t.Fatalf("subagent should skip relay/Tern/cmux, got %+v, err=%v", k, err)
	}
	// PI_BROWSER_RELAY=only forces relay even for subagents and rejects other app keywords.
	onlyEnv := map[string]string{"PI_BROWSER_RELAY": "only"}
	k, err = ResolveBrowserKind(ResolveParams{}, subSess, envLookup(onlyEnv))
	if err != nil || k.Kind != KindRelay {
		t.Fatalf("PI_BROWSER_RELAY=only should force relay for subagent, got %+v, err=%v", k, err)
	}
	_, err = ResolveBrowserKind(ResolveParams{App: "chromium", HasApp: true}, subSess, envLookup(onlyEnv))
	if err == nil || !strings.Contains(err.Error(), "has no other browser") {
		t.Fatalf("PI_BROWSER_RELAY=only should reject app:chromium, got %v", err)
	}

	// app: "auto" skips relay and falls through to Tern.
	k, err = ResolveBrowserKind(ResolveParams{App: "auto", HasApp: true}, ResolveSession{Settings: sRelay, PID: 100}, envLookup(ternEnv))
	if err != nil || k.Kind != KindTern {
		t.Fatalf("app:auto should skip relay and pick Tern, got %+v, err=%v", k, err)
	}

	// app: "chromium" selects headless Chromium directly even inside Tern.
	k, err = ResolveBrowserKind(ResolveParams{App: "chromium", HasApp: true}, ResolveSession{Settings: baseSettings()}, envLookup(ternEnv))
	if err != nil || k.Kind != KindHeadless || !k.Headless {
		t.Fatalf("app:chromium should pick headless Chromium, got %+v, err=%v", k, err)
	}
}

func TestResolveWebKitKind(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable bit test is unix-specific")
	}
	dir := t.TempDir()
	engineDir := filepath.Join(dir, "engine")
	emptyDir := filepath.Join(dir, "empty")
	if err := os.MkdirAll(engineDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(emptyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	host := filepath.Join(engineDir, "ABHost")
	if err := os.WriteFile(host, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// upstream: browser-webkit-kind.test.ts: "defaults to the WebKit host found through the env override or the path setting"
	s := DefaultSettings()
	s.Relay = false
	k, err := ResolveBrowserKind(ResolveParams{}, ResolveSession{Settings: s}, envLookup(map[string]string{"PI_BROWSER_WEBKIT_PATH": engineDir}))
	if err != nil || k.Kind != KindWebKit {
		t.Fatalf("expected webkit from PI_BROWSER_WEBKIT_PATH, got %+v, err=%v", k, err)
	}

	// upstream: browser-webkit-kind.test.ts: "leaves a visible window to Chromium on an engine without a viewer"
	k, err = ResolveBrowserKind(ResolveParams{Headed: new(true)}, ResolveSession{Settings: s}, envLookup(map[string]string{"PI_BROWSER_WEBKIT_PATH": engineDir}))
	if err != nil || k.Kind != KindHeadless || k.Headless {
		t.Fatalf("expected visible Chromium when ABView absent, got %+v, err=%v", k, err)
	}

	// upstream: browser-webkit-kind.test.ts: "Required and unavailable is an error, never a quiet Chromium."
	_, err = ResolveBrowserKind(ResolveParams{App: "webkit", HasApp: true}, ResolveSession{Settings: s}, envLookup(map[string]string{"PI_BROWSER_WEBKIT_PATH": emptyDir}))
	if err == nil || !strings.Contains(err.Error(), `app: "webkit" requires omp's WebKit engine`) {
		t.Fatalf("expected webkit missing error, got %v", err)
	}
}

// --- Tab call validation tests (ported from browser-tab-call.test.ts) ---

func TestValidateTabCallChain(t *testing.T) {
	// upstream: browser-tab-call.test.ts: "renders value, presence, and one-hop element calls"
	valid := [][]CallStep{
		{{Method: "click", Args: []json.RawMessage{json.RawMessage(`"text/Go"`)}}},
		{{Method: "waitFor", Args: []json.RawMessage{json.RawMessage(`"#x"`), json.RawMessage(`{"timeout":1000}`)}}},
		{{Method: "id", Args: []json.RawMessage{json.RawMessage(`5`)}}, {Method: "fill", Args: []json.RawMessage{json.RawMessage(`"Ada"`)}}},
		{{Method: "ref", Args: []json.RawMessage{json.RawMessage(`"e2"`)}}, {Method: "click"}},
		{{Method: "frame", Args: []json.RawMessage{json.RawMessage(`"#pay"`)}}, {Method: "click", Args: []json.RawMessage{json.RawMessage(`"#submit"`)}}},
	}
	for _, chain := range valid {
		if err := ValidateTabCallChain(chain); err != nil {
			t.Fatalf("expected valid chain %+v, got err: %v", chain, err)
		}
	}

	// upstream: browser-tab-call.test.ts: "rejects invalid call chains with actionable errors"
	cases := []struct {
		chain []CallStep
		want  string
	}{
		{nil, "Action 'call' requires a non-empty 'chain'."},
		{[]CallStep{{Method: "notAHelper"}}, `Unknown tab helper "notAHelper". Direct helpers: `},
		{[]CallStep{{Method: "id", Args: []json.RawMessage{json.RawMessage(`5`)}}}, "tab.id() returns an element handle"},
		{[]CallStep{{Method: "click"}, {Method: "focus"}}, "Only tab.id(n)/tab.ref(id)/tab.frame(selector) results accept a chained call; got tab.click()."},
		{[]CallStep{{Method: "ref"}, {Method: "remove"}}, `Unknown element method "remove".`},
		{[]CallStep{{Method: "id"}, {Method: "click"}, {Method: "focus"}}, "Call chains support one element-handle hop at most"},
	}
	for _, tc := range cases {
		err := ValidateTabCallChain(tc.chain)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("chain %+v: want error containing %q, got %v", tc.chain, tc.want, err)
		}
	}
}

// --- URL pattern & selector parser tests (ported from browser-url-patterns.test.ts) ---

func TestURLPatterns(t *testing.T) {
	pageURL := "https://www.stardoll.com/en/starplaza.php"

	// upstream: browser-url-patterns.test.ts: "matches a ** glob against the whole URL instead of searching for it as text"
	m, err := ParseURLPattern("**/starplaza.php")
	if err != nil {
		t.Fatal(err)
	}
	if !m(pageURL) || m(pageURL+"?tab=sale") {
		t.Fatalf("unexpected **/starplaza.php match")
	}
	m2, _ := ParseURLPattern("https://*.stardoll.com/en/*")
	if !m2(pageURL) {
		t.Fatalf("expected wildcard subdomain+path match")
	}

	// upstream: browser-url-patterns.test.ts: "lets a * at either end span path segments"
	m3, _ := ParseURLPattern("*sort=X*")
	if !m3("http://h/p?sort=X&page=1") || m3("http://h/p?sort=Y&page=1") {
		t.Fatalf("expected boundary * to span segments")
	}

	// upstream: browser-url-patterns.test.ts: "keeps a string without * a substring and a RegExp a RegExp"
	mSub, _ := ParseURLPattern("starplaza")
	if !mSub(pageURL) {
		t.Fatalf("expected substring match")
	}
	mRe, err := ParseURLPattern(map[string]any{"__omp_re": map[string]any{"source": "plaza", "flags": "gi"}})
	if err != nil || !mRe(pageURL) {
		t.Fatalf("expected regexp match, err=%v", err)
	}

	// upstream: browser-url-patterns.test.ts: "refuses a predicate before anything waits"
	if _, err := ParseURLPattern(map[string]any{"__omp_fn": "url => true"}); err == nil || !strings.Contains(err.Error(), "not a function") {
		t.Fatalf("expected predicate refusal, got %v", err)
	}

	// upstream: browser-url-patterns.test.ts: "redacts credential headers in the request listing only"
	redacted := RedactSensitiveHeaders(map[string]string{"Cookie": "sid=1", "accept": "*/*", "set-cookie": "sid=2"})
	if redacted["Cookie"] != "[redacted]" || redacted["set-cookie"] != "[redacted]" || redacted["accept"] != "*/*" {
		t.Fatalf("unexpected header redaction: %+v", redacted)
	}
}

func TestDocumentationAndGuidance(t *testing.T) {
	// upstream: browser-prelude-documentation.test.ts: "carries the Linux X11 click note on Linux hosts and for a configured browser on another machine"
	const x11Note = "On Linux under X11"
	if !strings.Contains(Documentation(DefaultSettings(), "linux"), x11Note) {
		t.Fatalf("expected X11 note on linux")
	}
	remoteSettings := DefaultSettings()
	remoteSettings.CdpURL = "http://10.0.0.5:9222"
	if !strings.Contains(Documentation(remoteSettings, "darwin"), x11Note) {
		t.Fatalf("expected X11 note for remote cdpUrl on darwin")
	}
	localSettings := DefaultSettings()
	localSettings.CdpURL = "http://127.0.0.1:9222"
	localDoc := Documentation(localSettings, "darwin")
	if strings.Contains(localDoc, x11Note) || strings.Contains(localDoc, "{{") {
		t.Fatalf("unexpected X11 note or unrendered placeholder on local darwin")
	}

	// upstream: browser-relay-kind.test.ts: "names the browser app: \"auto\" falls through to"
	s := DefaultSettings()
	s.Relay = true
	s.CdpURL = "http://user:pw@127.0.0.1:9333/?token=s3cret"
	g := Guidance(s, "darwin", envLookup(map[string]string{}))
	if !strings.Contains(g, "# Browser user sessions") || !strings.Contains(g, "# Browser Relay") {
		t.Fatalf("expected user session + relay guidance, got:\n%s", g)
	}
	if strings.Contains(g, "s3cret") || strings.Contains(g, "user:pw") {
		t.Fatalf("guidance leaked credentials:\n%s", g)
	}
}

func TestRegisterAndPreludeExport(t *testing.T) {
	eval.ResetPreludesForTests()
	e := sdk.New("test-browser")
	Register(e)

	p, ok := eval.LookupPrelude("browser")
	if !ok {
		t.Fatal("expected browser prelude to be registered")
	}
	if len(p.Exports) != 1 || p.Exports[0] != "browser" {
		t.Fatalf("unexpected exports: %v", p.Exports)
	}
	if p.Approval != "exec" {
		t.Fatalf("expected approval exec, got %q", p.Approval)
	}
	if !strings.Contains(p.JavaScript, "globalThis.browser") {
		t.Fatal("prelude.js missing globalThis.browser")
	}
	if !strings.Contains(p.Python, "browser = _make_browser()") {
		t.Fatal("prelude.py missing browser = _make_browser()")
	}
	if p.FirstCallDocumentation == "" || p.Documentation() == "" {
		t.Fatal("expected non-empty documentation and first-call documentation")
	}
}

// --- Manager fallback, reuse, and idle-close tests ---

type fakeTab struct {
	mu        sync.Mutex
	info      TabInfo
	alive     bool
	reused    int
	closed    bool
	killed    bool
	lastChain []CallStep
}

func (f *fakeTab) Info() TabInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.info
}
func (f *fakeTab) Reuse(_ context.Context, opts OpenOptions) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reused++
	if opts.URL != "" {
		f.info.URL = opts.URL
	}
	return "", nil
}
func (f *fakeTab) Call(_ context.Context, _ CallContext, chain []CallStep) (*CallResult, error) {
	f.mu.Lock()
	f.lastChain = chain
	f.mu.Unlock()
	return &CallResult{Value: "ok:" + chain[0].Method}, nil
}
func (f *fakeTab) Run(_ context.Context, _ CallContext, _, _ string, _ []json.RawMessage) (*CallResult, error) {
	return nil, fmt.Errorf("not supported")
}
func (f *fakeTab) Alive() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.alive && !f.closed
}
func (f *fakeTab) Close(_ context.Context, kill bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	f.killed = kill
	return nil
}

type fakeBackend struct {
	availOK     bool
	availReason string
	openErr     error
	openFn      func(opts OpenOptions) (Tab, error)
}

func (fb *fakeBackend) Available(_ context.Context) (bool, string) {
	return fb.availOK, fb.availReason
}
func (fb *fakeBackend) Open(_ context.Context, opts OpenOptions) (Tab, error) {
	if fb.openErr != nil {
		return nil, fb.openErr
	}
	if fb.openFn != nil {
		return fb.openFn(opts)
	}
	return &fakeTab{
		info:  TabInfo{URL: opts.URL, Title: "Fake", TargetID: "t1", Viewport: DefaultViewport},
		alive: true,
	}, nil
}

func TestManagerTernFallbackReuseAndIdleClose(t *testing.T) {
	origBackends := RegisteredBackends()
	t.Cleanup(func() {
		for k := range RegisteredBackends() {
			RegisterBackend(k, nil)
		}
		for k, b := range origBackends {
			RegisterBackend(k, b)
		}
	})

	// Register unavailable Tern backend and fake headless backend.
	RegisterBackend(string(KindTern), &fakeBackend{
		availOK:     false,
		availReason: "no Tern window",
	})
	var createdTab *fakeTab
	RegisterBackend(string(KindHeadless), &fakeBackend{
		availOK: true,
		openFn: func(opts OpenOptions) (Tab, error) {
			createdTab = &fakeTab{
				info: TabInfo{
					URL:         opts.URL,
					Title:       "Example",
					TargetID:    "target-1",
					Viewport:    DefaultViewport,
					Description: "omp's headless Chromium",
				},
				alive: true,
			}
			return createdTab, nil
		},
	})

	var clockMu sync.Mutex
	now := time.Now()
	m := NewManager()
	m.now = func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return now
	}
	m.env = envLookup(map[string]string{
		"TERN_PANE_SOCKET": "/tmp/tern.sock",
		"TERN_PANE":        "7",
	})
	s := DefaultSettings()
	s.WebKit = false
	m.SetSettingsProvider(func() Settings { return s })

	ctx := context.Background()
	req := InvokeRequest{Cwd: t.TempDir(), OwnerSessionID: "sess-1"}

	// Open inside Tern pane with unavailable Tern -> falls back to Chromium with omp's notice text.
	res, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"open","name":"docs","url":"https://example.com"}`))
	if err != nil {
		t.Fatalf("open fallback failed: %v", err)
	}
	if res.Details.Browser != KindHeadless {
		t.Fatalf("expected fallback browser headless, got %q", res.Details.Browser)
	}
	if !strings.Contains(res.Text, "Tern cannot show a browser here (no Tern window); opened Chromium instead.") {
		t.Fatalf("missing omp Tern fallback notice in:\n%s", res.Text)
	}

	// Reopening the same name reuses the fallen-back tab without hitting Tern again.
	res2, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"open","name":"docs","url":"https://example.com/2"}`))
	if err != nil {
		t.Fatalf("reuse failed: %v", err)
	}
	if !strings.HasPrefix(res2.Text, `Reused tab "docs"`) || createdTab.reused != 1 {
		t.Fatalf("expected reuse, text=%q reused=%d", res2.Text, createdTab.reused)
	}

	// Call works on the named tab.
	callRes, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"docs","chain":[{"method":"title","args":[]}]}`))
	if err != nil || callRes.Details.Value != "ok:title" {
		t.Fatalf("call failed: res=%+v err=%v", callRes, err)
	}

	// Tabs listing includes "docs".
	tabs := m.ListTabs()
	if len(tabs) != 1 || tabs[0].Name != "docs" {
		t.Fatalf("unexpected tabs: %+v", tabs)
	}

	// Advance clock past idleCloseSec -> ReleaseIdleTabsForOwner closes it.
	clockMu.Lock()
	now = now.Add(40 * time.Minute)
	clockMu.Unlock()
	reaped := m.ReleaseIdleTabsForOwner(ctx, "sess-1", 30*time.Minute)
	if reaped != 1 || !createdTab.closed {
		t.Fatalf("expected 1 reaped idle tab, got %d (closed=%v)", reaped, createdTab.closed)
	}
}

// --- Fake CDP endpoint test (WebSocket + HTTP /json/version + /json) ---

func startFakeCDPServer(t *testing.T, handler func(method string, params json.RawMessage) (any, error)) *httptest.Server {
	t.Helper()
	return startFakeCDPServerWithSession(t, func(_ string, method string, params json.RawMessage) (any, error) {
		return handler(method, params)
	})
}

func startFakeCDPServerWithSession(t *testing.T, handler func(sessionID, method string, params json.RawMessage) (any, error)) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/json/version", func(w http.ResponseWriter, _ *http.Request) {
		wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/devtools/browser/fake"
		_ = json.NewEncoder(w).Encode(map[string]string{
			"Browser":              "FakeChrome/130.0",
			"Protocol-Version":     "1.3",
			"webSocketDebuggerUrl": wsURL,
		})
	})
	mux.HandleFunc("/json", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": "A", "type": "page", "url": "https://mail.example/", "title": "Mail"},
			{"id": "W", "type": "service_worker", "url": "https://mail.example/sw.js", "title": "SW"},
		})
	})
	mux.HandleFunc("/devtools/browser/fake", func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Sec-WebSocket-Key")
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "no hijack", 500)
			return
		}
		conn, rw, err := hj.Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		sum := sha1.Sum([]byte(key + wsGUID))
		accept := base64.StdEncoding.EncodeToString(sum[:])
		_, _ = fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", accept)
		_ = rw.Flush()

		br := bufio.NewReader(conn)
		var writeMu sync.Mutex
		sendServerText := func(data []byte) error {
			writeMu.Lock()
			defer writeMu.Unlock()
			var hdr [10]byte
			hdr[0] = 0x81 // FIN + text
			n := len(data)
			idx := 2
			switch {
			case n < 126:
				hdr[1] = byte(n)
			case n <= 65535:
				hdr[1] = 126
				binary.BigEndian.PutUint16(hdr[2:4], uint16(n))
				idx = 4
			default:
				hdr[1] = 127
				binary.BigEndian.PutUint64(hdr[2:10], uint64(n))
				idx = 10
			}
			if _, err := conn.Write(hdr[:idx]); err != nil {
				return err
			}
			_, err := conn.Write(data)
			return err
		}

		for {
			var h [2]byte
			if _, err := io.ReadFull(br, h[:]); err != nil {
				return
			}
			opcode := h[0] & 0x0f
			masked := (h[1] & 0x80) != 0
			length := uint64(h[1] & 0x7f)
			if length == 126 {
				var ext [2]byte
				if _, err := io.ReadFull(br, ext[:]); err != nil {
					return
				}
				length = uint64(binary.BigEndian.Uint16(ext[:]))
			} else if length == 127 {
				var ext [8]byte
				if _, err := io.ReadFull(br, ext[:]); err != nil {
					return
				}
				length = binary.BigEndian.Uint64(ext[:])
			}
			var mask [4]byte
			if masked {
				if _, err := io.ReadFull(br, mask[:]); err != nil {
					return
				}
			}
			payload := make([]byte, length)
			if length > 0 {
				if _, err := io.ReadFull(br, payload); err != nil {
					return
				}
			}
			if masked {
				for i := range payload {
					payload[i] ^= mask[i&3]
				}
			}
			if opcode == 0x8 {
				return
			}
			var req struct {
				ID        int64           `json:"id"`
				SessionID string          `json:"sessionId"`
				Method    string          `json:"method"`
				Params    json.RawMessage `json:"params"`
			}
			if err := json.Unmarshal(payload, &req); err != nil {
				continue
			}
			go func(reqID int64, sessID, method string, params json.RawMessage) {
				res, herr := handler(sessID, method, params)
				resp := map[string]any{"id": reqID}
				if sessID != "" {
					resp["sessionId"] = sessID
				}
				if herr != nil {
					resp["error"] = map[string]any{"code": -32000, "message": herr.Error()}
				} else {
					if res == nil {
						res = map[string]any{}
					}
					resp["result"] = res
				}
				out, _ := json.Marshal(resp)
				_ = sendServerText(out)
			}(req.ID, req.SessionID, req.Method, req.Params)
		}
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestCDPClientAgainstFakeEndpoint(t *testing.T) {
	var seenMethods []string
	var mu sync.Mutex
	srv := startFakeCDPServer(t, func(method string, params json.RawMessage) (any, error) {
		mu.Lock()
		seenMethods = append(seenMethods, method)
		mu.Unlock()
		switch method {
		case "Target.createTarget":
			return map[string]any{"targetId": "fake-target-1"}, nil
		case "Target.attachToTarget":
			return map[string]any{"sessionId": "fake-session-1"}, nil
		case "Runtime.evaluate":
			var p struct {
				Expression string `json:"expression"`
			}
			_ = json.Unmarshal(params, &p)
			if strings.Contains(p.Expression, "document.title") && !strings.Contains(p.Expression, "location.href") {
				return map[string]any{"result": map[string]any{"type": "string", "value": "Fake Title"}}, nil
			}
			if strings.Contains(p.Expression, "location.href") {
				return map[string]any{"result": map[string]any{"type": "object", "value": map[string]any{
					"url": "https://fake.test/", "title": "Fake Title", "width": 1280, "height": 720, "dpr": 1,
				}}}, nil
			}
			return map[string]any{"result": map[string]any{"type": "number", "value": 42}}, nil
		default:
			return map[string]any{}, nil
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// upstream: browser-targets.test.ts: "lists a CDP endpoint's pages from its /json list"
	targets, text, err := ListCDPTargets(ctx, srv.URL)
	if err != nil {
		t.Fatalf("ListCDPTargets failed: %v", err)
	}
	if len(targets) != 1 || targets[0].Title != "Mail" || targets[0].URL != "https://mail.example/" {
		t.Fatalf("unexpected CDP targets: %+v", targets)
	}
	if !strings.Contains(text, `"Mail" https://mail.example/`) {
		t.Fatalf("unexpected target description: %s", text)
	}

	// Open a connected tab via ChromiumBackend against the fake CDP server.
	backend := NewChromiumBackend()
	tab, err := backend.Open(ctx, OpenOptions{
		Name:     "fake",
		Endpoint: srv.URL,
	})
	if err != nil {
		t.Fatalf("Open connected CDP failed: %v", err)
	}
	defer func() { _ = tab.Close(ctx, false) }()

	if info := tab.Info(); info.Title != "Fake Title" || info.TargetID != "fake-target-1" {
		t.Fatalf("unexpected tab info: %+v", info)
	}

	res, err := tab.Call(ctx, CallContext{}, []CallStep{{Method: "title"}})
	if err != nil || res.Value != "Fake Title" {
		t.Fatalf("tab.title() failed: res=%+v err=%v", res, err)
	}

	evalRes, err := tab.Call(ctx, CallContext{}, []CallStep{{
		Method: "evaluate",
		Args:   []json.RawMessage{json.RawMessage(`"21 * 2"`)},
	}})
	if err != nil || evalRes.Value != float64(42) {
		t.Fatalf("tab.evaluate() failed: res=%+v err=%v", evalRes, err)
	}

	// Verify tab.run() returns the clear Go-backend limitation error.
	if _, err := tab.Run(ctx, CallContext{}, "return 1;", "", nil); err == nil || !strings.Contains(err.Error(), "tab.evaluate") {
		t.Fatalf("expected clear tab.run() error, got %v", err)
	}
}

// --- Real headless Chromium integration test ---

func TestRealHeadlessChromiumEndToEnd(t *testing.T) {
	if os.Getenv("OMP_REAL_BROWSER") == "" {
		t.Skip("skipping real headless Chromium test (set OMP_REAL_BROWSER=1 to run)")
	}
	exe, err := FindChromiumExecutable(os.LookupEnv)
	if err != nil {
		t.Skipf("skipping real headless Chromium test (no Chrome-family browser installed): %v", err)
	}
	t.Logf("using Chromium executable: %s", exe)
	pageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<!doctype html>
<html>
<head><title>OhMyPig Browser Test</title></head>
<body>
  <h1 id="heading">Welcome</h1>
  <label for="name-input">Your Name</label>
  <input id="name-input" type="text" placeholder="Enter name" data-testid="name-field" />
  <select id="color-sel"><option value="r">Red</option><option value="b">Blue</option></select>
  <input id="agree-cb" type="checkbox" />
  <button id="go-btn" onclick="console.log('clicked', document.getElementById('name-input').value); document.getElementById('out').textContent = 'Hello, ' + document.getElementById('name-input').value + '!'">Greet</button>
  <p id="out">Waiting</p>
</body>
</html>`)
	}))
	defer pageServer.Close()

	origBackends := RegisteredBackends()
	t.Cleanup(func() {
		for k := range RegisteredBackends() {
			RegisterBackend(k, nil)
		}
		for k, b := range origBackends {
			RegisterBackend(k, b)
		}
	})

	chrom := NewChromiumBackend()
	RegisterBackend(string(KindHeadless), chrom)

	m := NewManager()
	// Force headless Chromium (skip Tern/WebKit/relay/cmux in test env).
	m.env = envLookup(map[string]string{})
	s := DefaultSettings()
	s.WebKit = false
	s.Relay = false
	s.Tern = false
	s.Cmux = false
	s.Headless = true
	s.ScreenshotDir = t.TempDir()
	m.SetSettingsProvider(func() Settings { return s })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req := InvokeRequest{Cwd: t.TempDir(), OwnerSessionID: "e2e-session"}
	defer m.ReleaseAllTabs(context.Background(), true)

	// 1. Open
	openParams, _ := json.Marshal(map[string]any{
		"action": "open",
		"name":   "e2e",
		"url":    pageServer.URL,
		"app":    "chromium",
	})
	openRes, err := m.Invoke(ctx, req, openParams)
	if err != nil {
		t.Fatalf("browser.open failed: %v", err)
	}
	if !strings.Contains(openRes.Text, `Opened tab "e2e"`) || !strings.Contains(openRes.Text, "OhMyPig Browser Test") {
		t.Fatalf("unexpected open text: %s", openRes.Text)
	}

	// 2. Observe (via CDP Accessibility.getFullAXTree)
	obsRes, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"e2e","chain":[{"method":"observe","args":[]}]}`))
	if err != nil {
		t.Fatalf("tab.observe() failed: %v", err)
	}
	obs, ok := obsRes.Details.Value.(Observation)
	if !ok {
		t.Fatalf("expected Observation struct, got %T", obsRes.Details.Value)
	}
	if len(obs.Elements) < 2 {
		t.Fatalf("expected >= 2 interactive elements (input + button), got %+v", obs.Elements)
	}
	var btnID int
	for _, em := range obs.Elements {
		if em.Role == "button" && em.Name == "Greet" {
			btnID = em.ID
		}
	}
	if btnID == 0 {
		t.Fatalf("did not find Greet button in observe elements: %+v", obs.Elements)
	}

	// Capture launched Chromium PID and profile dir before closing to verify OS-level cleanup.
	chrom.mu.Lock()
	var launchedPID int
	var profileDir string
	for _, proc := range chrom.browsers {
		if proc.launched != nil {
			launchedPID = proc.launched.pid
			profileDir = proc.launched.userDataDir
		}
	}
	chrom.mu.Unlock()
	if launchedPID <= 0 || profileDir == "" {
		t.Fatalf("expected launched PID > 0 and temp profileDir, got pid=%d dir=%q", launchedPID, profileDir)
	}
	// 3. Fill via label/ selector
	if _, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"e2e","chain":[{"method":"fill","args":["label/Your Name","Ada"]}]}`)); err != nil {
		t.Fatalf("tab.fill() failed: %v", err)
	}

	// 4. Click via tab.id(btnID).click()
	clickJSON := fmt.Sprintf(`{"action":"call","name":"e2e","chain":[{"method":"id","args":[%d]},{"method":"click","args":[]}]}`, btnID)
	if _, err := m.Invoke(ctx, req, json.RawMessage(clickJSON)); err != nil {
		t.Fatalf("tab.id(%d).click() failed: %v", btnID, err)
	}

	// 5. Text read
	textRes, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"e2e","chain":[{"method":"text","args":["#out"]}]}`))
	if err != nil {
		t.Fatalf("tab.text('#out') failed: %v", err)
	}
	if textRes.Details.Value != "Hello, Ada!" {
		t.Fatalf("expected 'Hello, Ada!', got %q", textRes.Details.Value)
	}

	// 6. ARIA snapshot (using embedded Playwright aria-snapshot.bundle.txt)
	ariaRes, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"e2e","chain":[{"method":"ariaSnapshot","args":[]}]}`))
	if err != nil {
		t.Fatalf("tab.ariaSnapshot() failed: %v", err)
	}
	ariaStr, _ := ariaRes.Details.Value.(string)
	if !strings.Contains(ariaStr, "Greet") || !strings.Contains(ariaStr, "[ref=e") {
		t.Fatalf("unexpected ariaSnapshot output:\n%s", ariaStr)
	}

	// 7. Screenshot
	shotRes, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"e2e","chain":[{"method":"screenshot","args":[]}]}`))
	if err != nil {
		t.Fatalf("tab.screenshot() failed: %v", err)
	}
	if len(shotRes.Details.Screenshots) != 1 || len(shotRes.Images) != 1 {
		t.Fatalf("expected 1 screenshot + 1 image, got %d / %d", len(shotRes.Details.Screenshots), len(shotRes.Images))
	}
	dest := shotRes.Details.Screenshots[0].Dest
	if st, err := os.Stat(dest); err != nil || st.Size() < 100 {
		t.Fatalf("expected non-empty screenshot file at %s, err=%v", dest, err)
	}

	// 8. Select, check, role/ & testid/ & text/ & xpath/ selectors, console capture, evaluate
	selRes, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"e2e","chain":[{"method":"select","args":["#color-sel",{"label":"Blue"}]}]}`))
	if err != nil {
		t.Fatalf("tab.select() failed: %v", err)
	}
	if arr, _ := selRes.Details.Value.([]any); len(arr) != 1 || arr[0] != "b" {
		t.Fatalf("unexpected select result: %+v", selRes.Details.Value)
	}
	if _, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"e2e","chain":[{"method":"check","args":["#agree-cb"]}]}`)); err != nil {
		t.Fatalf("tab.check() failed: %v", err)
	}
	chkRes, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"e2e","chain":[{"method":"isChecked","args":["#agree-cb"]}]}`))
	if err != nil || chkRes.Details.Value != true {
		t.Fatalf("expected isChecked=true, got %+v err=%v", chkRes, err)
	}
	for _, sel := range []string{`role/button[name="Greet" exact]`, `testid/name-field`, `placeholder/Enter name`, `text/Hello, Ada!`, `xpath///h1[@id='heading']`} {
		cJSON, _ := json.Marshal(map[string]any{"action": "call", "name": "e2e", "chain": []map[string]any{{"method": "count", "args": []any{sel}}}})
		cntRes, err := m.Invoke(ctx, req, cJSON)
		if err != nil || cntRes.Details.Value != float64(1) {
			t.Fatalf("selector %q: want count 1, got %+v err=%v", sel, cntRes, err)
		}
	}
	consRes, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"e2e","chain":[{"method":"console","args":[]}]}`))
	if err != nil {
		t.Fatalf("tab.console() failed: %v", err)
	}
	consMap, _ := consRes.Details.Value.(map[string]any)
	if entries, _ := consMap["entries"].([]cdpConsoleEntry); len(entries) == 0 || entries[0].Text != "clicked Ada" {
		t.Fatalf("expected captured console entry 'clicked Ada' from click, got %+v", consMap)
	}
	evalRes, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"e2e","chain":[{"method":"evaluate","args":["1 + 41"]}]}`))
	if err != nil || evalRes.Details.Value != float64(42) {
		t.Fatalf("tab.evaluate() failed: res=%+v err=%v", evalRes, err)
	}
	// Verify {__omp_fn} form (what prelude.js sends for JS functions)
	fnRes, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"e2e","chain":[{"method":"evaluate","args":[{"__omp_fn":"(a, b) => a * b"}, 6, 7]}]}`))
	if err != nil || fnRes.Details.Value != float64(42) {
		t.Fatalf("tab.evaluate({__omp_fn}) failed: res=%+v err=%v", fnRes, err)
	}

	// 9. Close and verify no tabs, Chromium handles, OS process, or profile directory remain.
	closeRes, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"close","name":"e2e"}`))
	if err != nil || !strings.Contains(closeRes.Text, `Released managed tab "e2e"`) {
		t.Fatalf("close failed: res=%+v err=%v", closeRes, err)
	}
	if len(m.ListTabs()) != 0 {
		t.Fatalf("expected 0 tabs after close, got %d", len(m.ListTabs()))
	}
	chrom.mu.Lock()
	remaining := len(chrom.browsers)
	chrom.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("expected 0 live Chromium processes after close, got %d", remaining)
	}
	if _, err := os.Stat(profileDir); !os.IsNotExist(err) {
		t.Fatalf("expected temp profile dir %q to be deleted, stat err=%v", profileDir, err)
	}
	if processAlive(launchedPID) {
		t.Fatalf("expected Chromium process pid %d to be gone after close", launchedPID)
	}
}

// TestRealHeadlessChromiumActionabilityFramesAndScreenshot verifies click
// actionability refusal, iframe offset clicking, CSS-pixel screenshot sizing,
// dialog handling, and process/profile cleanup against real headless Chromium.
func TestRealHeadlessChromiumActionabilityFramesAndScreenshot(t *testing.T) {
	if os.Getenv("OMP_REAL_BROWSER") == "" {
		t.Skip("skipping real headless Chromium test (set OMP_REAL_BROWSER=1 to run)")
	}
	if _, err := FindChromiumExecutable(os.LookupEnv); err != nil {
		t.Skipf("no Chromium executable: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.URL.Path == "/frame" {
			_, _ = io.WriteString(w, `<!doctype html><html><body style="margin:0"><button id="fbtn" style="width:120px;height:40px" onclick="document.getElementById('fout').textContent='frame-clicked'">FrameGo</button><p id="fout">init</p></body></html>`)
			return
		}
		_, _ = io.WriteString(w, `<!doctype html><html><body style="margin:0">
  <div style="position:relative;width:200px;height:60px">
    <button id="covered-btn" style="position:absolute;inset:0" onclick="window.__hit=true">Under</button>
    <div id="overlay" style="position:absolute;inset:0;background:rgba(0,0,0,0.5)">Banner</div>
  </div>
  <button id="dlg-btn" onclick="window.__dlgAns = confirm('Continue?')">Ask</button>
  <iframe id="pay" name="payframe" src="/frame" style="margin-left:120px;margin-top:80px;width:200px;height:120px;border:0"></iframe>
</body></html>`)
	}))
	defer srv.Close()

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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req := InvokeRequest{Cwd: t.TempDir(), OwnerSessionID: "e2e-act"}
	defer m.ReleaseAllTabs(context.Background(), true)

	openParams, _ := json.Marshal(map[string]any{"action": "open", "name": "act", "url": srv.URL, "app": "chromium"})
	if _, err := m.Invoke(ctx, req, openParams); err != nil {
		t.Fatalf("open failed: %v", err)
	}

	// Capture PID & profile before running actions.
	chrom.mu.Lock()
	var pid int
	var profDir string
	for _, p := range chrom.browsers {
		if p.launched != nil {
			pid, profDir = p.launched.pid, p.launched.userDataDir
		}
	}
	chrom.mu.Unlock()

	// 1. Covered button must be refused with omp's "blocked: covered by <div#overlay>".
	_, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"act","chain":[{"method":"click","args":["#covered-btn"]}]}`))
	if err == nil || !strings.Contains(err.Error(), "covered by <div#overlay>") {
		t.Fatalf("expected covered refusal naming <div#overlay>, got %v", err)
	}

	// 2. Frame click at offset iframe must hit #fbtn inside the iframe.
	if _, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"act","chain":[{"method":"frame","args":["#pay"]},{"method":"click","args":["#fbtn"]}]}`)); err != nil {
		t.Fatalf("frame click failed: %v", err)
	}
	ftxt, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"act","chain":[{"method":"frame","args":["#pay"]},{"method":"text","args":["#fout"]}]}`))
	if err != nil || ftxt.Details.Value != "frame-clicked" {
		t.Fatalf("expected iframe text 'frame-clicked', got %+v err=%v", ftxt, err)
	}

	// 3. Screenshot must be delivered in CSS pixels (1365x768, not 1706x960) with omp's line.
	ss, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"act","chain":[{"method":"screenshot","args":[]}]}`))
	if err != nil {
		t.Fatalf("screenshot failed: %v", err)
	}
	if len(ss.Details.Screenshots) != 1 || ss.Details.Screenshots[0].Width != 1365 || ss.Details.Screenshots[0].Height != 768 {
		t.Fatalf("expected CSS-pixel 1365x768 screenshot, got %+v", ss.Details.Screenshots)
	}
	if !strings.HasPrefix(ss.Text, "Screenshot 1365x768") {
		t.Fatalf("expected omp screenshot line, got %q", ss.Text)
	}

	// 4. Click opening confirm() finishes cleanly and setDialogs("accept") settles it.
	if _, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"act","chain":[{"method":"click","args":["#dlg-btn"]}]}`)); err != nil {
		t.Fatalf("click opening confirm failed: %v", err)
	}
	if _, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"act","chain":[{"method":"setDialogs","args":["accept"]}]}`)); err != nil {
		t.Fatalf("setDialogs(accept) failed: %v", err)
	}
	ans, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"call","name":"act","chain":[{"method":"evaluate","args":["window.__dlgAns"]}]}`))
	if err != nil || ans.Details.Value != true {
		t.Fatalf("expected window.__dlgAns === true after setDialogs(accept), got %+v err=%v", ans, err)
	}

	// 5. Close and verify OS process + temp profile cleanup.
	if _, err := m.Invoke(ctx, req, json.RawMessage(`{"action":"close","name":"act"}`)); err != nil {
		t.Fatalf("close failed: %v", err)
	}
	if _, err := os.Stat(profDir); !os.IsNotExist(err) {
		t.Fatalf("expected profile dir %q removed, err=%v", profDir, err)
	}
	if processAlive(pid) {
		t.Fatalf("expected Chromium pid %d exited after close", pid)
	}
}
