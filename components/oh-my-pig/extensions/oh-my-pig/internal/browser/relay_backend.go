package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/relay"
)

// upstream: packages/coding-agent/src/tools/browser/registry.ts:308-356,
//           packages/coding-agent/src/tools/browser/downloads.ts:56-118,246-278

// RelayBackend implements Backend and TargetLister for KindRelay by probing the
// local browser relay endpoint and reusing the existing Chromium CDP tab driver
// over the relay's /json/version + /cdp facade.
// upstream: registry.ts:308-356
type RelayBackend struct {
	chromium *ChromiumBackend
}

// NewRelayBackend creates a RelayBackend backed by the shared ChromiumBackend.
func NewRelayBackend(chromium *ChromiumBackend) *RelayBackend {
	if chromium == nil {
		chromium = NewChromiumBackend()
	}
	return &RelayBackend{chromium: chromium}
}

// Available reports true; relay endpoint readiness is validated in Open / ListTargets
// so explicit user-facing messages from omp's registry.ts reach the caller.
func (b *RelayBackend) Available(_ context.Context) (bool, string) {
	return true, ""
}

// resolveRelayEndpoint probes the relay at opts.Endpoint, optionally requesting
// a restart of an outdated or idle-older relay, and returns the normalized HTTP
// CDP URL, the browser WebSocket URL, and any outdated-extension notice.
// upstream: registry.ts:308-341
func relayAuthHeaders(token string) http.Header {
	h := http.Header{}
	if token != "" {
		h.Set("Authorization", "Bearer "+token)
	}
	return h
}

func resolveRelayEndpoint(ctx context.Context, rawEndpoint string) (cdpURL, wsURL, token, notice string, err error) {
	endpoint := strings.TrimSpace(rawEndpoint)
	if endpoint == "" {
		if endpoints := relay.DiscoverRelayEndpoints(); len(endpoints) > 0 {
			endpoint = endpoints[0]
		}
	}
	if endpoint == "" {
		return "", "", "", "", fmt.Errorf(
			"No local omp browser relay found. Open Chrome with the %s extension, or configure browser.relayUrl.",
			relay.ExtensionName,
		)
	}
	cdpURL, err = NormalizeConnectedCdpURL(endpoint)
	if err != nil {
		return "", "", "", "", err
	}
	token, err = relay.RelayTokenForURL(cdpURL)
	if err != nil {
		return "", "", "", "", fmt.Errorf("Cannot authenticate to browser relay at %s: %w", cdpURL, err)
	}
	outcome, err := relay.WaitForRelayExtension(ctx, cdpURL, nil)
	if err != nil {
		return "", "", "", "", err
	}
	if outcome == relay.RelayOutcomeOutdatedRelay || outcome == relay.RelayOutcomeIdleOlderRelay {
		idleOnly := outcome == relay.RelayOutcomeIdleOlderRelay
		restarted, rerr := relay.RequestRelayRestart(ctx, cdpURL, idleOnly)
		if rerr != nil {
			return "", "", "", "", rerr
		}
		if restarted {
			outcome, err = relay.WaitForRelayExtension(ctx, cdpURL, &relay.WaitOptions{Restarted: true})
			if err != nil {
				return "", "", "", "", err
			}
			// Re-read token after restart because the new leader wrote a fresh secret.
			if newTok, terr := relay.RelayTokenForURL(cdpURL); terr == nil && newTok != "" {
				token = newTok
			}
		}
	}
	switch outcome {
	case relay.RelayOutcomeUnreachable:
		return "", "", "", "", fmt.Errorf(
			"Nothing serves the %s browser relay at %s: Chrome is not running with the %s extension. Open Chrome; if the extension is loaded, run `%s browser-relay install` to register its native host.",
			relay.ExtensionName, cdpURL, relay.ExtensionName, relay.ExtensionName,
		)
	case relay.RelayOutcomeNoExtension:
		return "", "", "", "", fmt.Errorf(
			"The %s browser relay at %s is up, but its browser never said hello. Reload the %s extension in Chrome.",
			relay.ExtensionName, cdpURL, relay.ExtensionName,
		)
	case relay.RelayOutcomeOutdatedRelay:
		return "", "", "", "", fmt.Errorf(
			"The browser relay at %s comes from an older %s. Run `%s browser-relay install` with this %s, then reload the %s extension in Chrome.",
			cdpURL, relay.ExtensionName, relay.ExtensionName, relay.ExtensionName, relay.ExtensionName,
		)
	case relay.RelayOutcomeOutdatedExtension:
		return "", "", "", "", fmt.Errorf(
			"The %s extension is out of date. %s",
			relay.ExtensionName, relay.UpdateRelayExtension,
		)
	}
	wsURL, err = fetchCDPWebSocketURLWithHeaders(ctx, cdpURL, relayAuthHeaders(token))
	if err != nil {
		return "", "", "", "", err
	}
	notice = relay.RelayExtensionNotice(ctx, cdpURL)
	return cdpURL, wsURL, token, notice, nil
}

// ListTargets lists adoptable pages exposed by the browser relay.
// upstream: registry.ts:308-356, attach.ts:615-655
func (b *RelayBackend) ListTargets(ctx context.Context, opts OpenOptions, _ map[string]string) (TargetList, error) {
	cdpURL, _, token, _, err := resolveRelayEndpoint(ctx, opts.Endpoint)
	if err != nil {
		return TargetList{}, err
	}
	targets, text, err := listCDPTargetsWithHeaders(ctx, cdpURL, relayAuthHeaders(token))
	if err != nil {
		return TargetList{}, err
	}
	return TargetList{Targets: targets, Text: text}, nil
}

// Open connects to the browser relay, claims the opened or adopted tab, and returns
// a Tab backed by the shared CDP driver with relay-specific download handling
// (`Page.download*` + `OMP.downloadPath`) and non-closing release for adopted tabs.
// upstream: registry.ts:308-356, tab-supervisor.ts, downloads.ts:56-118,246-278
func (b *RelayBackend) Open(ctx context.Context, opts OpenOptions) (Tab, error) {
	cdpURL, wsURL, token, notice, err := resolveRelayEndpoint(ctx, opts.Endpoint)
	if err != nil {
		return nil, err
	}
	authHdr := relayAuthHeaders(token)

	// Each relay tab gets its own CDP WebSocket connection (mirroring omp's per-tab
	// worker connection `/cdp?claim=<targetId>`), so claims, overlay lifecycle, and
	// closing one tab never disturb another tab or another client on the same tab.
	var claimTargetID string
	if opts.Target != "" {
		// Look up the target ID ahead of dialing `/cdp?claim=` so the bridge clears
		// any foreign-frame refusal and mounts the overlay before attach.
		probeClient, derr := dialCDPWithHeaders(ctx, wsURL, authHdr, relay.DefaultMaxWSMessageBytes)
		if derr != nil {
			return nil, derr
		}
		var listResp struct {
			TargetInfos []struct {
				TargetID string `json:"targetId"`
				Type     string `json:"type"`
				Title    string `json:"title"`
				URL      string `json:"url"`
			} `json:"targetInfos"`
		}
		gerr := probeClient.Send(ctx, "", "Target.getTargets", nil, &listResp)
		_ = probeClient.Close()
		if gerr != nil {
			return nil, fmt.Errorf("Target.getTargets failed: %w", gerr)
		}
		needle := strings.ToLower(strings.TrimSpace(opts.Target))
		for _, ti := range listResp.TargetInfos {
			if ti.Type == "page" && ti.TargetID == opts.Target {
				claimTargetID = ti.TargetID
				break
			}
		}
		if claimTargetID == "" {
			for _, ti := range listResp.TargetInfos {
				if ti.Type == "page" && (strings.Contains(strings.ToLower(ti.URL), needle) || strings.Contains(strings.ToLower(ti.Title), needle)) {
					claimTargetID = ti.TargetID
					break
				}
			}
		}
		if claimTargetID == "" {
			var summary []string
			for _, ti := range listResp.TargetInfos {
				if ti.Type != "page" {
					continue
				}
				title := ti.Title
				if title == "" {
					title = "(untitled)"
				}
				summary = append(summary, fmt.Sprintf("- %s  %s  %s", ti.TargetID, title, ti.URL))
			}
			return nil, fmt.Errorf("No page target matched %q. Available pages:\n%s", opts.Target, strings.Join(summary, "\n"))
		}
	}

	dialURL := wsURL
	if claimTargetID != "" {
		if u, uerr := url.Parse(wsURL); uerr == nil {
			q := u.Query()
			q.Set("claim", claimTargetID)
			u.RawQuery = q.Encode()
			dialURL = u.String()
		}
	}

	client, err := dialCDPWithHeaders(ctx, dialURL, authHdr, relay.DefaultMaxWSMessageBytes)
	if err != nil {
		return nil, err
	}

	proc := &chromiumProcess{
		key:      "relay:" + cdpURL,
		kind:     KindRelay,
		client:   client,
		cdpURL:   cdpURL,
		refCount: 1,
	}

	tabOpts := opts
	tabOpts.Endpoint = cdpURL
	if claimTargetID != "" {
		tabOpts.Target = claimTargetID
	}

	tab, err := openRelayCDPTab(ctx, b.chromium, proc, tabOpts, notice)
	if err != nil {
		if tab == nil {
			_ = client.Close()
		}
		return tab, err
	}
	return tab, nil
}

// relayTab wraps *cdpTab so relay-specific Info() (description + extension notice),
// page-level download events (`Page.downloadWillBegin`/`Page.downloadProgress` +
// `OMP.downloadPath`), and clean per-tab WebSocket teardown work without disturbing
// ChromiumBackend's shared process pool.
type relayTab struct {
	*cdpTab
	cdpURL       string
	notice       string
	persist      bool
	unsubRelayDL func()
	unsubDetach  func()
}

func openRelayCDPTab(ctx context.Context, chromium *ChromiumBackend, proc *chromiumProcess, opts OpenOptions, notice string) (*relayTab, error) {
	var (
		targetID string
		ownsPage bool
		adopted  bool
	)

	if opts.Target != "" {
		targetID = opts.Target
		adopted = true
	} else {
		var createResp struct {
			TargetID string `json:"targetId"`
		}
		// Open default relay tabs in the background so the user's selected tab stays active
		// (upstream: tab-worker.ts:1617-1626, relay/bridge.ts:1886-1889).
		if err := proc.client.Send(ctx, "", "Target.createTarget", map[string]any{
			"url":        "about:blank",
			"background": true,
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

	tabCtx, tabCancel := context.WithCancel(context.Background())
	ct := &cdpTab{
		backend:             chromium,
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
		emulateViewport:     opts.Viewport != nil,
		initialUserAgent:    opts.UserAgent,
		insecure:            false,
		dialogPolicy:        opts.Dialogs,
		dialogWaiters:       make(map[chan cdpDialogState]struct{}),
		allowedDomains:      append([]string(nil), opts.AllowedDomains...),
		fetchSessions:       make(map[string]bool),
		childSessions:       make(map[string]bool),
		childParent:         make(map[string]string),
		openInitScripts:     append([]string(nil), opts.InitScripts...),
		downloadsDir:        "",
		downloadsConfigured: false,
		ownedDownloadsDir:   false,
		pendingDLs:          make(map[string]*cdpPendingDownload),
		dlNotify:            make(chan cdpDownload, 16),
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

	rt := &relayTab{
		cdpTab:  ct,
		cdpURL:  proc.cdpURL,
		notice:  notice,
		persist: opts.Persist,
	}

	ct.unsubTrace = proc.client.AttachTrace(ct.sessionID, ct.trace)
	ct.unsub = proc.client.Subscribe(ct.onCDPEvent)
	rt.unsubRelayDL = proc.client.Subscribe(rt.onRelayEvent)

	if err := ct.configureSession(ctx); err != nil {
		rt.cleanupOnOpenFailure()
		return nil, err
	}

	if opts.URL != "" {
		gotoMs := openNavigationBudgetMs(ctx, opts.TimeoutMs)
		_, err := ct.runWithDialogRace(ctx, fmt.Sprintf("tab.goto(%s)", jsLit(opts.URL)), false, func(runCtx context.Context) (*CallResult, error) {
			navCtx, navCancel := context.WithTimeout(runCtx, time.Duration(gotoMs)*time.Millisecond)
			defer navCancel()
			return &CallResult{}, ct.navigate(navCtx, opts.URL, opts.WaitUntil, gotoMs)
		})
		if err != nil {
			var navTimeout *NavigationTimeoutError
			if errors.As(err, &navTimeout) {
				ct.mu.Lock()
				hasDlg := ct.pendingDialog != nil && ct.pendingDialog.Open
				ct.mu.Unlock()
				if !hasDlg {
					metaCtx, metaCancel := context.WithTimeout(ct.tabCtx, 400*time.Millisecond)
					_ = ct.refreshMeta(metaCtx)
					metaCancel()
				}
				return rt, err
			}
			rt.cleanupOnOpenFailure()
			return nil, err
		}
	} else {
		_ = ct.refreshMeta(ctx)
	}
	ct.started.Store(true)
	return rt, nil
}

func (r *relayTab) cleanupOnOpenFailure() {
	if r.unsubRelayDL != nil {
		r.unsubRelayDL()
		r.unsubRelayDL = nil
	}
	if r.unsubDetach != nil {
		r.unsubDetach()
		r.unsubDetach = nil
	}
	r.cdpTab.closePageOnly()
}

// onRelayEvent handles relay-specific page events: Page.downloadWillBegin and
// Page.downloadProgress (resolving the saved file via OMP.downloadPath), plus
// Target.targetDestroyed / Target.detachedFromTarget so Alive() reflects tabs
// closed underneath in Chrome.
// upstream: downloads.ts:56-60, 246-278
func (r *relayTab) onRelayEvent(ev CDPEvent) {
	if ev.Method == "Target.targetDestroyed" {
		var p struct {
			TargetID string `json:"targetId"`
		}
		if json.Unmarshal(ev.Params, &p) == nil && p.TargetID == r.targetID {
			r.closed.Store(true)
			if r.tabCancel != nil {
				r.tabCancel()
			}
		}
		return
	}
	r.mu.Lock()
	sid := r.sessionID
	r.mu.Unlock()
	if ev.Method == "Target.detachedFromTarget" {
		var p struct {
			SessionID string `json:"sessionId"`
		}
		if json.Unmarshal(ev.Params, &p) == nil && p.SessionID == sid {
			r.closed.Store(true)
			if r.tabCancel != nil {
				r.tabCancel()
			}
		}
		return
	}
	if ev.SessionID != sid {
		return
	}
	switch ev.Method {
	case "Page.downloadWillBegin":
		var p struct {
			GUID              string `json:"guid"`
			URL               string `json:"url"`
			SuggestedFilename string `json:"suggestedFilename"`
		}
		if json.Unmarshal(ev.Params, &p) == nil && p.GUID != "" {
			r.mu.Lock()
			r.pendingDLs[p.GUID] = &cdpPendingDownload{
				guid:              p.GUID,
				url:               p.URL,
				suggestedFilename: p.SuggestedFilename,
			}
			r.mu.Unlock()
		}
	case "Page.downloadProgress":
		var p struct {
			GUID          string `json:"guid"`
			State         string `json:"state"`
			ReceivedBytes int64  `json:"receivedBytes"`
		}
		if json.Unmarshal(ev.Params, &p) == nil && p.GUID != "" {
			r.mu.Lock()
			pend := r.pendingDLs[p.GUID]
			if pend != nil {
				pend.receivedBytes = p.ReceivedBytes
				if p.State == "completed" {
					delete(r.pendingDLs, p.GUID)
					snap := *pend
					r.mu.Unlock()
					go r.completeRelayDownload(snap)
					return
				} else if p.State == "canceled" {
					delete(r.pendingDLs, p.GUID)
				}
			}
			r.mu.Unlock()
		}
	}
}

func (r *relayTab) completeRelayDownload(pend cdpPendingDownload) {
	c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var resp struct {
		FilePath string `json:"filePath"`
	}
	if err := r.send(c, "OMP.downloadPath", map[string]any{"guid": pend.guid}, &resp); err != nil || resp.FilePath == "" {
		return
	}
	// Wait briefly for Chrome's rename into place (upstream: savedFileAppears in downloads.ts:339-349).
	for range 100 {
		if st, err := os.Stat(resp.FilePath); err == nil && !st.IsDir() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	dl := cdpDownload{
		Path:              resp.FilePath,
		SuggestedFilename: pend.suggestedFilename,
		URL:               pend.url,
		Bytes:             pend.receivedBytes,
	}
	r.mu.Lock()
	r.completedDLs = append(r.completedDLs, dl)
	r.mu.Unlock()
	select {
	case r.dlNotify <- dl:
	default:
	}
}

func (r *relayTab) Info() TabInfo {
	info := r.cdpTab.Info()
	info.Description = "relay " + r.cdpURL
	if r.notice != "" {
		info.Notes = append(info.Notes, r.notice)
	}
	return info
}

// Close releases the relay tab. On session teardown (kill=true), a `persist: true`
// relay tab detaches without closing the user's browser tab (upstream:
// tab-supervisor.ts:2037-2050). An explicit `close` call (kill=false) still closes
// an owned tab while leaving borrowed/adopted tabs open.
func (r *relayTab) Close(ctx context.Context, kill bool) error {
	if r.unsubRelayDL != nil {
		r.unsubRelayDL()
		r.unsubRelayDL = nil
	}
	if r.unsubDetach != nil {
		r.unsubDetach()
		r.unsubDetach = nil
	}
	if kill && r.persist {
		r.ownsPage = false
	}
	r.cdpTab.closePageOnly()
	_ = r.proc.client.Close()
	_ = ctx
	return nil
}
