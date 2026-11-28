package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ObjectID-based helpers for element handles (tab.id(n) / tab.ref(eN)).
// Every helper runs via Runtime.callFunctionOn on an isolated-world RemoteObjectId
// so nothing is ever stored on the page's window/globalThis and concurrent calls
// cannot race on a shared slot.

func (t *cdpTab) objectActionableCenter(ctx context.Context, label, objID string, viaLabel bool, opMs int, ft frameTarget) (float64, float64, error) {
	retryMs := actionOpTimeoutMs
	if opMs > 0 && opMs < retryMs {
		retryMs = opMs
	}
	if retryMs > 300 {
		retryMs -= 250
	}
	deadline := time.Now().Add(time.Duration(retryMs) * time.Millisecond)
	var lastReason, lastCovered string
	var prevRect map[string]any
	refusalText := func() string {
		if lastReason == "covered" && lastCovered != "" {
			return "covered by " + lastCovered
		}
		if lastReason != "" {
			return lastReason
		}
		return ""
	}
	formatTimeoutRefusal := func() error {
		if r := refusalText(); r != "" {
			return &clickRefusedError{refusal: r}
		}
		return context.DeadlineExceeded
	}
	fnDecl := fmt.Sprintf(`function() {
		const el = this;
		if (!el) return { ok: false, reason: "not-found" };
		if (el.scrollIntoView) el.scrollIntoView({block:"center",inline:"center"});
		if (globalThis.__ompBrowserKit && globalThis.__ompBrowserKit.checkClickActionable) {
			return globalThis.__ompBrowserKit.checkClickActionable(el, %t);
		}
		const r = el.getBoundingClientRect();
		if (r.width === 0 && r.height === 0) return { ok: false, reason: "zero-size" };
		return { ok: true, x: r.left + r.width / 2, y: r.top + r.height / 2, rect: { x: r.x, y: r.y, width: r.width, height: r.height } };
	}`, viaLabel)
	for {
		v, err := t.callFunctionOnValue(ctx, objID, fnDecl, nil)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return 0, 0, formatTimeoutRefusal()
			}
			return 0, 0, err
		}
		m, ok := v.(map[string]any)
		if !ok {
			return 0, 0, errors.New("failed to compute element bounding box")
		}
		if okVal, _ := m["ok"].(bool); okVal {
			x, _ := m["x"].(float64)
			y, _ := m["y"].(float64)
			return x + ft.offsetX, y + ft.offsetY, nil
		}
		lastReason, _ = m["reason"].(string)
		lastCovered, _ = m["coveredBy"].(string)
		curRect, _ := m["rect"].(map[string]any)
		if det, _ := m["detached"].(bool); det {
			return 0, 0, fmt.Errorf("%s: the page replaced this element (it is detached from the document); look it up again", label)
		}
		if lastReason == "covered" && lastCovered != "" && sameActionableRect(prevRect, curRect) {
			return 0, 0, fmt.Errorf("%s blocked: covered by %s", label, lastCovered)
		}
		prevRect = curRect
		if time.Now().After(deadline) || ctx.Err() != nil {
			return 0, 0, formatTimeoutRefusal()
		}
		select {
		case <-ctx.Done():
			return 0, 0, formatTimeoutRefusal()
		case <-t.crashCh:
			return 0, 0, t.rendererCrashedError()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (t *cdpTab) clickObjectID(ctx context.Context, label, objID, button string, count, opMs int, ft frameTarget) (*CallResult, error) {
	x, y, err := t.objectActionableCenter(ctx, label, objID, false, opMs, ft)
	if err != nil {
		return nil, err
	}
	return t.clickPoint(ctx, x, y, button, count)
}

func (t *cdpTab) setCheckedObjectID(ctx context.Context, objID string, want bool, opMs int, ft frameTarget) (*CallResult, error) {
	v, err := t.callFunctionOnValue(ctx, objID, `function() { return globalThis.__ompBrowserKit.isChecked(this); }`, nil)
	if err != nil {
		return nil, err
	}
	if cur, ok := v.(bool); ok && cur == want {
		return &CallResult{}, nil
	}
	return t.clickObjectID(ctx, "element.check()", objID, "left", 1, opMs, ft)
}

func (t *cdpTab) fillObjectID(ctx context.Context, objID, value string) (*CallResult, error) {
	fnDecl := fmt.Sprintf(`function() {
		const el = this;
		const val = %s;
		if (!el || !el.isConnected) throw new Error("tab.fill(): element is detached");
		if (el.tagName === "SELECT") throw new Error("tab.fill() does not handle <select> elements; use tab.select()");
		if (el.disabled || el.getAttribute("aria-disabled") === "true" || (el.closest && el.closest("fieldset[disabled]"))) {
			throw new Error("tab.fill(): element is disabled");
		}
		if (el.readOnly) throw new Error("tab.fill(): element is read-only");
		el.scrollIntoView({block:"center",inline:"center"});
		el.focus();
		if (el.isContentEditable) {
			el.textContent = val;
			el.dispatchEvent(new InputEvent("input", { bubbles: true, composed: true, data: val }));
			el.dispatchEvent(new Event("change", { bubbles: true }));
			return;
		}
		const tag = (el.localName || el.tagName || "").toLowerCase();
		if (tag !== "input" && tag !== "textarea") {
			throw new Error("tab.fill(): element <" + tag + "> is not an editable input, textarea, or contenteditable");
		}
		let proto = Object.getPrototypeOf(el);
		let desc;
		while (proto && !(desc = Object.getOwnPropertyDescriptor(proto, "value"))) {
			proto = Object.getPrototypeOf(proto);
		}
		if (desc && desc.set) desc.set.call(el, val);
		else el.value = val;
		el.dispatchEvent(new Event("input", { bubbles: true, composed: true }));
		el.dispatchEvent(new Event("change", { bubbles: true }));
	}`, jsLit(value))
	_, err := t.callFunctionOnValue(ctx, objID, fnDecl, nil)
	return &CallResult{}, err
}

func (t *cdpTab) selectObjectID(ctx context.Context, objID string, values []any) (*CallResult, error) {
	choices, err := parseSelectChoices(values)
	if err != nil {
		return nil, err
	}
	encoded, _ := json.Marshal(choices)
	v, err := t.callFunctionOnValue(ctx, objID, fmt.Sprintf(`function() { return globalThis.__ompBrowserKit.selectOptions(this, %s); }`, string(encoded)), nil)
	return &CallResult{Value: v}, err
}

func (t *cdpTab) uploadFileObjectID(ctx context.Context, callCtx CallContext, objID string, rawPaths []any) (*CallResult, error) {
	files, err := resolveUploadPaths(callCtx.Cwd, rawPaths)
	if err != nil {
		return nil, err
	}
	var nodeResp struct {
		Node struct {
			BackendNodeID int `json:"backendNodeId"`
		} `json:"node"`
	}
	if err := t.send(ctx, "DOM.describeNode", map[string]any{"objectId": objID}, &nodeResp); err != nil {
		return nil, err
	}
	if err := t.send(ctx, "DOM.setFileInputFiles", map[string]any{
		"backendNodeId": nodeResp.Node.BackendNodeID,
		"files":         files,
	}, nil); err != nil {
		return nil, err
	}
	return &CallResult{Value: files}, nil
}

func (t *cdpTab) evalStylesObjectID(ctx context.Context, objID string, propArgs []any) (*CallResult, error) {
	props := parseStyleProps(propArgs)
	pJSON, _ := json.Marshal(props)
	v, err := t.callFunctionOnValue(ctx, objID, fmt.Sprintf(`function() { const cs = getComputedStyle(this); const out = {}; for (const p of %s) out[p] = cs.getPropertyValue(p); return out; }`, string(pJSON)), nil)
	return &CallResult{Value: v}, err
}

// evaluateOnObjectID runs a user function against the element in the page's MAIN
// world so user code sees page globals, even though objID came from an isolated world.
// We map objID -> backendNodeId via DOM.describeNode -> main-world objectId via DOM.resolveNode.
func (t *cdpTab) evaluateOnObjectID(ctx context.Context, objID string, args []any) (*CallResult, error) {
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
	// Resolve the isolated-world handle into a main-world handle via backendNodeId.
	// Never fall back silently to the isolated world, where page globals are absent.
	var desc struct {
		Node struct {
			BackendNodeID int `json:"backendNodeId"`
		} `json:"node"`
	}
	if err := t.send(ctx, "DOM.describeNode", map[string]any{"objectId": objID}, &desc); err != nil || desc.Node.BackendNodeID <= 0 {
		return nil, errors.New("element.evaluate(): element is gone or no longer attached to the page")
	}
	var mainRes struct {
		Object struct {
			ObjectID string `json:"objectId"`
		} `json:"object"`
	}
	if err := t.send(ctx, "DOM.resolveNode", map[string]any{"backendNodeId": desc.Node.BackendNodeID}, &mainRes); err != nil || mainRes.Object.ObjectID == "" {
		return nil, errors.New("element.evaluate(): element is gone or could not be resolved in the page's main world")
	}
	mainObjID := mainRes.Object.ObjectID
	defer func() {
		relCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		_ = t.send(relCtx, "Runtime.releaseObject", map[string]any{"objectId": mainObjID}, nil)
		cancel()
	}()
	extraArgs := make([]string, 0, len(args)-1)
	for _, a := range args[1:] {
		b, err := json.Marshal(a)
		if err != nil {
			return nil, err
		}
		extraArgs = append(extraArgs, string(b))
	}
	argExpr := "this"
	if len(extraArgs) > 0 {
		argExpr = "this, " + strings.Join(extraArgs, ", ")
	}
	fnDecl := fmt.Sprintf("function() { return (%s)(%s); }", fnSrc, argExpr)
	v, err := t.callFunctionOnValue(ctx, mainObjID, fnDecl, nil)
	return &CallResult{Value: v}, err
}

type selectChoice struct {
	Kind  string `json:"kind"`
	Value any    `json:"value"`
}

func parseSelectChoices(values []any) ([]selectChoice, error) {
	choices := make([]selectChoice, 0, len(values))
	for _, raw := range values {
		switch v := raw.(type) {
		case string:
			choices = append(choices, selectChoice{Kind: "string", Value: v})
		case map[string]any:
			if len(v) == 1 {
				if lbl, ok := v["label"].(string); ok {
					choices = append(choices, selectChoice{Kind: "label", Value: lbl})
					continue
				}
				if val, ok := v["value"].(string); ok {
					choices = append(choices, selectChoice{Kind: "value", Value: val})
					continue
				}
				if idx, ok := v["index"].(float64); ok && idx >= 0 && idx == float64(int(idx)) {
					choices = append(choices, selectChoice{Kind: "index", Value: int(idx)})
					continue
				}
			}
			return nil, errors.New("select() expects strings, {label: string}, {value: string}, or {index: non-negative integer}")
		default:
			return nil, errors.New("select() expects strings, {label: string}, {value: string}, or {index: non-negative integer}")
		}
	}
	return choices, nil
}

func resolveUploadPaths(cwd string, rawPaths []any) ([]string, error) {
	files := make([]string, 0, len(rawPaths))
	for _, p := range rawPaths {
		s, ok := p.(string)
		if !ok || s == "" {
			return nil, errors.New("uploadFile() expects file path strings")
		}
		abs, err := filepath.Abs(resolveToCwd(s, cwd))
		if err != nil {
			return nil, err
		}
		if _, err := os.Stat(abs); err != nil {
			return nil, fmt.Errorf("uploadFile(): %w", err)
		}
		files = append(files, abs)
	}
	return files, nil
}

func parseStyleProps(propArgs []any) []string {
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
	return props
}
