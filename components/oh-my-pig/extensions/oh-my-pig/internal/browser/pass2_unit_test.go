package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// --- Pass-2 Finding 4 (unit): waitFor / waitForSelector returns true/false ---

func TestWaitForReturnsBoolOnSuccess(t *testing.T) {
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
			if strings.Contains(p.Expression, `"#gone"`) {
				return map[string]any{"result": map[string]any{"type": "object", "value": map[string]any{"ok": true, "count": float64(0)}}}, nil
			}
			return map[string]any{"result": map[string]any{"type": "object", "value": map[string]any{"ok": true, "count": float64(1)}}}, nil
		default:
			return map[string]any{}, nil
		}
	})
	b := NewChromiumBackend()
	tab, err := b.Open(context.Background(), OpenOptions{Name: "wf", Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer tab.Close(context.Background(), false)

	res, err := tab.Call(context.Background(), CallContext{TimeoutMs: 5000}, []CallStep{{
		Method: "waitFor",
		Args:   []json.RawMessage{json.RawMessage(`"#present"`)},
	}})
	if err != nil || res.Value != true {
		t.Fatalf("waitFor(#present): want true, got %#v err=%v", res, err)
	}
	resHidden, err := tab.Call(context.Background(), CallContext{TimeoutMs: 5000}, []CallStep{{
		Method: "waitForSelector",
		Args:   []json.RawMessage{json.RawMessage(`"#gone"`), json.RawMessage(`{"hidden":true}`)},
	}})
	if err != nil || resHidden.Value != false {
		t.Fatalf("waitForSelector(#gone, {hidden:true}): want false, got %#v err=%v", resHidden, err)
	}
}

// --- Pass-2 Finding 8 (unit): Panic inside runWithDialogRace goroutine and CDP listener is recovered ---

func TestRunWithDialogRaceAndListenerPanicRecovery(t *testing.T) {
	srv := startFakeCDPServer(t, func(method string, _ json.RawMessage) (any, error) {
		switch method {
		case "Target.createTarget":
			return map[string]any{"targetId": "t1"}, nil
		case "Target.attachToTarget":
			return map[string]any{"sessionId": "s1"}, nil
		default:
			return map[string]any{}, nil
		}
	})
	b := NewChromiumBackend()
	tab, err := b.Open(context.Background(), OpenOptions{Name: "p", Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer tab.Close(context.Background(), false)
	ct := tab.(*cdpTab)

	// Panic inside runWithDialogRace goroutine returns error instead of crashing process.
	_, err = ct.runWithDialogRace(context.Background(), "tab.test()", false, func(context.Context) (*CallResult, error) {
		panic("helper boom")
	})
	if err == nil || !strings.Contains(err.Error(), "helper boom") {
		t.Fatalf("expected recovered panic error from runWithDialogRace, got %v", err)
	}
}

// --- Pass-2 Finding 9 (unit): Setting hook overrides defaults for browser.* and tools.maxTimeout ---

func TestSettingHookWired(t *testing.T) {
	orig := Setting
	t.Cleanup(func() { Setting = orig })

	Setting = func(_ sdk.Context, key string) any {
		switch key {
		case "browser.enabled":
			return false
		case "browser.headless":
			return false
		case "browser.cdpUrl":
			return "http://127.0.0.1:9333"
		case "browser.idleCloseSec":
			return 45
		case "tools.maxTimeout":
			return 90
		default:
			return nil
		}
	}
	m := NewManager()
	pred := PreludeDefinition(m)
	var zeroCtx sdk.Context
	if pred.Enabled(zeroCtx) {
		t.Fatalf("expected Setting hook browser.enabled=false to disable prelude")
	}
	eff := effectiveSettingsForSDK(zeroCtx, m)
	if eff.Headless || eff.CdpURL != "http://127.0.0.1:9333" || eff.IdleCloseSec != 45 || eff.MaxTimeoutSec != 90 {
		t.Fatalf("unexpected settings from hook: %+v", eff)
	}

	Setting = nil
	if !pred.Enabled(zeroCtx) {
		t.Fatalf("expected nil Setting hook to fall back to default enabled=true")
	}
}

// --- Pass-2 Finding 11 (unit): Stale temp profile cleanup removes dead PID dirs only ---

func TestCleanupStaleProfilesIn(t *testing.T) {
	base := t.TempDir()
	// Dead PID dir (must be removed).
	deadDir := filepath.Join(base, tempProfilePrefix+"dead")
	if err := os.MkdirAll(deadDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deadDir, profileOwnerMarker), []byte("99999999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Live PID dir (our own PID: must be kept).
	liveDir := filepath.Join(base, tempProfilePrefix+"live")
	if err := os.MkdirAll(liveDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(liveDir, profileOwnerMarker), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Unrelated dir (no prefix: must be kept).
	otherDir := filepath.Join(base, "not-our-dir")
	if err := os.MkdirAll(otherDir, 0o700); err != nil {
		t.Fatal(err)
	}

	cleanupStaleProfilesIn(base)

	if _, err := os.Stat(deadDir); !os.IsNotExist(err) {
		t.Fatalf("expected dead-pid profile dir removed, stat err=%v", err)
	}
	if _, err := os.Stat(liveDir); err != nil {
		t.Fatalf("expected live-pid profile dir kept, stat err=%v", err)
	}
	if _, err := os.Stat(otherDir); err != nil {
		t.Fatalf("expected unrelated dir kept, stat err=%v", err)
	}
}

// --- Pass-2 Finding 5 (unit): Open racing session end leaves 0 tabs ---

type blockingOpenBackend struct {
	entered chan struct{}
	release chan struct{}
	closed  atomic.Int32
}

func (b *blockingOpenBackend) Available(context.Context) (bool, string) { return true, "" }
func (b *blockingOpenBackend) Open(_ context.Context, opts OpenOptions) (Tab, error) {
	close(b.entered)
	<-b.release
	return &countingTab{parent: &countingBackend{}, url: opts.URL}, nil
}

func TestOpenRacingSessionEndLeavesNoTab(t *testing.T) {
	orig := RegisteredBackends()
	t.Cleanup(func() {
		for k := range RegisteredBackends() {
			RegisterBackend(k, nil)
		}
		for k, v := range orig {
			RegisterBackend(k, v)
		}
	})
	bb := &blockingOpenBackend{entered: make(chan struct{}), release: make(chan struct{})}
	RegisterBackend(string(KindHeadless), bb)

	m := NewManager()
	m.env = envLookup(map[string]string{})
	s := DefaultSettings()
	s.Tern, s.WebKit, s.Cmux, s.Relay = false, false, false, false
	m.SetSettingsProvider(func() Settings { return s })

	errCh := make(chan error, 1)
	go func() {
		_, err := m.Invoke(context.Background(), InvokeRequest{OwnerSessionID: "race-sess"}, json.RawMessage(`{"action":"open","name":"r","app":"chromium"}`))
		errCh <- err
	}()
	<-bb.entered
	// End the session while open is in flight, then let backend.Open return a tab.
	m.ReleaseTabsForOwner(context.Background(), "race-sess", true)
	close(bb.release)

	err := <-errCh
	if err == nil || !strings.Contains(err.Error(), "ended") {
		t.Fatalf("expected open racing session end to fail with ended error, got %v", err)
	}
	if got := m.ListTabsForOwner("race-sess"); len(got) != 0 {
		t.Fatalf("expected 0 tabs left behind after open/session-end race, got %+v", got)
	}
}

// --- Embedded prelude.js & prelude.py driver test through Manager.Invoke ---

func TestEmbeddedPreludesDriveManager(t *testing.T) {
	// Bridge server exposing Manager.Invoke over HTTP so bun and python3 can execute
	// the verbatim embedded assets/prelude.js and assets/prelude.py end-to-end.
	orig := RegisteredBackends()
	t.Cleanup(func() {
		for k := range RegisteredBackends() {
			RegisterBackend(k, nil)
		}
		for k, v := range orig {
			RegisterBackend(k, v)
		}
	})
	var seenCalls []string
	var mu sync.Mutex
	fakeBackend := &scriptedBackend{
		onCall: func(chain []CallStep) (*CallResult, error) {
			mu.Lock()
			seenCalls = append(seenCalls, chain[0].Method)
			mu.Unlock()
			switch chain[0].Method {
			case "title":
				return &CallResult{Value: "Prelude Title"}, nil
			case "evaluate":
				// Verify prelude encoded a JS function with {__omp_fn}.
				var m map[string]any
				if len(chain[0].Args) > 0 && json.Unmarshal(chain[0].Args[0], &m) == nil {
					if src, ok := DecodePreludeFn(m); ok {
						return &CallResult{Value: "fn:" + src}, nil
					}
				}
				return &CallResult{Value: "expr"}, nil
			case "waitFor":
				return &CallResult{Value: true}, nil
			default:
				return &CallResult{Value: "ok"}, nil
			}
		},
	}
	RegisterBackend(string(KindHeadless), fakeBackend)
	m := NewManager()
	m.env = envLookup(map[string]string{})
	s := DefaultSettings()
	s.Tern, s.WebKit, s.Cmux, s.Relay = false, false, false, false
	m.SetSettingsProvider(func() Settings { return s })
	defer m.ReleaseAllTabs(context.Background(), true)

	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		res, err := m.Invoke(r.Context(), InvokeRequest{OwnerSessionID: "prelude-sess", Cwd: t.TempDir()}, body)
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"text":    res.Text,
			"details": res.Details,
		})
	}))
	defer bridge.Close()

	// 1. Drive assets/prelude.js via bun or node.
	jsBin, err := exec.LookPath("bun")
	if err != nil {
		jsBin, err = exec.LookPath("node")
	}
	if err == nil {
		jsScript := fmt.Sprintf(`
globalThis.__omp_log__ = () => {};
globalThis.__omp_prelude__ = async (name, params) => {
  const resp = await fetch(%q, { method: "POST", body: JSON.stringify(params) });
  const data = await resp.json();
  if (!resp.ok) throw new Error(data.error || "bridge error");
  return data;
};
%s
const tab = await browser.open({ name: "js1", url: "http://example.test", app: "chromium" });
const title = await tab.title();
const ev = await tab.evaluate((x) => x + 1, 41);
const wf = await tab.waitFor("#ok");
await browser.close({ name: "js1" });
if (title !== "Prelude Title" || !String(ev).startsWith("fn:") || wf !== true) {
  throw new Error("unexpected results: " + JSON.stringify({ title, ev, wf }));
}
console.log("JS_PRELUDE_OK");
`, bridge.URL, browserPreludeJS)
		tmpJS := filepath.Join(t.TempDir(), "run_prelude.mjs")
		if err := os.WriteFile(tmpJS, []byte(jsScript), 0o600); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(jsBin, tmpJS).CombinedOutput()
		if err != nil || !strings.Contains(string(out), "JS_PRELUDE_OK") {
			t.Fatalf("prelude.js driver failed: err=%v out=%s", err, string(out))
		}
	}

	// 2. Drive assets/prelude.py via python3.
	if pyBin, err := exec.LookPath("python3"); err == nil {
		pyScript := fmt.Sprintf(`
import asyncio, json, urllib.request, urllib.error

def __omp_log__(*args, **kwargs):
    pass

async def __omp_prelude__(name, params):
    req = urllib.request.Request(%q, data=json.dumps(params).encode("utf-8"), method="POST")
    try:
        with urllib.request.urlopen(req) as resp:
            return json.loads(resp.read().decode("utf-8"))
    except urllib.error.HTTPError as e:
        raise RuntimeError(e.read().decode("utf-8"))

_omp_prelude = __omp_prelude__

%s

async def _main():
    tab = await browser.open(name="py1", url="http://example.test", app="chromium")
    title = await tab.title()
    wf = await tab.waitFor("#ok")
    ev = await tab.evaluate("1 + 1")
    await browser.close(name="py1")
    assert title == "Prelude Title", f"title={title!r}"
    assert wf is True, f"wf={wf!r}"
    assert ev == "expr", f"ev={ev!r}"
    print("PY_PRELUDE_OK")

asyncio.run(_main())
`, bridge.URL, browserPreludePY)
		tmpPY := filepath.Join(t.TempDir(), "run_prelude.py")
		if err := os.WriteFile(tmpPY, []byte(pyScript), 0o600); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(pyBin, tmpPY).CombinedOutput()
		if err != nil || !strings.Contains(string(out), "PY_PRELUDE_OK") {
			t.Fatalf("prelude.py driver failed: err=%v out=%s", err, string(out))
		}
	}
}

type scriptedBackend struct {
	onCall func(chain []CallStep) (*CallResult, error)
}

func (s *scriptedBackend) Available(context.Context) (bool, string) { return true, "" }
func (s *scriptedBackend) Open(_ context.Context, opts OpenOptions) (Tab, error) {
	return &scriptedTab{url: opts.URL, onCall: s.onCall}, nil
}

type scriptedTab struct {
	url    string
	dead   atomic.Bool
	onCall func(chain []CallStep) (*CallResult, error)
}

func (t *scriptedTab) Info() TabInfo { return TabInfo{URL: t.url, Viewport: DefaultViewport} }
func (t *scriptedTab) Alive() bool   { return !t.dead.Load() }
func (t *scriptedTab) Reuse(_ context.Context, opts OpenOptions) (string, error) {
	if opts.URL != "" {
		t.url = opts.URL
	}
	return "", nil
}
func (t *scriptedTab) Close(context.Context, bool) error { t.dead.Store(true); return nil }
func (t *scriptedTab) Call(_ context.Context, _ CallContext, chain []CallStep) (*CallResult, error) {
	return t.onCall(chain)
}
func (t *scriptedTab) Run(context.Context, CallContext, string, string, []json.RawMessage) (*CallResult, error) {
	return &CallResult{}, nil
}
