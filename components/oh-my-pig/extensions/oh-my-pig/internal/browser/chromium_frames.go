package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

func (t *cdpTab) callElementHandle(ctx context.Context, callCtx CallContext, kind string, handleArgs []json.RawMessage, step CallStep) (*CallResult, error) {
	label := "element." + step.Method + "()"
	if err := t.checkDialogBlocked(label); err != nil {
		return nil, err
	}
	answersDialog := step.Method == "click" || step.Method == "dblclick" || step.Method == "press"
	_, quickOpMs, actionOpMs := resolveOpTimeouts(callCtx.TimeoutMs)
	opMs := opCeilingMs(step.Method, quickOpMs, actionOpMs)
	return t.runWithDialogRace(ctx, label, answersDialog, func(runCtx context.Context) (*CallResult, error) {
		opCtx := runCtx
		var opCancel context.CancelFunc
		if opMs > 0 {
			opCtx, opCancel = context.WithTimeout(runCtx, time.Duration(opMs)*time.Millisecond)
			defer opCancel()
		}
		res, err := t.callElementHandleInner(opCtx, callCtx, kind, handleArgs, step)
		if err != nil && opMs > 0 && runCtx.Err() == nil {
			var refused *clickRefusedError
			var interrupted *typingInterruptedError
			if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &refused) || errors.As(err, &interrupted) {
				return nil, t.perOpTimeoutError(label, "", opMs, err, frameTarget{})
			}
		}
		return res, err
	})
}

func (t *cdpTab) callElementHandleInner(ctx context.Context, callCtx CallContext, kind string, handleArgs []json.RawMessage, step CallStep) (*CallResult, error) {
	worldID, err := t.ensureKit(ctx, "", 0)
	if err != nil {
		return nil, err
	}
	ft := frameTarget{}
	hArgs := decodeArgs(handleArgs)
	var objID string
	if kind == "id" {
		id, ok := argInt(hArgs, 0)
		if !ok {
			return nil, errors.New("tab.id() expects an integer element id")
		}
		resolvedObjID, err := t.resolveObservedNode(ctx, id, worldID)
		if err != nil {
			return nil, err
		}
		objID = resolvedObjID
	} else {
		ref := argString(hArgs, 0)
		resolvedObjID, err := t.evalObjectID(ctx, fmt.Sprintf(
			`(function(r){ const el = globalThis.__ompBrowserKit.resolveAriaRef(r); if (!el) throw new Error("ARIA ref " + JSON.stringify(r) + " is stale. Run tab.ariaSnapshot() again."); return el; })(%s)`,
			jsLit(ref),
		), worldID)
		if err != nil {
			return nil, err
		}
		if resolvedObjID == "" {
			return nil, fmt.Errorf("ARIA ref %s is stale. Run tab.ariaSnapshot() again.", jsLit(ref))
		}
		objID = resolvedObjID
	}
	defer func() {
		relCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		_ = t.send(relCtx, "Runtime.releaseObject", map[string]any{"objectId": objID}, nil)
		cancel()
	}()

	_, _, actionOpMs := resolveOpTimeouts(callCtx.TimeoutMs)
	args := decodeArgs(step.Args)
	switch step.Method {
	case "click", "dblclick":
		count := 1
		if step.Method == "dblclick" {
			count = 2
		}
		btn := "left"
		if opts := argMap(args, 0); opts != nil {
			if b, ok := opts["button"].(string); ok && b != "" {
				btn = b
			}
			if c, ok := opts["count"].(float64); ok && c >= 1 {
				count = int(c)
			}
		}
		return t.clickObjectID(ctx, "element."+step.Method+"()", objID, btn, count, actionOpMs, ft)
	case "hover":
		x, y, err := t.objectActionableCenter(ctx, "element.hover()", objID, false, actionOpMs, ft)
		if err != nil {
			return nil, err
		}
		return t.dispatchMouseMove(ctx, x, y)
	case "focus":
		_, err := t.callFunctionOnValue(ctx, objID, `function() { this.focus(); }`, nil)
		return &CallResult{}, err
	case "check":
		return t.setCheckedObjectID(ctx, objID, true, actionOpMs, ft)
	case "uncheck":
		return t.setCheckedObjectID(ctx, objID, false, actionOpMs, ft)
	case "fill":
		return t.fillObjectID(ctx, objID, argString(args, 0))
	case "type":
		if _, err := t.clickObjectID(ctx, "element.type()", objID, "left", 1, actionOpMs, ft); err != nil {
			return nil, err
		}
		return t.typeText(ctx, argString(args, 0))
	case "press":
		if _, err := t.callFunctionOnValue(ctx, objID, `function() { this.focus(); }`, nil); err != nil {
			return nil, err
		}
		return t.pressKeyCombo(ctx, argString(args, 0), nil, actionOpMs, ft)
	case "select":
		return t.selectObjectID(ctx, objID, args)
	case "uploadFile":
		return t.uploadFileObjectID(ctx, callCtx, objID, args)
	case "scrollIntoView":
		_, err := t.callFunctionOnValue(ctx, objID, `function() { this.scrollIntoView({block:"center",inline:"center"}); }`, nil)
		return &CallResult{}, err
	case "highlight":
		dur := 1500
		if m := argMap(args, 0); m != nil {
			if d, ok := m["duration"].(float64); ok && d > 0 {
				dur = int(d)
			}
		}
		_, err := t.callFunctionOnValue(ctx, objID, fmt.Sprintf(`function() { globalThis.__ompBrowserKit.highlight(this, %d); }`, dur), nil)
		return &CallResult{}, err
	case "boundingBox":
		v, err := t.callFunctionOnValue(ctx, objID, `function() { const r = this.getBoundingClientRect(); return {x:r.x,y:r.y,width:r.width,height:r.height}; }`, nil)
		return &CallResult{Value: v}, err
	case "isVisible":
		v, err := t.callFunctionOnValue(ctx, objID, `function() { return globalThis.__ompBrowserKit.isVisible(this); }`, nil)
		return &CallResult{Value: v}, err
	case "isHidden":
		v, err := t.callFunctionOnValue(ctx, objID, `function() { return !globalThis.__ompBrowserKit.isVisible(this); }`, nil)
		return &CallResult{Value: v}, err
	case "isEnabled":
		v, err := t.callFunctionOnValue(ctx, objID, `function() { return globalThis.__ompBrowserKit.isEnabled(this); }`, nil)
		return &CallResult{Value: v}, err
	case "isChecked":
		v, err := t.callFunctionOnValue(ctx, objID, `function() { return globalThis.__ompBrowserKit.isChecked(this); }`, nil)
		return &CallResult{Value: v}, err
	case "text":
		v, err := t.callFunctionOnValue(ctx, objID, `function() { return (this.innerText ?? this.textContent ?? ""); }`, nil)
		return &CallResult{Value: v}, err
	case "html":
		v, err := t.callFunctionOnValue(ctx, objID, `function() { return this.innerHTML; }`, nil)
		return &CallResult{Value: v}, err
	case "value":
		v, err := t.callFunctionOnValue(ctx, objID, `function() { return this.value ?? ""; }`, nil)
		return &CallResult{Value: v}, err
	case "attr":
		v, err := t.callFunctionOnValue(ctx, objID, fmt.Sprintf(`function() { return this.getAttribute(%s); }`, jsLit(argString(args, 0))), nil)
		return &CallResult{Value: v}, err
	case "styles":
		return t.evalStylesObjectID(ctx, objID, args)
	case "evaluate":
		return t.evaluateOnObjectID(ctx, objID, args)
	default:
		return nil, fmt.Errorf("unknown element method %q", step.Method)
	}
}

func (t *cdpTab) callFrameHandle(ctx context.Context, callCtx CallContext, frameArgs []json.RawMessage, step CallStep) (*CallResult, error) {
	if err := t.checkDialogBlocked("frame." + step.Method + "()"); err != nil {
		return nil, err
	}
	fArgs := decodeArgs(frameArgs)
	selector := argString(fArgs, 0)
	if selector == "" {
		return nil, errors.New("tab.frame() expects a non-empty selector, name, or URL")
	}
	frameID, ox, oy, err := t.resolveFrameTarget(ctx, selector)
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	execCtxID := t.frameContexts[frameID]
	t.mu.Unlock()
	if execCtxID == 0 {
		var worldResp struct {
			ExecutionContextID int64 `json:"executionContextId"`
		}
		if err := t.send(ctx, "Page.createIsolatedWorld", map[string]any{
			"frameId":             frameID,
			"worldName":           "omp-frame",
			"grantUniveralAccess": true,
		}, &worldResp); err != nil {
			return nil, fmt.Errorf("failed to access frame %q: %w", selector, err)
		}
		execCtxID = worldResp.ExecutionContextID
	}
	return t.callDirect(ctx, callCtx, step, frameTarget{
		frameID:   frameID,
		execCtxID: execCtxID,
		offsetX:   ox,
		offsetY:   oy,
	})
}

// resolveFrameTarget follows omp's frames.ts:163-191 resolution order:
// 1. Selector matching an owning <iframe> element -> DOM.describeNode -> contentDocument/frameId
// 2. Exact child frame name or exact child frame URL
// It also computes the iframe's viewport offset (offsetX, offsetY) in main-frame CSS pixels
// so pointer actions inside the frame land on the right coordinates.
func (t *cdpTab) resolveFrameTarget(ctx context.Context, selector string) (frameID string, offsetX, offsetY float64, err error) {
	frames, err := t.getFrameList(ctx)
	if err != nil {
		return "", 0, 0, err
	}
	// 1. Try selector in the main frame first (upstream: frames.ts:170-185).
	if elObjID, oerr := t.evalKitReadObjectID(ctx, frameTarget{}, fmt.Sprintf("globalThis.__ompBrowserKit.queryFirst(%s)", jsLit(selector))); oerr == nil && elObjID != "" {
		defer func() {
			relCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			_ = t.send(relCtx, "Runtime.releaseObject", map[string]any{"objectId": elObjID}, nil)
			cancel()
		}()
		var desc struct {
			Node struct {
				FrameID         string `json:"frameId"`
				ContentDocument *struct {
					FrameID string `json:"frameId"`
				} `json:"contentDocument"`
			} `json:"node"`
		}
		if derr := t.send(ctx, "DOM.describeNode", map[string]any{"objectId": elObjID}, &desc); derr == nil {
			var resolved string
			if desc.Node.ContentDocument != nil && desc.Node.ContentDocument.FrameID != "" {
				resolved = desc.Node.ContentDocument.FrameID
			} else if desc.Node.FrameID != "" && desc.Node.FrameID != t.mainFrameID {
				resolved = desc.Node.FrameID
			}
			if resolved == "" {
				// Element matched selector but has no contentDocument/frameId.
				return "", 0, 0, fmt.Errorf("tab.frame(%s) matched an element without a frame", jsLit(selector))
			}
			ox, oy := t.cumulativeFrameOffset(ctx, frames, resolved, elObjID)
			return resolved, ox, oy, nil
		}
	}
	// 2. Fall back to exact frame name or exact frame URL (upstream: frames.ts:186-190).
	for _, f := range frames {
		if f.ParentID == nil {
			continue
		}
		if f.Name == selector || f.URL == selector || f.ID == selector {
			ox, oy := t.cumulativeFrameOffset(ctx, frames, f.ID, "")
			return f.ID, ox, oy, nil
		}
	}
	return "", 0, 0, fmt.Errorf("tab.frame(%s) found no matching frame", jsLit(selector))
}

// cumulativeFrameOffset sums the content-box offsets of frameID and every
// ancestor iframe up to the main frame so pointer clicks inside nested frames
// land on the exact main-frame CSS coordinates.
func (t *cdpTab) cumulativeFrameOffset(ctx context.Context, frames []cdpFrameInfo, frameID, directObjID string) (float64, float64) {
	parentByID := make(map[string]string, len(frames))
	for _, f := range frames {
		if f.ParentID != nil {
			parentByID[f.ID] = *f.ParentID
		}
	}
	var totalX, totalY float64
	cur := frameID
	obj := directObjID
	for depth := 0; cur != "" && depth < 16; depth++ {
		parent, hasParent := parentByID[cur]
		if !hasParent {
			break
		}
		ox, oy := t.singleFrameOffset(ctx, cur, parent, obj)
		totalX += ox
		totalY += oy
		cur = parent
		obj = ""
	}
	return totalX, totalY
}

func (t *cdpTab) singleFrameOffset(ctx context.Context, frameID, parentFrameID, iframeObjID string) (float64, float64) {
	ownedObj := false
	if iframeObjID == "" && frameID != "" {
		var owner struct {
			BackendNodeID int `json:"backendNodeId"`
		}
		if err := t.send(ctx, "DOM.getFrameOwner", map[string]any{"frameId": frameID}, &owner); err == nil && owner.BackendNodeID > 0 {
			// Always resolve the iframe element in the parent frame's isolated world
			// so a hostile page cannot spoof getBoundingClientRect/getComputedStyle/parseFloat.
			parentIso, perr := t.isolatedWorldForFrame(ctx, parentFrameID)
			if perr != nil || parentIso <= 0 {
				return 0, 0
			}
			var resolved struct {
				Object struct {
					ObjectID string `json:"objectId"`
				} `json:"object"`
			}
			resolveParams := map[string]any{
				"backendNodeId":      owner.BackendNodeID,
				"executionContextId": parentIso,
			}
			err := t.send(ctx, "DOM.resolveNode", resolveParams, &resolved)
			if err != nil && isNavigatedContextError(err) {
				t.invalidateIsolatedWorld(parentFrameID, parentIso)
				parentIso, perr = t.isolatedWorldForFrame(ctx, parentFrameID)
				if perr == nil && parentIso > 0 {
					resolveParams["executionContextId"] = parentIso
					err = t.send(ctx, "DOM.resolveNode", resolveParams, &resolved)
				}
			}
			if err == nil && resolved.Object.ObjectID != "" {
				iframeObjID = resolved.Object.ObjectID
				ownedObj = true
			}
		}
	}
	if iframeObjID == "" {
		return 0, 0
	}
	if ownedObj {
		defer func() {
			relCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			_ = t.send(relCtx, "Runtime.releaseObject", map[string]any{"objectId": iframeObjID}, nil)
			cancel()
		}()
	}
	var rectResp struct {
		Result struct {
			Value map[string]any `json:"value"`
		} `json:"result"`
	}
	if err := t.send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId": iframeObjID,
		"functionDeclaration": `function() {
			const r = this.getBoundingClientRect();
			const cs = getComputedStyle(this);
			const bl = parseFloat(cs.borderLeftWidth) || 0;
			const bt = parseFloat(cs.borderTopWidth) || 0;
			const pl = parseFloat(cs.paddingLeft) || 0;
			const pt = parseFloat(cs.paddingTop) || 0;
			return { x: r.left + bl + pl, y: r.top + bt + pt };
		}`,
		"returnByValue": true,
	}, &rectResp); err == nil && rectResp.Result.Value != nil {
		x, _ := rectResp.Result.Value["x"].(float64)
		y, _ := rectResp.Result.Value["y"].(float64)
		return x, y
	}
	return 0, 0
}

type cdpFrameInfo struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	URL      string  `json:"url"`
	ParentID *string `json:"parentId"`
}

func (t *cdpTab) getFrameList(ctx context.Context) ([]cdpFrameInfo, error) {
	type frameNode struct {
		Frame struct {
			ID       string `json:"id"`
			ParentID string `json:"parentId"`
			Name     string `json:"name"`
			URL      string `json:"url"`
		} `json:"frame"`
		ChildFrames []frameNode `json:"childFrames"`
	}
	var resp struct {
		FrameTree frameNode `json:"frameTree"`
	}
	if err := t.send(ctx, "Page.getFrameTree", nil, &resp); err != nil {
		return nil, err
	}
	var out []cdpFrameInfo
	var walk func(n frameNode)
	walk = func(n frameNode) {
		var pid *string
		if n.Frame.ParentID != "" {
			p := n.Frame.ParentID
			pid = &p
		}
		out = append(out, cdpFrameInfo{
			ID:       n.Frame.ID,
			Name:     n.Frame.Name,
			URL:      n.Frame.URL,
			ParentID: pid,
		})
		for _, ch := range n.ChildFrames {
			walk(ch)
		}
	}
	walk(resp.FrameTree)
	return out, nil
}

func (t *cdpTab) listFrames(ctx context.Context) (*CallResult, error) {
	frames, err := t.getFrameList(ctx)
	if err != nil {
		return nil, err
	}
	return &CallResult{Value: frames}, nil
}
