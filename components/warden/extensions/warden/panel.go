package warden

import (
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	tui "github.com/MichaelKinsy/pigpen/components/pitui"
	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

// Port of src/panel.ts: the trace sidebar and the config overlay, as the SDK's focused remote components (the host
// draws what Render returns and routes input only while it owns focus). Keys are matched with pitui's MatchesKey and
// text is measured and wrapped with pitui's widthx, the Go port of pi-tui's utilities.
//
// Rendering is derived from the trace each time and does bounded work: the lines of the entries kept in memory
// (at most 100), with no host call, no file access and no waiting.

// panelActions are what a panel asks of its overlay.
type panelActions struct {
	// unfocus keeps the panel visible but gives keyboard input back to the editor. False says it could not (no overlay
	// handle): the panel closes itself instead.
	unfocus func() bool
	// focus takes keyboard input for the panel (a click on it).
	focus func()
}

// release hands keyboard input back to the editor; false means the panel should close instead.
func (a panelActions) release() bool { return a.unfocus != nil && a.unfocus() }

// panelBase carries what both panels share: scroll position, viewport, the render request and the border.
type panelBase struct {
	mu         sync.Mutex
	focused    bool
	scroll     int
	viewport   int
	invalidate func()
	theme      ThemeLike
	actions    panelActions
}

func (p *panelBase) SetInvalidate(fn func()) {
	p.mu.Lock()
	p.invalidate = fn
	p.mu.Unlock()
}

// SetFocused is called by the SDK when keyboard focus changes.
func (p *panelBase) SetFocused(focused bool) {
	p.mu.Lock()
	p.focused = focused
	p.mu.Unlock()
}

// SetViewport: the overlay tells us how tall we may be; the panel never shrinks below five rows.
func (p *panelBase) SetViewport(rows int) {
	p.mu.Lock()
	p.viewport = max(5, rows)
	p.mu.Unlock()
}

func (p *panelBase) requestRender() {
	p.mu.Lock()
	fn := p.invalidate
	p.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// frame puts the left border on the visible window of all, with the "more below" footer when it does not fit.
// p.mu must be held; endLabel is the footer when the window reaches the end.
func (p *panelBase) frame(all []string, width int, endLabel string) []string {
	border := p.theme.Fg("muted", "│ ")
	inner := max(10, width-2)
	maxScroll := max(0, len(all)-p.viewport)
	if p.scroll > maxScroll {
		p.scroll = maxScroll
	}
	end := min(len(all), p.scroll+p.viewport)
	visible := append([]string{}, all[p.scroll:end]...)
	if len(all) > p.viewport {
		last := len(visible) - 1
		below := len(all) - p.scroll - p.viewport
		above := p.scroll
		var label string
		if below > 0 {
			label = "… " + strconv.Itoa(below) + " more below"
			if above > 0 {
				label += " · " + strconv.Itoa(above) + " above"
			}
		} else {
			label = "… " + endLabel
			if above > 0 {
				label += " · " + strconv.Itoa(above) + " above"
			}
		}
		visible[last] = widthx.TruncateToWidth(p.theme.Fg("dim", label), inner, "", false)
	}
	for len(visible) < p.viewport {
		visible = append(visible, "")
	}
	out := make([]string, len(visible))
	for i, line := range visible {
		out[i] = border + line
	}
	return out
}

// wheel scrolls three lines per notch; a left press focuses the panel.
func (p *panelBase) mouse(event sdk.MouseEvent) (sdk.RemoteComponentResult, error) {
	p.mu.Lock()
	switch {
	case event.Type == sdk.MouseWheel:
		p.scroll = max(0, p.scroll+event.WheelDelta*3)
		p.mu.Unlock()
		p.requestRender()
	case event.Type == sdk.MousePress && event.Button == sdk.MouseButtonLeft:
		p.mu.Unlock()
		if p.actions.focus != nil {
			p.actions.focus()
		}
		p.requestRender()
	default:
		p.mu.Unlock()
	}
	return sdk.RemoteComponentResult{}, nil
}

// ---------------------------------------------------------------------------
// The trace sidebar.

// TracePanel lists the trace newest-first, live-updating while open. It opens without taking keyboard input: the
// editor keeps working while it is visible. A click inside it (fullscreen mode) focuses it; then up/down/j/k scroll a
// line, PgUp/PgDn a page, Home/End jump, d toggles compact and detailed, c clears the trace, Esc returns input to the
// editor, and q closes. Wheel scrolling works without focus.
type TracePanel struct {
	panelBase
	trace   *Trace
	compact bool
	title   string
	unsub   func()
}

// NewTracePanel builds the panel and subscribes it to the trace; Dispose unsubscribes.
func NewTracePanel(trace *Trace, theme ThemeLike, actions panelActions) *TracePanel {
	p := &TracePanel{trace: trace, compact: true, title: "pi-warden trace"}
	p.theme, p.actions, p.viewport = theme, actions, 20
	p.unsub = trace.Subscribe(func(TraceEvent) { p.requestRender() })
	return p
}

// Dispose stops listening to the trace.
func (p *TracePanel) Dispose() { p.unsub() }

func (p *TracePanel) lines(width int, entries []TraceEntry) []string {
	theme := p.theme
	modeLabel := "compact"
	if !p.compact {
		modeLabel = "detailed"
	}
	keys := "click for keys · wheel scrolls · /warden trace closes"
	if p.focused {
		keys = "↑↓ PgUp PgDn scroll · d " + modeLabel + " · c clear · esc back · q close"
	}
	plural := "s"
	if len(entries) == 1 {
		plural = ""
	}
	out := []string{
		theme.Bold(theme.Fg("accent", p.title)) + theme.Fg("muted", " · "+strconv.Itoa(len(entries))+" event"+plural),
		theme.Fg("muted", keys),
		theme.Fg("borderMuted", strings.Repeat("─", max(0, width))),
	}
	trunc := func(lines []string) []string {
		for i, l := range lines {
			lines[i] = widthx.TruncateToWidth(l, width, "", false)
		}
		return lines
	}
	if len(entries) == 0 {
		out = append(out, widthx.WrapTextWithAnsi(theme.Fg("muted", "No guarded activity yet this session. Verdicts, Jev scores, and what the agent was told will appear here."), max(10, width))...)
		return trunc(out)
	}
	const rail = 16
	for index := len(entries) - 1; index >= 0; index-- {
		entry := entries[index]
		status, body := ParseVerdictLine(entry.Line, string(entry.Guard))
		color := "text"
		if c, ok := LevelColor[status]; ok {
			color = c
		}
		chip, indent := "", rail
		if status != "" {
			chip = theme.Bold(theme.Fg(color, strings.ToUpper(status))) + "  "
			indent += utf16Len(status) + 2
		}
		head := theme.Fg("muted", entry.At.Format("15:04:05")) + " " + theme.Fg(color, theme.Bold(panelPadEnd(string(entry.Guard), 6))) + " "
		segments := make([]string, len(body))
		for n, segment := range body {
			segments[n] = RenderSegment(segment, n == 0, theme)
		}
		wrapped := widthx.WrapTextWithAnsi(strings.Join(segments, theme.Fg("dim", " · ")), max(10, width-indent))
		first := ""
		if len(wrapped) > 0 {
			first = wrapped[0]
		}
		out = append(out, head+chip+first)
		if !p.compact {
			if len(wrapped) > 1 {
				for _, continuation := range wrapped[1:] {
					out = append(out, strings.Repeat(" ", indent)+continuation)
				}
			}
			for _, detail := range entry.Details {
				for n, line := range widthx.WrapTextWithAnsi(detail, max(10, width-rail-2)) {
					marker := "  "
					if n == 0 {
						marker = "· "
					}
					out = append(out, strings.Repeat(" ", rail)+theme.Fg("muted", marker)+theme.Fg("dim", line))
				}
			}
		} else if len(entry.Details) > 0 {
			// Compact: show only the first detail line, truncated.
			first := entry.Details[0]
			if room := width - indent - 4; utf16Len(first) > room {
				first = utf16Slice(first, 0, max(0, room)) + "…"
			}
			out = append(out, strings.Repeat(" ", indent)+theme.Fg("dim", first))
		}
		out = append(out, "")
	}
	return trunc(out)
}

// Render draws the panel: a left border makes the overlay read as a pane; the content column is two cells narrower.
func (p *TracePanel) Render(width int) []string {
	// The snapshot is taken before the panel's lock: the trace calls its listeners (which ask this panel to render)
	// while it holds its own lock, so holding both in the other order could deadlock.
	entries := p.trace.Entries()
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.frame(p.lines(max(10, width-2), entries), width, "end of trace")
}

// HandleInput implements sdk.RemoteComponent.
func (p *TracePanel) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	if data == "q" || tui.MatchesKey(data, tui.KeyCtrl("c")) {
		return sdk.RemoteComponentResult{Done: true}, nil
	}
	if tui.MatchesKey(data, tui.KeyEscape) {
		if !p.actions.release() {
			return sdk.RemoteComponentResult{Done: true}, nil
		}
		p.requestRender()
		return sdk.RemoteComponentResult{}, nil
	}
	if data == "c" {
		p.trace.Clear()
		p.requestRender()
		return sdk.RemoteComponentResult{}, nil
	}
	p.mu.Lock()
	switch {
	case data == "d":
		p.compact = !p.compact
	case tui.MatchesKey(data, tui.KeyUp) || data == "k":
		p.scroll = max(0, p.scroll-1)
	case tui.MatchesKey(data, tui.KeyDown) || data == "j":
		p.scroll++
	case tui.MatchesKey(data, tui.KeyPageUp):
		p.scroll = max(0, p.scroll-p.viewport)
	case tui.MatchesKey(data, tui.KeyPageDown):
		p.scroll += p.viewport
	case tui.MatchesKey(data, tui.KeyHome):
		p.scroll = 0
	case tui.MatchesKey(data, tui.KeyEnd):
		p.scroll = math.MaxInt32
	default:
		p.mu.Unlock()
		return sdk.RemoteComponentResult{}, nil
	}
	p.mu.Unlock()
	p.requestRender()
	return sdk.RemoteComponentResult{}, nil
}

// HandleMouse implements sdk.MouseHandler.
func (p *TracePanel) HandleMouse(event sdk.MouseEvent) (sdk.RemoteComponentResult, error) {
	return p.mouse(event)
}

// ---------------------------------------------------------------------------
// The config overlay.

type configEntry struct {
	path, label string
	level       int
	value       any
	// kind is "boolean", "number" or "string".
	kind     string
	editable bool
}

// skippedSections are the top-level sections the config panel leaves out.
var skippedSections = map[string]bool{"action": true, "stuck": true, "done": true, "slop": true, "security": true, "rules": true, "context": true, "runaway": true, "notify": true, "subagent": true}

// flattenConfig lists the primitive settings of a config object in file order, a header row for each nested object;
// lists and null are left out.
func flattenConfig(data []byte) []configEntry {
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.UseNumber()
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil
	}
	return flattenObject(dec, "")
}

func flattenObject(dec *json.Decoder, prefix string) []configEntry {
	var entries []configEntry
	level := 0
	if prefix != "" {
		level = len(strings.Split(prefix, "."))
	}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return entries
		}
		key, _ := keyTok.(string)
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		skip := skippedSections[key] && prefix == ""
		valTok, err := dec.Token()
		if err != nil {
			return entries
		}
		switch v := valTok.(type) {
		case json.Delim:
			if v == '{' {
				sub := flattenObject(dec, path)
				if !skip {
					entries = append(entries, configEntry{path: path, label: key, level: level, value: "", kind: "string"})
					entries = append(entries, sub...)
				}
			} else { // a list: consumed and left out
				depth := 1
				for depth > 0 {
					t, err := dec.Token()
					if err != nil {
						return entries
					}
					if d, ok := t.(json.Delim); ok {
						if d == '[' || d == '{' {
							depth++
						} else {
							depth--
						}
					}
				}
			}
		case bool:
			if !skip {
				entries = append(entries, configEntry{path: path, label: key, level: level, value: v, kind: "boolean", editable: true})
			}
		case json.Number:
			if !skip {
				f, _ := v.Float64()
				entries = append(entries, configEntry{path: path, label: key, level: level, value: f, kind: "number", editable: true})
			}
		case string:
			if !skip {
				entries = append(entries, configEntry{path: path, label: key, level: level, value: v, kind: "string", editable: true})
			}
		}
	}
	_, _ = dec.Token() // the closing brace
	return entries
}

var reDecimal = regexp.MustCompile(`^[+-]?(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?$`)

// panelJSToNumber is Number(text): blank is 0, decimal and 0x/0o/0b forms parse, anything else is NaN.
func panelJSToNumber(text string) float64 {
	s := jsTrim(text)
	switch {
	case s == "":
		return 0
	case reDecimal.MatchString(s):
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return math.NaN()
		}
		return f
	case len(s) > 2 && s[0] == '0':
		base := map[byte]int{'x': 16, 'X': 16, 'o': 8, 'O': 8, 'b': 2, 'B': 2}[s[1]]
		if base != 0 {
			if n, err := strconv.ParseUint(s[2:], base, 64); err == nil {
				return float64(n)
			}
		}
	}
	return math.NaN()
}

func parsePanelValue(raw, kind string) any {
	switch kind {
	case "boolean":
		return raw == "true" || raw == "1"
	case "number":
		if n := panelJSToNumber(raw); !math.IsNaN(n) && !math.IsInf(n, 0) {
			return n
		}
	}
	return raw
}

// ConfigPanel browses and edits the user's settings: enter toggles a boolean or edits a value, s saves the edits into
// the user file, esc backs out, q closes.
type ConfigPanel struct {
	panelBase
	cursor     int
	editing    bool
	editBuffer string
	edits      map[string]any
	editOrder  []string
	entries    []configEntry
	save       func(edits map[string]any, order []string) error
	status     string
}

// NewConfigPanel builds the panel over the effective configuration; save persists a set of edits (dotted path -> value)
// into the user file.
func NewConfigPanel(cfg Config, theme ThemeLike, actions panelActions, save func(map[string]any, []string) error) *ConfigPanel {
	data, _ := json.Marshal(cfg)
	p := &ConfigPanel{entries: flattenConfig(data), edits: map[string]any{}, save: save}
	p.theme, p.actions, p.viewport = theme, actions, 20
	return p
}

func (p *ConfigPanel) lines(width int) []string {
	theme := p.theme
	mode := "browsing"
	if p.editing {
		mode = "editing"
	}
	keys := "click for keys · /warden config closes"
	if p.focused {
		esc := "back"
		if p.editing {
			esc = "cancel"
		}
		keys = "↑↓ navigate · enter toggle/edit · esc " + esc + " · s save · " + mode
	}
	out := []string{
		theme.Bold(theme.Fg("accent", "pi-warden config")) + theme.Fg("muted", " · "+strconv.Itoa(len(p.entries))+" keys"),
		theme.Fg("muted", keys),
		theme.Fg("borderMuted", strings.Repeat("─", max(0, width))),
	}
	for index, entry := range p.entries {
		selected := index == p.cursor
		indent := strings.Repeat("  ", entry.level)
		_, changed := p.edits[entry.path]
		label := entry.label
		if !entry.editable {
			out = append(out, indent+theme.Fg("accent", theme.Bold(label+"/")))
			continue
		}
		var value string
		switch {
		case selected && p.editing && entry.kind != "boolean":
			value = p.editBuffer + "\u2588"
		case entry.kind == "boolean":
			if entry.value.(bool) {
				value = "●"
			} else {
				value = "○"
			}
		case entry.kind == "number":
			value = formatNumber(entry.value.(float64))
		default:
			value = entry.value.(string)
			if utf16Len(value) > 40 {
				value = utf16Slice(value, 0, 37) + "…"
			}
		}
		mark := ""
		if changed {
			mark = theme.Fg("warning", " *")
		}
		marker := "  "
		if selected {
			marker = "> "
		}
		line := indent + marker + theme.Fg("muted", label) + " " + theme.Fg("text", value) + mark
		if selected {
			line = theme.Bold(line)
		}
		out = append(out, line)
	}
	if p.status != "" {
		out = append(out, "", theme.Fg("muted", p.status))
	}
	for i, l := range out {
		out[i] = widthx.TruncateToWidth(l, width, "", false)
	}
	return out
}

// Render draws the panel.
func (p *ConfigPanel) Render(width int) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.frame(p.lines(max(10, width-2)), width, "end")
}

func (p *ConfigPanel) setEdit(path string, value any) {
	if _, seen := p.edits[path]; !seen {
		p.editOrder = append(p.editOrder, path)
	}
	p.edits[path] = value
}

// HandleInput implements sdk.RemoteComponent.
func (p *ConfigPanel) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	if data == "q" && !p.isEditing() || tui.MatchesKey(data, tui.KeyCtrl("c")) {
		return sdk.RemoteComponentResult{Done: true}, nil
	}
	p.mu.Lock()
	entries := p.entries
	if len(entries) == 0 {
		p.mu.Unlock()
		return sdk.RemoteComponentResult{}, nil
	}
	if tui.MatchesKey(data, tui.KeyEscape) {
		if p.editing {
			p.editing = false
			p.mu.Unlock()
			p.requestRender()
			return sdk.RemoteComponentResult{}, nil
		}
		p.mu.Unlock()
		if !p.actions.release() {
			return sdk.RemoteComponentResult{Done: true}, nil
		}
		p.requestRender()
		return sdk.RemoteComponentResult{}, nil
	}
	if p.editing {
		entry := &entries[p.cursor]
		switch {
		case tui.MatchesKey(data, tui.KeyReturn):
			value := parsePanelValue(p.editBuffer, entry.kind)
			p.setEdit(entry.path, value)
			entry.value = value
			if f, ok := value.(float64); ok && entry.kind == "number" {
				entry.value = f
			} else if entry.kind == "number" {
				// The text was not a number: upstream keeps the raw text as the value; the panel keeps showing it.
				entry.kind, entry.value = "string", value
			}
			p.editing = false
		case tui.MatchesKey(data, tui.KeyBackspace):
			if n := utf16Len(p.editBuffer); n > 0 {
				p.editBuffer = utf16Slice(p.editBuffer, 0, n-1)
			}
		case utf16Len(data) == 1 && data >= " ":
			p.editBuffer += data
		default:
			p.mu.Unlock()
			return sdk.RemoteComponentResult{}, nil
		}
		p.mu.Unlock()
		p.requestRender()
		return sdk.RemoteComponentResult{}, nil
	}
	switch {
	case tui.MatchesKey(data, tui.KeyUp) || data == "k":
		p.cursor = max(0, p.cursor-1)
	case tui.MatchesKey(data, tui.KeyDown) || data == "j":
		p.cursor = min(len(entries)-1, p.cursor+1)
	case tui.MatchesKey(data, tui.KeyPageUp):
		p.cursor = max(0, p.cursor-p.viewport)
	case tui.MatchesKey(data, tui.KeyPageDown):
		p.cursor = min(len(entries)-1, p.cursor+p.viewport)
	case tui.MatchesKey(data, tui.KeyHome):
		p.cursor = 0
	case tui.MatchesKey(data, tui.KeyEnd):
		p.cursor = len(entries) - 1
	case data == "enter" || tui.MatchesKey(data, tui.KeyReturn):
		entry := &entries[p.cursor]
		if !entry.editable {
			break
		}
		if entry.kind == "boolean" {
			flipped := !entry.value.(bool)
			p.setEdit(entry.path, flipped)
			entry.value = flipped
		} else {
			p.editing = true
			if f, ok := entry.value.(float64); ok {
				p.editBuffer = formatNumber(f)
			} else {
				p.editBuffer = entry.value.(string)
			}
		}
	case data == "s":
		p.mu.Unlock()
		p.doSave()
		return sdk.RemoteComponentResult{}, nil
	default:
		p.mu.Unlock()
		return sdk.RemoteComponentResult{}, nil
	}
	// Keep the cursor visible.
	if p.cursor < p.scroll {
		p.scroll = p.cursor
	} else if p.cursor >= p.scroll+p.viewport {
		p.scroll = p.cursor - p.viewport + 1
	}
	p.mu.Unlock()
	p.requestRender()
	return sdk.RemoteComponentResult{}, nil
}

func (p *ConfigPanel) isEditing() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.editing
}

// doSave writes the edits into the user file and clears them; a failure is shown at the bottom of the panel.
func (p *ConfigPanel) doSave() {
	p.mu.Lock()
	if len(p.edits) == 0 {
		p.mu.Unlock()
		return
	}
	edits, order := p.edits, p.editOrder
	p.mu.Unlock()
	err := p.save(edits, order)
	p.mu.Lock()
	if err != nil {
		p.status = "could not save: " + err.Error()
	} else {
		p.edits, p.editOrder, p.status = map[string]any{}, nil, ""
	}
	p.mu.Unlock()
	p.requestRender()
}

// HandleMouse implements sdk.MouseHandler.
func (p *ConfigPanel) HandleMouse(event sdk.MouseEvent) (sdk.RemoteComponentResult, error) {
	return p.mouse(event)
}

// Edits is what has been changed and not yet saved, as dotted path -> value, in the order the edits were made.
func (p *ConfigPanel) Edits() map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]any, len(p.edits))
	for k, v := range p.edits {
		out[k] = v
	}
	return out
}
