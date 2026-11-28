package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"
)

// --- Queries & Waits ---

func (t *cdpTab) queryText(ctx context.Context, selector string, ft frameTarget) (*CallResult, error) {
	if selector == "" {
		v, err := t.evalKitRead(ctx, ft, `(document.body ? (document.body.innerText ?? document.body.textContent ?? "") : "")`)
		return &CallResult{Value: v}, err
	}
	if err := ValidateSelector(selector); err != nil {
		return nil, err
	}
	v, err := t.evalKitRead(ctx, ft, fmt.Sprintf(`(function(s){ const el = globalThis.__ompBrowserKit.queryFirst(s); return el ? (el.innerText ?? el.textContent ?? "") : null; })(%s)`, jsLit(selector)))
	return &CallResult{Value: v}, err
}

func (t *cdpTab) queryHTML(ctx context.Context, selector string, ft frameTarget) (*CallResult, error) {
	if selector == "" {
		v, err := t.evalKitRead(ctx, ft, `(document.body ? document.body.innerHTML : "")`)
		return &CallResult{Value: v}, err
	}
	if err := ValidateSelector(selector); err != nil {
		return nil, err
	}
	v, err := t.evalKitRead(ctx, ft, fmt.Sprintf(`(function(s){ const el = globalThis.__ompBrowserKit.queryFirst(s); return el ? el.innerHTML : null; })(%s)`, jsLit(selector)))
	return &CallResult{Value: v}, err
}

func (t *cdpTab) queryProp(ctx context.Context, selector, prop string, ft frameTarget) (*CallResult, error) {
	if err := ValidateSelector(selector); err != nil {
		return nil, err
	}
	v, err := t.evalKitRead(ctx, ft, fmt.Sprintf(`(function(s, p){ const el = globalThis.__ompBrowserKit.queryFirst(s); return el ? (el[p] ?? "") : null; })(%s, %s)`, jsLit(selector), jsLit(prop)))
	return &CallResult{Value: v}, err
}

func (t *cdpTab) queryAttr(ctx context.Context, selector, attr string, ft frameTarget) (*CallResult, error) {
	if err := ValidateSelector(selector); err != nil {
		return nil, err
	}
	v, err := t.evalKitRead(ctx, ft, fmt.Sprintf(`(function(s, a){ const el = globalThis.__ompBrowserKit.queryFirst(s); return el ? el.getAttribute(a) : null; })(%s, %s)`, jsLit(selector), jsLit(attr)))
	return &CallResult{Value: v}, err
}

func (t *cdpTab) queryCount(ctx context.Context, selector string, ft frameTarget) (*CallResult, error) {
	if err := ValidateSelector(selector); err != nil {
		return nil, err
	}
	v, err := t.evalKitRead(ctx, ft, fmt.Sprintf("globalThis.__ompBrowserKit.queryAll(%s).length", jsLit(selector)))
	return &CallResult{Value: v}, err
}

func (t *cdpTab) queryBox(ctx context.Context, selector string, ft frameTarget) (*CallResult, error) {
	if err := ValidateSelector(selector); err != nil {
		return nil, err
	}
	v, err := t.evalKitRead(ctx, ft, fmt.Sprintf(`(function(s){ const el = globalThis.__ompBrowserKit.queryFirst(s); if (!el) return null; const r = el.getBoundingClientRect(); return {x:r.x,y:r.y,width:r.width,height:r.height}; })(%s)`, jsLit(selector)))
	return &CallResult{Value: v}, err
}

func (t *cdpTab) queryStyles(ctx context.Context, selector string, propArgs []any, ft frameTarget) (*CallResult, error) {
	if err := ValidateSelector(selector); err != nil {
		return nil, err
	}
	props := []string{"display", "visibility", "opacity", "color", "background-color", "font-size", "font-weight", "position", "width", "height"}
	if len(propArgs) > 0 {
		if arr, ok := propArgs[0].([]any); ok && len(arr) > 0 {
			props = props[:0]
			for _, item := range arr {
				if s, ok := item.(string); ok {
					props = append(props, s)
				}
			}
		}
	}
	pJSON, _ := json.Marshal(props)
	v, err := t.evalKitRead(ctx, ft, fmt.Sprintf(`(function(s, ps){ const el = globalThis.__ompBrowserKit.queryFirst(s); if (!el) return null; const cs = getComputedStyle(el); const out = {}; for (const p of ps) out[p] = cs.getPropertyValue(p); return out; })(%s, %s)`, jsLit(selector), string(pJSON)))
	return &CallResult{Value: v}, err
}

func (t *cdpTab) evalStylesExpr(ctx context.Context, elExpr string, propArgs []any, execCtxID int64) (*CallResult, error) {
	props := []string{"display", "visibility", "opacity", "color", "background-color", "font-size", "font-weight", "position", "width", "height"}
	if len(propArgs) > 0 {
		if arr, ok := propArgs[0].([]any); ok && len(arr) > 0 {
			props = props[:0]
			for _, item := range arr {
				if s, ok := item.(string); ok {
					props = append(props, s)
				}
			}
		}
	}
	pJSON, _ := json.Marshal(props)
	v, err := t.evalValue(ctx, fmt.Sprintf(`(function(el, ps){ const cs = getComputedStyle(el); const out = {}; for (const p of ps) out[p] = cs.getPropertyValue(p); return out; })(%s, %s)`, elExpr, string(pJSON)), execCtxID)
	return &CallResult{Value: v}, err
}

func (t *cdpTab) queryBool(ctx context.Context, selector, fn string, ft frameTarget) (*CallResult, error) {
	if err := ValidateSelector(selector); err != nil {
		return nil, err
	}
	v, err := t.evalKitRead(ctx, ft, fmt.Sprintf(`(function(s){ const el = globalThis.__ompBrowserKit.queryFirst(s); return el ? globalThis.__ompBrowserKit.%s(el) : false; })(%s)`, fn, jsLit(selector)))
	return &CallResult{Value: v}, err
}

func (t *cdpTab) waitForSelector(ctx context.Context, callCtx CallContext, method, selector string, rawOpts any, ft frameTarget) (*CallResult, error) {
	if err := ValidateSelector(selector); err != nil {
		return nil, err
	}
	wopts, err := parseSelectorWaitOptions(rawOpts)
	if err != nil {
		return nil, err
	}
	waitMs := resolveWaitTimeout(callCtx.TimeoutMs, wopts.Timeout)
	if waitMs < 1 {
		waitMs = 1
	}
	timeout := time.Duration(waitMs) * time.Millisecond
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()

	check := func() (bool, int, error) {
		if t.closed.Load() {
			return false, 0, fmt.Errorf("Tab %q was closed", t.name)
		}
		if t.crashed.Load() {
			return false, 0, t.rendererCrashedError()
		}
		if !t.proc.client.Alive() {
			return false, 0, &BrowserDisconnectedError{}
		}
		v, err := t.evalKitRead(waitCtx, ft, fmt.Sprintf(`(function(s, state, vis, hid){
			const all = globalThis.__ompBrowserKit.queryAll(s);
			const el = all[0] || null;
			if (state === "detached") return { ok: !el, count: all.length };
			if (hid) return { ok: !el || !globalThis.__ompBrowserKit.isVisible(el), count: all.length };
			if (!el) return { ok: false, count: 0 };
			if (vis) return { ok: globalThis.__ompBrowserKit.isVisible(el), count: all.length };
			return { ok: true, count: all.length };
		})(%s, %s, %t, %t)`, jsLit(selector), jsLit(wopts.State), wopts.Visible, wopts.Hidden))
		if err != nil {
			if isNavigatedContextError(err) {
				t.invalidateIsolatedWorld(ft.frameID, 0)
				return false, 0, nil
			}
			if errors.Is(err, context.DeadlineExceeded) {
				return false, 0, nil
			}
			return false, 0, err
		}
		m, _ := v.(map[string]any)
		ok, _ := m["ok"].(bool)
		cnt, _ := m["count"].(float64)
		return ok, int(cnt), nil
	}

	label := "tab." + method + "(" + jsLit(selector) + ")"
	resultVal := func(count int) bool {
		if wopts.Hidden || wopts.State == "detached" {
			return count > 0
		}
		return true
	}
	if ok, cnt, err := check(); err != nil {
		return nil, err
	} else if ok {
		return &CallResult{Value: resultVal(cnt)}, nil
	}
	// When no explicit timeout was given and we're waiting for a matching element
	// (not hidden/detached), apply omp's 2s zero-match fail-fast (op-timeouts.ts:29).
	failFastOnZero := !wopts.HasTimeout && !wopts.Hidden && wopts.State != "detached"
	zeroStart := time.Now()
	sawMatch := false
	var lastCount int
	for {
		select {
		case <-waitCtx.Done():
			if !t.proc.client.Alive() {
				return nil, &BrowserDisconnectedError{}
			}
			return nil, fmt.Errorf("%s timed out after %dms%s", label, waitMs, formatSelectorMatchHint(lastCount))
		case <-ticker.C:
			var ok bool
			var err error
			ok, lastCount, err = check()
			if err != nil {
				return nil, err
			}
			if ok {
				return &CallResult{Value: resultVal(lastCount)}, nil
			}
			if lastCount > 0 {
				sawMatch = true
			} else if failFastOnZero && !sawMatch && time.Since(zeroStart) >= time.Duration(zeroMatchFailFastMs)*time.Millisecond {
				return nil, fmt.Errorf("%s failed fast after %dms%s", label, zeroMatchFailFastMs, formatSelectorMatchHint(0))
			}
		}
	}
}

func (t *cdpTab) waitForURL(ctx context.Context, callCtx CallContext, args []any) (*CallResult, error) {
	if len(args) == 0 {
		return nil, errors.New("tab.waitForUrl() requires a pattern")
	}
	matcher, err := ParseURLPattern(args[0])
	if err != nil {
		return nil, err
	}
	budgetBound, _, _ := resolveOpTimeouts(callCtx.TimeoutMs)
	waitMs := budgetBound
	if opts := argMap(args, 1); opts != nil {
		if ms, ok := opts["timeout"].(float64); ok && ms > 0 && !math.IsInf(ms, 0) {
			if int(ms) < waitMs {
				waitMs = int(ms)
			}
		}
	}
	timeout := time.Duration(waitMs) * time.Millisecond
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := t.refreshMeta(waitCtx); err != nil {
			if !isNavigatedContextError(err) && !errors.Is(err, context.DeadlineExceeded) && !t.proc.client.Alive() {
				return nil, &BrowserDisconnectedError{Cause: err}
			}
			if t.crashed.Load() {
				return nil, t.rendererCrashedError()
			}
		}
		u := t.Info().URL
		if matcher(u) {
			return &CallResult{Value: u}, nil
		}
		select {
		case <-waitCtx.Done():
			if !t.proc.client.Alive() {
				return nil, &BrowserDisconnectedError{}
			}
			return nil, fmt.Errorf("tab.waitForUrl() timed out after %dms; the URL is %s", waitMs, u)
		case <-ticker.C:
		}
	}
}

func (t *cdpTab) waitForText(ctx context.Context, callCtx CallContext, needle string, opts map[string]any, ft frameTarget) (*CallResult, error) {
	budgetBound, _, _ := resolveOpTimeouts(callCtx.TimeoutMs)
	waitMs := budgetBound
	var sel string
	var exact bool
	if opts != nil {
		if ms, ok := opts["timeout"].(float64); ok && ms > 0 && !math.IsInf(ms, 0) {
			if int(ms) < waitMs {
				waitMs = int(ms)
			}
		}
		sel, _ = opts["selector"].(string)
		exact, _ = opts["exact"].(bool)
	}
	timeout := time.Duration(waitMs) * time.Millisecond
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()
	for {
		if !t.proc.client.Alive() {
			return nil, &BrowserDisconnectedError{}
		}
		v, err := t.evalKitRead(waitCtx, ft, fmt.Sprintf(`(function(needle, sel, exact){
			const root = sel ? globalThis.__ompBrowserKit.queryFirst(sel) : document.body;
			if (!root) return false;
			const txt = (root.innerText || root.textContent || "");
			return exact ? txt.trim() === needle : txt.includes(needle);
		})(%s, %s, %t)`, jsLit(needle), jsLit(sel), exact))
		if err == nil {
			if b, ok := v.(bool); ok && b {
				return &CallResult{}, nil
			}
		} else if isNavigatedContextError(err) {
			t.invalidateIsolatedWorld(ft.frameID, 0)
		} else if !errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		select {
		case <-waitCtx.Done():
			if !t.proc.client.Alive() {
				return nil, &BrowserDisconnectedError{}
			}
			return nil, fmt.Errorf("tab.waitForText(%s) timed out after %dms", jsLit(needle), waitMs)
		case <-ticker.C:
		}
	}
}
