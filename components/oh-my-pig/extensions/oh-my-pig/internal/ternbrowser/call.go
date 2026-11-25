package ternbrowser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/browser"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/ternwire"
)

// Call executes a 1- or 2-step tab helper chain matching upstream TernTab.
// Every method either implements upstream's protocol behavior (using the page kit
// or native DOM reads + trusted input + typed daemon ops) or returns omp's exact
// upstream refusal error text. No fabricated results.
//
// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts
func (t *Tab) Call(ctx context.Context, callCtx browser.CallContext, chain []browser.CallStep) (*browser.CallResult, error) {
	if len(chain) == 0 {
		return nil, fmt.Errorf("empty call chain")
	}
	if len(chain) == 2 {
		return t.callChained(ctx, callCtx, chain[0], chain[1])
	}
	return t.callSingle(ctx, callCtx, chain[0], "")
}

// callChained handles `id(n).method(...)`, `ref(eN).method(...)`, and `frame(sel).method(...)`.
// Fixes S7: never falls back to clicking a wrong element or running in main frame.
// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:2812-2843,3237-3257,4089-4325
func (t *Tab) callChained(ctx context.Context, callCtx browser.CallContext, head, tail browser.CallStep) (*browser.CallResult, error) {
	switch head.Method {
	case "id":
		id, ok, err := argInt(head.Args, 0)
		if err != nil || !ok {
			return nil, fmt.Errorf("tab.id() requires a numeric element id")
		}
		spec, err := t.resolveIDHandle(ctx, id)
		if err != nil {
			return nil, err
		}
		return t.callElementMethod(ctx, callCtx, spec, "", tail)
	case "ref":
		raw, ok, err := argString(head.Args, 0)
		if err != nil || !ok {
			return nil, fmt.Errorf("tab.ref() requires a ref string")
		}
		spec, err := t.resolveRefHandle(ctx, raw)
		if err != nil {
			return nil, err
		}
		return t.callElementMethod(ctx, callCtx, spec, "", tail)
	case "frame":
		selOrName, ok, err := argString(head.Args, 0)
		if err != nil || !ok {
			return nil, fmt.Errorf("tab.frame() requires a frame selector, name, id, or URL")
		}
		framePath, err := t.resolveFramePath(ctx, selOrName)
		if err != nil {
			return nil, err
		}
		return t.callSingle(ctx, callCtx, tail, framePath)
	default:
		return nil, unsupportedErr(t.product, head.Method+"."+tail.Method, "unsupported handle chain")
	}
}

// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:2816-2825
func (t *Tab) resolveIDHandle(ctx context.Context, id int) (Selector, error) {
	spec := Selector{Engine: "id", ID: id}
	if t.nativeDom() {
		if _, err := t.queryNative(ctx, spec, "", 1, nil, false); err != nil {
			return Selector{}, err
		}
		ref, err := t.observedRef(id)
		if err != nil {
			return Selector{}, err
		}
		return Selector{Engine: "ref", Ref: ref}, nil
	}
	var cnt int
	if err := t.kit(ctx, "count", []any{spec}, "", &cnt); err != nil {
		return Selector{}, err
	}
	return spec, nil
}

// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:2832-2842
func (t *Tab) resolveRefHandle(ctx context.Context, raw string) (Selector, error) {
	ref := ParseAriaRefSelector(raw)
	if ref == "" {
		ref = strings.TrimSpace(raw)
	}
	spec := Selector{Engine: "ariaRef", Ref: ref}
	if t.nativeDom() {
		held, err := t.refThroughKit(ctx, spec, "")
		if err != nil {
			return Selector{}, err
		}
		if held == "" {
			// Also allow direct engine refs (e.g. "e1-5") if they resolve natively.
			if m, err2 := t.queryNative(ctx, Selector{Engine: "ref", Ref: ref}, "", 1, nil, false); err2 == nil && len(m.Nodes) > 0 {
				return Selector{Engine: "ref", Ref: ref}, nil
			}
			return Selector{}, fmt.Errorf("Unknown ARIA ref %s. Run tab.ariaSnapshot() to refresh refs (they renumber each snapshot).", jsonQuote(ref))
		}
		return Selector{Engine: "ref", Ref: held}, nil
	}
	var cnt int
	if err := t.kit(ctx, "count", []any{spec}, "", &cnt); err != nil {
		return Selector{}, err
	}
	if cnt == 0 {
		return Selector{}, fmt.Errorf("Unknown ARIA ref %s. Run tab.ariaSnapshot() to refresh refs (they renumber each snapshot).", jsonQuote(ref))
	}
	return spec, nil
}

// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:3237-3257
func (t *Tab) resolveFramePath(ctx context.Context, selectorOrNameOrURL string) (string, error) {
	if spec, err := ParseSelector(selectorOrNameOrURL); err == nil {
		var found string
		if err := t.kit(ctx, "frameOf", []any{spec}, "", &found); err == nil && found != "" {
			return found, nil
		}
	}
	frames, err := t.listFrames(ctx)
	if err != nil {
		return "", err
	}
	for _, f := range frames {
		if f.ParentID != nil && (f.ID == selectorOrNameOrURL || f.Name == selectorOrNameOrURL || f.URL == selectorOrNameOrURL) {
			return f.ID, nil
		}
	}
	return "", fmt.Errorf("tab.frame(%s) found no matching frame", jsonQuote(selectorOrNameOrURL))
}

// callElementMethod executes an element-handle method scoped strictly to spec/frame (S7).
// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:4089-4225
func (t *Tab) callElementMethod(ctx context.Context, callCtx browser.CallContext, spec Selector, frame string, step browser.CallStep) (*browser.CallResult, error) {
	args := step.Args
	switch step.Method {
	case "click":
		opts, _, _ := argMap(args, 0)
		btn, _ := opts["button"].(string)
		cnt := 1
		if f, ok := numVal(opts["count"]); ok && f >= 1 {
			cnt = int(f)
		}
		return &browser.CallResult{}, t.clickSpec(ctx, spec, frame, btn, cnt)
	case "dblclick":
		return &browser.CallResult{}, t.clickSpec(ctx, spec, frame, "left", 2)
	case "hover":
		return &browser.CallResult{}, t.hoverSpec(ctx, spec, frame)
	case "focus":
		return &browser.CallResult{}, t.focusSpec(ctx, spec, frame)
	case "check":
		return &browser.CallResult{}, t.setCheckedSpec(ctx, spec, true, frame)
	case "uncheck":
		return &browser.CallResult{}, t.setCheckedSpec(ctx, spec, false, frame)
	case "type":
		txt, _, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		return &browser.CallResult{}, t.typeSpec(ctx, spec, txt, frame)
	case "fill":
		val, _, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		return &browser.CallResult{}, t.fillSpec(ctx, spec, val, frame)
	case "press":
		key, _, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		return &browser.CallResult{}, t.pressSpec(ctx, key, &spec, frame)
	case "select":
		choices, err := NormalizeSelectOptions(args)
		if err != nil {
			return nil, err
		}
		vals, err := t.selectSpec(ctx, spec, choices, frame)
		return &browser.CallResult{Value: vals}, err
	case "uploadFile":
		paths, err := parseFilePathsArgs(args, 0)
		if err != nil {
			return nil, err
		}
		return &browser.CallResult{}, t.uploadFileSpec(ctx, callCtx.Cwd, spec, paths, frame)
	case "scrollIntoView":
		return &browser.CallResult{}, t.scrollIntoViewSpec(ctx, spec, frame)
	case "highlight":
		opts, _, _ := argMap(args, 0)
		return &browser.CallResult{}, t.highlightSpec(ctx, spec, opts, frame)
	case "boundingBox", "box":
		box, err := t.readBox(ctx, spec, frame)
		return &browser.CallResult{Value: box}, err
	case "isVisible":
		vis, err := t.isVisibleSpec(ctx, spec, frame)
		return &browser.CallResult{Value: vis}, err
	case "isHidden":
		vis, err := t.isVisibleSpec(ctx, spec, frame)
		return &browser.CallResult{Value: !vis}, err
	case "isEnabled":
		en, err := t.isEnabledSpec(ctx, spec, frame)
		return &browser.CallResult{Value: en}, err
	case "isChecked":
		ch, err := t.isCheckedSpec(ctx, spec, frame)
		return &browser.CallResult{Value: ch}, err
	case "text":
		txt, err := t.readTextSpec(ctx, spec, frame)
		if err != nil {
			return nil, err
		}
		if txt == nil {
			return &browser.CallResult{Value: ""}, nil
		}
		return &browser.CallResult{Value: *txt}, nil
	case "html":
		h, err := t.readHTMLSpec(ctx, spec, frame)
		if err != nil {
			return nil, err
		}
		if h == nil {
			return &browser.CallResult{Value: ""}, nil
		}
		return &browser.CallResult{Value: *h}, nil
	case "value":
		v, err := t.readValueSpec(ctx, spec, frame)
		return &browser.CallResult{Value: v}, err
	case "attr":
		name, _, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		v, err := t.readAttrSpec(ctx, spec, name, frame)
		return &browser.CallResult{Value: v}, err
	case "styles":
		var props []string
		_ = parseArg(args, 0, &props)
		if len(props) == 0 {
			props = defaultStyleProperties
		}
		var out map[string]string
		err := t.onKit(ctx, spec, frame, func(ks Selector, kf string) error {
			return t.kit(ctx, "read", []any{ks, "styles", props}, kf, &out)
		})
		if out == nil {
			out = map[string]string{}
		}
		return &browser.CallResult{Value: out}, err
	case "evaluate":
		// upstream: tab-call.ts:186-191 (element evaluate accepts function or string function source) & N1 (__omp_fn)
		if len(args) == 0 {
			return nil, fmt.Errorf("elementHandle.evaluate() expects a function or a source string")
		}
		var first any
		if err := json.Unmarshal(args[0], &first); err != nil {
			return nil, fmt.Errorf("elementHandle.evaluate() expects a function or a source string")
		}
		var fnSrc string
		if s, ok := first.(string); ok {
			fnSrc = s
		} else if s, ok := browser.DecodePreludeFn(first); ok {
			fnSrc = s
		} else {
			return nil, fmt.Errorf("elementHandle.evaluate() expects a function or a source string")
		}
		extra := make([]any, 0, max(0, len(args)-1))
		for i := 1; i < len(args); i++ {
			var v any
			_ = json.Unmarshal(args[i], &v)
			extra = append(extra, v)
		}
		val, err := t.evaluateOnElement(ctx, spec, fnSrc, extra, frame)
		return &browser.CallResult{Value: val}, err
	default:
		return nil, unsupportedErr(t.product, "element."+step.Method, "unsupported element handle method")
	}
}

// callSingle dispatches all single-step tab helper methods (and frame-scoped methods).
func (t *Tab) callSingle(ctx context.Context, callCtx browser.CallContext, step browser.CallStep, frame string) (*browser.CallResult, error) {
	args := step.Args
	product := t.product

	switch step.Method {
	// ─── Navigation ───────────────────────────────────────────────────
	case "url":
		// upstream: tern-tab.ts:1731
		return &browser.CallResult{Value: t.URL()}, nil
	case "title":
		// upstream: tern-tab.ts:1741
		if _, err := t.refreshState(ctx); err != nil {
			return nil, err
		}
		t.mu.Lock()
		title := t.title
		t.mu.Unlock()
		return &browser.CallResult{Value: title}, nil
	case "goto":
		// upstream: tern-tab.ts:1636
		rawURL, _, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		opts, _, _ := argMap(args, 1)
		waitUntil, _ := opts["waitUntil"].(string)
		timeout := 30 * time.Second
		if f, ok := numVal(opts["timeout"]); ok && f > 0 {
			timeout = time.Duration(f) * time.Millisecond
		}
		if err := t.Goto(ctx, rawURL, waitUntil, timeout); err != nil {
			return nil, err
		}
		return &browser.CallResult{Value: t.URL()}, nil
	case "back":
		// upstream: tern-tab.ts:1673
		opts, _, _ := argMap(args, 0)
		wu, _ := opts["waitUntil"].(string)
		u, err := t.historyNav(ctx, "back", wu)
		return &browser.CallResult{Value: u}, err
	case "forward":
		// upstream: tern-tab.ts:1678
		opts, _, _ := argMap(args, 0)
		wu, _ := opts["waitUntil"].(string)
		u, err := t.historyNav(ctx, "forward", wu)
		return &browser.CallResult{Value: u}, err
	case "reload":
		// upstream: tern-tab.ts:1683
		opts, _, _ := argMap(args, 0)
		wu, _ := opts["waitUntil"].(string)
		u, err := t.historyNav(ctx, "reload", wu)
		return &browser.CallResult{Value: u}, err
	case "pushState":
		// upstream: tern-tab.ts:1688
		rawURL, _, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		var out string
		if err := t.evalUser(ctx, "tab.pushState()", "function (u) { history.pushState(null, '', u); dispatchEvent(new PopStateEvent('popstate', { state: history.state })); return location.href; }", []any{rawURL}, "", &out); err != nil {
			return nil, err
		}
		if out != "" {
			t.mu.Lock()
			t.url = out
			t.mu.Unlock()
		}
		return &browser.CallResult{Value: t.URL()}, nil

	// ─── Inspection ───────────────────────────────────────────────────
	case "observe":
		// upstream: tern-tab.ts:2925-2945
		opts, _, _ := argMap(args, 0)
		obs, err := t.observe(ctx, opts)
		return &browser.CallResult{Value: obs}, err
	case "ariaSnapshot":
		// upstream: tern-tab.ts:2948-2973
		selStr, opts, err := parseOptionalSelectorAndOpts(args)
		if err != nil {
			return nil, err
		}
		snap, err := t.ariaSnapshot(ctx, selStr, opts, frame)
		return &browser.CallResult{Value: snap}, err
	case "a11y":
		return nil, unsupportedErr(product, "a11y", "axe-core bundle is not bundled in oh-my-pig's Tern backend")
	case "extract":
		// upstream: tern-tab.ts:2996-3010 (returns outerHTML extraction or explicit error when unreadable)
		var outer string
		if err := t.eval(ctx, "function () { return document.documentElement.outerHTML; }", nil, "isolated", "", &outer); err != nil {
			return nil, err
		}
		return nil, unsupportedErr(product, "extract", "readability markdown extractor is not ported in internal/ternbrowser; use tab.text() or tab.ariaSnapshot()")
	case "text":
		// upstream: tern-tab.ts:2598-2604 (defaults to "body")
		selStr, ok, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		if !ok || selStr == "" {
			selStr = "body"
		}
		spec, err := ParseSelector(selStr)
		if err != nil {
			return nil, err
		}
		txt, err := t.readTextSpec(ctx, spec, frame)
		if err != nil {
			return nil, err
		}
		if txt == nil {
			return &browser.CallResult{Value: nil}, nil
		}
		return &browser.CallResult{Value: *txt}, nil
	case "html":
		// upstream: tern-tab.ts:2607-2613 (defaults to "body")
		selStr, ok, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		if !ok || selStr == "" {
			selStr = "body"
		}
		spec, err := ParseSelector(selStr)
		if err != nil {
			return nil, err
		}
		h, err := t.readHTMLSpec(ctx, spec, frame)
		if err != nil {
			return nil, err
		}
		if h == nil {
			return &browser.CallResult{Value: nil}, nil
		}
		return &browser.CallResult{Value: *h}, nil
	case "value":
		// upstream: tern-tab.ts:2616-2623
		selStr, _, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		spec, err := ParseSelector(selStr)
		if err != nil {
			return nil, err
		}
		v, err := t.readValueSpec(ctx, spec, frame)
		return &browser.CallResult{Value: v}, err
	case "attr":
		// upstream: tern-tab.ts:2626-2632
		selStr, _, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		name, _, err := argString(args, 1)
		if err != nil {
			return nil, err
		}
		spec, err := ParseSelector(selStr)
		if err != nil {
			return nil, err
		}
		v, err := t.readAttrSpec(ctx, spec, name, frame)
		return &browser.CallResult{Value: v}, err
	case "count":
		// upstream: tern-tab.ts:2635-2638
		selStr, _, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		spec, err := ParseSelector(selStr)
		if err != nil {
			return nil, err
		}
		n, err := t.countSpec(ctx, spec, frame)
		return &browser.CallResult{Value: n}, err
	case "box":
		// upstream: tern-tab.ts:2641-2650
		selStr, _, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		spec, err := ParseSelector(selStr)
		if err != nil {
			return nil, err
		}
		box, err := t.readBox(ctx, spec, frame)
		return &browser.CallResult{Value: box}, err
	case "styles":
		// upstream: tern-tab.ts:2653-2658
		selStr, _, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		spec, err := ParseSelector(selStr)
		if err != nil {
			return nil, err
		}
		var props []string
		_ = parseArg(args, 1, &props)
		if len(props) == 0 {
			props = defaultStyleProperties
		}
		var out map[string]string
		err = t.onKit(ctx, spec, frame, func(ks Selector, kf string) error {
			return t.kit(ctx, "read", []any{ks, "styles", props}, kf, &out)
		})
		return &browser.CallResult{Value: out}, err
	case "isVisible":
		// upstream: tern-tab.ts:2661-2669
		selStr, _, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		spec, err := ParseSelector(selStr)
		if err != nil {
			return nil, err
		}
		vis, err := t.isVisibleSpec(ctx, spec, frame)
		return &browser.CallResult{Value: vis}, err
	case "isEnabled":
		// upstream: tern-tab.ts:2672-2678
		selStr, _, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		spec, err := ParseSelector(selStr)
		if err != nil {
			return nil, err
		}
		en, err := t.isEnabledSpec(ctx, spec, frame)
		return &browser.CallResult{Value: en}, err
	case "isChecked":
		// upstream: tern-tab.ts:2681-2684
		selStr, _, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		spec, err := ParseSelector(selStr)
		if err != nil {
			return nil, err
		}
		ch, err := t.isCheckedSpec(ctx, spec, frame)
		return &browser.CallResult{Value: ch}, err

	// ─── Capture & PDF ────────────────────────────────────────────────
	case "screenshot":
		// upstream: tern-tab.ts:3061-3151
		opts, _, _ := argMap(args, 0)
		if format, _ := opts["format"].(string); format == "webp" {
			return nil, fmt.Errorf(`tab.screenshot() format "webp" is Chromium-only; use "png" or "jpeg" here`)
		}
		format, _ := opts["format"].(string)
		if format == "" {
			format = "png"
		}
		mime := "image/png"
		if format == "jpeg" {
			mime = "image/jpeg"
		}
		var quality *int
		if f, ok := numVal(opts["quality"]); ok {
			quality = new(int(f))
		}
		fullPage, _ := opts["fullPage"].(bool)
		var sel *Selector
		if s, ok := opts["selector"].(string); ok && s != "" {
			parsed, err := ParseSelector(s)
			if err != nil {
				return nil, err
			}
			sel = &parsed
		}
		t.mu.Lock()
		scale := t.viewport.DeviceScaleFactor
		t.mu.Unlock()
		if scale <= 0 {
			scale = 1
		}
		rawBytes, err := t.captureBytes(ctx, sel, fullPage, format, quality, scale, frame)
		if err != nil {
			return nil, err
		}
		dest := ""
		if p, ok := opts["path"].(string); ok && p != "" {
			dest = resolveToCwd(p, callCtx.Cwd)
		} else {
			dir := callCtx.ScreenshotDir
			if dir == "" {
				dir = os.TempDir()
			}
			ext := "png"
			if format == "jpeg" {
				ext = "jpg"
			}
			dest = filepath.Join(dir, fmt.Sprintf("omp-screenshot-%d.%s", time.Now().UnixNano(), ext))
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(dest, rawBytes, 0o644); err != nil {
			return nil, err
		}
		return &browser.CallResult{
			Value: dest,
			Images: []browser.ImageOutput{{
				MimeType: mime,
				Data:     base64.StdEncoding.EncodeToString(rawBytes),
			}},
			Screenshots: []browser.ScreenshotResult{{
				Dest:     dest,
				MimeType: mime,
				Bytes:    len(rawBytes),
			}},
		}, nil
	case "diffScreenshot":
		return nil, unsupportedErr(product, "diffScreenshot", "PNG pixel diffing is not ported in internal/ternbrowser; use tab.screenshot()")
	case "pdf":
		// upstream: tern-tab.ts:3192-3212
		opts, _, _ := argMap(args, 0)
		var ignored []string
		for _, k := range []string{"format", "landscape", "scale", "printBackground", "margin", "pageRanges"} {
			if v, exists := opts[k]; exists && v != nil {
				ignored = append(ignored, k)
			}
		}
		if len(ignored) > 0 {
			return nil, unsupportedErr(
				product,
				"pdf",
				fmt.Sprintf("options %s are unavailable (the web view prints the whole document with its own pagination); only path is supported", strings.Join(ignored, ", ")),
			)
		}
		var ans struct {
			Data string `json:"data"`
		}
		if err := t.op(ctx, "pdf", opBlockOnlyReq{Op: "pdf", Block: t.block}, &ans); err != nil {
			return nil, err
		}
		if ans.Data == "" {
			return nil, fmt.Errorf("%s pdf answered without data", product)
		}
		rawPDF, err := base64.StdEncoding.DecodeString(ans.Data)
		if err != nil {
			return nil, err
		}
		var dest string
		if p, ok := opts["path"].(string); ok && p != "" {
			dest = resolveToCwd(p, callCtx.Cwd)
		} else {
			dest = filepath.Join(os.TempDir(), fmt.Sprintf("omp-browser-%d.pdf", time.Now().UnixNano()))
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(dest, rawPDF, 0o644); err != nil {
			return nil, err
		}
		return &browser.CallResult{Value: dest}, nil

	// ─── Actions ──────────────────────────────────────────────────────
	case "click":
		// upstream: tern-tab.ts:2226-2238
		selStr, _, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		spec, err := ParseSelector(selStr)
		if err != nil {
			return nil, err
		}
		opts, _, _ := argMap(args, 1)
		btn, _ := opts["button"].(string)
		cnt := 1
		if f, ok := numVal(opts["count"]); ok && f >= 1 {
			cnt = int(f)
		}
		return &browser.CallResult{}, t.clickSpec(ctx, spec, frame, btn, cnt)
	case "dblclick":
		// upstream: tern-tab.ts:2241
		selStr, _, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		spec, err := ParseSelector(selStr)
		if err != nil {
			return nil, err
		}
		return &browser.CallResult{}, t.clickSpec(ctx, spec, frame, "left", 2)
	case "hover":
		// upstream: tern-tab.ts:2246
		selStr, _, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		spec, err := ParseSelector(selStr)
		if err != nil {
			return nil, err
		}
		return &browser.CallResult{}, t.hoverSpec(ctx, spec, frame)
	case "focus":
		// upstream: tern-tab.ts:2256
		selStr, _, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		spec, err := ParseSelector(selStr)
		if err != nil {
			return nil, err
		}
		return &browser.CallResult{}, t.focusSpec(ctx, spec, frame)
	case "check":
		// upstream: tern-tab.ts:2297
		selStr, _, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		spec, err := ParseSelector(selStr)
		if err != nil {
			return nil, err
		}
		return &browser.CallResult{}, t.setCheckedSpec(ctx, spec, true, frame)
	case "uncheck":
		// upstream: tern-tab.ts:2302
		selStr, _, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		spec, err := ParseSelector(selStr)
		if err != nil {
			return nil, err
		}
		return &browser.CallResult{}, t.setCheckedSpec(ctx, spec, false, frame)
	case "type":
		// upstream: tern-tab.ts:2310
		selStr, _, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		text, _, err := argString(args, 1)
		if err != nil {
			return nil, err
		}
		spec, err := ParseSelector(selStr)
		if err != nil {
			return nil, err
		}
		return &browser.CallResult{}, t.typeSpec(ctx, spec, text, frame)
	case "fill":
		// upstream: tern-tab.ts:2324
		selStr, _, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		val, _, err := argString(args, 1)
		if err != nil {
			return nil, err
		}
		spec, err := ParseSelector(selStr)
		if err != nil {
			return nil, err
		}
		return &browser.CallResult{}, t.fillSpec(ctx, spec, val, frame)
	case "press":
		// upstream: tern-tab.ts:2348-2367 (supports press(key, {selector}) or press(selector, key))
		a0, _, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		key := a0
		var selSpec *Selector
		if len(args) > 1 && len(args[1]) > 0 && strings.TrimSpace(string(args[1])) != "null" {
			if s1, ok, err := argString(args, 1); err == nil && ok {
				// press(selector, key)
				parsed, err := ParseSelector(a0)
				if err != nil {
					return nil, err
				}
				selSpec = &parsed
				key = s1
			} else if opts, ok, err := argMap(args, 1); err == nil && ok {
				if selStr, ok := opts["selector"].(string); ok && selStr != "" {
					parsed, err := ParseSelector(selStr)
					if err != nil {
						return nil, err
					}
					selSpec = &parsed
				}
			}
		}
		return &browser.CallResult{}, t.pressSpec(ctx, key, selSpec, frame)
	case "keyDown":
		// upstream: tern-tab.ts:2370-2374 (fixes B7: guard duplicate modifier)
		name, _, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		k, err := ResolveKey(name)
		if err != nil {
			return nil, err
		}
		t.mu.Lock()
		stepIn := KeyDownStep(k, t.heldMods)
		t.mu.Unlock()
		if err := t.input(ctx, []InputStep{stepIn}); err != nil {
			return nil, err
		}
		if k.Modifier != "" {
			t.mu.Lock()
			if !slices.Contains(t.heldMods, k.Modifier) {
				t.heldMods = append(t.heldMods, k.Modifier)
			}
			t.mu.Unlock()
		}
		return &browser.CallResult{}, nil
	case "keyUp":
		// upstream: tern-tab.ts:2377-2384
		name, _, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		k, err := ResolveKey(name)
		if err != nil {
			return nil, err
		}
		t.mu.Lock()
		if k.Modifier != "" {
			if idx := slices.Index(t.heldMods, k.Modifier); idx >= 0 {
				t.heldMods = append(t.heldMods[:idx], t.heldMods[idx+1:]...)
			}
		}
		stepIn := KeyUpStep(k, t.heldMods)
		t.mu.Unlock()
		return &browser.CallResult{}, t.input(ctx, []InputStep{stepIn})
	case "mouseMove":
		// upstream: tern-tab.ts:2406-2420
		x, ok1, err1 := argFloat(args, 0)
		y, ok2, err2 := argFloat(args, 1)
		if err1 != nil || err2 != nil || !ok1 || !ok2 || math.IsNaN(x) || math.IsNaN(y) {
			return nil, fmt.Errorf("mouseMove coordinates must be finite numbers")
		}
		opts, _, _ := argMap(args, 2)
		css, _ := opts["css"].(bool)
		stepsCount := 1
		if f, ok := numVal(opts["steps"]); ok && f >= 1 {
			stepsCount = int(f)
		}
		t.mu.Lock()
		tx, ty, err := t.pointerSpace.ToViewport(x, y, css)
		if err != nil {
			t.mu.Unlock()
			return nil, err
		}
		fx, fy := tx, ty
		if t.pointer != nil {
			fx, fy = t.pointer[0], t.pointer[1]
		}
		heldBtn := "left"
		for b := range t.buttons {
			heldBtn = b
			break
		}
		t.mu.Unlock()
		events := make([]InputStep, 0, stepsCount)
		for s := 1; s <= stepsCount; s++ {
			px := fx + (tx-fx)*float64(s)/float64(stepsCount)
			py := fy + (ty-fy)*float64(s)/float64(stepsCount)
			events = append(events, t.mouseStep("move", px, py, heldBtn, new(0)))
		}
		t.mu.Lock()
		t.pointer = &[2]float64{tx, ty}
		t.mu.Unlock()
		return &browser.CallResult{}, t.input(ctx, events)
	case "mouseDown":
		// upstream: tern-tab.ts:2423-2428 (takes options object {button?, clickCount?})
		opts, _, err := argMap(args, 0)
		if err != nil {
			return nil, fmt.Errorf("tab.mouseDown() expects an options object { button?, clickCount? }")
		}
		btnName, _ := opts["button"].(string)
		btn, err := t.validateButton(btnName)
		if err != nil {
			return nil, err
		}
		clicks := 1
		if f, ok := numVal(opts["clickCount"]); ok && f >= 1 {
			clicks = int(f)
		}
		px, py, err := t.currentPointerOrCentre(ctx)
		if err != nil {
			return nil, err
		}
		t.mu.Lock()
		t.buttons[btn] = true
		t.mu.Unlock()
		return &browser.CallResult{}, t.input(ctx, []InputStep{t.mouseStep("down", px, py, btn, new(clicks))})
	case "mouseUp":
		// upstream: tern-tab.ts:2431-2436
		opts, _, err := argMap(args, 0)
		if err != nil {
			return nil, fmt.Errorf("tab.mouseUp() expects an options object { button?, clickCount? }")
		}
		btnName, _ := opts["button"].(string)
		btn, err := t.validateButton(btnName)
		if err != nil {
			return nil, err
		}
		clicks := 1
		if f, ok := numVal(opts["clickCount"]); ok && f >= 1 {
			clicks = int(f)
		}
		px, py, err := t.currentPointerOrCentre(ctx)
		if err != nil {
			return nil, err
		}
		t.mu.Lock()
		delete(t.buttons, btn)
		t.mu.Unlock()
		return &browser.CallResult{}, t.input(ctx, []InputStep{t.mouseStep("up", px, py, btn, new(clicks))})
	case "clickAt":
		// upstream: tern-tab.ts:2439-2450
		x, ok1, err1 := argFloat(args, 0)
		y, ok2, err2 := argFloat(args, 1)
		if err1 != nil || err2 != nil || !ok1 || !ok2 || math.IsNaN(x) || math.IsNaN(y) {
			return nil, fmt.Errorf("clickAt coordinates must be finite numbers")
		}
		opts, _, _ := argMap(args, 2)
		css, _ := opts["css"].(bool)
		btnName, _ := opts["button"].(string)
		btn, err := t.validateButton(btnName)
		if err != nil {
			return nil, err
		}
		cnt := 1
		if f, ok := numVal(opts["count"]); ok && f >= 1 {
			cnt = int(f)
		}
		t.mu.Lock()
		vx, vy, err := t.pointerSpace.ToViewport(x, y, css)
		t.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return &browser.CallResult{}, t.clickAtPoint(ctx, vx, vy, btn, cnt)
	case "wheel":
		// upstream: tern-tab.ts:2453-2457
		dx, dy, _, err := parseScrollDeltas("tab.wheel()", args)
		if err != nil {
			return nil, err
		}
		px, py, err := t.currentPointerOrCentre(ctx)
		if err != nil {
			return nil, err
		}
		t.mu.Lock()
		mods := append([]Modifier{}, t.heldMods...)
		t.mu.Unlock()
		return &browser.CallResult{}, t.input(ctx, []InputStep{{Type: "wheel", X: px, Y: py, DX: dx, DY: dy, Mods: mods}})
	case "scroll":
		// upstream: tern-tab.ts:2463-2480
		dx, dy, optIdx, err := parseScrollDeltas("tab.scroll()", args)
		if err != nil {
			return nil, err
		}
		opts, _, _ := argMap(args, optIdx)
		if selStr, ok := opts["selector"].(string); ok && selStr != "" {
			spec, err := ParseSelector(selStr)
			if err != nil {
				return nil, err
			}
			box, _, _, err := t.target(ctx, fmt.Sprintf("tab.scroll(%s)", jsonQuote(selStr)), spec, "point", "", 0)
			if err != nil {
				return nil, err
			}
			if t.nativeDom() {
				t.mu.Lock()
				mods := append([]Modifier{}, t.heldMods...)
				t.mu.Unlock()
				return &browser.CallResult{}, t.input(ctx, []InputStep{{Type: "wheel", X: box.X, Y: box.Y, DX: dx, DY: dy, Mods: mods}})
			}
			return &browser.CallResult{}, t.kit(ctx, "scrollBy", []any{spec, dx, dy}, "", nil)
		}
		cx, cy, err := t.viewportCentre(ctx)
		if err != nil {
			return nil, err
		}
		t.mu.Lock()
		mods := append([]Modifier{}, t.heldMods...)
		t.mu.Unlock()
		return &browser.CallResult{}, t.input(ctx, []InputStep{{Type: "wheel", X: cx, Y: cy, DX: dx, DY: dy, Mods: mods}})
	case "drag":
		// upstream: tern-tab.ts:2482-2507 (selectors or {x,y,css?} points)
		sx, sy, err := t.resolveDragTarget(ctx, args, 0, "from")
		if err != nil {
			return nil, err
		}
		ex, ey, err := t.resolveDragTarget(ctx, args, 1, "to")
		if err != nil {
			return nil, err
		}
		steps := make([]InputStep, 0, 13)
		steps = append(steps,
			t.mouseStep("move", sx, sy, "left", new(0)),
			t.mouseStep("down", sx, sy, "left", new(1)),
		)
		for s := 1; s <= 10; s++ {
			px := sx + (ex-sx)*float64(s)/10
			py := sy + (ey-sy)*float64(s)/10
			steps = append(steps, t.mouseStep("move", px, py, "left", new(0)))
		}
		steps = append(steps, t.mouseStep("up", ex, ey, "left", new(1)))
		t.mu.Lock()
		t.pointer = &[2]float64{ex, ey}
		t.mu.Unlock()
		return &browser.CallResult{}, t.input(ctx, steps)
	case "highlight":
		// upstream: tern-tab.ts:2510-2526
		selStr, _, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		spec, err := ParseSelector(selStr)
		if err != nil {
			return nil, err
		}
		opts, _, _ := argMap(args, 1)
		return &browser.CallResult{}, t.highlightSpec(ctx, spec, opts, frame)
	case "scrollIntoView":
		// upstream: tern-tab.ts:2529-2533
		selStr, _, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		spec, err := ParseSelector(selStr)
		if err != nil {
			return nil, err
		}
		return &browser.CallResult{}, t.scrollIntoViewSpec(ctx, spec, frame)
	case "select":
		// upstream: tern-tab.ts:2536-2543
		selStr, _, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		spec, err := ParseSelector(selStr)
		if err != nil {
			return nil, err
		}
		choices, err := NormalizeSelectOptions(args[1:])
		if err != nil {
			return nil, err
		}
		vals, err := t.selectSpec(ctx, spec, choices, frame)
		return &browser.CallResult{Value: vals}, err
	case "uploadFile":
		// upstream: tern-tab.ts:2549-2582
		selStr, _, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		spec, err := ParseSelector(selStr)
		if err != nil {
			return nil, err
		}
		paths, err := parseFilePathsArgs(args, 1)
		if err != nil {
			return nil, err
		}
		return &browser.CallResult{}, t.uploadFileSpec(ctx, callCtx.Cwd, spec, paths, frame)

	// ─── Waits ────────────────────────────────────────────────────────
	case "waitFor":
		// upstream: tern-tab.ts:2726-2747
		selStr, _, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		spec, err := ParseSelector(selStr)
		if err != nil {
			return nil, err
		}
		opts, _, _ := argMap(args, 1)
		timeout := waitTimeoutFromOpts(opts, 10*time.Second)
		if err := t.waitForSpec(ctx, fmt.Sprintf("tab.waitFor(%s)", jsonQuote(selStr)), spec, "visible", frame, timeout); err != nil {
			return nil, err
		}
		return &browser.CallResult{Value: true}, nil
	case "waitForSelector":
		// upstream: tern-tab.ts:2750-2788
		selStr, _, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		spec, err := ParseSelector(selStr)
		if err != nil {
			return nil, err
		}
		opts, _, _ := argMap(args, 1)
		timeout := waitTimeoutFromOpts(opts, 10*time.Second)
		state := "attached"
		if h, _ := opts["hidden"].(bool); h {
			state = "hidden"
		} else if v, _ := opts["visible"].(bool); v {
			state = "visible"
		}
		if err := t.waitForSpec(ctx, fmt.Sprintf("tab.waitForSelector(%s)", jsonQuote(selStr)), spec, state, frame, timeout); err != nil {
			return nil, err
		}
		return &browser.CallResult{Value: state != "hidden"}, nil
	case "waitForUrl":
		// upstream: tern-tab.ts:1715-1728, navigation.ts:21-27, network.ts:703-716 (N1 + N4)
		var rawPattern any
		if len(args) > 0 && len(args[0]) > 0 {
			_ = json.Unmarshal(args[0], &rawPattern)
		}
		matcher, err := compileURLMatcher(rawPattern)
		if err != nil {
			return nil, err
		}
		opts, _, _ := argMap(args, 1)
		timeout := waitTimeoutFromOpts(opts, 10*time.Second)
		iteration := 0
		err = t.poll(ctx, fmt.Sprintf("tab.waitForUrl(%v)", rawPattern), timeout, true, func() (bool, error) {
			iteration++
			if iteration%4 == 0 {
				_, _ = t.refreshState(ctx)
			}
			return matcher(t.URL()), nil
		}, func() string { return "the URL is " + t.URL() })
		if err != nil {
			return nil, err
		}
		return &browser.CallResult{Value: t.URL()}, nil
	case "waitForText":
		// upstream: tern-tab.ts:2691-2723
		needle, _, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		opts, _, _ := argMap(args, 1)
		timeout := waitTimeoutFromOpts(opts, 10*time.Second)
		exact, _ := opts["exact"].(bool)
		var rootSpec *Selector
		if selStr, ok := opts["selector"].(string); ok && selStr != "" {
			parsed, err := ParseSelector(selStr)
			if err != nil {
				return nil, err
			}
			rootSpec = &parsed
		}
		label := fmt.Sprintf("tab.waitForText(%s)", jsonQuote(needle))
		if t.nativeDom() {
			var within string
			if rootSpec != nil {
				q, err := t.queryForNative(ctx, *rootSpec, "")
				if err != nil {
					return nil, err
				}
				if q != nil {
					var w DomWaited
					if err := t.dom(ctx, "wait", opDomWaitReq{Op: "dom", Block: t.block, Read: "wait", For: *q, State: "attached", Timeout: int(timeout.Milliseconds())}, &w, timeout+waitReplyGrace); err != nil {
						return nil, err
					}
					if !w.Matched {
						return nil, fmt.Errorf("%s timed out after %dms", label, timeout.Milliseconds())
					}
					if len(w.Nodes) > 0 {
						within = w.Nodes[0].Ref
					}
				}
			}
			var w DomWaited
			if err := t.dom(ctx, "wait", opDomWaitReq{
				Op: "dom", Block: t.block, Read: "wait",
				For: DomQuery{By: "text", Value: needle, Exact: exact, Root: within}, State: "attached", Timeout: int(timeout.Milliseconds()),
			}, &w, timeout+waitReplyGrace); err != nil {
				return nil, err
			}
			if !w.Matched {
				return nil, fmt.Errorf("%s timed out after %dms", label, timeout.Milliseconds())
			}
			return &browser.CallResult{}, nil
		}
		var rootArg any
		if rootSpec != nil {
			rootArg = *rootSpec
		}
		err = t.poll(ctx, label, timeout, false, func() (bool, error) {
			var found bool
			if err := t.kit(ctx, "hasText", []any{needle, rootArg, exact}, "", &found); err != nil {
				return false, err
			}
			return found, nil
		}, nil)
		return &browser.CallResult{}, err

	// ─── Frames ───────────────────────────────────────────────────────
	case "frames":
		// upstream: tern-tab.ts:3217-3234
		frames, err := t.listFrames(ctx)
		return &browser.CallResult{Value: frames}, err

	// ─── Dialogs ──────────────────────────────────────────────────────
	case "dialog":
		// upstream: tern-tab.ts:3260-3263
		if err := t.pull(ctx); err != nil {
			return nil, err
		}
		t.mu.Lock()
		ds := t.dialogStateLocked()
		t.mu.Unlock()
		return &browser.CallResult{Value: ds}, nil
	case "handleDialog":
		// upstream: tern-tab.ts:3266-3279 (fixes S3: decode {accept: bool, text?: string})
		opts, ok, err := argMap(args, 0)
		if err != nil || !ok {
			return nil, fmt.Errorf("tab.handleDialog() expects an options object { accept: boolean, text?: string }")
		}
		accept, _ := opts["accept"].(bool)
		req := opDialogReq{
			Op:     "dialog",
			Block:  t.block,
			Accept: accept,
		}
		if txt, ok := opts["text"].(string); ok {
			req.Text = new(txt)
		}
		if err := t.op(ctx, "dialog", req, nil); err != nil {
			var te *ternwire.Error
			if errors.As(err, &te) && te.Kind == ternwire.ErrFailed {
				return nil, fmt.Errorf("tab.handleDialog() found no pending confirm or prompt")
			}
			return nil, err
		}
		t.mu.Lock()
		t.pendingDialog = nil
		t.mu.Unlock()
		return &browser.CallResult{}, nil
	case "setDialogs":
		// upstream: tern-tab.ts:3282-3287
		var policyPtr *string
		if len(args) > 0 && len(args[0]) > 0 && strings.TrimSpace(string(args[0])) != "null" {
			var s string
			if err := json.Unmarshal(args[0], &s); err != nil {
				return nil, fmt.Errorf(`tab.setDialogs() takes "accept", "dismiss" or null (back to the default); got %s`, string(args[0]))
			}
			policyPtr = &s
		}
		if err := assertDialogPolicy(policyPtr); err != nil {
			return nil, err
		}
		pol := "default"
		if policyPtr != nil {
			pol = *policyPtr
		}
		if err := t.op(ctx, "dialogs", opDialogsReq{Op: "dialogs", Block: t.block, Policy: pol}, nil); err != nil {
			return nil, err
		}
		_ = t.pull(ctx)
		t.mu.Lock()
		hasPending := t.pendingDialog != nil
		t.mu.Unlock()
		if policyPtr != nil && hasPending {
			_ = t.op(ctx, "dialog", opDialogReq{Op: "dialog", Block: t.block, Accept: *policyPtr == "accept"}, nil)
			t.mu.Lock()
			t.pendingDialog = nil
			t.mu.Unlock()
		}
		return &browser.CallResult{}, nil

	// ─── Emulation ────────────────────────────────────────────────────
	case "emulate":
		// upstream: tern-tab.ts:3292-3387 (fixes S1, A8)
		opts, _, err := argMap(args, 0)
		if err != nil {
			return nil, fmt.Errorf("tab.emulate() expects an options object")
		}
		em, err := t.emulate(ctx, opts)
		return &browser.CallResult{Value: em}, err
	case "devices":
		return nil, unsupportedErr(product, "devices", "Puppeteer device catalog is not bundled in oh-my-pig")

	// ─── Clipboard ────────────────────────────────────────────────────
	case "clipboardRead":
		// upstream: tern-tab.ts:3396-3400
		var ans struct {
			Text string `json:"text"`
		}
		if err := t.client.Request(ctx, "browser", opClipboardReq{Op: "clipboard"}, &ans); err != nil {
			return nil, err
		}
		return &browser.CallResult{Value: map[string]any{"text": ans.Text, "source": "page"}}, nil
	case "clipboardWrite":
		// upstream: tern-tab.ts:3403-3406
		txt, _, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		if err := t.client.Request(ctx, "browser", opClipboardReq{Op: "clipboard", Text: new(txt)}, nil); err != nil {
			return nil, err
		}
		return &browser.CallResult{Value: map[string]any{"source": "page"}}, nil
	case "clipboardCopy":
		// upstream: tern-tab.ts:3409-3412
		if err := t.op(ctx, "edit", opEditReq{Op: "edit", Block: t.block, Action: "copy"}, nil); err != nil {
			return nil, err
		}
		return &browser.CallResult{Value: map[string]any{"source": "page"}}, nil
	case "clipboardPaste":
		// upstream: tern-tab.ts:3415-3418
		if err := t.op(ctx, "edit", opEditReq{Op: "edit", Block: t.block, Action: "paste"}, nil); err != nil {
			return nil, err
		}
		return &browser.CallResult{Value: map[string]any{"source": "page"}}, nil

	// ─── Cookies & Storage ────────────────────────────────────────────
	case "cookies":
		// upstream: tern-tab.ts:3429-3442 (fixes S2)
		opts, _, _ := argMap(args, 0)
		var urls []string
		if rawURLs, exists := opts["urls"]; exists && rawURLs != nil {
			arr, ok := rawURLs.([]any)
			if !ok {
				return nil, fmt.Errorf("tab.cookies() expects urls to be an array")
			}
			for _, u := range arr {
				s, ok := u.(string)
				if !ok {
					return nil, fmt.Errorf("tab.cookies() received an invalid URL")
				}
				urls = append(urls, s)
			}
		}
		list, err := t.cookiesForURLs(ctx, urls)
		return &browser.CallResult{Value: list}, err
	case "setCookies":
		// upstream: tern-tab.ts:3445-3477 (fixes S1 & A/setCookie)
		var list []map[string]any
		if len(args) > 0 {
			if err := json.Unmarshal(args[0], &list); err != nil {
				var single map[string]any
				if err2 := json.Unmarshal(args[0], &single); err2 != nil {
					return nil, fmt.Errorf("tab.setCookies() received malformed cookie input")
				}
				list = []map[string]any{single}
			}
		}
		return &browser.CallResult{}, t.setCookies(ctx, list)
	case "clearCookies":
		// upstream: tern-tab.ts:3480-3489 (fixes S2)
		opts, _, _ := argMap(args, 0)
		var names []string
		if rawNames, exists := opts["names"]; exists && rawNames != nil {
			arr, ok := rawNames.([]any)
			if !ok {
				return nil, fmt.Errorf("tab.clearCookies() expects names to be an array")
			}
			for _, item := range arr {
				if s, ok := item.(string); ok {
					names = append(names, s)
				}
			}
		}
		return &browser.CallResult{}, t.clearCookies(ctx, names)
	case "storage":
		// upstream: tern-tab.ts:3492-3499
		kind, _, err := argString(args, 0)
		if err != nil || (kind != "local" && kind != "session") {
			return nil, fmt.Errorf(`tab.storage() expects "local" or "session"`)
		}
		opts, _, _ := argMap(args, 1)
		var evalArgs []any
		evalArgs = append(evalArgs, kind)
		if rawKey, exists := opts["key"]; exists && rawKey != nil {
			keyStr, ok := rawKey.(string)
			if !ok {
				return nil, fmt.Errorf("tab.storage() expects key to be a string")
			}
			evalArgs = append(evalArgs, keyStr)
		}
		fn := `function (kind, key) {
			var area = kind === "local" ? globalThis.localStorage : globalThis.sessionStorage;
			if (key !== undefined) return area.getItem(key);
			var out = {};
			for (var i = 0; i < area.length; i++) {
				var k = area.key(i);
				if (k !== null) { var v = area.getItem(k); if (v !== null) out[k] = v; }
			}
			return out;
		}`
		var out any
		if err := t.evalUser(ctx, "tab.storage()", fn, evalArgs, "", &out); err != nil {
			return nil, err
		}
		return &browser.CallResult{Value: out}, nil
	case "setStorage":
		// upstream: tern-tab.ts:3502-3507
		kind, _, err := argString(args, 0)
		if err != nil || (kind != "local" && kind != "session") {
			return nil, fmt.Errorf(`tab.setStorage() expects "local" or "session"`)
		}
		var entries [][]string
		if key, ok, err := argString(args, 1); err == nil && ok {
			val, _, _ := argString(args, 2)
			entries = [][]string{{key, val}}
		} else if m, ok, err := argMap(args, 1); err == nil && ok {
			for k, v := range m {
				entries = append(entries, []string{k, fmt.Sprintf("%v", v)})
			}
		} else {
			return nil, fmt.Errorf("tab.setStorage() expects a key/value or an object of entries")
		}
		fn := `function (kind, entries) {
			var area = kind === "local" ? globalThis.localStorage : globalThis.sessionStorage;
			for (var i = 0; i < entries.length; i++) area.setItem(entries[i][0], entries[i][1]);
		}`
		return &browser.CallResult{}, t.evalUser(ctx, "tab.setStorage()", fn, []any{kind, entries}, "", nil)
	case "clearStorage":
		// upstream: tern-tab.ts:3510-3513
		kind, _, err := argString(args, 0)
		if err != nil || (kind != "local" && kind != "session") {
			return nil, fmt.Errorf(`tab.clearStorage() expects "local" or "session"`)
		}
		fn := `function (kind) { (kind === "local" ? globalThis.localStorage : globalThis.sessionStorage).clear(); }`
		return &browser.CallResult{}, t.evalUser(ctx, "tab.clearStorage()", fn, []any{kind}, "", nil)
	case "saveState":
		// upstream: tern-tab.ts:3516-3526, storage-state.ts:471-477 (S2 + N3)
		reqPath, _, _ := argString(args, 0)
		dest := storageStatePath(t.name, reqPath, callCtx.Cwd)
		cookies, err := t.allCookies(ctx)
		if err != nil {
			return nil, err
		}
		var origin any
		_ = t.evalUser(ctx, "tab.saveState()", `function () {
			function dump(a) { var out = []; for (var i = 0; i < a.length; i++) { var k = a.key(i); if (k !== null) out.push({ name: k, value: a.getItem(k) || "" }); } return out; }
			return { origin: location.origin, localStorage: dump(localStorage), sessionStorage: dump(sessionStorage) };
		}`, nil, "", &origin)
		var origins []any
		if origin != nil {
			origins = []any{origin}
		} else {
			origins = []any{}
		}
		payload, err := json.MarshalIndent(map[string]any{"cookies": cookies, "origins": origins}, "", "  ")
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(dest, append(payload, '\n'), 0o600); err != nil {
			return nil, err
		}
		return &browser.CallResult{Value: dest}, nil
	case "loadState":
		// upstream: tern-tab.ts:3529-3548 (fixes C1: reads actual state file instead of returning fake ok)
		reqPath, ok, err := argString(args, 0)
		if err != nil || !ok || reqPath == "" {
			return nil, fmt.Errorf("tab.loadState() requires a path")
		}
		fullPath := resolveToCwd(reqPath, callCtx.Cwd)
		raw, err := os.ReadFile(fullPath)
		if err != nil {
			return nil, fmt.Errorf("tab.loadState() cannot read %s: %w", fullPath, err)
		}
		var state struct {
			Cookies []map[string]any `json:"cookies"`
			Origins []struct {
				Origin         string              `json:"origin"`
				LocalStorage   []map[string]string `json:"localStorage"`
				SessionStorage []map[string]string `json:"sessionStorage"`
			} `json:"origins"`
		}
		if err := json.Unmarshal(raw, &state); err != nil {
			return nil, fmt.Errorf("tab.loadState() received an invalid browser state file")
		}
		if len(state.Cookies) > 0 {
			if err := t.setCookies(ctx, state.Cookies); err != nil {
				return nil, err
			}
		}
		currentOrigin := "null"
		if u, err := url.Parse(t.URL()); err == nil && u.Scheme != "" && u.Host != "" {
			currentOrigin = u.Scheme + "://" + u.Host
		}
		loaded := []string{}
		skipped := []string{}
		for _, o := range state.Origins {
			if o.Origin == currentOrigin {
				fn := `function (o) {
					for (var i = 0; i < (o.localStorage || []).length; i++) localStorage.setItem(o.localStorage[i].name, o.localStorage[i].value);
					for (var j = 0; j < (o.sessionStorage || []).length; j++) sessionStorage.setItem(o.sessionStorage[j].name, o.sessionStorage[j].value);
				}`
				if err := t.evalUser(ctx, "tab.loadState()", fn, []any{o}, "", nil); err != nil {
					return nil, err
				}
				loaded = append(loaded, o.Origin)
			} else {
				skipped = append(skipped, o.Origin)
			}
		}
		return &browser.CallResult{Value: map[string]any{"loadedOrigins": loaded, "skippedOrigins": skipped}}, nil

	// ─── Init scripts & Downloads ─────────────────────────────────────
	case "addInitScript":
		// upstream: tern-tab.ts:3553-3564
		src, ok, err := argString(args, 0)
		if err != nil || !ok {
			return nil, fmt.Errorf("tab.addInitScript(source) requires a string")
		}
		t.mu.Lock()
		id := fmt.Sprintf("tern-init-%d", t.nextInitScript)
		t.nextInitScript++
		t.initScripts = append(t.initScripts, initScriptEntry{ID: id, Source: src})
		t.mu.Unlock()
		if err := t.syncScripts(ctx); err != nil {
			t.mu.Lock()
			if len(t.initScripts) > 0 {
				t.initScripts = t.initScripts[:len(t.initScripts)-1]
			}
			t.mu.Unlock()
			return nil, err
		}
		return &browser.CallResult{Value: map[string]string{"id": id}}, nil
	case "removeInitScript":
		// upstream: tern-tab.ts:3567-3574
		id, ok, err := argString(args, 0)
		if err != nil || !ok || id == "" {
			return nil, fmt.Errorf("tab.removeInitScript(id) requires a non-empty script id")
		}
		t.mu.Lock()
		idx := -1
		for i, s := range t.initScripts {
			if s.ID == id {
				idx = i
				break
			}
		}
		if idx < 0 {
			t.mu.Unlock()
			return nil, fmt.Errorf("Unknown init script %s", jsonQuote(id))
		}
		t.initScripts = append(t.initScripts[:idx], t.initScripts[idx+1:]...)
		t.mu.Unlock()
		return &browser.CallResult{}, t.syncScripts(ctx)
	case "initScripts":
		// upstream: tern-tab.ts:3577-3579
		t.mu.Lock()
		out := append([]initScriptEntry(nil), t.initScripts...)
		t.mu.Unlock()
		return &browser.CallResult{Value: out}, nil
	case "waitForDownload":
		// upstream: tern-tab.ts:3590-3610
		if err := t.enableDownloads(ctx, ""); err != nil {
			return nil, err
		}
		opts, _, _ := argMap(args, 0)
		timeout := waitTimeoutFromOpts(opts, 30*time.Second)
		t.mu.Lock()
		failedBefore := map[int]bool{}
		for id, d := range t.downloads {
			if d.State == "failed" {
				failedBefore[id] = true
			}
		}
		t.mu.Unlock()
		var ready BrowserDownload
		err := t.poll(ctx, "tab.waitForDownload()", timeout, true, func() (bool, error) {
			t.mu.Lock()
			defer t.mu.Unlock()
			if len(t.unclaimedDownloads) > 0 {
				ready = t.unclaimedDownloads[0]
				t.unclaimedDownloads = t.unclaimedDownloads[1:]
				return true, nil
			}
			for id, d := range t.downloads {
				if d.State == "failed" && !failedBefore[id] {
					msg := d.Err
					if msg == "" {
						msg = "unknown error"
					}
					return false, fmt.Errorf("Download of %s failed: %s", d.URL, msg)
				}
			}
			return false, nil
		}, nil)
		if err != nil {
			return nil, err
		}
		return &browser.CallResult{Value: ready}, nil
	case "downloads":
		// upstream: tern-tab.ts:3613-3617
		if err := t.enableDownloads(ctx, ""); err != nil {
			return nil, err
		}
		if err := t.pull(ctx); err != nil {
			return nil, err
		}
		t.mu.Lock()
		out := append([]BrowserDownload(nil), t.completedDownloads...)
		t.mu.Unlock()
		return &browser.CallResult{Value: out}, nil

	// ─── Console & Network ────────────────────────────────────────────
	case "console":
		// upstream: tern-tab.ts:3653-3659
		opts, _, _ := argMap(args, 0)
		res, err := t.readConsole(ctx, "console", opts)
		return &browser.CallResult{Value: res}, err
	case "errors":
		// upstream: tern-tab.ts:3662-3668
		opts, _, _ := argMap(args, 0)
		res, err := t.readConsole(ctx, "errors", opts)
		return &browser.CallResult{Value: res}, err
	case "clearConsole":
		// upstream: tern-tab.ts:3671-3674
		if err := t.pull(ctx); err != nil {
			return nil, err
		}
		t.mu.Lock()
		t.consoleEntries = nil
		t.consoleDropped = 0
		t.mu.Unlock()
		return &browser.CallResult{}, nil
	case "route":
		// upstream: tern-tab.ts:3710-3713 (N1: accepts string or {__omp_re:{source,flags}})
		var rawPat any
		if len(args) > 0 && len(args[0]) > 0 {
			_ = json.Unmarshal(args[0], &rawPat)
		}
		var ro RouteOptions
		if err := parseArg(args, 1, &ro); err != nil {
			return nil, err
		}
		if err := t.netLog.Route(rawPat, ro); err != nil {
			return nil, err
		}
		return &browser.CallResult{}, t.applyCapture(ctx)
	case "unroute":
		// upstream: tern-tab.ts:3716-3719
		var rawPat any
		if len(args) > 0 && len(args[0]) > 0 {
			_ = json.Unmarshal(args[0], &rawPat)
		}
		t.netLog.Unroute(rawPat)
		return &browser.CallResult{}, t.applyCapture(ctx)
	case "routes":
		// upstream: tern-tab.ts:3722-3724
		return &browser.CallResult{Value: t.netLog.Routes()}, nil
	case "requests":
		// upstream: tern-tab.ts:3727-3730
		if err := t.pull(ctx); err != nil {
			return nil, err
		}
		opts, _, _ := argMap(args, 0)
		limit := 0
		if f, ok := numVal(opts["limit"]); ok && f > 0 {
			limit = int(f)
		}
		clearAfter, _ := opts["clear"].(bool)
		return &browser.CallResult{Value: t.netLog.Requests(limit, clearAfter)}, nil
	case "request":
		// upstream: tern-tab.ts:3733-3736
		if err := t.pull(ctx); err != nil {
			return nil, err
		}
		var key string
		if s, ok, err := argString(args, 0); err == nil && ok {
			key = s
		} else if n, ok, err := argInt(args, 0); err == nil && ok {
			key = fmt.Sprintf("request-%d", n)
		}
		rec, ok := t.netLog.FindRequest(key)
		if !ok {
			return nil, fmt.Errorf("Unknown browser request id %s", jsonQuote(key))
		}
		return &browser.CallResult{Value: rec}, nil
	case "clearRequests":
		// upstream: tern-tab.ts:3739-3742
		if err := t.pull(ctx); err != nil {
			return nil, err
		}
		t.netLog.Clear()
		return &browser.CallResult{}, nil
	case "harStart":
		// upstream: tern-tab.ts:3745-3748
		if err := t.pull(ctx); err != nil {
			return nil, err
		}
		opts, _, _ := argMap(args, 0)
		content, _ := opts["content"].(string)
		return &browser.CallResult{}, t.netLog.HarStart(content)
	case "harStop":
		// upstream: tern-tab.ts:3751-3760 (writes HAR file and returns path)
		if err := t.pull(ctx); err != nil {
			return nil, err
		}
		har, err := t.netLog.HarStop()
		if err != nil {
			return nil, err
		}
		opts, _, _ := argMap(args, 0)
		var dest string
		if p, ok := opts["path"].(string); ok && p != "" {
			dest = resolveToCwd(p, callCtx.Cwd)
		} else {
			dest = filepath.Join(os.TempDir(), fmt.Sprintf("omp-browser-%d.har", time.Now().UnixNano()))
		}
		data, err := json.MarshalIndent(har, "", "  ")
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(dest, append(data, '\n'), 0o644); err != nil {
			return nil, err
		}
		return &browser.CallResult{Value: dest}, nil
	case "allowedDomains":
		// upstream: tern-tab.ts:3763-3765
		t.mu.Lock()
		out := append([]string(nil), t.allowedDomains...)
		t.mu.Unlock()
		return &browser.CallResult{Value: out}, nil

	// ─── Performance, Tracing, WebMCP, React, Recording ───────────────
	case "metrics":
		// upstream: tern-tab.ts:3800-3820
		fn := `function () {
			const nav = performance.getEntriesByType("navigation")[0];
			const round = value => Math.round((Number(value) || 0) * 100) / 100;
			return {
				domContentLoaded: round(nav && nav.domContentLoadedEventEnd),
				load: round(nav && nav.loadEventEnd),
				Nodes: document.getElementsByTagName("*").length,
				Timestamp: round(performance.now()),
			};
		}`
		var out map[string]float64
		if err := t.eval(ctx, fn, nil, "isolated", "", &out); err != nil {
			return nil, err
		}
		if out == nil {
			return nil, fmt.Errorf("tab.metrics() could not read navigation timing")
		}
		return &browser.CallResult{Value: out}, nil
	case "traceStart":
		// upstream: tern-tab.ts:3989
		return nil, unsupportedErr(product, "traceStart", "the web view exposes no performance-trace protocol")
	case "traceStop":
		// upstream: tern-tab.ts:3994
		return nil, unsupportedErr(product, "traceStop", "the web view exposes no performance-trace protocol")
	case "profileStart":
		// upstream: tern-tab.ts:3999
		return nil, unsupportedErr(product, "profileStart", "the web view exposes no CPU-profiler protocol")
	case "profileStop":
		// upstream: tern-tab.ts:4004
		return nil, unsupportedErr(product, "profileStop", "the web view exposes no CPU-profiler protocol")
	case "webmcpList":
		// upstream: tern-tab.ts:3879
		return nil, unsupportedErr(product, "webmcpList", noWebMcp(product))
	case "webmcpInvoke":
		// upstream: tern-tab.ts:3888
		return nil, unsupportedErr(product, "webmcpInvoke", noWebMcp(product))
	case "webmcpEvents":
		// upstream: tern-tab.ts:3893
		return nil, unsupportedErr(product, "webmcpEvents", noWebMcp(product))
	case "vitals", "reactEnable", "reactTree", "reactInspect", "reactRenders", "reactSuspense":
		return nil, unsupportedErr(product, step.Method, "React/Web Vitals page instrumentation is not ported in internal/ternbrowser")
	case "recordStart", "recordStop", "recordRestart", "recording":
		return nil, unsupportedErr(product, step.Method, "video recording encoder (ffmpeg) is not ported in internal/ternbrowser")

	// ─── Execution ────────────────────────────────────────────────────
	case "evaluate":
		// upstream: tern-tab.ts:1079-1090, 2870-2880 (N1: decode {__omp_fn:src}; N2: string always wrapped via UserSourceFunction, never called as a function)
		label := "tab.evaluate()"
		if frame != "" {
			label = "frame.evaluate()"
		}
		if len(args) == 0 {
			return nil, fmt.Errorf("%s expects a function or a source string", label)
		}
		var first any
		if err := json.Unmarshal(args[0], &first); err != nil {
			return nil, fmt.Errorf("%s expects a function or a source string", label)
		}
		var fnSource string
		var evalArgs []any
		if fnSrc, ok := browser.DecodePreludeFn(first); ok {
			fnSource = fnSrc
			evalArgs = make([]any, 0, max(0, len(args)-1))
			for i := 1; i < len(args); i++ {
				var v any
				_ = json.Unmarshal(args[i], &v)
				evalArgs = append(evalArgs, v)
			}
		} else if src, ok := first.(string); ok {
			fnSource = UserSourceFunction(src)
			evalArgs = nil
		} else {
			return nil, fmt.Errorf("%s expects a function or a source string", label)
		}
		var out any
		if err := t.evalUser(ctx, label, fnSource, evalArgs, frame, &out); err != nil {
			return nil, err
		}
		return &browser.CallResult{Value: out}, nil
	default:
		return nil, unsupportedErr(product, step.Method, "unknown browser helper")
	}
}

// ─── Element & query implementations ──────────────────────────────────

func (t *Tab) clickSpec(ctx context.Context, spec Selector, frame string, buttonName string, count int) error {
	btn, err := t.validateButton(buttonName)
	if err != nil {
		return err
	}
	action := "click"
	label := fmt.Sprintf("tab.click(%s)", describeSpec(spec))
	if count == 2 {
		action = "dblclick"
		label = fmt.Sprintf("tab.dblclick(%s)", describeSpec(spec))
	}
	box, _, _, err := t.target(ctx, label, spec, action, frame, 0)
	if err != nil {
		return err
	}
	return t.clickAtPoint(ctx, box.X, box.Y, btn, count)
}

func (t *Tab) hoverSpec(ctx context.Context, spec Selector, frame string) error {
	box, _, _, err := t.target(ctx, fmt.Sprintf("tab.hover(%s)", describeSpec(spec)), spec, "hover", frame, 0)
	if err != nil {
		return err
	}
	t.mu.Lock()
	t.pointer = &[2]float64{box.X, box.Y}
	t.mu.Unlock()
	return t.input(ctx, []InputStep{t.mouseStep("move", box.X, box.Y, "left", new(0))})
}

func (t *Tab) focusSpec(ctx context.Context, spec Selector, frame string) error {
	label := fmt.Sprintf("tab.focus(%s)", describeSpec(spec))
	if t.nativeDom() {
		_, ref, node, err := t.resolveTargetNative(ctx, label, spec, "focus", frame, 0, false)
		if err != nil {
			return err
		}
		if node != nil && slices.Contains(node.States, "editable") && !slices.Contains(node.States, "disabled") {
			box, _, _, err := t.resolveTargetNative(ctx, label, Selector{Engine: "ref", Ref: ref}, "type", frame, 0, false)
			if err != nil {
				return err
			}
			return t.clickAtPoint(ctx, box.X, box.Y, "left", 1)
		}
	} else {
		if _, _, _, err := t.target(ctx, label, spec, "focus", frame, 0); err != nil {
			return err
		}
	}
	return t.onKit(ctx, spec, frame, func(ks Selector, kf string) error {
		return t.kit(ctx, "focus", []any{ks}, kf, nil)
	})
}

func (t *Tab) setCheckedSpec(ctx context.Context, spec Selector, desired bool, frame string) error {
	verb := "uncheck"
	if desired {
		verb = "check"
	}
	label := fmt.Sprintf("tab.%s(%s)", verb, describeSpec(spec))
	box, _, _, err := t.target(ctx, label, spec, "check", frame, 0)
	if err != nil {
		return err
	}
	getChecked := func() (bool, error) {
		if t.nativeDom() {
			m, err := t.queryNative(ctx, spec, frame, 1, []string{"checked"}, false)
			if err != nil {
				return false, err
			}
			if len(m.Nodes) == 0 {
				return false, fmt.Errorf("No element matches %s", describeSpec(spec))
			}
			if m.Nodes[0].Checked == nil {
				return false, fmt.Errorf("tab.check() requires a checkbox, radio, or ARIA switch")
			}
			b, _ := m.Nodes[0].Checked.(bool)
			return b, nil
		}
		var res struct {
			Checked bool `json:"checked"`
		}
		if err := t.kit(ctx, "checkState", []any{spec}, frame, &res); err != nil {
			return false, err
		}
		return res.Checked, nil
	}
	cur, err := getChecked()
	if err != nil {
		return err
	}
	if cur == desired {
		return nil
	}
	if err := t.clickAtPoint(ctx, box.X, box.Y, "left", 1); err != nil {
		return err
	}
	after, err := getChecked()
	if err != nil {
		return err
	}
	if after != desired {
		stateWord := "checked"
		if desired {
			stateWord = "unchecked"
		}
		return fmt.Errorf("%s clicked the element but it is still %s", label, stateWord)
	}
	return nil
}

func (t *Tab) typeSpec(ctx context.Context, spec Selector, text string, frame string) error {
	box, _, _, err := t.target(ctx, fmt.Sprintf("tab.type(%s)", describeSpec(spec)), spec, "type", frame, 0)
	if err != nil {
		return err
	}
	if t.nativeDom() {
		if err := t.clickAtPoint(ctx, box.X, box.Y, "left", 1); err != nil {
			return err
		}
	} else {
		if err := t.kit(ctx, "caretToEnd", []any{spec}, frame, nil); err != nil {
			return err
		}
	}
	t.mu.Lock()
	held := append([]Modifier(nil), t.heldMods...)
	t.mu.Unlock()
	steps, err := TypeSteps(text, held)
	if err != nil {
		return err
	}
	return t.input(ctx, steps)
}

// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:2324-2345
func (t *Tab) fillSpec(ctx context.Context, spec Selector, text string, frame string) error {
	label := fmt.Sprintf("tab.fill(%s)", describeSpec(spec))
	if t.nativeDom() {
		m, err := t.queryNative(ctx, spec, frame, 1, []string{"attributes"}, false)
		if err != nil {
			return err
		}
		if len(m.Nodes) > 0 && isPickedControl(m.Nodes[0]) {
			_, ref, _, err := t.resolveTargetNative(ctx, label, spec, "point", frame, 0, true)
			if err != nil {
				return err
			}
			return t.onKit(ctx, Selector{Engine: "ref", Ref: ref}, frame, func(ks Selector, kf string) error {
				return t.kit(ctx, "prepareFill", []any{ks, text}, kf, nil)
			})
		}
		box, _, _, err := t.resolveTargetNative(ctx, label, spec, "fill", frame, 0, false)
		if err != nil {
			return err
		}
		if err := t.clickAtPoint(ctx, box.X, box.Y, "left", 1); err != nil {
			return err
		}
		if err := t.op(ctx, "edit", opEditReq{Op: "edit", Block: t.block, Action: "selectAll"}, nil); err != nil {
			return err
		}
	} else {
		if _, _, _, err := t.target(ctx, label, spec, "fill", frame, 0); err != nil {
			return err
		}
		var prep struct {
			Mode string `json:"mode"`
		}
		if err := t.kit(ctx, "prepareFill", []any{spec, text}, frame, &prep); err != nil {
			return err
		}
		if prep.Mode == "done" {
			return nil
		}
	}
	if len(text) > 0 {
		return t.input(ctx, []InputStep{{Type: "text", Text: text}})
	}
	t.mu.Lock()
	held := append([]Modifier(nil), t.heldMods...)
	t.mu.Unlock()
	steps, err := PressSteps("Backspace", held)
	if err != nil {
		return err
	}
	return t.input(ctx, steps)
}

func (t *Tab) pressSpec(ctx context.Context, key string, spec *Selector, frame string) error {
	if spec != nil {
		if !t.nativeDom() {
			label := fmt.Sprintf("tab.press(%s, { selector: %s })", jsonQuote(key), describeSpec(*spec))
			if _, _, _, err := t.target(ctx, label, *spec, "press", frame, 0); err != nil {
				return err
			}
		}
		if err := t.focusSpec(ctx, *spec, frame); err != nil {
			return err
		}
	}
	t.mu.Lock()
	held := append([]Modifier(nil), t.heldMods...)
	t.mu.Unlock()
	steps, err := PressSteps(key, held)
	if err != nil {
		return err
	}
	return t.input(ctx, steps)
}

func (t *Tab) selectSpec(ctx context.Context, spec Selector, choices []SelectOptionChoice, frame string) ([]string, error) {
	if _, _, _, err := t.target(ctx, fmt.Sprintf("tab.select(%s)", describeSpec(spec)), spec, "point", frame, 0); err != nil {
		return nil, err
	}
	var out []string
	err := t.onKit(ctx, spec, frame, func(ks Selector, kf string) error {
		return t.kit(ctx, "select", []any{ks, choices}, kf, &out)
	})
	return out, err
}

func (t *Tab) scrollIntoViewSpec(ctx context.Context, spec Selector, frame string) error {
	if _, _, _, err := t.target(ctx, fmt.Sprintf("tab.scrollIntoView(%s)", describeSpec(spec)), spec, "point", frame, 0); err != nil {
		return err
	}
	if !t.nativeDom() {
		return t.kit(ctx, "scrollIntoView", []any{spec}, frame, nil)
	}
	return nil
}

func (t *Tab) highlightSpec(ctx context.Context, spec Selector, opts map[string]any, frame string) error {
	durMs := 2000.0
	if f, ok := numVal(opts["duration"]); ok {
		if f < 0 {
			return fmt.Errorf("highlight duration must be a non-negative number")
		}
		durMs = f
	}
	if _, _, _, err := t.target(ctx, fmt.Sprintf("tab.highlight(%s)", describeSpec(spec)), spec, "point", frame, 0); err != nil {
		return err
	}
	id := fmt.Sprintf("omp-highlight-%d", time.Now().UnixNano())
	var overlayFrame string
	err := t.onKit(ctx, spec, frame, func(ks Selector, kf string) error {
		overlayFrame = kf
		return t.kit(ctx, "highlight", []any{ks, id}, kf, nil)
	})
	if err != nil {
		return err
	}
	defer func() {
		_ = t.kit(context.Background(), "removeOverlay", []any{id}, overlayFrame, nil)
	}()
	return sleepCtx(ctx, time.Duration(durMs)*time.Millisecond)
}

// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:2549-2582
func (t *Tab) uploadFileSpec(ctx context.Context, cwd string, spec Selector, filePaths []string, frame string) error {
	if len(filePaths) == 0 {
		return fmt.Errorf("tab.uploadFile() requires at least one file path")
	}
	absPaths := make([]string, 0, len(filePaths))
	for _, p := range filePaths {
		abs := resolveToCwd(p, cwd)
		if _, err := os.Stat(abs); err != nil {
			return fmt.Errorf("tab.uploadFile() cannot read %s", abs)
		}
		absPaths = append(absPaths, abs)
	}
	if _, _, _, err := t.target(ctx, fmt.Sprintf("tab.uploadFile(%s)", describeSpec(spec)), spec, "upload", frame, 0); err != nil {
		return err
	}
	return t.onKit(ctx, spec, frame, func(ks Selector, kf string) error {
		var isInput bool
		if err := t.kit(ctx, "isFileInput", []any{ks}, kf, &isInput); err != nil {
			return err
		}
		if isInput {
			_ = t.pull(ctx)
			t.mu.Lock()
			since := t.cursor
			t.mu.Unlock()
			if err := t.op(ctx, "files", opFilesReq{Op: "files", Block: t.block, Paths: absPaths}, nil); err != nil {
				return err
			}
			var chooser string
			if err := t.kit(ctx, "openChooser", []any{ks}, kf, &chooser); err == nil && chooser == "input" {
				answered := t.poll(ctx, "tab.uploadFile() chooser", 3*time.Second, true, func() (bool, error) {
					for _, ev := range t.eventsSince(since) {
						if ev.Type == "chooser" {
							return true, nil
						}
					}
					return false, nil
				}, nil) == nil
				if answered {
					return nil
				}
			}
			_ = t.op(ctx, "files", opFilesReq{Op: "files", Block: t.block, Paths: nil}, nil)
		}
		type filePayload struct {
			Name string `json:"name"`
			Type string `json:"type"`
			Data string `json:"data"`
		}
		payloads := make([]filePayload, 0, len(absPaths))
		for _, abs := range absPaths {
			raw, err := os.ReadFile(abs)
			if err != nil {
				return fmt.Errorf("tab.uploadFile() cannot read %s", abs)
			}
			payloads = append(payloads, filePayload{
				Name: filepath.Base(abs),
				Type: "application/octet-stream",
				Data: base64.StdEncoding.EncodeToString(raw),
			})
		}
		return t.kit(ctx, "setFiles", []any{ks, payloads}, kf, nil)
	})
}

// ─── Read helpers ─────────────────────────────────────────────────────

func (t *Tab) readTextSpec(ctx context.Context, spec Selector, frame string) (*string, error) {
	if t.nativeDom() {
		m, err := t.queryNative(ctx, spec, frame, 1, []string{"text"}, false)
		if err != nil || len(m.Nodes) == 0 {
			return nil, err
		}
		return new(strings.TrimSpace(m.Nodes[0].Text)), nil
	}
	var out *string
	err := t.onKit(ctx, spec, frame, func(ks Selector, kf string) error {
		return t.kit(ctx, "read", []any{ks, "text"}, kf, &out)
	})
	return out, err
}

func (t *Tab) readHTMLSpec(ctx context.Context, spec Selector, frame string) (*string, error) {
	if t.nativeDom() {
		m, err := t.queryNative(ctx, spec, frame, 1, []string{"html"}, false)
		if err != nil || len(m.Nodes) == 0 {
			return nil, err
		}
		return new(InnerHtmlOf(m.Nodes[0].HTML, m.Nodes[0].Tag)), nil
	}
	var out *string
	err := t.onKit(ctx, spec, frame, func(ks Selector, kf string) error {
		return t.kit(ctx, "read", []any{ks, "html"}, kf, &out)
	})
	return out, err
}

func (t *Tab) readValueSpec(ctx context.Context, spec Selector, frame string) (*string, error) {
	if t.nativeDom() {
		m, err := t.queryNative(ctx, spec, frame, 1, []string{"value"}, false)
		if err != nil || len(m.Nodes) == 0 {
			return nil, err
		}
		if m.Nodes[0].Value != "" {
			return new(m.Nodes[0].Value), nil
		}
		if formValueTags[m.Nodes[0].Tag] {
			return new(""), nil
		}
		return nil, nil
	}
	var out *string
	err := t.onKit(ctx, spec, frame, func(ks Selector, kf string) error {
		return t.kit(ctx, "read", []any{ks, "value"}, kf, &out)
	})
	return out, err
}

func (t *Tab) readAttrSpec(ctx context.Context, spec Selector, name string, frame string) (*string, error) {
	if t.nativeDom() {
		m, err := t.queryNative(ctx, spec, frame, 1, []string{"attributes"}, false)
		if err != nil || len(m.Nodes) == 0 {
			return nil, err
		}
		if v, ok := m.Nodes[0].Attributes[name]; ok {
			return new(v), nil
		}
		return nil, nil
	}
	var out *string
	err := t.onKit(ctx, spec, frame, func(ks Selector, kf string) error {
		return t.kit(ctx, "read", []any{ks, "attr", name}, kf, &out)
	})
	return out, err
}

func (t *Tab) countSpec(ctx context.Context, spec Selector, frame string) (int, error) {
	if t.nativeDom() {
		m, err := t.queryNative(ctx, spec, frame, 1, nil, false)
		return m.Total, err
	}
	var n int
	err := t.kit(ctx, "count", []any{spec}, frame, &n)
	return n, err
}

func (t *Tab) isVisibleSpec(ctx context.Context, spec Selector, frame string) (bool, error) {
	if t.nativeDom() {
		ref, _, found, err := t.locateNative(ctx, spec, frame)
		if err != nil || !found {
			return false, err
		}
		var res DomResolved
		if err := t.dom(ctx, "resolve", opDomResolveReq{
			Op: "dom", Block: t.block, Read: "resolve", Ref: ref, Action: "none", Stable: new(0),
		}, &res, 10*time.Second); err != nil {
			return false, err
		}
		return res.State != "detached" && res.State != "hidden", nil
	}
	var vis bool
	err := t.onKit(ctx, spec, frame, func(ks Selector, kf string) error {
		return t.kit(ctx, "read", []any{ks, "visible"}, kf, &vis)
	})
	return vis, err
}

func (t *Tab) isEnabledSpec(ctx context.Context, spec Selector, frame string) (bool, error) {
	if t.nativeDom() {
		m, err := t.queryNative(ctx, spec, frame, 1, nil, false)
		if err != nil || len(m.Nodes) == 0 {
			return false, err
		}
		return !slices.Contains(m.Nodes[0].States, "disabled"), nil
	}
	var en bool
	err := t.onKit(ctx, spec, frame, func(ks Selector, kf string) error {
		return t.kit(ctx, "read", []any{ks, "enabled"}, kf, &en)
	})
	return en, err
}

func (t *Tab) isCheckedSpec(ctx context.Context, spec Selector, frame string) (bool, error) {
	if t.nativeDom() {
		m, err := t.queryNative(ctx, spec, frame, 1, []string{"checked"}, false)
		if err != nil || len(m.Nodes) == 0 {
			return false, err
		}
		b, _ := m.Nodes[0].Checked.(bool)
		return b, nil
	}
	var ch bool
	err := t.onKit(ctx, spec, frame, func(ks Selector, kf string) error {
		return t.kit(ctx, "read", []any{ks, "checked"}, kf, &ch)
	})
	return ch, err
}

func (t *Tab) observe(ctx context.Context, opts map[string]any) (browser.Observation, error) {
	includeAll, _ := opts["includeAll"].(bool)
	viewportOnly, _ := opts["viewportOnly"].(bool)
	compact, _ := opts["compact"].(bool)
	selStr, _ := opts["selector"].(string)

	if t.nativeDom() {
		var rootRef string
		if selStr != "" {
			spec, err := ParseSelector(selStr)
			if err != nil {
				return browser.Observation{}, err
			}
			ref, _, found, err := t.locateNative(ctx, spec, "")
			if err != nil {
				return browser.Observation{}, err
			}
			if !found {
				return browser.Observation{}, fmt.Errorf("tab.observe: selector %s matched no element", describeSpec(spec))
			}
			rootRef = ref
		}
		var snap DomSnapshot
		req := opDomSnapshotReq{
			Op: "dom", Block: t.block, Read: "snapshot",
			Root: rootRef, IncludeAll: includeAll, ViewportOnly: viewportOnly,
		}
		if t.client.Supports("responsive") {
			req.FailUnresponsive = new(true)
		}
		if err := t.dom(ctx, "snapshot", req, &snap, 10*time.Second); err != nil {
			return browser.Observation{}, err
		}
		mapped := ObservationFrom(snap, compact)
		t.mu.Lock()
		t.observedRefs = mapped.Refs
		t.observedRects = mapped.Rects
		if mapped.Observation.URL != "" {
			t.url = mapped.Observation.URL
		}
		t.title = snap.Title
		t.mu.Unlock()
		return mapped.Observation, nil
	}

	kitOpts := map[string]any{}
	if includeAll {
		kitOpts["includeAll"] = true
	}
	if viewportOnly {
		kitOpts["viewportOnly"] = true
	}
	if compact {
		kitOpts["compact"] = true
	}
	if selStr != "" {
		spec, err := ParseSelector(selStr)
		if err != nil {
			return browser.Observation{}, err
		}
		kitOpts["root"] = spec
	}
	var obs browser.Observation
	if err := t.kit(ctx, "observe", []any{kitOpts}, "", &obs); err != nil {
		return browser.Observation{}, err
	}
	t.mu.Lock()
	if obs.URL != "" {
		t.url = obs.URL
	}
	if obs.Title != "" {
		t.title = obs.Title
	}
	t.mu.Unlock()
	return obs, nil
}

func (t *Tab) ariaSnapshot(ctx context.Context, selStr string, opts map[string]any, frame string) (string, error) {
	var rootArg any
	if selStr != "" {
		spec, err := ParseSelector(selStr)
		if err != nil {
			return "", err
		}
		rootArg = spec
	}
	reqOpts := map[string]any{}
	if d, ok := numVal(opts["depth"]); ok {
		reqOpts["depth"] = int(d)
	}
	if b, ok := opts["boxes"].(bool); ok {
		reqOpts["boxes"] = b
	}
	urls, _ := opts["urls"].(bool)
	var payload struct {
		Snapshot string            `json:"snapshot"`
		Hrefs    map[string]string `json:"hrefs"`
	}
	if err := t.kit(ctx, "ariaSnapshot", []any{rootArg, reqOpts, urls}, frame, &payload); err != nil {
		return "", err
	}
	return payload.Snapshot, nil
}

func (t *Tab) waitForSpec(ctx context.Context, label string, spec Selector, state string, frame string, timeout time.Duration) error {
	if t.nativeDom() {
		q, err := t.queryForNative(ctx, spec, frame)
		if err != nil {
			return err
		}
		if q == nil {
			if state == "hidden" || state == "detached" {
				return nil
			}
			return fmt.Errorf("%s timed out after %dms", label, timeout.Milliseconds())
		}
		var w DomWaited
		if err := t.dom(ctx, "wait", opDomWaitReq{
			Op: "dom", Block: t.block, Read: "wait",
			For: *q, State: state, Timeout: int(timeout.Milliseconds()),
		}, &w, timeout+waitReplyGrace); err != nil {
			return err
		}
		if !w.Matched {
			return fmt.Errorf("%s timed out after %dms", label, timeout.Milliseconds())
		}
		return nil
	}
	return t.poll(ctx, label, timeout, false, func() (bool, error) {
		switch state {
		case "hidden":
			cnt, err := t.countSpec(ctx, spec, frame)
			if err != nil {
				return false, err
			}
			if cnt == 0 {
				return true, nil
			}
			vis, err := t.isVisibleSpec(ctx, spec, frame)
			return !vis, err
		case "visible":
			return t.isVisibleSpec(ctx, spec, frame)
		default:
			cnt, err := t.countSpec(ctx, spec, frame)
			return cnt > 0, err
		}
	}, nil)
}

type frameInfo struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	URL      string  `json:"url"`
	ParentID *string `json:"parentId"`
	Selector string  `json:"selector,omitempty"`
}

func (t *Tab) listFrames(ctx context.Context) ([]frameInfo, error) {
	_ = t.pull(ctx)
	var children []struct {
		Path string `json:"path"`
		Name string `json:"name"`
		ID   string `json:"id"`
		Src  string `json:"src"`
		URL  string `json:"url"`
	}
	if err := t.kit(ctx, "frames", nil, "", &children); err != nil {
		return nil, err
	}
	t.mu.Lock()
	mainURL := t.url
	frameURLs := make(map[string]string, len(t.frameURLs))
	for k, v := range t.frameURLs {
		frameURLs[k] = v
	}
	t.mu.Unlock()

	res := []frameInfo{{ID: "main", Name: "", URL: mainURL, ParentID: nil}}
	for _, c := range children {
		parent := "main"
		if idx := strings.LastIndex(c.Path, "."); idx >= 0 {
			parent = c.Path[:idx]
		}
		name := c.Name
		if name == "" {
			name = c.ID
		}
		u := c.URL
		if u == "" {
			u = frameURLs[c.Path]
		}
		if u == "" {
			u = c.Src
		}
		fi := frameInfo{ID: c.Path, Name: name, URL: u, ParentID: new(parent)}
		if c.ID != "" {
			fi.Selector = fmt.Sprintf("iframe[id=%s]", jsonQuote(c.ID))
		} else if c.Name != "" {
			fi.Selector = fmt.Sprintf("iframe[name=%s]", jsonQuote(c.Name))
		}
		res = append(res, fi)
	}
	return res, nil
}

// emulate validates all options up front before mutating anything (fixes S1, A8).
// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:3292-3387
func (t *Tab) emulate(ctx context.Context, opts map[string]any) (map[string]any, error) {
	product := t.product
	if v, exists := opts["timezone"]; exists && v != nil {
		return nil, unsupportedErr(product, "emulate", "timezone cannot be overridden in the web view (Date keeps the system time zone)")
	}
	if v, exists := opts["headers"]; exists && v != nil {
		if m, ok := v.(map[string]any); ok && len(m) > 0 {
			return nil, unsupportedErr(product, "emulate", "extra HTTP headers cannot be added to the web view's navigations and subresources")
		}
	}
	if vp, ok := opts["viewport"].(map[string]any); ok {
		if isMob, _ := vp["isMobile"].(bool); isMob {
			return nil, unsupportedErr(product, "emulate", "the web view has no mobile or touch viewport emulation")
		}
		if hasTouch, _ := vp["hasTouch"].(bool); hasTouch {
			return nil, unsupportedErr(product, "emulate", "the web view has no mobile or touch viewport emulation")
		}
	}
	if v, exists := opts["media"]; exists && v != nil {
		return nil, unsupportedErr(product, "emulate", "the CSS media type cannot be overridden in the web view")
	}
	if _, exists := opts["reducedMotion"]; exists {
		return nil, unsupportedErr(product, "emulate", "prefers-reduced-motion cannot be overridden in the web view (CSS media queries follow the system)")
	}
	if v, exists := opts["cpuThrottling"]; exists && v != nil {
		if f, ok := numVal(v); !ok || f != 1 {
			return nil, unsupportedErr(product, "emulate", "CPU throttling needs the Chromium DevTools protocol")
		}
	}
	if v, exists := opts["network"]; exists && v != nil {
		return nil, unsupportedErr(product, "emulate", "network throttling needs the Chromium DevTools protocol (use offline: true for offline)")
	}
	if dev, ok := opts["device"].(string); ok && dev != "" {
		return nil, unsupportedErr(product, "emulate", "Puppeteer device catalog is not bundled in oh-my-pig")
	}

	pageChanged := false
	if vp, ok := opts["viewport"].(map[string]any); ok {
		wF, okW := numVal(vp["width"])
		hF, okH := numVal(vp["height"])
		if !okW || !okH || wF <= 0 || hF <= 0 {
			return nil, fmt.Errorf("tab.emulate() viewport requires positive width and height")
		}
		scale := 1.0
		if s, ok := numVal(vp["scale"]); ok && s > 0 {
			scale = s
		} else if s, ok := numVal(vp["deviceScaleFactor"]); ok && s > 0 {
			scale = s
		}
		if err := t.SetViewport(ctx, browser.Viewport{
			Width:             int(math.Round(wF)),
			Height:            int(math.Round(hF)),
			DeviceScaleFactor: scale,
		}); err != nil {
			return nil, err
		}
		t.mu.Lock()
		t.emulation["viewport"] = map[string]any{"width": int(math.Round(wF)), "height": int(math.Round(hF)), "scale": scale}
		t.mu.Unlock()
	}
	if rawUA, exists := opts["userAgent"]; exists {
		var uaPtr *string
		if s, ok := rawUA.(string); ok {
			uaPtr = new(s)
		}
		if err := t.op(ctx, "agent", opAgentReq{Op: "agent", Block: t.block, Value: uaPtr}, nil); err != nil {
			return nil, err
		}
		t.mu.Lock()
		t.emulation["userAgent"] = rawUA
		t.mu.Unlock()
	}
	if rawCS, exists := opts["colorScheme"]; exists {
		var csPtr *string
		if s, ok := rawCS.(string); ok && s != "" && s != "no-preference" {
			csPtr = new(s)
		}
		if err := t.op(ctx, "appearance", opAppearanceReq{Op: "appearance", Block: t.block, Value: csPtr}, nil); err != nil {
			return nil, err
		}
		t.mu.Lock()
		t.emulation["colorScheme"] = rawCS
		t.mu.Unlock()
	}
	if rawCred, exists := opts["credentials"]; exists {
		req := opCredentialsReq{Op: "credentials", Block: t.block}
		if m, ok := rawCred.(map[string]any); ok {
			req.Username, _ = m["username"].(string)
			req.Password, _ = m["password"].(string)
		}
		if err := t.op(ctx, "credentials", req, nil); err != nil {
			return nil, err
		}
		t.mu.Lock()
		t.emulation["credentials"] = rawCred
		t.mu.Unlock()
	}
	if rawGeo, exists := opts["geolocation"]; exists {
		t.mu.Lock()
		if m, ok := rawGeo.(map[string]any); ok {
			lat, _ := numVal(m["latitude"])
			lon, _ := numVal(m["longitude"])
			g := &GeolocationCoords{Latitude: lat, Longitude: lon}
			if acc, ok := numVal(m["accuracy"]); ok {
				g.Accuracy = new(acc)
			}
			t.pageEmulation.Geolocation = g
		} else {
			t.pageEmulation.Geolocation = nil
		}
		t.emulation["geolocation"] = rawGeo
		t.mu.Unlock()
		pageChanged = true
	}
	if rawLoc, exists := opts["locale"]; exists {
		t.mu.Lock()
		if s, ok := rawLoc.(string); ok {
			t.pageEmulation.Locale = s
		} else {
			t.pageEmulation.Locale = ""
		}
		t.emulation["locale"] = rawLoc
		t.mu.Unlock()
		pageChanged = true
	}
	if rawOff, exists := opts["offline"]; exists {
		b, _ := rawOff.(bool)
		t.mu.Lock()
		t.pageEmulation.Offline = new(b)
		t.emulation["offline"] = b
		t.mu.Unlock()
		pageChanged = true
	}
	if pageChanged {
		if err := t.applyCapture(ctx); err != nil {
			return nil, err
		}
	}
	t.mu.Lock()
	out := make(map[string]any, len(t.emulation))
	for k, v := range t.emulation {
		out[k] = v
	}
	t.mu.Unlock()
	return out, nil
}

func (t *Tab) readConsole(ctx context.Context, kind string, opts map[string]any) (map[string]any, error) {
	if err := t.pull(ctx); err != nil {
		return nil, err
	}
	since := 0
	if f, ok := numVal(opts["since"]); ok {
		since = int(f)
	}
	limit := consoleLimit
	if f, ok := numVal(opts["limit"]); ok && f >= 0 {
		limit = int(f)
	}
	level, _ := opts["level"].(string)
	clearAfter, _ := opts["clear"].(bool)

	t.mu.Lock()
	defer t.mu.Unlock()
	entries := make([]ConsoleEntry, 0)
	for _, e := range t.consoleEntries {
		if e.Seq <= since {
			continue
		}
		if kind == "console" {
			if e.Type != "console" || (level != "" && e.Level != level) {
				continue
			}
		} else {
			if e.Type == "console" {
				continue
			}
		}
		entries = append(entries, e)
		if len(entries) >= limit {
			break
		}
	}
	nextSeq := t.consoleSeq - 1
	if len(entries) > 0 {
		nextSeq = entries[len(entries)-1].Seq
	}
	dropped := t.consoleDropped
	if clearAfter {
		t.consoleEntries = nil
		t.consoleDropped = 0
	}
	return map[string]any{
		"entries": entries,
		"nextSeq": nextSeq,
		"dropped": dropped,
	}, nil
}

func (t *Tab) evaluateOnElement(ctx context.Context, spec Selector, fnSrc string, extra []any, frame string) (any, error) {
	var out any
	err := t.onKit(ctx, spec, frame, func(ks Selector, kf string) error {
		var token string
		if err := t.kit(ctx, "mark", []any{ks}, kf, &token); err != nil {
			return err
		}
		if token == "" {
			return fmt.Errorf("Element handle selector no longer resolves")
		}
		defer func() { _ = t.kit(context.Background(), "unmark", []any{token}, kf, nil) }()
		wrapper := fmt.Sprintf(`async function (token, args) {
			const find = root => {
				const direct = root.querySelector('[%s~="' + CSS.escape(token) + '"]');
				if (direct) return direct;
				for (const node of root.querySelectorAll("*")) {
					if (node.shadowRoot) {
						const found = find(node.shadowRoot);
						if (found) return found;
					}
				}
				return null;
			};
			const element = find(document);
			if (!element) throw new Error("Element handle selector no longer resolves");
			return await (%s)(element, ...args);
		}`, HandleAttribute, fnSrc)
		err := t.eval(ctx, wrapper, []any{token, extra}, "page", kf, &out)
		if err != nil {
			var te *ternwire.Error
			if errors.As(err, &te) && te.Kind == ternwire.ErrJS {
				return fmt.Errorf("elementHandle.evaluate() threw a JavaScript exception:\n%s", pageErrorText(te))
			}
		}
		return err
	})
	return out, err
}

func (t *Tab) currentPointerOrCentre(ctx context.Context) (float64, float64, error) {
	t.mu.Lock()
	p := t.pointer
	t.mu.Unlock()
	if p != nil {
		return p[0], p[1], nil
	}
	return t.viewportCentre(ctx)
}

func (t *Tab) resolveDragTarget(ctx context.Context, args []json.RawMessage, idx int, role string) (float64, float64, error) {
	if s, ok, err := argString(args, idx); err == nil && ok {
		spec, err := ParseSelector(s)
		if err != nil {
			return 0, 0, err
		}
		box, _, _, err := t.target(ctx, fmt.Sprintf("tab.drag() %s %s", role, jsonQuote(s)), spec, "drag", "", 0)
		if err != nil {
			return 0, 0, err
		}
		return box.X, box.Y, nil
	}
	if m, ok, err := argMap(args, idx); err == nil && ok {
		x, okX := numVal(m["x"])
		y, okY := numVal(m["y"])
		if okX && okY {
			css, _ := m["css"].(bool)
			t.mu.Lock()
			vx, vy, err := t.pointerSpace.ToViewport(x, y, css)
			t.mu.Unlock()
			return vx, vy, err
		}
	}
	return 0, 0, fmt.Errorf("Drag target must be a selector string or { x: number, y: number } point")
}

func describeSpec(spec Selector) string {
	if spec.Engine == "css" {
		return jsonQuote(spec.Query)
	}
	b, _ := json.Marshal(spec)
	return string(b)
}

func parseOptionalSelectorAndOpts(args []json.RawMessage) (string, map[string]any, error) {
	if len(args) == 0 {
		return "", nil, nil
	}
	if m, ok, err := argMap(args, 0); err == nil && ok && len(args) == 1 {
		return "", m, nil
	}
	sel, _, err := argString(args, 0)
	if err != nil {
		return "", nil, err
	}
	opts, _, _ := argMap(args, 1)
	return sel, opts, nil
}

func parseFilePathsArgs(args []json.RawMessage, startIdx int) ([]string, error) {
	var out []string
	for i := startIdx; i < len(args); i++ {
		var arr []string
		if err := json.Unmarshal(args[i], &arr); err == nil {
			out = append(out, arr...)
			continue
		}
		var s string
		if err := json.Unmarshal(args[i], &s); err == nil && s != "" {
			out = append(out, s)
		}
	}
	return out, nil
}

func waitTimeoutFromOpts(opts map[string]any, def time.Duration) time.Duration {
	if f, ok := numVal(opts["timeout"]); ok && f > 0 {
		return time.Duration(f) * time.Millisecond
	}
	return def
}

// WaitForResponse waits for a matching fetch/XHR/navigation response after now.
// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:3768-3795
func (t *Tab) WaitForResponse(ctx context.Context, pattern string, timeout time.Duration) (*RequestRecord, error) {
	if err := t.pull(ctx); err != nil {
		return nil, err
	}
	cursor := t.netLog.SettleCursor()
	var matched *RequestRecord
	err := t.poll(ctx, "tab.waitForResponse()", timeout, true, func() (bool, error) {
		for _, rec := range t.netLog.SettledAfter(cursor) {
			if rec.SettledSeq > cursor {
				cursor = rec.SettledSeq
			}
			if rec.Status == nil {
				continue
			}
			if strings.Contains(rec.URL, pattern) {
				matched = rec
				return true, nil
			}
		}
		return false, nil
	}, nil)
	return matched, err
}
