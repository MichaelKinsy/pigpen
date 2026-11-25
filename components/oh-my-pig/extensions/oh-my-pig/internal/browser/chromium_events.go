package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

func (t *cdpTab) onCDPEvent(ev CDPEvent) {
	// Browser-level download events arrive with empty SessionID.
	if ev.SessionID == "" {
		switch ev.Method {
		case "Browser.downloadWillBegin":
			var p struct {
				GUID              string `json:"guid"`
				URL               string `json:"url"`
				SuggestedFilename string `json:"suggestedFilename"`
			}
			if json.Unmarshal(ev.Params, &p) == nil && p.GUID != "" {
				t.mu.Lock()
				t.pendingDLs[p.GUID] = &cdpPendingDownload{
					guid:              p.GUID,
					url:               p.URL,
					suggestedFilename: p.SuggestedFilename,
				}
				t.mu.Unlock()
			}
		case "Browser.downloadProgress":
			var p struct {
				GUID          string `json:"guid"`
				State         string `json:"state"`
				ReceivedBytes int64  `json:"receivedBytes"`
				FilePath      string `json:"filePath"`
			}
			if json.Unmarshal(ev.Params, &p) == nil && p.GUID != "" {
				t.mu.Lock()
				pend := t.pendingDLs[p.GUID]
				if pend != nil {
					pend.receivedBytes = p.ReceivedBytes
					if p.State == "completed" {
						delete(t.pendingDLs, p.GUID)
						fp := p.FilePath
						if fp == "" {
							fp = filepath.Join(t.downloadsDir, pend.suggestedFilename)
						}
						dl := cdpDownload{
							Path:              fp,
							SuggestedFilename: pend.suggestedFilename,
							URL:               pend.url,
							Bytes:             p.ReceivedBytes,
						}
						t.completedDLs = append(t.completedDLs, dl)
						select {
						case t.dlNotify <- dl:
						default:
						}
					} else if p.State == "canceled" {
						delete(t.pendingDLs, p.GUID)
					}
				}
				t.mu.Unlock()
			}
		}
		return
	}
	t.mu.Lock()
	sid := t.sessionID
	isFetchSession := t.fetchSessions[ev.SessionID]
	isChildSession := t.childSessions[ev.SessionID]
	hasAllowlist := len(t.allowedDomains) > 0
	t.mu.Unlock()

	// Handle Fetch.requestPaused on the page session, any overlapping previous
	// session during reattach, and any auto-attached child session (OOPIF/worker).
	if ev.Method == "Fetch.requestPaused" && (ev.SessionID == sid || isFetchSession) {
		t.handleFetchRequestPaused(ev.SessionID, ev.Params)
		return
	}
	// Handle Target.attachedToTarget / Target.detachedFromTarget on the page
	// session and recursively on auto-attached child sessions (nested OOPIFs /
	// workers inside OOPIFs).
	if hasAllowlist && (ev.SessionID == sid || isChildSession) {
		switch ev.Method {
		case "Target.attachedToTarget":
			t.handleChildTargetAttached(ev.SessionID, ev.Params)
			return
		case "Target.detachedFromTarget":
			t.handleChildTargetDetached(ev.Params)
			return
		}
	}

	if ev.SessionID != sid {
		return
	}
	switch ev.Method {
	case "Page.loadEventFired":
		select {
		case t.loadEvents <- "load":
		default:
		}
	case "Page.domContentEventFired":
		select {
		case t.loadEvents <- "domcontentloaded":
		default:
		}
	case "Page.lifecycleEvent":
		var p struct {
			FrameID string `json:"frameId"`
			Name    string `json:"name"`
		}
		if json.Unmarshal(ev.Params, &p) == nil {
			t.mu.Lock()
			mainID := t.mainFrameID
			t.mu.Unlock()
			// Filter lifecycle events to the main frame so a subframe never ends main-frame wait.
			if mainID != "" && p.FrameID != "" && p.FrameID != mainID {
				break
			}
			switch p.Name {
			case "load":
				select {
				case t.loadEvents <- "load":
				default:
				}
			case "DOMContentLoaded":
				select {
				case t.loadEvents <- "domcontentloaded":
				default:
				}
			case "networkIdle":
				select {
				case t.loadEvents <- "networkidle0":
				default:
				}
			case "networkAlmostIdle":
				select {
				case t.loadEvents <- "networkidle2":
				default:
				}
			}
		}
	case "Inspector.targetCrashed":
		t.markCrashed()
	case "Page.frameNavigated":
		var p struct {
			Type  string `json:"type"`
			Frame struct {
				ID       string `json:"id"`
				ParentID string `json:"parentId"`
				URL      string `json:"url"`
			} `json:"frame"`
		}
		if json.Unmarshal(ev.Params, &p) == nil {
			t.mu.Lock()
			if p.Frame.ParentID == "" {
				if p.Frame.ID != "" {
					t.mainFrameID = p.Frame.ID
				}
				t.mainFrameNavigationRequested = false
				t.docGen++
				t.url = p.Frame.URL
				t.pendingDialog = nil
				t.observed = make(map[int]observedElement)
				// Drop stale isolated world for main frame; Runtime.executionContextCreated
				// for the new document's kitWorldName will repopulate it on commit.
				if p.Frame.ID != "" {
					if oldID, ok := t.isolatedWorlds[p.Frame.ID]; ok {
						delete(t.kitInstalled, oldID)
						delete(t.isolatedWorlds, p.Frame.ID)
					}
				}
				// Top-level document was replaced: any previously attached child targets
				// (OOPIFs / workers of the old document) are gone; drop any remaining
				// entries whose parent detach event was not emitted.
				for csid := range t.childSessions {
					delete(t.fetchSessions, csid)
					delete(t.childSessions, csid)
					delete(t.childParent, csid)
				}
			} else if p.Frame.ID != "" {
				if oldID, ok := t.isolatedWorlds[p.Frame.ID]; ok {
					delete(t.kitInstalled, oldID)
					delete(t.isolatedWorlds, p.Frame.ID)
				}
				delete(t.frameContexts, p.Frame.ID)
			}
			t.mu.Unlock()
		}
	case "Page.frameRequestedNavigation":
		var p struct {
			FrameID     string `json:"frameId"`
			Disposition string `json:"disposition"`
		}
		if json.Unmarshal(ev.Params, &p) == nil && p.FrameID != "" {
			// upstream: tab-worker.ts:1997-2000 — record pending main-frame navigation
			// without dropping the world or sending CDP into the commit window.
			if p.Disposition == "" || p.Disposition == "currentTab" {
				t.mu.Lock()
				if t.mainFrameID == "" || p.FrameID == t.mainFrameID {
					t.mainFrameNavigationRequested = true
				}
				t.mu.Unlock()
			}
		}
	case "Page.navigatedWithinDocument", "Page.frameStoppedLoading":
		var p struct {
			FrameID string `json:"frameId"`
		}
		if json.Unmarshal(ev.Params, &p) == nil && p.FrameID != "" {
			// upstream: tab-worker.ts:2002-2004, 2258-2259
			// NOTE: Do NOT clear on Page.frameClearedScheduledNavigation — Chrome emits
			// that right after Page.frameStartedNavigating / Network.requestWillBeSent
			// BEFORE the new document commits, which would disarm the navigation barrier
			// mid-flight and send Runtime.evaluate into a doomed context.
			t.mu.Lock()
			if t.mainFrameID == "" || p.FrameID == t.mainFrameID {
				t.mainFrameNavigationRequested = false
			}
			t.mu.Unlock()
		}
	case "Runtime.executionContextCreated":
		var p struct {
			Context struct {
				ID      int64  `json:"id"`
				Name    string `json:"name"`
				AuxData struct {
					IsDefault bool   `json:"isDefault"`
					FrameID   string `json:"frameId"`
				} `json:"auxData"`
			} `json:"context"`
		}
		if json.Unmarshal(ev.Params, &p) == nil && p.Context.AuxData.FrameID != "" {
			t.mu.Lock()
			if p.Context.AuxData.IsDefault {
				t.frameContexts[p.Context.AuxData.FrameID] = p.Context.ID
			} else if p.Context.Name == kitWorldName {
				// Record the isolated world ID but do NOT set kitInstalled[id] = true
				// here: Chrome also emits Runtime.executionContextCreated for worlds
				// created via Page.createIsolatedWorld (about:blank, adopted tabs),
				// where Page.addScriptToEvaluateOnNewDocument does not run. ensureKit
				// runs idempotent pageKitJS (`if (globalThis.__ompBrowserKit) return`)
				// once per world ID before marking it installed.
				t.isolatedWorlds[p.Context.AuxData.FrameID] = p.Context.ID
			}
			t.mu.Unlock()
		}
	case "Runtime.executionContextDestroyed":
		var p struct {
			ExecutionContextID int64 `json:"executionContextId"`
		}
		if json.Unmarshal(ev.Params, &p) == nil && p.ExecutionContextID > 0 {
			t.mu.Lock()
			delete(t.kitInstalled, p.ExecutionContextID)
			for fid, cid := range t.isolatedWorlds {
				if cid == p.ExecutionContextID {
					delete(t.isolatedWorlds, fid)
				}
			}
			for fid, cid := range t.frameContexts {
				if cid == p.ExecutionContextID {
					delete(t.frameContexts, fid)
				}
			}
			if ch, ok := t.ctxDestroyedWaiters[p.ExecutionContextID]; ok {
				close(ch)
				delete(t.ctxDestroyedWaiters, p.ExecutionContextID)
			}
			t.mu.Unlock()
		}
	case "Runtime.executionContextsCleared":
		t.mu.Lock()
		t.frameContexts = make(map[string]int64)
		t.isolatedWorlds = make(map[string]int64)
		t.kitInstalled = make(map[int64]bool)
		for cid, ch := range t.ctxDestroyedWaiters {
			close(ch)
			delete(t.ctxDestroyedWaiters, cid)
		}
		t.mu.Unlock()
	case "Page.javascriptDialogOpening":
		var p struct {
			Type          string `json:"type"`
			Message       string `json:"message"`
			DefaultPrompt string `json:"defaultPrompt"`
		}
		if json.Unmarshal(ev.Params, &p) == nil {
			t.mu.Lock()
			t.beforeUnloadOpen = p.Type == "beforeunload"
			t.mu.Unlock()
			t.handleDialogOpening(p.Type, p.Message, p.DefaultPrompt)
		}
	case "Page.javascriptDialogClosed":
		var p struct {
			Result bool `json:"result"`
		}
		_ = json.Unmarshal(ev.Params, &p)
		t.mu.Lock()
		if t.beforeUnloadOpen && !p.Result {
			t.mainFrameNavigationRequested = false
		}
		t.beforeUnloadOpen = false
		t.pendingDialog = nil
		t.mu.Unlock()
	case "Runtime.consoleAPICalled":
		var p struct {
			Type string `json:"type"`
			Args []struct {
				Type        string `json:"type"`
				Value       any    `json:"value"`
				Description string `json:"description"`
			} `json:"args"`
			StackTrace *struct {
				CallFrames []struct {
					URL          string `json:"url"`
					LineNumber   int    `json:"lineNumber"`
					ColumnNumber int    `json:"columnNumber"`
				} `json:"callFrames"`
			} `json:"stackTrace"`
		}
		if json.Unmarshal(ev.Params, &p) == nil {
			lvl := mapConsoleLevel(p.Type)
			args := make([]any, 0, len(p.Args))
			parts := make([]string, 0, len(p.Args))
			for _, a := range p.Args {
				if a.Value != nil {
					args = append(args, a.Value)
					parts = append(parts, fmt.Sprint(a.Value))
				} else if a.Description != "" {
					args = append(args, a.Description)
					parts = append(parts, a.Description)
				} else {
					args = append(args, a.Type)
					parts = append(parts, a.Type)
				}
			}
			var loc string
			if p.StackTrace != nil && len(p.StackTrace.CallFrames) > 0 {
				cf := p.StackTrace.CallFrames[0]
				if cf.URL != "" {
					loc = fmt.Sprintf("%s:%d:%d", cf.URL, cf.LineNumber+1, cf.ColumnNumber+1)
				}
			}
			t.mu.Lock()
			t.consoleSeq++
			entry := cdpConsoleEntry{
				Seq:      t.consoleSeq,
				TS:       time.Now().UnixMilli(),
				Type:     "console",
				Level:    lvl,
				Text:     strings.Join(parts, " "),
				Location: loc,
				Args:     args,
			}
			t.consoleEntries = append(t.consoleEntries, entry)
			if len(t.consoleEntries) > 500 {
				t.consoleEntries = t.consoleEntries[len(t.consoleEntries)-500:]
				t.consoleDropped++
			}
			t.mu.Unlock()
		}
	case "Runtime.exceptionThrown":
		var p struct {
			ExceptionDetails struct {
				Text      string `json:"text"`
				URL       string `json:"url"`
				Line      int    `json:"lineNumber"`
				Column    int    `json:"columnNumber"`
				Exception *struct {
					Description string `json:"description"`
				} `json:"exception"`
			} `json:"exceptionDetails"`
		}
		if json.Unmarshal(ev.Params, &p) == nil {
			txt := p.ExceptionDetails.Text
			var stack string
			if p.ExceptionDetails.Exception != nil && p.ExceptionDetails.Exception.Description != "" {
				stack = p.ExceptionDetails.Exception.Description
				if idx := strings.IndexByte(stack, '\n'); idx > 0 {
					txt = stack[:idx]
				} else {
					txt = stack
				}
			}
			var loc string
			if p.ExceptionDetails.URL != "" {
				loc = fmt.Sprintf("%s:%d:%d", p.ExceptionDetails.URL, p.ExceptionDetails.Line+1, p.ExceptionDetails.Column+1)
			}
			t.mu.Lock()
			t.consoleSeq++
			t.errorEntries = append(t.errorEntries, cdpErrorEntry{
				Seq:      t.consoleSeq,
				TS:       time.Now().UnixMilli(),
				Type:     "pageerror",
				Level:    "error",
				Text:     txt,
				Location: loc,
				Stack:    stack,
			})
			if len(t.errorEntries) > 500 {
				t.errorEntries = t.errorEntries[len(t.errorEntries)-500:]
				t.consoleDropped++
			}
			t.mu.Unlock()
		}
	case "Network.requestWillBeSent":
		var p struct {
			RequestID string `json:"requestId"`
			Type      string `json:"type"`
			Request   struct {
				URL      string            `json:"url"`
				Method   string            `json:"method"`
				Headers  map[string]string `json:"headers"`
				PostData string            `json:"postData"`
			} `json:"request"`
		}
		if json.Unmarshal(ev.Params, &p) == nil && p.RequestID != "" {
			t.mu.Lock()
			t.reqSeq++
			rec := &cdpRequestRecord{
				ID:             p.RequestID,
				Seq:            t.reqSeq,
				TS:             time.Now().UnixMilli(),
				Method:         strings.ToUpper(p.Request.Method),
				URL:            p.Request.URL,
				ResourceType:   strings.ToLower(p.Type),
				RequestHeaders: p.Request.Headers,
				Sizes:          map[string]int{"requestBody": len(p.Request.PostData)},
				PostData:       p.Request.PostData,
			}
			if rec.RequestHeaders == nil {
				rec.RequestHeaders = map[string]string{}
			}
			t.requests = append(t.requests, rec)
			t.reqByID[rec.ID] = rec
			t.inflightReqs[rec.ID] = time.Now()
			t.lastReqChange = time.Now()
			if len(t.requests) > 200 {
				old := t.requests[0]
				t.requests = t.requests[1:]
				delete(t.reqByID, old.ID)
			}
			t.mu.Unlock()
		}
	case "Network.responseReceived":
		var p struct {
			RequestID string `json:"requestId"`
			Response  struct {
				Status  int               `json:"status"`
				Headers map[string]string `json:"headers"`
			} `json:"response"`
		}
		if json.Unmarshal(ev.Params, &p) == nil {
			t.mu.Lock()
			if rec := t.reqByID[p.RequestID]; rec != nil {
				rec.Status = p.Response.Status
				ok := p.Response.Status >= 200 && p.Response.Status < 300
				rec.OK = &ok
				rec.ResponseHeaders = p.Response.Headers
				rec.DurationMs = time.Now().UnixMilli() - rec.TS
			}
			t.mu.Unlock()
		}
	case "Network.loadingFinished":
		var p struct {
			RequestID string `json:"requestId"`
		}
		if json.Unmarshal(ev.Params, &p) == nil && p.RequestID != "" {
			t.mu.Lock()
			delete(t.inflightReqs, p.RequestID)
			t.lastReqChange = time.Now()
			t.mu.Unlock()
		}
	case "Network.loadingFailed":
		var p struct {
			RequestID string `json:"requestId"`
			ErrorText string `json:"errorText"`
		}
		if json.Unmarshal(ev.Params, &p) == nil {
			t.mu.Lock()
			delete(t.inflightReqs, p.RequestID)
			t.lastReqChange = time.Now()
			if rec := t.reqByID[p.RequestID]; rec != nil {
				rec.FailureText = p.ErrorText
				rec.DurationMs = time.Now().UnixMilli() - rec.TS
				t.consoleSeq++
				t.errorEntries = append(t.errorEntries, cdpErrorEntry{
					Seq:      t.consoleSeq,
					TS:       time.Now().UnixMilli(),
					Type:     "requestfailed",
					Level:    "error",
					Text:     fmt.Sprintf("%s %s failed: %s", rec.Method, rec.URL, p.ErrorText),
					Location: rec.URL,
				})
			}
			t.mu.Unlock()
		}
	case "Fetch.requestPaused":
		t.handleFetchRequestPaused(ev.SessionID, ev.Params)
	}
}

// handleFetchRequestPaused enforces allowed_domains on any guarded session
// (the page session, an overlapping old session during reattach, or an
// auto-attached child session such as a cross-site iframe / OOPIF).
// upstream: network.ts:138-167, 454-471
func (t *cdpTab) handleFetchRequestPaused(sessionID string, params json.RawMessage) {
	var p struct {
		RequestID string `json:"requestId"`
		Request   struct {
			URL string `json:"url"`
		} `json:"request"`
	}
	if json.Unmarshal(params, &p) != nil || p.RequestID == "" {
		return
	}
	t.mu.Lock()
	allowed := MatchAllowedDomain(p.Request.URL, t.allowedDomains)
	t.mu.Unlock()
	go func() {
		c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if !allowed {
			_ = t.proc.client.Send(c, sessionID, "Fetch.failRequest", map[string]any{
				"requestId":   p.RequestID,
				"errorReason": "BlockedByClient",
			}, nil)
		} else {
			_ = t.proc.client.Send(c, sessionID, "Fetch.continueRequest", map[string]any{
				"requestId": p.RequestID,
			}, nil)
		}
	}()
}

// handleChildTargetAttached enforces allowed_domains on auto-attached child
// targets (OOPIFs / cross-site iframes and dedicated workers), matching
// Puppeteer's TargetManager + NetworkManager.addClient + patched WebWorker.js.
// Fetch.enable and recursive Target.setAutoAttach are installed BEFORE
// Runtime.runIfWaitingForDebugger; if interception cannot be enabled on an
// iframe child (for a real failure other than target-closed), we fail closed
// by NOT resuming the child target.
// upstream: network.ts:269, 423; patches/puppeteer-core@25.3.0.patch (WebWorker.js)
func (t *cdpTab) handleChildTargetAttached(parentSessionID string, params json.RawMessage) {
	var p struct {
		SessionID  string `json:"sessionId"`
		TargetInfo struct {
			TargetID string `json:"targetId"`
			Type     string `json:"type"`
			URL      string `json:"url"`
		} `json:"targetInfo"`
		WaitingForDebugger bool `json:"waitingForDebugger"`
	}
	if json.Unmarshal(params, &p) != nil || p.SessionID == "" {
		return
	}
	childSID := p.SessionID
	targetType := p.TargetInfo.Type
	waiting := p.WaitingForDebugger
	childURL := p.TargetInfo.URL

	switch targetType {
	case "iframe", "worker":
		t.mu.Lock()
		if t.fetchSessions == nil {
			t.fetchSessions = make(map[string]bool)
		}
		if t.childSessions == nil {
			t.childSessions = make(map[string]bool)
		}
		if t.childParent == nil {
			t.childParent = make(map[string]string)
		}
		t.fetchSessions[childSID] = true
		t.childSessions[childSID] = true
		t.childParent[childSID] = parentSessionID
		t.mu.Unlock()
	}

	recordChildHoldError := func(step string, err error) {
		loc := childURL
		if loc == "" {
			loc = targetType + ":" + p.TargetInfo.TargetID
		}
		t.mu.Lock()
		t.consoleSeq++
		t.errorEntries = append(t.errorEntries, cdpErrorEntry{
			Seq:      t.consoleSeq,
			TS:       time.Now().UnixMilli(),
			Type:     "interception",
			Level:    "error",
			Text:     fmt.Sprintf("allowed_domains held %s target %q paused because %s failed: %v", targetType, loc, step, err),
			Location: loc,
		})
		if len(t.errorEntries) > 500 {
			t.errorEntries = t.errorEntries[len(t.errorEntries)-500:]
			t.consoleDropped++
		}
		t.mu.Unlock()
	}

	go func() {
		c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		switch targetType {
		case "iframe":
			// Install Fetch.enable BEFORE resuming the OOPIF; fail closed if it fails
			// (leave the paused child unresumed so disallowed requests cannot leak)
			// and record the failure in tab.errors().
			if err := t.proc.client.Send(c, childSID, "Fetch.enable", map[string]any{
				"patterns": []map[string]any{{"urlPattern": "*"}},
			}, nil); err != nil {
				recordChildHoldError("Fetch.enable", err)
				return
			}
			// Recurse for nested cross-site iframes and workers inside this OOPIF.
			if err := t.proc.client.Send(c, childSID, "Target.setAutoAttach", map[string]any{
				"autoAttach":             true,
				"waitForDebuggerOnStart": true,
				"flatten":                true,
			}, nil); err != nil {
				recordChildHoldError("Target.setAutoAttach", err)
				return
			}
			if waiting {
				_ = t.proc.client.Send(c, childSID, "Runtime.runIfWaitingForDebugger", nil, nil)
			}
		case "worker":
			// Dedicated workers: attempt Fetch.enable + recursive auto-attach matching
			// Puppeteer's NetworkManager.addClient (which ignores "wasn't found" /
			// "Not supported" when Chromium routes dedicated worker requests through
			// the owning frame's Fetch session, NetworkManager.js:53-58). Fail closed
			// on any other error and record it in tab.errors().
			if err := t.proc.client.Send(c, childSID, "Fetch.enable", map[string]any{
				"patterns": []map[string]any{{"urlPattern": "*"}},
			}, nil); err != nil && !isUnsupportedChildFetchErr(err) {
				recordChildHoldError("Fetch.enable", err)
				return
			}
			_ = t.proc.client.Send(c, childSID, "Target.setAutoAttach", map[string]any{
				"autoAttach":             true,
				"waitForDebuggerOnStart": true,
				"flatten":                true,
			}, nil)
			if waiting {
				_ = t.proc.client.Send(c, childSID, "Runtime.runIfWaitingForDebugger", nil, nil)
			}
		default:
			// Match Puppeteer TargetManager.js:250-265: service_worker and other
			// non-iframe/non-worker targets are resumed and silently detached so
			// staying attached does not prevent service worker lifecycle destruction.
			if waiting {
				_ = t.proc.client.Send(c, childSID, "Runtime.runIfWaitingForDebugger", nil, nil)
			}
			_ = t.proc.client.Send(c, parentSessionID, "Target.detachFromTarget", map[string]any{
				"sessionId": childSID,
			}, nil)
		}
	}()
}

func (t *cdpTab) handleChildTargetDetached(params json.RawMessage) {
	var p struct {
		SessionID string `json:"sessionId"`
	}
	if json.Unmarshal(params, &p) != nil || p.SessionID == "" {
		return
	}
	t.mu.Lock()
	t.dropChildSubtreeLocked(p.SessionID)
	t.mu.Unlock()
}

// dropChildSubtreeLocked removes rootSID and every nested descendant session
// attached under it (Chrome only emits Target.detachedFromTarget for the
// direct child of the surviving session when an OOPIF subtree tears down).
func (t *cdpTab) dropChildSubtreeLocked(rootSID string) {
	queue := []string{rootSID}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		delete(t.fetchSessions, cur)
		delete(t.childSessions, cur)
		delete(t.childParent, cur)
		for csid, psid := range t.childParent {
			if psid == cur {
				queue = append(queue, csid)
			}
		}
	}
}

// isUnsupportedChildFetchErr mirrors Puppeteer NetworkManager.#canIgnoreError
// (NetworkManager.js:53-58): Chrome rejects Fetch.enable on dedicated worker
// sessions with "'Fetch.enable' wasn't found" because worker fetches are
// already intercepted by the owning document/iframe's Fetch session.
func isUnsupportedChildFetchErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "wasn't found") || strings.Contains(msg, "Not supported")
}

func (t *cdpTab) handleDialogOpening(dlgType, msg, def string) {
	t.mu.Lock()
	policy := t.dialogPolicy
	t.mu.Unlock()

	// Default omp policy: alerts and beforeunload auto-accept; confirms and prompts
	// stay pending for tab.handleDialog() unless policy is set.
	// upstream: dialogs.ts
	auto := false
	accept := true
	switch policy {
	case "accept":
		auto, accept = true, true
	case "dismiss":
		auto, accept = true, false
	default:
		if dlgType == "alert" || dlgType == "beforeunload" {
			auto, accept = true, true
		}
	}
	if auto {
		go func() {
			c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = t.send(c, "Page.handleJavaScriptDialog", map[string]any{"accept": accept}, nil)
		}()
		return
	}
	state := &cdpDialogState{
		Open:    true,
		Type:    dlgType,
		Message: msg,
	}
	if dlgType == "prompt" {
		state.DefaultValue = def
	}
	t.mu.Lock()
	t.pendingDialog = state
	waiters := make([]chan cdpDialogState, 0, len(t.dialogWaiters))
	for ch := range t.dialogWaiters {
		waiters = append(waiters, ch)
	}
	t.mu.Unlock()
	for _, ch := range waiters {
		select {
		case ch <- *state:
		default:
		}
	}
}

func (t *cdpTab) subscribeDialog() (chan cdpDialogState, func()) {
	ch := make(chan cdpDialogState, 1)
	t.mu.Lock()
	t.dialogWaiters[ch] = struct{}{}
	t.mu.Unlock()
	return ch, func() {
		t.mu.Lock()
		delete(t.dialogWaiters, ch)
		t.mu.Unlock()
	}
}

func formatDialogBlocksError(label string, dlg cdpDialogState, when string) error {
	m := dlg.Message
	if len(m) > 80 {
		m = m[:80]
	}
	dlgType := dlg.Type
	if dlgType == "" {
		dlgType = "JavaScript"
	}
	phrase := "is open"
	if when == "opened" {
		phrase = "opened while it ran"
	}
	return fmt.Errorf(
		`%s: a %s(%s) dialog %s and blocks the page until it is answered; call tab.handleDialog({ accept: true }) or tab.handleDialog({ accept: false }), or tab.setDialogs("accept" | "dismiss") to answer every dialog`,
		label, dlgType, jsLit(m), phrase,
	)
}

func mapConsoleLevel(t string) string {
	switch t {
	case "debug", "info", "warn", "error", "log":
		return t
	case "warning":
		return "warn"
	case "assert":
		return "error"
	default:
		return "log"
	}
}

func (t *cdpTab) checkDialogBlocked(label string) error {
	t.mu.Lock()
	dlg := t.pendingDialog
	t.mu.Unlock()
	if dlg != nil && dlg.Open {
		return formatDialogBlocksError(label, *dlg, "is open")
	}
	return nil
}
