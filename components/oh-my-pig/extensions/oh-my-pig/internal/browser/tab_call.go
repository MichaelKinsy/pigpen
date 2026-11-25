package browser

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// Ordered method lists matching omp's tab-call.ts verbatim so error messages
// list every supported method in upstream order.
// upstream: tab-call.ts:14-175
var (
	tabValueMethodList = []string{
		"url", "title", "goto", "back", "forward", "reload", "pushState",
		"frames", "dialog", "handleDialog", "setDialogs",
		"observe", "ariaSnapshot", "a11y",
		"webmcpList", "webmcpInvoke", "webmcpEvents",
		"screenshot", "diffScreenshot", "pdf", "extract",
		"click", "dblclick", "hover", "focus", "check", "uncheck",
		"keyDown", "keyUp", "mouseMove", "mouseDown", "mouseUp", "clickAt", "wheel", "highlight",
		"type", "fill", "press", "scroll", "drag", "scrollIntoView",
		"select", "uploadFile", "waitForUrl",
		"text", "html", "value", "attr", "count", "box", "styles",
		"isVisible", "isEnabled", "isChecked", "waitForText", "evaluate",
		"emulate", "devices",
		"clipboardRead", "clipboardWrite", "clipboardCopy", "clipboardPaste",
		"cookies", "setCookies", "clearCookies",
		"storage", "setStorage", "clearStorage", "saveState", "loadState",
		"addInitScript", "removeInitScript", "initScripts",
		"waitForDownload", "downloads",
		"console", "errors", "clearConsole",
		"traceStart", "traceStop", "profileStart", "profileStop", "metrics",
		"route", "unroute", "routes",
		"requests", "request", "clearRequests",
		"harStart", "harStop", "allowedDomains",
		"vitals", "reactEnable", "reactTree", "reactInspect", "reactRenders", "reactSuspense",
		"recordStart", "recordStop", "recordRestart", "recording",
	}
	tabPresenceMethodList = []string{"waitFor", "waitForSelector"}
	elementMethodList     = []string{
		"click", "dblclick", "check", "uncheck", "highlight",
		"type", "fill", "press", "hover", "focus", "select",
		"uploadFile", "scrollIntoView", "boundingBox", "isVisible", "isHidden",
		"text", "html", "value", "attr", "styles",
		"isEnabled", "isChecked", "evaluate",
	}
	frameMethodList = []string{
		"click", "fill", "type", "press",
		"text", "html", "value", "attr", "count",
		"isVisible", "ariaSnapshot", "evaluate",
		"waitFor", "waitForSelector", "screenshot",
	}

	directMethodsDescription  = strings.Join(append(append([]string(nil), tabValueMethodList...), tabPresenceMethodList...), ", ")
	elementMethodsDescription = strings.Join(elementMethodList, ", ")
	frameMethodsDescription   = strings.Join(frameMethodList, ", ")
)

// TabValueMethods are tab helpers whose resolved value is returned directly.
// upstream: TAB_VALUE_METHODS in tab-call.ts:14
var TabValueMethods = makeMethodSet(tabValueMethodList)

// TabPresenceMethods are tab helpers whose handle-or-null result is returned as a bool.
// upstream: TAB_PRESENCE_METHODS in tab-call.ts:120
var TabPresenceMethods = makeMethodSet(tabPresenceMethodList)

// TabHandleMethods return an element or frame handle for one chained call.
// upstream: TAB_HANDLE_METHODS in tab-call.ts:123
var TabHandleMethods = map[string]bool{
	"id":    true,
	"ref":   true,
	"frame": true,
}

// ElementMethods are allowed on element handles returned by tab.id() and tab.ref().
// upstream: ELEMENT_METHODS in tab-call.ts:126
var ElementMethods = makeMethodSet(elementMethodList)

// FrameMethods are allowed on frame handles returned by tab.frame().
// upstream: FRAME_METHODS in tab-call.ts:154
var FrameMethods = makeMethodSet(frameMethodList)

func makeMethodSet(list []string) map[string]bool {
	m := make(map[string]bool, len(list))
	for _, item := range list {
		m[item] = true
	}
	return m
}

// ValidateTabCallChain validates a call chain against omp's allowlist, returning
// omp's exact actionable error messages on invalid chains.
// upstream: renderTabCall in tab-call.ts:194
func ValidateTabCallChain(chain []CallStep) error {
	if len(chain) == 0 {
		return fmt.Errorf("Action 'call' requires a non-empty 'chain'.")
	}
	if len(chain) > 2 {
		return fmt.Errorf("Call chains support one element-handle hop at most; use tab.run(fn) for longer sequences.")
	}
	root := chain[0]
	if TabValueMethods[root.Method] || TabPresenceMethods[root.Method] {
		if len(chain) == 2 {
			return fmt.Errorf("Only tab.id(n)/tab.ref(id)/tab.frame(selector) results accept a chained call; got tab.%s().", root.Method)
		}
		return nil
	}
	if TabHandleMethods[root.Method] {
		isFrame := root.Method == "frame"
		if len(chain) == 1 {
			if isFrame {
				return fmt.Errorf(`tab.frame() returns a frame handle; call a method on it (tab.frame("#pay").click("#submit")) or use tab.run(fn).`)
			}
			return fmt.Errorf("tab.%s() returns an element handle; call a method on it (tab.id(5).click()) or use tab.run(fn).", root.Method)
		}
		step := chain[1]
		if isFrame {
			if !FrameMethods[step.Method] {
				return fmt.Errorf("Unknown frame method %q. Frame handles support: %s.", step.Method, frameMethodsDescription)
			}
			return nil
		}
		if !ElementMethods[step.Method] {
			return fmt.Errorf("Unknown element method %q. Element handles support: %s.", step.Method, elementMethodsDescription)
		}
		return nil
	}
	return fmt.Errorf("Unknown tab helper %q. Direct helpers: %s; handles via tab.id(n)/tab.ref(id)/tab.frame(selector).", root.Method, directMethodsDescription)
}

const (
	browserDefaultTimeoutSec = 30.0
	browserMinTimeoutSec     = 1.0
	browserMaxTimeoutSec     = 300.0
	cellBudgetSlackMs        = 2000
	quickOpTimeoutMs         = 20000
	actionOpTimeoutMs        = 8000
	zeroMatchFailFastMs      = 2000
)

// ClampTimeoutMs clamps a browser timeout in seconds (matching omp's
// clampTimeout without rounding fractional seconds up) and returns milliseconds.
// upstream: clampTimeout("browser", ...) in tool-timeouts.ts:34-39
func ClampTimeoutMs(requested *float64, maxTimeoutSec int) int {
	return int(math.Round(ClampTimeoutSec(requested, maxTimeoutSec) * 1000))
}

// ClampTimeoutSec clamps a browser timeout in seconds to omp's [1, 300] range
// (further capped by maxTimeoutSec if > 0).
// upstream: clampTimeout("browser", ...) in tool-timeouts.ts:34-39
func ClampTimeoutSec(requested *float64, maxTimeoutSec int) float64 {
	val := browserDefaultTimeoutSec
	if requested != nil && !math.IsNaN(*requested) && !math.IsInf(*requested, 0) {
		val = *requested
	}
	if maxTimeoutSec > 0 && val > float64(maxTimeoutSec) {
		val = float64(maxTimeoutSec)
	}
	if val > browserMaxTimeoutSec {
		val = browserMaxTimeoutSec
	}
	if val < browserMinTimeoutSec {
		val = browserMinTimeoutSec
	}
	return val
}

// TabCallBudgetSeconds computes the effective budget in seconds for a call/run
// when no explicit timeout is given, incorporating cellTimeoutMs and trailing
// wait options. Only numeric 0 (or +Inf) requests the max budget; JSON null
// does NOT trigger max budget.
// upstream: tabCallBudgetSeconds in tab-call.ts:251-266
func TabCallBudgetSeconds(chain []CallStep, cellTimeoutMs int, hasCellTimeout bool) *float64 {
	var budget float64
	hasBudget := false
	if hasCellTimeout {
		if cellTimeoutMs == 0 {
			budget = browserMaxTimeoutSec
			hasBudget = true
		} else if cellTimeoutMs > 0 {
			budget = math.Ceil(float64(cellTimeoutMs) / 1000.0)
			hasBudget = true
		}
	}
	if len(chain) > 0 {
		last := chain[len(chain)-1]
		if len(last.Args) > 0 {
			var opts map[string]any
			if err := unmarshalJSON(last.Args[len(last.Args)-1], &opts); err == nil && opts != nil {
				if rawT, ok := opts["timeout"]; ok {
					if v, ok := rawT.(float64); ok {
						if v == 0 || math.IsInf(v, 1) {
							if !hasBudget || browserMaxTimeoutSec > budget {
								budget = browserMaxTimeoutSec
								hasBudget = true
							}
						} else if !math.IsInf(v, 0) && !math.IsNaN(v) && v >= 0 {
							b := math.Ceil((v + cellBudgetSlackMs) / 1000.0)
							if !hasBudget || b > budget {
								budget = b
								hasBudget = true
							}
						}
					}
				}
			}
		}
	}
	if !hasBudget {
		return nil
	}
	if budget < browserDefaultTimeoutSec {
		budget = browserDefaultTimeoutSec
	}
	return &budget
}

// resolveOpTimeouts computes budgetBound, quickOpMs, and actionOpMs from a call budget.
// upstream: resolveOpTimeouts in op-timeouts.ts:44-51
func resolveOpTimeouts(cellTimeoutMs int) (budgetBound, quickOpMs, actionOpMs int) {
	if cellTimeoutMs <= 0 {
		cellTimeoutMs = int(browserDefaultTimeoutSec * 1000)
	}
	budgetBound = cellTimeoutMs - cellBudgetSlackMs
	if budgetBound < 1 {
		budgetBound = 1
	}
	quickOpMs = min(budgetBound, quickOpTimeoutMs)
	actionOpMs = min(budgetBound, actionOpTimeoutMs)
	return budgetBound, quickOpMs, actionOpMs
}

// resolveWaitTimeout computes the effective wait duration in milliseconds.
// upstream: resolveWaitTimeout in op-timeouts.ts:61-69
func resolveWaitTimeout(cellTimeoutMs int, explicit *float64) int {
	budgetBound, _, actionOpMs := resolveOpTimeouts(cellTimeoutMs)
	if explicit == nil {
		return budgetBound
	}
	v := *explicit
	if v == 0 || math.IsInf(v, 1) {
		return budgetBound
	}
	if !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0 {
		ms := int(math.Round(v))
		if ms > budgetBound {
			return budgetBound
		}
		return ms
	}
	return actionOpMs
}

// browserTextStart matches omp's browserTextStart in browser.ts:376-387:
// entered text (often a credential) is masked as <hidden> in the status line.
func browserTextStart(method, previous string) (int, bool) {
	switch method {
	case "fill", "type":
		if previous == "" || previous == "frame" {
			return 1, true
		}
		return 0, true
	case "clipboardWrite", "handleDialog":
		return 0, true
	default:
		return 0, false
	}
}

// FormatStatusCallChain builds the status-tree line matching omp's
// renderStatusCallChain + browserTextStart (hiding entered text as `<hidden>`).
// upstream: describeBrowserCall in browser.ts:348-387, renderStatusCallChain in run-code.ts:92-105
func FormatStatusCallChain(name string, chain []CallStep) string {
	var b strings.Builder
	b.WriteString(name)
	for idx, step := range chain {
		b.WriteByte('.')
		b.WriteString(step.Method)
		b.WriteByte('(')
		prev := ""
		if idx > 0 {
			prev = chain[idx-1].Method
		}
		textStart, hasTextStart := browserTextStart(step.Method, prev)
		for ai, raw := range step.Args {
			if ai > 0 {
				b.WriteString(", ")
			}
			if hasTextStart && ai >= textStart {
				b.WriteString("<hidden>")
				continue
			}
			b.WriteString(renderStatusArg(raw))
		}
		b.WriteByte(')')
	}
	return b.String()
}

func renderStatusArg(raw json.RawMessage) string {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return strings.TrimSpace(string(raw))
	}
	return renderStatusValue(v)
}

func renderStatusValue(v any) string {
	if m, ok := v.(map[string]any); ok && len(m) == 1 {
		if fn, ok := m["__omp_fn"].(string); ok {
			return "(" + fn + ")"
		}
		if reObj, ok := m["__omp_re"].(map[string]any); ok {
			if src, ok := reObj["source"].(string); ok {
				flags, _ := reObj["flags"].(string)
				sb, _ := json.Marshal(src)
				fb, _ := json.Marshal(flags)
				return fmt.Sprintf("new RegExp(%s, %s)", sb, fb)
			}
		}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}
