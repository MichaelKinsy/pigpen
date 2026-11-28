package ternbrowser

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/browser"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/ternwire"
)

// Helper types & functions ported from tern-tab.ts, dialogs.ts, storage-state.ts, select-options.ts.

const (
	eventLogLimit          = 4000
	consoleLimit           = 500
	navigationIdleDuration = 500 * time.Millisecond
	blankURL               = "about:blank"
	resolveStableUpdates   = 2
	resolveSettleMs        = 1000
	scrollWheelLimit       = 8
	waitReplyGrace         = 5 * time.Second
	handleLimit            = 10000
	zeroMatchFailFast      = 2 * time.Second
	defaultViewportW       = 1280
	defaultViewportH       = 800
)

var defaultStyleProperties = []string{
	"font-family", "font-size", "font-weight", "color", "background-color",
	"display", "visibility", "opacity", "position", "z-index",
	"width", "height", "margin", "padding",
}

var resolveActions = map[string]string{
	"click":    "click",
	"dblclick": "click",
	"check":    "click",
	"upload":   "click",
	"hover":    "hover",
	"drag":     "hover",
	"type":     "type",
	"fill":     "type",
	"press":    "none",
	"focus":    "none",
	"point":    "none",
}

var pickedInputTypes = map[string]bool{
	"date": true, "datetime-local": true, "month": true,
	"week": true, "time": true, "color": true, "range": true,
}

var formValueTags = map[string]bool{
	"input": true, "textarea": true, "select": true,
}

// pageOpDialog classifies block ops blocked/answered by a held confirm or prompt.
// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:269
var pageOpDialog = map[string]string{
	"eval":    "blocks",
	"dom":     "blocks",
	"edit":    "blocks",
	"capture": "blocks",
	"pdf":     "blocks",
	"input":   "answers",
}

// DialogState is the JSON-safe description of the currently pending JS dialog.
// upstream: packages/coding-agent/src/tools/browser/dialogs.ts:16
type DialogState struct {
	Open         bool   `json:"open"`
	Type         string `json:"type,omitempty"`
	Message      string `json:"message,omitempty"`
	DefaultValue string `json:"defaultValue,omitempty"`
}

func dialogBlocksPageError(label string, state DialogState, when string) error {
	t := state.Type
	if t == "" {
		t = "JavaScript"
	}
	msg := state.Message
	if len(msg) > 80 {
		msg = msg[:80]
	}
	whenText := "is open"
	if when != "is open" {
		whenText = "opened while it ran"
	}
	return fmt.Errorf(
		`%s: a %s(%s) dialog %s and blocks the page until it is answered; call tab.handleDialog({ accept: true }) or tab.handleDialog({ accept: false }), or tab.setDialogs("accept" | "dismiss") to answer every dialog`,
		label, t, jsonQuote(msg), whenText,
	)
}

func assertDialogPolicy(policy *string) error {
	if policy == nil || *policy == "accept" || *policy == "dismiss" {
		return nil
	}
	return fmt.Errorf(`tab.setDialogs() takes "accept", "dismiss" or null (back to the default); got %s`, jsonQuote(*policy))
}

func noWebMcp(product string) string {
	return fmt.Sprintf("the %s web view has no native WebMCP; open the tab with `app: \"chromium\"` to use Chromium", product)
}

func unsupportedErr(product, name, reason string) error {
	return fmt.Errorf("tab.%s() is not supported on the %s browser backend: %s", name, product, reason)
}

var pageErrPrefixRE = regexp.MustCompile(`^\S+ browser \w+ failed \(js\): `)

func pageErrorText(err *ternwire.Error) string {
	return pageErrPrefixRE.ReplaceAllString(err.Message, "")
}

func kitErrorText(err *ternwire.Error) string {
	text := pageErrorText(err)
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		text = text[:idx]
	}
	return strings.TrimPrefix(text, "Error: ")
}

func staleIDErr(id int) error {
	return fmt.Errorf("Element id %d is stale. Run tab.observe() again.", id)
}

func unknownIDErr(id int) error {
	return fmt.Errorf("Unknown element id %d. Run tab.observe() to refresh the element list.", id)
}

// targetTimeout formats the upstream actionability timeout error.
// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:381
func targetTimeout(label string, elapsed time.Duration, reason string, count int, detail string) error {
	var why string
	switch reason {
	case "missing":
		why = "no element matches (wrong page or selector?)"
	case "covered":
		if detail == "" {
			detail = "another element"
		}
		why = "the element is covered by " + detail
	case "hidden":
		why = "the element is not visible"
	case "disabled":
		why = "the element is disabled"
	case "notEditable":
		why = "the element is not editable"
	case "offViewport":
		why = "the element is outside the viewport"
	default:
		why = "the element keeps moving"
	}
	return fmt.Errorf("%s timed out after %dms: %s (%d match(es))", label, elapsed.Milliseconds(), why, count)
}

func isPickedControl(node DomNode) bool {
	if node.Tag == "select" {
		return true
	}
	if node.Tag == "input" {
		return pickedInputTypes[strings.ToLower(node.Attributes["type"])]
	}
	return false
}

// storageStatePath resolves the saveState file path, sanitising the tab name
// and using this Piglet's agent directory (PIG_CODING_AGENT_DIR -> sdk.AgentDir()
// -> ~/.pig/agent) + "browser-state/<name>.json" (N3).
// upstream: packages/coding-agent/src/tools/browser/storage-state.ts:471-477
var unsafeStateNameRE = regexp.MustCompile(`[^A-Za-z0-9._-]`)

func storageStatePath(tabName string, requestedPath string, cwd string) string {
	if requestedPath != "" {
		return resolveToCwd(requestedPath, cwd)
	}
	safe := unsafeStateNameRE.ReplaceAllString(tabName, "_")
	if safe == "." || safe == ".." {
		safe = "_"
	} else if safe == "" {
		safe = "main"
	}
	baseDir := resolveAgentDir()
	return filepath.Join(baseDir, "browser-state", safe+".json")
}

func resolveAgentDir() string {
	if dir := strings.TrimSpace(os.Getenv("PIG_CODING_AGENT_DIR")); dir != "" {
		return dir
	}
	if dir, err := sdk.AgentDir(); err == nil && dir != "" {
		return dir
	}
	if dir := strings.TrimSpace(os.Getenv("PIG_HOME")); dir != "" {
		return filepath.Join(dir, "agent")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".pig", "agent")
	}
	return filepath.Join(os.TempDir(), "pig-agent")
}

// UserSourceFunction wraps a JS expression or statement list into a function string (N2).
// Token-aware scan skips string literals, template literals, comments, and regex literals
// so `;` inside strings (`document.cookie.split(";").length`) or `=>` in expressions
// (`[1,2].map(x => x * 2)`) never misclassify an expression as a statement list.
// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:360-371
var stmtLeadingKeywordRE = regexp.MustCompile(`^(?:const|let|var|return|if|for|while|do|switch|try|throw|break|continue|debugger|import|export)\b`)

func UserSourceFunction(source string) string {
	if isJSStatementList(source) {
		return "function () {\n" + source + "\n}"
	}
	return "function () {\nreturn (\n" + source + "\n);\n}"
}

// isJSStatementList returns true when source is a JS statement list rather than
// a single expression: starts with a statement-only keyword (after skipping leading
// whitespace/comments), or contains a top-level `;` or top-level `return` keyword
// outside strings/templates/comments/nesting.
func isJSStatementList(src string) bool {
	n := len(src)
	i := skipWSAndComments(src, 0)
	if i >= n {
		return false
	}
	if src[i] == ';' {
		return true
	}
	if stmtLeadingKeywordRE.MatchString(src[i:]) {
		return true
	}
	// Track bracket depth and scan top-level tokens.
	depth := 0
	canRegex := true
	for i < n {
		ch := src[i]
		// Whitespace
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' || ch == '\f' || ch == '\v' {
			i++
			continue
		}
		// Line comment
		if ch == '/' && i+1 < n && src[i+1] == '/' {
			i += 2
			for i < n && src[i] != '\n' {
				i++
			}
			continue
		}
		// Block comment
		if ch == '/' && i+1 < n && src[i+1] == '*' {
			i += 2
			for i+1 < n && !(src[i] == '*' && src[i+1] == '/') {
				i++
			}
			i += 2
			continue
		}
		// String literal
		if ch == '\'' || ch == '"' {
			quote := ch
			i++
			for i < n {
				if src[i] == '\\' {
					i += 2
					continue
				}
				if src[i] == quote {
					i++
					break
				}
				i++
			}
			canRegex = false
			continue
		}
		// Template literal
		if ch == '`' {
			i++
			tmplExprDepth := 0
			for i < n {
				if src[i] == '\\' {
					i += 2
					continue
				}
				if tmplExprDepth == 0 && src[i] == '`' {
					i++
					break
				}
				if src[i] == '$' && i+1 < n && src[i+1] == '{' {
					tmplExprDepth++
					i += 2
					continue
				}
				if tmplExprDepth > 0 && src[i] == '}' {
					tmplExprDepth--
					i++
					continue
				}
				i++
			}
			canRegex = false
			continue
		}
		// Regex literal (when `/` appears in expression-start position)
		if ch == '/' && canRegex {
			i++
			inClass := false
			for i < n && src[i] != '\n' {
				if src[i] == '\\' {
					i += 2
					continue
				}
				if src[i] == '[' {
					inClass = true
				} else if src[i] == ']' {
					inClass = false
				} else if src[i] == '/' && !inClass {
					i++
					for i < n && ((src[i] >= 'a' && src[i] <= 'z') || (src[i] >= 'A' && src[i] <= 'Z')) {
						i++
					}
					break
				}
				i++
			}
			canRegex = false
			continue
		}
		// Nesting delimiters
		if ch == '(' || ch == '[' || ch == '{' {
			depth++
			canRegex = true
			i++
			continue
		}
		if ch == ')' || ch == ']' || ch == '}' {
			if depth > 0 {
				depth--
			}
			canRegex = false
			i++
			continue
		}
		// Top-level semicolon -> statement list
		if ch == ';' && depth == 0 {
			return true
		}
		// Identifiers / keywords
		if isJSIdentStart(ch) {
			start := i
			i++
			for i < n && isJSIdentPart(src[i]) {
				i++
			}
			word := src[start:i]
			if depth == 0 && word == "return" {
				return true
			}
			switch word {
			case "return", "typeof", "void", "delete", "in", "instanceof", "of", "new", "throw", "case", "yield", "await":
				canRegex = true
			default:
				canRegex = false
			}
			continue
		}
		// Numbers
		if ch >= '0' && ch <= '9' {
			for i < n && (isJSIdentPart(src[i]) || src[i] == '.') {
				i++
			}
			canRegex = false
			continue
		}
		// Operators / punctuation
		canRegex = true
		i++
	}
	return false
}

func skipWSAndComments(src string, i int) int {
	n := len(src)
	for i < n {
		ch := src[i]
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' || ch == '\f' || ch == '\v' {
			i++
			continue
		}
		if ch == '/' && i+1 < n && src[i+1] == '/' {
			i += 2
			for i < n && src[i] != '\n' {
				i++
			}
			continue
		}
		if ch == '/' && i+1 < n && src[i+1] == '*' {
			i += 2
			for i+1 < n && !(src[i] == '*' && src[i+1] == '/') {
				i++
			}
			i += 2
			continue
		}
		break
	}
	return i
}

func isJSIdentStart(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_' || ch == '$'
}

func isJSIdentPart(ch byte) bool {
	return isJSIdentStart(ch) || (ch >= '0' && ch <= '9')
}

// BrowserCookie matches upstream BrowserCookie (storage-state.ts:15).
type BrowserCookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Expires  float64 `json:"expires"`
	HTTPOnly bool    `json:"httpOnly"`
	Secure   bool    `json:"secure"`
	SameSite string  `json:"sameSite"`
}

// cookieFromTern normalizes a cookie from Tern's "cookies" op.
// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:455
func cookieFromTern(raw map[string]any) (BrowserCookie, bool) {
	name, ok1 := raw["name"].(string)
	val, ok2 := raw["value"].(string)
	if !ok1 || !ok2 {
		return BrowserCookie{}, false
	}
	sameSite := "Lax"
	if s, ok := raw["sameSite"].(string); ok && (s == "Strict" || s == "None") {
		sameSite = s
	}
	domain, _ := raw["domain"].(string)
	p, _ := raw["path"].(string)
	if p == "" {
		p = "/"
	}
	exp := float64(-1)
	if f, ok := numVal(raw["expires"]); ok {
		exp = f
	}
	httpOnly, _ := raw["httpOnly"].(bool)
	secure, _ := raw["secure"].(bool)
	return BrowserCookie{
		Name:     name,
		Value:    val,
		Domain:   domain,
		Path:     p,
		Expires:  exp,
		HTTPOnly: httpOnly,
		Secure:   secure,
		SameSite: sameSite,
	}, true
}

// cookieMatchesURL checks domain, path, and scheme (S2).
// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:432
func cookieMatchesURL(cookie BrowserCookie, u *url.URL) bool {
	if u == nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	rawDomain := strings.ToLower(cookie.Domain)
	domain := strings.TrimPrefix(rawDomain, ".")
	var domainMatch bool
	if strings.HasPrefix(rawDomain, ".") {
		domainMatch = host == domain || strings.HasSuffix(host, "."+domain)
	} else {
		domainMatch = host == domain
	}
	if !domainMatch {
		return false
	}
	if cookie.Secure && !strings.EqualFold(u.Scheme, "https") {
		return false
	}
	cp := cookie.Path
	if cp == "" {
		cp = "/"
	}
	up := u.Path
	if up == "" {
		up = "/"
	}
	if up == cp {
		return true
	}
	prefix := cp
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return strings.HasPrefix(up, prefix)
}

// ConsoleEntry is one captured console or error record.
type ConsoleEntry struct {
	Seq      int    `json:"seq"`
	TS       int64  `json:"ts"`
	Type     string `json:"type"` // "console" | "pageerror" | "requestfailed"
	Level    string `json:"level"`
	Text     string `json:"text"`
	Location string `json:"location,omitempty"`
	Stack    string `json:"stack,omitempty"`
	Args     []any  `json:"args,omitempty"`
}

func consoleLevel(v any) string {
	s, _ := v.(string)
	switch s {
	case "info", "warn", "error", "debug":
		return s
	case "warning":
		return "warn"
	default:
		return "log"
	}
}

// BrowserDownload is one completed download record.
type BrowserDownload struct {
	Path              string `json:"path"`
	SuggestedFilename string `json:"suggestedFilename"`
	URL               string `json:"url"`
	Bytes             int64  `json:"bytes"`
}

// SelectOptionChoice matches upstream SelectOptionChoice (select-options.ts:7).
type SelectOptionChoice struct {
	Kind  string `json:"kind"` // "string" | "label" | "value" | "index"
	Value any    `json:"value"`
}

// NormalizeSelectOptions validates and normalizes tab.select() arguments.
// upstream: packages/coding-agent/src/tools/browser/select-options.ts:16
func NormalizeSelectOptions(rawArgs []json.RawMessage) ([]SelectOptionChoice, error) {
	choices := make([]SelectOptionChoice, 0, len(rawArgs))
	for _, raw := range rawArgs {
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("select() expects strings, {label: string}, {value: string}, or {index: non-negative integer}")
		}
		switch x := v.(type) {
		case string:
			choices = append(choices, SelectOptionChoice{Kind: "string", Value: x})
		case nil, bool, float64:
			choices = append(choices, SelectOptionChoice{Kind: "string", Value: fmt.Sprintf("%v", x)})
		case map[string]any:
			if len(x) != 1 {
				return nil, fmt.Errorf("select() expects strings, {label: string}, {value: string}, or {index: non-negative integer}")
			}
			if lbl, ok := x["label"].(string); ok {
				choices = append(choices, SelectOptionChoice{Kind: "label", Value: lbl})
			} else if val, ok := x["value"].(string); ok {
				choices = append(choices, SelectOptionChoice{Kind: "value", Value: val})
			} else if idx, ok := x["index"].(float64); ok && idx >= 0 && idx == math.Trunc(idx) {
				choices = append(choices, SelectOptionChoice{Kind: "index", Value: int(idx)})
			} else {
				return nil, fmt.Errorf("select() expects strings, {label: string}, {value: string}, or {index: non-negative integer}")
			}
		default:
			return nil, fmt.Errorf("select() expects strings, {label: string}, {value: string}, or {index: non-negative integer}")
		}
	}
	return choices, nil
}

// TernEvent is one entry of Tern's "events" op.
// upstream: packages/coding-agent/src/tools/browser/tern/tern-tab.ts:259
type TernEvent struct {
	Seq  int
	Type string
	Raw  map[string]any
}

type initScriptEntry struct {
	ID     string `json:"id"`
	Source string `json:"source"`
}

type downloadTrack struct {
	URL   string
	Path  string
	State string
	Err   string
}

// parseScrollDeltas implements upstream scrollDeltas (tab-arguments.ts:89).
func parseScrollDeltas(label string, args []json.RawMessage) (dx, dy float64, optArgIdx int, err error) {
	if len(args) == 0 {
		return 0, 0, 0, nil
	}
	var first any
	if err := json.Unmarshal(args[0], &first); err != nil {
		return 0, 0, 0, fmt.Errorf("%s takes pixel deltas as numbers: (dx, dy) or ({x, y})", label)
	}
	if obj, ok := first.(map[string]any); ok {
		_, hasDX := obj["deltaX"]
		_, hasDY := obj["deltaY"]
		_, hasX := obj["x"]
		_, hasY := obj["y"]
		if !hasDX && !hasDY && !hasX && !hasY {
			b, _ := json.Marshal(obj)
			return 0, 0, 0, fmt.Errorf("%s takes pixel deltas as numbers: (dx, dy) or ({x, y}); got %s", label, string(b))
		}
		if f, ok := numVal(obj["deltaX"]); ok {
			dx = f
		} else if f, ok := numVal(obj["x"]); ok {
			dx = f
		}
		if f, ok := numVal(obj["deltaY"]); ok {
			dy = f
		} else if f, ok := numVal(obj["y"]); ok {
			dy = f
		}
		return dx, dy, 1, nil
	}
	if first != nil {
		f, ok := numVal(first)
		if !ok {
			return 0, 0, 0, fmt.Errorf("%s takes pixel deltas as numbers: (dx, dy) or ({x, y})", label)
		}
		dx = f
	}
	if len(args) > 1 {
		var second any
		if err := json.Unmarshal(args[1], &second); err == nil && second != nil {
			f, ok := numVal(second)
			if !ok {
				return 0, 0, 0, fmt.Errorf("%s takes pixel deltas as numbers: (dx, dy) or ({x, y})", label)
			}
			dy = f
		}
	}
	return dx, dy, 2, nil
}

// resolveToCwd resolves path relative to cwd (or os.TempDir if cwd empty).
func resolveToCwd(p, cwd string) string {
	if filepath.IsAbs(p) {
		return p
	}
	if cwd == "" {
		cwd = os.TempDir()
	}
	return filepath.Join(cwd, p)
}

func withoutTrailingNil(args []any) []any {
	end := len(args)
	for end > 0 && args[end-1] == nil {
		end--
	}
	return args[:end]
}

// parseArg unmarshals args[idx] into dst if present and non-null.
func parseArg(args []json.RawMessage, idx int, dst any) error {
	if idx >= len(args) || len(args[idx]) == 0 || strings.TrimSpace(string(args[idx])) == "null" {
		return nil
	}
	return json.Unmarshal(args[idx], dst)
}

func argString(args []json.RawMessage, idx int) (string, bool, error) {
	if idx >= len(args) || len(args[idx]) == 0 || strings.TrimSpace(string(args[idx])) == "null" {
		return "", false, nil
	}
	var s string
	if err := json.Unmarshal(args[idx], &s); err != nil {
		return "", false, err
	}
	return s, true, nil
}

func argInt(args []json.RawMessage, idx int) (int, bool, error) {
	if idx >= len(args) || len(args[idx]) == 0 || strings.TrimSpace(string(args[idx])) == "null" {
		return 0, false, nil
	}
	var n int
	if err := json.Unmarshal(args[idx], &n); err != nil {
		return 0, false, err
	}
	return n, true, nil
}

func argFloat(args []json.RawMessage, idx int) (float64, bool, error) {
	if idx >= len(args) || len(args[idx]) == 0 || strings.TrimSpace(string(args[idx])) == "null" {
		return 0, false, nil
	}
	var f float64
	if err := json.Unmarshal(args[idx], &f); err != nil {
		return 0, false, err
	}
	return f, true, nil
}

func argMap(args []json.RawMessage, idx int) (map[string]any, bool, error) {
	if idx >= len(args) || len(args[idx]) == 0 || strings.TrimSpace(string(args[idx])) == "null" {
		return nil, false, nil
	}
	var m map[string]any
	if err := json.Unmarshal(args[idx], &m); err != nil {
		return nil, false, err
	}
	return m, true, nil
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// TernBackendNotes computes omp's open result description and notes for a Tern tab.
// upstream: packages/coding-agent/src/tools/browser.ts:1006-1064
func ternOpenDescriptionAndNotes(pane int, block int, adopted *BlockEntry, urlStr, title string, shown *bool, requested *browser.Viewport, actual browser.Viewport) (string, []string) {
	var desc string
	if adopted == nil {
		desc = fmt.Sprintf("Tern browser picture-in-picture (pane %d)", pane)
	} else {
		kind := "picture-in-picture"
		if adopted.Owner == nil {
			kind = "docked"
		}
		desc = fmt.Sprintf("the user's Tern browser block %d (%s)", adopted.Block, kind)
	}

	var notes []string
	if adopted == nil && requested != nil && actual.Width > 0 && actual.Height > 0 {
		if absInt(actual.Width-requested.Width) > 1 || absInt(actual.Height-requested.Height) > 1 {
			notes = append(notes, fmt.Sprintf(
				`Tern sized the picture-in-picture %dx%d, not the requested %dx%d; pass app: "own" for a page at exactly that size.`,
				actual.Width, actual.Height, requested.Width, requested.Height,
			))
		}
	}
	if adopted != nil {
		notes = append(notes, fmt.Sprintf(
			"This is the user's own Tern browser block %d (%s at %s); closing the tab releases it to the user without closing it.",
			adopted.Block, jsonQuote(title), urlStr,
		))
	}
	if shown != nil && !*shown {
		if adopted != nil {
			notes = append(notes, fmt.Sprintf(
				"Not on screen: the user is viewing another Tern tab or session. Block %d shows once they switch to the tab holding it; tell them where it is.",
				adopted.Block,
			))
		} else {
			notes = append(notes, fmt.Sprintf(
				"Not on screen: the user hid pictures in picture (⌃⌥⌘P), or this Tern shows them only in the tab holding this omp pane (block %d); tell them where it is.",
				pane,
			))
		}
	}
	return desc, notes
}
