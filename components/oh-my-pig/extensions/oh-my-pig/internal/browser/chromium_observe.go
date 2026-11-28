package browser

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// --- Accessibility tree observation (upstream: tab-worker.ts:1832-1890, 3910-3960) ---

type cdpAXValue struct {
	Type  string `json:"type"`
	Value any    `json:"value"`
}

type cdpAXProperty struct {
	Name  string     `json:"name"`
	Value cdpAXValue `json:"value"`
}

type cdpAXNode struct {
	NodeID           string          `json:"nodeId"`
	Ignored          bool            `json:"ignored"`
	Role             *cdpAXValue     `json:"role"`
	Name             *cdpAXValue     `json:"name"`
	Description      *cdpAXValue     `json:"description"`
	Value            *cdpAXValue     `json:"value"`
	Properties       []cdpAXProperty `json:"properties"`
	ChildIDs         []string        `json:"childIds"`
	BackendDOMNodeID int             `json:"backendDOMNodeId"`
	FrameID          string          `json:"frameId"`
}

var observeInteractiveRoles = map[string]bool{
	"button": true, "link": true, "textbox": true, "combobox": true, "listbox": true,
	"checkbox": true, "radio": true, "tab": true, "menuitem": true,
	"menuitemcheckbox": true, "menuitemradio": true, "slider": true, "switch": true,
	"option": true, "spinbutton": true, "searchbox": true, "treeitem": true,
	"disclosuretriangle": true,
}

func axString(v *cdpAXValue) string {
	if v == nil || v.Value == nil {
		return ""
	}
	s, ok := v.Value.(string)
	if ok {
		return s
	}
	return fmt.Sprint(v.Value)
}

func axRole(n *cdpAXNode) string {
	return axString(n.Role)
}

func isInteractiveAXRole(role string) bool {
	return observeInteractiveRoles[strings.ToLower(role)]
}

func (t *cdpTab) observeAXTree(ctx context.Context, opts map[string]any) (*CallResult, error) {
	var (
		selector     string
		compact      bool
		viewportOnly bool
		includeAll   bool
	)
	if opts != nil {
		selector, _ = opts["selector"].(string)
		compact, _ = opts["compact"].(bool)
		viewportOnly, _ = opts["viewportOnly"].(bool)
		includeAll, _ = opts["includeAll"].(bool)
	}

	var scopeBackendID int
	if selector != "" {
		if err := ValidateSelector(selector); err != nil {
			return nil, err
		}
		objID, err := t.evalKitReadObjectID(ctx, frameTarget{}, fmt.Sprintf("globalThis.__ompBrowserKit.queryFirst(%s)", jsLit(selector)))
		if err != nil {
			return nil, err
		}
		if objID == "" {
			return nil, fmt.Errorf("tab.observe: selector %s matched no element", jsLit(selector))
		}
		defer func() {
			relCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			_ = t.send(relCtx, "Runtime.releaseObject", map[string]any{"objectId": objID}, nil)
			cancel()
		}()
		var desc struct {
			Node struct {
				BackendNodeID int `json:"backendNodeId"`
			} `json:"node"`
		}
		if err := t.send(ctx, "DOM.describeNode", map[string]any{"objectId": objID}, &desc); err != nil {
			return nil, err
		}
		scopeBackendID = desc.Node.BackendNodeID
	}

	_ = t.send(ctx, "Accessibility.enable", nil, nil)
	var axResp struct {
		Nodes []cdpAXNode `json:"nodes"`
	}
	if err := t.send(ctx, "Accessibility.getFullAXTree", nil, &axResp); err != nil {
		return nil, fmt.Errorf("Accessibility snapshot unavailable: %w", err)
	}

	byID := make(map[string]*cdpAXNode, len(axResp.Nodes))
	for i := range axResp.Nodes {
		n := &axResp.Nodes[i]
		byID[n.NodeID] = n
	}

	var rootNode *cdpAXNode
	if scopeBackendID > 0 {
		for i := range axResp.Nodes {
			if axResp.Nodes[i].BackendDOMNodeID == scopeBackendID {
				rootNode = &axResp.Nodes[i]
				break
			}
		}
	}
	if rootNode == nil && len(axResp.Nodes) > 0 {
		rootNode = &axResp.Nodes[0]
	}

	// Precompute which ancestors contain interactive descendants (for compact mode).
	interactiveAncestors := map[string]bool{}
	var markInteractive func(n *cdpAXNode) bool
	markInteractive = func(n *cdpAXNode) bool {
		if n == nil {
			return false
		}
		found := false
		for _, cid := range n.ChildIDs {
			ch := byID[cid]
			if ch == nil {
				continue
			}
			descHas := markInteractive(ch)
			if (!ch.Ignored && isInteractiveAXRole(axRole(ch))) || descHas {
				found = true
			}
		}
		if found {
			interactiveAncestors[n.NodeID] = true
		}
		return found
	}
	if compact && rootNode != nil {
		markInteractive(rootNode)
	}

	// Read scroll/viewport metrics from main frame.
	scrollRaw, _ := t.evalValue(ctx, `({
		url: location.href,
		title: document.title || "",
		x: window.scrollX || 0,
		y: window.scrollY || 0,
		width: window.innerWidth || 0,
		height: window.innerHeight || 0,
		dpr: window.devicePixelRatio || 1,
		scrollWidth: document.documentElement ? document.documentElement.scrollWidth : 0,
		scrollHeight: document.documentElement ? document.documentElement.scrollHeight : 0
	})`, 0)
	var (
		pageURL, pageTitle string
		vp                 = t.Info().Viewport
		scroll             ScrollState
	)
	if m, ok := scrollRaw.(map[string]any); ok {
		pageURL, _ = m["url"].(string)
		pageTitle, _ = m["title"].(string)
		scroll.X, _ = m["x"].(float64)
		scroll.Y, _ = m["y"].(float64)
		scroll.Width, _ = m["width"].(float64)
		scroll.Height, _ = m["height"].(float64)
		scroll.ScrollWidth, _ = m["scrollWidth"].(float64)
		scroll.ScrollHeight, _ = m["scrollHeight"].(float64)
		if scroll.Width > 0 {
			vp.Width = int(scroll.Width)
		}
		if scroll.Height > 0 {
			vp.Height = int(scroll.Height)
		}
		if dpr, ok := m["dpr"].(float64); ok && dpr > 0 {
			vp.DeviceScaleFactor = dpr
		}
		t.mu.Lock()
		if pageURL != "" {
			t.url = pageURL
		}
		t.title = pageTitle
		t.viewport = vp
		t.mu.Unlock()
	}

	t.mu.Lock()
	t.docGen++
	gen := t.docGen
	t.observed = make(map[int]observedElement)
	t.mu.Unlock()

	entries := make([]ObservationEntry, 0, 32)
	nextID := 1
	visited := map[string]bool{}

	var walk func(n *cdpAXNode)
	walk = func(n *cdpAXNode) {
		if n == nil || visited[n.NodeID] {
			return
		}
		visited[n.NodeID] = true

		role := axRole(n)
		roleLower := strings.ToLower(role)
		name := axString(n.Name)
		interactive := !n.Ignored && isInteractiveAXRole(role)
		emptyStructural := (roleLower == "generic" || roleLower == "none" || roleLower == "group") && name == "" && !interactiveAncestors[n.NodeID]
		if !n.Ignored && n.BackendDOMNodeID > 0 && (includeAll || interactive) && !(compact && emptyStructural) {
			inViewport := true
			if viewportOnly {
				inViewport = t.isBackendNodeInViewport(ctx, n.BackendDOMNodeID, float64(vp.Width), float64(vp.Height))
			}
			if inViewport {
				id := nextID
				nextID++
				t.mu.Lock()
				t.observed[id] = observedElement{
					backendNodeID: n.BackendDOMNodeID,
					frameID:       n.FrameID,
					docGen:        gen,
				}
				t.mu.Unlock()

				entry := ObservationEntry{
					ID:     id,
					Role:   role,
					Name:   name,
					States: []string{},
				}
				if n.Value != nil && n.Value.Value != nil {
					if s, ok := n.Value.Value.(string); !ok || s != "" {
						entry.Value = n.Value.Value
					}
				}
				if desc := axString(n.Description); desc != "" {
					entry.Description = desc
				}
				propMap := make(map[string]any, len(n.Properties))
				for _, prop := range n.Properties {
					if prop.Name == "keyshortcuts" {
						if ks := axString(&prop.Value); ks != "" {
							entry.Keyshortcuts = ks
						}
					} else {
						propMap[prop.Name] = prop.Value.Value
					}
				}
				// Emit states in omp's fixed order (tab-worker.ts:1862-1873).
				if b, ok := propMap["disabled"].(bool); ok && b {
					entry.States = append(entry.States, "disabled")
				}
				for _, kv := range []string{"checked", "pressed", "selected", "expanded"} {
					if v, ok := propMap[kv]; ok && v != nil {
						entry.States = append(entry.States, kv+"="+fmt.Sprint(v))
					}
				}
				for _, flag := range []string{"required", "readonly", "multiselectable", "multiline", "modal", "focused"} {
					if b, ok := propMap[flag].(bool); ok && b {
						entry.States = append(entry.States, flag)
					}
				}
				entries = append(entries, entry)
			}
		}

		for _, cid := range n.ChildIDs {
			walk(byID[cid])
		}
	}
	walk(rootNode)

	obs := Observation{
		URL:      pageURL,
		Title:    pageTitle,
		Viewport: vp,
		Scroll:   scroll,
		Elements: entries,
	}
	return &CallResult{Value: obs}, nil
}

func (t *cdpTab) isBackendNodeInViewport(ctx context.Context, backendNodeID int, vw, vh float64) bool {
	var boxResp struct {
		Model struct {
			Content []float64 `json:"content"`
		} `json:"model"`
	}
	if err := t.send(ctx, "DOM.getBoxModel", map[string]any{"backendNodeId": backendNodeID}, &boxResp); err != nil {
		return false
	}
	c := boxResp.Model.Content
	if len(c) < 8 {
		return false
	}
	minX, maxX := c[0], c[0]
	minY, maxY := c[1], c[1]
	for i := 2; i < 8; i += 2 {
		if c[i] < minX {
			minX = c[i]
		}
		if c[i] > maxX {
			maxX = c[i]
		}
		if c[i+1] < minY {
			minY = c[i+1]
		}
		if c[i+1] > maxY {
			maxY = c[i+1]
		}
	}
	return maxX >= 0 && minX <= vw && maxY >= 0 && minY <= vh && (maxX-minX) > 0 && (maxY-minY) > 0
}

// resolveObservedNode resolves an observe() id to a CDP Runtime RemoteObjectId.
// Returns omp's exact error messages for unknown vs stale ids.
// upstream: #resolveCachedHandle in tab-worker.ts:4268-4283
func (t *cdpTab) resolveObservedNode(ctx context.Context, id int, execCtxID int64) (string, error) {
	t.mu.Lock()
	obs, exists := t.observed[id]
	curGen := t.docGen
	t.mu.Unlock()
	if !exists {
		return "", fmt.Errorf("Unknown element id %d. Run tab.observe() to refresh the element list.", id)
	}
	if obs.docGen != curGen {
		return "", fmt.Errorf("Element id %d is stale. Run tab.observe() again.", id)
	}
	resolveParams := map[string]any{"backendNodeId": obs.backendNodeID}
	if execCtxID > 0 {
		resolveParams["executionContextId"] = execCtxID
	}
	var resp struct {
		Object struct {
			ObjectID string `json:"objectId"`
		} `json:"object"`
	}
	if err := t.send(ctx, "DOM.resolveNode", resolveParams, &resp); err != nil || resp.Object.ObjectID == "" {
		return "", fmt.Errorf("Element id %d is stale. Run tab.observe() again.", id)
	}
	// Verify the node is still connected; resolve text nodes to parentElement.
	var checkResp struct {
		Result struct {
			ObjectID string `json:"objectId"`
			Subtype  string `json:"subtype"`
		} `json:"result"`
	}
	if err := t.send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId": resp.Object.ObjectID,
		"functionDeclaration": `function() {
			const el = this.nodeType === 3 ? this.parentElement : this;
			if (!el || !el.isConnected) return null;
			return el;
		}`,
		"returnByValue": false,
	}, &checkResp); err != nil || checkResp.Result.ObjectID == "" || checkResp.Result.Subtype == "null" {
		return "", fmt.Errorf("Element id %d is stale. Run tab.observe() again.", id)
	}
	return checkResp.Result.ObjectID, nil
}

// --- Screenshot sizing, validation, and omp formatting (upstream: screenshot.ts) ---

const screenshotMaxEdge = 1568

// downscaleImageToEdge downscales a PNG/JPEG image in Go stdlib so its longest
// edge does not exceed maxEdge. Returns (bytes, mimeType, width, height).
func downscaleImageToEdge(raw []byte, format string, maxEdge int) ([]byte, string, int, int) {
	mime := "image/png"
	if format == "jpeg" {
		mime = "image/jpeg"
	} else if format == "webp" {
		mime = "image/webp"
	}
	if format == "webp" {
		// Go stdlib has no webp encoder; keep original bytes.
		return raw, mime, 0, 0
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return raw, mime, 0, 0
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxEdge && h <= maxEdge {
		return raw, mime, w, h
	}
	scale := float64(maxEdge) / float64(max(w, h))
	nw := max(1, int(math.Round(float64(w)*scale)))
	nh := max(1, int(math.Round(float64(h)*scale)))
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	// Box/nearest downscale using stdlib image.
	for y := range nh {
		sy := b.Min.Y + int(float64(y)/scale)
		if sy >= b.Max.Y {
			sy = b.Max.Y - 1
		}
		for x := range nw {
			sx := b.Min.X + int(float64(x)/scale)
			if sx >= b.Max.X {
				sx = b.Max.X - 1
			}
			dst.Set(x, y, img.At(sx, sy))
		}
	}
	var buf bytes.Buffer
	if format == "jpeg" {
		_ = jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 75})
	} else {
		_ = png.Encode(&buf, dst)
	}
	return buf.Bytes(), mime, nw, nh
}

func (t *cdpTab) captureScreenshot(ctx context.Context, callCtx CallContext, args []any, ft frameTarget) (*CallResult, error) {
	var opts map[string]any
	if len(args) > 0 {
		switch v := args[0].(type) {
		case map[string]any:
			opts = v
		case string:
			opts = map[string]any{"selector": v}
		case nil:
		default:
			return nil, errors.New("tab.screenshot() expects an options object")
		}
	}
	if opts != nil {
		if err := validateScreenshotOpts(opts); err != nil {
			return nil, err
		}
	}

	var (
		selector string
		fullPage bool
		silent   bool
		format   = "png"
		quality  int
		outPath  string
	)
	if opts != nil {
		selector, _ = opts["selector"].(string)
		fullPage, _ = opts["fullPage"].(bool)
		silent, _ = opts["silent"].(bool)
		if f, ok := opts["format"].(string); ok && f != "" {
			format = f
		}
		if q, ok := opts["quality"].(float64); ok {
			quality = int(q)
		}
		outPath, _ = opts["path"].(string)
	}

	_ = t.refreshMeta(ctx)
	t.mu.Lock()
	vp := t.viewport
	t.mu.Unlock()
	dpr := vp.DeviceScaleFactor
	if dpr <= 0 {
		dpr = 1
	}

	params := map[string]any{
		"format": format,
	}
	if (format == "jpeg" || format == "webp") && quality > 0 {
		params["quality"] = quality
	}

	area := "viewport"
	if fullPage {
		area = "fullPage"
		params["captureBeyondViewport"] = true
		var metrics struct {
			ContentSize struct {
				Width  float64 `json:"width"`
				Height float64 `json:"height"`
			} `json:"contentSize"`
		}
		if err := t.send(ctx, "Page.getLayoutMetrics", nil, &metrics); err == nil && metrics.ContentSize.Width > 0 && metrics.ContentSize.Height > 0 {
			params["clip"] = map[string]any{
				"x":      0,
				"y":      0,
				"width":  metrics.ContentSize.Width,
				"height": metrics.ContentSize.Height,
				"scale":  1.0 / dpr,
			}
		}
	} else if selector != "" {
		area = "element"
		elExpr, err := t.selectorExpr(selector)
		if err != nil {
			return nil, err
		}
		v, err := t.evalKitRead(ctx, ft, fmt.Sprintf(`(function(el){ el.scrollIntoView({block:"center",inline:"center"}); const r = el.getBoundingClientRect(); return {x:r.x+window.scrollX,y:r.y+window.scrollY,width:r.width,height:r.height}; })(%s)`, elExpr))
		if err != nil {
			return nil, err
		}
		if m, ok := v.(map[string]any); ok {
			x, _ := m["x"].(float64)
			y, _ := m["y"].(float64)
			params["clip"] = map[string]any{
				"x":      x + ft.offsetX,
				"y":      y + ft.offsetY,
				"width":  m["width"],
				"height": m["height"],
				"scale":  1.0 / dpr,
			}
		}
	} else {
		// Viewport capture: request 1x CSS pixels by passing clip with scale = 1/dpr.
		// upstream: screenshot.ts:302-329 ("model-visible image is in CSS pixels")
		var metrics struct {
			VisualViewport struct {
				PageX float64 `json:"pageX"`
				PageY float64 `json:"pageY"`
			} `json:"visualViewport"`
		}
		_ = t.send(ctx, "Page.getLayoutMetrics", nil, &metrics)
		if vp.Width > 0 && vp.Height > 0 {
			params["clip"] = map[string]any{
				"x":      metrics.VisualViewport.PageX,
				"y":      metrics.VisualViewport.PageY,
				"width":  float64(vp.Width),
				"height": float64(vp.Height),
				"scale":  1.0 / dpr,
			}
		}
	}

	// Match omp whileDrawing + captureAfterFrame (screenshot.ts:363-504):
	// attach a separate CDP session for the tiny screencast, wait up to 1500ms
	// for a render frame, and if the renderer drew none, attempt fromSurface:false
	// capped at 5000ms before failing with omp's exact named error.
	var resp struct {
		Data string `json:"data"`
	}
	capErr := t.captureScreenshotWhileDrawing(ctx, params, &resp)
	if capErr != nil {
		return nil, capErr
	}
	rawBytes, err := base64.StdEncoding.DecodeString(resp.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to decode screenshot: %w", err)
	}

	// Cap delivered image to model-safe max edge (1568px).
	deliveredBytes, mime, imgW, imgH := downscaleImageToEdge(rawBytes, format, screenshotMaxEdge)
	if imgW == 0 {
		imgW, imgH = vp.Width, vp.Height
	}

	kept := outPath != "" || callCtx.ScreenshotDir != ""
	savedBytes := deliveredBytes
	if kept {
		savedBytes = rawBytes
	}
	ext := format
	if ext == "jpeg" {
		ext = "jpg"
	}
	var dest string
	if outPath != "" {
		dest = resolveToCwd(outPath, callCtx.Cwd)
		_ = os.MkdirAll(filepath.Dir(dest), 0o755)
	} else if callCtx.ScreenshotDir != "" {
		_ = os.MkdirAll(callCtx.ScreenshotDir, 0o755)
		prefix := "screenshot"
		if ft.frameID != "" {
			prefix = "frame-screenshot"
		}
		ts := time.Now().UTC().Format("2006-01-02T15-04-05.000")
		dest = filepath.Join(callCtx.ScreenshotDir, fmt.Sprintf("%s-%s.%s", prefix, ts, ext))
	} else {
		prefix := "omp-sshots"
		if ft.frameID != "" {
			prefix = "omp-frame-sshots"
		}
		dest = filepath.Join(os.TempDir(), fmt.Sprintf("%s-%d.%s", prefix, time.Now().UnixNano(), ext))
	}
	if err := os.WriteFile(dest, savedBytes, 0o644); err != nil {
		return nil, err
	}

	sr := ScreenshotResult{
		Dest:     dest,
		MimeType: mime,
		Bytes:    len(savedBytes),
		Width:    imgW,
		Height:   imgH,
	}
	res := &CallResult{
		Value:       dest,
		Screenshots: []ScreenshotResult{sr},
	}
	if !silent {
		// Record pointer frame for viewport screenshots if image size differs from CSS viewport.
		var pf *PointerFrame
		if area == "viewport" && vp.Width > 0 && vp.Height > 0 {
			if math.Abs(float64(imgW-vp.Width)) > 1 || math.Abs(float64(imgH-vp.Height)) > 1 {
				pf = &PointerFrame{
					ViewportWidth:  vp.Width,
					ViewportHeight: vp.Height,
					Width:          float64(imgW),
					Height:         float64(imgH),
				}
			}
			t.mu.Lock()
			t.pointer.Use(pf)
			t.mu.Unlock()
		}
		line := fmt.Sprintf("Screenshot %dx%d", imgW, imgH)
		if kept {
			line += ", full size saved to " + dest
		}
		line += "."
		if pf != nil {
			line += fmt.Sprintf(
				" This image is the %dx%d viewport rescaled: clickAt, mouseMove and drag points are positions on this image. Pass {css: true} with a position measured on the page (box, evaluate) instead of rescaling it.",
				pf.ViewportWidth, pf.ViewportHeight,
			)
		}
		res.Text = line
		res.Images = []ImageOutput{{
			MimeType: mime,
			Data:     base64.StdEncoding.EncodeToString(deliveredBytes),
		}}
	}
	return res, nil
}

const (
	// renderFrameWaitMs matches omp's RENDER_FRAME_WAIT_MS (screenshot.ts:364).
	renderFrameWaitMs = 1500
	// stalledCaptureMs matches omp's STALLED_CAPTURE_MS (screenshot.ts:366).
	stalledCaptureMs = 5000
)

// waitForRenderFrame waits up to renderFrameWaitMs (1500ms) for the page to
// draw an animation frame before a capture (matching omp screenshot.ts:375-403).
// Hidden pages resolve true at once; visible pages that draw no frame within
// 1500ms resolve false so captureAfterFrame falls back to fromSurface:false
// capped at 5000ms instead of hanging until the 20s quickOp ceiling.
func (t *cdpTab) waitForRenderFrame(ctx context.Context) (bool, error) {
	expr := fmt.Sprintf(`new Promise(resolve => {
		if (document.visibilityState === "hidden") return resolve(true);
		const finish = (drew) => {
			document.removeEventListener("visibilitychange", done);
			resolve(drew);
		};
		const done = () => finish(true);
		document.addEventListener("visibilitychange", done);
		requestAnimationFrame(done);
		setTimeout(() => finish(false), %d);
	})`, renderFrameWaitMs)
	v, err := t.evalValue(ctx, expr, 0)
	if err != nil {
		return false, err
	}
	drew, ok := v.(bool)
	if !ok {
		// Minimal fake CDP servers in unit tests return a non-bool stub for
		// Runtime.evaluate; treat non-boolean as "drew" so capture proceeds normally.
		return true, nil
	}
	return drew, nil
}

// captureScreenshotWhileDrawing runs omp's whileDrawing (separate CDP session
// holding a 1x1 Page.startScreencast, screenshot.ts:442-466) around omp's
// captureAfterFrame (waitForRenderFrame(1500ms), then either normal capture or
// fromSurface:false capped at 5000ms with omp's exact named error,
// screenshot.ts:410-432).
//
// On owned headless Chrome, sends Page.bringToFront first so an older tab on a
// locked macOS display resumes receiving compositor BeginFrame ticks (without
// touching any user-visible tab or window; connected/headed/adopted tabs are
// never brought to front).
func (t *cdpTab) captureScreenshotWhileDrawing(ctx context.Context, params map[string]any, resp any) error {
	if t.proc.kind == KindHeadless && t.proc.headless && t.ownsPage {
		_ = t.send(ctx, "Page.bringToFront", nil, nil)
	}
	var scSession string
	var attachResp struct {
		SessionID string `json:"sessionId"`
	}
	if t.targetID != "" {
		if err := t.proc.client.Send(ctx, "", "Target.attachToTarget", map[string]any{
			"targetId": t.targetID,
			"flatten":  true,
		}, &attachResp); err == nil && attachResp.SessionID != "" {
			scSession = attachResp.SessionID
			_ = t.proc.client.Send(ctx, scSession, "Page.startScreencast", map[string]any{
				"format":    "jpeg",
				"quality":   1,
				"maxWidth":  1,
				"maxHeight": 1,
			}, nil)
		}
	}
	defer func() {
		if scSession != "" {
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			_ = t.proc.client.Send(stopCtx, scSession, "Page.stopScreencast", nil, nil)
			_ = t.proc.client.Send(stopCtx, "", "Target.detachFromTarget", map[string]any{"sessionId": scSession}, nil)
			stopCancel()
		}
	}()

	drew, err := t.waitForRenderFrame(ctx)
	if err != nil {
		if ctx.Err() != nil || t.crashed.Load() || !t.proc.client.Alive() {
			return err
		}
		// If rAF evaluation failed for a non-fatal reason (e.g. CSP/stub), proceed with normal capture.
		drew = true
	}
	if drew {
		return t.send(ctx, "Page.captureScreenshot", params, resp)
	}

	// Page drew no animation frame within 1500ms: fall back to fromSurface:false
	// capped at 5000ms and surface omp's exact named error on expiry
	// (upstream: screenshot.ts:415-431).
	fallbackParams := make(map[string]any, len(params)+1)
	for k, v := range params {
		fallbackParams[k] = v
	}
	fallbackParams["fromSurface"] = false
	stallCtx, stallCancel := context.WithTimeout(ctx, stalledCaptureMs*time.Millisecond)
	defer stallCancel()
	capErr := t.send(stallCtx, "Page.captureScreenshot", fallbackParams, resp)
	if capErr != nil && errors.Is(capErr, context.DeadlineExceeded) && ctx.Err() == nil && !t.crashed.Load() && t.proc.client.Alive() {
		return fmt.Errorf(
			"Screenshot failed: the page's renderer stopped drawing (no animation frame in %dms and no capture in %dms). DOM helpers still work; reopen the tab with kill:true to get a fresh renderer.",
			renderFrameWaitMs, stalledCaptureMs,
		)
	}
	return capErr
}
