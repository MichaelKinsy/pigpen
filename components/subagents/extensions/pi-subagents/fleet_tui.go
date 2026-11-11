package pi_subagents

// The fleet inspector's TUI. The original opens `/subagents-fleet` as `ctx.ui.custom(...)` with
// `overlayOptions: { anchor: "center", width: "95%", minWidth: 60, maxHeight: "85%", margin: 1 }` and renders
// SubagentFleetComponent (port/oracle/src/tui/fleet.ts). This is that component as a sdk.RemoteComponent: the same
// frame, rows, footer and keys over Context.Custom. The detail pane shows what the port keeps for a child (its status
// and result transcript tail) instead of the original's live transcript events; actions the port does not run
// (Prompt Audit, Inspect, steer, stop, tool details) show the port's refusal text where the original shows an action
// notice. Without a UI the command falls back to the original's textual fleet view.

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The original's layout and key constants (fleet.ts).
const (
	fleetMinWidth     = 36
	fleetBodyRatio    = 85 // bodyHeight = max(2, rows*85/100 - 6)
	fleetRosterMin    = 22
	fleetRosterMax    = 46
	fleetRosterRatio  = 38 // rosterWidth = (innerWidth-1)*38/100
	fleetRefreshMs    = 750
	fleetNarrowNotice = "Subagent fleet needs at least 36 columns. Esc closes."
)

// fleetTheme is the part of the host's sdk.UITheme the inspector draws with (the original's theme.fg/bold).
type fleetTheme interface {
	Fg(token, text string) string
	Bold(text string) string
}

// fleetThinkingToner is sdk.UITheme's thinking-border colour (runningTone falls back to accent).
type fleetThinkingToner interface {
	GetThinkingBorderColor(level string) func(string) string
}

// fleetItem is one roster row: a child the registry knows (the original's FleetItem).
type fleetItem struct {
	runID     string
	step      int // 0-based, for the transcript view
	agent     string
	state     runState
	task      string
	thinking  string
	startedAt time.Time
	endedAt   time.Time
	activity  time.Time
	output    string
	err       string
	session   string
}

// fleetItems is the registry as roster items: one per child, active first.
func fleetItems(runs []*runRecord) []fleetItem {
	var items []fleetItem
	for _, r := range runs {
		for i, s := range r.steps {
			items = append(items, fleetItem{
				runID: r.id, step: i, agent: s.agent, state: s.state, task: s.task,
				thinking: s.thinking, startedAt: s.startedAt, endedAt: s.endedAt,
				activity: s.lastActivity, output: s.output, err: s.err, session: s.session,
			})
		}
	}
	return items
}

// fleetInspector is the original's SubagentFleetComponent.
type fleetInspector struct {
	theme   func() fleetTheme
	tone    func(level string) func(string) string // runningTone; nil falls back to accent
	rows    func() int
	refresh time.Duration

	mu               sync.Mutex
	items            []fleetItem
	selected         int
	selectedKey      string
	detailScroll     int
	detailAutoFollow bool
	notice           string
	steerDraft       *string // the steer compose line while it is open

	invalidate     func()
	invalidateMu   sync.Mutex
	stopRefresh    chan struct{}
	stopRefreshOne sync.Once
}

var fleetInspectorOpen atomic.Bool

// newFleetInspector builds the component over the live registry and host.
func newFleetInspector(ctx sdk.Context) *fleetInspector {
	c := &fleetInspector{
		theme:   func() fleetTheme { return ctx.UITheme() },
		rows:    func() int { return ctx.Height() },
		refresh: fleetRefreshMs * time.Millisecond,
		detailAutoFollow: true,
		tone: func(level string) func(string) string {
			return ctx.UITheme().GetThinkingBorderColor(level)
		},
	}
	c.items = fleetItems(fleet.snapshot())
	return c
}

// SetInvalidate is sdk.RemoteComponentInvalidator: the host's render request (tui.requestRender).
func (c *fleetInspector) SetInvalidate(f func()) {
	c.invalidateMu.Lock()
	c.invalidate = f
	c.invalidateMu.Unlock()
	if f != nil {
		c.startRefresh()
	}
}

// Dispose is sdk.RemoteComponentDisposer: stop the refresh loop.
func (c *fleetInspector) Dispose() {
	c.stopRefreshOne.Do(func() {
		if c.stopRefresh != nil {
			close(c.stopRefresh)
		}
	})
}

func (c *fleetInspector) startRefresh() {
	c.mu.Lock()
	if c.stopRefresh != nil {
		c.mu.Unlock()
		return
	}
	c.stopRefresh = make(chan struct{})
	stop := c.stopRefresh
	c.mu.Unlock()
	go func() {
		t := time.NewTicker(maxDuration(fleetRefreshMs*time.Millisecond, c.refresh))
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				c.refreshSnapshot()
				c.invalidateMu.Lock()
				f := c.invalidate
				c.invalidateMu.Unlock()
				if f != nil {
					f()
				}
			}
		}
	}()
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

// refreshSnapshot keeps the selection on its child across registry updates (the original's refresh()).
func (c *fleetInspector) refreshSnapshot() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refreshSnapshotLocked()
}

func fleetItemKey(it fleetItem) string { return fmt.Sprintf("%s:%d", it.runID, it.step) }

func fleetItemKeyOf(items []fleetItem, i int) string {
	if i < 0 || i >= len(items) {
		return ""
	}
	return fleetItemKey(items[i])
}

// HandleInput is the component's key router (DEFAULT_FLEET_KEYBINDINGS).
func (c *fleetInspector) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	if isFleetKeyRelease(data) {
		return sdk.RemoteComponentResult{}, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refreshSnapshotLocked()
	if c.steerDraft != nil {
		c.handleSteerDraft(data)
		c.requestRender()
		return sdk.RemoteComponentResult{}, nil
	}
	switch {
	case fleetMatches(data, "escape"), fleetMatches(data, "ctrl+c"), fleetMatches(data, "q"):
		return sdk.RemoteComponentResult{Done: true}, nil
	case fleetMatches(data, "up"), fleetMatches(data, "k"):
		c.moveSelection(-1)
	case fleetMatches(data, "down"), fleetMatches(data, "j"):
		c.moveSelection(1)
	case fleetMatches(data, "home"):
		c.selectAt(0)
	case fleetMatches(data, "end"):
		c.selectAt(len(c.items) - 1)
	case fleetMatches(data, "pageUp"):
		c.moveSelection(-10)
	case fleetMatches(data, "pageDown"):
		c.moveSelection(10)
	case fleetMatches(data, "K"):
		c.detailScroll = max(0, c.detailScroll-1)
		c.detailAutoFollow = false
	case fleetMatches(data, "J"):
		c.detailScroll++
		c.detailAutoFollow = false
	case fleetMatches(data, "r"), fleetMatches(data, "R"):
		c.notice = ""
	case fleetMatches(data, "p"):
		c.notice = refusal("the Prompt Audit view (`p`)")
	case fleetMatches(data, "return"), fleetMatches(data, "H"):
		c.notice = refusal("the Inspect plugin (`Enter`/`H`)")
	case fleetMatches(data, "s"):
		if len(c.items) > 0 {
			draft := ""
			c.steerDraft = &draft
			c.notice = ""
		}
	case fleetMatches(data, "D"):
		c.notice = c.controlNotice("stop")
	case fleetMatches(data, "x"), fleetMatches(data, "X"), fleetMatches(data, "ctrl+o"):
		c.notice = refusal("tool details (`x`)")
	default:
		return sdk.RemoteComponentResult{}, nil
	}
	c.requestRender()
	return sdk.RemoteComponentResult{}, nil
}

// requestRender asks the host for a repaint (sdk.RemoteComponentInvalidator's callback).
func (c *fleetInspector) requestRender() {
	c.invalidateMu.Lock()
	f := c.invalidate
	c.invalidateMu.Unlock()
	if f != nil {
		f()
	}
}

// controlNotice runs a control action on the selected run and returns its message (the port's refusal text included).
func (c *fleetInspector) controlNotice(action string) string {
	if len(c.items) == 0 {
		return ""
	}
	id := c.items[c.selected].runID
	out, err := actions[action].run(sdk.Context{}, map[string]any{"id": id})
	if err != nil {
		return err.Error()
	}
	if ok, isOK := out.(explicitOK); isOK {
		return firstLine(ok.Content)
	}
	return ""
}

// handleSteerDraft is the steer compose line: type the message, Enter steers, Esc cancels (the original's steerDraft;
// its Tab-cycled delivery modes are not ported).
func (c *fleetInspector) handleSteerDraft(data string) {
	switch {
	case data == "\x1b":
		c.steerDraft = nil
	case data == "\r" || data == "\n":
		message := jsTrim(*c.steerDraft)
		c.steerDraft = nil
		if message != "" {
			id := c.items[c.selected].runID
			out, err := actions["steer"].run(sdk.Context{}, map[string]any{"id": id, "message": message})
			if err != nil {
				c.notice = err.Error()
			} else if ok, isOK := out.(explicitOK); isOK {
				c.notice = firstLine(ok.Content)
			}
		}
	case data == "\x7f" || data == "\b":
		runes := []rune(*c.steerDraft)
		if len(runes) > 0 {
			*c.steerDraft = string(runes[:len(runes)-1])
		}
	default:
		if data != "" && !strings.Contains(data, "\x1b") {
			*c.steerDraft += data
		}
	}
}

func (c *fleetInspector) refreshSnapshotLocked() {
	previous := c.selectedKey
	c.items = fleetItems(fleet.snapshot())
	kept := -1
	for i, it := range c.items {
		if previous != "" && fleetItemKey(it) == previous {
			kept = i
			break
		}
	}
	if kept < 0 {
		kept = min(c.selected, max(0, len(c.items)-1))
	}
	c.selected = kept
	c.selectedKey = fleetItemKeyOf(c.items, kept)
}

func (c *fleetInspector) moveSelection(delta int) {
	if len(c.items) == 0 {
		return
	}
	c.selected = max(0, min(len(c.items)-1, c.selected+delta))
	c.selectedKey = fleetItemKeyOf(c.items, c.selected)
	c.detailAutoFollow = true
	c.notice = ""
}

func (c *fleetInspector) selectAt(i int) {
	if len(c.items) == 0 {
		return
	}
	c.selected = max(0, min(len(c.items)-1, i))
	c.selectedKey = fleetItemKeyOf(c.items, c.selected)
	c.detailAutoFollow = true
	c.notice = ""
}

// Render draws the inspector (fleet.ts: render, rosterLines, the footer).
func (c *fleetInspector) Render(width int) []string {
	if width < fleetMinWidth {
		return []string{truncVisible(fleetNarrowNotice, width)}
	}
	theme := c.theme()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refreshSnapshotLocked()
	innerWidth := width - 2
	rows := 32
	if c.rows != nil {
		if r := c.rows(); r > 0 {
			rows = r
		}
	}
	bodyHeight := max(2, rows*fleetBodyRatio/100-6)
	rosterWidth := max(fleetRosterMin, min(fleetRosterMax, (innerWidth-1)*fleetRosterRatio/100))
	detailWidth := max(1, innerWidth-rosterWidth-1)
	roster := c.rosterLines(rosterWidth, bodyHeight)
	detail := c.detailLines(detailWidth)
	maxDetailScroll := max(0, len(detail)-bodyHeight)
	if c.detailAutoFollow {
		c.detailScroll = maxDetailScroll
	} else if c.detailScroll > maxDetailScroll {
		c.detailScroll = maxDetailScroll
	}
	visible := detail[min(c.detailScroll, len(detail)) : min(c.detailScroll+bodyHeight, len(detail))]

	lines := []string{theme.Fg("border", "╭"+strings.Repeat("─", innerWidth)+"╮")}
	var title, status string
	if len(c.items) == 0 {
		title = " " + theme.Bold("Subagent fleet inspector") + " " + theme.Fg("dim", "· live controls")
		status = theme.Fg("dim", "no children ")
	} else {
		selected := c.items[c.selected]
		title = " " + theme.Bold("Subagent fleet inspector") + " " + theme.Fg("dim", "· live controls")
		status = c.statusGlyph(selected) + " " + selected.agent + " · " + string(selected.state) + " "
	}
	lines = append(lines, theme.Fg("border", "│")+rightAligned(title, status, innerWidth)+theme.Fg("border", "│"))
	lines = append(lines, theme.Fg("border", "├"+strings.Repeat("─", rosterWidth)+"┬"+strings.Repeat("─", detailWidth)+"┤"))
	for i := 0; i < bodyHeight; i++ {
		left, right := "", ""
		if i < len(roster) {
			left = roster[i]
		}
		if i < len(visible) {
			right = visible[i]
		}
		lines = append(lines, theme.Fg("border", "│")+fit(left, rosterWidth)+theme.Fg("border", "│")+fit(right, detailWidth)+theme.Fg("border", "│"))
	}
	lines = append(lines, theme.Fg("border", "├"+strings.Repeat("─", rosterWidth)+"┴"+strings.Repeat("─", detailWidth)+"┤"))
	position := "0/0"
	if len(c.items) > 0 {
		position = fmt.Sprintf("%d/%d", c.selected+1, len(c.items))
	}
	footer := " " + bindingLabel("selectUp") + "/" + bindingLabel("selectDown") + " agent · p Prompt Audit · " +
		bindingLabel("inspect") + " Inspect · " + bindingLabel("steer") + " steer · " + bindingLabel("stop") + " stop · " +
		bindingLabel("toggleTools") + " tools · " + bindingLabel("refresh") + " refresh · " + bindingLabel("close") + " close · " + position
	lines = append(lines, theme.Fg("border", "│")+fit(theme.Fg("dim", footer), innerWidth)+theme.Fg("border", "│"))
	lines = append(lines, theme.Fg("border", "╰"+strings.Repeat("─", innerWidth)+"╯"))
	return lines
}

// rosterLines is the left pane: one row per visible child, the selected one marked (fleet.ts: rosterLines).
func (c *fleetInspector) rosterLines(width, bodyHeight int) []string {
	if len(c.items) == 0 {
		return []string{c.theme().Fg("dim", "No tracked children")}
	}
	theme := c.theme()
	start := max(0, min(c.selected-bodyHeight+1, max(0, len(c.items)-bodyHeight)))
	rows := make([]string, 0, bodyHeight)
	for offset := 0; offset < bodyHeight && start+offset < len(c.items); offset++ {
		index := start + offset
		item := c.items[index]
		marker := " "
		if index == c.selected {
			marker = theme.Fg("accent", "›")
		}
		agent := item.agent
		if index == c.selected {
			agent = theme.Bold(item.agent)
		}
		identity := item.runID
		if len(identity) > 8 {
			identity = identity[:8]
		}
		left := marker + " " + c.statusGlyph(item) + " " + agent + " " + theme.Fg("dim", "· "+identity)
		rows = append(rows, rightAligned(left, theme.Fg("dim", string(item.state)), width))
	}
	return rows
}

// detailLines is the right pane: the selected child's status and result transcript tail, wrapped (the original shows
// its live transcript here).
func (c *fleetInspector) detailLines(width int) []string {
	if len(c.items) == 0 {
		return nil
	}
	theme := c.theme()
	item := c.items[c.selected]
	raw := []string{
		c.statusGlyph(item) + " " + item.agent + " · " + string(item.state),
		fmt.Sprintf("Run: %s · Step %d", item.runID, item.step+1),
	}
	if item.task != "" {
		raw = append(raw, "Task: "+item.task)
	}
	if !item.startedAt.IsZero() {
		raw = append(raw, "Started: "+item.startedAt.UTC().Format(time.RFC3339))
	}
	if a := formatActivityLabel(item.activity, fleetNow()); a != "" {
		raw = append(raw, "Activity: "+a)
	}
	if item.session != "" {
		raw = append(raw, "Session: "+shortenPath(item.session))
	}
	if item.err != "" {
		raw = append(raw, "Error: "+item.err)
	}
	raw = append(raw, "", "Result transcript tail:")
	tail := outputTail(item.output, 20)
	if len(tail) == 0 {
		raw = append(raw, "  (no recovered final output available yet)")
	}
	for _, l := range tail {
		raw = append(raw, "  "+l)
	}
	var lines []string
	for _, l := range raw {
		for _, w := range wrapPlain(l, width) {
			lines = append(lines, theme.Fg("dim", w))
		}
	}
	if c.notice != "" {
		for _, w := range wrapPlain(c.notice, width) {
			lines = append(lines, theme.Fg("warning", w))
		}
	}
	if c.steerDraft != nil {
		draft := "steer › " + *c.steerDraft + "▌  (Enter sends · Esc cancels)"
		for _, w := range wrapPlain(draft, width) {
			lines = append(lines, theme.Fg("accent", w))
		}
	}
	return lines
}

// statusGlyph is the original's fleet.ts statusGlyph (runningTone falls back to accent).
func (c *fleetInspector) statusGlyph(item fleetItem) string {
	theme := c.theme()
	switch item.state {
	case stateRunning:
		if c.tone != nil && item.thinking != "" {
			if tone := c.tone(item.thinking); tone != nil {
				return tone("●")
			}
		}
		return theme.Fg("accent", "●")
	case stateQueued:
		return theme.Fg("muted", "◦")
	case stateComplete:
		return theme.Fg("success", "✓")
	case stateStopped, statePaused:
		return theme.Fg("warning", "■")
	}
	return theme.Fg("error", "✗")
}

// bindingLabel is the original's: the keys of one action, "↑/↓"-style (fleet.ts: DEFAULT_FLEET_KEYBINDINGS).
func bindingLabel(action string) string {
	labels := map[string]string{
		"close":       "Esc/ctrl+c/q",
		"scrollUp":    "K",
		"scrollDown":  "J",
		"selectUp":    "↑/k",
		"selectDown":  "↓/j",
		"selectFirst": "home",
		"selectLast":  "end",
		"pageUp":      "pageUp",
		"pageDown":    "pageDown",
		"refresh":     "r/R",
		"steer":       "s",
		"inspect":     "Enter/H",
		"stop":        "D",
		"toggleTools": "x/X/ctrl+o",
	}
	return labels[action]
}

// fleetMatches is matchesFleetAction for one key (the defaults above; a capital letter is shift+lowercase).
func fleetMatches(data, key string) bool {
	want := key
	if len(want) == 1 && want[0] >= 'A' && want[0] <= 'Z' {
		want = "shift+" + strings.ToLower(want)
	}
	switch want {
	case "escape":
		return data == "\x1b"
	case "ctrl+c":
		return data == "\x03"
	case "ctrl+o":
		return data == "\x0f"
	case "return":
		return data == "\r" || data == "\n"
	case "up":
		return data == "\x1b[A"
	case "down":
		return data == "\x1b[B"
	case "home":
		return data == "\x1b[H" || data == "\x1b[1~" || data == "\x1b[7~"
	case "end":
		return data == "\x1b[F" || data == "\x1b[4~" || data == "\x1b[8~"
	case "pageUp":
		return data == "\x1b[5~"
	case "pageDown":
		return data == "\x1b[6~"
	case "shift+k":
		return data == "K"
	case "shift+j":
		return data == "J"
	case "shift+r":
		return data == "R"
	case "shift+x":
		return data == "X"
	case "shift+h":
		return data == "H"
	case "shift+d":
		return data == "D"
	}
	return data == want
}

// isFleetKeyRelease drops a Kitty key release, as pi-tui does for a component that did not ask for releases.
func isFleetKeyRelease(data string) bool {
	return strings.HasSuffix(data, ":1u") || strings.HasSuffix(data, ":3u")
}

// ---- ANSI-aware layout helpers (pi-tui's visibleWidth/truncateToWidth; fleet.ts fit/rightAligned) ----

// sgrSequence matches an ANSI escape sequence: zero display width.
var sgrSequence = regexp.MustCompile("\x1b" + `\[[0-9;:?]*[ -/]*[@-~]`)

// visibleWidth is a string's display width: escape sequences are zero (pi-tui counts double-width runes as two; rune
// count is the port's approximation).
func visibleWidth(s string) int {
	return utf8.RuneCountInString(sgrSequence.ReplaceAllString(s, ""))
}

// truncVisible cuts a string at display width, keeping its escape sequences so styling still closes.
func truncVisible(s string, width int) string {
	if visibleWidth(s) <= width {
		return s
	}
	var b strings.Builder
	n := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			loc := sgrSequence.FindStringIndex(s[i:])
			if loc == nil {
				i++
				continue
			}
			b.WriteString(s[i : i+loc[1]])
			i += loc[1]
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if n < width {
			b.WriteRune(r)
			n++
		}
		i += size
	}
	return b.String()
}

// fit is fleet.ts's fit: cut to width, then pad.
func fit(s string, width int) string {
	clipped := truncVisible(s, max(0, width))
	return clipped + strings.Repeat(" ", max(0, width-visibleWidth(clipped)))
}

// rightAligned is fleet.ts's: left, then the gap, then right at the row's end.
func rightAligned(left, right string, width int) string {
	rightWidth := visibleWidth(right)
	leftWidth := max(0, width-rightWidth-1)
	return fit(left, leftWidth) + strings.Repeat(" ", max(1, width-leftWidth-rightWidth)) + fit(right, rightWidth)
}

// wrapPlain word-wraps one plain line at display width.
func wrapPlain(s string, width int) []string {
	if width < 1 {
		width = 1
	}
	if visibleWidth(s) <= width {
		return []string{s}
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{""}
	}
	var lines []string
	line := ""
	for _, w := range words {
		switch {
		case line == "":
			line = w
		case visibleWidth(line)+1+visibleWidth(w) <= width:
			line += " " + w
		default:
			lines = append(lines, line)
			line = w
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}
