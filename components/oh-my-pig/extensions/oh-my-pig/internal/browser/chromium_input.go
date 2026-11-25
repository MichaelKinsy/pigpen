package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func (t *cdpTab) selectorExpr(selector string) (string, error) {
	if err := ValidateSelector(selector); err != nil {
		return "", err
	}
	if strings.HasPrefix(strings.TrimSpace(selector), "role/") {
		if _, err := ParseRoleSelector(strings.TrimSpace(selector)[5:]); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf(`(function(s){ const el = globalThis.__ompBrowserKit.queryFirst(s); if (!el) throw new Error("No element matched selector " + JSON.stringify(s)); return el; })(%s)`, jsLit(selector)), nil
}

// waitForSelectorPresent waits for at least one element matching selector
// before running an action, matching omp's #zeroMatchWatchdog + locator wait
// (upstream: op-timeouts.ts:29, tab-worker.ts:2958-2982, 4322-4327).
//
// Matches omp verbatim: `zeroDeadline` is fixed ONCE at the start of the op
// (`Date.now() + ZERO_MATCH_FAIL_FAST_MS`). An inconclusive probe (navigated-
// context error while a slow navigation commits) skips without counting toward
// failure and never returns a raw CDP error; once the new document commits, the
// first conclusive zero count at or after `zeroDeadline` fails fast immediately
// with omp's exact `failed fast after 2000ms` message.
func (t *cdpTab) waitForSelectorPresent(ctx context.Context, label, selector string, opMs int, ft frameTarget) error {
	if err := ValidateSelector(selector); err != nil {
		return err
	}
	if strings.HasPrefix(strings.TrimSpace(selector), "role/") {
		if _, err := ParseRoleSelector(strings.TrimSpace(selector)[5:]); err != nil {
			return err
		}
	}
	// Aria-ref selectors never enter the zero-match watchdog (upstream: tab-worker.ts:2880).
	isAriaRef := isAriaRefSelector(selector)
	zeroDeadline := time.Now().Add(time.Duration(zeroMatchFailFastMs) * time.Millisecond)
	for {
		if ft.frameID == "" && t.isMainFrameNavigating() {
			if err := t.awaitRequestedNavigation(ctx, opMs, false); err != nil {
				return err
			}
		}
		worldID, err := t.ensureKit(ctx, ft.frameID, ft.execCtxID)
		var confirmedZero bool
		if err == nil {
			var v any
			v, err = t.evalValue(ctx, fmt.Sprintf("globalThis.__ompBrowserKit.queryAll(%s).length", jsLit(selector)), worldID)
			if err == nil {
				if f, ok := v.(float64); ok && f > 0 {
					return nil
				}
				confirmedZero = true
			}
		}
		if err != nil {
			if isNavigatedContextError(err) {
				// Inconclusive probe during navigation commit (upstream: tab-worker.ts:2968-2970):
				// wait for the new document without advancing or restarting zeroDeadline.
				t.invalidateIsolatedWorld(ft.frameID, worldID)
				if ft.frameID == "" {
					if werr := t.awaitRequestedNavigation(ctx, opMs, true); werr != nil {
						return werr
					}
				}
				if ctx.Err() != nil {
					return ctx.Err()
				}
				continue
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		if confirmedZero && !isAriaRef && !time.Now().Before(zeroDeadline) {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("%s failed fast after %dms%s", label, zeroMatchFailFastMs, formatSelectorMatchHint(0))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.crashCh:
			return t.rendererCrashedError()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func sameActionableRect(a, b map[string]any) bool {
	if a == nil || b == nil {
		return false
	}
	for _, k := range []string{"x", "y", "width", "height"} {
		av, aok := a[k].(float64)
		bv, bok := b[k].(float64)
		if !aok || !bok {
			return false
		}
		diff := av - bv
		if diff < 0 {
			diff = -diff
		}
		if diff >= 0.5 {
			return false
		}
	}
	return true
}

func (t *cdpTab) elementActionableCenter(ctx context.Context, label, elExpr string, viaLabel bool, opMs int, ft frameTarget) (float64, float64, error) {
	retryMs := actionOpTimeoutMs
	if opMs > 0 && opMs < retryMs {
		retryMs = opMs
	}
	// End the actionability retry loop slightly before the per-op ceiling so a
	// ClickRefusedError (covered/hidden/moving/zero-size) always wins over a
	// generic timeout (upstream: interactions.ts:387-406, tab-worker.ts:2921).
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
	for {
		if ft.frameID == "" && t.isMainFrameNavigating() {
			if err := t.awaitRequestedNavigation(ctx, opMs, false); err != nil {
				return 0, 0, err
			}
		}
		worldID, err := t.ensureKit(ctx, ft.frameID, ft.execCtxID)
		var v any
		if err == nil {
			v, err = t.evalValue(ctx, fmt.Sprintf(`(function(el, viaLabel){
			if (!el) return { ok: false, reason: "not-found" };
			if (el.scrollIntoView) el.scrollIntoView({block:"center",inline:"center"});
			if (globalThis.__ompBrowserKit && globalThis.__ompBrowserKit.checkClickActionable) {
				return globalThis.__ompBrowserKit.checkClickActionable(el, viaLabel);
			}
			const r = el.getBoundingClientRect();
			if (r.width === 0 && r.height === 0) return { ok: false, reason: "zero-size" };
			return { ok: true, x: r.left + r.width / 2, y: r.top + r.height / 2, rect: { x: r.x, y: r.y, width: r.width, height: r.height } };
		})(%s, %t)`, elExpr, viaLabel), worldID)
		}
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return 0, 0, formatTimeoutRefusal()
			}
			if isNavigatedContextError(err) && ctx.Err() == nil && !t.crashed.Load() {
				// Inconclusive probe during navigation commit: wait for the new document
				// and retry within ctx's deadline (never surface a bare CDP context error).
				t.invalidateIsolatedWorld(ft.frameID, worldID)
				if ft.frameID == "" {
					if werr := t.awaitRequestedNavigation(ctx, opMs, true); werr != nil {
						return 0, 0, werr
					}
				} else {
					select {
					case <-ctx.Done():
						return 0, 0, formatTimeoutRefusal()
					case <-t.crashCh:
						return 0, 0, t.rendererCrashedError()
					case <-time.After(50 * time.Millisecond):
					}
				}
				if time.Now().After(deadline) || ctx.Err() != nil {
					return 0, 0, formatTimeoutRefusal()
				}
				continue
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
		// Match omp actionableClickPoint (interactions.ts:396-398): refuse a cover
		// immediately once the target's box holds still across two checks.
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

func (t *cdpTab) elementCenterExpr(ctx context.Context, elExpr string, _ int64, ft frameTarget) (float64, float64, error) {
	return t.elementActionableCenter(ctx, "element", elExpr, false, actionOpTimeoutMs, ft)
}
func (t *cdpTab) clickSelector(ctx context.Context, selector, button string, count int, opMs int, ft frameTarget) (*CallResult, error) {
	label := fmt.Sprintf("tab.click(%s)", jsLit(selector))
	if err := t.waitForSelectorPresent(ctx, label, selector, opMs, ft); err != nil {
		return nil, err
	}
	elExpr, err := t.selectorExpr(selector)
	if err != nil {
		return nil, err
	}
	viaLabel := strings.HasPrefix(strings.TrimSpace(selector), "label/")
	x, y, err := t.elementActionableCenter(ctx, label, elExpr, viaLabel, opMs, ft)
	if err != nil {
		return nil, err
	}
	return t.clickPoint(ctx, x, y, button, count)
}

func (t *cdpTab) clickElementExpr(ctx context.Context, elExpr, button string, count int, ft frameTarget) (*CallResult, error) {
	x, y, err := t.elementActionableCenter(ctx, "element.click()", elExpr, false, actionOpTimeoutMs, ft)
	if err != nil {
		return nil, err
	}
	return t.clickPoint(ctx, x, y, button, count)
}
func (t *cdpTab) clickPoint(ctx context.Context, x, y float64, button string, count int) (*CallResult, error) {
	if count < 1 {
		count = 1
	}
	// Match omp clickPoint (interactions.ts:2267-2280): write mouseMoved to the
	// CDP WebSocket FIRST (preserving strict wire ordering before mousePressed),
	// but do not wait for Chrome's mouseMoved reply before sending mousePressed —
	// Chrome can hold a pointer move ack for 5s until the next compositor frame
	// in a background tab, and mousePressed flushes it immediately.
	moveReply, err := t.sendAsync(ctx, "Input.dispatchMouseEvent", map[string]any{
		"type":      "mouseMoved",
		"x":         x,
		"y":         y,
		"modifiers": t.currentMods(),
	})
	if err != nil {
		return nil, err
	}
	moveWaited := false
	defer func() {
		if !moveWaited {
			_ = moveReply()
		}
	}()
	t.mu.Lock()
	t.mouseX, t.mouseY = x, y
	t.mu.Unlock()
	for i := 1; i <= count; i++ {
		if _, err := t.dispatchMouseButton(ctx, "mousePressed", x, y, button, i); err != nil {
			return nil, err
		}
		if _, err := t.dispatchMouseButton(ctx, "mouseReleased", x, y, button, i); err != nil {
			return nil, err
		}
	}
	moveWaited = true
	if err := moveReply(); err != nil {
		return nil, err
	}
	return &CallResult{}, nil
}

func (t *cdpTab) currentMods() int {
	t.mu.Lock()
	m := t.heldMods
	t.mu.Unlock()
	return m
}

func (t *cdpTab) dispatchMouseMove(ctx context.Context, x, y float64) (*CallResult, error) {
	// On a locked macOS display, an older background tab in our own headless
	// Chrome stops receiving compositor frames, causing standalone mouseMoved
	// (hover / mouseMove) to stall 5s for an input ack. Activating the target
	// via Page.bringToFront on owned headless Chrome restores frame ticks
	// without changing any user-visible tab or window.
	if t.proc.kind == KindHeadless && t.proc.headless && t.ownsPage {
		_ = t.send(ctx, "Page.bringToFront", nil, nil)
	}
	t.mu.Lock()
	t.mouseX, t.mouseY = x, y
	mods := t.heldMods
	t.mu.Unlock()
	err := t.send(ctx, "Input.dispatchMouseEvent", map[string]any{
		"type":      "mouseMoved",
		"x":         x,
		"y":         y,
		"modifiers": mods,
	}, nil)
	return &CallResult{}, err
}
func (t *cdpTab) dispatchMouseButton(ctx context.Context, evType string, x, y float64, button string, clickCount int) (*CallResult, error) {
	if button == "" {
		button = "left"
	}
	t.mu.Lock()
	mods := t.heldMods
	t.mu.Unlock()
	err := t.send(ctx, "Input.dispatchMouseEvent", map[string]any{
		"type":       evType,
		"x":          x,
		"y":          y,
		"button":     button,
		"clickCount": clickCount,
		"modifiers":  mods,
	}, nil)
	return &CallResult{}, err
}

func (t *cdpTab) dispatchWheel(ctx context.Context, x, y, dx, dy float64) (*CallResult, error) {
	t.mu.Lock()
	mods := t.heldMods
	t.mu.Unlock()
	err := t.send(ctx, "Input.dispatchMouseEvent", map[string]any{
		"type":      "mouseWheel",
		"x":         x,
		"y":         y,
		"deltaX":    dx,
		"deltaY":    dy,
		"modifiers": mods,
	}, nil)
	return &CallResult{}, err
}

func (t *cdpTab) hoverSelector(ctx context.Context, selector string, opMs int, ft frameTarget) (*CallResult, error) {
	label := fmt.Sprintf("tab.hover(%s)", jsLit(selector))
	if err := t.waitForSelectorPresent(ctx, label, selector, opMs, ft); err != nil {
		return nil, err
	}
	elExpr, err := t.selectorExpr(selector)
	if err != nil {
		return nil, err
	}
	x, y, err := t.elementActionableCenter(ctx, label, elExpr, false, opMs, ft)
	if err != nil {
		return nil, err
	}
	return t.dispatchMouseMove(ctx, x, y)
}

func (t *cdpTab) focusSelector(ctx context.Context, selector string, opMs int, ft frameTarget) (*CallResult, error) {
	label := fmt.Sprintf("tab.focus(%s)", jsLit(selector))
	if err := t.waitForSelectorPresent(ctx, label, selector, opMs, ft); err != nil {
		return nil, err
	}
	elExpr, err := t.selectorExpr(selector)
	if err != nil {
		return nil, err
	}
	_, err = t.evalKitAction(ctx, ft, fmt.Sprintf("(%s).focus()", elExpr))
	return &CallResult{}, err
}

func (t *cdpTab) setCheckedSelector(ctx context.Context, selector string, want bool, opMs int, ft frameTarget) (*CallResult, error) {
	verb := "check"
	if !want {
		verb = "uncheck"
	}
	label := fmt.Sprintf("tab.%s(%s)", verb, jsLit(selector))
	if err := t.waitForSelectorPresent(ctx, label, selector, opMs, ft); err != nil {
		return nil, err
	}
	elExpr, err := t.selectorExpr(selector)
	if err != nil {
		return nil, err
	}
	return t.setCheckedExpr(ctx, elExpr, want, ft)
}

func (t *cdpTab) setCheckedExpr(ctx context.Context, elExpr string, want bool, ft frameTarget) (*CallResult, error) {
	v, err := t.evalKitRead(ctx, ft, fmt.Sprintf("globalThis.__ompBrowserKit.isChecked(%s)", elExpr))
	if err != nil {
		return nil, err
	}
	if cur, ok := v.(bool); ok && cur == want {
		return &CallResult{}, nil
	}
	return t.clickElementExpr(ctx, elExpr, "left", 1, ft)
}

func (t *cdpTab) fillSelector(ctx context.Context, selector, value string, opMs int, ft frameTarget) (*CallResult, error) {
	label := fmt.Sprintf("tab.fill(%s)", jsLit(selector))
	if err := t.waitForSelectorPresent(ctx, label, selector, opMs, ft); err != nil {
		return nil, err
	}
	elExpr, err := t.selectorExpr(selector)
	if err != nil {
		return nil, err
	}
	return t.fillElementExpr(ctx, elExpr, value, ft)
}

func (t *cdpTab) fillElementExpr(ctx context.Context, elExpr, value string, ft frameTarget) (*CallResult, error) {
	_, err := t.evalKitAction(ctx, ft, fmt.Sprintf(`(function(el, val){
		if (!el || !el.isConnected) throw new Error("tab.fill(): element is detached");
		if (el.tagName === "SELECT") {
			throw new Error("tab.fill() does not handle <select> elements; use tab.select()");
		}
		if (el.disabled || el.getAttribute("aria-disabled") === "true" || (el.closest && el.closest("fieldset[disabled]"))) {
			throw new Error("tab.fill(): element is disabled");
		}
		if (el.readOnly) {
			throw new Error("tab.fill(): element is read-only");
		}
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
	})(%s, %s)`, elExpr, jsLit(value)))
	return &CallResult{}, err
}

func (t *cdpTab) typeSelector(ctx context.Context, selector, text string, opMs int, ft frameTarget) (*CallResult, error) {
	if _, err := t.clickSelector(ctx, selector, "left", 1, opMs, ft); err != nil {
		return nil, err
	}
	return t.typeText(ctx, text)
}

func (t *cdpTab) typeText(ctx context.Context, text string) (*CallResult, error) {
	t.mu.Lock()
	mods := t.heldMods
	t.mu.Unlock()
	for _, r := range text {
		s := string(r)
		switch r {
		case '\n', '\r':
			if _, err := t.pressKeyCombo(ctx, "Enter", nil, 0, frameTarget{}); err != nil {
				return nil, err
			}
		case '\t':
			if _, err := t.pressKeyCombo(ctx, "Tab", nil, 0, frameTarget{}); err != nil {
				return nil, err
			}
		default:
			kd, _ := resolveCDPKey(s)
			if err := t.send(ctx, "Input.dispatchKeyEvent", map[string]any{
				"type":                  "keyDown",
				"key":                   s,
				"code":                  kd.code,
				"windowsVirtualKeyCode": kd.vk,
				"text":                  s,
				"unmodifiedText":        s,
				"modifiers":             mods,
			}, nil); err != nil {
				return nil, err
			}
			if err := t.send(ctx, "Input.dispatchKeyEvent", map[string]any{
				"type":                  "keyUp",
				"key":                   s,
				"code":                  kd.code,
				"windowsVirtualKeyCode": kd.vk,
				"modifiers":             mods,
			}, nil); err != nil {
				return nil, err
			}
		}
	}
	return &CallResult{}, nil
}

type cdpKeyDef struct {
	key     string
	code    string
	vk      int
	modMask int // Alt=1, Ctrl=2, Meta=4, Shift=8
}

func resolveCDPKey(name string) (cdpKeyDef, bool) {
	switch name {
	case "Alt", "AltLeft", "AltRight":
		return cdpKeyDef{key: "Alt", code: "AltLeft", vk: 18, modMask: 1}, true
	case "Control", "ControlLeft", "ControlRight", "Ctrl":
		return cdpKeyDef{key: "Control", code: "ControlLeft", vk: 17, modMask: 2}, true
	case "Meta", "MetaLeft", "MetaRight", "Command", "Cmd":
		return cdpKeyDef{key: "Meta", code: "MetaLeft", vk: 91, modMask: 4}, true
	case "Shift", "ShiftLeft", "ShiftRight":
		return cdpKeyDef{key: "Shift", code: "ShiftLeft", vk: 16, modMask: 8}, true
	case "Enter", "\r", "\n":
		return cdpKeyDef{key: "Enter", code: "Enter", vk: 13}, true
	case "Tab", "\t":
		return cdpKeyDef{key: "Tab", code: "Tab", vk: 9}, true
	case "Backspace":
		return cdpKeyDef{key: "Backspace", code: "Backspace", vk: 8}, true
	case "Delete":
		return cdpKeyDef{key: "Delete", code: "Delete", vk: 46}, true
	case "Escape":
		return cdpKeyDef{key: "Escape", code: "Escape", vk: 27}, true
	case "Space", " ":
		return cdpKeyDef{key: " ", code: "Space", vk: 32}, true
	case "ArrowUp":
		return cdpKeyDef{key: "ArrowUp", code: "ArrowUp", vk: 38}, true
	case "ArrowDown":
		return cdpKeyDef{key: "ArrowDown", code: "ArrowDown", vk: 40}, true
	case "ArrowLeft":
		return cdpKeyDef{key: "ArrowLeft", code: "ArrowLeft", vk: 37}, true
	case "ArrowRight":
		return cdpKeyDef{key: "ArrowRight", code: "ArrowRight", vk: 39}, true
	case "Home":
		return cdpKeyDef{key: "Home", code: "Home", vk: 36}, true
	case "End":
		return cdpKeyDef{key: "End", code: "End", vk: 35}, true
	case "PageUp":
		return cdpKeyDef{key: "PageUp", code: "PageUp", vk: 33}, true
	case "PageDown":
		return cdpKeyDef{key: "PageDown", code: "PageDown", vk: 34}, true
	case "Insert":
		return cdpKeyDef{key: "Insert", code: "Insert", vk: 45}, true
	case "F1", "F2", "F3", "F4", "F5", "F6", "F7", "F8", "F9", "F10", "F11", "F12":
		n := 0
		_, _ = fmt.Sscanf(name[1:], "%d", &n)
		return cdpKeyDef{key: name, code: name, vk: 111 + n}, true
	}
	if len(name) == 4 && strings.HasPrefix(name, "Key") && name[3] >= 'A' && name[3] <= 'Z' {
		ch := strings.ToLower(name[3:])
		return cdpKeyDef{key: ch, code: name, vk: int(name[3])}, true
	}
	if len(name) == 6 && strings.HasPrefix(name, "Digit") && name[5] >= '0' && name[5] <= '9' {
		ch := name[5:]
		return cdpKeyDef{key: ch, code: name, vk: int(name[5])}, true
	}
	runes := []rune(name)
	if len(runes) == 1 {
		r := runes[0]
		if r >= 'a' && r <= 'z' {
			return cdpKeyDef{key: name, code: "Key" + strings.ToUpper(name), vk: int(r - 32)}, true
		}
		if r >= 'A' && r <= 'Z' {
			return cdpKeyDef{key: name, code: "Key" + name, vk: int(r)}, true
		}
		if r >= '0' && r <= '9' {
			return cdpKeyDef{key: name, code: "Digit" + name, vk: int(r)}, true
		}
		return cdpKeyDef{key: name}, true
	}
	return cdpKeyDef{}, false
}

// macEditingCommand returns the Chrome editing command for macOS Meta combos
// (upstream: EDITING_COMMANDS in interactions.ts:2138-2162).
func macEditingCommand(defs []cdpKeyDef) string {
	if runtime.GOOS != "darwin" || len(defs) < 2 {
		return ""
	}
	last := defs[len(defs)-1]
	if len(last.key) != 1 {
		return ""
	}
	var hasMeta, hasShift, hasOther bool
	for _, d := range defs[:len(defs)-1] {
		switch d.modMask {
		case 4:
			hasMeta = true
		case 8:
			hasShift = true
		default:
			hasOther = true
		}
	}
	if !hasMeta || hasOther {
		return ""
	}
	letter := strings.ToLower(last.key)
	if hasShift {
		if letter == "z" {
			return "redo"
		}
		return ""
	}
	switch letter {
	case "a":
		return "selectAll"
	case "c":
		return "copy"
	case "v":
		return "paste"
	case "x":
		return "cut"
	case "z":
		return "undo"
	}
	return ""
}

func (t *cdpTab) dispatchSingleKey(ctx context.Context, name string, down bool) (*CallResult, error) {
	kd, ok := resolveCDPKey(name)
	if !ok {
		return nil, fmt.Errorf("Unknown key: %q", name)
	}
	t.mu.Lock()
	if down {
		t.heldMods |= kd.modMask
	} else {
		t.heldMods &^= kd.modMask
	}
	mods := t.heldMods
	t.mu.Unlock()
	evType := "keyUp"
	if down {
		evType = "keyDown"
	}
	params := map[string]any{
		"type":                  evType,
		"key":                   kd.key,
		"code":                  kd.code,
		"windowsVirtualKeyCode": kd.vk,
		"modifiers":             mods,
	}
	if down && len(kd.key) == 1 && (mods&(1|2|4)) == 0 {
		params["text"] = kd.key
	}
	err := t.send(ctx, "Input.dispatchKeyEvent", params, nil)
	return &CallResult{}, err
}

func (t *cdpTab) pressKeyCombo(ctx context.Context, combo string, args []any, opMs int, ft frameTarget) (*CallResult, error) {
	// Optional focus selector in args[1] (either string or {selector: ...});
	// upstream fails at once when missing without waiting (tab-worker.ts:3268),
	// but if a slow navigation arrives mid-lookup we first locate the element via
	// read-only evalKitRead (which waits for the new document on context loss,
	// never returning a raw CDP context error) and then focus at most once via
	// evalKitAction.
	if len(args) > 1 {
		var sel string
		if s, ok := args[1].(string); ok {
			sel = s
		} else if m, ok := args[1].(map[string]any); ok {
			sel, _ = m["selector"].(string)
		}
		if sel != "" {
			if err := ValidateSelector(sel); err != nil {
				return nil, err
			}
			if strings.HasPrefix(strings.TrimSpace(sel), "role/") {
				if _, err := ParseRoleSelector(strings.TrimSpace(sel)[5:]); err != nil {
					return nil, err
				}
			}
			// Step 1 (read-only lookup): wait out a slow arriving document if context
			// is lost before any element is found or focused, while failing at once
			// when the settled document has no match (matching tab-worker.ts:3267-3268).
			foundVal, err := t.evalKitRead(ctx, ft, fmt.Sprintf(`Boolean(globalThis.__ompBrowserKit.queryFirst(%s))`, jsLit(sel)))
			if err != nil {
				return nil, err
			}
			if found, _ := foundVal.(bool); !found {
				return nil, fmt.Errorf("No element found for selector: %s", sel)
			}
			// Step 2 (mutating focus): runs at most once via evalKitAction (never retried on abort).
			_, err = t.evalKitAction(ctx, ft, fmt.Sprintf(`(function(s){
				const el = globalThis.__ompBrowserKit.queryFirst(s);
				if (!el) throw new Error("No element found for selector: " + s);
				el.focus();
			})(%s)`, jsLit(sel)))
			if err != nil {
				return nil, err
			}
		}
	}
	_ = opMs
	parts := strings.Split(combo, "+")
	if combo == "+" {
		parts = []string{"+"}
	}
	defs := make([]cdpKeyDef, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			kd, ok := resolveCDPKey(p)
			if !ok {
				return nil, fmt.Errorf("Unknown key: %q", p)
			}
			defs = append(defs, kd)
		}
	}
	if len(defs) == 0 {
		return nil, fmt.Errorf("Unknown key: %q", combo)
	}
	editCmd := macEditingCommand(defs)
	t.mu.Lock()
	mods := t.heldMods
	t.mu.Unlock()
	for idx, kd := range defs {
		mods |= kd.modMask
		params := map[string]any{
			"type":                  "keyDown",
			"key":                   kd.key,
			"code":                  kd.code,
			"windowsVirtualKeyCode": kd.vk,
			"modifiers":             mods,
		}
		if len(kd.key) == 1 && (mods&(1|2|4)) == 0 {
			params["text"] = kd.key
		} else if kd.key == "Enter" {
			params["text"] = "\r"
		}
		if idx == len(defs)-1 && editCmd != "" {
			params["commands"] = []string{editCmd}
		}
		if err := t.send(ctx, "Input.dispatchKeyEvent", params, nil); err != nil {
			return nil, err
		}
	}
	for i := len(defs) - 1; i >= 0; i-- {
		kd := defs[i]
		mods &^= kd.modMask
		if err := t.send(ctx, "Input.dispatchKeyEvent", map[string]any{
			"type":                  "keyUp",
			"key":                   kd.key,
			"code":                  kd.code,
			"windowsVirtualKeyCode": kd.vk,
			"modifiers":             mods,
		}, nil); err != nil {
			return nil, err
		}
	}
	return &CallResult{}, nil
}

func (t *cdpTab) scrollPageOrElement(ctx context.Context, args []any, opMs int, ft frameTarget) (*CallResult, error) {
	dx, dy, opts, err := parseScrollDeltas("tab.scroll()", args)
	if err != nil {
		return nil, err
	}
	if opts != nil {
		if sel, ok := opts["selector"].(string); ok && sel != "" {
			if err := t.waitForSelectorPresent(ctx, fmt.Sprintf("tab.scroll(%s)", jsLit(sel)), sel, opMs, ft); err != nil {
				return nil, err
			}
			elExpr, err := t.selectorExpr(sel)
			if err != nil {
				return nil, err
			}
			_, err = t.evalKitAction(ctx, ft, fmt.Sprintf("(%s).scrollBy(%f, %f)", elExpr, dx, dy))
			return &CallResult{}, err
		}
	}
	_, err = t.evalValue(ctx, fmt.Sprintf("window.scrollBy(%f, %f)", dx, dy), ft.execCtxID)
	return &CallResult{}, err
}

func (t *cdpTab) dragBetween(ctx context.Context, args []any, opMs int, ft frameTarget) (*CallResult, error) {
	if len(args) < 2 {
		return nil, errors.New("tab.drag() requires from and to targets")
	}
	// Upstream fails at once when drag selectors do not resolve (tab-worker.ts:4122).
	resolvePoint := func(target any, role string) (float64, float64, error) {
		switch v := target.(type) {
		case string:
			if err := ValidateSelector(v); err != nil {
				return 0, 0, err
			}
			if strings.HasPrefix(strings.TrimSpace(v), "role/") {
				if _, err := ParseRoleSelector(strings.TrimSpace(v)[5:]); err != nil {
					return 0, 0, err
				}
			}
			elExpr := fmt.Sprintf(`(function(s){ const el = globalThis.__ompBrowserKit.queryFirst(s); if (!el) throw new Error("Drag %s selector did not resolve: " + s); return el; })(%s)`, role, jsLit(v))
			label := fmt.Sprintf("tab.drag() %s %s", role, jsLit(v))
			return t.elementActionableCenter(ctx, label, elExpr, false, opMs, ft)
		case map[string]any:
			x, _ := v["x"].(float64)
			y, _ := v["y"].(float64)
			css, _ := v["css"].(bool)
			return t.mapPointerPoint(x, y, css)
		default:
			return 0, 0, errors.New("drag target must be a selector string or {x, y}")
		}
	}
	x1, y1, err := resolvePoint(args[0], "from")
	if err != nil {
		return nil, err
	}
	x2, y2, err := resolvePoint(args[1], "to")
	if err != nil {
		return nil, err
	}
	if _, err := t.dispatchMouseMove(ctx, x1, y1); err != nil {
		return nil, err
	}
	if _, err := t.dispatchMouseButton(ctx, "mousePressed", x1, y1, "left", 1); err != nil {
		return nil, err
	}
	steps := 5
	for i := 1; i <= steps; i++ {
		f := float64(i) / float64(steps)
		if _, err := t.dispatchMouseMove(ctx, x1+(x2-x1)*f, y1+(y2-y1)*f); err != nil {
			return nil, err
		}
	}
	return t.dispatchMouseButton(ctx, "mouseReleased", x2, y2, "left", 1)
}

func (t *cdpTab) scrollIntoViewSelector(ctx context.Context, selector string, opMs int, ft frameTarget) (*CallResult, error) {
	if err := t.waitForSelectorPresent(ctx, fmt.Sprintf("tab.scrollIntoView(%s)", jsLit(selector)), selector, opMs, ft); err != nil {
		return nil, err
	}
	elExpr, err := t.selectorExpr(selector)
	if err != nil {
		return nil, err
	}
	_, err = t.evalKitAction(ctx, ft, fmt.Sprintf(`(%s).scrollIntoView({block:"center",inline:"center"})`, elExpr))
	return &CallResult{}, err
}

func (t *cdpTab) highlightSelector(ctx context.Context, selector string, opts map[string]any, opMs int, ft frameTarget) (*CallResult, error) {
	if err := t.waitForSelectorPresent(ctx, fmt.Sprintf("tab.highlight(%s)", jsLit(selector)), selector, opMs, ft); err != nil {
		return nil, err
	}
	elExpr, err := t.selectorExpr(selector)
	if err != nil {
		return nil, err
	}
	dur := 1500
	if opts != nil {
		if d, ok := opts["duration"].(float64); ok && d > 0 {
			dur = int(d)
		}
	}
	_, err = t.evalKitAction(ctx, ft, fmt.Sprintf("globalThis.__ompBrowserKit.highlight(%s, %d)", elExpr, dur))
	return &CallResult{}, err
}

func (t *cdpTab) selectSelector(ctx context.Context, selector string, values []any, opMs int, ft frameTarget) (*CallResult, error) {
	if err := t.waitForSelectorPresent(ctx, fmt.Sprintf("tab.select(%s)", jsLit(selector)), selector, opMs, ft); err != nil {
		return nil, err
	}
	elExpr, err := t.selectorExpr(selector)
	if err != nil {
		return nil, err
	}
	return t.selectExpr(ctx, elExpr, values, ft)
}
func (t *cdpTab) selectExpr(ctx context.Context, elExpr string, values []any, ft frameTarget) (*CallResult, error) {
	type choice struct {
		Kind  string `json:"kind"`
		Value any    `json:"value"`
	}
	choices := make([]choice, 0, len(values))
	for _, raw := range values {
		switch v := raw.(type) {
		case string:
			choices = append(choices, choice{Kind: "string", Value: v})
		case map[string]any:
			if len(v) == 1 {
				if lbl, ok := v["label"].(string); ok {
					choices = append(choices, choice{Kind: "label", Value: lbl})
					continue
				}
				if val, ok := v["value"].(string); ok {
					choices = append(choices, choice{Kind: "value", Value: val})
					continue
				}
				if idx, ok := v["index"].(float64); ok && idx >= 0 && idx == float64(int(idx)) {
					choices = append(choices, choice{Kind: "index", Value: int(idx)})
					continue
				}
			}
			return nil, errors.New("select() expects strings, {label: string}, {value: string}, or {index: non-negative integer}")
		default:
			return nil, errors.New("select() expects strings, {label: string}, {value: string}, or {index: non-negative integer}")
		}
	}
	encoded, _ := json.Marshal(choices)
	v, err := t.evalKitAction(ctx, ft, fmt.Sprintf("globalThis.__ompBrowserKit.selectOptions(%s, %s)", elExpr, string(encoded)))
	return &CallResult{Value: v}, err
}

func (t *cdpTab) uploadFileSelector(ctx context.Context, callCtx CallContext, selector string, paths []any, opMs int, ft frameTarget) (*CallResult, error) {
	if err := t.waitForSelectorPresent(ctx, fmt.Sprintf("tab.uploadFile(%s)", jsLit(selector)), selector, opMs, ft); err != nil {
		return nil, err
	}
	elExpr, err := t.selectorExpr(selector)
	if err != nil {
		return nil, err
	}
	return t.uploadFileExpr(ctx, callCtx, elExpr, paths, ft)
}

func (t *cdpTab) uploadFileExpr(ctx context.Context, callCtx CallContext, elExpr string, rawPaths []any, ft frameTarget) (*CallResult, error) {
	files := make([]string, 0, len(rawPaths))
	for _, p := range rawPaths {
		s, ok := p.(string)
		if !ok || s == "" {
			return nil, errors.New("uploadFile() expects file path strings")
		}
		abs, err := filepath.Abs(resolveToCwd(s, callCtx.Cwd))
		if err != nil {
			return nil, err
		}
		if _, err := os.Stat(abs); err != nil {
			return nil, fmt.Errorf("uploadFile(): %w", err)
		}
		files = append(files, abs)
	}
	objID, err := t.evalKitReadObjectID(ctx, ft, elExpr)
	if err != nil {
		return nil, err
	}
	if objID == "" {
		return nil, errors.New("uploadFile(): failed to resolve file input element")
	}
	defer func() {
		relCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		_ = t.send(relCtx, "Runtime.releaseObject", map[string]any{"objectId": objID}, nil)
		cancel()
	}()
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
