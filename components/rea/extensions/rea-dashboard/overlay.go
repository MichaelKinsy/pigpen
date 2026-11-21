package readashboard

import (
	"strings"
	"sync"
	"unicode"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

type tabID int

const (
	tabInvestigation tabID = iota
	tabTargets
	tabProviders
	tabAbout
	tabCount
)

var (
	tabNames  = [tabCount]string{"Investigation", "Targets", "Providers", "About"}
	tabShorts = [tabCount]string{"Inv", "Tgt", "Prov", "About"}
)

type mode int

const (
	modeList   mode = iota
	modeDetail      // a call, or a provider
	modeMenu        // the actions of a target
	modeResult      // a quick inspect
)

// Actions of a target, in menu order.
const (
	actInvestigate = iota
	actInspect
	actDerive
	actCount
)

var actionNames = [actCount]string{"Investigate…", "Quick inspect", "Derive piglet…"}

// Values the overlay closes with, which /rea acts on once the overlay is gone.
const (
	closeInvestigate = "investigate"
	closeDerive      = "derive"
)

// overlay is the /rea component: four keyboard-driven tabs over one model snapshot per frame.
type overlay struct {
	d      *dashboard
	height func() int

	mu         sync.Mutex
	tab        tabID
	sel        [tabCount]int
	follow     bool // the Investigation selection follows the newest call
	mode       mode
	menuSel    int
	scroll     int
	rawOpen    bool
	notice     string
	invalidate func()
	unsub      func()
	closed     bool

	// Where the last frame put things, for the mouse.
	bodyTop  int
	bodyRows int
	offset   int
	listLen  int
	tabSpans [tabCount][2]int
}

func newOverlay(d *dashboard, tab tabID, height func() int) *overlay {
	if tab < 0 || tab >= tabCount {
		tab = tabInvestigation
	}
	return &overlay{d: d, height: height, tab: tab, follow: true}
}

// SetInvalidate ties the SDK's redraw callback to the dashboard's change notifications; nil detaches.
func (o *overlay) SetInvalidate(fn func()) {
	o.mu.Lock()
	unsub := o.unsub
	o.unsub, o.invalidate = nil, fn
	closed := o.closed
	o.mu.Unlock()
	if unsub != nil {
		unsub()
	}
	if fn != nil && !closed {
		sub := o.d.subscribe(fn)
		o.mu.Lock()
		o.unsub = sub
		o.mu.Unlock()
	}
}

// Dispose detaches the overlay from the dashboard. It is safe to call more than once.
func (o *overlay) Dispose() {
	o.mu.Lock()
	o.closed = true
	unsub := o.unsub
	o.unsub = nil
	o.mu.Unlock()
	if unsub != nil {
		unsub()
	}
}

func (o *overlay) termHeight() int {
	if o.height != nil {
		if h := o.height(); h > 0 {
			return h
		}
	}
	return 24
}

// Render draws the overlay at the terminal width; the host frames it with a one-cell box.
func (o *overlay) Render(width int) []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	w, h := max(width-2, 1), max(o.termHeight()-2, 1)
	m := o.d.model()
	v := o.build(m, w)
	o.listLen = len(v.rows)

	out := make([]string, 0, h)
	out = append(out, o.tabBar(w))
	if h >= 4 {
		out = append(out, styled(strings.Repeat("─", w), styDim))
	}
	footer := 0
	if h >= 3 {
		footer = 1
	}
	body := max(h-len(out)-footer, 0)
	o.bodyTop, o.bodyRows = len(out), body

	maxOffset := max(len(v.rows)-body, 0)
	if v.sel >= 0 {
		if v.sel < o.offset {
			o.offset = v.sel
		}
		if v.sel >= o.offset+body {
			o.offset = v.sel - body + 1
		}
	} else {
		o.scroll = min(max(o.scroll, 0), maxOffset)
		o.offset = o.scroll
	}
	o.offset = min(max(o.offset, 0), maxOffset)

	for i := range body {
		idx := o.offset + i
		switch {
		case idx >= len(v.rows):
			out = append(out, "")
		case idx == v.sel:
			out = append(out, styled(padTo(sanitize(joinText(v.rows[idx])), w), styRev))
		default:
			out = append(out, renderSegs(v.rows[idx], w))
		}
	}
	if footer > 0 {
		hint := v.hint
		if o.notice != "" {
			hint = o.notice
		}
		out = append(out, renderSegs([]seg{{hint, styDim}}, w))
	}
	return out
}

// tabBar draws the tabs, in short form when the long one does not fit, and records where each tab is for the
// mouse.
func (o *overlay) tabBar(w int) string {
	o.tabSpans = [tabCount][2]int{}
	for _, names := range [][tabCount]string{tabNames, tabShorts} {
		total := 0
		for _, n := range names {
			total += textWidth(n) + 2
		}
		if total > w {
			continue
		}
		var segs []seg
		x := 0
		for t := tabID(0); t < tabCount; t++ {
			label := " " + names[t] + " "
			style := styDim
			if t == o.tab {
				style = styRev + ";" + styBold
			}
			segs = append(segs, seg{label, style})
			o.tabSpans[t] = [2]int{x, x + textWidth(label)}
			x += textWidth(label)
		}
		return renderSegs(segs, w)
	}
	label := "‹ " + tabShorts[o.tab] + " " + string(rune('1'+int(o.tab))) + "/4 ›"
	return renderSegs([]seg{{label, styBold}}, w)
}

// HandleInput applies one key.
func (o *overlay) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	k := parseKey(data)
	if k.kind == keyNone {
		return sdk.RemoteComponentResult{}, nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.notice = ""
	return o.key(k, o.d.model()), nil
}

// HandleMouse applies a wheel turn or a left click: tabs and list rows select, the wheel scrolls.
func (o *overlay) HandleMouse(ev sdk.MouseEvent) (sdk.RemoteComponentResult, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	m := o.d.model()
	switch {
	case ev.Type == sdk.MouseWheel:
		delta := ev.WheelDelta
		if delta == 0 {
			return sdk.RemoteComponentResult{}, nil
		}
		o.move(min(max(delta, -3), 3), m)
	case ev.Type == sdk.MouseClick && ev.Button == sdk.MouseButtonLeft:
		if ev.Y == 0 {
			for t := tabID(0); t < tabCount; t++ {
				if span := o.tabSpans[t]; span[1] > span[0] && ev.X >= span[0] && ev.X < span[1] {
					o.switchTab(t)
				}
			}
			break
		}
		idx := o.offset + ev.Y - o.bodyTop
		if o.mode == modeList && o.tab != tabAbout && ev.Y >= o.bodyTop && ev.Y < o.bodyTop+o.bodyRows && idx >= 0 && idx < o.listLen {
			o.sel[o.tab] = idx
			o.follow = o.tab == tabInvestigation && idx == o.listLen-1
			if ev.ClickCount >= 2 {
				return o.enter(m), nil
			}
		}
	}
	return sdk.RemoteComponentResult{}, nil
}

func closeOverlay() sdk.RemoteComponentResult { return sdk.RemoteComponentResult{Done: true} }

func (o *overlay) switchTab(t tabID) {
	if t == o.tab {
		return
	}
	o.tab, o.mode, o.scroll, o.offset, o.rawOpen = t, modeList, 0, 0, false
}

func (o *overlay) key(k key, m model) sdk.RemoteComponentResult {
	// A selection that follows the newest call is on it, whether or not a frame was drawn since it arrived.
	if o.follow && o.tab == tabInvestigation && len(m.Calls) > 0 {
		o.sel[tabInvestigation] = len(m.Calls) - 1
	}
	switch k.kind {
	case keyCtrlC:
		return closeOverlay()
	case keyEsc:
		if o.mode == modeList {
			return closeOverlay()
		}
		o.mode, o.scroll, o.rawOpen = modeList, 0, false
	case keyTab, keyRight:
		o.switchTab((o.tab + 1) % tabCount)
	case keyShiftTab, keyLeft:
		o.switchTab((o.tab + tabCount - 1) % tabCount)
	case keyUp:
		o.move(-1, m)
	case keyDown:
		o.move(1, m)
	case keyPageUp:
		o.move(-max(o.bodyRows-1, 1), m)
	case keyPageDown:
		o.move(max(o.bodyRows-1, 1), m)
	case keyHome:
		o.move(-1<<30, m)
	case keyEnd:
		o.move(1<<30, m)
	case keyEnter:
		return o.enter(m)
	case keyText:
		return o.text(unicode.ToLower(k.r), m)
	}
	return sdk.RemoteComponentResult{}
}

func (o *overlay) text(r rune, m model) sdk.RemoteComponentResult {
	switch r {
	case 'q':
		return closeOverlay()
	case '1', '2', '3', '4':
		o.switchTab(tabID(r - '1'))
	case 'h':
		o.switchTab((o.tab + tabCount - 1) % tabCount)
	case 'l':
		o.switchTab((o.tab + 1) % tabCount)
	case 'j':
		o.move(1, m)
	case 'k':
		o.move(-1, m)
	case 'i':
		return o.act(actInvestigate, m)
	case 'v':
		return o.act(actInspect, m)
	case 'd':
		return o.act(actDerive, m)
	case 'r':
		switch {
		case o.tab == tabProviders && o.mode != modeDetail:
			if o.d.startDoctor(true) {
				o.notice = "re-running rea doctor…"
			} else {
				o.notice = "doctor not available or already running"
			}
		case o.tab == tabTargets && o.mode == modeResult:
			return o.act(actInspect, m)
		}
	}
	return sdk.RemoteComponentResult{}
}

// listSize is the number of selectable rows of the current tab in list mode.
func (o *overlay) listSize(m model) int {
	switch o.tab {
	case tabInvestigation:
		return len(m.Calls)
	case tabTargets:
		return len(targets(m.Calls))
	case tabProviders:
		return len(providersFor(m))
	}
	return 0
}

// move moves the selection or scrolls a pane; negative is up.
func (o *overlay) move(delta int, m model) {
	switch {
	case o.mode == modeMenu:
		o.menuSel = min(max(o.menuSel+delta, 0), actCount-1)
	case o.mode == modeList && o.tab != tabAbout:
		n := o.listSize(m)
		if n == 0 {
			return
		}
		o.sel[o.tab] = min(max(o.sel[o.tab]+delta, 0), n-1)
		if o.tab == tabInvestigation {
			o.follow = o.sel[o.tab] == n-1
		}
	default:
		o.scroll = max(o.scroll+delta, 0)
		if delta == 1<<30 {
			o.scroll = 1 << 30 // clamped to the end when drawn
		}
	}
}

// selectedTarget is the target under the Targets selection.
func (o *overlay) selectedTarget(m model) (target, bool) {
	ts := targets(m.Calls)
	if len(ts) == 0 {
		return target{}, false
	}
	return ts[min(max(o.sel[tabTargets], 0), len(ts)-1)], true
}

func (o *overlay) enter(m model) sdk.RemoteComponentResult {
	switch {
	case o.tab == tabInvestigation && o.mode == modeList && len(m.Calls) > 0:
		o.mode, o.scroll = modeDetail, 0
	case o.tab == tabProviders && o.mode == modeList && o.listSize(m) > 0:
		o.mode, o.scroll = modeDetail, 0
	case o.tab == tabTargets && o.mode == modeList:
		if _, ok := o.selectedTarget(m); ok {
			o.mode, o.menuSel = modeMenu, 0
		}
	case o.tab == tabTargets && o.mode == modeMenu:
		return o.act(o.menuSel, m)
	case o.tab == tabTargets && o.mode == modeResult:
		o.rawOpen = !o.rawOpen
	}
	return sdk.RemoteComponentResult{}
}

// act runs a target action. Investigate and Derive close the overlay: /rea asks for their input and sends the
// message afterwards. Quick inspect runs in the background and opens its result.
func (o *overlay) act(action int, m model) sdk.RemoteComponentResult {
	if o.tab != tabTargets || o.mode == modeDetail {
		return sdk.RemoteComponentResult{}
	}
	t, ok := o.selectedTarget(m)
	if !ok {
		return sdk.RemoteComponentResult{}
	}
	switch action {
	case actInvestigate:
		return sdk.RemoteComponentResult{Done: true, Value: map[string]any{"action": closeInvestigate, "target": t.Value}}
	case actDerive:
		return sdk.RemoteComponentResult{Done: true, Value: map[string]any{"action": closeDerive, "target": t.Value}}
	case actInspect:
		if job, seen := m.Inspect[t.Value]; seen && !job.Running && o.mode != modeResult {
			o.mode, o.scroll, o.rawOpen = modeResult, 0, false
			return sdk.RemoteComponentResult{}
		}
		if msg := o.d.startInspect(t.Value); msg != "" {
			o.notice = msg
			return sdk.RemoteComponentResult{}
		}
		o.mode, o.scroll, o.rawOpen = modeResult, 0, false
	}
	return sdk.RemoteComponentResult{}
}

func joinText(segs []seg) string {
	var b strings.Builder
	for _, s := range segs {
		b.WriteString(s.text)
	}
	return b.String()
}
