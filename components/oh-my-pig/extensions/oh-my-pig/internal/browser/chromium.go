package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ChromiumBackend implements Backend and TargetLister for KindHeadless,
// KindConnected, and KindSpawned over Chrome DevTools Protocol (CDP).
type ChromiumBackend struct {
	mu           sync.Mutex
	browsers     map[string]*chromiumProcess
	acquireLocks map[string]*sync.Mutex
}

type chromiumProcess struct {
	key      string
	kind     Kind
	headless bool
	exePath  string
	client   *CDPClient
	launched *launchedBrowser
	cdpURL   string
	refCount int
}

// NewChromiumBackend returns a new Chromium/CDP backend.
func NewChromiumBackend() *ChromiumBackend {
	return &ChromiumBackend{
		browsers:     make(map[string]*chromiumProcess),
		acquireLocks: make(map[string]*sync.Mutex),
	}
}

func (b *ChromiumBackend) lockAcquireKey(key string) func() {
	b.mu.Lock()
	lk, ok := b.acquireLocks[key]
	if !ok {
		lk = &sync.Mutex{}
		b.acquireLocks[key] = lk
	}
	b.mu.Unlock()
	lk.Lock()
	return lk.Unlock
}

// Available probes whether Chromium can be launched or a connected endpoint is valid.
func (b *ChromiumBackend) Available(_ context.Context) (bool, string) {
	// Availability for KindConnected/KindSpawned depends on OpenOptions; for
	// KindHeadless, Open checks FindChromiumExecutable and returns an actionable
	// error rather than triggering a fallback to a nonexistent backend.
	return true, ""
}

// ListTargets lists adoptable pages from a connected CDP endpoint.
func (b *ChromiumBackend) ListTargets(ctx context.Context, opts OpenOptions, _ map[string]string) (TargetList, error) {
	cdpURL, err := NormalizeConnectedCdpURL(opts.Endpoint)
	if err != nil {
		return TargetList{}, err
	}
	targets, text, err := ListCDPTargets(ctx, cdpURL)
	if err != nil {
		return TargetList{}, err
	}
	return TargetList{Targets: targets, Text: text}, nil
}

// Open launches or connects to a Chromium browser and opens (or adopts) a tab.
func (b *ChromiumBackend) Open(ctx context.Context, opts OpenOptions) (Tab, error) {
	proc, err := b.acquireProcess(ctx, opts)
	if err != nil {
		return nil, err
	}

	tab, err := openCDPTab(ctx, b, proc, opts)
	if err != nil {
		// NavigationTimeoutError keeps the newly created tab open on what loaded
		// (upstream tab-supervisor.ts:880-901); only release the process lease
		// when no live tab is returned.
		if tab == nil {
			b.releaseProcess(proc, proc.kind == KindSpawned)
		}
		return tab, err
	}
	return tab, nil
}

func (b *ChromiumBackend) acquireProcess(ctx context.Context, opts OpenOptions) (*chromiumProcess, error) {
	var (
		key  string
		kind Kind
	)
	switch {
	case opts.Endpoint != "":
		cdpURL, err := NormalizeConnectedCdpURL(opts.Endpoint)
		if err != nil {
			return nil, err
		}
		key = "connected:" + cdpURL
		kind = KindConnected
		opts.Endpoint = cdpURL
	case opts.Executable != "":
		key = fmt.Sprintf("spawned:%q:%q", opts.Executable, opts.Argv)
		kind = KindSpawned
	default:
		h := "0"
		if opts.Headless {
			h = "1"
		}
		tls := ""
		if opts.Insecure {
			tls = "tls"
		}
		key = "headless:" + h + ":" + tls
		kind = KindHeadless
	}

	unlock := b.lockAcquireKey(key)
	defer unlock()

	b.mu.Lock()
	if existing, ok := b.browsers[key]; ok {
		if existing.client.Alive() {
			existing.refCount++
			b.mu.Unlock()
			return existing, nil
		}
		delete(b.browsers, key)
		go func(old *chromiumProcess) {
			_ = old.client.Close()
			old.launched.stop()
		}(existing)
	}
	b.mu.Unlock()
	var (
		client   *CDPClient
		launched *launchedBrowser
		cdpURL   string
		err      error
	)

	switch kind {
	case KindConnected:
		cdpURL = opts.Endpoint
		wsURL, err := WaitForCDP(ctx, cdpURL)
		if err != nil {
			return nil, err
		}
		client, err = DialCDP(ctx, wsURL)
		if err != nil {
			return nil, err
		}
	case KindSpawned:
		if !filepath.IsAbs(opts.Executable) {
			return nil, fmt.Errorf("app must be an absolute executable path (got %q)", opts.Executable)
		}
		port, err := FindFreeTCPPort()
		if err != nil {
			return nil, err
		}
		args := ResolveSpawnArgs(opts.Executable, opts.Argv, opts.Cwd)
		args = append(args, fmt.Sprintf("--remote-debugging-port=%d", port))
		launched, err = launchSpawnedProcess(ctx, opts.Executable, args, port)
		if err != nil {
			return nil, err
		}
		cdpURL = launched.cdpURL
		client, err = DialCDP(ctx, launched.wsURL)
		if err != nil {
			launched.stop()
			return nil, err
		}
	default: // KindHeadless
		exe, err := FindChromiumExecutable(os.LookupEnv)
		if err != nil {
			return nil, err
		}
		vp := DefaultViewport
		if opts.Viewport != nil {
			vp = *opts.Viewport
		}
		launched, err = launchHeadlessChromiumProcess(ctx, exe, vp, opts.Headless, opts.Insecure)
		if err != nil {
			return nil, err
		}
		cdpURL = launched.cdpURL
		client, err = DialCDP(ctx, launched.wsURL)
		if err != nil {
			launched.stop()
			return nil, err
		}
		if opts.Headless {
			// Wait for Chromium's GPU/Viz service to finish initializing before any
			// tab target is created in headless mode. Creating a target before Viz is
			// ready races under load (and on locked macOS displays) and leaves that
			// tab's compositor without a frame sink, stalling Page.captureScreenshot
			// and requestAnimationFrame indefinitely. SystemInfo.getInfo blocks in
			// Chrome until GpuDataManager finishes GPU startup (~150-900ms, once per
			// launched headless browser); capped to min(5s, remaining/2) (floor 500ms)
			// so short open budgets still keep time for tab creation and navigation.
			gpuWait := 5 * time.Second
			if dl, ok := ctx.Deadline(); ok {
				if half := time.Until(dl) / 2; half < gpuWait {
					gpuWait = max(half, 500*time.Millisecond)
				}
			}
			gpuCtx, gpuCancel := context.WithTimeout(ctx, gpuWait)
			_ = client.Send(gpuCtx, "", "SystemInfo.getInfo", nil, nil)
			gpuCancel()
		}
	}

	proc := &chromiumProcess{
		key:      key,
		kind:     kind,
		headless: opts.Headless,
		exePath:  opts.Executable,
		client:   client,
		launched: launched,
		cdpURL:   cdpURL,
		refCount: 1,
	}
	b.mu.Lock()
	b.browsers[key] = proc
	b.mu.Unlock()
	// If an owned headless Chromium process exits unexpectedly (e.g. SIGKILL
	// or browser crash), reap its temp profile directory promptly instead of
	// waiting for a future stale-profile sweep.
	if launched != nil && launched.owned && launched.userDataDir != "" && launched.done != nil {
		go func(p *chromiumProcess, lb *launchedBrowser) {
			<-lb.done
			b.mu.Lock()
			if b.browsers[p.key] == p {
				delete(b.browsers, p.key)
			}
			b.mu.Unlock()
			_ = p.client.Close()
			lb.stop()
		}(proc, launched)
	}
	_ = err
	return proc, nil
}

func (b *ChromiumBackend) releaseProcess(proc *chromiumProcess, kill bool) {
	b.mu.Lock()
	proc.refCount--
	shouldDispose := proc.refCount <= 0
	if shouldDispose && b.browsers[proc.key] == proc {
		delete(b.browsers, proc.key)
	}
	b.mu.Unlock()

	if shouldDispose {
		_ = proc.client.Close()
		if proc.kind == KindHeadless || (proc.kind == KindSpawned && kill) {
			proc.launched.stop()
		} else if proc.launched != nil && proc.launched.userDataDir != "" && proc.kind == KindSpawned {
			// Still stop spawned process if we launched it and refCount hit 0 with no tabs left.
			if kill {
				proc.launched.stop()
			}
		}
	}
}

// --- CDP Tab implementation ---

type cdpDialogState struct {
	Open         bool   `json:"open"`
	Type         string `json:"type,omitempty"`
	Message      string `json:"message,omitempty"`
	DefaultValue string `json:"defaultValue,omitempty"`
}

type cdpConsoleEntry struct {
	Seq      int64  `json:"seq"`
	TS       int64  `json:"ts"`
	Type     string `json:"type"`
	Level    string `json:"level"`
	Text     string `json:"text"`
	Location string `json:"location,omitempty"`
	Args     []any  `json:"args"`
}

type cdpErrorEntry struct {
	Seq      int64  `json:"seq"`
	TS       int64  `json:"ts"`
	Type     string `json:"type"`
	Level    string `json:"level"`
	Text     string `json:"text"`
	Location string `json:"location,omitempty"`
	Stack    string `json:"stack,omitempty"`
}

type cdpRequestRecord struct {
	ID              string            `json:"id"`
	Seq             int64             `json:"seq"`
	TS              int64             `json:"ts"`
	Method          string            `json:"method"`
	URL             string            `json:"url"`
	ResourceType    string            `json:"resourceType"`
	Status          int               `json:"status,omitempty"`
	OK              *bool             `json:"ok,omitempty"`
	FailureText     string            `json:"failureText,omitempty"`
	DurationMs      int64             `json:"durationMs,omitempty"`
	RequestHeaders  map[string]string `json:"requestHeaders"`
	ResponseHeaders map[string]string `json:"responseHeaders,omitempty"`
	Sizes           map[string]int    `json:"sizes"`
	PostData        string            `json:"-"`
}

type cdpDownload struct {
	Path              string `json:"path"`
	SuggestedFilename string `json:"suggestedFilename"`
	URL               string `json:"url"`
	Bytes             int64  `json:"bytes"`
}

type cdpPendingDownload struct {
	guid              string
	url               string
	suggestedFilename string
	receivedBytes     int64
}

type cdpInitScript struct {
	ID     string `json:"id"`
	Source string `json:"source"`
}

type observedElement struct {
	frameID       string
	backendNodeID int
	docGen        uint64
}

// RendererCrashedError signals that Chromium's renderer process for a tab
// crashed (`Inspector.targetCrashed`). Matches omp's RendererCrashedError
// (tab-worker.ts:1308, 2038-2052).
type RendererCrashedError struct {
	Message string
}

func (e *RendererCrashedError) Error() string {
	if e == nil || e.Message == "" {
		return "Browser tab's renderer crashed during this run"
	}
	return e.Message
}

// BrowserDisconnectedError signals that the underlying Chromium browser process
// or CDP WebSocket died unexpectedly (distinct from a per-tab timeout or selector miss).
type BrowserDisconnectedError struct {
	Cause error
}

func (e *BrowserDisconnectedError) Error() string {
	if e == nil || e.Cause == nil {
		return "Browser process exited or lost its CDP connection"
	}
	return fmt.Sprintf("Browser process exited or lost its CDP connection: %v", e.Cause)
}

func (e *BrowserDisconnectedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

const kitWorldName = "omp-browser-kit"

type cdpTab struct {
	backend    *ChromiumBackend
	proc       *chromiumProcess
	name       string
	targetID   string
	sessionID  string
	ownsPage   bool
	adopted    bool
	unsub      func()
	unsubTrace func()
	trace      *cdpTraceRing
	closed     atomic.Bool
	crashed    atomic.Bool
	started    atomic.Bool
	tabCtx     context.Context
	tabCancel  context.CancelFunc
	crashCh    chan struct{} // closed once when Inspector.targetCrashed arrives
	// allowDefaultWorldFallbackForTest is set ONLY in unit tests against minimal
	// fake CDP servers that do not implement Page.createIsolatedWorld. Production
	// code NEVER installs pageKitJS in the page's main world.
	allowDefaultWorldFallbackForTest bool

	mu                           sync.Mutex
	mainFrameID                  string
	mainFrameNavigationRequested bool
	beforeUnloadOpen             bool
	docGen                       uint64
	url                          string
	title                        string
	viewport                     Viewport
	initialViewport              Viewport
	emulateViewport              bool
	initialUserAgent             string
	insecure                     bool
	pointer                      PointerSpace
	dialogPolicy                 string // "", "accept", "dismiss"
	pendingDialog                *cdpDialogState
	dialogWaiters                map[chan cdpDialogState]struct{}
	allowedDomains               []string
	fetchSessions                map[string]bool   // page + auto-attached child sessions guarded by Fetch.enable
	childSessions                map[string]bool   // auto-attached child sessions (OOPIFs / workers) owned by this tab
	childParent                  map[string]string // child sessionID -> parent sessionID (for subtree cleanup)
	openInitScripts              []string
	initScripts                  []cdpInitScript
	downloadsDir                 string
	downloadsConfigured          bool
	ownedDownloadsDir            bool
	createdDLDir                 bool
	pendingDLs                   map[string]*cdpPendingDownload
	completedDLs                 []cdpDownload
	dlNotify                     chan cdpDownload
	consoleSeq                   int64
	consoleDropped               int
	consoleEntries               []cdpConsoleEntry
	errorEntries                 []cdpErrorEntry
	reqSeq                       int64
	requests                     []*cdpRequestRecord
	reqByID                      map[string]*cdpRequestRecord
	inflightReqs                 map[string]time.Time
	lastReqChange                time.Time
	loadEvents                   chan string
	mouseX                       float64
	mouseY                       float64
	heldMods                     int
	frameContexts                map[string]int64        // frameId -> default world executionContextId
	isolatedWorlds               map[string]int64        // frameId -> isolated world executionContextId
	kitInstalled                 map[int64]bool          // executionContextId -> pageKitJS injected
	ctxDestroyedWaiters          map[int64]chan struct{} // executionContextId -> closed when context destroyed/cleared
	observed                     map[int]observedElement
	ariaBaselines                map[string]*ariaSnapshotBaseline
	emulateState                 map[string]any
	baseUserAgent                string
}

// closePageOnly detaches/closes the CDP target and event subscription WITHOUT
// calling backend.releaseProcess (used during openCDPTab rollback so Open
// releases the process lease exactly once).
func (t *cdpTab) closePageOnly() {
	if t.closed.Swap(true) {
		return
	}
	if t.tabCancel != nil {
		t.tabCancel()
	}
	if t.unsub != nil {
		t.unsub()
	}
	if t.unsubTrace != nil {
		t.unsubTrace()
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if t.ownsPage && t.proc.client.Alive() {
		_ = t.proc.client.Send(closeCtx, "", "Target.closeTarget", map[string]any{"targetId": t.targetID}, nil)
	} else if t.proc.client.Alive() {
		_ = t.proc.client.Send(closeCtx, "", "Target.detachFromTarget", map[string]any{"sessionId": t.sessionID}, nil)
	}
	t.cleanupEmptyOwnedDownloadsDir()
}

func (t *cdpTab) cleanupEmptyOwnedDownloadsDir() {
	t.mu.Lock()
	shouldClean := t.ownedDownloadsDir && t.createdDLDir && len(t.completedDLs) == 0 && t.downloadsDir != ""
	dir := t.downloadsDir
	t.mu.Unlock()
	if shouldClean {
		_ = os.RemoveAll(dir)
	}
}

func openCDPTab(ctx context.Context, backend *ChromiumBackend, proc *chromiumProcess, opts OpenOptions) (*cdpTab, error) {
	var (
		targetID string
		ownsPage bool
		adopted  bool
	)

	adoptExisting := (opts.Target != "" && (proc.kind == KindConnected || proc.kind == KindSpawned)) ||
		(opts.Target == "" && proc.kind == KindSpawned && !IsChromiumBrowserExecutable(proc.exePath))
	if adoptExisting {
		var listResp struct {
			TargetInfos []struct {
				TargetID string `json:"targetId"`
				Type     string `json:"type"`
				Title    string `json:"title"`
				URL      string `json:"url"`
			} `json:"targetInfos"`
		}
		if err := proc.client.Send(ctx, "", "Target.getTargets", nil, &listResp); err != nil {
			return nil, fmt.Errorf("Target.getTargets failed: %w", err)
		}
		type pageEntry struct {
			id, title, url string
		}
		var pages []pageEntry
		for _, ti := range listResp.TargetInfos {
			if ti.Type == "page" {
				pages = append(pages, pageEntry{id: ti.TargetID, title: ti.Title, url: ti.URL})
			}
		}
		if opts.Target != "" {
			needle := strings.ToLower(strings.TrimSpace(opts.Target))
			for _, p := range pages {
				if p.id == opts.Target {
					targetID = p.id
					adopted = true
					break
				}
			}
			if targetID == "" {
				for _, p := range pages {
					if strings.Contains(strings.ToLower(p.url), needle) ||
						strings.Contains(strings.ToLower(p.title), needle) {
						targetID = p.id
						adopted = true
						break
					}
				}
			}
			if targetID == "" {
				var summary []string
				for _, p := range pages {
					ti := p.title
					if ti == "" {
						ti = "(untitled)"
					}
					summary = append(summary, fmt.Sprintf("- %s  %s  %s", p.id, ti, p.url))
				}
				return nil, fmt.Errorf("No page target matched %q. Available pages:\n%s", opts.Target, strings.Join(summary, "\n"))
			}
		} else if len(pages) > 0 {
			// Spawned non-Chromium app (e.g. Electron) adopts its first usable window.
			// upstream: pickPageFromList in attach.ts:813-816, tab-supervisor.ts:2466-2477
			for _, p := range pages {
				lu := strings.ToLower(p.url)
				if !strings.HasPrefix(lu, "devtools://") && !strings.HasPrefix(lu, "chrome-extension://") {
					targetID = p.id
					adopted = true
					break
				}
			}
			if targetID == "" {
				targetID = pages[0].id
				adopted = true
			}
		}
	}
	if targetID == "" {
		var createResp struct {
			TargetID string `json:"targetId"`
		}
		if err := proc.client.Send(ctx, "", "Target.createTarget", map[string]any{
			"url": "about:blank",
		}, &createResp); err != nil {
			return nil, fmt.Errorf("Target.createTarget failed: %w", err)
		}
		targetID = createResp.TargetID
		ownsPage = true
	}

	var attachResp struct {
		SessionID string `json:"sessionId"`
	}
	if err := proc.client.Send(ctx, "", "Target.attachToTarget", map[string]any{
		"targetId": targetID,
		"flatten":  true,
	}, &attachResp); err != nil {
		if ownsPage {
			_ = proc.client.Send(ctx, "", "Target.closeTarget", map[string]any{"targetId": targetID}, nil)
		}
		return nil, fmt.Errorf("Target.attachToTarget failed: %w", err)
	}

	vp := DefaultViewport
	if opts.Viewport != nil {
		vp = *opts.Viewport
	}
	if vp.DeviceScaleFactor == 0 {
		vp.DeviceScaleFactor = 1
	}

	dlDir := opts.DownloadsPath
	ownedDLDir := false
	if dlDir == "" {
		dlDir = filepath.Join(os.TempDir(), fmt.Sprintf("omp-downloads-%s-%d", sanitizeName(opts.Name), time.Now().UnixNano()))
		ownedDLDir = true
	}

	tabCtx, tabCancel := context.WithCancel(context.Background())
	t := &cdpTab{
		backend:             backend,
		proc:                proc,
		name:                opts.Name,
		targetID:            targetID,
		sessionID:           attachResp.SessionID,
		ownsPage:            ownsPage,
		adopted:             adopted,
		tabCtx:              tabCtx,
		tabCancel:           tabCancel,
		crashCh:             make(chan struct{}),
		trace:               &cdpTraceRing{},
		url:                 "about:blank",
		viewport:            vp,
		initialViewport:     vp,
		emulateViewport:     opts.Headless || opts.Viewport != nil,
		initialUserAgent:    opts.UserAgent,
		insecure:            opts.Insecure,
		dialogPolicy:        opts.Dialogs,
		dialogWaiters:       make(map[chan cdpDialogState]struct{}),
		allowedDomains:      append([]string(nil), opts.AllowedDomains...),
		fetchSessions:       make(map[string]bool),
		childSessions:       make(map[string]bool),
		childParent:         make(map[string]string),
		openInitScripts:     append([]string(nil), opts.InitScripts...),
		downloadsDir:        dlDir,
		downloadsConfigured: opts.DownloadsPath != "",
		ownedDownloadsDir:   ownedDLDir,
		pendingDLs:          make(map[string]*cdpPendingDownload),
		reqByID:             make(map[string]*cdpRequestRecord),
		inflightReqs:        make(map[string]time.Time),
		lastReqChange:       time.Now(),
		loadEvents:          make(chan string, 32),
		frameContexts:       make(map[string]int64),
		isolatedWorlds:      make(map[string]int64),
		kitInstalled:        make(map[int64]bool),
		ctxDestroyedWaiters: make(map[int64]chan struct{}),
		observed:            make(map[int]observedElement),
		ariaBaselines:       make(map[string]*ariaSnapshotBaseline),
		emulateState:        make(map[string]any),
	}
	t.unsubTrace = proc.client.AttachTrace(t.sessionID, t.trace)
	t.unsub = proc.client.Subscribe(t.onCDPEvent)
	if err := t.configureSession(ctx); err != nil {
		t.closePageOnly()
		return nil, err
	}
	if opts.URL != "" {
		gotoMs := openNavigationBudgetMs(ctx, opts.TimeoutMs)
		_, err := t.runWithDialogRace(ctx, fmt.Sprintf("tab.goto(%s)", jsLit(opts.URL)), false, func(runCtx context.Context) (*CallResult, error) {
			navCtx, navCancel := context.WithTimeout(runCtx, time.Duration(gotoMs)*time.Millisecond)
			defer navCancel()
			return &CallResult{}, t.navigate(navCtx, opts.URL, opts.WaitUntil, gotoMs)
		})
		if err != nil {
			var navTimeout *NavigationTimeoutError
			if errors.As(err, &navTimeout) {
				// Keep the newly created tab open on what loaded (upstream: tab-supervisor.ts:880-901),
				// but bound the best-effort meta read so a blocked page can never hang open().
				t.mu.Lock()
				hasDlg := t.pendingDialog != nil && t.pendingDialog.Open
				t.mu.Unlock()
				if !hasDlg {
					metaCtx, metaCancel := context.WithTimeout(t.tabCtx, 400*time.Millisecond)
					_ = t.refreshMeta(metaCtx)
					metaCancel()
				}
				return t, err
			}
			t.closePageOnly()
			return nil, err
		}
	} else {
		_ = t.refreshMeta(ctx)
	}
	t.started.Store(true)

	return t, nil
}

func (t *cdpTab) rendererCrashedError() error {
	if !t.started.Load() {
		return &RendererCrashedError{Message: "Browser tab's renderer crashed while the tab was starting"}
	}
	return &RendererCrashedError{Message: "Browser tab's renderer crashed during this run"}
}

func (t *cdpTab) markCrashed() {
	if t.crashed.Swap(true) {
		return
	}
	close(t.crashCh)
}

func (t *cdpTab) send(ctx context.Context, method string, params any, out any) error {
	if t.closed.Load() {
		return fmt.Errorf("Tab %q was closed", t.name)
	}
	if t.crashed.Load() {
		return t.rendererCrashedError()
	}
	if !t.proc.client.Alive() {
		return &BrowserDisconnectedError{}
	}
	t.mu.Lock()
	sid := t.sessionID
	t.mu.Unlock()
	sendCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if t.tabCtx != nil {
		stop := context.AfterFunc(t.tabCtx, cancel)
		defer stop()
	}
	stopCrash := make(chan struct{})
	defer close(stopCrash)
	go func() {
		select {
		case <-t.crashCh:
			cancel()
		case <-stopCrash:
		}
	}()
	err := t.proc.client.Send(sendCtx, sid, method, params, out)
	if err != nil {
		if t.crashed.Load() {
			return t.rendererCrashedError()
		}
		if t.tabCtx != nil && t.tabCtx.Err() != nil && ctx.Err() == nil {
			return fmt.Errorf("Tab %q was closed", t.name)
		}
		if !t.proc.client.Alive() {
			return &BrowserDisconnectedError{Cause: err}
		}
	}
	return err
}

func (t *cdpTab) sendAsync(ctx context.Context, method string, params any) (func() error, error) {
	if t.closed.Load() {
		return nil, fmt.Errorf("Tab %q was closed", t.name)
	}
	if t.crashed.Load() {
		return nil, t.rendererCrashedError()
	}
	if !t.proc.client.Alive() {
		return nil, &BrowserDisconnectedError{}
	}
	t.mu.Lock()
	sid := t.sessionID
	t.mu.Unlock()
	sendCtx, cancel := context.WithCancel(ctx)
	var stopTab func() bool
	if t.tabCtx != nil {
		stopTab = context.AfterFunc(t.tabCtx, cancel)
	}
	stopCrash := make(chan struct{})
	go func() {
		select {
		case <-t.crashCh:
			cancel()
		case <-stopCrash:
		}
	}()
	waitRaw, err := t.proc.client.SendAsync(sendCtx, sid, method, params, nil)
	if err != nil {
		close(stopCrash)
		if stopTab != nil {
			stopTab()
		}
		cancel()
		return nil, err
	}
	return func() error {
		defer func() {
			close(stopCrash)
			if stopTab != nil {
				stopTab()
			}
			cancel()
		}()
		err := waitRaw()
		if err != nil {
			if t.crashed.Load() {
				return t.rendererCrashedError()
			}
			if t.tabCtx != nil && t.tabCtx.Err() != nil && ctx.Err() == nil {
				return fmt.Errorf("Tab %q was closed", t.name)
			}
			if !t.proc.client.Alive() {
				return &BrowserDisconnectedError{Cause: err}
			}
		}
		return err
	}, nil
}

// configureSession initializes every CDP domain and open-time tab setting on
// the tab's current CDP session. Used identically by initial openCDPTab() and
// by reattachSession() so no security or emulation setting (especially
// Fetch.enable + Target.setAutoAttach for allowed_domains) can ever be forgotten
// on reattach.
//
// Fetch.enable and Target.setAutoAttach run FIRST so reattachSession can enable
// interception on the new session BEFORE detaching the old one (zero-leak swap).
// Fails closed if Fetch.enable or child-target auto-attach cannot be installed
// when allowedDomains is set (upstream: network.ts:269, 423; patches/puppeteer-core@25.3.0.patch).
func (t *cdpTab) configureSession(ctx context.Context) error {
	t.mu.Lock()
	sid := t.sessionID
	hasBaseUA := t.baseUserAgent != ""
	emulateVP := t.emulateViewport
	vp := t.initialViewport
	initUA := t.initialUserAgent
	insecure := t.insecure
	dlConfigured := t.downloadsConfigured
	dlDir := t.downloadsDir
	openScripts := append([]string(nil), t.openInitScripts...)
	allowed := append([]string(nil), t.allowedDomains...)
	t.initScripts = nil
	if len(allowed) > 0 {
		if t.fetchSessions == nil {
			t.fetchSessions = make(map[string]bool)
		}
		t.fetchSessions[sid] = true
	}
	t.mu.Unlock()

	if len(allowed) > 0 {
		// Install Fetch interception FIRST and fail closed (upstream: network.ts:269).
		if err := t.send(ctx, "Fetch.enable", map[string]any{"patterns": []map[string]any{{"urlPattern": "*"}}}, nil); err != nil {
			t.mu.Lock()
			delete(t.fetchSessions, sid)
			t.mu.Unlock()
			return fmt.Errorf("failed to enable allowed_domains interception (Fetch.enable): %w", err)
		}
		// Auto-attach child targets (cross-site iframes / OOPIFs and dedicated workers)
		// paused at start so Fetch.enable is installed on every child before it executes
		// any network request (upstream: Puppeteer TargetManager + NetworkManager.addClient).
		if err := t.send(ctx, "Target.setAutoAttach", map[string]any{
			"autoAttach":             true,
			"waitForDebuggerOnStart": true,
			"flatten":                true,
		}, nil); err != nil {
			t.mu.Lock()
			delete(t.fetchSessions, sid)
			t.mu.Unlock()
			return fmt.Errorf("failed to enable allowed_domains child-target interception (Target.setAutoAttach): %w", err)
		}
	}

	for _, method := range []string{"Page.enable", "Runtime.enable", "Network.enable", "DOM.enable"} {
		if err := t.send(ctx, method, nil, nil); err != nil {
			return fmt.Errorf("CDP %s failed: %w", method, err)
		}
	}
	_ = t.send(ctx, "Inspector.enable", nil, nil)
	_ = t.send(ctx, "Accessibility.enable", nil, nil)
	_ = t.send(ctx, "Page.setLifecycleEventsEnabled", map[string]any{"enabled": true}, nil)
	// Keep background tabs interactive (prevents 5s mouseMoved stalls after
	// target=_blank opens a new tab; upstream: tab-worker.ts:2265-2273).
	_ = t.send(ctx, "Emulation.setFocusEmulationEnabled", map[string]any{"enabled": true}, nil)
	// Pre-register the isolated browser-kit world on every new document so Chrome
	// creates the world and injects pageKitJS automatically on commit without us
	// calling Page.createIsolatedWorld during a navigation window.
	_ = t.send(ctx, "Page.addScriptToEvaluateOnNewDocument", map[string]any{
		"source":    pageKitJS,
		"worldName": kitWorldName,
	}, nil)

	// Identify main frame ID so lifecycle events from subframes are ignored,
	// and verify the session answers a page command.
	frames, ferr := t.getFrameList(ctx)
	if ferr == nil && len(frames) > 0 {
		t.mu.Lock()
		t.mainFrameID = frames[0].ID
		t.mu.Unlock()
	}

	if !hasBaseUA {
		if uaVal, uerr := t.evalValue(ctx, "navigator.userAgent", 0); uerr == nil {
			if uaStr, ok := uaVal.(string); ok {
				t.mu.Lock()
				t.baseUserAgent = uaStr
				t.mu.Unlock()
			}
		}
	}

	if emulateVP {
		if err := t.applyViewport(ctx, vp); err != nil {
			return fmt.Errorf("CDP Emulation.setDeviceMetricsOverride failed: %w", err)
		}
	}
	if initUA != "" {
		if err := t.send(ctx, "Network.setUserAgentOverride", map[string]any{"userAgent": initUA}, nil); err != nil {
			return fmt.Errorf("CDP Network.setUserAgentOverride failed: %w", err)
		}
	}
	if insecure {
		if err := t.send(ctx, "Security.setIgnoreCertificateErrors", map[string]any{"ignore": true}, nil); err != nil {
			return fmt.Errorf("CDP Security.setIgnoreCertificateErrors failed: %w", err)
		}
	}
	if dlConfigured {
		_ = os.MkdirAll(dlDir, 0o755)
		_ = t.enableDownloads(ctx, dlDir)
	}
	for _, src := range openScripts {
		if _, err := t.addInitScript(ctx, src); err != nil {
			return fmt.Errorf("CDP init script registration failed: %w", err)
		}
	}
	return nil
}

// reattachSession attaches a fresh CDP session to the same target and enables
// all open-time settings (with Fetch.enable + Target.setAutoAttach FIRST) BEFORE
// detaching the old session, so zero requests can escape allowed_domains during
// the swap while preserving page JS state and unblocking any open dialog / stuck
// load (upstream: tab-supervisor.ts:1664-1682, tab-worker.ts:2426-2445).
// Fails closed if configureSession (including Fetch.enable) or a renderer
// liveness check fails.
func (t *cdpTab) reattachSession(ctx context.Context) bool {
	if t.closed.Load() || t.crashed.Load() || !t.proc.client.Alive() {
		return false
	}
	t.mu.Lock()
	oldSid := t.sessionID
	oldChildren := make([]string, 0, len(t.childSessions))
	for csid := range t.childSessions {
		oldChildren = append(oldChildren, csid)
	}
	t.mu.Unlock()

	// 1. Attach the NEW session BEFORE detaching the old one so allowed_domains
	// interception stays continuously active across the swap.
	var attachResp struct {
		SessionID string `json:"sessionId"`
	}
	attCtx, attCancel := context.WithTimeout(ctx, 2*time.Second)
	err := t.proc.client.Send(attCtx, "", "Target.attachToTarget", map[string]any{
		"targetId": t.targetID,
		"flatten":  true,
	}, &attachResp)
	attCancel()
	if err != nil || attachResp.SessionID == "" {
		return false
	}

	if t.unsubTrace != nil {
		t.unsubTrace()
	}
	// Keep oldSid (and any old child sessions) in t.fetchSessions until AFTER
	// oldSid is detached so paused requests on the old session are still failed
	// or continued cleanly during the overlap window.
	t.mu.Lock()
	t.sessionID = attachResp.SessionID
	t.mainFrameNavigationRequested = false
	t.beforeUnloadOpen = false
	t.pendingDialog = nil
	t.frameContexts = make(map[string]int64)
	t.isolatedWorlds = make(map[string]int64)
	t.kitInstalled = make(map[int64]bool)
	for cid, ch := range t.ctxDestroyedWaiters {
		close(ch)
		delete(t.ctxDestroyedWaiters, cid)
	}
	t.observed = make(map[int]observedElement)
	t.emulateState = make(map[string]any)
	t.pointer = PointerSpace{}
	t.requests = nil
	t.reqByID = make(map[string]*cdpRequestRecord)
	t.inflightReqs = make(map[string]time.Time)
	t.mu.Unlock()
	t.unsubTrace = t.proc.client.AttachTrace(attachResp.SessionID, t.trace)

	detachOld := func() {
		if oldSid != "" {
			detCtx, detCancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
			_ = t.proc.client.Send(detCtx, "", "Target.detachFromTarget", map[string]any{"sessionId": oldSid}, nil)
			detCancel()
		}
		t.mu.Lock()
		delete(t.fetchSessions, oldSid)
		for _, csid := range oldChildren {
			delete(t.fetchSessions, csid)
			delete(t.childSessions, csid)
			delete(t.childParent, csid)
		}
		t.mu.Unlock()
	}

	initCtx, initCancel := context.WithTimeout(ctx, 4*time.Second)
	defer initCancel()
	// Configure the new session FIRST (installing Fetch.enable + Target.setAutoAttach
	// before any other command and while the old session is still attached).
	if err := t.configureSession(initCtx); err != nil {
		// Fail closed: detach the broken new session and keep old cleaned up on release.
		detCtx, detCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		_ = t.proc.client.Send(detCtx, "", "Target.detachFromTarget", map[string]any{"sessionId": attachResp.SessionID}, nil)
		detCancel()
		detachOld()
		return false
	}
	// Best-effort unblock of an open dialog or stuck load after Fetch.enable is live
	// (upstream: #recoverAttachedTarget in tab-worker.ts:2426-2445).
	_ = t.send(initCtx, "Page.handleJavaScriptDialog", map[string]any{"accept": false}, nil)
	_ = t.send(initCtx, "Page.stopLoading", nil, nil)

	// Detach the old session ONLY AFTER the new session's Fetch.enable and domains
	// are fully registered with Chrome's network interceptor.
	detachOld()

	// Verify the page renderer (not just the browser process) answers a short command.
	if _, err := t.evalValue(initCtx, "1", 0); err != nil {
		return false
	}
	return true
}
func (t *cdpTab) applyViewport(ctx context.Context, vp Viewport) error {
	if vp.DeviceScaleFactor == 0 {
		vp.DeviceScaleFactor = 1
	}
	err := t.send(ctx, "Emulation.setDeviceMetricsOverride", map[string]any{
		"width":             vp.Width,
		"height":            vp.Height,
		"deviceScaleFactor": vp.DeviceScaleFactor,
		"mobile":            vp.IsMobile,
	}, nil)
	if err == nil {
		if vp.HasTouch {
			_ = t.send(ctx, "Emulation.setTouchEmulationEnabled", map[string]any{"enabled": true}, nil)
		}
		t.mu.Lock()
		t.viewport = vp
		t.pointer.Resized(vp)
		t.mu.Unlock()
	}
	return err
}

func (t *cdpTab) enableDownloads(ctx context.Context, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err == nil {
		t.mu.Lock()
		if t.ownedDownloadsDir && dir == t.downloadsDir {
			t.createdDLDir = true
		}
		t.mu.Unlock()
	}
	return t.proc.client.Send(ctx, "", "Browser.setDownloadBehavior", map[string]any{
		"behavior":      "allow",
		"downloadPath":  dir,
		"eventsEnabled": true,
	}, nil)
}

func (t *cdpTab) addInitScript(ctx context.Context, source string) (string, error) {
	var resp struct {
		Identifier string `json:"identifier"`
	}
	if err := t.send(ctx, "Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": source}, &resp); err != nil {
		return "", err
	}
	t.mu.Lock()
	t.initScripts = append(t.initScripts, cdpInitScript{ID: resp.Identifier, Source: source})
	t.mu.Unlock()
	return resp.Identifier, nil
}

func (t *cdpTab) Info() TabInfo {
	t.mu.Lock()
	defer t.mu.Unlock()
	desc := "omp's headless Chromium"
	if t.proc.kind == KindHeadless && !t.proc.headless {
		desc = "omp's Chromium (in a window)"
	} else if t.proc.kind == KindConnected {
		desc = "connected " + t.proc.cdpURL
	} else if t.proc.kind == KindSpawned {
		exe := t.proc.exePath
		pidStr := "?"
		if t.proc.launched != nil {
			if exe == "" {
				exe = t.proc.launched.exePath
			}
			if t.proc.launched.pid > 0 {
				pidStr = strconv.Itoa(t.proc.launched.pid)
			}
		}
		desc = fmt.Sprintf("spawned %s (pid %s)", exe, pidStr)
	}
	return TabInfo{
		URL:         t.url,
		Title:       t.title,
		Viewport:    t.viewport,
		TargetID:    t.targetID,
		Description: desc,
		Adopted:     t.adopted,
	}
}

func (t *cdpTab) Alive() bool {
	return !t.closed.Load() && !t.crashed.Load() && t.proc.client.Alive()
}

func (t *cdpTab) Freeze(ctx context.Context) error {
	return t.send(ctx, "Page.setWebLifecycleState", map[string]any{"state": "frozen"}, nil)
}

func (t *cdpTab) Unfreeze(ctx context.Context) error {
	return t.send(ctx, "Page.setWebLifecycleState", map[string]any{"state": "active"}, nil)
}

func (t *cdpTab) Reuse(ctx context.Context, opts OpenOptions) (string, error) {
	if opts.Dialogs != "" {
		t.mu.Lock()
		t.dialogPolicy = opts.Dialogs
		t.mu.Unlock()
	}
	if opts.Viewport != nil {
		if err := t.applyViewport(ctx, *opts.Viewport); err != nil {
			return "", err
		}
	}
	if opts.URL != "" {
		gotoMs := openNavigationBudgetMs(ctx, opts.TimeoutMs)
		_, err := t.runWithDialogRace(ctx, fmt.Sprintf("tab.goto(%s)", jsLit(opts.URL)), false, func(runCtx context.Context) (*CallResult, error) {
			navCtx, navCancel := context.WithTimeout(runCtx, time.Duration(gotoMs)*time.Millisecond)
			defer navCancel()
			return &CallResult{}, t.navigate(navCtx, opts.URL, opts.WaitUntil, gotoMs)
		})
		if err != nil {
			return "", err
		}
	} else {
		_ = t.refreshMeta(ctx)
	}
	return "", nil
}

// openNavigationBudgetMs computes the inner goto deadline for open()/reuse()
// so navigate() hits its own NavigationTimeoutError before the outer open
// deadline fires (upstream: tab-supervisor.ts:313, 862-863).
const openNavigationReportMs = 500

func openNavigationBudgetMs(ctx context.Context, timeoutMs int) int {
	var remainingMs float64
	if dl, ok := ctx.Deadline(); ok {
		remainingMs = float64(time.Until(dl).Milliseconds())
	} else if timeoutMs > 0 {
		remainingMs = float64(timeoutMs)
	} else {
		remainingMs = float64(browserDefaultTimeoutSec * 1000)
	}
	slack := float64(openNavigationReportMs)
	if half := remainingMs / 2; half < slack {
		slack = half
	}
	gotoMs := int(remainingMs - slack)
	if gotoMs < 1 {
		gotoMs = 1
	}
	return gotoMs
}

func (t *cdpTab) Close(ctx context.Context, kill bool) error {
	if t.closed.Swap(true) {
		return nil
	}
	if t.tabCancel != nil {
		t.tabCancel()
	}
	if t.unsub != nil {
		t.unsub()
	}
	if t.unsubTrace != nil {
		t.unsubTrace()
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if t.ownsPage && t.proc.client.Alive() {
		_ = t.proc.client.Send(closeCtx, "", "Target.closeTarget", map[string]any{"targetId": t.targetID}, nil)
	} else if t.proc.client.Alive() {
		_ = t.proc.client.Send(closeCtx, "", "Target.detachFromTarget", map[string]any{"sessionId": t.sessionID}, nil)
	}
	t.cleanupEmptyOwnedDownloadsDir()
	t.backend.releaseProcess(t.proc, kill)
	_ = ctx
	return nil
}

// CDPTrace returns the formatted ring buffer of recent CDP messages for this tab.
func (t *cdpTab) CDPTrace(limit int) string {
	if t == nil || t.trace == nil {
		return ""
	}
	return t.trace.Format(limit)
}

// Run returns an explicit error explaining that Node/Puppeteer tab.run() is
// unavailable without Node, pointing callers to tab.evaluate() and direct helpers.
func (t *cdpTab) Run(_ context.Context, _ CallContext, _ string, _ string, _ []json.RawMessage) (*CallResult, error) {
	return nil, errors.New(
		"tab.run() with a Puppeteer/Node runtime is not supported in oh-my-pig's Go CDP backend (no Node/Puppeteer runtime). " +
			"Use direct tab.* helpers (tab.click, tab.fill, tab.observe, tab.screenshot, ...) from your Eval cell, or run page-side JavaScript with tab.evaluate(...).",
	)
}
