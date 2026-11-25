package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// --- ARIA Snapshot, Extract, Screenshot, PDF, Evaluate ---

func (t *cdpTab) captureAriaSnapshot(ctx context.Context, args []any, ft frameTarget) (*CallResult, error) {
	var selector string
	var optsMap map[string]any
	if len(args) > 0 {
		switch v := args[0].(type) {
		case string:
			selector = v
			optsMap = argMap(args, 1)
		case map[string]any:
			optsMap = v
		}
	}
	var opts ariaSnapshotOptions
	if optsMap != nil {
		if d, ok := optsMap["depth"].(float64); ok {
			di := int(d)
			opts.Depth = &di
		}
		opts.Boxes, _ = optsMap["boxes"].(bool)
		opts.Interactive, _ = optsMap["interactive"].(bool)
		opts.Compact, _ = optsMap["compact"].(bool)
		opts.URLs, _ = optsMap["urls"].(bool)
		opts.Diff, _ = optsMap["diff"].(bool)
	}
	script := buildAriaSnapshotEvaluator(selector, opts.Depth, opts.Boxes, opts.URLs)
	v, err := t.evalKitRead(ctx, ft, script)
	if err != nil {
		return nil, err
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("ariaSnapshot returned unexpected payload")
	}
	snap, _ := m["snapshot"].(string)
	hrefs := map[string]string{}
	if hm, ok := m["hrefs"].(map[string]any); ok {
		for k, val := range hm {
			if s, ok := val.(string); ok {
				hrefs[k] = s
			}
		}
	}
	processed := postProcessAriaSnapshot(snap, opts, hrefs)
	if opts.Diff {
		key := fmt.Sprintf("%s|%s|%v|%t|%t|%t|%t", ft.frameID, selector, opts.Depth, opts.Boxes, opts.Interactive, opts.Compact, opts.URLs)
		pageURL := t.Info().URL
		t.mu.Lock()
		if t.ariaBaselines == nil {
			t.ariaBaselines = make(map[string]*ariaSnapshotBaseline)
		}
		diff := diffAriaSnapshot(t.ariaBaselines, key, pageURL, processed)
		t.mu.Unlock()
		return &CallResult{Value: diff}, nil
	}
	return &CallResult{Value: processed}, nil
}

func (t *cdpTab) extractReadable(ctx context.Context, args []any, ft frameTarget) (*CallResult, error) {
	format := "markdown"
	var opts map[string]any
	if len(args) > 0 {
		switch v := args[0].(type) {
		case string:
			format = v
			opts = argMap(args, 1)
		case map[string]any:
			opts = v
		}
	}
	var sel string
	if opts != nil {
		if f, ok := opts["format"].(string); ok && f != "" {
			format = f
		}
		sel, _ = opts["selector"].(string)
	}
	if format != "markdown" && format != "text" {
		return nil, fmt.Errorf(`tab.extract() format must be "markdown" or "text", got %s`, jsLit(format))
	}
	v, err := t.evalKitRead(ctx, ft, fmt.Sprintf(`(function(sel, fmt){
		const root = sel ? globalThis.__ompBrowserKit.queryFirst(sel) : (document.querySelector("article, main, [role=main]") || document.body);
		if (!root) return "";
		if (fmt === "markdown" && globalThis.__ompBrowserKit && globalThis.__ompBrowserKit.htmlToMarkdown) {
			return globalThis.__ompBrowserKit.htmlToMarkdown(root);
		}
		return (root.innerText || root.textContent || "").trim();
	})(%s, %s)`, jsLit(sel), jsLit(format)))
	if err != nil {
		return nil, err
	}
	txt, _ := v.(string)
	if txt == "" {
		return nil, fmt.Errorf("tab.extract(%s) found no readable content on %s", jsLit(format), t.Info().URL)
	}
	return &CallResult{Value: txt}, nil
}

var pdfPaperFormats = map[string][2]float64{
	"letter":  {8.5, 11},
	"legal":   {8.5, 14},
	"tabloid": {11, 17},
	"ledger":  {17, 11},
	"a0":      {33.1, 46.8},
	"a1":      {23.4, 33.1},
	"a2":      {16.54, 23.4},
	"a3":      {11.7, 16.54},
	"a4":      {8.27, 11.7},
	"a5":      {5.83, 8.27},
	"a6":      {4.13, 5.83},
}

func parsePDFDimensionInches(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x / 96.0, true
	case string:
		s := strings.TrimSpace(strings.ToLower(x))
		for _, u := range []struct {
			suf string
			div float64
		}{
			{"in", 1}, {"cm", 2.54}, {"mm", 25.4}, {"px", 96},
		} {
			if strings.HasSuffix(s, u.suf) {
				var n float64
				if _, err := fmt.Sscanf(strings.TrimSpace(strings.TrimSuffix(s, u.suf)), "%f", &n); err == nil {
					return n / u.div, true
				}
			}
		}
		var n float64
		if _, err := fmt.Sscanf(s, "%f", &n); err == nil {
			return n / 96.0, true
		}
	}
	return 0, false
}

func (t *cdpTab) capturePDF(ctx context.Context, callCtx CallContext, opts map[string]any) (*CallResult, error) {
	params := map[string]any{
		"printBackground": true,
	}
	var outPath string
	if opts != nil {
		if b, ok := opts["landscape"].(bool); ok {
			params["landscape"] = b
		}
		if s, ok := opts["scale"].(float64); ok && s > 0 {
			params["scale"] = s
		}
		if b, ok := opts["printBackground"].(bool); ok {
			params["printBackground"] = b
		}
		if pr, ok := opts["pageRanges"].(string); ok && pr != "" {
			params["pageRanges"] = pr
		}
		if f, ok := opts["format"].(string); ok && f != "" {
			if dim, ok := pdfPaperFormats[strings.ToLower(strings.TrimSpace(f))]; ok {
				params["paperWidth"] = dim[0]
				params["paperHeight"] = dim[1]
			}
		}
		if m, ok := opts["margin"].(map[string]any); ok {
			if v, ok := parsePDFDimensionInches(m["top"]); ok {
				params["marginTop"] = v
			}
			if v, ok := parsePDFDimensionInches(m["bottom"]); ok {
				params["marginBottom"] = v
			}
			if v, ok := parsePDFDimensionInches(m["left"]); ok {
				params["marginLeft"] = v
			}
			if v, ok := parsePDFDimensionInches(m["right"]); ok {
				params["marginRight"] = v
			}
		}
		outPath, _ = opts["path"].(string)
	}
	var resp struct {
		Data string `json:"data"`
	}
	if err := t.send(ctx, "Page.printToPDF", params, &resp); err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(resp.Data)
	if err != nil {
		return nil, err
	}
	dest := filepath.Join(os.TempDir(), fmt.Sprintf("omp-page-%d.pdf", time.Now().UnixNano()))
	if outPath != "" {
		dest = resolveToCwd(outPath, callCtx.Cwd)
		_ = os.MkdirAll(filepath.Dir(dest), 0o755)
	}
	if err := os.WriteFile(dest, raw, 0o644); err != nil {
		return nil, err
	}
	return &CallResult{Value: dest}, nil
}

func (t *cdpTab) evaluateUser(ctx context.Context, args []any, execCtxID int64) (*CallResult, error) {
	if len(args) == 0 {
		return nil, errors.New("tab.evaluate() requires a function or expression")
	}
	var expr string
	switch first := args[0].(type) {
	case string:
		// Evaluate as expression in page world.
		expr = fmt.Sprintf("(%s)", first)
	case map[string]any:
		fnSrc, ok := DecodePreludeFn(first)
		if !ok {
			return nil, errors.New("tab.evaluate() requires a function or expression string")
		}
		callArgs := make([]string, 0, len(args)-1)
		for _, a := range args[1:] {
			b, err := json.Marshal(a)
			if err != nil {
				return nil, err
			}
			callArgs = append(callArgs, string(b))
		}
		expr = fmt.Sprintf("(%s)(%s)", fnSrc, strings.Join(callArgs, ", "))
	default:
		return nil, errors.New("tab.evaluate() requires a function or expression string")
	}
	v, err := t.evalValue(ctx, expr, execCtxID)
	return &CallResult{Value: v}, err
}

func (t *cdpTab) evaluateOnElement(ctx context.Context, elExpr string, args []any) (*CallResult, error) {
	if len(args) == 0 {
		return nil, errors.New("element.evaluate() requires a function")
	}
	var fnSrc string
	switch first := args[0].(type) {
	case string:
		fnSrc = first
	case map[string]any:
		fnSrc, _ = DecodePreludeFn(first)
	}
	if fnSrc == "" {
		return nil, errors.New("element.evaluate() requires a function or function expression")
	}
	callArgs := []string{elExpr}
	for _, a := range args[1:] {
		b, err := json.Marshal(a)
		if err != nil {
			return nil, err
		}
		callArgs = append(callArgs, string(b))
	}
	v, err := t.evalValue(ctx, fmt.Sprintf("(%s)(%s)", fnSrc, strings.Join(callArgs, ", ")), 0)
	return &CallResult{Value: v}, err
}

// --- Emulation, Cookies, Storage, Console, Network, Downloads ---

func (t *cdpTab) getCookies(ctx context.Context, opts map[string]any) (*CallResult, error) {
	params := map[string]any{}
	if opts != nil {
		if urls, ok := opts["urls"].([]any); ok && len(urls) > 0 {
			params["urls"] = urls
		}
	}
	var resp struct {
		Cookies []map[string]any `json:"cookies"`
	}
	if err := t.send(ctx, "Network.getCookies", params, &resp); err != nil {
		return nil, err
	}
	return &CallResult{Value: resp.Cookies}, nil
}

func (t *cdpTab) setCookies(ctx context.Context, args []any) (*CallResult, error) {
	var cookies []map[string]any
	for _, a := range args {
		switch v := a.(type) {
		case map[string]any:
			cookies = append(cookies, v)
		case []any:
			for _, item := range v {
				if m, ok := item.(map[string]any); ok {
					cookies = append(cookies, m)
				}
			}
		}
	}
	curURL := t.Info().URL
	for _, c := range cookies {
		if _, hasURL := c["url"]; !hasURL {
			if _, hasDom := c["domain"]; !hasDom && curURL != "" && curURL != "about:blank" {
				c["url"] = curURL
			}
		}
		if err := t.send(ctx, "Network.setCookie", c, nil); err != nil {
			return nil, err
		}
	}
	return &CallResult{}, nil
}

// clearCookies deletes only cookies matching the current page URL (and optional
// names filter), matching omp's clearPageCookies (storage-state.ts:417-432).
func (t *cdpTab) clearCookies(ctx context.Context, opts map[string]any) (*CallResult, error) {
	var wanted map[string]bool
	if opts != nil {
		if rawNames, exists := opts["names"]; exists && rawNames != nil {
			arr, ok := rawNames.([]any)
			if !ok {
				return nil, errors.New("tab.clearCookies() expects names to be an array")
			}
			wanted = make(map[string]bool, len(arr))
			for _, item := range arr {
				s, ok := item.(string)
				if !ok {
					return nil, errors.New("tab.clearCookies() expects names to be an array of strings")
				}
				wanted[s] = true
			}
		}
	}
	_ = t.refreshMeta(ctx)
	curURL := t.Info().URL
	params := map[string]any{}
	if curURL != "" && curURL != "about:blank" {
		params["urls"] = []string{curURL}
	} else {
		// No page URL yet: do not wipe other tabs' cookies.
		return &CallResult{}, nil
	}
	var resp struct {
		Cookies []struct {
			Name   string `json:"name"`
			Domain string `json:"domain"`
			Path   string `json:"path"`
		} `json:"cookies"`
	}
	if err := t.send(ctx, "Network.getCookies", params, &resp); err != nil {
		return nil, err
	}
	for _, c := range resp.Cookies {
		if wanted != nil && !wanted[c.Name] {
			continue
		}
		delParams := map[string]any{"name": c.Name}
		if c.Domain != "" {
			delParams["domain"] = c.Domain
		}
		if c.Path != "" {
			delParams["path"] = c.Path
		}
		if err := t.send(ctx, "Network.deleteCookies", delParams, nil); err != nil {
			return nil, err
		}
	}
	return &CallResult{}, nil
}

func resolveStorageKind(args []any) (string, error) {
	kind := argString(args, 0)
	if kind == "" {
		return "", errors.New(`storage kind must be "local" or "session"`)
	}
	switch kind {
	case "local":
		return "localStorage", nil
	case "session":
		return "sessionStorage", nil
	default:
		return "", fmt.Errorf(`storage kind must be "local" or "session", got %s`, jsLit(kind))
	}
}

func (t *cdpTab) readStorage(ctx context.Context, args []any, execCtxID int64) (*CallResult, error) {
	if len(args) == 0 {
		return nil, errors.New(`tab.storage() requires "local" or "session"`)
	}
	storeObj, err := resolveStorageKind(args)
	if err != nil {
		return nil, err
	}
	var key string
	hasKey := false
	if len(args) > 1 && args[1] != nil {
		switch v := args[1].(type) {
		case string:
			key, hasKey = v, true
		case map[string]any:
			if rawK, exists := v["key"]; exists && rawK != nil {
				ks, ok := rawK.(string)
				if !ok {
					return nil, errors.New("tab.storage() expects key to be a string")
				}
				key, hasKey = ks, true
			}
		default:
			return nil, errors.New("tab.storage() expects { key?: string }")
		}
	}
	if hasKey {
		v, err := t.evalValue(ctx, fmt.Sprintf("%s.getItem(%s)", storeObj, jsLit(key)), execCtxID)
		return &CallResult{Value: v}, err
	}
	v, err := t.evalValue(ctx, fmt.Sprintf(`(function(s){ const o = {}; for (let i = 0; i < s.length; i++) { const k = s.key(i); o[k] = s.getItem(k); } return o; })(%s)`, storeObj), execCtxID)
	return &CallResult{Value: v}, err
}

func (t *cdpTab) writeStorage(ctx context.Context, args []any, execCtxID int64) (*CallResult, error) {
	if len(args) < 2 {
		return nil, errors.New(`tab.setStorage() requires a storage kind ("local" or "session") and a key/value or entries object`)
	}
	storeObj, err := resolveStorageKind(args)
	if err != nil {
		return nil, err
	}
	if m, ok := args[1].(map[string]any); ok {
		encoded, _ := json.Marshal(m)
		_, err := t.evalValue(ctx, fmt.Sprintf(`(function(s, obj){ for (const [k, v] of Object.entries(obj)) s.setItem(k, String(v)); })(%s, %s)`, storeObj, string(encoded)), execCtxID)
		return &CallResult{}, err
	}
	k, ok := args[1].(string)
	if !ok {
		return nil, errors.New("tab.setStorage() expects a key string or entries object")
	}
	if len(args) < 3 {
		return nil, errors.New("tab.setStorage(kind, key, value) requires a value")
	}
	var val string
	if s, ok := args[2].(string); ok {
		val = s
	} else {
		val = fmt.Sprint(args[2])
	}
	_, err = t.evalValue(ctx, fmt.Sprintf("%s.setItem(%s, %s)", storeObj, jsLit(k), jsLit(val)), execCtxID)
	return &CallResult{}, err
}

func (t *cdpTab) clearStorage(ctx context.Context, args []any, execCtxID int64) (*CallResult, error) {
	if len(args) == 0 {
		return nil, errors.New(`tab.clearStorage() requires "local" or "session"`)
	}
	storeObj, err := resolveStorageKind(args)
	if err != nil {
		return nil, err
	}
	_, err = t.evalValue(ctx, storeObj+".clear()", execCtxID)
	return &CallResult{}, err
}

func (t *cdpTab) queryConsole(opts map[string]any) *CallResult {
	t.mu.Lock()
	defer t.mu.Unlock()
	var (
		level string
		since int64
		limit int
		clear bool
	)
	if opts != nil {
		level, _ = opts["level"].(string)
		if s, ok := opts["since"].(float64); ok {
			since = int64(s)
		}
		if l, ok := opts["limit"].(float64); ok {
			limit = int(l)
		}
		clear, _ = opts["clear"].(bool)
	}
	out := make([]cdpConsoleEntry, 0, len(t.consoleEntries))
	for _, e := range t.consoleEntries {
		if e.Seq <= since {
			continue
		}
		if level != "" && e.Level != level {
			continue
		}
		out = append(out, e)
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	res := map[string]any{
		"entries": out,
		"nextSeq": t.consoleSeq,
		"dropped": t.consoleDropped,
	}
	if clear {
		t.consoleEntries = nil
		t.consoleDropped = 0
	}
	return &CallResult{Value: res}
}

func (t *cdpTab) queryErrors(opts map[string]any) *CallResult {
	t.mu.Lock()
	defer t.mu.Unlock()
	var (
		since int64
		limit int
		clear bool
	)
	if opts != nil {
		if s, ok := opts["since"].(float64); ok {
			since = int64(s)
		}
		if l, ok := opts["limit"].(float64); ok {
			limit = int(l)
		}
		clear, _ = opts["clear"].(bool)
	}
	out := make([]cdpErrorEntry, 0, len(t.errorEntries))
	for _, e := range t.errorEntries {
		if e.Seq <= since {
			continue
		}
		out = append(out, e)
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	res := map[string]any{
		"entries": out,
		"nextSeq": t.consoleSeq,
		"dropped": t.consoleDropped,
	}
	if clear {
		t.errorEntries = nil
	}
	return &CallResult{Value: res}
}

func (t *cdpTab) queryRequests(opts map[string]any) *CallResult {
	t.mu.Lock()
	defer t.mu.Unlock()
	var (
		matcher     URLMatcher
		since       int64
		limit       int
		clear       bool
		methodF     string
		typeFilter  map[string]bool
		statusClass string
		statusExact int
		hasStatus   bool
	)
	if opts != nil {
		if f, ok := opts["filter"]; ok && f != nil {
			if m, err := ParseURLPattern(f); err == nil {
				matcher = m
			}
		}
		if s, ok := opts["since"].(float64); ok {
			since = int64(s)
		}
		if l, ok := opts["limit"].(float64); ok {
			limit = int(l)
		}
		clear, _ = opts["clear"].(bool)
		if m, ok := opts["method"].(string); ok {
			methodF = strings.ToUpper(m)
		}
		switch tv := opts["type"].(type) {
		case string:
			if tv != "" {
				typeFilter = map[string]bool{strings.ToLower(tv): true}
			}
		case []any:
			typeFilter = map[string]bool{}
			for _, item := range tv {
				if s, ok := item.(string); ok && s != "" {
					typeFilter[strings.ToLower(s)] = true
				}
			}
		}
		switch sv := opts["status"].(type) {
		case float64:
			statusExact = int(sv)
			hasStatus = true
		case string:
			s := strings.TrimSpace(strings.ToLower(sv))
			if s != "" {
				statusClass = s
				hasStatus = true
			}
		}
	}
	out := make([]cdpRequestRecord, 0, len(t.requests))
	for _, r := range t.requests {
		if since > 0 && r.TS < since {
			continue
		}
		if matcher != nil && !matcher(r.URL) {
			continue
		}
		if methodF != "" && r.Method != methodF {
			continue
		}
		if len(typeFilter) > 0 && !typeFilter[r.ResourceType] {
			continue
		}
		if hasStatus {
			if statusClass != "" {
				if strings.HasSuffix(statusClass, "xx") && len(statusClass) == 3 {
					digit := int(statusClass[0] - '0')
					if r.Status/100 != digit {
						continue
					}
				} else if statusClass == "error" || statusClass == "failed" {
					if r.FailureText == "" && r.Status < 400 {
						continue
					}
				}
			} else if r.Status != statusExact {
				continue
			}
		}
		cp := *r
		cp.RequestHeaders = RedactSensitiveHeaders(r.RequestHeaders)
		if r.ResponseHeaders != nil {
			cp.ResponseHeaders = RedactSensitiveHeaders(r.ResponseHeaders)
		}
		out = append(out, cp)
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	if clear {
		t.requests = nil
		t.reqByID = make(map[string]*cdpRequestRecord)
	}
	return &CallResult{Value: out}
}

func (t *cdpTab) getRequestDetail(ctx context.Context, id string) (*CallResult, error) {
	t.mu.Lock()
	rec := t.reqByID[id]
	t.mu.Unlock()
	if rec == nil {
		return nil, fmt.Errorf("no recorded request with id %q", id)
	}
	detail := map[string]any{
		"id":              rec.ID,
		"seq":             rec.Seq,
		"ts":              rec.TS,
		"method":          rec.Method,
		"url":             rec.URL,
		"resourceType":    rec.ResourceType,
		"requestHeaders":  rec.RequestHeaders,
		"responseHeaders": rec.ResponseHeaders,
		"sizes":           rec.Sizes,
	}
	if rec.Status > 0 {
		detail["status"] = rec.Status
	}
	if rec.OK != nil {
		detail["ok"] = *rec.OK
	}
	var bodyResp struct {
		Body          string `json:"body"`
		Base64Encoded bool   `json:"base64Encoded"`
	}
	if err := t.send(ctx, "Network.getResponseBody", map[string]any{"requestId": id}, &bodyResp); err == nil {
		if bodyResp.Base64Encoded {
			detail["body"] = map[string]string{"base64": bodyResp.Body}
		} else {
			detail["body"] = bodyResp.Body
		}
	}
	return &CallResult{Value: detail}, nil
}

func (t *cdpTab) waitForDownload(ctx context.Context, callCtx CallContext, opts map[string]any) (*CallResult, error) {
	if t.closed.Load() || (t.tabCtx != nil && t.tabCtx.Err() != nil) {
		return nil, fmt.Errorf("Tab %q was closed", t.name)
	}
	if t.crashed.Load() {
		return nil, t.rendererCrashedError()
	}
	_ = t.enableDownloads(ctx, t.downloadsDir)
	budgetBound, _, _ := resolveOpTimeouts(callCtx.TimeoutMs)
	waitMs := budgetBound
	if opts != nil {
		if ms, ok := opts["timeout"].(float64); ok && ms > 0 && !math.IsInf(ms, 0) {
			if int(ms) < waitMs {
				waitMs = int(ms)
			}
		}
	}
	timeout := time.Duration(waitMs) * time.Millisecond
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var tabDone <-chan struct{}
	if t.tabCtx != nil {
		tabDone = t.tabCtx.Done()
	}
	select {
	case dl := <-t.dlNotify:
		return &CallResult{Value: dl}, nil
	case <-t.crashCh:
		return nil, t.rendererCrashedError()
	case <-tabDone:
		return nil, fmt.Errorf("Tab %q was closed", t.name)
	case <-waitCtx.Done():
		if ctx.Err() != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("tab.waitForDownload() timed out after %dms", waitMs)
	}
}

func sanitizeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "tab"
	}
	return b.String()
}
