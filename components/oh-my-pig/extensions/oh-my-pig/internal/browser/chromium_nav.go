package browser

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

func (t *cdpTab) navigate(ctx context.Context, targetURL, waitUntil string, budgetMs int) error {
	if err := t.checkDialogBlocked("tab.goto()"); err != nil {
		return err
	}
	// Drain stale load events.
	for {
		select {
		case <-t.loadEvents:
		default:
			goto drained
		}
	}
drained:
	var resp struct {
		FrameID   string `json:"frameId"`
		ErrorText string `json:"errorText"`
	}
	if err := t.send(ctx, "Page.navigate", map[string]any{"url": targetURL}, &resp); err != nil {
		if t.crashed.Load() {
			return t.rendererCrashedError()
		}
		if errors.Is(err, context.DeadlineExceeded) {
			// Match omp (tab-worker.ts:3148, 4366-4376): send best-effort Page.stopLoading
			// unguarded on navigate timeout so a pending load does not stall later ops.
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			_ = t.send(stopCtx, "Page.stopLoading", nil, nil)
			stopCancel()
			return &NavigationTimeoutError{
				Message: fmt.Sprintf(`tab.goto(%s) timed out after %dms; pending navigation stopped — retry with a longer tool timeout or waitUntil:"domcontentloaded"`, jsLit(targetURL), budgetMs),
			}
		}
		return err
	}
	if resp.ErrorText != "" {
		return fmt.Errorf("navigation to %s failed: %s", targetURL, resp.ErrorText)
	}
	if waitUntil == "" {
		waitUntil = "load"
	}
	if err := t.waitForLifecycle(ctx, waitUntil, targetURL, budgetMs); err != nil {
		return err
	}
	_ = t.refreshMeta(ctx)
	return nil
}

func (t *cdpTab) waitForLifecycle(ctx context.Context, waitUntil, targetURL string, budgetMs int) error {
	want := strings.ToLower(waitUntil)
	if want == "networkidle" {
		want = "networkidle0"
	}
	navTimeoutErr := func() error {
		if t.crashed.Load() {
			return t.rendererCrashedError()
		}
		// Match omp (tab-worker.ts:3148, 4366-4376): best-effort Page.stopLoading on timeout.
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		_ = t.send(stopCtx, "Page.stopLoading", nil, nil)
		stopCancel()
		label := fmt.Sprintf("tab.goto(%s)", jsLit(targetURL))
		if targetURL == "" {
			label = "navigation"
		}
		return &NavigationTimeoutError{
			Message: fmt.Sprintf(`%s timed out after %dms; pending navigation stopped — retry with a longer tool timeout or waitUntil:"domcontentloaded"`, label, budgetMs),
		}
	}
	if want == "load" || want == "domcontentloaded" {
		ticker := time.NewTicker(80 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case ev := <-t.loadEvents:
				if ev == want || (want == "domcontentloaded" && ev == "load") {
					return nil
				}
			case <-ticker.C:
				var rs string
				probeCtx, probeCancel := context.WithTimeout(ctx, 1500*time.Millisecond)
				v, err := t.evalValue(probeCtx, "document.readyState", 0)
				probeCancel()
				if err != nil {
					if t.crashed.Load() {
						return t.rendererCrashedError()
					}
					if !t.proc.client.Alive() {
						return &BrowserDisconnectedError{Cause: err}
					}
				} else if s, ok := v.(string); ok {
					rs = s
				}
				if rs == "complete" || (want == "domcontentloaded" && rs == "interactive") {
					return nil
				}
			case <-ctx.Done():
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					return navTimeoutErr()
				}
				return ctx.Err()
			}
		}
	}
	// networkidle0 (0 in-flight connections for 500ms) / networkidle2 (<= 2 in-flight for 500ms).
	maxAllowed := 0
	if want == "networkidle2" {
		maxAllowed = 2
	}
	const quietDuration = 500 * time.Millisecond
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case ev := <-t.loadEvents:
			if ev == want || ev == "networkidle0" {
				t.mu.Lock()
				inflight := len(t.inflightReqs)
				t.mu.Unlock()
				if inflight <= maxAllowed {
					return nil
				}
			}
		case <-ticker.C:
			var rs string
			probeCtx, probeCancel := context.WithTimeout(ctx, 1500*time.Millisecond)
			v, err := t.evalValue(probeCtx, "document.readyState", 0)
			probeCancel()
			if err != nil {
				if t.crashed.Load() {
					return t.rendererCrashedError()
				}
				if !t.proc.client.Alive() {
					return &BrowserDisconnectedError{Cause: err}
				}
			} else if s, ok := v.(string); ok {
				rs = s
			}
			if rs == "complete" {
				t.mu.Lock()
				inflight := len(t.inflightReqs)
				quietFor := time.Since(t.lastReqChange)
				t.mu.Unlock()
				if inflight <= maxAllowed && quietFor >= quietDuration {
					return nil
				}
			}
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return navTimeoutErr()
			}
			return ctx.Err()
		}
	}
}

func (t *cdpTab) refreshMeta(ctx context.Context) error {
	v, err := t.evalValue(ctx, `({url: location.href, title: document.title, width: window.innerWidth, height: window.innerHeight, dpr: window.devicePixelRatio || 1})`, 0)
	if err != nil {
		return err
	}
	if m, ok := v.(map[string]any); ok {
		t.mu.Lock()
		if u, ok := m["url"].(string); ok && u != "" {
			t.url = u
		}
		if ti, ok := m["title"].(string); ok {
			t.title = ti
		}
		if w, ok := m["width"].(float64); ok && w > 0 {
			t.viewport.Width = int(w)
		}
		if h, ok := m["height"].(float64); ok && h > 0 {
			t.viewport.Height = int(h)
		}
		if dpr, ok := m["dpr"].(float64); ok && dpr > 0 {
			t.viewport.DeviceScaleFactor = dpr
		}
		t.mu.Unlock()
	}
	return nil
}

const (
	requestedNavigationWaitMs = 5000
	mainFramePollMs           = 25
)

func (t *cdpTab) isMainFrameNavigating() bool {
	t.mu.Lock()
	v := t.mainFrameNavigationRequested
	t.mu.Unlock()
	return v
}

// awaitRequestedNavigation waits for a main-frame navigation requested by the
// page (e.g. a link click or script setting location) to commit and finish
// parsing before sending any CDP commands into it.
// While mainFrameNavigationRequested is true, ZERO CDP traffic is sent.
// At the bound (min(5000ms, perOpMs/2)), matches omp (tab-worker.ts:3014-3023,
// navigation.ts:106-141): clears mainFrameNavigationRequested and returns nil
// WITHOUT sending Page.stopLoading, so slow navigations (>5s) are never aborted
// and the caller's own helper/wait timeout governs from there.
func (t *cdpTab) awaitRequestedNavigation(ctx context.Context, perOpMs int, contextLost bool) error {
	if !contextLost && !t.isMainFrameNavigating() {
		return nil
	}
	waitMs := requestedNavigationWaitMs
	if perOpMs > 0 && perOpMs/2 < waitMs {
		waitMs = perOpMs / 2
	}
	if waitMs < 1 {
		waitMs = 1
	}
	deadline := time.Now().Add(time.Duration(waitMs) * time.Millisecond)
	for {
		if t.crashed.Load() {
			return t.rendererCrashedError()
		}
		if t.closed.Load() {
			return fmt.Errorf("Tab %q was closed", t.name)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		remaining := time.Until(deadline)
		if t.isMainFrameNavigating() {
			if remaining <= 0 {
				// Match omp tab-worker.ts:3022: clear flag and let the helper run.
				t.mu.Lock()
				t.mainFrameNavigationRequested = false
				t.mu.Unlock()
				return nil
			}
			sleep := min(time.Duration(mainFramePollMs)*time.Millisecond, remaining)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.crashCh:
				return t.rendererCrashedError()
			case <-time.After(sleep):
			}
			continue
		}
		if remaining <= 0 {
			return nil
		}
		probeCtx, probeCancel := context.WithTimeout(ctx, remaining)
		v, err := t.evalValue(probeCtx, "document.readyState", 0)
		probeCancel()
		state := "loading"
		if err != nil {
			if t.crashed.Load() {
				return t.rendererCrashedError()
			}
			if !t.proc.client.Alive() {
				return &BrowserDisconnectedError{Cause: err}
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// Match omp navigation.ts:133: any non-context-loss error (including probe
			// deadline) leaves the page alone and returns so the helper's own deadline decides.
			if !isNavigatedContextError(err) {
				return nil
			}
		} else if s, ok := v.(string); ok {
			state = s
		}
		if state != "loading" && !t.isMainFrameNavigating() {
			return nil
		}
		remaining = time.Until(deadline)
		if remaining <= 0 {
			if t.isMainFrameNavigating() {
				t.mu.Lock()
				t.mainFrameNavigationRequested = false
				t.mu.Unlock()
			}
			return nil
		}
		sleep := min(time.Duration(mainFramePollMs)*time.Millisecond, remaining)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.crashCh:
			return t.rendererCrashedError()
		case <-time.After(sleep):
		}
	}
}

// isolatedWorldForFrame returns (and caches) the dedicated isolated world
// context for frameID (empty string = main frame) so page scripts cannot spoof
// or hijack __ompBrowserKit or Playwright's ARIA ref table.
// Never silently falls back to the page's main world: returns an error when
// the isolated world cannot be obtained (unless allowDefaultWorldFallbackForTest
// is explicitly enabled by a fake-CDP unit test).
func (t *cdpTab) isolatedWorldForFrame(ctx context.Context, frameID string) (int64, error) {
	if frameID == "" {
		t.mu.Lock()
		frameID = t.mainFrameID
		t.mu.Unlock()
		if frameID == "" {
			frames, err := t.getFrameList(ctx)
			if err != nil {
				if t.allowDefaultWorldFallbackForTest {
					return 0, nil
				}
				return 0, fmt.Errorf("failed to resolve main frame for isolated world: %w", err)
			}
			if len(frames) > 0 {
				frameID = frames[0].ID
				t.mu.Lock()
				t.mainFrameID = frameID
				t.mu.Unlock()
			}
		}
	}
	if frameID == "" {
		if t.allowDefaultWorldFallbackForTest {
			return 0, nil
		}
		return 0, errors.New("failed to resolve frame for isolated world: empty frame tree")
	}
	t.mu.Lock()
	if id := t.isolatedWorlds[frameID]; id > 0 {
		t.mu.Unlock()
		return id, nil
	}
	t.mu.Unlock()
	var resp struct {
		ExecutionContextID int64 `json:"executionContextId"`
	}
	err := t.send(ctx, "Page.createIsolatedWorld", map[string]any{
		"frameId":             frameID,
		"worldName":           kitWorldName,
		"grantUniveralAccess": true,
	}, &resp)
	if err != nil {
		if t.allowDefaultWorldFallbackForTest {
			return 0, nil
		}
		return 0, fmt.Errorf("Page.createIsolatedWorld failed for frame %q: %w", frameID, err)
	}
	if resp.ExecutionContextID <= 0 {
		if t.allowDefaultWorldFallbackForTest {
			return 0, nil
		}
		return 0, fmt.Errorf("Page.createIsolatedWorld returned invalid context id %d for frame %q", resp.ExecutionContextID, frameID)
	}
	t.mu.Lock()
	t.isolatedWorlds[frameID] = resp.ExecutionContextID
	t.mu.Unlock()
	return resp.ExecutionContextID, nil
}

// ensureKit installs pageKitJS in the target frame's isolated world and returns
// the executionContextId where __ompBrowserKit lives. Never installs pageKitJS
// in the page's main world in production.
func (t *cdpTab) ensureKit(ctx context.Context, frameID string, fallbackExecCtxID int64) (int64, error) {
	worldID, err := t.isolatedWorldForFrame(ctx, frameID)
	if err != nil {
		return 0, err
	}
	if worldID == 0 {
		if !t.allowDefaultWorldFallbackForTest {
			return 0, errors.New("isolated world unavailable for browser kit")
		}
		worldID = fallbackExecCtxID
	}
	if worldID > 0 {
		t.mu.Lock()
		installed := t.kitInstalled[worldID]
		t.mu.Unlock()
		if installed {
			return worldID, nil
		}
	}
	if _, err := t.evalValue(ctx, pageKitJS, worldID); err != nil {
		if isNavigatedContextError(err) && ctx.Err() == nil && !t.crashed.Load() {
			t.invalidateIsolatedWorld(frameID, worldID)
			if werr := t.awaitRequestedNavigation(ctx, actionOpTimeoutMs, true); werr != nil {
				return 0, werr
			}
			worldID, err = t.isolatedWorldForFrame(ctx, frameID)
			if err != nil {
				return 0, err
			}
			if worldID == 0 {
				if !t.allowDefaultWorldFallbackForTest {
					return 0, errors.New("isolated world unavailable for browser kit")
				}
				worldID = fallbackExecCtxID
			}
			t.mu.Lock()
			installed := worldID > 0 && t.kitInstalled[worldID]
			t.mu.Unlock()
			if installed {
				return worldID, nil
			}
			_, err = t.evalValue(ctx, pageKitJS, worldID)
		}
		if err != nil {
			return 0, err
		}
	}
	if worldID > 0 {
		t.mu.Lock()
		if t.kitInstalled == nil {
			t.kitInstalled = make(map[int64]bool)
		}
		t.kitInstalled[worldID] = true
		t.mu.Unlock()
	}
	return worldID, nil
}

// isNavigatedContextError reports whether err indicates the execution context
// targeted by an evaluation was destroyed or replaced by a navigation.
// upstream: navigation.ts:91-98
func isNavigatedContextError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "Execution context was destroyed") ||
		strings.Contains(msg, "Cannot find context with specified id") ||
		strings.Contains(msg, "should belong to the same JavaScript world")
}

// invalidateIsolatedWorld drops a stale cached isolated world for frameID
// (or worldID) so the next ensureKit resolves the live document's world.
// When worldID > 0, it ONLY removes entries matching that specific dead worldID
// so it never wipes a newer world ID that Runtime.executionContextCreated
// already registered for the newly committed document.
func (t *cdpTab) invalidateIsolatedWorld(frameID string, worldID int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if worldID > 0 {
		delete(t.kitInstalled, worldID)
		for fid, cid := range t.isolatedWorlds {
			if cid == worldID {
				delete(t.isolatedWorlds, fid)
			}
		}
		return
	}
	key := frameID
	if key == "" {
		key = t.mainFrameID
	}
	if key != "" {
		if oldID, ok := t.isolatedWorlds[key]; ok {
			delete(t.kitInstalled, oldID)
			delete(t.isolatedWorlds, key)
		}
	}
}

// watchContextDestroyed returns a channel that closes if execCtxID is destroyed
// or all contexts are cleared (Runtime.executionContextDestroyed /
// Runtime.executionContextsCleared). Used to bound Runtime.evaluate calls so a
// Chrome-dropped reply on a doomed context never stalls until the call deadline.
func (t *cdpTab) watchContextDestroyed(execCtxID int64) <-chan struct{} {
	if execCtxID <= 0 {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ctxDestroyedWaiters == nil {
		t.ctxDestroyedWaiters = make(map[int64]chan struct{})
	}
	ch, ok := t.ctxDestroyedWaiters[execCtxID]
	if !ok {
		ch = make(chan struct{})
		t.ctxDestroyedWaiters[execCtxID] = ch
	}
	return ch
}

// sendInContext sends a Runtime.evaluate targeting execCtxID and aborts after
// 250ms if Chrome clears/destroys execCtxID and silently drops the CDP reply,
// surfacing the same "Execution context was destroyed" error that Chrome normally
// returns. Never stops loads and never retries on its own.
func (t *cdpTab) sendInContext(ctx context.Context, execCtxID int64, method string, params any, out any) error {
	if execCtxID <= 0 {
		return t.send(ctx, method, params, out)
	}
	destroyed := t.watchContextDestroyed(execCtxID)
	sendCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var ctxLost atomic.Bool
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-destroyed:
			select {
			case <-done:
				return
			case <-time.After(250 * time.Millisecond):
				ctxLost.Store(true)
				cancel()
				return
			}
		case <-done:
			return
		}
	}()
	err := t.send(sendCtx, method, params, out)
	if err != nil && ctx.Err() == nil && !t.crashed.Load() && !t.closed.Load() && ctxLost.Load() {
		return errors.New("Execution context was destroyed")
	}
	return err
}

// evalKitRead runs a strictly READ-ONLY expression in ft's isolated browser-kit
// world. If a navigation destroys the world before evaluation completes (no
// result received and a navigated-context error), it waits behind
// awaitRequestedNavigation and re-reads on the new document (up to 2 retries
// for script-chained hops, never re-running after a successful evaluation).
// Matches omp's read-only #rereadAfterNavigation (tab-worker.ts:3026-3034, 3064).
// NEVER call this for mutating actions — use evalKitAction instead.
func (t *cdpTab) evalKitRead(ctx context.Context, ft frameTarget, expr string) (any, error) {
	const maxReadNavRetries = 2
	for attempt := 0; ; attempt++ {
		worldID, err := t.ensureKit(ctx, ft.frameID, ft.execCtxID)
		if err != nil {
			if !isNavigatedContextError(err) || attempt >= maxReadNavRetries || ctx.Err() != nil || t.crashed.Load() || !t.proc.client.Alive() {
				return nil, err
			}
			t.invalidateIsolatedWorld(ft.frameID, worldID)
			if werr := t.awaitRequestedNavigation(ctx, quickOpTimeoutMs, true); werr != nil {
				return nil, werr
			}
			continue
		}
		v, err := t.evalValue(ctx, expr, worldID)
		if err == nil {
			return v, nil
		}
		if !isNavigatedContextError(err) || attempt >= maxReadNavRetries || ctx.Err() != nil || t.crashed.Load() || !t.proc.client.Alive() {
			return nil, err
		}
		t.invalidateIsolatedWorld(ft.frameID, worldID)
		if werr := t.awaitRequestedNavigation(ctx, quickOpTimeoutMs, true); werr != nil {
			return nil, werr
		}
	}
}

// evalKitAction runs a mutating / side-effecting expression in ft's isolated
// browser-kit world AT MOST ONCE. Only the idempotent ensureKit setup may
// recover from a stale world before the expression is sent; once expr is
// dispatched to Chrome, it is NEVER retried or re-run regardless of navigation
// or context loss (upstream: tab-worker.ts:3086-3091, act() has no reread).
func (t *cdpTab) evalKitAction(ctx context.Context, ft frameTarget, expr string) (any, error) {
	worldID, err := t.ensureKit(ctx, ft.frameID, ft.execCtxID)
	if err != nil {
		return nil, err
	}
	return t.evalValue(ctx, expr, worldID)
}

// evalKitReadObjectID is the read-only object-handle lookup counterpart of
// evalKitRead (used to locate an element/iframe handle before acting on it).
func (t *cdpTab) evalKitReadObjectID(ctx context.Context, ft frameTarget, expr string) (string, error) {
	const maxReadNavRetries = 2
	for attempt := 0; ; attempt++ {
		worldID, err := t.ensureKit(ctx, ft.frameID, ft.execCtxID)
		if err != nil {
			if !isNavigatedContextError(err) || attempt >= maxReadNavRetries || ctx.Err() != nil || t.crashed.Load() || !t.proc.client.Alive() {
				return "", err
			}
			t.invalidateIsolatedWorld(ft.frameID, worldID)
			if werr := t.awaitRequestedNavigation(ctx, quickOpTimeoutMs, true); werr != nil {
				return "", werr
			}
			continue
		}
		objID, err := t.evalObjectID(ctx, expr, worldID)
		if err == nil {
			return objID, nil
		}
		if !isNavigatedContextError(err) || attempt >= maxReadNavRetries || ctx.Err() != nil || t.crashed.Load() || !t.proc.client.Alive() {
			return "", err
		}
		t.invalidateIsolatedWorld(ft.frameID, worldID)
		if werr := t.awaitRequestedNavigation(ctx, quickOpTimeoutMs, true); werr != nil {
			return "", werr
		}
	}
}
func (t *cdpTab) evalValue(ctx context.Context, expr string, execCtxID int64) (any, error) {
	params := map[string]any{
		"expression":    expr,
		"returnByValue": true,
		"awaitPromise":  true,
	}
	if execCtxID > 0 {
		params["contextId"] = execCtxID
	}
	var resp struct {
		Result struct {
			Type        string `json:"type"`
			Subtype     string `json:"subtype"`
			Value       any    `json:"value"`
			Description string `json:"description"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text      string `json:"text"`
			Exception *struct {
				Description string `json:"description"`
				Value       any    `json:"value"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	if err := t.sendInContext(ctx, execCtxID, "Runtime.evaluate", params, &resp); err != nil {
		return nil, err
	}
	if resp.ExceptionDetails != nil {
		msg := resp.ExceptionDetails.Text
		if resp.ExceptionDetails.Exception != nil {
			if resp.ExceptionDetails.Exception.Description != "" {
				msg = resp.ExceptionDetails.Exception.Description
			} else if resp.ExceptionDetails.Exception.Value != nil {
				msg = fmt.Sprint(resp.ExceptionDetails.Exception.Value)
			}
		}
		return nil, errors.New(cleanJSException(msg))
	}
	return resp.Result.Value, nil
}

// cleanJSException strips V8 stack trace lines ("    at ...") and a leading
// "Error: " prefix while preserving the full (potentially multi-line) error
// message from user page code.
func cleanJSException(msg string) string {
	lines := strings.Split(msg, "\n")
	end := len(lines)
	for i, ln := range lines {
		if strings.HasPrefix(ln, "    at ") {
			end = i
			break
		}
	}
	msg = strings.Join(lines[:end], "\n")
	msg = strings.TrimPrefix(msg, "Error: ")
	return strings.TrimSpace(msg)
}

// callFunctionOnValue calls fnDecl with `this` bound to objectID (which lives in
// its owning execution context, e.g. the isolated world) and returns the value.
func (t *cdpTab) callFunctionOnValue(ctx context.Context, objectID, fnDecl string, callArgs []map[string]any) (any, error) {
	params := map[string]any{
		"objectId":            objectID,
		"functionDeclaration": fnDecl,
		"returnByValue":       true,
		"awaitPromise":        true,
	}
	if len(callArgs) > 0 {
		params["arguments"] = callArgs
	}
	var resp struct {
		Result struct {
			Type    string `json:"type"`
			Subtype string `json:"subtype"`
			Value   any    `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text      string `json:"text"`
			Exception *struct {
				Description string `json:"description"`
				Value       any    `json:"value"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	if err := t.send(ctx, "Runtime.callFunctionOn", params, &resp); err != nil {
		return nil, err
	}
	if resp.ExceptionDetails != nil {
		msg := resp.ExceptionDetails.Text
		if resp.ExceptionDetails.Exception != nil {
			if resp.ExceptionDetails.Exception.Description != "" {
				msg = resp.ExceptionDetails.Exception.Description
			} else if resp.ExceptionDetails.Exception.Value != nil {
				msg = fmt.Sprint(resp.ExceptionDetails.Exception.Value)
			}
		}
		return nil, errors.New(cleanJSException(msg))
	}
	return resp.Result.Value, nil
}

func (t *cdpTab) evalObjectID(ctx context.Context, expr string, execCtxID int64) (string, error) {
	params := map[string]any{
		"expression":    expr,
		"returnByValue": false,
		"awaitPromise":  true,
	}
	if execCtxID > 0 {
		params["contextId"] = execCtxID
	}
	var resp struct {
		Result struct {
			Type     string `json:"type"`
			Subtype  string `json:"subtype"`
			ObjectID string `json:"objectId"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text      string `json:"text"`
			Exception *struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	if err := t.sendInContext(ctx, execCtxID, "Runtime.evaluate", params, &resp); err != nil {
		return "", err
	}
	if resp.ExceptionDetails != nil {
		msg := resp.ExceptionDetails.Text
		if resp.ExceptionDetails.Exception != nil && resp.ExceptionDetails.Exception.Description != "" {
			msg = resp.ExceptionDetails.Exception.Description
		}
		return "", errors.New(cleanJSException(msg))
	}
	return resp.Result.ObjectID, nil
}
