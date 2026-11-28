package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// NavigationTimeoutError signals that initial navigation timed out while the
// newly created tab stayed open on whatever loaded.
// upstream: NavigationTimeoutError in tab-supervisor.ts:883
type NavigationTimeoutError struct {
	Message string
}

func (e *NavigationTimeoutError) Error() string { return e.Message }

// tabKey scopes every named tab to its owning session so two sessions in the
// same PiG process using the default name ("main") never collide.
type tabKey struct {
	owner string
	name  string
}

// managedTab is one entry in the Manager's live tab table.
type managedTab struct {
	name           string
	kind           ResolvedKind
	tab            Tab
	ownerSessionID string
	dialogPolicy   string
	allowedDomains []string
	persist        bool
	lastActivityAt time.Time
	frozen         bool
}

// Manager owns the live tab table (keyed by session + tab name), backend
// dispatch, fallback chains, idle close, and session cleanup for the browser prelude.
// upstream: packages/coding-agent/src/tools/browser.ts + tab-supervisor.ts
type Manager struct {
	mu            sync.Mutex
	tabs          map[tabKey]*managedTab
	nameLocks     map[tabKey]*sync.Mutex
	endedSessions map[string]bool
	sessionGen    map[string]uint64
	cancelSeq     uint64
	ownerCancels  map[string]map[uint64]context.CancelFunc
	settings      func() Settings
	env           func(string) (string, bool)
	now           func() time.Time
	pid           int
	platform      string
}

// NewManager creates a Manager with default settings and OS environment lookup.
func NewManager() *Manager {
	return &Manager{
		tabs:          make(map[tabKey]*managedTab),
		nameLocks:     make(map[tabKey]*sync.Mutex),
		endedSessions: make(map[string]bool),
		sessionGen:    make(map[string]uint64),
		ownerCancels:  make(map[string]map[uint64]context.CancelFunc),
		settings:      DefaultSettings,
		env:           os.LookupEnv,
		now:           time.Now,
		pid:           os.Getpid(),
		platform:      runtime.GOOS,
	}
}

// StartSession marks ownerSessionID live again (clearing any tombstone left by
// an earlier lifecycle.End / session_shutdown on /reload or /resume).
func (m *Manager) StartSession(ownerSessionID string) {
	if ownerSessionID == "" {
		return
	}
	m.mu.Lock()
	delete(m.endedSessions, ownerSessionID)
	m.mu.Unlock()
}

func (m *Manager) trackOwnerContext(ctx context.Context, owner string) (context.Context, uint64, func(), error) {
	if owner == "" {
		return ctx, 0, func() {}, nil
	}
	m.mu.Lock()
	if m.endedSessions[owner] {
		m.mu.Unlock()
		return nil, 0, func() {}, fmt.Errorf("session %q has ended", owner)
	}
	gen := m.sessionGen[owner]
	subCtx, cancel := context.WithCancel(ctx)
	if m.ownerCancels == nil {
		m.ownerCancels = make(map[string]map[uint64]context.CancelFunc)
	}
	set := m.ownerCancels[owner]
	if set == nil {
		set = make(map[uint64]context.CancelFunc)
		m.ownerCancels[owner] = set
	}
	m.cancelSeq++
	id := m.cancelSeq
	set[id] = cancel
	m.mu.Unlock()
	return subCtx, gen, func() {
		cancel()
		m.mu.Lock()
		if s := m.ownerCancels[owner]; s != nil {
			delete(s, id)
			if len(s) == 0 {
				delete(m.ownerCancels, owner)
			}
		}
		m.mu.Unlock()
	}, nil
}
func (m *Manager) lockKey(key tabKey) func() {
	m.mu.Lock()
	nm, ok := m.nameLocks[key]
	if !ok {
		nm = &sync.Mutex{}
		m.nameLocks[key] = nm
	}
	m.mu.Unlock()
	nm.Lock()
	return nm.Unlock
}

// SetSettingsProvider configures how the manager reads fallback browser.* settings
// when a call does not supply per-session Settings from sdk.Context.GetSettings().
func (m *Manager) SetSettingsProvider(fn func() Settings) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if fn == nil {
		m.settings = DefaultSettings
	} else {
		m.settings = fn
	}
}

// Settings returns the current fallback Settings snapshot.
func (m *Manager) Settings() Settings {
	m.mu.Lock()
	fn := m.settings
	m.mu.Unlock()
	if fn == nil {
		return DefaultSettings()
	}
	return fn()
}

// BrowserParams is the wire shape sent by prelude.js / prelude.py to __omp_prelude__("browser", params).
// upstream: browserSchema in browser.ts:125
type BrowserParams struct {
	Action         string            `json:"action"`
	Name           string            `json:"name,omitempty"`
	URL            string            `json:"url,omitempty"`
	App            json.RawMessage   `json:"app,omitempty"`
	Argv           []string          `json:"argv,omitempty"`
	Target         *string           `json:"target,omitempty"`
	Viewport       *viewportInput    `json:"viewport,omitempty"`
	WaitUntil      string            `json:"wait_until,omitempty"`
	Dialogs        string            `json:"dialogs,omitempty"`
	AllowedDomains []string          `json:"allowed_domains,omitempty"`
	InitScripts    []string          `json:"init_scripts,omitempty"`
	Downloads      *string           `json:"downloads,omitempty"`
	UserAgent      *string           `json:"user_agent,omitempty"`
	Insecure       bool              `json:"insecure,omitempty"`
	Headed         *bool             `json:"headed,omitempty"`
	Code           string            `json:"code,omitempty"`
	Fn             string            `json:"fn,omitempty"`
	Args           []json.RawMessage `json:"args,omitempty"`
	Chain          []CallStep        `json:"chain,omitempty"`
	Timeout        *float64          `json:"timeout,omitempty"`
	All            bool              `json:"all,omitempty"`
	Kill           bool              `json:"kill,omitempty"`
	Persist        *bool             `json:"persist,omitempty"`
}

type viewportInput struct {
	Width             int      `json:"width"`
	Height            int      `json:"height"`
	Scale             *float64 `json:"scale,omitempty"`
	DeviceScaleFactor *float64 `json:"deviceScaleFactor,omitempty"`
	IsMobile          bool     `json:"isMobile,omitempty"`
	HasTouch          bool     `json:"hasTouch,omitempty"`
}

func (v *viewportInput) toViewport() *Viewport {
	if v == nil {
		return nil
	}
	vp := &Viewport{
		Width:    v.Width,
		Height:   v.Height,
		IsMobile: v.IsMobile,
		HasTouch: v.HasTouch,
	}
	if v.Scale != nil {
		vp.DeviceScaleFactor = *v.Scale
	} else if v.DeviceScaleFactor != nil {
		vp.DeviceScaleFactor = *v.DeviceScaleFactor
	}
	return vp
}

// normalizeAppField unpacks either a string `app` or omp's legacy object `app`.
// upstream: withStringApp in browser.ts:484
func normalizeAppField(p *BrowserParams) (appStr string, hasApp bool, err error) {
	if len(p.App) == 0 || string(p.App) == "null" {
		return "", false, nil
	}
	var s string
	if err := json.Unmarshal(p.App, &s); err == nil {
		return s, true, nil
	}
	var obj map[string]any
	if err := json.Unmarshal(p.App, &obj); err != nil {
		return "", false, fmt.Errorf("browser received invalid 'app' argument: expected string")
	}
	if cdp, ok := obj["cdp_url"].(string); ok {
		appStr, hasApp = cdp, true
	} else if cdp, ok := obj["cdpUrl"].(string); ok {
		appStr, hasApp = cdp, true
	} else if pth, ok := obj["path"].(string); ok {
		appStr, hasApp = pth, true
	} else if b, ok := obj["relay"].(bool); ok {
		if b {
			appStr, hasApp = "relay", true
		} else {
			appStr, hasApp = "auto", true
		}
	} else if b, ok := obj["tern"].(bool); ok {
		if b {
			appStr, hasApp = "tern", true
		} else {
			appStr, hasApp = "own", true
		}
	}
	if len(p.Argv) == 0 {
		rawArgv := obj["args"]
		if rawArgv == nil {
			rawArgv = obj["argv"]
		}
		if arr, ok := rawArgv.([]any); ok {
			for _, item := range arr {
				if sv, ok := item.(string); ok {
					p.Argv = append(p.Argv, sv)
				}
			}
		}
	}
	if p.Target == nil {
		if t, ok := obj["target"].(string); ok {
			p.Target = &t
		}
	}
	return appStr, hasApp, nil
}

// InvokeRequest carries the session context for one Manager.Invoke call.
type InvokeRequest struct {
	Cwd            string
	OwnerSessionID string
	TaskDepth      int
	CellTimeoutMs  int
	HasCellTimeout bool
	// Settings, when non-nil, overrides the manager's fallback settings for this call.
	Settings *Settings
}

// InvokeDetails is the structured details object returned to the prelude.
// upstream: BrowserPreludeDetails in browser.ts:161
type InvokeDetails struct {
	Action      string             `json:"action"`
	Name        string             `json:"name"`
	URL         string             `json:"url,omitempty"`
	Browser     Kind               `json:"browser,omitempty"`
	Viewport    *Viewport          `json:"viewport,omitempty"`
	Screenshots []ScreenshotResult `json:"screenshots,omitempty"`
	Value       any                `json:"value,omitempty"`
}

// InvokeResult is the return value of Manager.Invoke.
type InvokeResult struct {
	Text    string
	Details InvokeDetails
	Images  []ImageOutput
}

// Invoke executes one browser prelude action: open, close, tabs, targets, call, or run.
// Any panic inside the call or a backend is recovered and returned as an error so
// a fused PiG extension can never crash the host process.
// upstream: invokeBrowser in browser.ts:434
func (m *Manager) Invoke(ctx context.Context, req InvokeRequest, rawParams json.RawMessage) (res *InvokeResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			res = nil
			err = fmt.Errorf("browser internal panic: %v", r)
		}
	}()

	var params BrowserParams
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return nil, fmt.Errorf("browser received invalid arguments: %w", err)
	}
	switch params.Action {
	case "open", "close", "run", "call", "tabs", "targets":
	default:
		return nil, fmt.Errorf("browser received invalid arguments: unknown action %q", params.Action)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	settings := m.Settings()
	if req.Settings != nil {
		settings = *req.Settings
	}
	requestedTimeout := params.Timeout
	if requestedTimeout == nil && (params.Action == "call" || params.Action == "run") {
		requestedTimeout = TabCallBudgetSeconds(params.Chain, req.CellTimeoutMs, req.HasCellTimeout)
	}
	timeoutMs := ClampTimeoutMs(requestedTimeout, settings.MaxTimeoutSec)

	name := params.Name
	if name == "" {
		name = DefaultTabName
	}

	opCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	switch params.Action {
	case "open":
		return m.openTab(opCtx, ctx, req, name, &params, settings, timeoutMs)
	case "close":
		return m.closeTab(opCtx, req, name, &params)
	case "tabs":
		return &InvokeResult{
			Details: InvokeDetails{
				Action: "tabs",
				Name:   name,
				Value:  m.ListTabsForOwner(req.OwnerSessionID),
			},
		}, nil
	case "targets":
		return m.listTargets(opCtx, req, name, &params, settings, timeoutMs)
	case "call":
		return m.callTab(opCtx, ctx, req, name, &params, settings, timeoutMs)
	case "run":
		return m.runTab(opCtx, ctx, req, name, &params, settings, timeoutMs)
	default:
		return nil, fmt.Errorf("unknown action %q", params.Action)
	}
}

func (m *Manager) resolveSession(req InvokeRequest, settings Settings) ResolveSession {
	return ResolveSession{
		Cwd:       req.Cwd,
		TaskDepth: req.TaskDepth,
		Settings:  settings,
		PID:       m.pid,
		Platform:  m.platform,
	}
}

func (m *Manager) openTab(ctx, callerCtx context.Context, req InvokeRequest, name string, params *BrowserParams, settings Settings, timeoutMs int) (*InvokeResult, error) {
	key := tabKey{owner: req.OwnerSessionID, name: name}
	// Serialise opens per (session, tab name) (upstream: acquireChains in tab-supervisor.ts:519).
	unlock := m.lockKey(key)
	defer unlock()

	openCtx, gen, stopTrack, err := m.trackOwnerContext(ctx, req.OwnerSessionID)
	if err != nil {
		return nil, err
	}
	defer stopTrack()
	ctx = openCtx
	if err := callerCtx.Err(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("Browser open timed out after %dms while acquiring tab %q", timeoutMs, name)
	}
	appStr, hasApp, err := normalizeAppField(params)
	if err != nil {
		return nil, err
	}
	normalizedDomains, err := NormalizeAllowedDomains(params.AllowedDomains)
	if err != nil {
		return nil, err
	}
	if params.Dialogs != "" && params.Dialogs != "accept" && params.Dialogs != "dismiss" {
		return nil, fmt.Errorf(`dialogs must be "accept" or "dismiss"; got %q`, params.Dialogs)
	}

	rParams := ResolveParams{
		App:       appStr,
		HasApp:    hasApp,
		Argv:      params.Argv,
		HasTarget: params.Target != nil,
		Insecure:  params.Insecure,
		Headed:    params.Headed,
	}
	if params.Target != nil {
		rParams.Target = *params.Target
	}
	sess := m.resolveSession(req, settings)
	resolved, err := ResolveBrowserKind(rParams, sess, m.env)
	if err != nil {
		return nil, err
	}

	autoTern := resolved.Kind == KindTern && appStr != "tern" && params.Target == nil
	autoWebKit := appStr != "webkit"
	existing := m.getLiveTab(ctx, key)
	autoCmux := resolved.Kind == KindCmux && (existing == nil || existing.kind.Kind != KindCmux)

	kind := resolved
	if autoTern && existing != nil && existing.kind.Kind != KindTern {
		kind = existing.kind
	} else if existing != nil && existing.kind.Kind == KindHeadless &&
		params.Headed == nil &&
		(!params.Insecure || existing.kind.Insecure) &&
		(resolved.Kind == KindHeadless || (resolved.Kind == KindWebKit && autoWebKit)) {
		kind = existing.kind
	} else if resolved.Kind == KindWebKit && autoWebKit && existing != nil {
		chrom := chromiumKind(rParams, settings)
		if existing.kind.Key() == chrom.Key() {
			kind = chrom
		}
	}

	type fallbackFailure struct {
		backend Kind
		reason  string
	}
	var failures []fallbackFailure

	vp := params.Viewport.toViewport()
	var chromiumOnly string
	if params.Insecure {
		chromiumOnly = "it cannot ignore invalid HTTPS certificates (insecure)"
	} else if vp != nil && (vp.IsMobile || vp.HasTouch) {
		chromiumOnly = "it cannot emulate a mobile or touch viewport"
	}
	if kind.Kind == KindWebKit && chromiumOnly != "" && !autoWebKit {
		return nil, fmt.Errorf(`app: "webkit" cannot take this open: %s. Pass app: "chromium", or omit app.`, chromiumOnly)
	}

	for {
		if kind.Kind == KindWebKit && chromiumOnly != "" {
			failures = append(failures, fallbackFailure{backend: KindWebKit, reason: chromiumOnly})
			kind = chromiumKind(rParams, settings)
		}

		openedLabel := "Chromium"
		if kind.Kind == KindWebKit {
			openedLabel = "omp's WebKit browser"
		}
		var notes []string
		for _, f := range failures {
			switch f.backend {
			case KindTern:
				notes = append(notes, fmt.Sprintf(
					`Tern cannot show a browser here (%s); opened %s instead. Pass app: "own" to skip Tern, or app: "tern" to require it.`,
					f.reason, openedLabel,
				))
			case KindCmux:
				notes = append(notes, fmt.Sprintf(
					`cmux could not open a browser surface here (%s); opened %s instead. Pass app: "own" to skip cmux.`,
					f.reason, openedLabel,
				))
			default:
				notes = append(notes, fmt.Sprintf(
					`omp's WebKit browser could not take this open (%s); opened Chromium instead. Pass app: "chromium" to skip it, or app: "webkit" to require it.`,
					f.reason,
				))
			}
		}

		res, openErr := m.openOnKind(ctx, callerCtx, req, key, gen, params, normalizedDomains, settings, kind, timeoutMs, notes)
		if openErr == nil {
			return res, nil
		}
		if callerCtx.Err() != nil {
			return nil, openErr
		}
		var next ResolvedKind
		if kind.Kind == KindTern && autoTern && IsUnavailable(openErr) {
			failures = append(failures, fallbackFailure{backend: KindTern, reason: openErr.Error()})
			next = ownBrowserKind(rParams, sess, m.env)
		} else if kind.Kind == KindWebKit && autoWebKit && IsUnavailable(openErr) {
			failures = append(failures, fallbackFailure{backend: KindWebKit, reason: openErr.Error()})
			next = chromiumKind(rParams, settings)
		} else if kind.Kind == KindCmux && autoCmux && IsUnavailable(openErr) {
			failures = append(failures, fallbackFailure{backend: KindCmux, reason: openErr.Error()})
			next = ownBrowserKind(rParams, sess, m.env)
		} else {
			return nil, openErr
		}
		if cur := m.getLiveTab(ctx, key); cur != nil && cur.kind.Kind == kind.Kind {
			_, _ = m.releaseKey(ctx, key, false)
		}
		kind = next
	}
}

func (m *Manager) openOnKind(
	ctx, callerCtx context.Context,
	req InvokeRequest,
	key tabKey,
	ownerGen uint64,
	params *BrowserParams,
	normalizedDomains []string,
	settings Settings,
	kind ResolvedKind,
	timeoutMs int,
	notes []string,
) (*InvokeResult, error) {
	name := key.name
	existing := m.getLiveTab(ctx, key)
	if existing != nil && existing.kind.Key() != kind.Key() {
		return nil, fmt.Errorf(
			"Tab %q is bound to a different browser (%s, not %s). Close it first, or reopen it with the app and headed it was opened with.",
			name, existing.kind.Describe(), kind.Describe(),
		)
	}
	if params.Insecure && kind.Kind == KindRelay {
		return nil, fmt.Errorf(
			`insecure is not supported through the Chrome relay: the extension debugger API does not expose the CDP Security domain. Pass app: "chromium" to use omp's Chromium instead.`,
		)
	}
	if params.Downloads != nil && kind.Kind == KindRelay {
		return nil, fmt.Errorf(
			"The omp browser relay cannot choose where Chrome saves downloads: open the tab without `downloads`, and tab.waitForDownload() and tab.downloads() report where Chrome saved each file.",
		)
	}

	backend, ok := LookupBackend(string(kind.Kind))
	if !ok {
		return nil, m.missingBackendError(kind)
	}

	if avail, reason := backend.Available(ctx); !avail {
		return nil, &UnavailableError{Backend: kind.Kind, Reason: reason}
	}

	initScripts, err := resolveInitScriptSources(params.InitScripts, req.Cwd)
	if err != nil {
		return nil, err
	}

	var downloadsPath string
	if params.Downloads != nil {
		downloadsPath = resolveToCwd(*params.Downloads, req.Cwd)
	}
	waitUntil := params.WaitUntil
	if waitUntil == "networkidle" {
		waitUntil = "networkidle0"
	}
	opts := OpenOptions{
		Name:           name,
		URL:            params.URL,
		WaitUntil:      waitUntil,
		Viewport:       params.Viewport.toViewport(),
		Dialogs:        params.Dialogs,
		AllowedDomains: normalizedDomains,
		InitScripts:    initScripts,
		DownloadsPath:  downloadsPath,
		Insecure:       params.Insecure,
		Headless:       kind.Headless,
		Cwd:            req.Cwd,
		ScreenshotDir:  expandTilde(settings.ScreenshotDir),
		TimeoutMs:      timeoutMs,
		Endpoint:       kind.CdpURL,
		Pane:           kind.Pane,
		Executable:     kind.Path,
		Argv:           kind.Args,
	}
	if kind.Kind == KindTern || kind.Kind == KindCmux {
		opts.Endpoint = kind.SocketPath
	}
	if params.Target != nil {
		opts.Target = *params.Target
	}
	if params.UserAgent != nil {
		opts.UserAgent = *params.UserAgent
	}
	if params.Persist != nil {
		opts.Persist = *params.Persist
	}

	// Worker-init / policy options cannot be applied cleanly to a live tab:
	// close and reopen when init scripts, downloads, user agent, insecure,
	// dialogs policy, or allowed_domains change.
	// upstream: browser.ts:773-783, tab-supervisor.ts:596-606
	needsReopen := existing != nil && (len(initScripts) > 0 ||
		params.Downloads != nil ||
		params.UserAgent != nil ||
		params.Insecure ||
		(params.Dialogs != "" && params.Dialogs != existing.dialogPolicy) ||
		(params.AllowedDomains != nil && !sameAllowedDomains(normalizedDomains, existing.allowedDomains)))
	if needsReopen {
		_, _ = m.releaseKey(ctx, key, false)
		existing = nil
	}

	var (
		tab       Tab
		created   bool
		reuseNote string
	)
	if existing != nil {
		tab = existing.tab
		m.mu.Lock()
		wasFrozen := existing.frozen
		m.mu.Unlock()
		if f, ok := tab.(Freezer); ok && wasFrozen {
			_ = f.Unfreeze(ctx)
			m.mu.Lock()
			existing.frozen = false
			m.mu.Unlock()
		}
		reuseNote, err = tab.Reuse(ctx, opts)
		if err != nil {
			var navTimeout *NavigationTimeoutError
			if errors.As(err, &navTimeout) {
				return nil, fmt.Errorf("%s\nTab %q stays open on what loaded; reach it with browser.tab(%q).", navTimeout.Error(), name, name)
			}
			if ctx.Err() != nil && callerCtx.Err() == nil {
				return nil, fmt.Errorf("Browser open timed out after %dms while opening tab %q", timeoutMs, name)
			}
			return nil, err
		}
		m.mu.Lock()
		existing.lastActivityAt = m.now()
		if params.Persist != nil {
			existing.persist = *params.Persist
		}
		m.mu.Unlock()
	} else {
		tab, err = backend.Open(ctx, opts)
		var navTimeout *NavigationTimeoutError
		if err != nil && tab != nil && errors.As(err, &navTimeout) && callerCtx.Err() == nil {
			mt := &managedTab{
				name:           name,
				kind:           kind,
				tab:            tab,
				ownerSessionID: req.OwnerSessionID,
				dialogPolicy:   params.Dialogs,
				allowedDomains: normalizedDomains,
				persist:        opts.Persist,
				lastActivityAt: m.now(),
			}
			if !m.storeTab(key, ownerGen, mt) {
				return nil, fmt.Errorf("session %q has ended", req.OwnerSessionID)
			}
			return nil, fmt.Errorf("%s\nTab %q stays open on what loaded; reach it with browser.tab(%q).", navTimeout.Error(), name, name)
		}
		if err != nil {
			if ctx.Err() != nil && callerCtx.Err() == nil && !IsUnavailable(err) {
				m.mu.Lock()
				ended := req.OwnerSessionID != "" && (m.endedSessions[req.OwnerSessionID] || m.sessionGen[req.OwnerSessionID] != ownerGen)
				m.mu.Unlock()
				if ended {
					return nil, fmt.Errorf("session %q has ended", req.OwnerSessionID)
				}
				return nil, fmt.Errorf("Browser open timed out after %dms while launching or connecting to the browser (%s): %v", timeoutMs, kind.Describe(), err)
			}
			return nil, err
		}
		created = true
		mt := &managedTab{
			name:           name,
			kind:           kind,
			tab:            tab,
			ownerSessionID: req.OwnerSessionID,
			dialogPolicy:   params.Dialogs,
			allowedDomains: normalizedDomains,
			persist:        opts.Persist,
			lastActivityAt: m.now(),
		}
		if !m.storeTab(key, ownerGen, mt) {
			return nil, fmt.Errorf("session %q has ended", req.OwnerSessionID)
		}
	}
	if settings.IdleCloseSec > 0 && req.OwnerSessionID != "" {
		go m.ReleaseIdleTabsForOwner(context.Background(), req.OwnerSessionID, time.Duration(settings.IdleCloseSec)*time.Second)
	}

	info := tab.Info()
	verb := "Reused"
	if created {
		verb = "Opened"
	}
	desc := info.Description
	if desc == "" {
		desc = describeDefaultBrowser(kind)
	}
	line := fmt.Sprintf("%s tab %q on %s", verb, name, desc)
	if params.URL == "" || !SameAddress(params.URL, info.URL) {
		line += ", at " + info.URL
	}
	if info.Title != "" {
		line += fmt.Sprintf(", titled %q", info.Title)
	}
	lines := []string{line}
	lines = append(lines, info.Notes...)
	lines = append(lines, notes...)
	if reuseNote != "" {
		lines = append(lines, reuseNote)
	}

	vpCopy := info.Viewport
	return &InvokeResult{
		Text: strings.Join(lines, "\n"),
		Details: InvokeDetails{
			Action:   "open",
			Name:     name,
			URL:      info.URL,
			Browser:  kind.Kind,
			Viewport: &vpCopy,
		},
	}, nil
}

// storeTab publishes mt under key, closing any displaced prior tab so it cannot leak.
// If the owning session has already ended (or reloaded into a newer generation),
// mt is closed immediately and false is returned.
func (m *Manager) storeTab(key tabKey, ownerGen uint64, mt *managedTab) bool {
	m.mu.Lock()
	if key.owner != "" && (m.endedSessions[key.owner] || m.sessionGen[key.owner] != ownerGen) {
		m.mu.Unlock()
		_ = mt.tab.Close(context.Background(), true)
		return false
	}
	old := m.tabs[key]
	m.tabs[key] = mt
	m.mu.Unlock()
	if old != nil && old != mt {
		_ = old.tab.Close(context.Background(), false)
	}
	return true
}

func (m *Manager) missingBackendError(kind ResolvedKind) error {
	switch kind.Kind {
	case KindTern:
		return &UnavailableError{
			Backend: KindTern,
			Reason:  "Tern browser backend is not registered in this build",
		}
	case KindWebKit:
		return &UnavailableError{
			Backend: KindWebKit,
			Reason:  "omp's WebKit browser backend is not ported in oh-my-pig",
		}
	case KindCmux:
		return &UnavailableError{
			Backend: KindCmux,
			Reason:  "cmux browser backend is not ported in oh-my-pig",
		}
	case KindRelay:
		return fmt.Errorf(
			`The omp browser relay backend is not ported in oh-my-pig. Pass app: "auto" or app: "chromium", or connect directly with a CDP URL (e.g. app: "http://127.0.0.1:9222").`,
		)
	default:
		return fmt.Errorf("browser backend %q is not registered", kind.Kind)
	}
}

func describeDefaultBrowser(kind ResolvedKind) string {
	switch kind.Kind {
	case KindHeadless:
		if kind.Headless {
			return "omp's headless Chromium"
		}
		return "omp's Chromium (in a window)"
	case KindSpawned:
		return "spawned " + kind.Path
	case KindConnected:
		return "connected " + kind.CdpURL
	case KindRelay:
		return "relay " + kind.CdpURL
	case KindTern:
		return fmt.Sprintf("Tern browser picture-in-picture (pane %d)", kind.Pane)
	case KindWebKit:
		return "omp's headless WebKit browser"
	case KindCmux:
		surf := kind.Surface
		if surf == "" {
			surf = "split"
		}
		return fmt.Sprintf("cmux browser (%s)", surf)
	default:
		return string(kind.Kind)
	}
}

func (m *Manager) listTargets(ctx context.Context, req InvokeRequest, name string, params *BrowserParams, settings Settings, timeoutMs int) (*InvokeResult, error) {
	appStr, hasApp, err := normalizeAppField(params)
	if err != nil {
		return nil, err
	}
	target := ""
	if params.Target != nil {
		target = *params.Target
	}
	rParams := ResolveParams{
		App:       appStr,
		HasApp:    hasApp,
		Argv:      params.Argv,
		Target:    target,
		HasTarget: true,
		Insecure:  params.Insecure,
		Headed:    params.Headed,
	}
	kind, err := ResolveBrowserKind(rParams, m.resolveSession(req, settings), m.env)
	if err != nil {
		return nil, err
	}
	if kind.Kind != KindTern && kind.Kind != KindRelay && kind.Kind != KindConnected {
		return nil, fmt.Errorf(
			`No pages to adopt here (the browser would be %s): targets exist only inside a Tern pane (its browser blocks), with the omp relay (app: "relay" or the browser.relay setting), or at a CDP endpoint (app: "http://127.0.0.1:9222" or the browser.cdpUrl setting).`,
			kind.Describe(),
		)
	}
	backend, ok := LookupBackend(string(kind.Kind))
	if !ok {
		return nil, m.missingBackendError(kind)
	}
	lister, ok := backend.(TargetLister)
	if !ok {
		return nil, fmt.Errorf("%s has no pages to adopt.", kind.Describe())
	}

	taken := m.takenTargets(kind.Kind)
	opts := OpenOptions{
		Endpoint:  kind.CdpURL,
		Pane:      kind.Pane,
		TimeoutMs: timeoutMs,
	}
	if kind.Kind == KindTern {
		opts.Endpoint = kind.SocketPath
	}
	list, err := lister.ListTargets(ctx, opts, taken)
	if err != nil {
		return nil, err
	}
	targets := list.Targets
	if targets == nil {
		targets = []TargetInfo{}
	}
	return &InvokeResult{
		Text: list.Text,
		Details: InvokeDetails{
			Action:  "targets",
			Name:    name,
			Browser: kind.Kind,
			Value:   targets,
		},
	}, nil
}

func (m *Manager) closeTab(ctx context.Context, req InvokeRequest, name string, params *BrowserParams) (*InvokeResult, error) {
	if params.All {
		// Never treat an empty session ID as "all sessions in the process".
		count := 0
		if req.OwnerSessionID != "" {
			count = m.ReleaseTabsForOwner(ctx, req.OwnerSessionID, params.Kill)
		}
		plural := "s"
		if count == 1 {
			plural = ""
		}
		return &InvokeResult{
			Text: fmt.Sprintf("Released %d managed tab%s", count, plural),
			Details: InvokeDetails{
				Action: "close",
				Name:   name,
			},
		}, nil
	}
	closed, err := m.releaseKey(ctx, tabKey{owner: req.OwnerSessionID, name: name}, params.Kill)
	if err != nil {
		return nil, err
	}
	text := fmt.Sprintf("No tab named %q", name)
	if closed {
		text = fmt.Sprintf("Released managed tab %q", name)
	}
	return &InvokeResult{
		Text: text,
		Details: InvokeDetails{
			Action: "close",
			Name:   name,
		},
	}, nil
}

func (m *Manager) callTab(ctx, callerCtx context.Context, req InvokeRequest, name string, params *BrowserParams, settings Settings, timeoutMs int) (*InvokeResult, error) {
	if err := ValidateTabCallChain(params.Chain); err != nil {
		return nil, err
	}
	key := tabKey{owner: req.OwnerSessionID, name: name}
	mt, deadErr := m.getLiveTabWithReason(ctx, key)
	if mt == nil {
		if deadErr != nil {
			return nil, deadErr
		}
		return nil, fmt.Errorf("No open browser tab named %q. Open one first with browser.open({ name: %q, url: ... }).", name, name)
	}
	m.mu.Lock()
	wasFrozen := mt.frozen
	m.mu.Unlock()
	if f, ok := mt.tab.(Freezer); ok && wasFrozen {
		_ = f.Unfreeze(ctx)
		m.mu.Lock()
		mt.frozen = false
		m.mu.Unlock()
	}
	m.mu.Lock()
	mt.lastActivityAt = m.now()
	m.mu.Unlock()

	callCtx := CallContext{
		Cwd:           req.Cwd,
		ScreenshotDir: expandTilde(settings.ScreenshotDir),
		TimeoutMs:     timeoutMs,
	}
	startedAt := time.Now()
	stalledLabel := callChainLabel(params.Chain)
	res, err := mt.tab.Call(ctx, callCtx, params.Chain)
	if err != nil {
		return nil, m.handleCallFailure(key, mt, err, callerCtx, timeoutMs, stalledLabel, startedAt)
	}
	if res == nil {
		res = &CallResult{}
	}
	info := mt.tab.Info()
	return &InvokeResult{
		Text: res.Text,
		Details: InvokeDetails{
			Action:      "call",
			Name:        name,
			URL:         info.URL,
			Browser:     mt.kind.Kind,
			Screenshots: res.Screenshots,
			Value:       res.Value,
		},
		Images: res.Images,
	}, nil
}

func (m *Manager) runTab(ctx, callerCtx context.Context, req InvokeRequest, name string, params *BrowserParams, settings Settings, timeoutMs int) (*InvokeResult, error) {
	code := strings.TrimSpace(params.Code)
	fn := strings.TrimSpace(params.Fn)
	if (code == "") == (fn == "") {
		return nil, fmt.Errorf("Action 'run' requires exactly one of 'code' or 'fn'.")
	}
	key := tabKey{owner: req.OwnerSessionID, name: name}
	mt, deadErr := m.getLiveTabWithReason(ctx, key)
	if mt == nil {
		if deadErr != nil {
			return nil, deadErr
		}
		return nil, fmt.Errorf("No open browser tab named %q. Open one first with browser.open({ name: %q, url: ... }).", name, name)
	}
	m.mu.Lock()
	wasFrozen := mt.frozen
	m.mu.Unlock()
	if f, ok := mt.tab.(Freezer); ok && wasFrozen {
		_ = f.Unfreeze(ctx)
		m.mu.Lock()
		mt.frozen = false
		m.mu.Unlock()
	}
	m.mu.Lock()
	mt.lastActivityAt = m.now()
	m.mu.Unlock()

	callCtx := CallContext{
		Cwd:           req.Cwd,
		ScreenshotDir: expandTilde(settings.ScreenshotDir),
		TimeoutMs:     timeoutMs,
	}
	startedAt := time.Now()
	res, err := mt.tab.Run(ctx, callCtx, code, fn, params.Args)
	if err != nil {
		return nil, m.handleCallFailure(key, mt, err, callerCtx, timeoutMs, "tab.run()", startedAt)
	}
	if res == nil {
		res = &CallResult{}
	}
	info := mt.tab.Info()
	return &InvokeResult{
		Text: res.Text,
		Details: InvokeDetails{
			Action:      "run",
			Name:        name,
			URL:         info.URL,
			Browser:     mt.kind.Kind,
			Screenshots: res.Screenshots,
			Value:       res.Value,
		},
		Images: res.Images,
	}, nil
}

// workerRecycleResets matches omp's WORKER_RESETS + RECYCLE_RESETS
// (tab-supervisor.ts:320-323) so the model knows what a session reattach resets.
const workerRecycleResets = "Tab state set since it was opened was reset (tab.route routes, emulation and user agent, init scripts, element ids, the mapping of positions on a scaled screenshot, the request log, HAR recording, run globals), any open dialog was dismissed, and any page still loading was stopped."

// longerBudgetHint matches omp's longerBudgetHint (tab-supervisor.ts:325-328).
func longerBudgetHint(timeoutMs int) string {
	maxMs := int(browserMaxTimeoutSec * 1000)
	if timeoutMs >= maxMs {
		return ""
	}
	return fmt.Sprintf(" To give long page work more time, raise the Eval cell's `timeout` or pass `timeout` to `tab.run` (seconds, at most %d); also raise or disable `tools.maxTimeout` if it caps that budget.", int(browserMaxTimeoutSec))
}

// callChainLabel formats the active helper call for whole-call timeout diagnostics
// (matching omp's describeInflight in tab-worker.ts:1933-1939).
func callChainLabel(chain []CallStep) string {
	if len(chain) == 0 {
		return ""
	}
	if len(chain) == 2 {
		switch chain[0].Method {
		case "id", "ref":
			return "element." + chain[1].Method + "()"
		case "frame":
			lbl, _ := opLabelAndSelector("frame.", chain[1].Method, decodeArgs(chain[1].Args))
			return lbl
		}
	}
	lbl, _ := opLabelAndSelector("tab.", chain[0].Method, decodeArgs(chain[0].Args))
	return lbl
}

// reopenAllowedDomainsHint reminds the model to re-pass allowed_domains when a
// tab with an allowlist was closed after a crash or failed reattach.
func reopenAllowedDomainsHint(domains []string) string {
	if len(domains) == 0 {
		return ""
	}
	b, err := json.Marshal(domains)
	if err != nil {
		return " Pass the same allowed_domains when reopening."
	}
	return fmt.Sprintf(" Pass the same allowed_domains (%s) when reopening.", string(b))
}

func (m *Manager) handleCallFailure(key tabKey, mt *managedTab, err error, callerCtx context.Context, timeoutMs int, stalledLabel string, startedAt time.Time) error {
	var crashed *RendererCrashedError
	if errors.As(err, &crashed) {
		relCtx, relCancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, _ = m.releaseKey(relCtx, key, false)
		relCancel()
		return fmt.Errorf("%s; the tab was closed. Open it again with browser.open().%s", crashed.Error(), reopenAllowedDomainsHint(mt.allowedDomains))
	}
	var disconn *BrowserDisconnectedError
	if errors.As(err, &disconn) {
		relCtx, relCancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, _ = m.releaseKey(relCtx, key, false)
		relCancel()
		return disconn
	}
	if errors.Is(err, context.DeadlineExceeded) && callerCtx.Err() == nil {
		// Whole-call budget expired: attach a fresh CDP session and enable
		// interception BEFORE detaching the old session so the page and its JS
		// state survive while open-time security and session settings are restored
		// by configureSession (upstream: tab-supervisor.ts:1664-1682, tab-worker.ts:2611).
		var stalledClause string
		if stalledLabel != "" {
			elapsedSec := time.Since(startedAt).Seconds()
			stalledClause = fmt.Sprintf(" (stalled on %s (%.1fs))", stalledLabel, elapsedSec)
		}
		var traceDump string
		if ParseFlag(os.Getenv("OMP_BROWSER_TRACE_CDP"), false) {
			if ct, ok := mt.tab.(*cdpTab); ok {
				traceDump = "\n--- CDP trace (last 80) ---\n" + ct.CDPTrace(80)
			}
		}
		if ct, ok := mt.tab.(*cdpTab); ok {
			reCtx, reCancel := context.WithTimeout(context.Background(), 4*time.Second)
			reattached := ct.reattachSession(reCtx)
			reCancel()
			if reattached {
				return fmt.Errorf("Browser code execution timed out after %dms%s. omp reattached the tab to a new worker without reloading its page. %s%s%s", timeoutMs, stalledClause, workerRecycleResets, longerBudgetHint(timeoutMs), traceDump)
			}
		}
		relCtx, relCancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, _ = m.releaseKey(relCtx, key, false)
		relCancel()
		return fmt.Errorf("Browser code execution timed out after %dms%s. The tab could not be reattached and was released; open it again with browser.open().%s%s%s", timeoutMs, stalledClause, reopenAllowedDomainsHint(mt.allowedDomains), longerBudgetHint(timeoutMs), traceDump)
	}
	return err
}

func (m *Manager) getLiveTab(ctx context.Context, key tabKey) *managedTab {
	mt, _ := m.getLiveTabWithReason(ctx, key)
	return mt
}

// getLiveTabWithReason returns the live tab or, if it died while idle (renderer
// crashed or browser process exited), cleans it up and returns an explicit
// reason instead of silently saying "No open browser tab named" (upstream:
// tab-worker.ts:2498-2500).
func (m *Manager) getLiveTabWithReason(ctx context.Context, key tabKey) (*managedTab, error) {
	m.mu.Lock()
	mt, ok := m.tabs[key]
	m.mu.Unlock()
	if !ok {
		return nil, nil
	}
	if !mt.tab.Alive() {
		var reason error
		if ct, ok := mt.tab.(*cdpTab); ok {
			switch {
			case ct.crashed.Load():
				reason = fmt.Errorf("Browser tab's renderer had crashed before this run, so its code did not run; the tab was closed. Open it again with browser.open().%s", reopenAllowedDomainsHint(mt.allowedDomains))
			case ct.proc != nil && !ct.proc.client.Alive():
				reason = &BrowserDisconnectedError{}
			}
		}
		m.mu.Lock()
		removed := false
		if m.tabs[key] == mt {
			delete(m.tabs, key)
			removed = true
		}
		m.mu.Unlock()
		if removed {
			_ = mt.tab.Close(ctx, false)
		}
		return nil, reason
	}
	return mt, nil
}

func (m *Manager) takenTargets(kind Kind) map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]string{}
	for _, mt := range m.tabs {
		if mt.kind.Kind == kind && mt.tab.Alive() {
			if tid := mt.tab.Info().TargetID; tid != "" {
				out[tid] = mt.name
			}
		}
	}
	return out
}

// ListTabsForOwner returns currently alive managed tabs owned by ownerSessionID.
func (m *Manager) ListTabsForOwner(ownerSessionID string) []ManagedTabInfo {
	m.mu.Lock()
	var dead []*managedTab
	out := make([]ManagedTabInfo, 0, len(m.tabs))
	for k, mt := range m.tabs {
		if k.owner != ownerSessionID {
			continue
		}
		if !mt.tab.Alive() {
			dead = append(dead, mt)
			delete(m.tabs, k)
			continue
		}
		info := mt.tab.Info()
		out = append(out, ManagedTabInfo{
			Name:     mt.name,
			URL:      info.URL,
			Title:    info.Title,
			TargetID: info.TargetID,
			Kind:     mt.kind.Kind,
			Persist:  mt.persist,
		})
	}
	m.mu.Unlock()
	for _, mt := range dead {
		_ = mt.tab.Close(context.Background(), false)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ListTabs returns all currently alive managed tabs across all sessions.
// upstream: listTabs in tab-supervisor.ts:486
func (m *Manager) ListTabs() []ManagedTabInfo {
	m.mu.Lock()
	var dead []*managedTab
	out := make([]ManagedTabInfo, 0, len(m.tabs))
	for k, mt := range m.tabs {
		if !mt.tab.Alive() {
			dead = append(dead, mt)
			delete(m.tabs, k)
			continue
		}
		info := mt.tab.Info()
		out = append(out, ManagedTabInfo{
			Name:     mt.name,
			URL:      info.URL,
			Title:    info.Title,
			TargetID: info.TargetID,
			Kind:     mt.kind.Kind,
			Persist:  mt.persist,
		})
	}
	m.mu.Unlock()
	for _, mt := range dead {
		_ = mt.tab.Close(context.Background(), false)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (m *Manager) releaseKey(ctx context.Context, key tabKey, kill bool) (bool, error) {
	m.mu.Lock()
	mt, ok := m.tabs[key]
	if ok {
		delete(m.tabs, key)
	}
	m.mu.Unlock()
	if !ok {
		return false, nil
	}
	return true, mt.tab.Close(ctx, kill)
}

// ReleaseTab closes and forgets one named tab in the empty-owner scope (kept for callers/tests).
func (m *Manager) ReleaseTab(ctx context.Context, name string, kill bool) (bool, error) {
	return m.releaseKey(ctx, tabKey{name: name}, kill)
}

// ReleaseTabsForOwner marks ownerSessionID ended, cancels any in-flight opens
// for it, and closes ONLY tabs owned by ownerSessionID.
// If ownerSessionID is empty, releases nothing.
func (m *Manager) ReleaseTabsForOwner(ctx context.Context, ownerSessionID string, kill bool) int {
	if ownerSessionID == "" {
		return 0
	}
	m.mu.Lock()
	if m.endedSessions == nil {
		m.endedSessions = make(map[string]bool)
	}
	if m.sessionGen == nil {
		m.sessionGen = make(map[string]uint64)
	}
	m.endedSessions[ownerSessionID] = true
	m.sessionGen[ownerSessionID]++
	cancels := make([]context.CancelFunc, 0, len(m.ownerCancels[ownerSessionID]))
	for _, c := range m.ownerCancels[ownerSessionID] {
		cancels = append(cancels, c)
	}
	delete(m.ownerCancels, ownerSessionID)
	var victims []*managedTab
	for k, mt := range m.tabs {
		if k.owner == ownerSessionID {
			victims = append(victims, mt)
			delete(m.tabs, k)
		}
	}
	m.mu.Unlock()
	for _, c := range cancels {
		c()
	}
	for _, mt := range victims {
		_ = mt.tab.Close(ctx, kill)
	}
	return len(victims)
}

// DropHeadlessTabs closes ONLY owned KindHeadless tabs belonging to
// ownerSessionID without tombstoning the session, bumping sessionGen,
// cancelling ownerCancels, or closing connected/spawned/relay tabs.
// upstream: dropHeadlessTabs in packages/coding-agent/src/tools/browser.ts:399
func (m *Manager) DropHeadlessTabs(ctx context.Context, ownerSessionID string) int {
	if ownerSessionID == "" {
		return 0
	}
	m.mu.Lock()
	var victims []*managedTab
	for k, mt := range m.tabs {
		if k.owner != ownerSessionID {
			continue
		}
		if mt.kind.Kind != KindHeadless {
			continue
		}
		victims = append(victims, mt)
		delete(m.tabs, k)
	}
	m.mu.Unlock()
	for _, mt := range victims {
		_ = mt.tab.Close(ctx, false)
	}
	return len(victims)
}

// ReleaseAllTabs closes every managed tab in this process (intended for tests/process teardown).
func (m *Manager) ReleaseAllTabs(ctx context.Context, kill bool) int {
	m.mu.Lock()
	victims := make([]*managedTab, 0, len(m.tabs))
	for k, mt := range m.tabs {
		victims = append(victims, mt)
		delete(m.tabs, k)
	}
	m.mu.Unlock()
	for _, mt := range victims {
		_ = mt.tab.Close(ctx, kill)
	}
	return len(victims)
}

// ReleaseIdleTabsForOwner closes non-persisted headless/Tern tabs owned by
// ownerSessionID that have been idle longer than idleLimit.
// upstream: releaseIdleTabsForOwner in tab-supervisor.ts
func (m *Manager) ReleaseIdleTabsForOwner(ctx context.Context, ownerSessionID string, idleLimit time.Duration) int {
	if ownerSessionID == "" || idleLimit <= 0 {
		return 0
	}
	cutoff := m.now().Add(-idleLimit)
	m.mu.Lock()
	var victims []*managedTab
	for k, mt := range m.tabs {
		if k.owner != ownerSessionID || mt.persist {
			continue
		}
		if mt.kind.Kind != KindHeadless && mt.kind.Kind != KindTern && mt.kind.Kind != KindWebKit {
			continue
		}
		if mt.tab.Info().Adopted {
			continue
		}
		if mt.lastActivityAt.Before(cutoff) {
			victims = append(victims, mt)
			delete(m.tabs, k)
		}
	}
	m.mu.Unlock()
	for _, mt := range victims {
		_ = mt.tab.Close(ctx, false)
	}
	return len(victims)
}

// FreezeIdleTabsForOwner freezes non-persisted headless tabs owned by
// ownerSessionID when a turn ends.
func (m *Manager) FreezeIdleTabsForOwner(ctx context.Context, ownerSessionID string, settings Settings) {
	if ownerSessionID == "" || !settings.FreezeOnTurnEnd {
		return
	}
	m.mu.Lock()
	var targets []*managedTab
	for k, mt := range m.tabs {
		if k.owner == ownerSessionID && !mt.persist && !mt.frozen && mt.kind.Kind == KindHeadless {
			targets = append(targets, mt)
		}
	}
	m.mu.Unlock()
	for _, mt := range targets {
		if f, ok := mt.tab.(Freezer); ok {
			if err := f.Freeze(ctx); err == nil {
				m.mu.Lock()
				mt.frozen = true
				m.mu.Unlock()
			}
		}
	}
}

// resolveInitScriptSources stats every entry against cwd; existing regular files
// are read as UTF-8, and any other entry is kept verbatim as inline JS source.
// upstream: resolveInitScriptSources in open-options.ts:6-20
func resolveInitScriptSources(entries []string, cwd string) ([]string, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		candidate := resolveToCwd(entry, cwd)
		if st, err := os.Stat(candidate); err == nil && st.Mode().IsRegular() {
			data, rerr := os.ReadFile(candidate)
			if rerr != nil {
				return nil, fmt.Errorf("failed to read init script %q: %w", entry, rerr)
			}
			out = append(out, string(data))
			continue
		}
		out = append(out, entry)
	}
	return out, nil
}

func unmarshalJSON(raw json.RawMessage, dst any) error {
	if len(raw) == 0 {
		return fmt.Errorf("empty json")
	}
	return json.Unmarshal(raw, dst)
}
