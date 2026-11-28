package ansi

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/surface"
)

// testThemePalette mimics PiG's dark theme palette wire object so tests exercise
// real sdk.UITheme tokens rather than the zero sdk.Context.
var testThemePalette = map[string]any{
	"name":       "dark",
	"appearance": "dark",
	"modifiers":  true,
	"mode":       "truecolor",
	"foregrounds": map[string]any{
		"text":               "\x1b[38;2;230;230;230m",
		"dim":                "\x1b[38;2;120;120;120m\x1b[2m",
		"muted":              "\x1b[38;2;150;150;150m",
		"accent":             "\x1b[38;2;100;180;255m",
		"success":            "\x1b[38;2;80;200;120m",
		"error":              "\x1b[38;2;240;90;90m",
		"warning":            "\x1b[38;2;240;190;70m",
		"border":             "\x1b[38;2;90;90;110m",
		"borderAccent":       "\x1b[38;2;120;160;240m",
		"borderMuted":        "\x1b[38;2;75;75;90m",
		"toolTitle":          "\x1b[38;2;170;200;255m",
		"toolOutput":         "\x1b[38;2;210;210;210m",
		"toolDiffAdded":      "\x1b[38;2;90;220;130m",
		"toolDiffRemoved":    "\x1b[38;2;240;100;100m",
		"toolDiffContext":    "\x1b[38;2;140;140;140m",
		"customMessageLabel": "\x1b[38;2;215;165;110m",
		"thinkingText":       "\x1b[38;2;155;155;175m",
		"thinkingHigh":       "\x1b[38;2;190;130;255m",
		"syntaxString":       "\x1b[38;2;150;220;130m",
		"syntaxNumber":       "\x1b[38;2;230;170;100m",
		"syntaxKeyword":      "\x1b[38;2;180;130;250m",
		"mdHeading":          "\x1b[38;2;130;190;255m",
		"mdQuoteBorder":      "\x1b[38;2;110;110;130m",
		"mdQuote":            "\x1b[38;2;165;165;175m",
		"mdCodeBlockBorder":  "\x1b[38;2;100;100;120m",
		"mdListBullet":       "\x1b[38;2;120;180;250m",
	},
	"backgrounds": map[string]any{
		"toolPendingBg": "\x1b[48;2;25;28;38m",
		"toolSuccessBg": "\x1b[48;2;22;34;26m",
		"toolErrorBg":   "\x1b[48;2;38;22;24m",
	},
}

// ansiTestHost runs an sdk.Extension over a net.Pipe fake PiG host so tests
// exercise the SDK's real view encoder, resolver, UITheme palette, and events.
type ansiTestHost struct {
	t             *testing.T
	nc            net.Conn
	cwd           string
	sessID        string
	writeMu       sync.Mutex
	mu            sync.Mutex
	pending       map[string]chan rawWireResp
	calls         []hostCallRec
	notifs        []hostCallRec
	handlers      map[string][]int
	nextID        int
	done          chan error
	thinkingLevel string
	contextTokens int
	contextWindow int
	contextPct    float64
	sessionName   string
	modelInfo     map[string]any
}

type rawWireResp struct {
	Result json.RawMessage
	Err    string
}

type hostCallRec struct {
	Method string
	Args   map[string]any
}

func startAnsiTestHost(t *testing.T, cwd string, extraReg func(*sdk.Extension)) *ansiTestHost {
	t.Helper()
	surface.ResetForTests()
	surface.SetNative(false)
	t.Cleanup(surface.ResetForTests)
	return startAnsiTestHostNoReset(t, cwd, extraReg)
}

// startAnsiTestHostNoReset starts a second concurrent session host without
// resetting the shared surface listeners.
func startAnsiTestHostNoReset(t *testing.T, cwd string, extraReg func(*sdk.Extension)) *ansiTestHost {
	t.Helper()
	return startAnsiTestHostWithID(t, cwd, "", extraReg)
}

func startAnsiTestHostWithID(t *testing.T, cwd, sessID string, extraReg func(*sdk.Extension)) *ansiTestHost {
	t.Helper()
	if cwd == "" {
		cwd = t.TempDir()
	}
	if sessID == "" {
		sessID = fmt.Sprintf("ansi-test-%d", time.Now().UnixNano())
	}
	t.Cleanup(func() { lifecycle.End(sessID) })
	ext := sdk.New("oh-my-pig-ansi-test")
	Register(ext)
	if extraReg != nil {
		extraReg(ext)
	}

	hostSide, extSide := net.Pipe()
	h := &ansiTestHost{
		t:             t,
		nc:            hostSide,
		cwd:           cwd,
		sessID:        sessID,
		pending:       map[string]chan rawWireResp{},
		handlers:      map[string][]int{},
		done:          make(chan error, 1),
		thinkingLevel: "medium",
		contextTokens: 512,
		contextWindow: 128000,
		contextPct:    0.4,
		sessionName:   "proof-session",
		modelInfo: map[string]any{
			"id":            "proof-1",
			"name":          "Proof 1",
			"reasoning":     true,
			"contextWindow": 128000,
			"cost": map[string]any{
				"input":  3.0,
				"output": 15.0,
			},
		},
	}
	go func() { h.done <- ext.RunWithConn(extSide) }()

	// Read register frame.
	var regFrame struct {
		Type     string `json:"type"`
		Register *struct {
			Handlers []struct {
				Event     string `json:"event"`
				HandlerID int    `json:"handler_id"`
			} `json:"handlers"`
		} `json:"register"`
	}
	for {
		raw := h.readFrame()
		_ = json.Unmarshal(raw, &regFrame)
		if regFrame.Type == "register" {
			break
		}
	}
	if regFrame.Register != nil {
		for _, hd := range regFrame.Register.Handlers {
			h.handlers[hd.Event] = append(h.handlers[hd.Event], hd.HandlerID)
		}
	}

	stateJSON, _ := json.Marshal(map[string]any{
		"hasUI": true,
		"theme": testThemePalette,
		"session": map[string]any{
			"sessionId":   sessID,
			"sessionName": h.sessionName,
		},
		"model": h.modelInfo,
	})
	h.writeFrame(map[string]any{
		"type": "ready",
		"ready": map[string]any{
			"cwd":   cwd,
			"mode":  "tui",
			"width": 120,
			"state": json.RawMessage(stateJSON),
		},
	})

	go h.loop()
	t.Cleanup(func() {
		h.writeFrame(map[string]any{"type": "shutdown", "shutdown": map[string]any{"reason": "test"}})
		select {
		case <-h.done:
		case <-time.After(3 * time.Second):
		}
		_ = h.nc.Close()
	})
	return h
}

// pushStateUpdate sends a host state_update notification (updating replicated
// model/session identity in the SDK) before an event is dispatched.
func (h *ansiTestHost) pushStateUpdate(model map[string]any, sessName string) {
	h.mu.Lock()
	if model != nil {
		h.modelInfo = model
	}
	if sessName != "" {
		h.sessionName = sessName
	}
	curModel := h.modelInfo
	curName := h.sessionName
	h.mu.Unlock()

	stateObj := map[string]any{
		"hasUI": true,
		"theme": testThemePalette,
		"session": map[string]any{
			"sessionId":   h.sessID,
			"sessionName": curName,
		},
		"model": curModel,
	}
	h.writeFrame(map[string]any{
		"type": "notify",
		"notify": map[string]any{
			"method": "state_update",
			"args":   map[string]any{"state": stateObj},
		},
	})
}

func (h *ansiTestHost) editorInstallCalls() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, notif := range h.notifs {
		if notif.Method == "ui.editor.install" {
			n++
		}
	}
	return n
}

func (h *ansiTestHost) workingVisibleCalls() []bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []bool
	for _, c := range h.calls {
		if c.Method == "ui.setWorkingVisible" {
			if v, ok := boolField(c.Args, "visible"); ok {
				out = append(out, v)
			}
		}
	}
	return out
}
func (h *ansiTestHost) bandWidgetCalls() []hostCallRec {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []hostCallRec
	for _, c := range h.calls {
		if c.Method == "ui.setWidget" && strField(c.Args, "key") == statusBandWidgetKey {
			out = append(out, c)
		}
	}
	return out
}

func (h *ansiTestHost) errorBannerWidgetOps() []hostCallRec {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []hostCallRec
	for _, c := range h.calls {
		if (c.Method == "ui.setWidget" || c.Method == "widget_push") && strField(c.Args, "key") == nativeErrorBannerWidgetKey {
			out = append(out, c)
		}
	}
	return out
}

func (h *ansiTestHost) waitForErrorBannerOps(minCount int, timeout time.Duration) []hostCallRec {
	h.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ops := h.errorBannerWidgetOps()
		if len(ops) >= minCount {
			return ops
		}
		time.Sleep(5 * time.Millisecond)
	}
	return h.errorBannerWidgetOps()
}

func (h *ansiTestHost) waitForBandWidgetCount(minCount int, timeout time.Duration) []hostCallRec {
	h.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		wc := h.bandWidgetCalls()
		if len(wc) >= minCount {
			return wc
		}
		time.Sleep(5 * time.Millisecond)
	}
	return h.bandWidgetCalls()
}

func (h *ansiTestHost) setEditorTextCalls() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, c := range h.calls {
		if c.Method == "ui.setEditorText" {
			n++
		}
	}
	return n
}

func (h *ansiTestHost) footerCalls() []hostCallRec {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []hostCallRec
	for _, c := range h.calls {
		if c.Method == "ui.setFooter" {
			out = append(out, c)
		}
	}
	return out
}

func (h *ansiTestHost) waitForFooterCount(minCount int, timeout time.Duration) []hostCallRec {
	h.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		fc := h.footerCalls()
		if len(fc) >= minCount {
			return fc
		}
		time.Sleep(5 * time.Millisecond)
	}
	return h.footerCalls()
}

func (h *ansiTestHost) readFrame() []byte {
	var hdr [4]byte
	if _, err := io.ReadFull(h.nc, hdr[:]); err != nil {
		return nil
	}
	buf := make([]byte, binary.BigEndian.Uint32(hdr[:]))
	if _, err := io.ReadFull(h.nc, buf); err != nil {
		return nil
	}
	return buf
}

func (h *ansiTestHost) writeFrame(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(data)))
	h.writeMu.Lock()
	_, _ = h.nc.Write(hdr[:])
	_, _ = h.nc.Write(data)
	h.writeMu.Unlock()
}

func (h *ansiTestHost) loop() {
	for {
		raw := h.readFrame()
		if raw == nil {
			return
		}
		var env struct {
			Type     string `json:"type"`
			ID       string `json:"id"`
			Response *struct {
				Result json.RawMessage `json:"result"`
				Error  *struct {
					Message string `json:"message"`
				} `json:"error"`
			} `json:"response"`
			Call *struct {
				Method string          `json:"method"`
				Args   json.RawMessage `json:"args"`
			} `json:"call"`
			Notify *struct {
				Method string          `json:"method"`
				Args   json.RawMessage `json:"args"`
			} `json:"notify"`
			WidgetPush json.RawMessage `json:"widget_push"`
		}
		if json.Unmarshal(raw, &env) != nil {
			continue
		}
		switch env.Type {
		case "response":
			h.mu.Lock()
			ch := h.pending[env.ID]
			delete(h.pending, env.ID)
			h.mu.Unlock()
			if ch != nil && env.Response != nil {
				errMsg := ""
				if env.Response.Error != nil {
					errMsg = env.Response.Error.Message
				}
				ch <- rawWireResp{Result: env.Response.Result, Err: errMsg}
			}
		case "call":
			var args map[string]any
			_ = json.Unmarshal(env.Call.Args, &args)
			h.mu.Lock()
			h.calls = append(h.calls, hostCallRec{Method: env.Call.Method, Args: args})
			thinking := h.thinkingLevel
			tok := h.contextTokens
			win := h.contextWindow
			pct := h.contextPct
			sName := h.sessionName
			mInfo := h.modelInfo
			h.mu.Unlock()
			var res any
			switch env.Call.Method {
			case "getModelInfo":
				res = mInfo
			case "getThinkingLevel":
				res = map[string]any{"level": thinking}
			case "getContextUsage":
				res = map[string]any{"tokens": tok, "contextWindow": win, "percent": pct}
			case "getBranch":
				res = []any{}
			case "getSessionName":
				res = map[string]any{"name": sName}
			case "fileMutation.enter":
				res = map[string]any{"ticket": "t-1"}
			case "fileMutation.leave":
				res = map[string]any{"ok": true}
			}
			h.writeFrame(map[string]any{
				"type":        "call_result",
				"id":          env.ID,
				"call_result": map[string]any{"result": res},
			})
		case "notify":
			var args map[string]any
			_ = json.Unmarshal(env.Notify.Args, &args)
			h.mu.Lock()
			h.notifs = append(h.notifs, hostCallRec{Method: env.Notify.Method, Args: args})
			h.mu.Unlock()
		case "widget_push":
			var args map[string]any
			_ = json.Unmarshal(env.WidgetPush, &args)
			h.mu.Lock()
			h.calls = append(h.calls, hostCallRec{Method: "widget_push", Args: args})
			h.mu.Unlock()
		}
	}
}
func (h *ansiTestHost) request(req map[string]any) rawWireResp {
	h.t.Helper()
	h.mu.Lock()
	h.nextID++
	id := fmt.Sprintf("req-%d", h.nextID)
	ch := make(chan rawWireResp, 1)
	h.pending[id] = ch
	h.mu.Unlock()

	h.writeFrame(map[string]any{
		"type":    "request",
		"id":      id,
		"request": req,
	})
	select {
	case resp := <-ch:
		return resp
	case <-time.After(5 * time.Second):
		h.t.Fatalf("timeout waiting for %v response", req["method"])
		return rawWireResp{}
	}
}

func (h *ansiTestHost) resolveRenderer(tool string) (rendererID string, use string) {
	h.t.Helper()
	rawArgs, _ := json.Marshal(map[string]any{"tool": tool})
	resp := h.request(map[string]any{
		"method": "resolve_tool_renderers",
		"args":   json.RawMessage(rawArgs),
	})
	if resp.Err != "" {
		h.t.Fatalf("resolve_tool_renderers(%s) error: %s", tool, resp.Err)
	}
	var out struct {
		Use       string `json:"use"`
		Renderers string `json:"renderers"`
	}
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		h.t.Fatalf("unmarshal resolve result: %v", err)
	}
	return out.Renderers, out.Use
}

// encodedViewNode mirrors kitwire's encoded ViewNode JSON.
type encodedViewNode struct {
	Kind     string            `json:"kind"`
	ID       string            `json:"id"`
	Text     string            `json:"text"`
	Content  []string          `json:"content"`
	Children []encodedViewNode `json:"children"`
}
type encodedView struct {
	Root encodedViewNode `json:"root"`
}

func (v encodedView) nodeCount() int {
	var walk func(n encodedViewNode) int
	walk = func(n encodedViewNode) int {
		c := 1
		for _, ch := range n.Children {
			c += walk(ch)
		}
		return c
	}
	return walk(v.Root)
}

func (v encodedView) allIDs() []string {
	var ids []string
	var walk func(n encodedViewNode)
	walk = func(n encodedViewNode) {
		if n.ID != "" {
			ids = append(ids, n.ID)
		}
		for _, ch := range n.Children {
			walk(ch)
		}
	}
	walk(v.Root)
	return ids
}

// renderedLines extracts every row produced by the encoded view, checking
// kit.Lines verbatim (as PiG's host does) and wrapping kit.Text to width.
func (v encodedView) renderedLines(width int) []string {
	var out []string
	var walk func(n encodedViewNode)
	walk = func(n encodedViewNode) {
		switch n.Kind {
		case "lines":
			out = append(out, n.Content...)
		case "text":
			if n.Text != "" {
				for _, l := range strings.Split(n.Text, "\n") {
					out = append(out, tui.WrapTextWithAnsi(l, max(1, width))...)
				}
			}
		default:
			for _, ch := range n.Children {
				walk(ch)
			}
		}
	}
	walk(v.Root)
	return out
}

func (h *ansiTestHost) renderToolView(rendererID, card, tool, phase string, args map[string]any, res *sdk.ToolRenderResult, isError, expanded, isPartial bool, width int) encodedView {
	h.t.Helper()
	payload := map[string]any{
		"card":      card,
		"renderers": rendererID,
		"phase":     phase,
		"args":      args,
		"options": map[string]any{
			"expanded":  expanded,
			"isPartial": isPartial,
		},
		"context": map[string]any{
			"cwd":       h.cwd,
			"isPartial": isPartial,
			"expanded":  expanded,
			"isError":   isError,
		},
		"width": width,
	}
	if res != nil {
		payload["result"] = res
	}
	rawArgs, _ := json.Marshal(payload)
	resp := h.request(map[string]any{
		"method": "render_tool",
		"tool":   tool,
		"args":   json.RawMessage(rawArgs),
	})
	if resp.Err != "" {
		h.t.Fatalf("render_tool(%s/%s) error: %s", tool, phase, resp.Err)
	}
	var env struct {
		View encodedView `json:"view"`
	}
	if err := json.Unmarshal(resp.Result, &env); err != nil {
		h.t.Fatalf("unmarshal render_tool view: %v (%s)", err, string(resp.Result))
	}
	return env.View
}

func (h *ansiTestHost) emitEvent(event string, data map[string]any) {
	h.t.Helper()
	ids := h.handlers[event]
	rawArgs, _ := json.Marshal(data)
	for _, hid := range ids {
		resp := h.request(map[string]any{
			"method":     "event",
			"event":      event,
			"handler_id": hid,
			"args":       json.RawMessage(rawArgs),
		})
		if resp.Err != "" {
			h.t.Fatalf("event %s error: %s", event, resp.Err)
		}
	}
}

func (h *ansiTestHost) executeRegisteredTool(tool string, params map[string]any) (sdk.ToolRenderResult, bool) {
	h.t.Helper()
	rawArgs, _ := json.Marshal(params)
	resp := h.request(map[string]any{
		"method":       "tool_call",
		"tool":         tool,
		"tool_call_id": fmt.Sprintf("tc-%d", time.Now().UnixNano()),
		"args":         json.RawMessage(rawArgs),
	})
	if resp.Err != "" {
		return sdk.ToolRenderResult{
			Content: []map[string]any{{"type": "text", "text": resp.Err}},
		}, true
	}
	var out struct {
		Content any  `json:"content"`
		Details any  `json:"details"`
		IsError bool `json:"is_error"`
	}
	_ = json.Unmarshal(resp.Result, &out)
	var content []map[string]any
	switch c := out.Content.(type) {
	case string:
		content = []map[string]any{{"type": "text", "text": c}}
	case []any:
		for _, item := range c {
			if m, ok := item.(map[string]any); ok {
				content = append(content, m)
			}
		}
	}
	return sdk.ToolRenderResult{
		Content: content,
		Details: out.Details,
	}, out.IsError
}

// assertTerminalViewInvariants checks:
// 1. Node count < 4096 (PiG host limit).
// 2. Zero omp.* role IDs in terminal views.
// 3. Every emitted row has tui.VisibleWidth(row) <= width.
// 4. Zero C1 control runes (U+0080-U+009F) or raw unsafe C0 controls in rendered output.
func assertTerminalViewInvariants(t *testing.T, label string, v encodedView, width int) []string {
	t.Helper()
	if n := v.nodeCount(); n >= hostMaxViewNodes {
		t.Errorf("%s: node count %d >= host limit %d", label, n, hostMaxViewNodes)
	}
	for _, id := range v.allIDs() {
		if strings.HasPrefix(id, "omp.") {
			t.Errorf("%s: terminal view must not emit omp.* role ID %q", label, id)
		}
	}
	lines := v.renderedLines(width)
	for i, row := range lines {
		if strings.ContainsAny(row, "\r\n") {
			t.Errorf("%s row %d contains embedded CR/LF: %q", label, i, row)
		}
		// Check raw row before StripANSI so unsanitized OSC/APC/C1/DEL/NUL
		// sequences cannot slip past StripANSI. Only SGR CSI (\x1b[...m) is
		// permitted from palette styling.
		if strings.Contains(row, "\x1b]") || strings.Contains(row, "\x1b_") || strings.Contains(row, "\x1bP") {
			t.Errorf("%s row %d contains raw OSC/APC/DCS escape sequence: %q", label, i, row)
		}
		for _, r := range row {
			if (r < 0x20 && r != '\x1b' && r != '\t') || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
				t.Errorf("%s row %d contains raw unsafe control rune U+%04X: %q", label, i, r, row)
			}
		}
		if w := tui.VisibleWidth(row); w > width {
			t.Errorf("%s row %d width %d > %d: %q", label, i, w, width, StripANSI(row))
		}
		plain := StripANSI(row)
		for _, r := range plain {
			if (r < 0x20 && r != '\t') || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
				t.Errorf("%s row %d contains unsafe control rune U+%04X: %q", label, i, r, plain)
			}
		}
	}
	return lines
}

// ═══════════════════════════════════════════════════════════════════════════
// Core tests: symbols, sanitization, frame widths 1..200, host chrome
// ═══════════════════════════════════════════════════════════════════════════

func TestSymbolsAndLanguageIconsMatchUpstream(t *testing.T) {
	// upstream: packages/tui/src/theme/symbols.ts (UNICODE_SYMBOLS)
	if unicodeSymbols.formatBracketLeft != "⟦" || unicodeSymbols.formatBracketRight != "⟧" {
		t.Fatalf("brackets = %q/%q, want ⟦/⟧", unicodeSymbols.formatBracketLeft, unicodeSymbols.formatBracketRight)
	}
	if unicodeSymbols.mdQuoteBorder != "▏" {
		t.Fatalf("mdQuoteBorder = %q, want ▏", unicodeSymbols.mdQuoteBorder)
	}
	if got := formatThinkingDisplay("medium"); got != "◑ med" {
		t.Fatalf("thinking medium = %q, want %q", got, "◑ med")
	}
	if got := formatThinkingDisplay("minimal"); got != "○ min" {
		t.Fatalf("thinking minimal = %q, want %q", got, "○ min")
	}
	if got := formatThinkingDisplay("xhigh"); got != "◕ xhigh" {
		t.Fatalf("thinking xhigh = %q, want %q", got, "◕ xhigh")
	}
	cases := map[string]string{
		"app.ts":      "🟦",
		"run.sh":      "💻",
		"data.json":   "🧾",
		"main.c":      "Ⓒ",
		"main.cpp":    "➕",
		"notes.txt":   "🗒",
		"unknown.xyz": "⌘",
		"Main.java":   "☕",
		"Dockerfile":  "🐳",
		"archive.zip": "🗜",
	}
	for fp, want := range cases {
		if got := langIcon(fp); got != want {
			t.Errorf("langIcon(%q) = %q, want %q", fp, got, want)
		}
	}
}

func TestSanitizeStripsC0C1AndANSI(t *testing.T) {
	// upstream: packages/utils/src/sanitize-text.ts
	in := "ok\x1b[31mred\x1b[0m\x1b]0;evil\x07\x00\x07\x7f\u009b2J\u009d0;title\x07end"
	got := sanitizeDisplayText(in)
	if got != "okred2J0;titleend" {
		t.Fatalf("sanitizeDisplayText = %q, want %q", got, "okred2J0;titleend")
	}
	// Bare CR progress overwrite collapses to final segment.
	lines := sanitizeDisplayLines("progress 10%\rprogress 50%\rprogress 100%\r\ndone\tnow")
	if len(lines) != 2 || lines[0] != "progress 100%" || lines[1] != "done   now" {
		t.Fatalf("sanitizeDisplayLines = %#v", lines)
	}
}

func TestFrameFuzzWidths1To200HostileContent(t *testing.T) {
	// upstream: packages/tui/test/output-block.test.ts: "keeps tool and overlay frame rows inside a one-column viewport"
	p := palette{}
	hostileLines := []string{
		"✅ all tests passed ❌ ⚡ ⭐ ⌛ ⏰ ☕ ⛔ ❓ ➕ ⏳",
		"⚠️ ✔️ 1️⃣ 🇺🇸 👨‍👩‍👧‍👦 👍🏽 🗑 🗄 🛠 🖼",
		"tab\tseparated\tcolumns\tand\tmore\ttabs\t\t\t!",
		"controls:\x00\x01\x07\x1b[31mred\x1b[0m\x1b]0;osc\x07\u009b31mC1\x7f",
		strings.Repeat("日本語テスト🚀", 40),
		strings.Repeat("A", 500),
	}
	cleanLines := make([]string, len(hostileLines))
	for i, l := range hostileLines {
		cleanLines[i] = strings.Join(sanitizeDisplayLines(l), " ")
	}

	for width := 1; width <= 200; width++ {
		rows := p.renderOutputBlock(blockOpts{
			header:     "✅ Header with emoji 👨‍👩‍👧‍👦 and ⚠️ wide glyphs",
			headerMeta: "⏱ 195ms/5m",
			footerMeta: "⏱ 1.2s/5m · ⤴ local://7.log · ✘ 1 · extra-long-footer-fact",
			sections: []blockSection{
				{lines: cleanLines[:3]},
				{label: "Output", lines: cleanLines[3:]},
			},
			width: width,
		})
		if len(rows) == 0 {
			t.Fatalf("width %d: empty frame", width)
		}
		for ri, row := range rows {
			if w := tui.VisibleWidth(row); w > width {
				t.Fatalf("width %d row %d overflow: tui.VisibleWidth=%d > %d: %q", width, ri, w, width, row)
			}
		}
	}
}

func TestFrameMassive100kLinePayloadBounded(t *testing.T) {
	p := palette{}
	huge := make([]string, 100_000)
	for i := range huge {
		huge[i] = fmt.Sprintf("line %d: ✅ payload", i)
	}
	shown, hidden := p.formatOutputPaneLines(outputPaneOpts{
		lines:             huge,
		expanded:          true,
		collapsedMaxLines: 10,
		edge:              "tail",
		width:             80,
	})
	if len(shown) > maxExpandedOutputLines+1 {
		t.Fatalf("expanded lines = %d, expected <= %d", len(shown), maxExpandedOutputLines+1)
	}
	if hidden == 0 {
		t.Fatalf("expected hidden count > 0 for 100k lines")
	}
	for i, l := range clampRows(shown, 80) {
		if w := tui.VisibleWidth(l); w > 80 {
			t.Fatalf("row %d width %d > 80", i, w)
		}
	}
}

func TestFrameFooterOverflowWrapsIntoBody(t *testing.T) {
	// upstream: packages/tui/test/output-block.test.ts: "wraps footer meta that overflows the border into body rows instead of clipping it"
	p := palette{}
	rows := p.renderOutputBlock(blockOpts{
		header:     "Header",
		footerMeta: "very-long-footer-metadata-that-cannot-fit-in-24-cols",
		sections:   []blockSection{{lines: []string{"body"}}},
		width:      24,
	})
	joined := strings.Join(rows, "\n")
	if !strings.Contains(joined, "very-long-footer") || !strings.Contains(joined, "24-cols") {
		t.Fatalf("overflowing footer was clipped instead of wrapped:\n%s", joined)
	}
	last := rows[len(rows)-1]
	if strings.Contains(last, "very-long") {
		t.Fatalf("bottom border should be plain when footer wraps into body, got %q", last)
	}
	for i, r := range rows {
		if w := tui.VisibleWidth(r); w > 24 {
			t.Fatalf("row %d width %d > 24: %q", i, w, r)
		}
	}
}

func TestChromeWelcomeStatusLineAndWorkingIntentOnly(t *testing.T) {
	h := startAnsiTestHost(t, "", nil)
	h.emitEvent(sdk.EventSessionStart, map[string]any{})

	// Verify working indicator and header/footer installed on session_start.
	h.mu.Lock()
	calls := append([]hostCallRec(nil), h.calls...)
	h.mu.Unlock()
	sawIndicator := false
	for _, c := range calls {
		if c.Method == "ui.setWorkingIndicator" {
			sawIndicator = true
		}
	}
	if !sawIndicator {
		t.Fatalf("expected ui.setWorkingIndicator call on session_start, calls=%v", calls)
	}

	// Tool start WITHOUT `i` (on a tool with no deriver) must NOT invent a working message.
	h.emitEvent(sdk.EventToolExecutionStart, map[string]any{
		"toolName": "bash",
		"args":     map[string]any{"command": "echo secret"},
	})
	// Tool start WITH `i` (including trailing dots before colon-space) sets label via
	// intent.WorkingLabelFromEvent (label cut first, then trailing periods normalized).
	h.emitEvent(sdk.EventToolExecutionStart, map[string]any{
		"toolName": "bash",
		"args":     map[string]any{"command": "echo hi", "i": "Running unit tests...: internal/ansi"},
	})
	h.mu.Lock()
	var workMsgs []string
	for _, c := range h.calls {
		if c.Method == "ui.setWorkingMessage" {
			workMsgs = append(workMsgs, strField(c.Args, "message"))
		}
	}
	h.mu.Unlock()
	if len(workMsgs) != 1 || workMsgs[0] != "Running unit tests" {
		t.Fatalf("working messages = %#v, want [\"Running unit tests\"]", workMsgs)
	}

	// Status line formatting checks: one-decimal context usage, no extra tokens segment,
	// scratch path icon & prefix stripping, thinking glyph.
	pct := 0.4
	tok := 512
	statusLines := RenderStatusLine(sdk.UITheme{}, StatusLineData{
		ModelName:      "Proof 1",
		ModelReasoning: true,
		ThinkingLevel:  "medium",
		Cwd:            "/tmp/omp-proof2/reference/ansi-omp/omp/work",
		ContextTokens:  &tok,
		ContextWindow:  128000,
		ContextPercent: &pct,
		SessionName:    "my-session",
	}, 120)
	if len(statusLines) != 1 {
		t.Fatalf("status line rows = %d, want 1", len(statusLines))
	}
	plain := StripANSI(statusLines[0])
	if tui.VisibleWidth(statusLines[0]) > 120 {
		t.Fatalf("status line width %d > 120", tui.VisibleWidth(statusLines[0]))
	}
	for _, want := range []string{"π", "◑ Proof 1", "🗑", "0.4%/128K", "my-session"} {
		if !strings.Contains(plain, want) {
			t.Errorf("status line missing %q in %q", want, plain)
		}
	}
	if strings.Contains(plain, "🪙") {
		t.Errorf("default preset status line must not include non-preset token_total 🪙: %q", plain)
	}

	// Welcome banner width invariant across narrow and wide viewports.
	for _, w := range []int{10, 40, 80, 120} {
		wl := RenderWelcomeLines(sdk.UITheme{}, "v19.1.1", w, 0)
		for i, r := range wl {
			if vw := tui.VisibleWidth(r); vw > w {
				t.Errorf("welcome width=%d row=%d overflow %d > %d: %q", w, i, vw, w, StripANSI(r))
			}
		}
	}
}

// TestStatusLineRepaintsOnDataChanges verifies N1 and widget-only band invariants:
// changing model, thinking level, context/cost (turn_end), provider error
// banner, and agent start/end updates the `omp-status-band` widget while:
//   - `ui.setWorkingVisible(false)` suppresses PiG's own working border/loader,
//   - `SetEditorComponent` (`ui.editor.install`) is NEVER called (PiG's stock
//     editor stays active so `/` and `@` completion, history, paste, and exit
//     remain native),
//   - `ui.setEditorText` is NEVER called (zero risk of erasing user keystrokes),
//   - 1,000 forced repaints complete cleanly without touching editor text.
func TestStatusLineRepaintsOnDataChanges(t *testing.T) {
	h := startAnsiTestHost(t, "", nil)
	h.emitEvent(sdk.EventSessionStart, map[string]any{})

	wc := h.waitForBandWidgetCount(1, time.Second)
	if len(wc) < 1 {
		t.Fatalf("expected initial omp-status-band widget on session_start")
	}
	lastBandPlain := func(recs []hostCallRec) string {
		lines := stringSliceField(recs[len(recs)-1].Args, "lines")
		return StripANSI(strings.Join(lines, "\n"))
	}
	if got := lastBandPlain(wc); !strings.Contains(got, "◑ Proof 1") {
		t.Fatalf("initial band widget missing compact model: %q", got)
	}
	if vis := h.workingVisibleCalls(); len(vis) == 0 || vis[len(vis)-1] != false {
		t.Fatalf("expected SetWorkingVisible(false) on terminal session_start, got %v", vis)
	}

	// 1. Model change after session_start must repaint the band widget.
	prevN := len(wc)
	h.pushStateUpdate(map[string]any{
		"id":            "opus-4",
		"name":          "Claude Opus 4",
		"reasoning":     true,
		"contextWindow": 200000,
		"cost": map[string]any{
			"input":  15.0,
			"output": 75.0,
		},
	}, "")
	time.Sleep(15 * time.Millisecond)
	h.emitEvent(sdk.EventModelSelect, map[string]any{})
	wc = h.waitForBandWidgetCount(prevN+1, time.Second)
	if len(wc) <= prevN {
		t.Fatalf("model_select did not trigger band widget repaint (count stayed %d)", len(wc))
	}
	if got := lastBandPlain(wc); !strings.Contains(got, "◑ Opus 4") {
		t.Fatalf("band after model_select = %q, want \"◑ Opus 4\"", got)
	}

	// 2. Thinking level change must repaint with compact thinking glyph (◒).
	prevN = len(wc)
	h.mu.Lock()
	h.thinkingLevel = "high"
	h.mu.Unlock()
	h.emitEvent(sdk.EventThinkingLevelSelect, map[string]any{})
	wc = h.waitForBandWidgetCount(prevN+1, time.Second)
	if len(wc) <= prevN {
		t.Fatalf("thinking_level_select did not trigger band widget repaint")
	}
	if got := lastBandPlain(wc); !strings.Contains(got, "◒ Opus 4") {
		t.Fatalf("band after thinking_level_select = %q, want \"◒ Opus 4\"", got)
	}

	// 3. turn_end updates context usage and accumulates cost and repaints.
	prevN = len(wc)
	h.mu.Lock()
	h.contextTokens = 24000
	h.contextWindow = 200000
	h.contextPct = 12.0
	h.mu.Unlock()
	h.emitEvent(sdk.EventTurnEnd, map[string]any{
		"message": map[string]any{
			"usage": map[string]any{
				"input":  10000.0,
				"output": 2000.0,
			},
		},
	})
	wc = h.waitForBandWidgetCount(prevN+1, time.Second)
	if len(wc) <= prevN {
		t.Fatalf("turn_end did not trigger band widget repaint")
	}
	if got := lastBandPlain(wc); !strings.Contains(got, "12%") || !strings.Contains(got, "200K") || !strings.Contains(got, "$0.30") {
		t.Fatalf("band after turn_end = %q, want embedded 12%%, 200K, and cost $0.30", got)
	}

	// 4. Provider error turn pins omp's error banner above the band; next agent_start clears it.
	prevN = len(wc)
	h.emitEvent(sdk.EventTurnEnd, map[string]any{
		"message": map[string]any{
			"stopReason":   "error",
			"errorMessage": "400 Proof simulated provider error",
		},
	})
	wc = h.waitForBandWidgetCount(prevN+1, time.Second)
	if len(wc) <= prevN {
		t.Fatalf("error turn_end did not trigger band widget repaint")
	}
	if got := lastBandPlain(wc); !strings.Contains(got, "✘ 400 Proof simulated provider error") || !strings.Contains(got, "Dismissed when you send your next message.") {
		t.Fatalf("band after error turn_end missing pinned error banner: %q", got)
	}

	// 5. agent_start clears pinned error and shows working row + active brand; agent_end restores idle.
	prevN = len(wc)
	h.emitEvent(sdk.EventAgentStart, map[string]any{})
	wc = h.waitForBandWidgetCount(prevN+1, time.Second)
	if len(wc) <= prevN {
		t.Fatalf("agent_start did not trigger band widget repaint")
	}
	if got := lastBandPlain(wc); strings.Contains(got, "Dismissed when you send") || !strings.Contains(got, "⎋ Working…") || !strings.Contains(got, "⠋ 0s") {
		t.Fatalf("band after agent_start = %q, want cleared error + working row + active brand", got)
	}
	h.emitEvent(sdk.EventAgentEnd, map[string]any{})
	deadline := time.Now().Add(time.Second)
	var lastIdle string
	for time.Now().Before(deadline) {
		wc = h.bandWidgetCalls()
		if len(wc) > 0 {
			lastIdle = lastBandPlain(wc)
			if !strings.Contains(lastIdle, "Working…") && strings.Contains(lastIdle, "π") {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if strings.Contains(lastIdle, "Working…") || !strings.Contains(lastIdle, "π") {
		t.Fatalf("band after agent_end = %q, want idle π without Working…", lastIdle)
	}

	// 6. Stress test: 1,000 forced repaints must NEVER touch editor text or install a delegated editor.
	chromeMu.Lock()
	st := chromeSessions[h.sessID]
	chromeMu.Unlock()
	if st == nil {
		t.Fatalf("missing chromeState for session %q", h.sessID)
	}
	for range 1000 {
		st.requestFooterRepaint()
	}
	time.Sleep(40 * time.Millisecond)

	if n := h.setEditorTextCalls(); n != 0 {
		t.Fatalf("ui.setEditorText called %d times, want 0 (repaints must never touch editor text)", n)
	}
	if installs := h.editorInstallCalls(); installs != 0 {
		t.Fatalf("ui.editor.install count = %d, want 0 (no delegated editor component)", installs)
	}
}

func TestSurfaceSwitchInvalidatesAndDispatches(t *testing.T) {
	// Verify that Register + surface.SetNative cleanly flips between terminal and native builders
	// AND clears terminal header/footer when entering native mode (N2c).
	surface.ResetForTests()
	t.Cleanup(surface.ResetForTests)

	surface.SetNative(false)
	h := startAnsiTestHostNoReset(t, "", nil)
	h.emitEvent(sdk.EventSessionStart, map[string]any{})
	rid, use := h.resolveRenderer("bash")
	if use != "own" || rid == "" {
		t.Fatalf("resolveRenderer(bash) = (%q, %q), want own", rid, use)
	}
	vTerm := h.renderToolView(rid, "c-switch", "bash", "result",
		map[string]any{"command": "echo hi"},
		&sdk.ToolRenderResult{
			Content: []map[string]any{{"type": "text", "text": "hi"}},
			Details: map[string]any{"wallTimeMs": 120, "timeoutSeconds": 300},
		}, false, false, false, 80)
	assertTerminalViewInvariants(t, "bash-term", vTerm, 80)

	// Switching to native triggers tool_render_invalidate AND clears terminal header/footer.
	surface.SetNative(true)
	time.Sleep(30 * time.Millisecond)
	h.mu.Lock()
	sawInv := false
	for _, n := range h.notifs {
		if n.Method == "tool_render_invalidate" && strField(n.Args, "card") == "c-switch" {
			sawInv = true
		}
	}
	clearedHeader, clearedFooter, clearedWidget, restoredWorking := false, false, false, false
	for _, c := range h.calls {
		if c.Method == "ui.setHeader" && c.Args["clear"] == true {
			clearedHeader = true
		}
		if c.Method == "ui.setFooter" && c.Args["clear"] == true {
			clearedFooter = true
		}
		if c.Method == "ui.setWidget" && strField(c.Args, "key") == statusBandWidgetKey {
			if _, hasLines := c.Args["lines"]; !hasLines && c.Args["content"] == nil {
				clearedWidget = true
			}
		}
		if c.Method == "ui.setWorkingVisible" {
			if v, ok := boolField(c.Args, "visible"); ok && v {
				restoredWorking = true
			}
		}
	}
	h.mu.Unlock()
	if !sawInv {
		t.Errorf("expected tool_render_invalidate on surface.SetNative(true)")
	}
	if !clearedHeader || !clearedFooter || !clearedWidget || !restoredWorking {
		t.Errorf("switching terminal->native must clear header/footer/band-widget and restore workingVisible (header=%v, footer=%v, widget=%v, workingVisible=%v)",
			clearedHeader, clearedFooter, clearedWidget, restoredWorking)
	}
}

// TestWorkingTickerAcrossMidTurnSurfaceSwitches verifies Finding 10:
//   - Starting a turn while native (Tern) starts no terminal working ticker.
//   - Falling back native -> terminal mid-turn installs terminal chrome AND
//     starts the 250 ms working ticker so the spinner advances past frame 0.
//   - Switching terminal -> native mid-turn stops the ticker (no more widget
//     pushes) and restores host working visibility.
//   - Falling back native -> terminal a second time mid-turn restarts the
//     ticker, and `agent_end` stops it and returns the band to idle `π`.
func TestWorkingTickerAcrossMidTurnSurfaceSwitches(t *testing.T) {
	surface.ResetForTests()
	t.Cleanup(surface.ResetForTests)

	// 1. Start session in native mode, begin a turn while native.
	surface.SetNative(true)
	h := startAnsiTestHostNoReset(t, "", nil)
	h.emitEvent(sdk.EventSessionStart, map[string]any{})
	h.emitEvent(sdk.EventAgentStart, map[string]any{})

	chromeMu.Lock()
	st := chromeSessions[h.sessID]
	chromeMu.Unlock()
	if st == nil {
		t.Fatalf("missing chromeState for session %q", h.sessID)
	}
	st.mu.Lock()
	tickWhileNative := st.workingTickStop != nil
	st.mu.Unlock()
	if tickWhileNative {
		t.Fatalf("working ticker must not run while surface is native")
	}
	if wc := h.bandWidgetCalls(); len(wc) != 0 {
		t.Fatalf("expected 0 band widget calls while native, got %d", len(wc))
	}

	bandFramesWithGlyph := func(recs []hostCallRec) []string {
		var out []string
		for _, c := range recs {
			lines := stringSliceField(c.Args, "lines")
			if len(lines) == 0 {
				continue
			}
			out = append(out, StripANSI(strings.Join(lines, "\n")))
		}
		return out
	}

	// 2. Native -> terminal mid-turn: chrome installs and working ticker advances past frame 0.
	surface.SetNative(false)
	wc := h.waitForBandWidgetCount(1, time.Second)
	if len(wc) < 1 {
		t.Fatalf("expected omp-status-band widget on native->terminal mid-turn fallback")
	}
	advanced := false
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		for _, plain := range bandFramesWithGlyph(h.bandWidgetCalls()) {
			if strings.Contains(plain, "⎋ Working…") &&
				!strings.Contains(plain, ompActivitySpinnerFrames[0]+" ") &&
				strings.Contains(plain, ompActivitySpinnerFrames[1]+" ") {
				advanced = true
				break
			}
		}
		if advanced {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !advanced {
		t.Fatalf("working ticker did not advance spinner past frame 0 (%q) after native->terminal mid-turn switch; frames=%q",
			ompActivitySpinnerFrames[0], bandFramesWithGlyph(h.bandWidgetCalls()))
	}

	// 3. Terminal -> native mid-turn: ticker stops and no further widget pushes happen while native.
	surface.SetNative(true)
	time.Sleep(40 * time.Millisecond)
	st.mu.Lock()
	tickAfterNative := st.workingTickStop != nil
	st.mu.Unlock()
	if tickAfterNative {
		t.Fatalf("working ticker must stop on terminal->native mid-turn switch")
	}
	countAfterNative := len(h.bandWidgetCalls())
	time.Sleep(350 * time.Millisecond)
	if got := len(h.bandWidgetCalls()); got != countAfterNative {
		t.Fatalf("band widget kept repainting while native: count grew from %d to %d", countAfterNative, got)
	}

	// 4. Native -> terminal again mid-turn: ticker restarts and advances spinner again.
	beforeSecondFallback := len(h.bandWidgetCalls())
	surface.SetNative(false)
	wc = h.waitForBandWidgetCount(beforeSecondFallback+1, time.Second)
	if len(wc) <= beforeSecondFallback {
		t.Fatalf("expected omp-status-band widget on second native->terminal mid-turn switch")
	}
	advancedAgain := false
	deadline = time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		all := h.bandWidgetCalls()
		if len(all) > beforeSecondFallback {
			for _, plain := range bandFramesWithGlyph(all[beforeSecondFallback:]) {
				if strings.Contains(plain, "⎋ Working…") &&
					strings.Contains(plain, ompActivitySpinnerFrames[1]+" ") {
					advancedAgain = true
					break
				}
			}
		}
		if advancedAgain {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !advancedAgain {
		t.Fatalf("working ticker did not restart on second native->terminal mid-turn switch")
	}

	// 5. agent_end stops the ticker and restores idle π.
	h.emitEvent(sdk.EventAgentEnd, map[string]any{})
	st.mu.Lock()
	tickAfterEnd := st.workingTickStop != nil
	st.mu.Unlock()
	if tickAfterEnd {
		t.Fatalf("working ticker must stop on agent_end")
	}
	settledIdle := false
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		frames := bandFramesWithGlyph(h.bandWidgetCalls())
		if len(frames) > 0 {
			last := frames[len(frames)-1]
			if !strings.Contains(last, "Working…") && strings.Contains(last, "π") {
				settledIdle = true
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !settledIdle {
		t.Fatalf("band did not settle to idle π after agent_end")
	}
}

// TestNativeErrorBannerWidgetLifecycle verifies Audit Finding 9 (Tern dock error banner):
//   - On the native surface, a provider-error turn publishes `omp-error-banner`
//     as a structured `kit.View` (`omp.errorBanner` -> `omp.errorBanner.message`)
//     carrying omp's formatted error text and NO terminal-only "Dismissed when..."
//     hint, while `omp-status-band` is NOT published.
//   - `agent_start` clears `omp-error-banner` (`content: nil`, no view).
//   - Switching terminal -> native with a pinned error transfers it to the native
//     `omp-error-banner` widget; switching native -> terminal clears `omp-error-banner`
//     and shows the banner inside terminal `omp-status-band`.
func TestNativeErrorBannerWidgetLifecycle(t *testing.T) {
	surface.ResetForTests()
	t.Cleanup(surface.ResetForTests)

	surface.SetNative(true)
	h := startAnsiTestHostNoReset(t, "", nil)
	h.emitEvent(sdk.EventSessionStart, map[string]any{})

	rawErr := `400: {"message":"Proof simulated provider error","type":"invalid_request_error"}`
	wantFormatted := "400 Proof simulated provider error (type=invalid_request_error)"

	// 1. Provider error turn on native surface pushes structured `omp.errorBanner` kit.View.
	h.emitEvent(sdk.EventTurnEnd, map[string]any{
		"message": map[string]any{
			"stopReason":   "error",
			"errorMessage": rawErr,
		},
	})
	ops := h.waitForErrorBannerOps(1, time.Second)
	if len(ops) != 1 {
		t.Fatalf("expected 1 omp-error-banner widget op on native error turn, got %d (%+v)", len(ops), ops)
	}
	if ops[0].Method != "widget_push" {
		t.Fatalf("expected widget_push for kit.View widget, got %q", ops[0].Method)
	}
	view := asMap(ops[0].Args["view"])
	root := asMap(view["root"])
	if strField(root, "kind") != "container" || strField(root, "id") != "omp.errorBanner" {
		t.Fatalf("native error banner root = %#v, want container id=omp.errorBanner", root)
	}
	kids := asSlice(root["children"])
	if len(kids) != 1 {
		t.Fatalf("native error banner children = %d, want 1 (message only, no terminal hint): %#v", len(kids), kids)
	}
	msgNode := asMap(kids[0])
	if strField(msgNode, "kind") != "text" || strField(msgNode, "id") != "omp.errorBanner.message" || strField(msgNode, "text") != wantFormatted {
		t.Fatalf("native error banner message node = %#v, want text id=omp.errorBanner.message text=%q", msgNode, wantFormatted)
	}
	if wc := h.bandWidgetCalls(); len(wc) != 0 {
		t.Fatalf("terminal omp-status-band must not be published while native, got %d calls", len(wc))
	}

	// 2. Next agent_start clears the native `omp-error-banner` widget.
	h.emitEvent(sdk.EventAgentStart, map[string]any{})
	ops = h.waitForErrorBannerOps(2, time.Second)
	if len(ops) < 2 {
		t.Fatalf("expected agent_start to clear omp-error-banner widget, got %d ops", len(ops))
	}
	clearOp := ops[len(ops)-1]
	if clearOp.Method != "ui.setWidget" || clearOp.Args["view"] != nil || clearOp.Args["content"] != nil {
		t.Fatalf("expected ui.setWidget clear op on agent_start, got %+v", clearOp)
	}

	// 3. Re-pin error while native, then switch native -> terminal: native widget
	// clears and terminal `omp-status-band` shows the formatted error + dismiss hint.
	h.emitEvent(sdk.EventTurnEnd, map[string]any{
		"message": map[string]any{
			"stopReason":   "error",
			"errorMessage": rawErr,
		},
	})
	prevOps := len(h.waitForErrorBannerOps(3, time.Second))
	surface.SetNative(false)
	ops = h.waitForErrorBannerOps(prevOps+1, time.Second)
	if len(ops) <= prevOps || ops[len(ops)-1].Method != "ui.setWidget" || ops[len(ops)-1].Args["view"] != nil {
		t.Fatalf("native->terminal switch must clear omp-error-banner, ops=%+v", ops)
	}
	wc := h.waitForBandWidgetCount(1, time.Second)
	if len(wc) == 0 {
		t.Fatalf("native->terminal switch did not install omp-status-band")
	}
	bandPlain := StripANSI(strings.Join(stringSliceField(wc[len(wc)-1].Args, "lines"), "\n"))
	if !strings.Contains(bandPlain, "✘ "+wantFormatted) || !strings.Contains(bandPlain, "Dismissed when you send your next message.") {
		t.Fatalf("terminal status band after native->terminal switch missing formatted error banner:\n%s", bandPlain)
	}

	// 4. Switch terminal -> native while error is still pinned: native widget re-publishes.
	prevOps = len(h.errorBannerWidgetOps())
	surface.SetNative(true)
	ops = h.waitForErrorBannerOps(prevOps+1, time.Second)
	if len(ops) <= prevOps || ops[len(ops)-1].Method != "widget_push" {
		t.Fatalf("terminal->native switch with pinned error must re-publish omp-error-banner, ops=%+v", ops)
	}
}

// TestPerSessionLifecycleAndCardInvalidation verifies N2: two live sessions,
// Register called twice, lifecycle.End(sessionA) drops sessionA's card
// invalidators so a surface change invalidates ONLY sessionB's cards.
func TestPerSessionLifecycleAndCardInvalidation(t *testing.T) {
	surface.ResetForTests()
	t.Cleanup(surface.ResetForTests)
	surface.SetNative(false)

	hA := startAnsiTestHostNoReset(t, "", nil)
	hB := startAnsiTestHostNoReset(t, "", nil)

	ridA, _ := hA.resolveRenderer("bash")
	ridB, _ := hB.resolveRenderer("bash")
	res := &sdk.ToolRenderResult{Content: []map[string]any{{"type": "text", "text": "ok"}}}

	hA.renderToolView(ridA, "card-A", "bash", "result", map[string]any{"command": "echo a"}, res, false, false, false, 80)
	hB.renderToolView(ridB, "card-B", "bash", "result", map[string]any{"command": "echo b"}, res, false, false, false, 80)

	// End session A via lifecycle.End; session B stays alive.
	lifecycle.End(hA.sessID)

	// Flip surface: only session B's card should be invalidated.
	surface.SetNative(true)
	time.Sleep(30 * time.Millisecond)

	hA.mu.Lock()
	invA := 0
	for _, n := range hA.notifs {
		if n.Method == "tool_render_invalidate" && strField(n.Args, "card") == "card-A" {
			invA++
		}
	}
	hA.mu.Unlock()

	hB.mu.Lock()
	invB := 0
	for _, n := range hB.notifs {
		if n.Method == "tool_render_invalidate" && strField(n.Args, "card") == "card-B" {
			invB++
		}
	}
	hB.mu.Unlock()

	if invA != 0 {
		t.Errorf("ended session A received %d tool_render_invalidate notifications, want 0", invA)
	}
	if invB == 0 {
		t.Errorf("live session B did not receive tool_render_invalidate after session A ended")
	}

	// M2: late events and late renders for ended session A must NOT re-create
	// its session entry or leak a card invalidator onto future surface flips.
	hA.emitEvent(sdk.EventTurnEnd, map[string]any{})
	hA.emitEvent(sdk.EventModelSelect, map[string]any{})
	hA.emitEvent(sdk.EventAgentStart, map[string]any{})
	hA.renderToolView(ridA, "card-A-late", "bash", "result", map[string]any{"command": "echo late"}, res, false, false, false, 80)

	chromeMu.Lock()
	_, resurrected := chromeSessions[hA.sessID]
	chromeMu.Unlock()
	if resurrected {
		t.Errorf("late events/renders resurrected ended session %q in chromeSessions", hA.sessID)
	}

	surface.SetNative(false)
	time.Sleep(30 * time.Millisecond)
	hA.mu.Lock()
	lateInvA := 0
	for _, n := range hA.notifs {
		if n.Method == "tool_render_invalidate" {
			lateInvA++
		}
	}
	hA.mu.Unlock()
	if lateInvA != 0 {
		t.Errorf("ended session A received %d tool_render_invalidate notifications after late render, want 0", lateInvA)
	}
}

// TestSessionReloadClearsTombstone verifies that after lifecycle.End(id) (e.g.
// session_shutdown with reason "reload" or resuming back to a session):
//  1. Late events/renders before any new session_start are still ignored.
//  2. A fresh extension instance emitting session_start for the SAME session ID
//     clears the tombstone and restores header, footer repaints, and card
//     invalidation cleanly.
func TestSessionReloadClearsTombstone(t *testing.T) {
	surface.ResetForTests()
	t.Cleanup(surface.ResetForTests)
	surface.SetNative(false)

	sessID := fmt.Sprintf("ansi-reload-%d", time.Now().UnixNano())
	workDir := t.TempDir()

	// 1. Initial session runs, installs chrome, renders a card, then ends (reload).
	h1 := startAnsiTestHostWithID(t, workDir, sessID, nil)
	h1.emitEvent(sdk.EventSessionStart, map[string]any{})
	if fc := h1.waitForFooterCount(1, time.Second); len(fc) < 1 {
		t.Fatalf("initial session did not install footer")
	}
	rid1, _ := h1.resolveRenderer("bash")
	res := &sdk.ToolRenderResult{Content: []map[string]any{{"type": "text", "text": "ok"}}}
	h1.renderToolView(rid1, "card-pre-reload", "bash", "result", map[string]any{"command": "echo 1"}, res, false, false, false, 80)

	lifecycle.End(sessID)

	// 2. Late events/renders arriving after End and BEFORE new session_start stay ignored.
	h1.emitEvent(sdk.EventTurnEnd, map[string]any{})
	h1.renderToolView(rid1, "card-late", "bash", "result", map[string]any{"command": "echo late"}, res, false, false, false, 80)
	chromeMu.Lock()
	_, resurrected := chromeSessions[sessID]
	chromeMu.Unlock()
	if resurrected {
		t.Fatalf("late event/render before reload session_start resurrected ended session %q", sessID)
	}

	// 3. Reloaded extension instance starts with the SAME session ID.
	h2 := startAnsiTestHostWithID(t, workDir, sessID, nil)
	h2.emitEvent(sdk.EventSessionStart, map[string]any{})
	wc := h2.waitForBandWidgetCount(1, time.Second)
	if len(wc) < 1 {
		t.Fatalf("reloaded session with same ID did not install status-band widget")
	}
	h2.mu.Lock()
	sawHeader := false
	for _, c := range h2.calls {
		if c.Method == "ui.setHeader" && c.Args["clear"] != true {
			sawHeader = true
		}
	}
	h2.mu.Unlock()
	if !sawHeader {
		t.Fatalf("reloaded session with same ID did not install header")
	}

	// Status band repaints work after reload with zero editor delegation or SetEditorText calls.
	prevN := len(wc)
	h2.pushStateUpdate(map[string]any{
		"id":            "opus-reload",
		"name":          "Claude Opus Reload",
		"reasoning":     true,
		"contextWindow": 200000,
	}, "")
	time.Sleep(15 * time.Millisecond)
	h2.emitEvent(sdk.EventModelSelect, map[string]any{})
	wc = h2.waitForBandWidgetCount(prevN+1, time.Second)
	if len(wc) <= prevN {
		t.Fatalf("reloaded session did not repaint band widget on model_select")
	}
	lastLines := stringSliceField(wc[len(wc)-1].Args, "lines")
	if !strings.Contains(StripANSI(strings.Join(lastLines, "\n")), "Opus Reload") {
		t.Fatalf("reloaded band missing new model: %v", lastLines)
	}
	if installs := h2.editorInstallCalls(); installs != 0 {
		t.Fatalf("reloaded session ui.editor.install count = %d, want 0", installs)
	}
	if n := h2.setEditorTextCalls(); n != 0 {
		t.Fatalf("reloaded session ui.setEditorText called %d times, want 0", n)
	}
	rid2, _ := h2.resolveRenderer("bash")
	h2.renderToolView(rid2, "card-post-reload", "bash", "result", map[string]any{"command": "echo 2"}, res, false, false, false, 80)
	surface.SetNative(true)
	time.Sleep(30 * time.Millisecond)
	h1.mu.Lock()
	inv1 := 0
	for _, n := range h1.notifs {
		if n.Method == "tool_render_invalidate" {
			inv1++
		}
	}
	h1.mu.Unlock()
	h2.mu.Lock()
	inv2 := 0
	for _, n := range h2.notifs {
		if n.Method == "tool_render_invalidate" && strField(n.Args, "card") == "card-post-reload" {
			inv2++
		}
	}
	h2.mu.Unlock()
	if inv1 != 0 {
		t.Errorf("pre-reload instance received %d tool_render_invalidate notifications, want 0", inv1)
	}
	if inv2 == 0 {
		t.Errorf("reloaded session did not receive tool_render_invalidate on surface switch")
	}
}

func TestMergedCallViewSuppressedAfterResult(t *testing.T) {
	h := startAnsiTestHost(t, "", nil)
	rid, _ := h.resolveRenderer("bash")

	// Partial call renders non-empty command block.
	vCall := h.renderToolView(rid, "c-merge", "bash", "call",
		map[string]any{"command": "echo hi"}, nil, false, false, true, 80)
	if len(vCall.renderedLines(80)) < 3 {
		t.Fatalf("expected framed call while partial, got %#v", vCall.renderedLines(80))
	}

	// Result renders full card.
	vRes := h.renderToolView(rid, "c-merge", "bash", "result",
		map[string]any{"command": "echo hi"},
		&sdk.ToolRenderResult{Content: []map[string]any{{"type": "text", "text": "hi"}}},
		false, false, false, 80)
	if len(vRes.renderedLines(80)) < 4 {
		t.Fatalf("expected full result card, got %#v", vRes.renderedLines(80))
	}

	// Subsequent call render on same card returns empty container (0 rows).
	vCallAfter := h.renderToolView(rid, "c-merge", "bash", "call",
		map[string]any{"command": "echo hi"}, nil, false, false, false, 80)
	if len(vCallAfter.renderedLines(80)) != 0 {
		t.Fatalf("expected 0 rows from CallView after result, got %#v", vCallAfter.renderedLines(80))
	}
}
