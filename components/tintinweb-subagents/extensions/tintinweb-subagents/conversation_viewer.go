package tintinweb_subagents

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"

	tui "github.com/MichaelKinsy/pigpen/components/pitui"
	"github.com/MichaelKinsy/pigpen/components/pitui/jsstring"
	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

// The live conversation overlay for viewing an agent's session: a scrollable, live-updating view of its transcript.
// upstream: ui/conversation-viewer.ts ConversationViewer.

const (
	chromeLinesBase = 6 // top border + header + header sep + footer sep + footer + bottom border
	minViewport     = 3
	// viewportHeightPct is the height ceiling shared by the overlay's maxHeight and the viewer's viewport cap.
	viewportHeightPct = 70
	// resultMaxChars caps a single tool result or bash output before the viewer elides the rest. The cap bounds render
	// cost: buildContentLines runs on every render and on every scroll key.
	resultMaxChars = 16_000
)

// markdownModes is the cycle order of the viewer's `m` key; markdownModeLabels are short footer labels.
var markdownModes = []viewerMarkdownMode{viewerMarkdownOff, viewerMarkdownAssistant, viewerMarkdownAll}

var markdownModeLabels = map[viewerMarkdownMode]string{viewerMarkdownOff: "raw", viewerMarkdownAssistant: "md", viewerMarkdownAll: "md+"}

// viewerHost is the terminal the viewer runs in. upstream: the TUI the viewer is constructed with.
type viewerHost interface {
	// Rows is the terminal height (terminal.rows).
	Rows() int
	// RequestRender asks for a redraw.
	RequestRender()
}

// viewerSession is a session's transcript: the messages (each a map the viewer only reads; the session either updates
// it in place or hands out a fresh copy, with viewerIDKey, per change) and a change subscription. upstream: AgentSession.messages and AgentSession.subscribe.
type viewerSession interface {
	Messages() []map[string]any
	Subscribe(onChange func()) (unsubscribe func())
}

// emptySession is a session with no messages.
type emptySession struct{}

func (emptySession) Messages() []map[string]any { return nil }
func (emptySession) Subscribe(func()) func()    { return func() {} }

// viewerOptions are the optional parts of a viewer. upstream: the trailing ConversationViewer constructor arguments.
type viewerOptions struct {
	// OnStop aborts the agent shown here; nil: no stop affordance.
	OnStop func()
	// Keybindings are the user's keybindings; nil: the hardcoded defaults.
	Keybindings viewerKeybindings
	// OnSteer sends a steering message to the agent; nil: no compose affordance.
	OnSteer func(message string)
	// ShowCost: whether the header shows an estimated cost after the token count (read once, at construction).
	ShowCost bool
	// ViewerMarkdown is the current `viewerMarkdown` setting, read live; nil: assistant.
	ViewerMarkdown func() viewerMarkdownMode
	// OnMarkdownMode persists a mode chosen with `m`.
	OnMarkdownMode func(viewerMarkdownMode)
}

type markdownComponent interface {
	SetText(text string)
	Render(width int) []string
}

type markdownEntry struct {
	md     markdownComponent
	text   string
	failed bool
}

type conversationViewer struct {
	mu       sync.Mutex
	host     viewerHost
	session  viewerSession
	record   func() agentView
	activity func() *agentActivity
	theme    uiTheme
	done     func()
	opts     viewerOptions
	keys     viewerKeys

	scrollOffset int
	autoScroll   bool
	unsubscribe  func()
	lastInnerW   int
	closed       bool
	// stopArmed is the two-press confirm guard for the stop key, so a stray key cannot kill the agent.
	stopArmed bool
	// composer is the steering composer, present while the user is typing a message to the agent.
	composer *steerInput
	// after are handlers to run once the lock is released.
	after []func()
	// markdownTheme is resolved once; markdownModeOverride is set by the `m` key and wins over the setting.
	markdownTheme        *tui.MarkdownTheme
	markdownModeOverride viewerMarkdownMode
	// markdownCache holds one Markdown per message, so its own text/width cache does the work: a fresh instance per
	// render would re-parse the whole transcript on every keystroke.
	markdownCache map[any]*markdownEntry

	// wrap and newMarkdown are the leaf renderers (a test counts or breaks them).
	wrap        func(text string, width int) []string
	newMarkdown func(text string, theme *tui.MarkdownTheme, style *tui.DefaultTextStyle) markdownComponent
}

func newConversationViewer(host viewerHost, session viewerSession, record func() agentView, activity func() *agentActivity, th uiTheme, done func(), opts viewerOptions) *conversationViewer {
	if activity == nil {
		activity = func() *agentActivity { return nil }
	}
	v := &conversationViewer{
		host: host, session: session, record: record, activity: activity, theme: th, done: done, opts: opts,
		keys: createViewerKeys(opts.Keybindings), autoScroll: true, markdownTheme: fallbackMarkdownTheme(th),
		markdownCache: map[any]*markdownEntry{},
		wrap:          widthx.WrapTextWithAnsi,
		newMarkdown: func(text string, theme *tui.MarkdownTheme, style *tui.DefaultTextStyle) markdownComponent {
			return tui.NewMarkdownWithOptions(text, 0, 0, theme, style, &tui.MarkdownOptions{PreserveOrderedListMarkers: true, PreserveBackslashEscapes: true})
		},
	}
	v.unsubscribe = session.Subscribe(func() {
		v.mu.Lock()
		closed := v.closed
		v.mu.Unlock()
		if !closed {
			host.RequestRender()
		}
	})
	return v
}

// fallbackMarkdownTheme is the Markdown theme built from the viewer's theme. The theme carries only fg and bold, so
// the three remaining styles are raw SGR: plain text instead would silently drop `*emphasis*`'s markers.
// upstream: resolveMarkdownTheme/fallbackMarkdownTheme (pi's own theme, with syntax-highlighted fences, is not
// available to a Go extension).
func fallbackMarkdownTheme(th uiTheme) *tui.MarkdownTheme {
	sgr := func(on, off int) func(string) string {
		return func(text string) string { return fmt.Sprintf("\x1b[%dm%s\x1b[%dm", on, text, off) }
	}
	fg := func(color string) func(string) string { return func(t string) string { return th.Fg(color, t) } }
	return &tui.MarkdownTheme{
		Heading:         func(t string) string { return th.Bold(th.Fg("accent", t)) },
		Link:            fg("accent"),
		LinkUrl:         fg("muted"),
		Code:            fg("muted"),
		CodeBlock:       fg("muted"),
		CodeBlockBorder: fg("dim"),
		Quote:           fg("muted"),
		QuoteBorder:     fg("dim"),
		Hr:              fg("dim"),
		ListBullet:      fg("accent"),
		Bold:            th.Bold,
		Italic:          sgr(3, 23),
		Underline:       sgr(4, 24),
		Strikethrough:   sgr(9, 29),
	}
}

// capResult caps text at resultMaxChars UTF-16 units, reporting the elision separately rather than appending it: the
// notice is the viewer's chrome, not the tool's output (appended, a cut inside a code fence would render it as source).
func capResult(text string) (string, int) {
	n := jsstring.Length(text)
	if n <= resultMaxChars {
		return text, 0
	}
	return jsstring.Slice(text, 0, resultMaxChars), n - resultMaxChars
}

// humanCount is `999` · `1.5k` · `8.4M`: a magnitude cue past 1000. The bracket is chosen against the rounded value, so
// 999,999 reads `1M` rather than `1000.0k`.
func humanCount(n int) string {
	if n < 1_000 {
		return strconv.Itoa(n)
	}
	thousands := n < 999_950
	value, unit := float64(n)/1_000_000, "M"
	if thousands {
		value, unit = float64(n)/1_000, "k"
	}
	return strings.TrimSuffix(jsstring.ToFixed(value, 1), ".0") + unit
}

func truncationNote(elided int) string {
	return fmt.Sprintf("... (truncated, %s more character%s)", humanCount(elided), plural(elided, "", "s"))
}

// ---- input ----

func (v *conversationViewer) HandleInput(data string) {
	var render bool
	var after []func()
	func() {
		v.mu.Lock()
		defer v.mu.Unlock() // a panic below must not leave the viewer locked
		render = v.handleInputLocked(data)
		after, v.after = v.after, nil
	}()
	// The stop and steer handlers run outside the lock: they call into the manager.
	for _, f := range after {
		f()
	}
	if render {
		v.host.RequestRender()
	}
}

// handleInputLocked handles one key and reports whether a redraw is wanted.
func (v *conversationViewer) handleInputLocked(data string) bool {
	// While composing a steer message, the input owns all keys (Enter sends, Esc cancels).
	if v.composer != nil {
		v.composer.handleInput(data)
		return true
	}
	if tui.MatchesKey(data, "escape") || tui.MatchesKey(data, "ctrl+c") || tui.MatchesKey(data, "q") {
		v.closed = true
		v.done()
		return false
	}
	// Enter opens the steering composer (only while the agent can still be steered); when not steerable, fall
	// through so the key still disarms a pending stop.
	if tui.MatchesKey(data, "enter") && v.canSteer() {
		v.stopArmed = false
		v.openComposer()
		return true
	}
	// Stop/abort the agent (only while it can still be stopped). Two-press: the first "x" arms, the second confirms;
	// any other key disarms.
	if tui.MatchesKey(data, "x") {
		if v.isStoppable() {
			if v.stopArmed {
				v.stopArmed = false
				v.after = append(v.after, v.opts.OnStop)
			} else {
				v.stopArmed = true
			}
			return true
		}
		return false
	}
	// Cycle raw → assistant-only → everything: the escape hatch that makes Markdown rendering safe to default on.
	if tui.MatchesKey(data, "m") {
		v.stopArmed = false
		next := markdownModes[(indexOfMode(v.markdownMode())+1)%len(markdownModes)]
		v.markdownModeOverride = next
		if f := v.opts.OnMarkdownMode; f != nil {
			// Persisting writes a file: not under the lock, not on the key path's critical section.
			v.after = append(v.after, func() { f(next) })
		}
		return true
	}
	v.stopArmed = false

	total := len(v.buildContentLines(v.lastInnerW))
	viewport := v.viewportHeight()
	maxScroll := maxInt(0, total-viewport)

	switch {
	case v.keys.scrollUp(data):
		v.scrollOffset = maxInt(0, v.scrollOffset-1)
		v.autoScroll = v.scrollOffset >= maxScroll
	case v.keys.scrollDown(data):
		v.scrollOffset = min(maxScroll, v.scrollOffset+1)
		v.autoScroll = v.scrollOffset >= maxScroll
	case v.keys.pageUp(data):
		v.scrollOffset = maxInt(0, v.scrollOffset-viewport)
		v.autoScroll = false
	case v.keys.pageDown(data):
		v.scrollOffset = min(maxScroll, v.scrollOffset+viewport)
		v.autoScroll = v.scrollOffset >= maxScroll
	case tui.MatchesKey(data, "home"):
		v.scrollOffset = 0
		v.autoScroll = false
	case tui.MatchesKey(data, "end"):
		v.scrollOffset = maxScroll
		v.autoScroll = true
	}
	return false
}

func indexOfMode(m viewerMarkdownMode) int {
	for i, x := range markdownModes {
		if x == m {
			return i
		}
	}
	return -1
}

// markdownMode is the mode in force: an `m` press, else the setting, else the default.
func (v *conversationViewer) markdownMode() viewerMarkdownMode {
	if v.markdownModeOverride != "" {
		return v.markdownModeOverride
	}
	if v.opts.ViewerMarkdown != nil {
		return v.opts.ViewerMarkdown()
	}
	return viewerMarkdownAssistant
}

func (v *conversationViewer) active() bool {
	s := v.record().Status
	return s == "running" || s == "queued"
}

// isStoppable: only when a stop handler exists and the agent is still active.
func (v *conversationViewer) isStoppable() bool { return v.opts.OnStop != nil && v.active() }

// canSteer: only when a steer handler exists and the agent is still active.
func (v *conversationViewer) canSteer() bool { return v.opts.OnSteer != nil && v.active() }

// openComposer opens the inline steering composer and routes subsequent input to it.
func (v *conversationViewer) openComposer() {
	in := newSteerInput()
	in.onSubmit = func(value string) {
		message := jsstring.Trim(value)
		v.composer = nil
		if message != "" {
			v.after = append(v.after, func() { v.opts.OnSteer(message) })
		}
	}
	in.onEscape = func() { v.composer = nil }
	v.composer = in
}

func (v *conversationViewer) Invalidate() {}

func (v *conversationViewer) Dispose() {
	v.mu.Lock()
	v.closed = true
	unsub := v.unsubscribe
	v.unsubscribe = nil
	v.mu.Unlock()
	if unsub != nil {
		unsub()
	}
}

// ---- render ----

func (v *conversationViewer) Render(width int) []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.renderLocked(width)
}

func (v *conversationViewer) renderLocked(width int) []string {
	if width < 6 { // too narrow for any meaningful rendering
		return nil
	}
	th := v.theme
	innerW := width - 4 // border + padding
	v.lastInnerW = innerW
	var lines []string

	pad := func(s string, n int) string { return s + strings.Repeat(" ", maxInt(0, n-widthx.VisibleWidth(s))) }
	row := func(content string) string {
		return th.Fg("border", "│") + " " + widthx.TruncateToWidth(pad(content, innerW), innerW, "...", true) + " " + th.Fg("border", "│")
	}
	hrTop := th.Fg("border", "╭"+strings.Repeat("─", width-2)+"╮")
	hrBot := th.Fg("border", "╰"+strings.Repeat("─", width-2)+"╯")
	hrMid := row(th.Fg("dim", strings.Repeat("─", innerW)))

	// Header
	rec := v.record()
	act := v.activity()
	lines = append(lines, hrTop)
	modeTag := ""
	if label := getPromptModeLabel(rec.Type); label != "" {
		modeTag = " " + th.Fg("dim", "("+label+")")
	}
	var statusIcon string
	switch rec.Status {
	case "running":
		statusIcon = th.Fg("accent", "●")
	case "completed":
		statusIcon = th.Fg("success", "✓")
	case "error":
		statusIcon = th.Fg("error", "✗")
	default:
		statusIcon = th.Fg("dim", "○")
	}
	headerParts := []string{formatDuration(rec.StartedAt, rec.CompletedAt)}
	toolUses := rec.ToolUses
	if act != nil {
		toolUses = act.ToolUses
	}
	if toolUses > 0 {
		headerParts = append([]string{fmt.Sprintf("%d tool%s", toolUses, plural(toolUses, "", "s"))}, headerParts...)
	}
	// Spend from the record, context from the live session.
	if tokens := getLifetimeTotal(rec.Usage); tokens > 0 {
		var percent *float64
		if act != nil {
			percent = getSessionContextPercent(act.Session)
		}
		headerParts = append(headerParts, formatSessionTokens(tokens, percent, th, rec.CompactionCount))
	}
	if v.opts.ShowCost {
		if cost := formatCost(getLifetimeCost(rec.Usage)); cost != "" {
			headerParts = append(headerParts, cost)
		}
	}
	lines = append(lines, row(statusIcon+" "+renderAgentName(rec.Type, th, agentNameStyle{Bold: true})+modeTag+"  "+th.Fg("muted", rec.Description)+" "+th.Fg("dim", "·")+" "+fgPreservingNestedStyles(th, "dim", strings.Join(headerParts, " · "))))
	if line := v.invocationLine(rec); line != "" {
		lines = append(lines, row(line))
	}
	lines = append(lines, hrMid)

	// Content area: rebuilt every render (live data, no cache needed).
	content := v.buildContentLines(innerW)
	viewport := v.viewportHeight()
	maxScroll := maxInt(0, len(content)-viewport)
	if v.autoScroll {
		v.scrollOffset = maxScroll
	}
	visibleStart := min(v.scrollOffset, maxScroll)
	end := min(len(content), visibleStart+viewport)
	visible := content[visibleStart:end]
	for i := 0; i < viewport; i++ {
		if i < len(visible) {
			lines = append(lines, row(visible[i]))
		} else {
			lines = append(lines, row(""))
		}
	}

	// Footer
	lines = append(lines, hrMid)
	if v.composer != nil {
		// Composer row: the input renders its own `> ` prompt and cursor.
		lines = append(lines, row(v.composer.render(innerW)))
		hint := th.Fg("dim", "Enter send · Esc cancel")
		left := th.Fg("accent", "✎ steer")
		gap := maxInt(1, innerW-widthx.VisibleWidth(left)-widthx.VisibleWidth(hint))
		lines = append(lines, row(left+strings.Repeat(" ", gap)+hint))
	} else {
		// Actions on the left, navigation on the right. The scroll hint keeps its full key list; it leads the right
		// group so "Esc close" is the only part that truncates first.
		sep := th.Fg("dim", " · ")
		var actions []string
		if v.canSteer() {
			actions = append(actions, th.Fg("dim", "Enter steer"))
		}
		if v.isStoppable() {
			if v.stopArmed {
				actions = append(actions, th.Fg("error", "x again to STOP"))
			} else {
				actions = append(actions, th.Fg("dim", "x stop"))
			}
		}
		// Abbreviated (`raw`/`md`/`md+`): the idle footer is already full at 80 columns.
		actions = append(actions, th.Fg("dim", "m "+markdownModeLabels[v.markdownMode()]))
		footerRight := th.Fg("dim", "↑↓ scroll · PgUp/PgDn or Shift+↑↓ · Esc close")

		// The line-count/scroll-% readout is prepended only when there is spare width: it is dropped first.
		scrollPct := "100%"
		if len(content) > viewport {
			scrollPct = strconv.FormatFloat(jsRound(float64(visibleStart+viewport)/float64(len(content))*100), 'f', -1, 64) + "%"
		}
		count := th.Fg("dim", fmt.Sprintf("%d lines · %s", len(content), scrollPct))
		withCount := strings.Join(append([]string{count}, actions...), sep)
		footerLeft := strings.Join(actions, sep)
		if widthx.VisibleWidth(withCount)+widthx.VisibleWidth(footerRight)+1 <= innerW {
			footerLeft = withCount
		}
		gap := maxInt(1, innerW-widthx.VisibleWidth(footerLeft)-widthx.VisibleWidth(footerRight))
		lines = append(lines, row(footerLeft+strings.Repeat(" ", gap)+footerRight))
	}
	lines = append(lines, hrBot)
	return lines
}

func (v *conversationViewer) viewportHeight() int {
	// The cap mirrors the overlay's maxHeight: otherwise the viewer would render more lines than the overlay shows and
	// clip the footer.
	maxRows := v.host.Rows() * viewportHeightPct / 100
	return maxInt(minViewport, maxRows-v.chromeLines())
}

func (v *conversationViewer) chromeLines() int {
	n := chromeLinesBase
	if v.invocationLine(v.record()) != "" {
		n++
	}
	if v.composer != nil { // the composer adds one row above the footer hint while it is open
		n++
	}
	return n
}

// invocationLine: the canonical id here, the short label everywhere else (this overlay has the width to
// disambiguate providers).
func (v *conversationViewer) invocationLine(rec agentView) string {
	t := buildInvocationTags(rec.Invocation)
	model := t.ModelID
	if model == "" {
		model = t.ModelName
	}
	parts := t.Tags
	if model != "" {
		parts = append([]string{model}, t.Tags...)
	}
	if len(parts) == 0 {
		return ""
	}
	return v.theme.Fg("dim", "  ↳ "+strings.Join(parts, " · "))
}

// rawLines wraps text literally: the pre-Markdown path, and the fallback from it.
func (v *conversationViewer) rawLines(text string, width int, dim bool) []string {
	lines := v.wrap(text, width)
	if !dim {
		return lines
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = v.theme.Fg("dim", l)
	}
	return out
}

// msgKey identifies a message across renders: its stable id when the session provides one (a session that hands out
// copies), else the identity of its map (a session that updates its messages in place).
func msgKey(m map[string]any) any {
	if id, ok := m[viewerIDKey]; ok {
		return id
	}
	return reflect.ValueOf(m).Pointer()
}

// markdownLines renders text as Markdown, reusing this message's component instance.
func (v *conversationViewer) markdownLines(msg map[string]any, text string, width int, dim bool) []string {
	key := msgKey(msg)
	entry := v.markdownCache[key]
	if entry == nil {
		var style *tui.DefaultTextStyle
		if dim {
			// Keeps result prose visually receded, the way the raw path's per-line dim did. Fenced code is left alone:
			// the theme highlights it with its own colors.
			style = &tui.DefaultTextStyle{Color: func(t string) string { return v.theme.Fg("dim", t) }}
		}
		entry = &markdownEntry{md: v.newMarkdown(text, v.markdownTheme, style), text: text}
		v.markdownCache[key] = entry
	} else if entry.text != text {
		// Streaming: the message object is stable, its text grows. A failed prefix remains unsafe after append-only
		// deltas, so retry only when the content was replaced or truncated.
		shouldRetry := !strings.HasPrefix(text, entry.text)
		entry.md.SetText(text)
		entry.text = text
		if shouldRetry {
			entry.failed = false
		}
	}
	if entry.failed {
		return v.rawLines(text, width, dim)
	}
	lines, ok := renderMarkdownSafely(entry.md, width)
	if !ok {
		// The parser is recursive and this is arbitrary tool output: render() is on the TUI's critical path, so a throw
		// degrades to the literal path, remembered so it does not repeat on every render and scroll key.
		entry.failed = true
		return v.rawLines(text, width, dim)
	}
	return lines
}

func renderMarkdownSafely(md markdownComponent, width int) (lines []string, ok bool) {
	defer func() {
		if recover() != nil {
			lines, ok = nil, false
		}
	}()
	return md.Render(width), true
}

// ---- transcript ----

// contentMaps is a message content array, whichever form it was decoded to.
func contentMaps(v any) []map[string]any {
	switch c := v.(type) {
	case []map[string]any:
		return c
	case []any:
		out := make([]map[string]any, 0, len(c))
		for _, x := range c {
			if m, ok := x.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}

func msgStr(v any) string { s, _ := v.(string); return s }

// extractText is the text blocks of a message content array joined by newlines. upstream: context.ts extractText.
func extractText(content any) string {
	var parts []string
	for _, c := range contentMaps(content) {
		if c["type"] == "text" {
			parts = append(parts, msgStr(c["text"]))
		}
	}
	return strings.Join(parts, "\n")
}

func (v *conversationViewer) buildContentLines(width int) []string {
	if width <= 0 {
		return nil
	}
	th := v.theme
	messages := v.session.Messages()
	var lines []string
	if len(messages) == 0 {
		return []string{th.Fg("dim", "(waiting for first message...)")}
	}
	trunc := func(s string) string { return widthx.TruncateToWidth(s, width, "...", false) }

	mode := v.markdownMode()
	needsSeparator := false
	seen := map[any]bool{}
	for _, msg := range messages {
		seen[msgKey(msg)] = true
		switch role := msgStr(msg["role"]); role {
		case "user":
			text := msgStr(msg["content"])
			if _, isString := msg["content"].(string); !isString {
				text = extractText(msg["content"])
			}
			if jsstring.Trim(text) == "" {
				continue
			}
			if needsSeparator {
				lines = append(lines, th.Fg("dim", "───"))
			}
			lines = append(lines, th.Fg("accent", "[User]"))
			lines = append(lines, v.wrap(jsstring.Trim(text), width)...)
		case "assistant":
			var textParts, toolCalls []string
			for _, c := range contentMaps(msg["content"]) {
				switch {
				case c["type"] == "text" && msgStr(c["text"]) != "":
					textParts = append(textParts, msgStr(c["text"]))
				case c["type"] == "toolCall":
					name := msgStr(c["name"])
					if name == "" {
						name = msgStr(c["toolName"])
					}
					if name == "" {
						name = "unknown"
					}
					toolCalls = append(toolCalls, name)
				}
			}
			if needsSeparator {
				lines = append(lines, th.Fg("dim", "───"))
			}
			lines = append(lines, th.Bold("[Assistant]"))
			if len(textParts) > 0 {
				text := jsstring.Trim(strings.Join(textParts, "\n"))
				if mode == viewerMarkdownOff {
					lines = append(lines, v.rawLines(text, width, false)...)
				} else {
					lines = append(lines, v.markdownLines(msg, text, width, false)...)
				}
			}
			for _, name := range toolCalls {
				lines = append(lines, trunc(th.Fg("muted", "  [Tool: "+name+"]")))
			}
		case "toolResult":
			text, elided := capResult(jsstring.Trim(extractText(msg["content"])))
			if text == "" {
				continue
			}
			if needsSeparator {
				lines = append(lines, th.Fg("dim", "───"))
			}
			lines = append(lines, th.Fg("dim", "[Result]"))
			if mode == viewerMarkdownAll {
				lines = append(lines, v.markdownLines(msg, text, width, true)...)
			} else {
				lines = append(lines, v.rawLines(text, width, true)...)
			}
			if elided > 0 {
				lines = append(lines, trunc(th.Fg("dim", truncationNote(elided))))
			}
		case "bashExecution":
			if needsSeparator {
				lines = append(lines, th.Fg("dim", "───"))
			}
			lines = append(lines, trunc(th.Fg("muted", "  $ "+msgStr(msg["command"]))))
			if out := jsstring.Trim(msgStr(msg["output"])); out != "" {
				// Same cap as a tool result, never Markdown: command output is definitionally not authored as Markdown.
				text, elided := capResult(out)
				lines = append(lines, v.rawLines(text, width, true)...)
				if elided > 0 {
					lines = append(lines, trunc(th.Fg("dim", truncationNote(elided))))
				}
			}
		default:
			continue
		}
		needsSeparator = true
	}
	for k := range v.markdownCache { // a message that is gone (compacted away) does not pin its render
		if !seen[k] {
			delete(v.markdownCache, k)
		}
	}

	// Streaming indicator for running agents.
	if act := v.activity(); v.record().Status == "running" && act != nil {
		lines = append(lines, "", trunc(th.Fg("accent", "▍ ")+th.Fg("dim", describeActivity(act.ActiveTools, act.ResponseText))))
	}

	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = trunc(l)
	}
	return out
}
