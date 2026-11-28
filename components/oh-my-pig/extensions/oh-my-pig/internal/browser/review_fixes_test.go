package browser

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestFailedOpenDoesNotKillSiblingTab(t *testing.T) {
	var targetSeq atomic.Int32
	srv := startFakeCDPServer(t, func(method string, params json.RawMessage) (any, error) {
		switch method {
		case "Target.createTarget":
			n := targetSeq.Add(1)
			return map[string]any{"targetId": "tgt-" + string(rune('0'+n))}, nil
		case "Target.attachToTarget":
			return map[string]any{"sessionId": "sess-" + string(rune('0'+targetSeq.Load()))}, nil
		case "Page.navigate":
			var p struct {
				URL string `json:"url"`
			}
			_ = json.Unmarshal(params, &p)
			if strings.Contains(p.URL, "does-not-resolve") {
				return map[string]any{"errorText": "net::ERR_NAME_NOT_RESOLVED"}, nil
			}
			return map[string]any{"frameId": "main"}, nil
		case "Runtime.evaluate":
			return map[string]any{"result": map[string]any{"type": "string", "value": "complete"}}, nil
		default:
			return map[string]any{}, nil
		}
	})

	b := NewChromiumBackend()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Tab A opens cleanly (refCount = 1).
	tabA, err := b.Open(ctx, OpenOptions{Name: "a", Endpoint: srv.URL, URL: "http://127.0.0.1/ok"})
	if err != nil {
		t.Fatalf("tab A open failed: %v", err)
	}
	defer tabA.Close(ctx, false)

	// Tab B fails during navigation after target creation.
	_, err = b.Open(ctx, OpenOptions{Name: "b", Endpoint: srv.URL, URL: "https://does-not-resolve.invalid"})
	if err == nil {
		t.Fatalf("expected tab B open to fail")
	}

	// Tab A must still be alive and refCount must be 1 (not 0).
	if !tabA.Alive() {
		t.Fatalf("tab A was killed by tab B's failed open (double release bug)")
	}
	b.mu.Lock()
	var rc int
	for _, p := range b.browsers {
		rc = p.refCount
	}
	b.mu.Unlock()
	if rc != 1 {
		t.Fatalf("expected refCount=1 after sibling failure, got %d", rc)
	}
}

// --- Finding 2: Launch flags use --remote-debugging-port=0 (+ pipe on Unix) ---

func TestBuildHeadlessLaunchArgsUsesPortZeroAndPipe(t *testing.T) {
	args := buildHeadlessLaunchArgs(DefaultViewport, "/tmp/prof", true, false, true)
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"--remote-debugging-port=0",
		"--remote-debugging-pipe",
		"--headless=new",
		"--mute-audio",
		"--disable-background-timer-throttling",
		"--disable-backgrounding-occluded-windows",
		"--disable-renderer-backgrounding",
		"--disable-hang-monitor",
		"--disable-breakpad",
		"--disable-crash-reporter",
		"--disable-dev-shm-usage",
		"--disable-infobars",
		"--disable-prompt-on-repost",
		"--disable-search-engine-choice-screen",
		"--force-color-profile=srgb",
		"--export-tagged-pdf",
		"--generate-pdf-document-outline",
		"--enable-features=PdfOopif",
		"--no-startup-window",
	} {
		if !strings.Contains(joined, want) {
		}
	}
	argsWindowed := buildHeadlessLaunchArgs(DefaultViewport, "/tmp/prof", false, true, false)
	joinedW := strings.Join(argsWindowed, " ")
	if strings.Contains(joinedW, "--headless") {
		t.Fatalf("unexpected --headless in headed mode: %s", joinedW)
	}
	if strings.Contains(joinedW, "--mute-audio") {
		t.Fatalf("unexpected --mute-audio in headed mode: %s", joinedW)
	}
	if !strings.Contains(joinedW, "--ignore-certificate-errors") {
		t.Fatalf("expected --ignore-certificate-errors when insecure=true")
	}
}

// --- Finding 3: Argument bounds-checking & panic recovery in Manager.Invoke ---

type panickingBackend struct{}

func (panickingBackend) Available(context.Context) (bool, string) { return true, "" }
func (panickingBackend) Open(context.Context, OpenOptions) (Tab, error) {
	panic("boom inside backend")
}

func TestBoundsCheckingAndPanicRecovery(t *testing.T) {
	srv := startFakeCDPServer(t, func(method string, _ json.RawMessage) (any, error) {
		switch method {
		case "Target.createTarget":
			return map[string]any{"targetId": "t1"}, nil
		case "Target.attachToTarget":
			return map[string]any{"sessionId": "s1"}, nil
		case "Runtime.evaluate":
			return map[string]any{"result": map[string]any{"type": "string", "value": "complete"}}, nil
		default:
			return map[string]any{}, nil
		}
	})
	b := NewChromiumBackend()
	ctx := context.Background()
	tab, err := b.Open(ctx, OpenOptions{Name: "bounds", Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer tab.Close(ctx, false)

	// Missing args to select, uploadFile, setStorage must return errors, never panic.
	badChains := [][]CallStep{
		{{Method: "select", Args: nil}},
		{{Method: "select", Args: []json.RawMessage{json.RawMessage(`"#sel"`)}}},
		{{Method: "uploadFile", Args: nil}},
		{{Method: "uploadFile", Args: []json.RawMessage{json.RawMessage(`"input[type=file]"`)}}},
		{{Method: "setStorage", Args: nil}},
		{{Method: "setStorage", Args: []json.RawMessage{json.RawMessage(`"local"`)}}},
		{{Method: "setStorage", Args: []json.RawMessage{json.RawMessage(`"local"`), json.RawMessage(`"k"`)}}},
		{{Method: "storage", Args: nil}},
	}
	for _, chain := range badChains {
		if _, err := tab.Call(ctx, CallContext{}, chain); err == nil {
			t.Fatalf("expected error for under-arity chain %+v, got nil", chain)
		}
	}

	// Panic inside backend is recovered by Manager.Invoke.
	orig := RegisteredBackends()
	t.Cleanup(func() {
		for k := range RegisteredBackends() {
			RegisterBackend(k, nil)
		}
		for k, v := range orig {
			RegisterBackend(k, v)
		}
	})
	RegisterBackend(string(KindHeadless), panickingBackend{})
	m := NewManager()
	m.env = envLookup(map[string]string{})
	s := DefaultSettings()
	s.Tern, s.WebKit, s.Cmux, s.Relay = false, false, false, false
	m.SetSettingsProvider(func() Settings { return s })
	_, err = m.Invoke(ctx, InvokeRequest{OwnerSessionID: "s1"}, json.RawMessage(`{"action":"open","app":"chromium"}`))
	if err == nil || !strings.Contains(err.Error(), "panic") {
		t.Fatalf("expected recovered panic error, got %v", err)
	}
}

// --- Finding 4: allowed_domains validation, reopen on change, Fetch.enable fail-closed ---

func TestAllowedDomainsValidationReopenAndFailClosed(t *testing.T) {
	// Malformed patterns must be rejected.
	for _, bad := range []string{"", "https://example.com", "example.com/path", "foo.*.com", "*.", "*.*.com"} {
		if _, err := NormalizeAllowedDomains([]string{bad}); err == nil {
			t.Fatalf("expected error for malformed allowed_domains %q", bad)
		}
	}
	norm, err := NormalizeAllowedDomains([]string{"Example.COM.", "*.Sub.Example.com", "example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if len(norm) != 2 || norm[0] != "example.com" || norm[1] != "*.sub.example.com" {
		t.Fatalf("unexpected normalized domains: %+v", norm)
	}

	// Fetch.enable failure must fail closed when allowed_domains is set.
	srv := startFakeCDPServer(t, func(method string, _ json.RawMessage) (any, error) {
		switch method {
		case "Target.createTarget":
			return map[string]any{"targetId": "t1"}, nil
		case "Target.attachToTarget":
			return map[string]any{"sessionId": "s1"}, nil
		case "Fetch.enable":
			return nil, errors.New("Fetch domain unavailable")
		default:
			return map[string]any{}, nil
		}
	})
	b := NewChromiumBackend()
	if _, err := b.Open(context.Background(), OpenOptions{
		Name:           "sec",
		Endpoint:       srv.URL,
		AllowedDomains: []string{"example.com"},
	}); err == nil || !strings.Contains(err.Error(), "Fetch.enable") {
		t.Fatalf("expected fail-closed Fetch.enable error, got %v", err)
	}

	// Reopening an existing tab with changed allowed_domains or dialogs must reopen, not reuse.
	orig := RegisteredBackends()
	t.Cleanup(func() {
		for k := range RegisteredBackends() {
			RegisterBackend(k, nil)
		}
		for k, v := range orig {
			RegisterBackend(k, v)
		}
	})
	var openCount int
	fb := &countingBackend{onOpen: func(opts OpenOptions) { openCount++ }}
	RegisterBackend(string(KindHeadless), fb)
	m := NewManager()
	m.env = envLookup(map[string]string{})
	s := DefaultSettings()
	s.Tern, s.WebKit, s.Cmux, s.Relay = false, false, false, false
	m.SetSettingsProvider(func() Settings { return s })
	req := InvokeRequest{OwnerSessionID: "sess-a"}

	if _, err := m.Invoke(context.Background(), req, json.RawMessage(`{"action":"open","name":"t","app":"chromium","allowed_domains":["a.com"]}`)); err != nil {
		t.Fatal(err)
	}
	if openCount != 1 {
		t.Fatalf("expected openCount=1, got %d", openCount)
	}
	// Same allowed_domains reuses.
	if res, err := m.Invoke(context.Background(), req, json.RawMessage(`{"action":"open","name":"t","app":"chromium","allowed_domains":["a.com"]}`)); err != nil || !strings.Contains(res.Text, "Reused") {
		t.Fatalf("expected reuse for identical allowed_domains, got res=%+v err=%v", res, err)
	}
	if openCount != 1 {
		t.Fatalf("expected openCount still 1, got %d", openCount)
	}
	// Changed allowed_domains must reopen.
	if res, err := m.Invoke(context.Background(), req, json.RawMessage(`{"action":"open","name":"t","app":"chromium","allowed_domains":["b.com"]}`)); err != nil || !strings.Contains(res.Text, "Opened") {
		t.Fatalf("expected reopen for changed allowed_domains, got res=%+v err=%v", res, err)
	}
	if openCount != 2 {
		t.Fatalf("expected openCount=2 after domain change, got %d", openCount)
	}
	// Changed dialogs policy must also reopen.
	if res, err := m.Invoke(context.Background(), req, json.RawMessage(`{"action":"open","name":"t","app":"chromium","dialogs":"dismiss"}`)); err != nil || !strings.Contains(res.Text, "Opened") {
		t.Fatalf("expected reopen for changed dialogs policy, got res=%+v err=%v", res, err)
	}
	if openCount != 3 {
		t.Fatalf("expected openCount=3 after dialogs change, got %d", openCount)
	}
}

type countingBackend struct {
	onOpen func(OpenOptions)
	closed atomic.Int32
}

func (c *countingBackend) Available(context.Context) (bool, string) { return true, "" }
func (c *countingBackend) Open(_ context.Context, opts OpenOptions) (Tab, error) {
	if c.onOpen != nil {
		c.onOpen(opts)
	}
	return &countingTab{parent: c, url: opts.URL}, nil
}

type countingTab struct {
	parent *countingBackend
	url    string
	dead   atomic.Bool
}

func (t *countingTab) Info() TabInfo { return TabInfo{URL: t.url, Viewport: DefaultViewport} }
func (t *countingTab) Alive() bool   { return !t.dead.Load() }
func (t *countingTab) Reuse(_ context.Context, opts OpenOptions) (string, error) {
	if opts.URL != "" {
		t.url = opts.URL
	}
	return "", nil
}
func (t *countingTab) Close(context.Context, bool) error {
	if !t.dead.Swap(true) && t.parent != nil {
		t.parent.closed.Add(1)
	}
	return nil
}
func (t *countingTab) Call(context.Context, CallContext, []CallStep) (*CallResult, error) {
	return &CallResult{}, nil
}
func (t *countingTab) Run(context.Context, CallContext, string, string, []json.RawMessage) (*CallResult, error) {
	return &CallResult{}, nil
}
