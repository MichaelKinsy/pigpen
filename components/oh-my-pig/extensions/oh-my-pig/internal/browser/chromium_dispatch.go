package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// frameTarget identifies which frame a call operates in and its viewport offset
// (in main-frame CSS pixels) so pointer inputs inside iframes land at the right
// coordinates.
type frameTarget struct {
	frameID   string
	execCtxID int64 // default world for user evaluate (0 = main frame default)
	offsetX   float64
	offsetY   float64
}

// Call dispatches a validated 1- or 2-step tab helper chain over CDP.
func (t *cdpTab) Call(ctx context.Context, callCtx CallContext, chain []CallStep) (*CallResult, error) {
	if len(chain) == 0 {
		return nil, errors.New("Action 'call' requires a non-empty 'chain'.")
	}
	if len(chain) == 2 {
		switch chain[0].Method {
		case "id":
			return t.callElementHandle(ctx, callCtx, "id", chain[0].Args, chain[1])
		case "ref":
			return t.callElementHandle(ctx, callCtx, "ref", chain[0].Args, chain[1])
		case "frame":
			return t.callFrameHandle(ctx, callCtx, chain[0].Args, chain[1])
		}
	}
	return t.callDirect(ctx, callCtx, chain[0], frameTarget{})
}

func decodeArgs(raw []json.RawMessage) []any {
	out := make([]any, len(raw))
	for i, r := range raw {
		var v any
		_ = json.Unmarshal(r, &v)
		out[i] = v
	}
	return out
}

func argString(args []any, idx int) string {
	if idx >= len(args) {
		return ""
	}
	s, _ := args[idx].(string)
	return s
}

func argMap(args []any, idx int) map[string]any {
	if idx >= len(args) {
		return nil
	}
	m, _ := args[idx].(map[string]any)
	return m
}

func argFloat(args []any, idx int) (float64, bool) {
	if idx >= len(args) {
		return 0, false
	}
	f, ok := args[idx].(float64)
	return f, ok
}

func argInt(args []any, idx int) (int, bool) {
	f, ok := argFloat(args, idx)
	return int(f), ok
}

// runWithDialogRace runs fn while watching for a confirm/prompt dialog opening,
// a renderer crash (`Inspector.targetCrashed`), tab close, or call deadline.
// If answersDialog is true (click, dblclick, press), opening a dialog completes
// the action cleanly; otherwise it fails at once with omp's dialog-blocks error
// (upstream: dialogs.ts:31-36, tab-worker.ts:2038-2052, 3225, 3281).
func (t *cdpTab) runWithDialogRace(ctx context.Context, label string, answersDialog bool, fn func(context.Context) (*CallResult, error)) (*CallResult, error) {
	if t.crashed.Load() {
		return nil, t.rendererCrashedError()
	}
	if t.closed.Load() {
		return nil, fmt.Errorf("Tab %q was closed", t.name)
	}
	dlgCh, unsub := t.subscribeDialog()
	defer unsub()

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	type outcome struct {
		res *CallResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- outcome{err: fmt.Errorf("browser internal panic in %s: %v", label, r)}
			}
		}()
		res, err := fn(runCtx)
		done <- outcome{res: res, err: err}
	}()

	var tabDone <-chan struct{}
	if t.tabCtx != nil {
		tabDone = t.tabCtx.Done()
	}

	select {
	case out := <-done:
		return out.res, out.err
	case <-t.crashCh:
		cancel()
		return nil, t.rendererCrashedError()
	case dlg := <-dlgCh:
		cancel()
		if answersDialog {
			return &CallResult{}, nil
		}
		return nil, formatDialogBlocksError(label, dlg, "opened")
	case <-tabDone:
		cancel()
		return nil, fmt.Errorf("Tab %q was closed", t.name)
	case <-ctx.Done():
		cancel()
		return nil, ctx.Err()
	}
}

// methodAwaitsNavigation reports whether method is a document read, selector
// action, or content wait that upstream runs behind `#awaitRequestedNavigation`
// (upstream: tab-worker.ts:3058-3098). Event waits (waitForUrl, waitForDownload),
// explicit navigations (goto, reload, back, forward), dialogs, and passive
// queries bypass the barrier.
func methodAwaitsNavigation(method string) bool {
	switch method {
	case "url", "title", "frames", "observe", "ariaSnapshot", "extract", "screenshot", "pdf",
		"text", "html", "value", "attr", "count", "box", "styles",
		"isVisible", "isEnabled", "isChecked",
		"click", "dblclick", "hover", "focus", "check", "uncheck",
		"fill", "type", "press", "scroll", "drag", "scrollIntoView",
		"highlight", "select", "uploadFile",
		"waitFor", "waitForSelector", "waitForText", "evaluate",
		"storage", "setStorage", "clearStorage", "pushState":
		return true
	default:
		return false
	}
}

// clickRefusedError carries the last failed actionability check when a click
// op hits its per-op timeout so perOpTimeoutError emits omp's exact hint:
// `; the element never became clickable (last check: ...)` (upstream: interactions.ts:165, tab-worker.ts:2921, 2933-2934).
type clickRefusedError struct {
	refusal string
}

func (e *clickRefusedError) Error() string {
	return e.refusal
}

// typingInterruptedError carries partial typing progress when an action hits
// its per-op timeout after input already reached the page (upstream: interactions.ts:178, tab-worker.ts:2923, 2932).
type typingInterruptedError struct {
	progress string
}

func (e *typingInterruptedError) Error() string {
	return e.progress
}

// opLabelAndSelector returns omp's human-readable op label (carrying selector /
// argument where omp does) and the target selector (if the op targets a selector
// whose match count should be probed on per-op timeout).
// upstream: tab-worker.ts:1926-1930, 3133-3828
func opLabelAndSelector(prefix, method string, args []any) (label, selector string) {
	switch method {
	case "click", "dblclick", "hover", "focus", "check", "uncheck",
		"fill", "type", "scrollIntoView", "highlight", "select", "uploadFile",
		"waitFor", "waitForSelector":
		sel := argString(args, 0)
		return fmt.Sprintf("%s%s(%s)", prefix, method, jsLit(sel)), sel
	case "text", "html":
		sel := argString(args, 0)
		if sel == "" {
			sel = "body"
		}
		return fmt.Sprintf("%s%s(%s)", prefix, method, jsLit(sel)), sel
	case "value", "count", "box", "styles", "isVisible", "isEnabled", "isChecked":
		sel := argString(args, 0)
		return fmt.Sprintf("%s%s(%s)", prefix, method, jsLit(sel)), sel
	case "attr":
		sel := argString(args, 0)
		attr := argString(args, 1)
		return fmt.Sprintf("%sattr(%s, %s)", prefix, jsLit(sel), jsLit(attr)), sel
	case "ariaSnapshot":
		if len(args) > 0 {
			if sel, ok := args[0].(string); ok && sel != "" {
				return fmt.Sprintf("%sariaSnapshot(%s)", prefix, jsLit(sel)), ""
			}
		}
		return prefix + "ariaSnapshot()", ""
	case "screenshot":
		if len(args) > 0 {
			if sel, ok := args[0].(string); ok && sel != "" {
				return fmt.Sprintf("%sscreenshot({ selector: %s })", prefix, jsLit(sel)), ""
			}
			if m, ok := args[0].(map[string]any); ok {
				if sel, ok := m["selector"].(string); ok && sel != "" {
					return fmt.Sprintf("%sscreenshot({ selector: %s })", prefix, jsLit(sel)), ""
				}
				if fp, ok := m["fullPage"].(bool); ok && fp {
					return prefix + "screenshot({ fullPage: true })", ""
				}
			}
		}
		return prefix + "screenshot()", ""
	case "extract":
		format := argString(args, 0)
		if format == "" {
			format = "markdown"
		}
		return fmt.Sprintf("%sextract(%s)", prefix, jsLit(format)), ""
	case "goto", "pushState":
		return fmt.Sprintf("%s%s(%s)", prefix, method, jsLit(argString(args, 0))), ""
	case "press", "keyDown", "keyUp":
		return fmt.Sprintf("%s%s(%s)", prefix, method, jsLit(argString(args, 0))), ""
	case "waitForText":
		return fmt.Sprintf("%swaitForText(%s)", prefix, jsLit(argString(args, 0))), ""
	default:
		return prefix + method + "()", ""
	}
}

func (t *cdpTab) callDirect(ctx context.Context, callCtx CallContext, step CallStep, ft frameTarget) (*CallResult, error) {
	args := decodeArgs(step.Args)
	method := step.Method
	prefix := "tab."
	if ft.frameID != "" {
		prefix = "frame."
	}
	label, selector := opLabelAndSelector(prefix, method, args)
	switch method {
	case "dialog":
		t.mu.Lock()
		dlg := t.pendingDialog
		t.mu.Unlock()
		if dlg == nil {
			return &CallResult{Value: cdpDialogState{Open: false}}, nil
		}
		return &CallResult{Value: *dlg}, nil
	case "handleDialog":
		t.mu.Lock()
		pending := t.pendingDialog
		t.mu.Unlock()
		if pending == nil || !pending.Open {
			return nil, errors.New("tab.handleDialog() found no pending confirm or prompt")
		}
		opts := argMap(args, 0)
		accept := true
		promptText := ""
		if opts != nil {
			if b, ok := opts["accept"].(bool); ok {
				accept = b
			}
			if s, ok := opts["promptText"].(string); ok {
				promptText = s
			} else if s, ok := opts["text"].(string); ok {
				promptText = s
			}
		} else if s := argString(args, 0); s != "" {
			promptText = s
		}
		params := map[string]any{"accept": accept}
		if promptText != "" {
			params["promptText"] = promptText
		}
		if err := t.send(ctx, "Page.handleJavaScriptDialog", params, nil); err != nil {
			return nil, err
		}
		t.mu.Lock()
		t.pendingDialog = nil
		t.mu.Unlock()
		return &CallResult{}, nil
	case "setDialogs":
		if len(args) == 0 || args[0] == nil {
			t.mu.Lock()
			t.dialogPolicy = ""
			t.mu.Unlock()
			return &CallResult{}, nil
		}
		pol, ok := args[0].(string)
		if !ok || (pol != "accept" && pol != "dismiss") {
			return nil, fmt.Errorf(`tab.setDialogs() takes "accept", "dismiss" or null (back to the default); got %s`, jsLit(args[0]))
		}
		t.mu.Lock()
		t.dialogPolicy = pol
		pending := t.pendingDialog
		t.mu.Unlock()
		// Also settle any already-pending dialog (upstream: dialogs.ts:113-116).
		if pending != nil && pending.Open {
			if err := t.send(ctx, "Page.handleJavaScriptDialog", map[string]any{"accept": pol == "accept"}, nil); err != nil {
				if !strings.Contains(strings.ToLower(err.Error()), "no dialog") && !strings.Contains(strings.ToLower(err.Error()), "already handled") {
					return nil, err
				}
			}
			t.mu.Lock()
			t.pendingDialog = nil
			t.mu.Unlock()
		}
		return &CallResult{}, nil
	case "console":
		return t.queryConsole(argMap(args, 0)), nil
	case "errors":
		return t.queryErrors(argMap(args, 0)), nil
	case "clearConsole":
		t.mu.Lock()
		t.consoleEntries = nil
		t.errorEntries = nil
		t.consoleDropped = 0
		t.mu.Unlock()
		return &CallResult{}, nil
	case "requests":
		return t.queryRequests(argMap(args, 0)), nil
	case "request":
		if len(args) == 0 || args[0] == nil {
			return nil, errors.New("tab.request() requires a request id")
		}
		var reqID string
		switch v := args[0].(type) {
		case string:
			reqID = v
		case float64:
			reqID = strconv.FormatInt(int64(v), 10)
		default:
			reqID = fmt.Sprint(v)
		}
		return t.getRequestDetail(ctx, reqID)
	case "clearRequests":
		t.mu.Lock()
		t.requests = nil
		t.reqByID = make(map[string]*cdpRequestRecord)
		t.mu.Unlock()
		return &CallResult{}, nil
	case "allowedDomains":
		t.mu.Lock()
		cp := append([]string(nil), t.allowedDomains...)
		t.mu.Unlock()
		if cp == nil {
			cp = []string{}
		}
		return &CallResult{Value: cp}, nil
	case "downloads":
		t.mu.Lock()
		cp := append([]cdpDownload(nil), t.completedDLs...)
		t.mu.Unlock()
		if cp == nil {
			cp = []cdpDownload{}
		}
		return &CallResult{Value: cp}, nil
	case "waitForDownload":
		return t.waitForDownload(ctx, callCtx, argMap(args, 0))
	}

	if err := t.checkDialogBlocked(label); err != nil {
		return nil, err
	}

	answersDialog := method == "click" || method == "dblclick" || method == "clickAt" || method == "press"
	budgetBound, quickOpMs, actionOpMs := resolveOpTimeouts(callCtx.TimeoutMs)
	opMs := opCeilingMs(method, quickOpMs, actionOpMs)
	return t.runWithDialogRace(ctx, label, answersDialog, func(runCtx context.Context) (*CallResult, error) {
		opCtx := runCtx
		var opCancel context.CancelFunc
		if opMs > 0 {
			opCtx, opCancel = context.WithTimeout(runCtx, time.Duration(opMs)*time.Millisecond)
			defer opCancel()
		}
		if ft.frameID == "" && methodAwaitsNavigation(method) {
			waitBudget := opMs
			if waitBudget <= 0 {
				waitBudget = waitBarrierBudgetMs(method, args, budgetBound, callCtx.TimeoutMs)
			}
			if err := t.awaitRequestedNavigation(opCtx, waitBudget, false); err != nil {
				if opMs > 0 && errors.Is(err, context.DeadlineExceeded) && runCtx.Err() == nil {
					return nil, t.perOpTimeoutError(label, selector, opMs, nil, ft)
				}
				return nil, err
			}
		}
		res, err := t.callDirectInner(opCtx, callCtx, step, args, ft)
		if err != nil && opMs > 0 && runCtx.Err() == nil {
			var refused *clickRefusedError
			var interrupted *typingInterruptedError
			if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &refused) || errors.As(err, &interrupted) {
				return nil, t.perOpTimeoutError(label, selector, opMs, err, ft)
			}
		}
		return res, err
	})
}

// matchCountProbeTimeoutMs matches omp's MATCH_COUNT_PROBE_TIMEOUT_MS (tab-worker.ts:381).
const matchCountProbeTimeoutMs = 1000

// selectorMatchCount runs a bounded best-effort count probe for a timed-out
// selector op (matching omp's #selectorMatchCount in tab-worker.ts:2989-3003).
// Returns (count, "", true) on count, (0, failure, true) on probe failure/stall,
// or (0, "", false) for aria-ref / empty selector.
func (t *cdpTab) selectorMatchCount(selector string, ft frameTarget) (count int, failure string, ok bool) {
	if selector == "" || isAriaRefSelector(selector) {
		return 0, "", false
	}
	probeCtx, cancel := context.WithTimeout(context.Background(), matchCountProbeTimeoutMs*time.Millisecond)
	defer cancel()
	worldID, err := t.ensureKit(probeCtx, ft.frameID, ft.execCtxID)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return 0, fmt.Sprintf("no answer within %dms", matchCountProbeTimeoutMs), true
		}
		msg := strings.SplitN(err.Error(), "\n", 2)[0]
		if msg == "" {
			msg = "unknown error"
		}
		return 0, msg, true
	}
	v, err := t.evalValue(probeCtx, fmt.Sprintf("globalThis.__ompBrowserKit.queryAll(%s).length", jsLit(selector)), worldID)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return 0, fmt.Sprintf("no answer within %dms", matchCountProbeTimeoutMs), true
		}
		msg := strings.SplitN(err.Error(), "\n", 2)[0]
		if msg == "" {
			msg = "unknown error"
		}
		return 0, msg, true
	}
	if f, isNum := v.(float64); isNum {
		return int(f), "", true
	}
	return 0, "unknown error", true
}

// isAriaRefSelector reports whether selector addresses an ARIA snapshot ref
// (`aria-ref=...`, `aria-ref/...`, `ariaref/...`).
func isAriaRefSelector(selector string) bool {
	s := strings.TrimSpace(selector)
	return strings.HasPrefix(s, "aria-ref=") || strings.HasPrefix(s, "aria-ref/") || strings.HasPrefix(s, "ariaref/")
}

// perOpTimeoutError converts an expired per-op deadline into omp's named error
// with its selector match-count / refusal / typing-interrupted hint, keeping the
// tab alive (matching omp #runOp in tab-worker.ts:2920-2942; never probes
// DOM.getDocument or closes a busy/navigating tab).
func (t *cdpTab) perOpTimeoutError(label, selector string, opMs int, cause error, ft frameTarget) error {
	if t.crashed.Load() {
		return t.rendererCrashedError()
	}
	if !t.proc.client.Alive() {
		return &BrowserDisconnectedError{}
	}
	var refused *clickRefusedError
	var interrupted *typingInterruptedError
	hasRefusal := cause != nil && errors.As(cause, &refused) && refused.refusal != ""
	hasInterrupted := cause != nil && errors.As(cause, &interrupted) && interrupted.progress != ""

	var count int
	var probeFail string
	var hasProbe bool
	if selector != "" && !hasInterrupted {
		count, probeFail, hasProbe = t.selectorMatchCount(selector, ft)
	}
	var countNote string
	if hasProbe {
		if probeFail == "" {
			countNote = fmt.Sprintf("selector matches %d element(s)", count)
		} else {
			countNote = fmt.Sprintf("could not count matches: %s", probeFail)
		}
	}
	var hint string
	switch {
	case hasInterrupted:
		hint = "; " + interrupted.progress
	case hasRefusal:
		if countNote != "" {
			hint = fmt.Sprintf("; the element never became clickable (last check: %s; %s)", refused.refusal, countNote)
		} else {
			hint = fmt.Sprintf("; the element never became clickable (last check: %s)", refused.refusal)
		}
	case hasProbe && probeFail == "":
		hint = formatSelectorMatchHint(count)
	case countNote != "":
		hint = "; " + countNote
	}
	return fmt.Errorf("%s timed out after %dms%s", label, opMs, hint)
}

// waitBarrierBudgetMs computes the per-op budget passed to awaitRequestedNavigation
// for wait helpers (matching omp tab-worker.ts:3314-3325, 3658-3660, where
// `documentOp(..., w, ...)` passes the wait's own resolved timeout `w`, so
// `awaitRequestedNavigation` waits at most min(5000ms, w/2)).
func waitBarrierBudgetMs(method string, args []any, budgetBound, cellTimeoutMs int) int {
	switch method {
	case "waitFor", "waitForSelector":
		if len(args) > 1 {
			if wopts, err := parseSelectorWaitOptions(args[1]); err == nil && wopts.HasTimeout {
				return resolveWaitTimeout(cellTimeoutMs, wopts.Timeout)
			}
		}
	case "waitForText":
		if opts := argMap(args, 1); opts != nil {
			if ms, ok := opts["timeout"].(float64); ok && ms > 0 {
				return resolveWaitTimeout(cellTimeoutMs, &ms)
			}
		}
	}
	return budgetBound
}

// opCeilingMs returns omp's per-op fail-fast deadline in milliseconds for a
// direct helper (0 = uncapped, uses the full cell budget, e.g. goto/evaluate/waitFor*).
// upstream: op-timeouts.ts:7-15, tab-worker.ts:3133-3455
func opCeilingMs(method string, quickOpMs, actionOpMs int) int {
	switch method {
	case "url", "title", "frames", "observe", "ariaSnapshot", "extract", "screenshot", "pdf",
		"text", "html", "value", "attr", "count", "box", "styles",
		"isVisible", "isEnabled", "isChecked",
		"cookies", "storage", "metrics":
		return quickOpMs
	case "click", "dblclick", "hover", "focus", "check", "uncheck",
		"fill", "type", "press", "keyDown", "keyUp",
		"mouseMove", "mouseDown", "mouseUp", "clickAt", "wheel", "scroll",
		"drag", "scrollIntoView", "highlight", "select", "uploadFile",
		"setCookies", "clearCookies", "setStorage", "clearStorage",
		"emulate", "addInitScript", "removeInitScript", "pushState":
		return actionOpMs
	default:
		return 0
	}
}

func (t *cdpTab) callDirectInner(ctx context.Context, callCtx CallContext, step CallStep, args []any, ft frameTarget) (*CallResult, error) {
	method := step.Method
	budgetBound, _, actionOpMs := resolveOpTimeouts(callCtx.TimeoutMs)

	switch method {
	case "url":
		_ = t.refreshMeta(ctx)
		t.mu.Lock()
		u := t.url
		t.mu.Unlock()
		return &CallResult{Value: u}, nil
	case "title":
		v, err := t.evalValue(ctx, "document.title", ft.execCtxID)
		if err != nil {
			return nil, err
		}
		s, _ := v.(string)
		t.mu.Lock()
		t.title = s
		t.mu.Unlock()
		return &CallResult{Value: s}, nil
	case "goto":
		targetURL := argString(args, 0)
		if targetURL == "" {
			return nil, errors.New("tab.goto() requires a URL string")
		}
		opts := argMap(args, 1)
		waitUntil, _ := opts["waitUntil"].(string)
		navCtx, cancel := context.WithTimeout(ctx, time.Duration(budgetBound)*time.Millisecond)
		defer cancel()
		if err := t.navigate(navCtx, targetURL, waitUntil, budgetBound); err != nil {
			return nil, err
		}
		return &CallResult{}, nil
	case "back", "forward":
		opts := argMap(args, 0)
		waitUntil := "load"
		if opts != nil {
			if w, ok := opts["waitUntil"].(string); ok && w != "" {
				waitUntil = w
			}
		}
		var hist struct {
			CurrentIndex int `json:"currentIndex"`
			Entries      []struct {
				ID int `json:"id"`
			} `json:"entries"`
		}
		if err := t.send(ctx, "Page.getNavigationHistory", nil, &hist); err != nil {
			return nil, err
		}
		next := hist.CurrentIndex - 1
		if method == "forward" {
			next = hist.CurrentIndex + 1
		}
		if next >= 0 && next < len(hist.Entries) {
			if err := t.send(ctx, "Page.navigateToHistoryEntry", map[string]any{"entryId": hist.Entries[next].ID}, nil); err != nil {
				return nil, err
			}
			navCtx, cancel := context.WithTimeout(ctx, time.Duration(budgetBound)*time.Millisecond)
			defer cancel()
			_ = t.waitForLifecycle(navCtx, waitUntil, "", budgetBound)
			_ = t.refreshMeta(ctx)
		}
		return &CallResult{Value: t.Info().URL}, nil
	case "reload":
		opts := argMap(args, 0)
		waitUntil := "load"
		if opts != nil {
			if w, ok := opts["waitUntil"].(string); ok && w != "" {
				waitUntil = w
			}
		}
		if err := t.send(ctx, "Page.reload", nil, nil); err != nil {
			return nil, err
		}
		navCtx, cancel := context.WithTimeout(ctx, time.Duration(budgetBound)*time.Millisecond)
		defer cancel()
		_ = t.waitForLifecycle(navCtx, waitUntil, "", budgetBound)
		_ = t.refreshMeta(ctx)
		return &CallResult{Value: t.Info().URL}, nil
	case "pushState":
		target := argString(args, 0)
		_, err := t.evalValue(ctx, fmt.Sprintf(`(function(u){ history.pushState(history.state, "", u); window.dispatchEvent(new PopStateEvent("popstate", { state: history.state })); return location.href; })(%s)`, jsLit(target)), ft.execCtxID)
		if err != nil {
			return nil, err
		}
		_ = t.refreshMeta(ctx)
		return &CallResult{Value: t.Info().URL}, nil
	case "frames":
		return t.listFrames(ctx)
	case "observe":
		return t.observeAXTree(ctx, argMap(args, 0))
	case "ariaSnapshot":
		return t.captureAriaSnapshot(ctx, args, ft)
	case "extract":
		return t.extractReadable(ctx, args, ft)
	case "screenshot":
		return t.captureScreenshot(ctx, callCtx, args, ft)
	case "pdf":
		return t.capturePDF(ctx, callCtx, argMap(args, 0))
	case "text":
		return t.queryText(ctx, argString(args, 0), ft)
	case "html":
		return t.queryHTML(ctx, argString(args, 0), ft)
	case "value":
		return t.queryProp(ctx, argString(args, 0), "value", ft)
	case "attr":
		sel := argString(args, 0)
		name := argString(args, 1)
		return t.queryAttr(ctx, sel, name, ft)
	case "count":
		return t.queryCount(ctx, argString(args, 0), ft)
	case "box":
		return t.queryBox(ctx, argString(args, 0), ft)
	case "styles":
		var propArgs []any
		if len(args) > 1 {
			propArgs = args[1:]
		}
		return t.queryStyles(ctx, argString(args, 0), propArgs, ft)
	case "isVisible":
		return t.queryBool(ctx, argString(args, 0), "isVisible", ft)
	case "isEnabled":
		return t.queryBool(ctx, argString(args, 0), "isEnabled", ft)
	case "isChecked":
		return t.queryBool(ctx, argString(args, 0), "isChecked", ft)
	case "click", "dblclick":
		sel := argString(args, 0)
		opts := argMap(args, 1)
		count := 1
		if method == "dblclick" {
			count = 2
		}
		if opts != nil {
			if c, ok := opts["count"].(float64); ok && c >= 1 {
				count = int(c)
			}
		}
		btn := "left"
		if opts != nil {
			if b, ok := opts["button"].(string); ok && b != "" {
				btn = b
			}
		}
		return t.clickSelector(ctx, sel, btn, count, actionOpMs, ft)
	case "hover":
		return t.hoverSelector(ctx, argString(args, 0), actionOpMs, ft)
	case "focus":
		return t.focusSelector(ctx, argString(args, 0), actionOpMs, ft)
	case "check":
		return t.setCheckedSelector(ctx, argString(args, 0), true, actionOpMs, ft)
	case "uncheck":
		return t.setCheckedSelector(ctx, argString(args, 0), false, actionOpMs, ft)
	case "fill":
		return t.fillSelector(ctx, argString(args, 0), argString(args, 1), actionOpMs, ft)
	case "type":
		return t.typeSelector(ctx, argString(args, 0), argString(args, 1), actionOpMs, ft)
	case "press":
		return t.pressKeyCombo(ctx, argString(args, 0), args, actionOpMs, ft)
	case "keyDown":
		return t.dispatchSingleKey(ctx, argString(args, 0), true)
	case "keyUp":
		return t.dispatchSingleKey(ctx, argString(args, 0), false)
	case "mouseMove":
		x, _ := argFloat(args, 0)
		y, _ := argFloat(args, 1)
		css := false
		if m := argMap(args, 2); m != nil {
			css, _ = m["css"].(bool)
		}
		vx, vy, err := t.mapPointerPoint(x, y, css)
		if err != nil {
			return nil, err
		}
		return t.dispatchMouseMove(ctx, vx, vy)
	case "mouseDown":
		btn := "left"
		if m := argMap(args, 0); m != nil {
			if b, ok := m["button"].(string); ok && b != "" {
				btn = b
			}
		}
		t.mu.Lock()
		mx, my := t.mouseX, t.mouseY
		t.mu.Unlock()
		return t.dispatchMouseButton(ctx, "mousePressed", mx, my, btn, 1)
	case "mouseUp":
		btn := "left"
		if m := argMap(args, 0); m != nil {
			if b, ok := m["button"].(string); ok && b != "" {
				btn = b
			}
		}
		t.mu.Lock()
		mx, my := t.mouseX, t.mouseY
		t.mu.Unlock()
		return t.dispatchMouseButton(ctx, "mouseReleased", mx, my, btn, 1)
	case "clickAt":
		x, _ := argFloat(args, 0)
		y, _ := argFloat(args, 1)
		btn := "left"
		count := 1
		css := false
		if m := argMap(args, 2); m != nil {
			if b, ok := m["button"].(string); ok && b != "" {
				btn = b
			}
			if c, ok := m["count"].(float64); ok && c >= 1 {
				count = int(c)
			}
			css, _ = m["css"].(bool)
		}
		vx, vy, err := t.mapPointerPoint(x, y, css)
		if err != nil {
			return nil, err
		}
		return t.clickPoint(ctx, vx, vy, btn, count)
	case "wheel":
		dx, dy, _, err := parseScrollDeltas("tab.wheel()", args)
		if err != nil {
			return nil, err
		}
		t.mu.Lock()
		mx, my := t.mouseX, t.mouseY
		t.mu.Unlock()
		return t.dispatchWheel(ctx, mx, my, dx, dy)
	case "scroll":
		return t.scrollPageOrElement(ctx, args, actionOpMs, ft)
	case "drag":
		return t.dragBetween(ctx, args, actionOpMs, ft)
	case "scrollIntoView":
		return t.scrollIntoViewSelector(ctx, argString(args, 0), actionOpMs, ft)
	case "highlight":
		return t.highlightSelector(ctx, argString(args, 0), argMap(args, 1), actionOpMs, ft)
	case "select":
		if len(args) < 2 {
			return nil, errors.New("tab.select() requires a selector and at least one option")
		}
		return t.selectSelector(ctx, argString(args, 0), args[1:], actionOpMs, ft)
	case "uploadFile":
		if len(args) < 2 {
			return nil, errors.New("tab.uploadFile() requires a selector and at least one file path")
		}
		return t.uploadFileSelector(ctx, callCtx, argString(args, 0), args[1:], actionOpMs, ft)
	case "waitFor", "waitForSelector":
		var rawOpts any
		if len(args) > 1 {
			rawOpts = args[1]
		}
		return t.waitForSelector(ctx, callCtx, method, argString(args, 0), rawOpts, ft)
	case "waitForUrl":
		return t.waitForURL(ctx, callCtx, args)
	case "waitForText":
		return t.waitForText(ctx, callCtx, argString(args, 0), argMap(args, 1), ft)
	case "evaluate":
		return t.evaluateUser(ctx, args, ft.execCtxID)
	case "emulate":
		return t.emulate(ctx, argMap(args, 0))
	case "devices":
		return &CallResult{Value: KnownDeviceNames()}, nil
	case "cookies":
		return t.getCookies(ctx, argMap(args, 0))
	case "setCookies":
		return t.setCookies(ctx, args)
	case "clearCookies":
		return t.clearCookies(ctx, argMap(args, 0))
	case "storage":
		return t.readStorage(ctx, args, ft.execCtxID)
	case "setStorage":
		return t.writeStorage(ctx, args, ft.execCtxID)
	case "clearStorage":
		return t.clearStorage(ctx, args, ft.execCtxID)
	case "addInitScript":
		if len(args) == 0 {
			return nil, errors.New("tab.addInitScript(source) requires a string")
		}
		src, ok := args[0].(string)
		if !ok {
			return nil, errors.New("tab.addInitScript(source) requires a string")
		}
		id, err := t.addInitScript(ctx, src)
		if err != nil {
			return nil, err
		}
		return &CallResult{Value: map[string]any{"id": id}}, nil
	case "removeInitScript":
		id := argString(args, 0)
		if id == "" {
			return nil, errors.New("tab.removeInitScript(id) requires a non-empty script id")
		}
		if err := t.send(ctx, "Page.removeScriptToEvaluateOnNewDocument", map[string]any{"identifier": id}, nil); err != nil {
			return nil, err
		}
		t.mu.Lock()
		filtered := t.initScripts[:0]
		for _, s := range t.initScripts {
			if s.ID != id {
				filtered = append(filtered, s)
			}
		}
		t.initScripts = filtered
		t.mu.Unlock()
		return &CallResult{}, nil
	case "initScripts":
		t.mu.Lock()
		cp := append([]cdpInitScript(nil), t.initScripts...)
		t.mu.Unlock()
		return &CallResult{Value: cp}, nil
	case "metrics":
		var resp struct {
			Metrics []struct {
				Name  string  `json:"name"`
				Value float64 `json:"value"`
			} `json:"metrics"`
		}
		_ = t.send(ctx, "Performance.enable", nil, nil)
		if err := t.send(ctx, "Performance.getMetrics", nil, &resp); err != nil {
			return nil, err
		}
		m := make(map[string]float64, len(resp.Metrics))
		for _, item := range resp.Metrics {
			m[item.Name] = item.Value
		}
		return &CallResult{Value: m}, nil
	default:
		return nil, fmt.Errorf("tab.%s() is not yet ported on the Go CDP backend (see internal/browser/UPSTREAM.md)", method)
	}
}

func (t *cdpTab) mapPointerPoint(x, y float64, css bool) (float64, float64, error) {
	t.mu.Lock()
	vp := t.viewport
	vx, vy, err := t.pointer.ToViewport(x, y, css, &vp)
	t.mu.Unlock()
	return vx, vy, err
}
