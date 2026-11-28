package browser

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func TestBrowserSlashCommand(t *testing.T) {
	origSetting := Setting
	origSetSetting := SetSetting
	origApply := applyBrowserModeChangeFn
	t.Cleanup(func() {
		Setting = origSetting
		SetSetting = origSetSetting
		applyBrowserModeChangeFn = origApply
	})

	enabled := false
	headless := true
	Setting = func(_ sdk.Context, key string) any {
		switch key {
		case "browser.enabled":
			return enabled
		case "browser.headless":
			return headless
		}
		return nil
	}
	var lastKey string
	var lastVal any
	SetSetting = func(_ sdk.Context, key string, value any) error {
		lastKey = key
		lastVal = value
		if b, ok := value.(bool); ok && key == "browser.headless" {
			headless = b
		}
		return nil
	}
	var applied []bool
	var applyErr error
	applyBrowserModeChangeFn = func(_ sdk.Context, next bool) error {
		applied = append(applied, next)
		return applyErr
	}

	ext := sdk.New("test-browser-cmd")
	registerBrowserCommand(ext)
	var liveCtx sdk.Context
	var mu sync.Mutex
	ext.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		mu.Lock()
		liveCtx = ctx
		mu.Unlock()
		return nil, nil
	})
	h := startTestFakeHost(t, ext, "sess-browser-cmd")
	h.fire("session_start", map[string]any{})

	mu.Lock()
	ctx := liveCtx
	mu.Unlock()

	// 1. Disabled browser does not mutate setting or apply.
	if err := handleBrowserCommand(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if lastKey != "" || len(applied) != 0 {
		t.Fatalf("disabled browser should not mutate setting or apply: key=%q applied=%v", lastKey, applied)
	}

	// 2. Toggle from headless=true -> visible.
	enabled = true
	if err := handleBrowserCommand(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if lastKey != "browser.headless" || lastVal != false {
		t.Fatalf("expected browser.headless=false, got %s=%v", lastKey, lastVal)
	}
	if len(applied) != 1 || applied[0] != false {
		t.Fatalf("expected apply(false), got %v", applied)
	}

	// 3. Explicit headless.
	if err := handleBrowserCommand(ctx, "headless"); err != nil {
		t.Fatal(err)
	}
	if !headless || len(applied) != 2 || applied[1] != true {
		t.Fatalf("expected headless=true, got headless=%v applied=%v", headless, applied)
	}

	// 4. Invalid argument -> does not mutate.
	if err := handleBrowserCommand(ctx, "bogus"); err != nil {
		t.Fatal(err)
	}
	if len(applied) != 2 {
		t.Fatalf("invalid arg should not apply, got %v", applied)
	}

	// 5. Apply error surfaces cleanly without returning error.
	applyErr = errors.New("cdp down")
	if err := handleBrowserCommand(ctx, "visible"); err != nil {
		t.Fatal(err)
	}
	if headless || len(applied) != 3 {
		t.Fatalf("expected visible applied even on error, got headless=%v applied=%v", headless, applied)
	}
}

func TestDefaultApplyBrowserModeChangeDropsOnlyHeadlessAndDoesNotTombstone(t *testing.T) {
	origBackends := RegisteredBackends()
	t.Cleanup(func() {
		defaultManager.ReleaseAllTabs(context.Background(), false)
		for k := range RegisteredBackends() {
			RegisterBackend(k, nil)
		}
		for k, b := range origBackends {
			RegisterBackend(k, b)
		}
	})

	var headlessTab *fakeTab
	var connectedTab *fakeTab
	var reopenCount int

	RegisterBackend(string(KindHeadless), &fakeBackend{
		availOK: true,
		openFn: func(opts OpenOptions) (Tab, error) {
			reopenCount++
			ft := &fakeTab{
				info: TabInfo{
					URL:         opts.URL,
					Title:       "Headless",
					TargetID:    "hl-1",
					Viewport:    DefaultViewport,
					Description: "omp's headless Chromium",
				},
				alive: true,
			}
			headlessTab = ft
			return ft, nil
		},
	})
	RegisterBackend(string(KindConnected), &fakeBackend{
		availOK: true,
		openFn: func(opts OpenOptions) (Tab, error) {
			ft := &fakeTab{
				info: TabInfo{
					URL:         opts.URL,
					Title:       "Connected",
					TargetID:    "conn-1",
					Viewport:    DefaultViewport,
					Description: "connected Chrome",
				},
				alive: true,
			}
			connectedTab = ft
			return ft, nil
		},
	})

	const sid = "sess-drop-headless-only"
	defaultManager.StartSession(sid)

	ext := sdk.New("test-browser-mode-apply")
	var liveCtx sdk.Context
	var mu sync.Mutex
	ext.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		mu.Lock()
		liveCtx = ctx
		mu.Unlock()
		return nil, nil
	})
	h := startTestFakeHost(t, ext, sid)
	h.fire("session_start", map[string]any{})
	mu.Lock()
	sdkCtx := liveCtx
	mu.Unlock()

	bg := context.Background()
	sHeadless := DefaultSettings()
	sHeadless.Enabled = true
	sHeadless.Headless = true

	// Open 1 headless tab (app: "chromium") and 1 connected tab (cdpUrl set).
	if _, err := defaultManager.Invoke(bg, InvokeRequest{OwnerSessionID: sid, Settings: &sHeadless}, json.RawMessage(`{"action":"open","name":"hl","app":"chromium","url":"https://example.com"}`)); err != nil {
		t.Fatalf("open headless tab failed: %v", err)
	}
	sConn := DefaultSettings()
	sConn.Enabled = true
	sConn.CdpURL = "http://127.0.0.1:9222"
	if _, err := defaultManager.Invoke(bg, InvokeRequest{OwnerSessionID: sid, Settings: &sConn}, json.RawMessage(`{"action":"open","name":"conn","url":"https://example.com"}`)); err != nil {
		t.Fatalf("open connected tab failed: %v", err)
	}
	if headlessTab == nil || connectedTab == nil {
		t.Fatalf("expected both tabs created: hl=%v conn=%v", headlessTab, connectedTab)
	}

	// Call the real defaultApplyBrowserModeChange (NOT stubbed!).
	if err := defaultApplyBrowserModeChange(sdkCtx, false); err != nil {
		t.Fatalf("defaultApplyBrowserModeChange failed: %v", err)
	}

	// Verify headless tab closed, connected tab untouched.
	headlessTab.mu.Lock()
	hlClosed := headlessTab.closed
	headlessTab.mu.Unlock()
	if !hlClosed {
		t.Fatalf("expected headless tab to be closed by defaultApplyBrowserModeChange")
	}

	connectedTab.mu.Lock()
	connClosed := connectedTab.closed
	connectedTab.mu.Unlock()
	if connClosed {
		t.Fatalf("connected tab must NOT be closed by defaultApplyBrowserModeChange")
	}

	// Verify session is NOT tombstoned and a subsequent browser.open succeeds!
	sVisible := DefaultSettings()
	sVisible.Enabled = true
	sVisible.Headless = false
	if _, err := defaultManager.Invoke(bg, InvokeRequest{OwnerSessionID: sid, Settings: &sVisible}, json.RawMessage(`{"action":"open","name":"hl2","app":"chromium","url":"https://example.org"}`)); err != nil {
		t.Fatalf("subsequent browser.open after mode switch failed (session tombstoned?): %v", err)
	}
	if reopenCount != 2 {
		t.Fatalf("expected 2 headless opens total, got %d", reopenCount)
	}
}
