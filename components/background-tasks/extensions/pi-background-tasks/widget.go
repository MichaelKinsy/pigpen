package pi_background_tasks

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

// The mini-dashboard widget: its visibility, the finished-task expiry timer and the stack of widgets that share the host
// widget of a placement. upstream: extensions/widget-visibility.ts, stacked-widget.ts, background-tasks.ts (syncWidget).

const (
	widgetModeCompact  = "compact"
	widgetModeExpanded = "expanded"
	widgetModeHidden   = "hidden"
	// maxTimerDelayMs is the longest wait a native timer takes; a longer one would fire at once.
	maxTimerDelayMs = 2_147_483_647
)

type widgetVisibility struct {
	mode, lastVisibleMode string
}

func visibleWidgetMode(mode string) string {
	if mode == widgetModeExpanded {
		return widgetModeExpanded
	}
	return widgetModeCompact
}

func newWidgetVisibility(mode string) widgetVisibility {
	return widgetVisibility{mode: mode, lastVisibleMode: visibleWidgetMode(mode)}
}

func (v *widgetVisibility) toggle() {
	if v.mode == widgetModeHidden {
		v.mode = v.lastVisibleMode
		return
	}
	v.lastVisibleMode = visibleWidgetMode(v.mode)
	v.mode = widgetModeHidden
}

func shouldRenderWidget(hasUI bool, trackedTaskCount, visibleTaskCount int, showWidget bool, mode string) bool {
	return hasUI && trackedTaskCount > 0 && visibleTaskCount > 0 && showWidget && mode != widgetModeHidden
}

// expiryTask is what the expiry timer needs of a task.
type expiryTask struct {
	status    string
	updatedAt int64
}

// nextWidgetExpiryDelay is the wait until the earliest finished task leaves the widget, in ms; ok is false when none will.
func nextWidgetExpiryDelay(tasks []expiryTask, retentionMs, now int64) (delay int64, ok bool) {
	for _, t := range tasks {
		if t.status == statusRunning {
			continue
		}
		remaining := t.updatedAt + retentionMs - now
		if remaining < 0 {
			continue
		}
		candidate := remaining + 1
		if !ok || candidate < delay {
			delay, ok = candidate, true
		}
	}
	return delay, ok
}

// widgetExpiry owns one one-shot timer, so relative-time labels need no per-second redraw.
type widgetExpiry struct {
	mu      sync.Mutex
	timers  timers
	refresh func()
	timer   timer
	gen     int
}

func newWidgetExpiry(refresh func(), tm timers) *widgetExpiry {
	return &widgetExpiry{refresh: refresh, timers: tm}
}

func (w *widgetExpiry) clear() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.clearLocked()
}

func (w *widgetExpiry) clearLocked() {
	if w.timer != nil {
		w.timer.Stop()
	}
	w.timer = nil
	w.gen++
}

func (w *widgetExpiry) schedule(tasks []expiryTask, retentionMs, now int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.clearLocked()
	delay, ok := nextWidgetExpiryDelay(tasks, retentionMs, now)
	if !ok {
		return
	}
	// A wait past the native limit is bounded, not the retention: the refresh rechecks the remaining lifetime and schedules again.
	delay = min(delay, maxTimerDelayMs)
	gen := w.gen
	w.timer = w.timers.After(time.Duration(delay)*time.Millisecond, func() {
		w.mu.Lock()
		if w.gen != gen || w.timer == nil {
			w.mu.Unlock()
			return
		}
		w.timer = nil
		w.mu.Unlock()
		w.refresh()
	})
}

// ── the stack of mini-dashboards sharing one host widget per placement ─────────────────────────────────────────────────

const (
	miniRankBackgroundTasks = 40
	placementAbove          = "aboveEditor"
	placementBelow          = "belowEditor"
)

var stackWidgetKey = map[string]string{placementAbove: "kendex-mini-dashboard-stack-above", placementBelow: "kendex-mini-dashboard-stack-below"}

// widgetHost is where the stack draws.
type widgetHost interface {
	hasUI() bool
	// setWidget shows lines under key at placement; nil lines remove the widget.
	setWidget(key string, lines []string, placement string)
	width() int
	rows() int
	theme() uiTheme
}

type miniEntry struct {
	key       string
	rank      int
	sequence  int
	placement string
	render    func(width, rows int, theme uiTheme) []string
}

// miniStack is the registry of widget entries ranked and laid out in one host widget per placement. The Go port keeps its own:
// a Go extension cannot see another extension's JavaScript globals (PORT.md).
type miniStack struct {
	mu       sync.Mutex
	entries  map[string]*miniEntry
	next     int
	active   map[string]bool
	lastHost widgetHost
}

func newMiniStack() *miniStack {
	return &miniStack{entries: map[string]*miniEntry{}, next: 1, active: map[string]bool{}}
}

func (s *miniStack) sorted(placement string) []*miniEntry {
	var out []*miniEntry
	for _, e := range s.entries {
		if e.placement == placement {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.rank != b.rank {
			return a.rank < b.rank
		}
		if a.sequence != b.sequence {
			return a.sequence < b.sequence
		}
		return strings.Compare(a.key, b.key) < 0
	})
	return out
}

// lines is the stack at placement, every line cut to width.
func (s *miniStack) lines(placement string, width, rows int, theme uiTheme) []string {
	s.mu.Lock()
	entries := s.sorted(placement)
	s.mu.Unlock()
	var out []string
	for _, e := range entries {
		out = append(out, e.render(width, rows, theme)...)
	}
	return truncateAll(out, max(1, width))
}

// set adds, replaces or (with a nil render) removes the entry of key and brings the host widgets in line.
func (s *miniStack) set(host widgetHost, key string, rank int, render func(width, rows int, theme uiTheme) []string, placement string) {
	s.mu.Lock()
	previous := s.entries[key]
	previousPlacement := ""
	if previous != nil {
		previousPlacement = previous.placement
	}
	if !host.hasUI() || render == nil {
		delete(s.entries, key)
		s.mu.Unlock()
		if previousPlacement != "" {
			s.sync(host, previousPlacement)
		}
		return
	}
	seq := s.next
	if previous != nil {
		seq = previous.sequence
	} else {
		s.next++
	}
	s.entries[key] = &miniEntry{key: key, rank: rank, sequence: seq, placement: placement, render: render}
	s.mu.Unlock()
	if previousPlacement != "" && previousPlacement != placement {
		s.sync(host, previousPlacement)
	}
	s.sync(host, placement)
}

// sync pushes the placement's lines to the host, or removes the host widget once the stack is empty.
func (s *miniStack) sync(host widgetHost, placement string) {
	s.mu.Lock()
	empty := len(s.sorted(placement)) == 0
	wasActive := s.active[placement]
	s.active[placement] = !empty
	s.lastHost = host
	s.mu.Unlock()
	key := stackWidgetKey[placement]
	if empty {
		if wasActive {
			host.setWidget(key, nil, placement)
		}
		return
	}
	host.setWidget(key, s.lines(placement, host.width(), host.rows(), host.theme()), placement)
}

// repush draws every active placement again, at the host's current width.
func (s *miniStack) repush(host widgetHost) {
	for _, placement := range []string{placementAbove, placementBelow} {
		s.mu.Lock()
		active := s.active[placement]
		s.mu.Unlock()
		if active {
			s.sync(host, placement)
		}
	}
}

// ── the runtime's widget ───────────────────────────────────────────────────────────────────────────────────────────────

// ctxHost is the widgetHost of a handler context. Host calls go through post, the runtime's ordered outbox, so a handler that
// holds the runtime lock never waits for the host. The widget is the kit form of the component factory the original registers
// (rows laid out at the terminal's width, drawn every frame, not cut at ten rows). Pi's RPC mode does not render a component
// widget, so the original's stack never marks itself active there and sends nothing, not even its removal; neither does the port.
type ctxHost struct {
	ctx  sdk.Context
	post func(sdk.Context, func(sdk.Context))
}

func (h ctxHost) hasUI() bool { return h.ctx.HasUI() }
func (h ctxHost) setWidget(key string, lines []string, placement string) {
	if h.ctx.Mode() == "rpc" {
		return
	}
	h.post(h.ctx, func(ctx sdk.Context) {
		if lines == nil {
			_ = ctx.SetWidget(key, nil, sdk.WidgetOptions{"placement": placement})
			return
		}
		_ = ctx.SetWidget(key, kit.View{Root: kit.NewLines(lines)}, sdk.WidgetOptions{"placement": placement})
	})
}
func (h ctxHost) width() int {
	if w := h.ctx.Width(); w > 0 {
		return w
	}
	return 80
}
func (h ctxHost) rows() int {
	if r := h.ctx.Height(); r > 0 {
		return r
	}
	return 24
}
func (h ctxHost) theme() uiTheme { return h.ctx.UITheme() }

// clampAboveEditorWidget keeps an aboveEditor widget from pushing the chat above the viewport top: at least 4 lines, the rest
// replaced by a muted hint.
func clampAboveEditorWidget(lines []string, terminalRows int, theme uiTheme) []string {
	const reserveForOtherUI = 10
	maxLines := max(4, terminalRows-reserveForOtherUI)
	if len(lines) <= maxLines {
		return lines
	}
	hidden := len(lines) - (maxLines - 1)
	return append(append([]string{}, lines[:maxLines-1]...), theme.Fg("muted", fmt.Sprintf("… %d more (open dashboard for full view)", hidden)))
}

// widgetFinishedRetentionMs is Math.max(0, Math.floor(seconds * 1000)). JavaScript keeps an enormous retention as a number (Infinity
// for 1e308 seconds); an int64 cannot, so it stops at a retention no clock reaches (2^62 ms), which no comparison tells apart.
func (rt *bgRuntime) widgetFinishedRetentionMs(cwd string) int64 {
	ms := math.Max(0, math.Floor(settingNumber("widgetFinishedRetentionSeconds", float64(defaultWidgetFinishedRetain)/1000, cwd)*1000))
	if ms >= 1<<62 {
		return 1 << 62
	}
	return int64(ms)
}

// widgetTasks are the running tasks and the finished ones still inside their retention.
func (rt *bgRuntime) widgetTasks(now int64) []*task {
	retention := rt.widgetFinishedRetentionMs(rt.cwd())
	var out []*task
	for _, t := range rt.sortedTasks() {
		if t.Status == statusRunning || now-t.UpdatedAt <= retention {
			out = append(out, t)
		}
	}
	return out
}

func (rt *bgRuntime) renderWidgetLines(theme uiTheme, now int64) []string {
	sorted := rt.widgetTasks(now)
	var running, display []*task
	for _, t := range sorted {
		if t.Status == statusRunning {
			running = append(running, t)
		}
	}
	display = append(display, running...)
	for _, t := range sorted {
		if t.Status != statusRunning {
			display = append(display, t)
		}
	}
	finished := len(sorted) - len(running)
	toggleHint, dashboardHint := "", ""
	if rt.widgetToggleShortcut != "none" {
		toggleHint = " · " + formatShortcutHint(rt.widgetToggleShortcut) + " toggle"
	}
	if rt.dashboardShortcut != "none" {
		dashboardHint = " · " + formatShortcutHint(rt.dashboardShortcut) + " dashboard"
	}
	summary := theme.Fg("customMessageLabel", theme.Bold("Background tasks")) + " " + theme.Fg("muted", fmt.Sprintf("%d running · %d finished%s%s", len(running), finished, toggleHint, dashboardHint))
	if len(display) == 0 {
		return []string{summary}
	}
	shownCount := widgetCompactTasks
	if rt.visibility.mode == widgetModeExpanded {
		shownCount = len(display)
	}
	shownCount = min(shownCount, len(display))
	cwd := rt.cwd()
	lines := []string{summary}
	for i, t := range display[:shownCount] {
		branch := "├"
		if i == shownCount-1 && shownCount == len(display) {
			branch = "└"
		}
		activityAt := t.UpdatedAt
		if t.LastOutputAt != nil {
			activityAt = *t.LastOutputAt
		}
		lines = append(lines, bgTree(theme, branch, cwd)+bgStatusIcon(t.Status, theme)+" "+theme.Fg("accent", t.ID)+" "+theme.Fg("dim",
			fmt.Sprintf("%s · %s · %s", summarizeTaskStatus(t.Status, t.ExitCode, t.TerminationReason), compactText(taskDisplayName(t.Title, t.Command), 72), formatRelativeTime(float64(activityAt), float64(now)))))
	}
	if hidden := len(display) - shownCount; hidden > 0 {
		lines = append(lines, bgTree(theme, "└", cwd)+theme.Fg("muted", fmt.Sprintf("… %d more", hidden)))
	}
	return lines
}

// syncWidget draws, replaces or removes the mini-dashboard for the current tasks. It runs with the runtime lock held.
func (rt *bgRuntime) syncWidget(host widgetHost) {
	rt.expiry.clear()
	now := clockNow()
	visible := rt.widgetTasks(now)
	cwd := rt.cwd()
	if !shouldRenderWidget(host.hasUI(), rt.tasks.size(), len(visible), settingBoolean("showWidget", true, cwd), rt.visibility.mode) {
		rt.clearWidget(host)
		return
	}
	placement := placementAbove
	if settingString("widgetPlacement", placementAbove, cwd) == placementBelow {
		placement = placementBelow
	}
	// The stack calls this with the runtime lock held (syncWidget, repushWidgets).
	rt.stack.set(host, bgWidgetKey, miniRankBackgroundTasks, func(width, rows int, theme uiTheme) []string {
		lines := frameWidget(rt.renderWidgetLines(theme, clockNow()), width, theme)
		if placement == placementAbove {
			return clampAboveEditorWidget(lines, rows, theme)
		}
		return lines
	}, placement)
	tasks := make([]expiryTask, len(visible))
	for i, t := range visible {
		tasks[i] = expiryTask{t.Status, t.UpdatedAt}
	}
	rt.expiry.schedule(tasks, rt.widgetFinishedRetentionMs(cwd), now)
}

func (rt *bgRuntime) clearWidget(host widgetHost) {
	rt.expiry.clear()
	if host != nil {
		rt.stack.set(host, bgWidgetKey, miniRankBackgroundTasks, nil, placementAbove)
	}
}

// currentHost is the widget host of the active context, nil without one.
func (rt *bgRuntime) currentHost() widgetHost {
	if !rt.active {
		return nil
	}
	return ctxHost{ctx: rt.ctx, post: rt.post}
}

// repushWidgets draws the stack again at the terminal's current width (the SDK sends widget rows laid out for one width).
func (rt *bgRuntime) repushWidgets() {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if host := rt.currentHost(); host != nil {
		rt.stack.repush(host)
	}
}

// toggleWidget is the shortcut of widgetToggleShortcut.
func (rt *bgRuntime) toggleWidget() {
	rt.visibility.toggle()
	if host := rt.currentHost(); host != nil {
		rt.syncWidget(host)
	}
}

// adoptAndSync is `activeCtx = ctx; syncWidget(ctx)` of the handlers that follow a session's context (session_tree,
// session_compact).
func (rt *bgRuntime) adoptAndSync(ctx sdk.Context) {
	rt.withLock(func() {
		rt.setContext(ctx)
		if host := rt.currentHost(); host != nil {
			rt.syncWidget(host)
		}
	})
}

// toggleWidgetShortcut is the handler of widgetToggleShortcut: `activeCtx = ctx; toggleWidget()`.
func (rt *bgRuntime) toggleWidgetShortcut(ctx sdk.Context) {
	rt.withLock(func() {
		rt.setContext(ctx)
		rt.toggleWidget()
	})
}

// watchWidth makes the widget lay its rows out again when the terminal's width changes: Pi draws a component widget at the
// current width every frame, the port sends rows laid out at one width.
func (rt *bgRuntime) watchWidth(ctx sdk.Context) {
	if rt.unwatchWidth != nil || !ctx.HasUI() || ctx.Mode() == "rpc" {
		return
	}
	if off, err := ctx.OnWidthChange(func(c sdk.Context, _ int) {
		defer rt.enter(c)()
		rt.repushWidgets()
		rt.settle()
	}); err == nil {
		rt.unwatchWidth = off
	}
}
