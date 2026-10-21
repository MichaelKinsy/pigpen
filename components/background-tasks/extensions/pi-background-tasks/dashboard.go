package pi_background_tasks

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	tui "github.com/MichaelKinsy/pigpen/components/pitui"
	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

// The dashboard overlay: task list, details and output of the selected task. upstream: extensions/dashboard.ts.

// dashboardDeps is what the dashboard needs of the runtime. Tasks are snapshots, so a render never races the runtime.
type dashboardDeps struct {
	sortedTasks func() []*snapshot
	getTask     func(id string) *snapshot
	// getTaskOutput reads the task's log tail on its own goroutine and calls done with it.
	getTaskOutput      func(id string, done func(text string))
	requestStop        func(id string) (ok bool, message string)
	clearFinishedTasks func() int
}

type detailEntry struct {
	value string
	width int
	lines []string
}

type dashboard struct {
	mu   sync.Mutex
	deps dashboardDeps
	// theme, rows and wrap are the terminal: colors, the terminal height and the ANSI-aware wrap (a test counts its calls).
	theme uiTheme
	rows  func() int
	wrap  func(text string, width int) []string

	invalidateHost func()
	selectedID     string
	taskScroll     int
	rightScroll    int
	lastRightMax   int
	followOutput   bool
	commandExpand  bool
	activePane     string

	disposed    bool
	outputID    string
	hasOutput   bool
	outputText  string
	outputLines []string
	pending     map[string]bool
	detailCache map[string]detailEntry
	ticker      *time.Timer
}

func newDashboard(deps dashboardDeps, theme uiTheme, initialID string, rows func() int, wrap func(string, int) []string) *dashboard {
	d := &dashboard{deps: deps, theme: theme, rows: rows, wrap: wrap, activePane: "tasks", pending: map[string]bool{}, detailCache: map[string]detailEntry{}}
	d.selectedID = initialID
	if d.selectedID == "" {
		if sorted := deps.sortedTasks(); len(sorted) > 0 {
			d.selectedID = sorted[0].ID
		}
	}
	d.outputLines = splitOutputLines("")
	return d
}

// SetInvalidate installs the host's render request (sdk.RemoteComponentInvalidator).
func (d *dashboard) SetInvalidate(f func()) {
	d.mu.Lock()
	d.invalidateHost = f
	d.mu.Unlock()
}

func (d *dashboard) requestRender() {
	d.mu.Lock()
	f := d.invalidateHost
	d.mu.Unlock()
	if f != nil {
		f()
	}
}

// Dispose releases the component (sdk.RemoteComponentDisposer); a read still pending renders nothing.
func (d *dashboard) Dispose() {
	d.mu.Lock()
	d.disposed = true
	d.detailCache = map[string]detailEntry{}
	d.outputID, d.hasOutput, d.outputText, d.outputLines = "", false, "", nil
	if d.ticker != nil {
		d.ticker.Stop()
	}
	d.ticker = nil
	d.invalidateHost = nil
	d.mu.Unlock()
}

// resetTheme is the original's invalidate(): styled lines are rebuilt.
func (d *dashboard) resetTheme() {
	d.mu.Lock()
	d.detailCache = map[string]detailEntry{}
	d.outputLines = splitOutputLines(d.outputText)
	d.mu.Unlock()
}

func (d *dashboard) frameRows() int {
	return max(dashboardMinFrameRows, int(math.Floor(float64(max(1, d.rows()))*0.72)))
}
func (d *dashboard) innerRows() int { return max(4, d.frameRows()-dashboardFrameVerticalOverhead) }
func (d *dashboard) bodyRows() int  { return max(1, d.innerRows()-2) }
func (d *dashboard) taskRows() int  { return max(1, d.bodyRows()-1) }

// ensureTimer refreshes the dashboard each second while a task runs, so elapsed times move.
func (d *dashboard) ensureTimer(sorted []*snapshot) {
	hasRunning := false
	for _, t := range sorted {
		if t.Status == statusRunning {
			hasRunning = true
		}
	}
	if !hasRunning {
		if d.ticker != nil {
			d.ticker.Stop()
		}
		d.ticker = nil
		return
	}
	if d.ticker != nil || d.disposed {
		return
	}
	var tick func()
	tick = func() {
		d.mu.Lock()
		if d.disposed {
			d.mu.Unlock()
			return
		}
		d.ticker = time.AfterFunc(time.Second, tick)
		f := d.invalidateHost
		d.mu.Unlock()
		if f != nil {
			f()
		}
	}
	d.ticker = time.AfterFunc(time.Second, tick)
}

func (d *dashboard) selected(sorted []*snapshot) *snapshot {
	if len(sorted) == 0 {
		return nil
	}
	if d.selectedID != "" {
		if t := d.deps.getTask(d.selectedID); t != nil {
			return t
		}
	}
	d.selectedID = sorted[0].ID
	return d.deps.getTask(d.selectedID)
}

// outputFor is the output lines of a task, started reading on first sight; the render never waits for the log.
func (d *dashboard) outputFor(t *snapshot) []string {
	if t == nil {
		if d.outputID != "" || d.hasOutput {
			d.outputID, d.hasOutput, d.outputText, d.outputLines = "", false, "", splitOutputLines("")
		}
		return d.outputLines
	}
	if !d.hasOutput || d.outputID != t.ID {
		d.outputID, d.hasOutput, d.outputText, d.outputLines = t.ID, true, "", splitOutputLines("")
	}
	if !d.pending[t.ID] {
		d.pending[t.ID] = true
		id := t.ID
		d.deps.getTaskOutput(id, func(text string) {
			d.mu.Lock()
			delete(d.pending, id)
			if d.disposed || !d.hasOutput || d.outputID != id || d.outputText == text {
				d.mu.Unlock()
				return
			}
			d.outputText = text
			d.outputLines = splitOutputLines(text)
			f := d.invalidateHost
			d.mu.Unlock()
			if f != nil {
				f()
			}
		})
	}
	return d.outputLines
}

var (
	oscSeq     = regexp.MustCompile(`\x1b\][^\x07]*(?:\x07|\x1b\\)`)
	csiSeq     = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	controlSeq = regexp.MustCompile(`[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]`)
)

func sanitizeDashboardLine(line string) string {
	line = oscSeq.ReplaceAllString(line, "")
	line = csiSeq.ReplaceAllString(line, "")
	line = controlSeq.ReplaceAllString(line, "")
	return strings.ReplaceAll(line, "\t", "  ")
}

func (d *dashboard) wrapLine(line string, target int) []string {
	sanitized := strings.ReplaceAll(sanitizeDashboardLine(line), "\r", "")
	wrapped := d.wrap(sanitized, max(1, target))
	if len(wrapped) == 0 {
		return []string{""}
	}
	return wrapped
}

// pushDetail adds a labelled value, wrapped, to target and returns how many lines it left out. Each label keeps only its
// current value and width.
func (d *dashboard) pushDetail(target *[]string, label, value string, width, maxLines int) int {
	theme := d.theme
	prefix := theme.Fg("muted", label) + ": "
	indent := strings.Repeat(" ", len(label)+2)
	cached, ok := d.detailCache[label]
	if !ok || cached.value != value || cached.width != width {
		var lines []string
		for index, chunk := range splitLines(value) {
			lead := theme.Fg("muted", indent)
			avail := max(1, width-len(indent))
			if index == 0 {
				lead = prefix
				avail = max(1, width-vwidth(label+": "))
			}
			for partIndex, part := range d.wrapLine(chunk, avail) {
				if partIndex == 0 {
					lines = append(lines, lead+part)
				} else {
					lines = append(lines, theme.Fg("muted", indent)+part)
				}
			}
		}
		cached = detailEntry{value, width, lines}
		d.detailCache[label] = cached
	}
	lines := cached.lines
	if len(lines) > maxLines {
		*target = append(*target, lines[:max(1, maxLines-1)]...)
		*target = append(*target, theme.Fg("muted", indent)+fmt.Sprintf("… %d more line(s)", len(lines)-(maxLines-1)))
		return len(lines) - (maxLines - 1)
	}
	*target = append(*target, lines...)
	return 0
}

func (d *dashboard) syncTaskScroll(sorted []*snapshot) {
	index := 0
	for i, t := range sorted {
		if t.ID == d.selectedID {
			index = i
		}
	}
	rows := d.taskRows()
	maxScroll := max(0, len(sorted)-rows)
	if index < d.taskScroll {
		d.taskScroll = index
	}
	if index >= d.taskScroll+rows {
		d.taskScroll = index - rows + 1
	}
	d.taskScroll = clampIdx(d.taskScroll, 0, maxScroll)
}

func (d *dashboard) renderBody(width int) []string {
	theme := d.theme
	sorted := d.deps.sortedTasks()
	selected := d.selected(sorted)
	bodyRows := d.bodyRows()
	taskViewport := d.taskRows()
	d.syncTaskScroll(sorted)

	footerFor := func(commandHidden int) string {
		hint := ""
		switch {
		case d.commandExpand:
			hint = theme.Fg("dim", " · ") + ansiYellow("x") + " " + theme.Fg("dim", "collapse command")
		case commandHidden > 0:
			hint = theme.Fg("dim", " · ") + ansiYellow("x") + " " + theme.Fg("dim", "expand command")
		}
		return ansiYellow("←/→ tab") + " " + theme.Fg("dim", "pane · ") + ansiYellow("s") + " " + theme.Fg("dim", "stop · ") + ansiYellow("c") + " " + theme.Fg("dim", "clear · ") +
			ansiYellow("f") + " " + theme.Fg("dim", "follow · ") + ansiYellow("-/=") + " " + theme.Fg("dim", "page right") + hint
	}
	var lines []string
	if len(sorted) == 0 {
		lines = append(lines, theme.Fg("dim", "No background tasks yet. Use /bg run <command> or the bg_task tool."))
		for len(lines) < bodyRows {
			lines = append(lines, "")
		}
		lines = append(lines, "")
		lines = append(lines, widthx.WrapTextWithAnsi(footerFor(0), max(1, width))...)
		return truncateAll(lines, width)
	}
	taskPaneWidth := clampIdx(int(math.Floor(float64(width)*0.34)), taskPaneMinWidth, taskPaneMaxWidth)
	detailPaneWidth := max(24, width-taskPaneWidth-3)
	var left, right []string
	commandHidden := 0

	pill := func(active bool, label string) string {
		if active {
			return activePill(theme, label)
		}
		return inactivePill(theme, label)
	}
	left = append(left, pill(d.activePane == "tasks", " Tasks ")+" "+theme.Fg("dim", fmt.Sprintf("(%d)", len(sorted))), "")
	if d.taskScroll > 0 {
		left = append(left, theme.Fg("dim", fmt.Sprintf("↑ %d earlier task(s)", d.taskScroll)))
	}
	end := min(len(sorted), d.taskScroll+taskViewport)
	for _, t := range sorted[d.taskScroll:end] {
		isSelected := selected != nil && t.ID == selected.ID
		color := "dim"
		if isSelected {
			color = "text"
		}
		row := " " + bgStatusIcon(t.Status, theme) + " " + theme.Fg("accent", t.ID) + " " + theme.Fg(color,
			fmt.Sprintf("%s · %s", summarizeTaskStatus(t.Status, t.ExitCode, t.TerminationReason), compactText(taskDisplayName(t.Title, t.Command), max(12, taskPaneWidth-24))))
		if isSelected {
			row = theme.Bg("selectedBg", padAnsi(row, taskPaneWidth))
		}
		left = append(left, row)
	}
	if hiddenBelow := max(0, len(sorted)-(d.taskScroll+taskViewport)); hiddenBelow > 0 {
		left = append(left, theme.Fg("dim", fmt.Sprintf("↓ %d more task(s)", hiddenBelow)))
	}

	if selected == nil {
		right = append(right, theme.Fg("dim", "Select a task to inspect output."))
	} else {
		outputLines := d.outputFor(selected)
		sanitized := make([]string, len(outputLines))
		for i, l := range outputLines {
			sanitized[i] = sanitizeDashboardLine(l)
		}
		where := "top"
		switch {
		case d.followOutput:
			where = "follow"
		case d.rightScroll > 0:
			where = fmt.Sprintf("line %d", d.rightScroll+1)
		}
		right = append(right, pill(d.activePane == "output", " Watch "+selected.ID+" ")+" "+theme.Fg("dim", where), "")
		right = append(right, theme.Fg("muted", "Status")+": "+bgStatusText(selected, theme)+fmt.Sprintf(" · pid %d", selected.Pid))
		now := clockNow()
		right = append(right, theme.Fg("muted", "Started")+": "+formatRelativeTime(float64(selected.StartedAt), float64(now))+" · "+formatDuration(float64(taskElapsedMs(selected, now)))+" elapsed")
		if selected.ExpiresAt != nil {
			right = append(right, theme.Fg("muted", "Expiry")+": "+formatRelativeTime(*selected.ExpiresAt, float64(now)))
		}
		commandMax := 4
		if d.commandExpand {
			commandMax = math.MaxInt32
		}
		commandHidden = d.pushDetail(&right, "Command", selected.Command, detailPaneWidth, commandMax)
		d.pushDetail(&right, "Cwd", selected.Cwd, detailPaneWidth, 2)
		d.pushDetail(&right, "Log", selected.LogFile, detailPaneWidth, 2)
		d.pushDetail(&right, "Wakeups", wakeupsLine(selected), detailPaneWidth, 2)
		right = append(right, "", theme.Fg("muted", theme.Bold("Output")))
		right = append(right, sanitized...)
	}
	d.lastRightMax = max(0, len(right)-bodyRows)
	if d.followOutput {
		d.rightScroll = d.lastRightMax
	}
	d.rightScroll = clampIdx(d.rightScroll, 0, d.lastRightMax)
	visibleRight := right[min(d.rightScroll, len(right)):min(d.rightScroll+bodyRows, len(right))]

	rowCount := min(bodyRows, max(len(left), len(visibleRight)))
	for i := 0; i < rowCount; i++ {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(visibleRight) {
			r = visibleRight[i]
		}
		lines = append(lines, padAnsi(l, taskPaneWidth)+theme.Fg("dim", " │ ")+truncW(r, detailPaneWidth))
	}
	for len(lines) < bodyRows {
		lines = append(lines, "")
	}
	lines = append(lines, "")
	lines = append(lines, widthx.WrapTextWithAnsi(footerFor(commandHidden), max(1, width))...)
	return truncateAll(lines, width)
}

// Render draws the framed dashboard at width.
func (d *dashboard) Render(width int) []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	sorted := d.deps.sortedTasks()
	d.ensureTimer(sorted)
	running := 0
	for _, t := range sorted {
		if t.Status == statusRunning {
			running++
		}
	}
	return frameDashboard(d.renderBody(dashboardContentWidth(width)), width, d.theme, "Background Tasks", fmt.Sprintf("%d running · %d finished", running, len(sorted)-running), frameOptions{paddingTop: dashboardPaddingY})
}

func (d *dashboard) moveSelection(delta int) {
	sorted := d.deps.sortedTasks()
	if len(sorted) == 0 {
		d.selectedID = ""
		return
	}
	current := 0
	for i, t := range sorted {
		if t.ID == d.selectedID {
			current = i
		}
	}
	d.selectedID = sorted[clampIdx(current+delta, 0, len(sorted)-1)].ID
	d.commandExpand = false
	d.rightScroll = 0
	d.followOutput = false
	d.syncTaskScroll(sorted)
}

func (d *dashboard) moveRightPane(delta int) {
	d.followOutput = false
	d.rightScroll = clampIdx(d.rightScroll+delta, 0, d.lastRightMax)
}

// HandleInput is the dashboard's keyboard (sdk.RemoteComponent).
func (d *dashboard) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	d.mu.Lock()
	redraw := true
	switch {
	case tui.MatchesKey(data, "escape") || tui.MatchesKey(data, "ctrl+c"):
		d.mu.Unlock()
		return sdk.RemoteComponentResult{Done: true}, nil
	case tui.MatchesKey(data, "left"):
		d.activePane = "tasks"
	case tui.MatchesKey(data, "right"):
		d.activePane = "output"
	case tui.MatchesKey(data, "tab"):
		if d.activePane == "tasks" {
			d.activePane = "output"
		} else {
			d.activePane = "tasks"
		}
	case tui.MatchesKey(data, "up"):
		if d.activePane == "tasks" {
			d.moveSelection(-1)
		} else {
			d.moveRightPane(-1)
		}
	case tui.MatchesKey(data, "down"):
		if d.activePane == "tasks" {
			d.moveSelection(1)
		} else {
			d.moveRightPane(1)
		}
	case tui.MatchesKey(data, "home"):
		if d.activePane == "tasks" {
			d.moveSelection(-math.MaxInt32)
		} else {
			d.followOutput, d.rightScroll = false, 0
		}
	case tui.MatchesKey(data, "end"):
		if d.activePane == "tasks" {
			d.moveSelection(math.MaxInt32)
		} else {
			d.followOutput, d.rightScroll = true, d.lastRightMax
		}
	case tui.MatchesKey(data, "-") || tui.MatchesKey(data, "pageup") || tui.MatchesKey(data, "shift+up"):
		d.moveRightPane(-d.bodyRows())
	case tui.MatchesKey(data, "=") || tui.MatchesKey(data, "pagedown") || tui.MatchesKey(data, "shift+down"):
		d.moveRightPane(d.bodyRows())
	case data == "f":
		d.followOutput = !d.followOutput
		if d.followOutput {
			d.rightScroll = d.lastRightMax
		}
	case data == "x":
		if d.selected(d.deps.sortedTasks()) != nil {
			d.commandExpand = !d.commandExpand
		}
	case data == "s":
		if sel := d.selected(d.deps.sortedTasks()); sel != nil {
			id := sel.ID
			d.mu.Unlock()
			d.deps.requestStop(id)
			d.requestRender()
			return sdk.RemoteComponentResult{}, nil
		}
	case data == "c":
		d.mu.Unlock()
		d.deps.clearFinishedTasks()
		d.requestRender()
		return sdk.RemoteComponentResult{}, nil
	default:
		redraw = false
	}
	f := d.invalidateHost
	d.mu.Unlock()
	if redraw && f != nil {
		f()
	}
	return sdk.RemoteComponentResult{}, nil
}

// ── opening it from the runtime ────────────────────────────────────────────────────────────────────────────────────────

// dashboardDeps are the runtime's: every access takes the runtime lock, and a read of a log runs without it.
func (rt *bgRuntime) dashboardDeps() dashboardDeps {
	snapshots := func() []*snapshot {
		rt.mu.Lock()
		defer rt.mu.Unlock()
		var out []*snapshot
		for _, t := range rt.sortedTasks() {
			s := *taskSnapshot(t)
			out = append(out, &s)
		}
		return out
	}
	return dashboardDeps{
		sortedTasks: snapshots,
		getTask: func(id string) *snapshot {
			rt.mu.Lock()
			defer rt.mu.Unlock()
			t := rt.tasks.get(id)
			if t == nil {
				return nil
			}
			s := *taskSnapshot(t)
			return &s
		},
		// The dashboard asks for the log tail it shows (logTailMaxChars); the read starts under the lock, in call order.
		getTaskOutput: func(id string, done func(string)) {
			rt.mu.Lock()
			t := rt.tasks.get(id)
			if t == nil {
				rt.mu.Unlock()
				go done("")
				return
			}
			read := rt.reader.start(t, settingNumber("logTailMaxChars", defaultLogTailMaxChars, rt.cwd()), t.output)
			rt.mu.Unlock()
			go func() { done(read.wait().text) }()
		},
		requestStop: func(id string) (bool, string) {
			var ok bool
			var message string
			rt.withLock(func() { ok, message = rt.requestStop(rt.tasks.get(id), stopUser) })
			rt.settle()
			return ok, message
		},
		clearFinishedTasks: func() int {
			var n int
			rt.withLock(func() { n = rt.clearFinishedTasks() })
			rt.settle()
			return n
		},
	}
}

// openDashboard shows the overlay; without a UI it lists the tasks instead.
func (rt *bgRuntime) openDashboard(ctx sdk.Context, initialID string) error {
	if !ctx.HasUI() {
		rt.withLock(func() { rt.notify(rt.formatTaskListText(), "info") })
		return nil
	}
	d := newDashboard(rt.dashboardDeps(), ctx.UITheme(), initialID, func() int {
		if h := ctx.Height(); h > 0 {
			return h
		}
		return 32
	}, widthx.WrapTextWithAnsi)
	_, err := ctx.Custom(d, sdk.RemoteOverlayOptions{Overlay: true, OverlayOptions: &sdk.OverlayOptions{Anchor: "center", MaxHeight: sdk.OverlayPercent(75), Width: sdk.OverlayCells(dashboardWidth)}})
	return err
}
